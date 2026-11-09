import json
import unittest
from unittest.mock import patch
from subprocess import CompletedProcess
from synopkgland_app import SharedFolderConfig

class SharedFolderTest(unittest.TestCase):
    def test_sdk_controller_resolves_and_requires_package_selection(self):
        cfg = SharedFolderConfig('ExampleApp', '/var/packages/ExampleApp/var')
        with patch('synopkgland_app.shared_folder.subprocess.run') as run:
            run.return_value = CompletedProcess([], 0, json.dumps({'path':'/volume2/Media/Projects/Deep'}).encode(), b'')
            self.assertEqual(cfg.require('data'), '/volume2/Media/Projects/Deep')
            args, kwargs = run.call_args
            self.assertEqual(args[0][-2:], ['--key','data'])
            self.assertEqual(kwargs['env']['SYNOPKG_PKGVAR'], '/var/packages/ExampleApp/var')
            run.return_value = CompletedProcess([], 2, b'', b'File Station > selected folder > Permission')
            with self.assertRaisesRegex(OSError, 'File Station'):
                cfg.require('data')
            run.return_value = CompletedProcess([], 0, b'{"path":"relative"}', b'')
            with self.assertRaises(ValueError):
                cfg.require('data')
        with self.assertRaises(ValueError):
            cfg.require('../escape')
