"""Repo-scoped, single-use GitHub runners. PAT never enters a job container."""
import http.client
import io
import json
import logging
import os
import pathlib
import re
import signal
import socket
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

OWNER = 'synopackageland'
REPOS = (
    'syno-package-land', 'syno-pkg-land-sdk', 'syno-pkg-land-cli',
    'syno-pkg-land-templates', 'syno-pkg-land-ui', 'syno-pkg-land-freshrss',
    'syno-pkg-land-openlist', 'syno-pkg-land-uptime-kuma', 'syno-pkg-connect',
    'syno-pkg-gopeed', 'syno-reclaim', 'synopkgland-community-apps',
)
MANAGED = {'ci.managed': 'private-ci-v1', 'ci.role': 'runner'}
MAX_AGE = 3600
IDLE_AGE = 300
CACHE_VOLUMES = {'ci-cache-go': '/cache/go', 'ci-cache-cargo': '/cache/cargo',
                 'ci-cache-npm': '/cache/npm'}
THREAD_GOBUILD = 'synopkgland-cache-gobuild'
LOG = logging.getLogger('ci')


class APIError(Exception):
    def __init__(self, status):
        self.status = status
        super().__init__(f'API HTTP {status}')


class GitHub:
    def __init__(self, token):
        self.token = token

    def request(self, path, data=None, method='GET'):
        req = urllib.request.Request(
            'https://api.github.com/' + path, method=method,
            data=None if data is None else json.dumps(data).encode(),
            headers={'Authorization': 'Bearer ' + self.token,
                     'Accept': 'application/vnd.github+json',
                     'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'private-ci'})
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                body = response.read()
                return json.loads(body) if body else None
        except urllib.error.HTTPError as error:
            # Response bodies and request objects can contain credentials.
            raise APIError(error.code) from None

    def pages(self, path, key):
        for page in range(1, 101):
            sep = '&' if '?' in path else '?'
            result = self.request(f'{path}{sep}per_page=100&page={page}')[key]
            yield from result
            if len(result) < 100:
                return
        raise RuntimeError('pagination bound exceeded')

    def bundle(self, repo):
        if repo not in {'syno-pkg-land-sdk', 'syno-pkg-land-templates'}:
            raise ValueError('invalid dependency repository')
        # Only Git's credential-helper pipe sees the PAT; neither argv nor env
        # contains it. A bundle contains objects/refs, never local Git config.
        with tempfile.TemporaryDirectory(prefix='ci-source-') as directory:
            mirror = pathlib.Path(directory) / 'source.git'
            bundle = pathlib.Path(directory) / 'source.bundle'
            env = os.environ | {'GIT_TERMINAL_PROMPT': '0', 'GIT_TRACE': '0',
                                'GIT_TRACE_CURL': '0', 'GIT_CURL_VERBOSE': '0'}
            commands = [
                ['git', '-c', 'credential.helper=', '-c', 'credential.helper=/app/git-credential-ci',
                 'clone', '--quiet', '--mirror', f'https://github.com/{OWNER}/{repo}.git', str(mirror)],
                ['git', '-C', str(mirror), 'bundle', 'create', str(bundle), '--all'],
            ]
            for command in commands:
                result = subprocess.run(command, env=env, stdout=subprocess.PIPE,
                                        stderr=subprocess.PIPE, timeout=120)
                if result.returncode:
                    raise RuntimeError('dependency Git operation failed')
            if bundle.stat().st_size > 64 * 1024**2:
                raise ValueError('dependency bundle exceeds bound')
            data = bundle.read_bytes()
            if self.token.encode() in data:
                raise ValueError('credential found in dependency bundle')
            return data


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self):
        super().__init__('localhost', timeout=30)

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect('/var/run/docker.sock')


class Docker:
    def request(self, method, path, data=None, binary=False):
        conn = UnixHTTP()
        body = data if binary else None if data is None else json.dumps(data).encode()
        try:
            conn.request(method, '/v1.45' + path, body,
                         {'Content-Type': 'application/x-tar' if binary else 'application/json'})
            response = conn.getresponse()
            raw = response.read()
            if response.status >= 400:
                raise APIError(response.status)
            return json.loads(raw) if raw else None
        finally:
            conn.close()

    def containers(self):
        filters = urllib.parse.quote(json.dumps({'label': ['ci.managed=private-ci-v1', 'ci.role=runner']}))
        return self.request('GET', '/containers/json?all=1&filters=' + filters)

    def remove(self, cid):
        self.request('DELETE', '/containers/' + cid + '?force=1&v=1')

    @staticmethod
    def cache_mounts():
        return [{'Type': 'volume', 'Source': name, 'Target': target}
                for name, target in CACHE_VOLUMES.items()]

    def ensure_caches(self):
        for name in CACHE_VOLUMES:
            volume = self.request('POST', '/volumes/create', {
                'Name': name, 'Labels': {'ci.managed': 'private-ci-v1', 'ci.role': 'cache'}})
            if volume.get('Labels') != {'ci.managed': 'private-ci-v1', 'ci.role': 'cache'}:
                raise ValueError('cache volume ownership mismatch')

    def trim_caches(self, image, thread=False):
        name = 'ci-trim-gobuild' if thread else 'ci-trim-cache'
        mounts = self.cache_mounts()
        if thread:
            # Inspect, never create or discover another thread/project volume.
            self.request('GET', '/volumes/' + THREAD_GOBUILD)
            mounts = [{'Type': 'volume', 'Source': THREAD_GOBUILD, 'Target': '/thread-gobuild'}]
        # Recover a helper left by controller restart; do not touch runners.
        try:
            old = self.request('GET', '/containers/' + name + '/json')
            if old['Config'].get('Labels') != {'ci.managed': 'private-ci-v1', 'ci.role': 'trim'}:
                raise ValueError('trim helper ownership mismatch')
            self.remove(old['Id'])
        except APIError as error:
            if error.status != 404:
                raise
        spec = {
            'Image': image, 'Hostname': name, 'User': '0:0' if thread else '1001:1001',
            'Labels': {'ci.managed': 'private-ci-v1', 'ci.role': 'trim'},
            'Tty': True,  # Helper prints one JSON record; no multiplexed stream.
            'Entrypoint': ['python3', '-B', '/app/cache_trim.py'],
            'Cmd': ['--thread-gobuild'] if thread else [],
            'HostConfig': {'NetworkMode': 'none', 'ReadonlyRootfs': True,
                           'Mounts': mounts, 'CapDrop': ['ALL'],
                           'SecurityOpt': ['no-new-privileges:true'],
                           'NanoCpus': 500_000_000, 'Memory': 256 * 1024**2,
                           'MemorySwap': 256 * 1024**2, 'PidsLimit': 32,
                           'LogConfig': {'Type': 'local', 'Config': {'max-size': '1m', 'max-file': '2'}}},
        }
        cid = self.request('POST', '/containers/create?name=' + name, spec)['Id']
        try:
            self.request('POST', '/containers/' + cid + '/start')
            # Poll rather than block Docker's connection for the full trim.
            deadline = time.monotonic() + 600
            while True:
                state = self.request('GET', '/containers/' + cid + '/json')['State']
                if not state['Running']:
                    if state['ExitCode'] != 0:
                        raise RuntimeError('cache trim failed')
                    metrics = self.request('GET', '/containers/' + cid + '/logs?stdout=1')
                    LOG.info('cache trim %s', json.dumps(metrics, sort_keys=True))
                    break
                if time.monotonic() > deadline:
                    raise RuntimeError('cache trim timeout')
                time.sleep(1)
        finally:
            self.remove(cid)

    def launch(self, name, image, repo, runner_id, config, now):
        if repo not in REPOS:
            raise ValueError('cache access outside private allowlist')
        labels = MANAGED | {'ci.repo': repo, 'ci.runner-id': str(runner_id), 'ci.created': str(now)}
        spec = {
            'Image': image, 'Hostname': name, 'User': '1001:1001', 'Labels': labels,
            'Env': ['HOME=/home/runner', 'RUNNER_TOOL_CACHE=/opt/hostedtoolcache',
                    'AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache', 'CI=true',
                    'GOMODCACHE=/cache/go/mod', 'GOCACHE=/cache/go/build',
                    'CARGO_HOME=/home/runner/.cargo', 'CARGO_INCREMENTAL=0',
                    'RUSTC_WRAPPER=/usr/local/bin/sccache',
                    'SCCACHE_DIR=/cache/cargo/sccache', 'SCCACHE_CACHE_SIZE=6G',
                    'NPM_CONFIG_CACHE=/cache/npm'],
            'HostConfig': {'NetworkMode': 'ci-jobs', 'Memory': 4 * 1024**3,
                           'Mounts': self.cache_mounts(),
                           'MemorySwap': 4 * 1024**3, 'NanoCpus': 2_000_000_000,
                           'PidsLimit': 512, 'CapDrop': ['ALL'],
                           'SecurityOpt': ['no-new-privileges:true'],
                           'LogConfig': {'Type': 'local', 'Config': {'max-size': '10m', 'max-file': '2'}},
                           'Init': True, 'RestartPolicy': {'Name': 'no'}},
        }
        if repo != 'syno-pkg-land-sdk':
            spec['Env'] += ['SYNOPKGLAND_SDK_DIR=/home/runner/deps/sdk',
                            'GOPRIVATE=github.com/synopackageland/syno-pkg-land-sdk',
                            'CARGO_NET_GIT_FETCH_WITH_CLI=true']
        if repo in {'syno-pkg-land-cli', 'syno-pkg-land-templates'}:
            spec['Env'] += ['SYNOPKGLAND_TEMPLATES_DIR=/home/runner/deps/templates']
        created = self.request('POST', '/containers/create?name=' + name, spec)
        cid = created['Id']
        try:
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode='w') as tar:
                value = config.encode()
                entry = tarfile.TarInfo('.jitconfig')
                entry.size, entry.mode, entry.uid, entry.gid = len(value), 0o600, 1001, 1001
                tar.addfile(entry, io.BytesIO(value))
            self.request('PUT', f'/containers/{cid}/archive?path=/home/runner', archive.getvalue(), True)
            for dep, source in getattr(self, 'dependency_bundles', {}).items():
                if dep not in {'sdk', 'templates'} or not source.startswith((b'# v2 git bundle\n', b'# v3 git bundle\n')):
                    raise ValueError('invalid dependency bundle')
                output = io.BytesIO()
                with tarfile.open(fileobj=output, mode='w') as dst:
                    entry = tarfile.TarInfo('.bundles')
                    entry.type, entry.mode, entry.uid, entry.gid = tarfile.DIRTYPE, 0o700, 1001, 1001
                    dst.addfile(entry)
                    entry = tarfile.TarInfo('.bundles/' + dep + '.bundle')
                    entry.size, entry.mode, entry.uid, entry.gid = len(source), 0o600, 1001, 1001
                    dst.addfile(entry, io.BytesIO(source))
                self.request('PUT', f'/containers/{cid}/archive?path=/home/runner', output.getvalue(), True)
            self.request('POST', f'/containers/{cid}/start')
            return cid
        except Exception:
            self.remove(cid)
            raise


