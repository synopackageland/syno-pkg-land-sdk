use super::*;
use serde_json::{Value, json};
use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::UnixStream;
use std::thread;

/// The runtime side of one channel.
struct Runtime {
    w: UnixStream,
    r: BufReader<UnixStream>,
}

impl Runtime {
    fn start(app: App) -> Self {
        let (rt, app_end) = UnixStream::pair().unwrap();
        let w = app_end.try_clone().unwrap();
        thread::spawn(move || app.serve(app_end, w));
        Runtime {
            w: rt.try_clone().unwrap(),
            r: BufReader::new(rt),
        }
    }
    fn send(&mut self, v: Value) {
        self.w.write_all(format!("{v}\n").as_bytes()).unwrap();
    }
    fn recv(&mut self) -> Value {
        let mut line = String::new();
        self.r.read_line(&mut line).unwrap();
        serde_json::from_str(&line).unwrap()
    }
    fn call(&mut self, method: &str, params: &str) -> Value {
        self.send(json!({"t": "hello", "versions": [1]}));
        let hello = self.recv();
        let ctx =
            r#"{"user":"alice","admin":false,"locale":"en","request_id":"r1","deadline_ms":1000}"#;
        self.w
            .write_all(
                format!(
                    r#"{{"t":"call","id":"1","method":"{method}","params":{params},"ctx":{ctx}}}"#
                )
                .as_bytes(),
            )
            .unwrap();
        self.w.write_all(b"\n").unwrap();
        hello
    }
}

#[test]
fn hello_lists_the_methods() {
    let mut app = App::new();
    app.method("a.two", |_, _| Ok(2));
    app.method("a.one", |_, _| Ok(1));
    let mut rt = Runtime::start(app);
    let hello = rt.call("a.one", "null");
    assert_eq!(hello["version"], 1);
    assert_eq!(hello["methods"], json!(["a.one", "a.two"]));
    assert_eq!(
        rt.recv(),
        json!({"t": "result", "id": "1", "ok": true, "data": 1})
    );
}

