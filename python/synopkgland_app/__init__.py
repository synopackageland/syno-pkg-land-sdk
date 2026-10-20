"""Python SDK for Synology Package Land Apps (channel spec/bridge/v1).

The Go runtime checks the request's origin, the DSM session, the entry gate
and the route policy, then starts the App once per call (mode exec) and
hands it the call on stdin; a handler here only sees calls that passed them.
Standard library only; Python 3.8 or later (DSM 7 ships 3.8).
"""

from __future__ import annotations

import json
import os
import re
import sys
import threading
import time
import traceback
from typing import TYPE_CHECKING, Any, BinaryIO, Callable, Dict, List, Optional, Tuple, TypeVar

from ._refusals import REFUSALS
from ._version import SDK_VERSION

if TYPE_CHECKING:
    from . import frames

__all__ = ["App", "Context", "Error", "SDK", "VERSION"]

VERSION = 1
SDK = "python/" + SDK_VERSION

_METHOD = re.compile(r"[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+\Z")
_APP_CODE = re.compile(r"[a-z][a-z0-9_]{0,63}\Z")

# The runtime's own refusals (spec/bridge/v1 README, Errors): an App that
# returned one would make the browser act on a decision the runtime did not
# take, so they become internal.
_RESERVED = frozenset(
    {
        "session_required",
        "admin_required",
        "app_privilege_denied",
        "auth_unavailable",
        "unknown_method",
        "cross_origin",
        "origin_required",
        "request_timeout",
        "request_canceled",
        "request_too_large",
        "json_required",
        "method_not_allowed",
        "app_unavailable",
    }
)

_MAX_MESSAGE = 1024
_INTERNAL: frames.Error = {"code": "internal", "message": "internal error"}


class Error(Exception):
    """An error the browser may see: a stable code and a message.

    code is the App's own ^[a-z][a-z0-9_]{0,63}$ code, or a protocol v1
    refusal such as SCOPE_DENIED that a service returned. The runtime's own
    codes (session_required, request_timeout, ...) and anything else reach
    the browser as internal. message is cut at 1024 bytes.
    """

    def __init__(self, code: str, message: str) -> None:
        Exception.__init__(self, code + ": " + message)
        self.code = code
        self.message = message

    @classmethod
    def bad_request(cls, message: str) -> Error:
        return cls("bad_request", message)


def _canceled() -> Error:
    return Error("request_canceled", "the request is over")


class Context:
    """What the runtime verified about the call, and the way back to it.

    user is the DSM account ("" for a public method) and admin whether it is
    an administrator; locale is the browser's first language tag, or "".
    deadline is the time.monotonic() value at which the request ends
    (deadline_ms is the time that was left when the call was sent).
    cancelled is set when the request is over: deadline, browser gone or
    channel closed. Never keep a Context beyond its call.
    """

    def __init__(self, ctx: frames.Context, channel: _Channel, batch_id: Optional[int] = None, started_at: Optional[float] = None) -> None:
        self._batch_id = batch_id
        self.user: str = ctx["user"]
        self.admin: bool = ctx["admin"]
        self.locale: str = ctx["locale"]
        self.request_id: str = ctx["request_id"]
        self.deadline_ms: int = ctx["deadline_ms"]
        self.deadline: float = (time.monotonic() if started_at is None else started_at) + self.deadline_ms / 1000
        self.cancelled: threading.Event = channel.cancelled
        self._channel = channel

    def runtime(self, op: str, params: Any = None) -> Any:
        """Ask the runtime for a service and return its result.

        Raises Error with the runtime's code when it refuses (unknown_service
        for an op it does not offer), and request_canceled once the request
        is over.
        """
        return self._channel.service(op, params, self._batch_id)


def _error(e: Error) -> frames.Error:
    """The result error for e: its code when an App may return it."""
    if not (_APP_CODE.match(e.code) and e.code not in _RESERVED or e.code in REFUSALS):
        print("synopkgland_app: an App may not return error code %r; sending internal" % e.code, file=sys.stderr)
        return _INTERNAL
    message = e.message.encode(errors="replace")[:_MAX_MESSAGE].decode(errors="ignore")
    return {"code": e.code, "message": message}