def eligible(job):
    labels = {str(v).lower() for v in job.get('labels', [])}
    return (job.get('status') == 'queued' and {'self-hosted', 'private-ci'} <= labels
            and labels <= {'self-hosted', 'private-ci', 'linux', 'x64'})


class Controller:
    def __init__(self, github, docker, image):
        self.gh, self.docker, self.image = github, docker, image
        self.cursor = 0
        self.last_orphan_scan = 0
        self.cache_ready = False
        self.last_daily_trim = 0

    @staticmethod
    def prefix(repo):
        if repo not in REPOS:
            raise ValueError('repo outside private allowlist')
        return f'repos/{OWNER}/{repo}'

    def private(self, repo):
        meta = self.gh.request(self.prefix(repo))
        return (meta.get('private') is True and not meta.get('archived')
                and meta.get('full_name', '').lower() == f'{OWNER}/{repo}')

    def retire(self, container):
        labels = container['Labels']
        repo = labels.get('ci.repo')
        runner_id = labels.get('ci.runner-id', '')
        self.docker.remove(container['Id'])
        if repo in REPOS and runner_id.isdigit():
            try:
                self.gh.request(self.prefix(repo) + '/actions/runners/' + runner_id, method='DELETE')
            except APIError as error:
                if error.status != 404:
                    raise
        LOG.info('runner retired')

    def reconcile(self):
        active = []
        for c in self.docker.containers():
            labels = c.get('Labels', {})
            if any(labels.get(key) != value for key, value in MANAGED.items()):
                continue
            repo = labels.get('ci.repo')
            age = time.time() - float(labels.get('ci.created', '0'))
            retire = c['State'] != 'running' or age > MAX_AGE or repo not in REPOS
            if not retire:
                # A private-to-public transition removes existing runners too.
                retire = not self.private(repo)
            if not retire and age > IDLE_AGE:
                try:
                    runner = self.gh.request(self.prefix(repo) + '/actions/runners/' + labels['ci.runner-id'])
                    retire = not runner.get('busy', False)
                except APIError as error:
                    if error.status == 404:
                        retire = True
                    else:
                        raise
            if retire:
                self.retire(c)
            else:
                active.append(c)
        # Recover API-side registrations if creation failed or the controller
        # was killed between JIT registration and container creation.
        ids = {(c['Labels']['ci.repo'], int(c['Labels']['ci.runner-id'])) for c in active}
        if time.time() - self.last_orphan_scan >= 600:
            for repo in REPOS:
                for runner in self.gh.pages(self.prefix(repo) + '/actions/runners', 'runners'):
                    if (re.fullmatch(r'ci-linux-01', runner['name'])
                            and 'private-ci' in {v['name'].lower() for v in runner['labels']}
                            and (repo, runner['id']) not in ids):
                        self.gh.request(self.prefix(repo) + '/actions/runners/' + str(runner['id']), method='DELETE')
            self.last_orphan_scan = time.time()
        return active

    def tick(self):
        if self.reconcile():
            return  # Global capacity one, including jobs surviving restart.
        if not self.cache_ready:
            self.docker.ensure_caches()
            self.cache_ready = True
        # Runs after retirement and before each new job, including idle polls.
        # No CI job is reading/writing these volumes while they are pruned.
        self.docker.trim_caches(self.image)
        if time.time() - self.last_daily_trim >= 86400:
            self.docker.trim_caches(self.image, thread=True)
            self.last_daily_trim = time.time()
        for offset in range(len(REPOS)):
            index = (self.cursor + offset) % len(REPOS)
            repo = REPOS[index]
            prefix = self.prefix(repo)
            runs = self.gh.pages(prefix + '/actions/runs?status=queued', 'workflow_runs')
            found = any(eligible(job) for run in runs for job in
                        self.gh.pages(prefix + f'/actions/runs/{run["id"]}/jobs?filter=latest', 'jobs'))
            # Matrix/dependent jobs may queue while the run is in_progress.
            if not found:
                runs = self.gh.pages(prefix + '/actions/runs?status=in_progress', 'workflow_runs')
                found = any(eligible(job) for run in runs for job in
                            self.gh.pages(prefix + f'/actions/runs/{run["id"]}/jobs?filter=latest', 'jobs'))
            if not found:
                continue
            # Recheck immediately before registration.
            if not self.private(repo):
                continue
            self.docker.dependency_bundles = {}
            if repo != 'syno-pkg-land-sdk':
                dependencies = ['sdk', 'templates'] if repo in {'syno-pkg-land-cli', 'syno-pkg-land-templates'} else ['sdk']
                for dep in dependencies:
                    self.docker.dependency_bundles[dep] = self.gh.bundle('syno-pkg-land-' + dep)
            # Source transfer can take time. Visibility must still be private
            # after fetching dependencies, immediately before JIT registration.
            if not self.private(repo):
                continue
            jit = self.gh.request(prefix + '/actions/runners/generate-jitconfig', {
                'name': 'ci-linux-01', 'runner_group_id': 1,
                'labels': ['self-hosted', 'linux', 'x64', 'private-ci'], 'work_folder': '_work'}, 'POST')
            runner_id = jit['runner']['id']
            try:
                self.docker.launch('ci-linux-01', self.image, repo, runner_id,
                                   jit['encoded_jit_config'], time.time())
            except Exception:
                self.gh.request(prefix + '/actions/runners/' + str(runner_id), method='DELETE')
                raise
            self.cursor = (index + 1) % len(REPOS)
            LOG.info('single-use runner started for %s', repo)
            return


def main():
    logging.basicConfig(level=logging.INFO, format='%(asctime)s %(message)s')
    with open('/run/secrets/github_token') as secret:
        token = secret.read().strip()
    if not token:
        raise RuntimeError('empty token file')
    controller = Controller(GitHub(token), Docker(), os.environ.get('CI_RUNNER_IMAGE', 'ci-runner:1'))
    stopping = False

    def stop(*_):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    LOG.info('controller ready; capacity=1; interval=30s')
    while not stopping:
        try:
            controller.tick()
        except Exception as error:
            # Do not dump traceback, URLs, request data or response bodies.
            LOG.error('reconcile failed: %s%s', type(error).__name__,
                      f' HTTP {error.status}' if isinstance(error, APIError) else '')
        for _ in range(30):
            if stopping:
                break
            time.sleep(1)


if __name__ == '__main__':
    main()
