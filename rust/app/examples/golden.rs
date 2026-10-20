//! The api golden fixture (api/golden_test.go goldenServer) as a Rust App,
//! for bridge/golden_test.go.

use serde_json::value::RawValue;
use syno_pkg_land_app::{App, Context, Error, Params};

#[derive(serde::Serialize)]
struct Echo<'a> {
    params: &'a RawValue,
    user: &'a str,
}

#[derive(serde::Serialize)]
struct Order {
    zeta: &'static str,
    alpha: &'static str,
    empty: Option<Vec<String>>,
}

// The bridge golden fixture (bridge/golden_test.go bridgeGoApp): services
// and transfers.
#[derive(serde::Deserialize)]
struct Svc {
    op: String,
    #[serde(default)]
    params: Option<Box<RawValue>>,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "snake_case")]
enum SvcReply {
    Result(Box<RawValue>),
    Error { code: String, message: String },
}

fn svc(ctx: &Context, p: &Params) -> Result<SvcReply, Error> {
    let p: Svc = serde_json::from_str(p.get()).map_err(|_| Error::bad_request("invalid params"))?;
    Ok(match ctx.runtime::<Box<RawValue>>(&p.op, &p.params) {
        Ok(result) => SvcReply::Result(result),
        Err(e) => SvcReply::Error {
            code: e.code,
            message: e.message,
        },
    })
}

#[derive(serde::Deserialize)]
struct Upload {
    query: Box<RawValue>,
    upload: Spooled,
}

#[derive(serde::Deserialize)]
struct Fetch {
    download: Spooled,
}

#[derive(serde::Deserialize)]
struct Spooled {
    path: String,
    #[serde(default)]
    size: u64,
}

#[derive(serde::Serialize)]
struct Uploaded {
    query: Box<RawValue>,
    size: u64,
    content: String,
}

#[derive(serde::Serialize)]
struct File {
    name: &'static str,
    content_type: &'static str,
}

fn main() -> std::io::Result<()> {
    let mut app = App::new();
    app.method("demo.echo", |ctx, p| {
        let params: &RawValue =
            serde_json::from_str(p.get()).map_err(|_| Error::bad_request("invalid params"))?;
        serde_json::value::to_raw_value(&Echo {
            params,
            user: &ctx.user,
        })
        .map_err(|_| Error::new("internal", "internal error"))
    });
    app.method("demo.order", |_, _| {
        Ok(Order {
            zeta: "<a href=\"x\">&</a>",
            alpha: "\u{2028}\u{2029}\t\u{1}é",
            empty: None,
        })
    });
    app.method("demo.fail", |_, _| {
        Err::<(), _>(Error::new("conflict", "a <b> & c"))
    });
    app.method("demo.nothing", |_, _| Ok(()));
    app.method("demo.admin", |_, _| Ok(true));
    app.method("demo.ping", |_, _| Ok("pong"));
    app.method("demo.svc", svc);
    app.method("demo.svc_public", svc);
    app.method("demo.upload", |_, p| {
        let p: Upload =
            serde_json::from_str(p.get()).map_err(|_| Error::bad_request("invalid params"))?;
        let content = std::fs::read_to_string(&p.upload.path)
            .map_err(|_| Error::new("internal", "internal error"))?;
        Ok(Uploaded {
            query: p.query,
            size: p.upload.size,
            content,
        })
    });
    app.method("demo.fetch", |ctx, p| {
        let p: Fetch =
            serde_json::from_str(p.get()).map_err(|_| Error::bad_request("invalid params"))?;
        std::fs::write(&p.download.path, format!("hello {}", ctx.user))
            .map_err(|_| Error::new("internal", "internal error"))?;
        Ok(File {
            name: "a b.txt",
            content_type: "text/plain; charset=utf-8",
        })
    });
    app.run()
}
