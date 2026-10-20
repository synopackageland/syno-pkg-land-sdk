import contextlib
import io
import json
import socket
import threading
import time
import unittest

from synopkgland_app import App, Error


class Runtime:
    """The runtime side of one channel, over a socket pair."""

    def __init__(self, app):
        self.sock, app_end = socket.socketpair()
        self.sock.settimeout(5)
        self.r = self.sock.makefile("rb")
        self.thread = threading.Thread(target=self.serve, args=(app, app_end), daemon=True)
        self.thread.start()

    @staticmethod
    def serve(app, conn):
        with conn, conn.makefile("rb") as r, conn.makefile("wb") as w:
            app.serve(r, w)

    def send(self, frame):
        self.sock.sendall(json.dumps(frame).encode() + b"\n")

    def recv(self):
        return json.loads(self.r.readline())

    def close(self):
        self.r.close()
        self.sock.close()

    def call(self, method, params=None, user="alice"):
        self.send({"t": "hello", "versions": [1]})
        hello = self.recv()
        ctx = {"user": user, "admin": False, "locale": "en", "request_id": "r1", "deadline_ms": 1000}
        self.send({"t": "call", "id": "1", "method": method, "params": params, "ctx": ctx})
        return hello


class AppTest(unittest.TestCase):
    def runtime(self, app):
        rt = Runtime(app)
        self.addCleanup(rt.close)
        return rt

    def test_batch_isolates_errors_and_binds_each_context(self):
        app = App()
        app.method("a.read")(lambda ctx, p: ctx.user)
        def fail(ctx, p):
            raise Error("conflict", "taken")
        app.method("a.fail")(fail)
        rt = self.runtime(app)
        rt.send({"t": "hello", "versions": [1], "batch": 1})
        self.assertEqual(rt.recv()["batch"], 1)
        calls = []
        for i, method in enumerate(["a.read", "a.fail", "a.read"]):
            calls.append({"t": "call", "id": str(i), "method": method, "params": None,
                          "ctx": {"user": "alice" if i == 0 else "", "admin": False, "locale": "en", "request_id": "r1", "deadline_ms": 1000}})
        rt.send({"t": "batch", "v": 1, "calls": calls})
        self.assertEqual(rt.recv()["data"], "alice")
        self.assertEqual(rt.recv()["error"]["code"], "conflict")
        self.assertEqual(rt.recv()["data"], "")

    def test_hello_lists_the_methods(self):
        app = App()
        app.method("a.one")(lambda ctx, p: 1)
        app.method("a.two")(lambda ctx, p: 2)
        rt = self.runtime(app)
        hello = rt.call("a.one")
        self.assertEqual(hello["version"], 1)
        self.assertEqual(sorted(hello["methods"]), ["a.one", "a.two"])
        self.assertEqual(rt.recv(), {"t": "result", "id": "1", "ok": True, "data": 1})

    def test_handler_gets_context_and_params(self):
        app = App()

        @app.method("hello.greet")
        def greet(ctx, params):
            return {"message": params["greeting"] + ", " + ctx.user, "locale": ctx.locale}

        rt = self.runtime(app)
        rt.call("hello.greet", {"greeting": "Hi"})
        self.assertEqual(rt.recv()["data"], {"message": "Hi, alice", "locale": "en"})

    def test_error_and_crash(self):
        app = App()

        @app.method("a.fail")
        def fail(ctx, params):
            raise Error("conflict", "taken")

        @app.method("a.crash")
        def crash(ctx, params):
            raise ValueError("secret detail")

        rt = self.runtime(app)
        rt.call("a.fail")
        self.assertEqual(rt.recv(), {"t": "result", "id": "1", "ok": False, "error": {"code": "conflict", "message": "taken"}})
        rt = self.runtime(app)
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rt.call("a.crash")
            self.assertEqual(rt.recv()["error"], {"code": "internal", "message": "internal error"})
        self.assertIn("secret detail", stderr.getvalue())

    def fail_with(self, code, message="no"):
        app = App()

        @app.method("a.fail")
        def fail(ctx, params):
            raise Error(code, message)

        rt = self.runtime(app)
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            rt.call("a.fail")
            error = rt.recv()["error"]
        return error, stderr.getvalue()

    def test_protocol_refusals_pass(self):
        self.assertEqual(self.fail_with("SCOPE_DENIED")[0], {"code": "SCOPE_DENIED", "message": "no"})

    def test_codes_an_app_may_not_return_become_internal(self):
        for code in ["session_required", "app_unavailable", "NOT_A_REFUSAL", "Mixed", "a-b", "x" * 65, ""]:
            error, log = self.fail_with(code)
            self.assertEqual(error, {"code": "internal", "message": "internal error"}, code)
            self.assertIn("may not return error code %r" % code, log)

    def test_long_messages_are_cut_at_1024_bytes(self):
        error, _ = self.fail_with("conflict", "é" * 600)
        self.assertEqual(error["message"], "é" * 512)
        self.assertEqual(self.fail_with("conflict", "a" + "é" * 600)[0]["message"], "a" + "é" * 511)

    def test_runtime_service_call(self):
        app = App()

        @app.method("a.save")
        def save(ctx, params):
            return ctx.runtime("files.list", {"path": "x", "content": "y"})

        rt = self.runtime(app)
        rt.call("a.save")
        svc = rt.recv()
        self.assertEqual((svc["t"], svc["op"], svc["params"]), ("svc", "files.list", {"path": "x", "content": "y"}))
        rt.send({"t": "svc_result", "id": svc["id"], "ok": True, "result": {"bytes": 1}})
        self.assertEqual(rt.recv()["data"], {"bytes": 1})

    def test_runtime_service_refusal_raises(self):
        app = App()

        @app.method("a.save")
        def save(ctx, params):
            try:
                ctx.runtime("files.list", {})
            except Error as e:
                return e.code

        rt = self.runtime(app)
        rt.call("a.save")
        svc = rt.recv()
        rt.send({"t": "svc_result", "id": svc["id"], "ok": False, "error": {"code": "bad_request", "message": "no"}})
        self.assertEqual(rt.recv()["data"], "bad_request")

    def test_cancel_sets_the_flag(self):
        app = App()
        started = threading.Event()

        @app.method("a.slow")
        def slow(ctx, params):
            started.set()
            ctx.cancelled.wait(2)
            return ctx.cancelled.is_set()

        rt = self.runtime(app)
        rt.call("a.slow")
        started.wait(2)
        rt.send({"t": "cancel", "id": "1", "reason": "deadline"})
        self.assertEqual(rt.recv()["data"], True)

    def test_deadline_is_a_monotonic_time(self):
        app = App()
        app.method("a.left")(lambda ctx, p: ctx.deadline - time.monotonic())
        rt = self.runtime(app)
        rt.call("a.left")
        self.assertTrue(0.5 < rt.recv()["data"] <= 1.0)

    def test_cancel_ends_a_pending_service_call(self):
        app = App()

        @app.method("a.wait")
        def wait(ctx, params):
            try:
                ctx.runtime("files.list")
            except Error as e:
                return [e.code, ctx.cancelled.is_set()]

        rt = self.runtime(app)
        rt.call("a.wait")
        self.assertEqual(rt.recv()["params"], None)
        rt.send({"t": "cancel", "id": "1", "reason": "canceled"})
        self.assertEqual(rt.recv()["data"], ["request_canceled", True])

    def test_no_service_call_after_cancel(self):
        app = App()

        @app.method("a.late")
        def late(ctx, params):
            ctx.cancelled.wait(2)
            try:
                ctx.runtime("files.list", {})
            except Error as e:
                return e.code

        rt = self.runtime(app)
        rt.call("a.late")
        rt.send({"t": "cancel", "id": "1", "reason": "deadline"})
        self.assertEqual(rt.recv(), {"t": "result", "id": "1", "ok": True, "data": "request_canceled"})

    def test_data_that_is_not_json_is_internal(self):
        for data in [object(), float("nan"), {1j: 1}]:
            app = App()
            app.method("a.bad")(lambda ctx, p: data)
            rt = self.runtime(app)
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                rt.call("a.bad")
                self.assertEqual(rt.recv()["error"], {"code": "internal", "message": "internal error"}, data)
            self.assertIn("Traceback", stderr.getvalue())

    def test_lone_surrogates_go_out_escaped(self):
        app = App()
        app.method("a.echo")(lambda ctx, p: p)
        rt = self.runtime(app)
        rt.call("a.echo", "\ud800 é")
        self.assertEqual(rt.recv()["data"], "\ud800 é")

    def test_method_names_follow_the_manifest(self):
        app = App()
        for name in ["hello", "Hello.greet", "hello.", "hello.greet-all", ""]:
            with self.assertRaises(ValueError, msg=name):
                app.method(name)
        app.method("hello.greet_all.v2")(lambda ctx, p: None)
        with self.assertRaises(ValueError):
            app.method("hello.greet_all.v2")

    def test_bad_request(self):
        e = Error.bad_request("note must be a string")
        self.assertEqual((e.code, e.message), ("bad_request", "note must be a string"))


if __name__ == "__main__":
    unittest.main()
