import json
import os
import subprocess
import sys
import unittest
from unittest import mock

from synopkgland_app import App, SDK

HERE = os.path.dirname(os.path.abspath(__file__))
CTX = {"user": "alice", "admin": False, "locale": "en", "request_id": "0" * 32, "deadline_ms": 1000}


def run_app(*args, frames=(), bridge="stdio"):
    """Runs exec_app.py as the runtime would, with frames on its stdin."""
    env = {"PATH": os.environ["PATH"], "PYTHONPATH": os.path.dirname(HERE), "PYTHONDONTWRITEBYTECODE": "1"}
    if bridge is not None:
        env["SYNOPKGLAND_BRIDGE"] = bridge
    stdin = b"".join(json.dumps(f).encode() + b"\n" for f in frames)
    cmd = [sys.executable, os.path.join(HERE, "exec_app.py")] + list(args)
    return subprocess.run(cmd, input=stdin, env=env, capture_output=True, timeout=10)


def call(method, params=None):
    return [{"t": "hello", "versions": [1]}, {"t": "call", "id": "1", "method": method, "params": params, "ctx": CTX}]


class RunTest(unittest.TestCase):
    def test_answers_one_call_on_stdin_and_stdout(self):
        out = run_app(frames=call("a.quiet", [1]))
        frames = [json.loads(line) for line in out.stdout.splitlines()]
        self.assertEqual(frames[0]["methods"], ["a.noisy", "a.quiet"])
        self.assertEqual(frames[1], {"t": "result", "id": "1", "ok": True, "data": [1]})
        self.assertEqual(out.returncode, 0)

    def test_print_goes_to_stderr_not_the_channel(self):
        out = run_app(frames=call("a.noisy"))
        frames = [json.loads(line) for line in out.stdout.splitlines()]
        self.assertEqual(frames[1]["data"], "ok")
        self.assertIn(b"not a frame", out.stderr)

    def test_sdk_name_carries_the_release_version(self):
        with open(os.path.join(HERE, "..", "..", "node", "package.json")) as source:
            version = json.load(source)["version"]
        self.assertEqual(SDK, "python/" + version)

    def test_describe_prints_the_methods(self):
        out = run_app("--describe", bridge=None)
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertEqual(json.loads(out.stdout), {"sdk": SDK, "versions": [1], "methods": ["a.noisy", "a.quiet"]})

    def test_refuses_other_modes(self):
        for bridge in [None, "", "socket:/tmp/app.sock"]:
            out = run_app(frames=call("a.quiet"), bridge=bridge)
            self.assertEqual(out.returncode, 2, bridge)
            self.assertIn(b"SYNOPKGLAND_BRIDGE", out.stderr)
            self.assertEqual(out.stdout, b"")

    def test_refuses_to_run_as_root(self):
        with mock.patch("os.geteuid", return_value=0), self.assertRaises(SystemExit) as e:
            App().run()
        self.assertIn("root", str(e.exception.code))


if __name__ == "__main__":
    unittest.main()
