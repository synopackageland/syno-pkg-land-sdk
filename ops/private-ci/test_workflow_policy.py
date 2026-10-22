import copy
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('policy', Path(__file__).with_name('workflow-policy.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
OWN = module.OWN_WORKFLOW


class API:
    def __init__(self):
        self.enabled = False
        self.ws = [{'id': 1, 'path': '.github/workflows/upstream.yml', 'state': 'active'},
                   {'id': 2, 'path': OWN, 'state': 'disabled_manually'}]
        self.files = [w['path'] for w in self.ws]
        self.calls = []
        self.approval = {'approval_policy': 'first_time_contributors'}
        self.busy = False
        self.busy_path = '.github/workflows/upstream.yml'
        self.refuse = False
        self.bad_readback = False

    def __call__(self, path, method='GET', data=None):
        suffix = path.removeprefix('repos/owner/repo')
        self.calls.append((suffix, method, data))
        if suffix == '':
            return {'private': False, 'default_branch': 'main'}
        if suffix == '/actions/permissions':
            if method == 'PUT':
                if data['enabled']:
                    assert self.ws[0]['state'] == 'disabled_manually'
                    assert self.approval['approval_policy'] == 'all_external_contributors'
                self.enabled = data['enabled']
            return {'enabled': self.enabled}
        if suffix == '/actions/permissions/fork-pr-contributor-approval':
            if method == 'PUT':
                self.approval = data
            return self.approval
        if suffix.startswith('/actions/runs?'):
            busy = self.busy and 'status=queued' in suffix
            return {'total_count': int(busy), 'workflow_runs': [{'path': self.busy_path}] if busy else []}
        if suffix.startswith('/actions/workflows?'):
            out = copy.deepcopy(self.ws)
            if self.enabled and self.bad_readback:
                out[0]['state'] = 'active'
            return {'workflows': out}
        if suffix.startswith('/contents/'):
            return [{'path': p, 'name': p.split('/')[-1]} for p in self.files]
        if suffix.startswith('/actions/workflows/'):
            if self.refuse:
                raise module.PolicyError('HTTP 422')
            wid = int(suffix.split('/')[-2])
            for w in self.ws:
                if w['id'] == wid:
                    w['state'] = 'active' if suffix.endswith('/enable') else 'disabled_manually'
            return None
        raise AssertionError(suffix)


class TestPolicy(unittest.TestCase):
    def setUp(self):
        self.api = API()
        self.policy = module.Policy(self.api, 'owner/repo')

    def test_activate_disables_before_enable_and_reads_back(self):
        result = self.policy.apply('activate')
        self.assertTrue(result['enabled'])
        mutations = [(p, d) for p, m, d in self.api.calls if m == 'PUT']
        self.assertLess(mutations.index(('/actions/workflows/1/disable', None)),
                        mutations.index(('/actions/permissions', {'enabled': True})))
        self.assertEqual([w['path'] for w in result['workflows'] if w['state'] == 'active'], [OWN])

    def test_prepare_is_idempotent_and_stays_off_without_own(self):
        self.api.ws.pop()
        self.policy.apply('prepare')
        self.api.calls.clear()
        self.policy.apply('prepare')
        self.assertFalse(self.api.enabled)
        self.assertFalse(any(m == 'PUT' for p, m, d in self.api.calls))

    def test_pending_runs_prevent_mutation(self):
        self.api.busy = True
        with self.assertRaisesRegex(module.PolicyError, 'Unfinished'):
            self.policy.apply('activate')
        self.assertFalse(self.api.enabled)
        self.assertEqual(self.api.ws[0]['state'], 'active')

    def test_api_refusal_fails_closed(self):
        self.api.refuse = True
        with self.assertRaisesRegex(module.PolicyError, '422'):
            self.policy.apply('activate')
        self.assertFalse(self.api.enabled)

    def test_unregistered_file_prevents_enable(self):
        self.api.files.append('.github/workflows/new-upstream.yml')
        with self.assertRaisesRegex(module.PolicyError, 'not registered'):
            self.policy.apply('activate')
        self.assertFalse(self.api.enabled)

    def test_missing_owned_workflow_prevents_enable(self):
        self.api.files.remove(OWN)
        with self.assertRaises(module.PolicyError):
            self.policy.apply('activate')
        self.assertFalse(self.api.enabled)

    def test_post_enable_failure_rolls_back(self):
        self.api.bad_readback = True
        with self.assertRaises(module.PolicyError):
            self.policy.apply('activate')
        self.assertFalse(self.api.enabled)

    def test_already_enabled_requires_explicit_pause(self):
        self.api.enabled = True
        with self.assertRaisesRegex(module.PolicyError, 'Pause'):
            self.policy.apply('prepare')
        self.assertFalse(any(m == 'PUT' for p, m, d in self.api.calls))
        self.policy.apply('pause')
        self.assertFalse(self.api.enabled)

    def test_pagination(self):
        def api(path, *args):
            page = int(path.split('page=')[-1])
            return {'workflows': [None] * (100 if page == 1 else 2)}
        self.assertEqual(len(module.Policy(api, 'owner/repo').workflows()), 102)

    def test_managed_dependabot_is_explicitly_reported_and_never_disabled(self):
        managed = {'id': 3, 'path': module.MANAGED_DEPENDABOT, 'state': 'active'}
        self.api.ws.append(managed)
        self.api.busy = True
        self.api.busy_path = module.MANAGED_DEPENDABOT
        result = self.policy.apply('activate')
        self.assertTrue(result['enabled'])
        self.assertEqual(result['allowed_exceptions'], [managed])
        self.assertFalse(any(p == '/actions/workflows/3/disable' for p, m, d in self.api.calls))
        self.assertEqual(self.api.ws[0]['state'], 'disabled_manually')

    def test_dependabot_yaml_and_similarly_named_dynamic_workflows_are_not_exempt(self):
        for path in ('.github/workflows/dependabot-updates.yml',
                     'dynamic/dependabot/other', module.MANAGED_DEPENDABOT + '-extra'):
            with self.subTest(path=path), self.assertRaises(module.PolicyError):
                self.policy.verify([{'path': OWN, 'state': 'active'},
                                    {'path': path, 'state': 'active'}], True)

    def test_managed_exception_does_not_allow_other_running_workflows(self):
        self.api.busy = True
        self.api.busy_path = '.github/workflows/dependabot-updates.yml'
        with self.assertRaisesRegex(module.PolicyError, 'Unfinished'):
            self.policy.apply('activate')
        self.assertFalse(self.api.enabled)

    def test_exception_still_requires_owned_workflow_active(self):
        with self.assertRaises(module.PolicyError):
            self.policy.verify([{'path': module.MANAGED_DEPENDABOT, 'state': 'active'}], True)

    def test_token_file_permissions_and_dotenv(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / 'token'
            p.write_text('GITHUB_TOKEN="test-value"\nOTHER=ignored\n')
            p.chmod(0o600)
            self.assertEqual(module.read_token(p), 'test-value')
            p.chmod(0o644)
            with self.assertRaises(module.PolicyError):
                module.read_token(p)


if __name__ == '__main__':
    unittest.main()
