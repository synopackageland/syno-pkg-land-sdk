//! Thin SDK for Rust Apps behind the synopkgland Go runtime.
//!
//! The Go runtime checks the request's origin, the DSM session, the entry
//! gate and the route policy (channel `spec/bridge/v1`); a handler here only
//! sees calls that passed them, and asks the runtime for services with
//! [`Context::runtime`]. Standard library plus serde_json; see README.md.

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::value::RawValue;
use std::collections::{BTreeMap, HashMap};
use std::ffi::OsString;
use std::io::{self, BufRead, BufReader, Read, Write};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, mpsc};
use std::thread;
use std::time::{Duration, Instant};

pub mod refusals;

pub const VERSION: u64 = 1;
const SDK: &str = concat!("rust/", env!("CARGO_PKG_VERSION"));

/// An error the browser may see: a stable code and a message.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct Error {
    pub code: String,
    pub message: String,
}

impl Error {
    pub fn new(code: &str, message: &str) -> Self {
        Error {
            code: code.into(),
            message: message.into(),
        }
    }

    pub fn bad_request(message: &str) -> Self {
        Error::new("bad_request", message)
    }
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.message)
    }
}

impl std::error::Error for Error {}

/// The request's `params`, as the browser sent them.
pub struct Params(Box<RawValue>);

impl Params {
    /// The raw JSON text.
    pub fn get(&self) -> &str {
        self.0.get()
    }

    /// Decodes the params; a mismatch is `bad_request`.
    pub fn decode<T: DeserializeOwned>(&self) -> Result<T, Error> {
        serde_json::from_str(self.0.get()).map_err(|_| Error::bad_request("invalid params"))
    }
}

/// What the runtime verified about the call, and the way back to it.
pub struct Context {
    pub user: String,
    pub admin: bool,
    pub locale: String,
    pub request_id: String,
    /// Time left when the runtime sent the call.
    pub deadline_ms: u64,
    deadline: Instant,
    cancelled: Arc<AtomicBool>,
    channel: Arc<Channel>,
    batch_id: Option<usize>,
}

impl Context {
    /// When the request's time runs out; the runtime answers
    /// `request_timeout` then and sends `cancel`.
    pub fn deadline(&self) -> Instant {
        self.deadline
    }

    /// Whether the runtime said the request is over (deadline or browser gone).
    pub fn cancelled(&self) -> bool {
        self.cancelled.load(Ordering::SeqCst)
    }

    /// Asks the runtime for a service and decodes its result as `T`:
    /// `Box<RawValue>` keeps the JSON text and its number literals, `Value`
    /// any shape. A refusal is the runtime's code; a result that is not a `T`
    /// is `internal`.
    pub fn runtime<T: DeserializeOwned>(
        &self,
        op: &str,
        params: &impl Serialize,
    ) -> Result<T, Error> {
        let raw = self.channel.service(op, params, self.batch_id)?;
        serde_json::from_str(raw.get()).map_err(|e| {
            eprintln!("{op}: unexpected service result: {e}");
            Error::new("internal", "internal error")
        })
    }
}

#[derive(serde::Deserialize)]
struct CallContext {
    user: String,
    admin: bool,
    locale: String,
    request_id: String,
    deadline_ms: u64,
}

#[derive(serde::Deserialize)]
struct Frame {
    #[serde(default)]
    batch: Option<u64>,
    #[serde(default)]
    v: Option<u64>,
    #[serde(default)]
    calls: Vec<Frame>,
    t: String,
    #[serde(default)]
    id: Option<String>,
    #[serde(default)]
    versions: Vec<u64>,
    #[serde(default)]
    method: Option<String>,
    #[serde(default)]
    params: Option<Box<RawValue>>,
    #[serde(default)]
    ctx: Option<CallContext>,
    #[serde(default)]
    ok: Option<bool>,
    #[serde(default)]
    result: Option<Box<RawValue>>,
    #[serde(default)]
    error: Option<Error>,
}

type Reply = Result<Box<RawValue>, Error>;

struct Channel {
    w: Mutex<Box<dyn Write + Send>>,
    /// The calls waiting for a service; None once the request is over, so no
    /// service is asked after a cancel.
    pending: Mutex<Option<HashMap<String, mpsc::Sender<Reply>>>>,
    next: AtomicU64,
}

