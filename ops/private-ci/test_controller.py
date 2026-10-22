import io
import pathlib
import tarfile
import unittest
from unittest.mock import Mock, patch
from controller import Controller, Docker, GitHub, MANAGED, REPOS, CACHE_VOLUMES, THREAD_GOBUILD, eligible


class ControllerTests(unittest.TestCase):
    def test_foreign_and_hosted_labels_never_qualify(self):
        for labels in [[], ['ubuntu-latest'], ['self-hosted'], ['private-ci'],
                       ['self-hosted', 'private-ci', 'special']]:
            self.assertFalse(eligible({'status': 'queued', 'labels': labels}))
        self.assertTrue(eligible({'status': 'queued', 'labels': ['self-hosted', 'private-ci']}))
        self.assertFalse(eligible({'status': 'completed', 'labels': ['self-hosted', 'private-ci']}))

    def test_public_and_unknown_repos_fail_closed(self):
        gh = Mock()
        controller = Controller(gh, Mock(), 'ci-runner:1')
        gh.request.return_value = {'private': False, 'full_name': 'synopackageland/' + REPOS[0]}
        self.assertFalse(controller.private(REPOS[0]))
        with self.assertRaises(ValueError):
            controller.private('catalog')

    def test_restart_keeps_running_job_without_double_launch(self):
        controller = Controller(Mock(), Mock(), 'ci-runner:1')
        with patch.object(controller, 'reconcile', return_value=[{'Id': 'already-running'}]):
            controller.tick()
        controller.docker.launch.assert_not_called()
        controller.gh.request.assert_not_called()

    def test_private_to_public_transition_retires_container(self):
        docker, gh = Mock(), Mock()
        gh.pages.return_value = []
        docker.containers.return_value = [{
            'Id': 'managed', 'State': 'running', 'Labels': MANAGED | {
                'ci.repo': REPOS[0], 'ci.runner-id': '42', 'ci.created': '100'}}]
        controller = Controller(gh, docker, 'ci-runner:1')
        with patch('controller.time.time', return_value=110), patch.object(controller, 'private', return_value=False):
            self.assertEqual(controller.reconcile(), [])
        docker.remove.assert_called_once_with('managed')
        gh.request.assert_called_once_with(controller.prefix(REPOS[0]) + '/actions/runners/42', method='DELETE')

    def test_unrelated_container_is_never_removed(self):
        gh, docker = Mock(), Mock()
        gh.pages.return_value = []
        docker.containers.return_value = [
            {'Id': 'other', 'Labels': {}},
            {'Id': 'controller', 'Labels': {'ci.managed': 'private-ci-v1', 'ci.role': 'controller'}},
        ]
        controller = Controller(gh, docker, 'ci-runner:1')
        controller.reconcile()
        docker.remove.assert_not_called()

    def test_job_has_no_host_mounts_socket_privilege_or_pat(self):
        docker = Docker()
        calls = []

        def request(method, path, data=None, binary=False):
            calls.append((method, path, data, binary))
            return {'Id': 'new'} if path.startswith('/containers/create') else None

        with patch.object(docker, 'request', side_effect=request):
            docker.launch('ci-linux-01', 'ci-runner:1', REPOS[0], 42, 'one-time-config', 100)
        spec = calls[0][2]
        self.assertEqual(spec['HostConfig']['Mounts'], Docker.cache_mounts())
        self.assertTrue(all(m['Type'] == 'volume' and m['Source'].startswith('ci-cache-')
                            for m in spec['HostConfig']['Mounts']))
        self.assertFalse(any('github_token' in v for v in spec['Env']))
        self.assertNotIn('Binds', spec['HostConfig'])
        self.assertNotIn('Privileged', spec['HostConfig'])
        self.assertEqual(spec['HostConfig']['CapDrop'], ['ALL'])
        self.assertEqual(spec['User'], '1001:1001')
        self.assertEqual(spec['Hostname'], 'ci-linux-01')
        self.assertNotIn('one-time-config', str(spec))
        self.assertLessEqual(spec['HostConfig']['Memory'], 4 * 1024**3)

    def test_invalid_dependency_payload_never_starts_job(self):
        for name, data in [('sdk', b'not a bundle'), ('../../outside', b'# v2 git bundle\n')]:
            with self.subTest(name=name):
                docker = Docker()
                docker.dependency_bundles = {name: data}
                calls = []

                def request(method, url, data=None, binary=False):
                    calls.append((method, url))
                    return {'Id': 'job'} if url.startswith('/containers/create') else None

                with patch.object(docker, 'request', side_effect=request), self.assertRaises(ValueError):
                    docker.launch('ci-linux-01', 'ci-runner:1', REPOS[0], 42, 'single-use', 100)
                self.assertNotIn(('POST', '/containers/job/start'), calls)
                self.assertIn(('DELETE', '/containers/job?force=1&v=1'), calls)

    def test_bundle_cannot_fetch_public_or_unvalidated_repo(self):
        github = GitHub('test-token')
        for repo in ['catalog', '../bad', 'syno-reclaim']:
            with self.assertRaises(ValueError):
                github.bundle(repo)

    def test_dependency_fetch_does_not_export_pat_to_argv_or_env(self):
        token = 'fixture-credential-never-exported'
        github = GitHub(token)

        def execute(command, **options):
            self.assertNotIn(token, repr(command))
            self.assertNotIn(token, repr(options['env']))
            if 'bundle' in command:
                pathlib.Path(command[5]).write_bytes(b'# v2 git bundle\nfixture')
            return Mock(returncode=0)

        with patch('controller.subprocess.run', side_effect=execute):
            self.assertTrue(github.bundle('syno-pkg-land-sdk').startswith(b'# v2 git bundle\n'))

    def test_launch_failure_unregisters_jit_runner(self):
        gh, docker = Mock(), Mock()
        controller = Controller(gh, docker, 'ci-runner:1')
        gh.pages.side_effect = lambda path, key: ([{'id': 10}] if key == 'workflow_runs' else
            [{'status': 'queued', 'labels': ['self-hosted', 'private-ci']}])
        gh.request.return_value = {'runner': {'id': 42}, 'encoded_jit_config': 'single-use'}
        docker.launch.side_effect = RuntimeError('launch failed')
        with patch.object(controller, 'reconcile', return_value=[]), \
                patch.object(controller, 'private', return_value=True), self.assertRaises(RuntimeError):
            controller.tick()
        self.assertEqual(gh.request.call_args.kwargs, {'method': 'DELETE'})
        self.assertTrue(gh.request.call_args.args[0].endswith('/actions/runners/42'))

    def test_visibility_change_during_source_fetch_prevents_registration(self):
        gh, docker = Mock(), Mock()
        controller = Controller(gh, docker, 'ci-runner:1')
        gh.pages.side_effect = lambda path, key: ([{'id': 10}] if key == 'workflow_runs' else
            [{'status': 'queued', 'labels': ['self-hosted', 'private-ci']}])
        answers = iter([True, False] + [False] * len(REPOS))
        with patch.object(controller, 'reconcile', return_value=[]), \
                patch.object(controller, 'private', side_effect=lambda _: next(answers)):
            controller.tick()
        gh.bundle.assert_called_once()
        gh.request.assert_not_called()
        docker.launch.assert_not_called()


if __name__ == '__main__':
    unittest.main()
