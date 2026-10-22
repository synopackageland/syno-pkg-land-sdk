#!/usr/bin/env python3
"""Keep inherited automation off without editing upstream files (stdlib only)."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

OWN_WORKFLOW = '.github/workflows/synopkgland-synology-fast-ci.yml'
MANAGED_DEPENDABOT = 'dynamic/dependabot/dependabot-updates'
BUSY = ('queued', 'in_progress', 'waiting', 'pending', 'requested')


class PolicyError(Exception):
    pass


def read_token(path):
    with open(path) as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
            raise PolicyError('Token file must be a regular file with mode 0600 or 0400')
        value = stream.read().strip()
    # Accept either a dedicated token file or the operator's existing .env file.
    for line in value.splitlines():
        key, sep, candidate = line.removeprefix('export ').partition('=')
        if sep and key.strip() == 'GITHUB_TOKEN':
            value = candidate.strip().strip('\"\'')
            break
    if not value or any(c.isspace() for c in value):
        raise PolicyError('Invalid token file')
    return value


class GitHub:
    def __init__(self, token):
        self.token = token

    def __call__(self, path, method='GET', data=None):
        request = urllib.request.Request(
            'https://api.github.com/' + path, method=method,
            data=None if data is None else json.dumps(data).encode(),
            headers={'Authorization': 'Bearer ' + self.token,
                     'Accept': 'application/vnd.github+json',
                     'X-GitHub-Api-Version': '2022-11-28',
                     'User-Agent': 'synopkgland-workflow-policy'})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                raw = response.read()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as error:
            # Never include request headers, token or raw response in errors.
            raise PolicyError(f'{method} {path}: HTTP {error.code}') from None
        except (OSError, ValueError):
            raise PolicyError(f'{method} {path}: request failed') from None


class Policy:
    def __init__(self, api, repo, workflow=OWN_WORKFLOW):
        if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repo):
            raise PolicyError('Expected owner/repository')
        if not re.fullmatch(r'\.github/workflows/[A-Za-z0-9_.-]+\.ya?ml', workflow):
            raise PolicyError('Expected an exact .github/workflows YAML path')
        self.api = api
        self.prefix = 'repos/' + repo
        self.workflow = workflow

    def call(self, suffix='', method='GET', data=None):
        return self.api(self.prefix + suffix, method, data)

    def workflows(self):
        result = []
        page = 1
        while True:
            body = self.call(f'/actions/workflows?per_page=100&page={page}')
            batch = body['workflows']
            result.extend(batch)
            if len(batch) < 100:
                return result
            page += 1

    def idle(self):
        for state in BUSY:
            for run in self.runs(state):
                if run.get('path') != MANAGED_DEPENDABOT:
                    raise PolicyError(f'Unfinished runs ({state}); drain them before proceeding')

    def runs(self, state):
        page = 1
        while True:
            body = self.call(f'/actions/runs?status={state}&per_page=100&page={page}')
            batch = body['workflow_runs']
            if body['total_count'] and not batch and page == 1:
                raise PolicyError('Unfinished run inventory is incomplete')
            yield from batch
            if len(batch) < 100:
                return
            page += 1

    def off(self):
        self.call('/actions/permissions', 'PUT', {'enabled': False})
        if self.call('/actions/permissions')['enabled']:
            raise PolicyError('Repository Actions did not turn off')

    def approval(self, private):
        if private:
            return None
        suffix = '/actions/permissions/fork-pr-contributor-approval'
        expected = {'approval_policy': 'all_external_contributors'}
        if self.call(suffix) != expected:
            self.call(suffix, 'PUT', expected)
        for attempt in range(6):
            if self.call(suffix) == expected:
                break
            if attempt == 5:
                raise PolicyError('Fork PR approval readback failed')
            time.sleep(2)
        return expected['approval_policy']

    def apply(self, mode):
        if mode == 'pause':
            self.off()
            self.idle()
            return {'enabled': False, 'paused': True}
        if self.call('/actions/permissions')['enabled']:
            raise PolicyError('Pause repository Actions first; no live enable/disable window is allowed')
        # All mutations below happen with repository Actions disabled.
        try:
            self.idle()
            meta = self.call()
            approval = self.approval(meta['private'])
            workflows = self.workflows()
            blocked = []
            for w in workflows:
                if w['path'] not in (self.workflow, MANAGED_DEPENDABOT) and w['state'] not in ('disabled_manually', 'deleted'):
                    try:
                        self.call(f"/actions/workflows/{w['id']}/disable", 'PUT')
                    except PolicyError as error:
                        # Continue disabling the rest, but never activate on partial success.
                        blocked.append(str(error))
            if blocked:
                raise PolicyError('; '.join(blocked))
            workflows = self.workflows()
            self.verify(workflows, require_own=False)
            if mode == 'activate':
                # Require the reviewed workflow to exist on the default branch.
                ref = urllib.parse.quote(meta['default_branch'], safe='')
                files = self.call('/contents/.github/workflows?ref=' + ref)
                paths = {f['path'] for f in files if f['name'].endswith(('.yml', '.yaml'))}
                known = {w['path'] for w in workflows if w['state'] != 'deleted'}
                if self.workflow not in paths or paths - known:
                    raise PolicyError('Default-branch workflow missing or not registered; leave Actions off and retry later')
                own = [w for w in workflows if w['path'] == self.workflow and w['state'] != 'deleted']
                if len(own) != 1:
                    raise PolicyError('Expected exactly one registered owned workflow')
                if own[0]['state'] != 'active':
                    self.call(f"/actions/workflows/{own[0]['id']}/enable", 'PUT')
                self.verify(self.workflows(), require_own=True)
                self.idle()
                self.call('/actions/permissions', 'PUT', {'enabled': True})
                if not self.call('/actions/permissions')['enabled']:
                    raise PolicyError('Repository Actions enable readback failed')
                workflows = self.workflows()
                self.verify(workflows, require_own=True)
                # Detect inherited runs if state changed concurrently with activation.
                self.no_foreign_runs()
            return {'enabled': mode == 'activate', 'approval_policy': approval,
                    'allowed_exceptions': [{k: w[k] for k in ('id', 'path', 'state')}
                                           for w in workflows if w['path'] == MANAGED_DEPENDABOT],
                    'workflows': [{k: w[k] for k in ('id', 'path', 'state')} for w in workflows]}
        except Exception:
            # Roll back even if the enable request succeeded but its response was lost.
            self.off()
            raise

    def verify(self, workflows, require_own):
        foreign = [w['path'] for w in workflows if w['path'] not in (self.workflow, MANAGED_DEPENDABOT)
                   and w['state'] not in ('disabled_manually', 'deleted')]
        if foreign:
            raise PolicyError('Non-owned workflows are not disabled: ' + ', '.join(foreign))
        if require_own and [w['path'] for w in workflows
                            if w['state'] == 'active' and w['path'] != MANAGED_DEPENDABOT] != [self.workflow]:
            raise PolicyError('Active workflow readback failed')

    def no_foreign_runs(self):
        for state in BUSY:
            for run in self.runs(state):
                if run.get('path') not in (self.workflow, MANAGED_DEPENDABOT):
                    raise PolicyError('Non-owned unfinished run found after activation')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('repo', help='explicit owner/repository; no discovery or account-wide changes')
    parser.add_argument('--token-file', type=Path, required=True, help='0600 token or .env file; never pass a PAT value')
    parser.add_argument('--workflow', default=OWN_WORKFLOW, help='exact allowed workflow path')
    parser.add_argument('--mode', choices=['pause', 'prepare', 'activate'], default='prepare')
    args = parser.parse_args()
    try:
        result = Policy(GitHub(read_token(args.token_file)), args.repo, args.workflow).apply(args.mode)
        print(json.dumps(result, indent=2))
    except (PolicyError, OSError) as error:
        print('workflow-policy: ' + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
