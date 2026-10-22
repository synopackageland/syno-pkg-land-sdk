import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from cache_trim import MAX_AGE, old_files, trim, usage
from controller import CACHE_VOLUMES, THREAD_GOBUILD, Controller, Docker, REPOS, APIError


class TrimTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.roots = tuple(self.base / name for name in ('go', 'cargo', 'npm'))
        for root in self.roots:
            root.mkdir()
        self.now = 2_000_000_000

    def file(self, rel, days, size=128 * 1024):
        p = self.base / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(b'x' * size)
        stamp = self.now - days * 86400
        os.utime(p, (stamp, stamp))
        return p

    def test_excess_evicts_oldest_to_eighty_percent(self):
        old = self.file('npm/old', 10)
        new = self.file('npm/new', 1)
        self.file('cargo/sccache/recent', 0)
        result = trim(self.roots, 350 * 1024, self.now)
        self.assertFalse(old.exists())
        self.assertTrue(new.exists())
        self.assertLessEqual(result['after'], int(350 * 1024 * .8))
        self.assertEqual(result['evicted_units'], 1)

    def test_go_age_uses_last_access_not_just_modification(self):
        stale = self.file('go/build/ab/old-d', 4)
        accessed = self.file('go/build/ab/read-d', 4)
        os.utime(accessed, (self.now, self.now - 4 * 86400))
        result = trim(self.roots, 10 * 1024**2, self.now)
        self.assertFalse(stale.exists())
        self.assertTrue(accessed.exists())
        self.assertEqual(result['stale_go_files'], 1)

    def test_daily_thread_trim_uses_only_mtime_and_leaves_new_files(self):
        root = self.base / 'thread'
        stale = self.file('thread/a/old-d', 4)
        os.utime(stale, (self.now, self.now - 4 * 86400))
        fresh = self.file('thread/b/new-d', 2)
        other = self.file('unrelated/old-d', 10)
        self.assertEqual(old_files(root, self.now, use_atime=False), 1)
        self.assertFalse(stale.exists())
        self.assertTrue(fresh.exists())
        self.assertTrue(other.exists())

    def test_cargo_sources_are_removed_as_complete_crates(self):
        a = self.file('cargo/registry/src/index/crate-1/src/lib.rs', 10)
        b = self.file('cargo/registry/src/index/crate-1/Cargo.toml', 10)
        fresh = self.file('npm/new', 0)
        # Set directory mtimes too: eviction uses latest file access/mtime and dir mtime.
        for p in [a.parent, a.parent.parent]:
            os.utime(p, (self.now - 10 * 86400, self.now - 10 * 86400))
        trim(self.roots, 300 * 1024, self.now)
        self.assertFalse(a.parent.parent.exists())
        self.assertTrue(fresh.exists())

    def test_symlinks_cannot_escape_to_unrelated_files(self):
        other = self.file('outside/keep', 10)
        (self.roots[2] / 'escape').symlink_to(other.parent, target_is_directory=True)
        self.file('npm/new', 0)
        trim(self.roots, 100 * 1024, self.now)
        self.assertTrue(other.exists())
        self.assertFalse((self.roots[2] / 'escape').exists())

    def test_root_symlink_rejected_and_no_growth_is_idempotent(self):
        result = trim(self.roots, now=self.now)
        self.assertEqual(result['before'], result['after'])
        self.assertEqual(trim(self.roots, now=self.now), result)
        self.roots[0].rmdir()
        self.roots[0].symlink_to(self.roots[1], target_is_directory=True)
        with self.assertRaises(ValueError):
            trim(self.roots, now=self.now)


class CacheDockerTests(unittest.TestCase):
    def test_public_cannot_obtain_cache_mounts(self):
        docker = Docker()
        with patch.object(docker, 'request') as api, self.assertRaises(ValueError):
            docker.launch('ci-linux-01', 'ci-runner:1', 'catalog', 1, 'jit', 0)
        api.assert_not_called()

    def test_trim_helper_mounts_exact_allowlist_and_cleans_up(self):
        for thread in (False, True):
            docker, calls = Docker(), []
            def request(method, path, data=None, **kwargs):
                calls.append((method, path, data))
                if method == 'GET' and path.startswith('/containers/ci-trim'):
                    raise APIError(404)
                if path.startswith('/containers/create'):
                    return {'Id': 'helper'}
                if path == '/containers/helper/json':
                    return {'State': {'Running': False, 'ExitCode': 0}}
                return {}
            with patch.object(docker, 'request', side_effect=request):
                docker.trim_caches('ci-runner:1', thread=thread)
            spec = next(data for method, path, data in calls if path.startswith('/containers/create'))
            sources = {m['Source'] for m in spec['HostConfig']['Mounts']}
            self.assertEqual(sources, {THREAD_GOBUILD} if thread else set(CACHE_VOLUMES))
            self.assertEqual(spec['HostConfig']['NetworkMode'], 'none')
            self.assertTrue(spec['HostConfig']['ReadonlyRootfs'])
            self.assertGreaterEqual(int(spec['HostConfig']['LogConfig']['Config']['max-file']), 2)
            self.assertNotIn('Binds', spec['HostConfig'])
            self.assertIn(('DELETE', '/containers/helper?force=1&v=1', None), calls)

    def test_active_job_is_never_pruned_and_daily_only_once(self):
        gh, docker = Mock(), Mock()
        gh.pages.return_value = []
        c = Controller(gh, docker, 'ci-runner:1')
        with patch.object(c, 'reconcile', return_value=[{'Id': 'busy'}]):
            c.tick()
        docker.trim_caches.assert_not_called()
        with patch.object(c, 'reconcile', return_value=[]):
            c.tick()
            c.tick()
        self.assertEqual(docker.trim_caches.call_count, 3)
        self.assertEqual(sum(bool(call.kwargs.get('thread')) for call in docker.trim_caches.call_args_list), 1)
        docker.ensure_caches.assert_called_once()

    def test_trim_failure_prevents_dispatch(self):
        gh, docker = Mock(), Mock()
        c = Controller(gh, docker, 'ci-runner:1')
        docker.trim_caches.side_effect = RuntimeError('failure')
        with patch.object(c, 'reconcile', return_value=[]), self.assertRaises(RuntimeError):
            c.tick()
        gh.request.assert_not_called()
        docker.launch.assert_not_called()


if __name__ == '__main__':
    unittest.main()