fn canceled() -> Error {
    Error::new("request_canceled", "the request is over")
}

impl Channel {
    fn send(&self, line: &str) -> io::Result<()> {
        let mut w = self.w.lock().unwrap_or_else(|e| e.into_inner());
        w.write_all(line.as_bytes())?;
        w.write_all(b"\n")?;
        w.flush()
    }

    fn service(&self, op: &str, params: &impl Serialize, batch_id: Option<usize>) -> Reply {
        let id = format!("s{}", self.next.fetch_add(1, Ordering::SeqCst) + 1);
        let (tx, rx) = mpsc::channel();
        let mut frame = serde_json::json!({"t": "svc", "id": id, "op": op, "params": params});
        if let Some(index) = batch_id {
            frame["batch_id"] = index.into();
        }
        {
            let mut pending = self.pending.lock().unwrap();
            pending.as_mut().ok_or_else(canceled)?.insert(id, tx);
            // Sent under the lock: a cancel read meanwhile waits, so it
            // always finds this call pending.
            self.send(&frame.to_string()).map_err(|_| canceled())?;
        }
        rx.recv().unwrap_or_else(|_| Err(canceled()))
    }

    /// Routes frames that arrive while the handler runs.
    fn read_rest(&self, mut r: impl BufRead, cancelled: &AtomicBool) {
        let mut line = String::new();
        while r.read_line(&mut line).is_ok_and(|n| n > 0) {
            if let Ok(f) = serde_json::from_str::<Frame>(&line) {
                match f.t.as_str() {
                    "svc_result" => {
                        let tx =
                            f.id.and_then(|id| self.pending.lock().unwrap().as_mut()?.remove(&id));
                        if let Some(tx) = tx {
                            let reply = match (f.ok, f.error) {
                                (Some(true), _) => Ok(f.result.unwrap_or_else(null)),
                                (_, Some(e)) => Err(e),
                                _ => Err(Error::new("internal", "malformed service reply")),
                            };
                            let _ = tx.send(reply);
                        }
                    }
                    "cancel" => {
                        cancelled.store(true, Ordering::SeqCst);
                        self.end();
                    }
                    _ => {}
                }
            }
            line.clear();
        }
        cancelled.store(true, Ordering::SeqCst);
        self.end();
    }

    /// Ends the waiting service calls with request_canceled and refuses later ones.
    fn end(&self) {
        let pending = self.pending.lock().unwrap().take();
        for tx in pending.into_iter().flat_map(|p| p.into_values()) {
            let _ = tx.send(Err(canceled()));
        }
    }
}

fn null() -> Box<RawValue> {
    RawValue::from_string("null".into()).unwrap()
}

type Handler = Box<dyn Fn(&Context, &Params) -> Result<Box<RawValue>, Error> + Send + Sync>;

/// The App: its methods, served on one channel per call.
#[derive(Default)]
pub struct App {
    methods: BTreeMap<String, Handler>,
}

impl App {
    pub fn new() -> Self {
        Self::default()
    }

    /// Registers a method. Its result is any `Serialize` value.
    pub fn method<T, F>(&mut self, name: &str, f: F) -> &mut Self
    where
        T: Serialize,
        F: Fn(&Context, &Params) -> Result<T, Error> + Send + Sync + 'static,
    {
        let handler: Handler = Box::new(move |ctx, params| {
            let out = f(ctx, params)?;
            serde_json::value::to_raw_value(&out)
                .map_err(|_| Error::new("internal", "internal error"))
        });
        assert!(
            self.methods.insert(name.into(), handler).is_none(),
            "method registered twice: {name}"
        );
        self
    }