def _encode(frame: Any) -> bytes:
    text = json.dumps(frame, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
    try:
        return text.encode() + b"\n"
    except UnicodeEncodeError:
        # A lone surrogate: escaped, the runtime reads it as U+FFFD.
        return json.dumps(frame, separators=(",", ":"), allow_nan=False).encode() + b"\n"


_Pending = Tuple[threading.Event, List[Any]]


class _Channel:
    def __init__(self, rfile: BinaryIO, wfile: BinaryIO) -> None:
        self._rfile, self._wfile = rfile, wfile
        self._lock = threading.Lock()
        self._pending: Dict[str, _Pending] = {}
        self._next = 0
        self.cancelled = threading.Event()

    def recv(self) -> Optional[Dict[str, Any]]:
        try:
            line = self._rfile.readline()
            frame = json.loads(line) if line else None
        except (OSError, ValueError):
            return None
        return frame if isinstance(frame, dict) else None

    def send(self, line: bytes) -> None:
        """Write one encoded frame; a closed channel ends the call."""
        try:
            with self._lock:
                self._wfile.write(line)
                self._wfile.flush()
        except OSError:
            self.end()

    def service(self, op: str, params: Any, batch_id: Optional[int] = None) -> Any:
        done, reply = threading.Event(), []  # type: threading.Event, List[Any]
        with self._lock:
            if self.cancelled.is_set():
                raise _canceled()
            self._next += 1
            sid = "s%d" % self._next
            self._pending[sid] = (done, reply)
        try:
            frame: Dict[str, Any] = {"t": "svc", "id": sid, "op": op, "params": params}
            if batch_id is not None: frame["batch_id"] = batch_id
            line = _encode(frame)
        except Exception:
            with self._lock:
                self._pending.pop(sid, None)
            raise
        self.send(line)
        done.wait()
        if not reply:
            raise _canceled()
        if not reply[0].get("ok"):
            error = reply[0].get("error") or {}
            raise Error(error.get("code", "internal"), error.get("message", ""))
        return reply[0].get("result")

    def route(self) -> None:
        """Route the frames that arrive while the handler runs."""
        while True:
            frame = self.recv()
            if frame is None:
                break
            if frame.get("t") == "svc_result":
                with self._lock:
                    pending = self._pending.pop(frame.get("id", ""), None)
                if pending:
                    pending[1].append(frame)
                    pending[0].set()
            elif frame.get("t") == "cancel":
                self.end()
        self.end()

    def end(self) -> None:
        """The request is over: set cancelled and wake every service call."""
        with self._lock:
            self.cancelled.set()
            pending, self._pending = list(self._pending.values()), {}
        for done, _ in pending:
            done.set()


Handler = Callable[[Context, Any], Any]
H = TypeVar("H", bound=Handler)


class App:
    """The App's methods, served on one channel per call."""

    def __init__(self) -> None:
        self._methods: Dict[str, Handler] = {}

    def method(self, name: str) -> Callable[[H], H]:
        """Register the decorated function as the handler of method name.

        The handler gets the Context and the request's params and returns
        any JSON value; raising Error sends that error, and any other
        exception sends internal (the traceback goes to stderr).
        """
        if not _METHOD.match(name):
            raise ValueError("not a method name: %r" % name)
        if name in self._methods:
            raise ValueError("method registered twice: " + name)

        def register(handler: H) -> H:
            self._methods[name] = handler
            return handler

        return register

    def serve(self, rfile: BinaryIO, wfile: BinaryIO) -> None:
        """Answer one call on a channel."""
        channel = _Channel(rfile, wfile)
        hello = channel.recv()
        if not hello or hello.get("t") != "hello" or VERSION not in hello.get("versions", []):
            print("synopkgland_app: the runtime does not offer channel version %d" % VERSION, file=sys.stderr)
            return
        reply: Dict[str, Any] = {"t": "hello", "version": VERSION, "sdk": SDK, "methods": sorted(self._methods)}
        batch = hello.get("batch") == 1
        if batch: reply["batch"] = 1
        channel.send(_encode(reply))
        request = channel.recv()
        if not request: return
        if batch:
            calls = request.get("calls")
            if request.get("t") != "batch" or request.get("v") != 1 or not isinstance(calls, list) or not 1 <= len(calls) <= 8: return
        else:
            calls = [request]
        threading.Thread(target=channel.route, daemon=True).start()
        started_at = time.monotonic()
        for i, call in enumerate(calls):
            if call.get("t") != "call": return
            ctx = Context(call["ctx"], channel, i if batch else None, started_at if batch else None)
            channel.send(self._result(ctx, call))

    def _result(self, ctx: Context, call: Dict[str, Any]) -> bytes:
        frame = {"t": "result", "id": call["id"]}
        try:
            data = self._methods[call["method"]](ctx, call["params"])
            return _encode(dict(frame, ok=True, data=data))
        except Error as e:
            return _encode(dict(frame, ok=False, error=_error(e)))
        except Exception:
            traceback.print_exc()
            return _encode(dict(frame, ok=False, error=_INTERNAL))

    def describe(self) -> Dict[str, Any]:
        """What --describe prints: the App's methods, for the package build."""
        return {"sdk": SDK, "versions": [VERSION], "methods": sorted(self._methods)}

    def run(self) -> None:
        """Answer the one call the runtime sends on stdin (SYNOPKGLAND_BRIDGE=stdio).

        With --describe, print describe() as JSON instead.
        """
        if sys.argv[1:] == ["--describe"]:
            print(json.dumps(self.describe()))
            return
        if os.geteuid() == 0:
            sys.exit("synopkgland_app: refusing to run as root")
        if os.environ.get("SYNOPKGLAND_BRIDGE") != "stdio":
            print("synopkgland_app: the runtime starts an App with SYNOPKGLAND_BRIDGE=stdio; run --describe to list its methods", file=sys.stderr)
            sys.exit(2)
        # The channel keeps the real stdin and stdout; print() and child
        # processes write to stderr instead and read nothing.
        sys.stdout.flush()
        rfile, wfile = os.fdopen(os.dup(0), "rb"), os.fdopen(os.dup(1), "wb")
        null = os.open(os.devnull, os.O_RDONLY)
        os.dup2(null, 0)
        os.close(null)
        os.dup2(2, 1)
        self.serve(rfile, wfile)