#[test]
fn handler_gets_context_and_raw_params() {
    #[derive(serde::Deserialize)]
    struct Greet {
        greeting: String,
    }
    let mut app = App::new();
    app.method("hello.greet", |ctx, params| {
        let p: Greet = params.decode()?;
        Ok(json!({"message": format!("{}, {}", p.greeting, ctx.user), "raw": params.get()}))
    });
    let mut rt = Runtime::start(app);
    rt.call("hello.greet", r#"{"greeting":"Hi","n":2.50}"#);
    assert_eq!(
        rt.recv()["data"],
        json!({"message": "Hi, alice", "raw": r#"{"greeting":"Hi","n":2.50}"#})
    );
}

#[test]
fn bad_params_are_bad_request() {
    let mut app = App::new();
    app.method("a.b", |_, params| params.decode::<Vec<u8>>());
    let mut rt = Runtime::start(app);
    rt.call("a.b", r#"{"x":1}"#);
    assert_eq!(rt.recv()["error"]["code"], "bad_request");
}

#[test]
fn app_error() {
    let mut app = App::new();
    app.method("a.fail", |_, _| {
        Err::<(), _>(Error::new("conflict", "taken"))
    });
    let mut rt = Runtime::start(app);
    rt.call("a.fail", "null");
    assert_eq!(
        rt.recv(),
        json!({"t": "result", "id": "1", "ok": false, "error": {"code": "conflict", "message": "taken"}})
    );
}

#[test]
fn panic_is_internal() {
    let mut app = App::new();
    app.method("a.crash", |_, _| -> Result<(), Error> {
        panic!("secret detail")
    });
    let mut rt = Runtime::start(app);
    rt.call("a.crash", "null");
    assert_eq!(
        rt.recv()["error"],
        json!({"code": "internal", "message": "internal error"})
    );
}

#[test]
fn runtime_service_call_and_refusal() {
    let mut app = App::new();
    app.method("a.save", |ctx, _| {
        let ok: Value = ctx.runtime("files.list", &json!({"path": "x", "content": "y"}))?;
        let refused = ctx.runtime::<Value>("files.list", &json!({})).unwrap_err();
        Ok(json!([ok, refused.code]))
    });
    let mut rt = Runtime::start(app);
    rt.call("a.save", "null");
    let svc = rt.recv();
    assert_eq!(
        (&svc["t"], &svc["op"]),
        (&json!("svc"), &json!("files.list"))
    );
    rt.send(json!({"t": "svc_result", "id": svc["id"], "ok": true, "result": {"bytes": 1}}));
    let svc = rt.recv();
    rt.send(json!({"t": "svc_result", "id": svc["id"], "ok": false, "error": {"code": "bad_request", "message": "no"}}));
    assert_eq!(rt.recv()["data"], json!([{"bytes": 1}, "bad_request"]));
}

#[test]
fn cancel_sets_the_flag() {
    let mut app = App::new();
    app.method("a.slow", |ctx, _| {
        for _ in 0..200 {
            if ctx.cancelled() {
                return Ok(true);
            }
            thread::sleep(std::time::Duration::from_millis(10));
        }
        Ok(false)
    });
    let mut rt = Runtime::start(app);
    rt.call("a.slow", "null");
    rt.send(json!({"t": "cancel", "id": "1", "reason": "deadline"}));
    assert_eq!(rt.recv()["data"], true);
}

/// A cancel ends the pending service call with request_canceled, and no
/// service frame is sent after it, as in the Python and Node SDKs.
#[test]
fn cancel_ends_service_calls() {
    let mut app = App::new();
    app.method("a.slow", |ctx, _| {
        let pending = ctx.runtime::<Value>("files.list", &json!({})).unwrap_err();
        let after = ctx.runtime::<Value>("files.list", &json!({})).unwrap_err();
        Ok(json!([pending.code, pending.message, after.code]))
    });
    let mut rt = Runtime::start(app);
    rt.call("a.slow", "null");
    assert_eq!(rt.recv()["t"], json!("svc"));
    rt.send(json!({"t": "cancel", "id": "1", "reason": "deadline"}));
    let result = rt.recv();
    assert_eq!(
        result["t"],
        json!("result"),
        "a svc was sent after the cancel"
    );
    assert_eq!(
        result["data"],
        json!([
            "request_canceled",
            "the request is over",
            "request_canceled"
        ])
    );
}

#[test]
fn runtime_result_keeps_number_literals() {
    #[derive(Debug, serde::Deserialize)]
    struct Size {
        bytes: u64,
    }
    let mut app = App::new();
    app.method("a.size", |ctx, _| {
        let raw: Box<RawValue> = ctx.runtime("files.stat", &json!({}))?;
        let size: Size = ctx.runtime("files.stat", &json!({}))?;
        let bad = ctx.runtime::<Size>("files.stat", &json!({})).unwrap_err();
        Ok(json!([raw.get(), size.bytes, bad.code]))
    });
    let mut rt = Runtime::start(app);
    rt.call("a.size", "null");
    for result in [
        r#"{"bytes":2.50}"#,
        r#"{"bytes":18446744073709551615}"#,
        r#"{"bytes":"x"}"#,
    ] {
        let svc = rt.recv();
        rt.w.write_all(
            format!(
                r#"{{"t":"svc_result","id":{},"ok":true,"result":{result}}}"#,
                svc["id"]
            )
            .as_bytes(),
        )
        .unwrap();
        rt.w.write_all(b"\n").unwrap();
    }
    assert_eq!(
        rt.recv()["data"],
        json!([r#"{"bytes":2.50}"#, 18446744073709551615u64, "internal"])
    );
}

#[test]
fn deadline_is_when_the_time_left_runs_out() {
    let mut app = App::new();
    let sent = std::time::Instant::now();
    app.method("a.when", move |ctx, _| {
        let left = ctx.deadline().duration_since(sent);
        Ok(left > std::time::Duration::ZERO
            && left <= std::time::Duration::from_millis(1000) + sent.elapsed())
    });
    let mut rt = Runtime::start(app);
    rt.call("a.when", "null");
    assert_eq!(rt.recv()["data"], true);
}

#[test]
fn run_needs_the_stdio_channel() {
    assert!(check_mode(Some("stdio".into())).is_ok());
    for other in [None, Some("socket:/tmp/app.sock".into()), Some("".into())] {
        assert_eq!(
            check_mode(other).unwrap_err().to_string(),
            "SYNOPKGLAND_BRIDGE must be stdio; run the App from the synopkgland runtime"
        );
    }
}

#[test]
fn a_service_refusal_passes_through() {
    let mut app = App::new();
    app.method("a.move", |ctx, _| {
        let e = ctx
            .runtime::<Value>("delegate.move", &json!({}))
            .unwrap_err();
        assert_eq!(e.code, refusals::SCOPE_DENIED);
        Err::<(), _>(e)
    });
    let mut rt = Runtime::start(app);
    rt.call("a.move", "null");
    let svc = rt.recv();
    rt.send(json!({"t": "svc_result", "id": svc["id"], "ok": false, "error": {"code": "SCOPE_DENIED", "message": "no"}}));
    assert_eq!(
        rt.recv()["error"],
        json!({"code": "SCOPE_DENIED", "message": "no"})
    );
}

#[test]
fn a_raw_result_keeps_number_literals() {
    let mut app = App::new();
    app.method("a.echo", |_, params| {
        RawValue::from_string(params.get().to_owned()).map_err(|_| Error::bad_request("x"))
    });
    let mut rt = Runtime::start(app);
    rt.call("a.echo", r#"{"n":2.50,"id":18446744073709551616}"#);
    let mut line = String::new();
    rt.r.read_line(&mut line).unwrap();
    assert_eq!(
        line,
        "{\"t\":\"result\",\"id\":\"1\",\"ok\":true,\"data\":{\"n\":2.50,\"id\":18446744073709551616}}\n"
    );
}

#[test]
fn describe_lists_the_sorted_methods() {
    let mut app = App::new();
    app.method("a.two", |_, _| Ok(2))
        .method("a.one", |_, _| Ok(1));
    assert_eq!(
        app.describe(),
        json!({"sdk": concat!("rust/", env!("CARGO_PKG_VERSION")), "versions": [1], "methods": ["a.one", "a.two"]})
    );
}

#[test]
fn only_a_lone_describe_flag_describes() {
    assert!(describing(["app", "--describe"].map(OsString::from)));
    for args in [&["app"][..], &["app", "--describe", "x"], &["app", "-d"]] {
        assert!(!describing(args.iter().map(OsString::from)), "{args:?}");
    }
}