    /// Answers one call on a channel.
    pub fn serve(&self, r: impl Read + Send + 'static, w: impl Write + Send + 'static) {
        let mut r = BufReader::new(r);
        let channel = Arc::new(Channel {
            w: Mutex::new(Box::new(w)),
            pending: Mutex::new(Some(HashMap::new())),
            next: AtomicU64::new(0),
        });
        let mut read = || -> Option<Frame> {
            let mut line = String::new();
            r.read_line(&mut line).ok().filter(|n| *n > 0)?;
            serde_json::from_str(&line).ok()
        };
        let Some(hello) = read().filter(|f| f.t == "hello" && f.versions.contains(&VERSION)) else {
            return;
        };
        let batch = hello.batch == Some(1);
        let methods: Vec<&String> = self.methods.keys().collect();
        let mut hello =
            serde_json::json!({"t": "hello", "version": VERSION, "sdk": SDK, "methods": methods});
        if batch {
            hello["batch"] = 1.into();
        }
        if channel.send(&hello.to_string()).is_err() {
            return;
        }
        let Some(request) = read() else {
            return;
        };
        let calls = if batch {
            if request.t != "batch"
                || request.v != Some(1)
                || request.calls.is_empty()
                || request.calls.len() > 8
            {
                return;
            }
            request.calls
        } else {
            vec![request]
        };
        let received = Instant::now();
        let cancelled = Arc::new(AtomicBool::new(false));
        {
            let (channel, cancelled) = (channel.clone(), cancelled.clone());
            thread::spawn(move || channel.read_rest(r, &cancelled));
        }
        for (index, call) in calls.into_iter().enumerate() {
            let Frame {
                t,
                id: Some(id),
                method: Some(method),
                params,
                ctx: Some(c),
                ..
            } = call
            else {
                return;
            };
            if t != "call" {
                return;
            }
            let ctx = Context {
                user: c.user,
                admin: c.admin,
                locale: c.locale,
                request_id: c.request_id,
                deadline_ms: c.deadline_ms,
                deadline: received + Duration::from_millis(c.deadline_ms),
                cancelled: cancelled.clone(),
                channel: channel.clone(),
                batch_id: batch.then_some(index),
            };
            let params = Params(params.unwrap_or_else(null));
            let outcome = match self.methods.get(&method) {
                Some(h) => catch_unwind(AssertUnwindSafe(|| h(&ctx, &params)))
                    .unwrap_or_else(|_| Err(Error::new("internal", "internal error"))),
                None => Err(Error::new("internal", "internal error")),
            };
            let id = serde_json::to_string(&id).unwrap();
            let frame = match outcome {
                Ok(data) => format!(
                    r#"{{"t":"result","id":{id},"ok":true,"data":{}}}"#,
                    data.get()
                ),
                Err(e) => format!(
                    r#"{{"t":"result","id":{id},"ok":false,"error":{}}}"#,
                    serde_json::to_string(&e).unwrap()
                ),
            };
            let _ = channel.send(&frame);
        }
    }

    /// What `--describe` prints: the App's methods, for the package build.
    pub fn describe(&self) -> serde_json::Value {
        let methods: Vec<&String> = self.methods.keys().collect();
        serde_json::json!({"sdk": SDK, "versions": [VERSION], "methods": methods})
    }

    /// Answers the one call the runtime sends on stdin (`SYNOPKGLAND_BRIDGE=stdio`).
    /// With `--describe`, prints `describe()` as one JSON line instead.
    pub fn run(self) -> io::Result<()> {
        if describing(std::env::args_os()) {
            return writeln!(io::stdout(), "{}", self.describe());
        }
        // SAFETY: geteuid has no memory effects.
        if unsafe { libc::geteuid() } == 0 {
            return Err(io::Error::other("refusing to run as root"));
        }
        check_mode(std::env::var_os("SYNOPKGLAND_BRIDGE"))?;
        self.serve(io::stdin(), io::stdout());
        Ok(())
    }
}

/// The arguments are exactly `--describe`.
fn describing(args: impl IntoIterator<Item = OsString>) -> bool {
    let args: Vec<OsString> = args.into_iter().skip(1).collect();
    args == ["--describe"]
}

/// v1 has one channel mode: the call on stdin and stdout.
fn check_mode(mode: Option<OsString>) -> io::Result<()> {
    if mode.is_some_and(|m| m == "stdio") {
        return Ok(());
    }
    Err(io::Error::other(
        "SYNOPKGLAND_BRIDGE must be stdio; run the App from the synopkgland runtime",
    ))
}

#[cfg(test)]
mod tests;
