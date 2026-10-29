#!/usr/bin/env python3
"""Exercise real npm publish requests against a loopback-only registry."""
import base64
import http.server
import io
import json
import os
from pathlib import Path
import runpy
import subprocess
import tarfile
import tempfile
import threading
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent
prepare = runpy.run_path(str(ROOT / 'prepare-npm-publish.py'))['prepare']
validate = runpy.run_path(str(ROOT / 'check-npm-metadata.py'))['validate']
publish = runpy.run_path(str(ROOT / 'publish-npm.py'))['publish']

class PublishTests(unittest.TestCase):
    def test_directory_request_is_clean_and_preserves_gated_bytes(self):
        requests = []
        class Registry(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args): pass
            def do_GET(self):
                self.send_response(404); self.end_headers(); self.wfile.write(b'{"error":"not found"}')
            def do_PUT(self):
                requests.append(json.loads(self.rfile.read(int(self.headers['Content-Length']))))
                self.send_response(201); self.end_headers(); self.wfile.write(b'{"ok":true}')
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Registry)
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        try:
            with tempfile.TemporaryDirectory() as temporary:
                work = Path(temporary)
                original = work / 'input.tgz'
                with tarfile.open(original, 'w:gz') as archive:
                    for name, data in {
                        'package/package.json': json.dumps({'name':'public-release-fixture','version':'1.0.0','files':['index.js']}),
                        'package/index.js':'export const answer = 42;\n',
                    }.items():
                        content = data.encode(); member = tarfile.TarInfo(name); member.size = len(content)
                        archive.addfile(member, io.BytesIO(content))
                stage = prepare(original, work / 'clean')
                registry = 'http://127.0.0.1:' + str(server.server_port)
                config = work / 'npmrc'
                config.write_text('registry='+registry+'\n//127.0.0.1:'+str(server.server_port)+'/:_authToken=synthetic-test-value\n')
                env = {**os.environ, 'NPM_CONFIG_USERCONFIG':str(config), 'NPM_CONFIG_PROVENANCE':'false'}
                for argument, cwd in [(str(original), work), (None, Path(stage['packageDirectory']))]:
                    command = (['npm','publish',argument,'--registry='+registry,'--ignore-scripts','--access=public'] if argument else
                               [os.sys.executable,str(ROOT/'publish-npm.py'),stage['packageDirectory'],'--userconfig',str(config)])
                    subprocess.run(command,cwd=cwd,env=env,capture_output=True,check=True,timeout=30)
                old = requests[0]['versions']['1.0.0']
                with self.assertRaises(ValueError): validate(old)
                clean = requests[1]['versions']['1.0.0']
                validate(requests[1])
                self.assertNotIn('_from', clean); self.assertNotIn('_resolved', clean)
                sent = base64.b64decode(next(iter(requests[1]['_attachments'].values()))['data'])
                self.assertEqual(sent, Path(stage['tarball']).read_bytes())
        finally:
            server.shutdown(); server.server_close(); thread.join()

    def test_manifest_local_dependency_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            work = Path(temporary); artifact = work / 'input.tgz'
            with tarfile.open(artifact, 'w:gz') as archive:
                content = json.dumps({'name':'fixture','version':'1.0.0','dependencies':{'example':'file:../source'}}).encode()
                member = tarfile.TarInfo('package/package.json');member.size = len(content)
                archive.addfile(member,io.BytesIO(content))
            with self.assertRaises(ValueError): prepare(artifact, work/'clean')

    def test_nested_registry_metadata_is_rejected(self):
        for key in ['_from','_resolved']:
            with self.assertRaises(ValueError): validate({'versions':{'1.0.0':{key:'fixture'}}})

    def test_publish_wrapper_rejects_tarball_before_running_npm(self):
        with tempfile.TemporaryDirectory() as temporary:
            artifact = Path(temporary) / 'package.tgz'
            artifact.write_bytes(b'fixture')
            with patch('subprocess.run') as run:
                with self.assertRaisesRegex(ValueError, 'tarball paths are forbidden'):
                    publish(artifact, Path(temporary) / 'npmrc')
                run.assert_not_called()

if __name__ == '__main__': unittest.main()
