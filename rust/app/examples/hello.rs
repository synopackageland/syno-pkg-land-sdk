//! A minimal App: one method that reads the caller, one that writes a file in
//! the package's data directory (`SYNOPKGLAND_DATA_DIR`, set by the runtime).

use serde_json::json;
use std::fs::{DirBuilder, OpenOptions};
use std::io::Write;
use std::os::unix::fs::DirBuilderExt;
use std::path::PathBuf;
use syno_pkg_land_app::{App, Error};

#[derive(serde::Deserialize)]
struct Save {
    note: String,
}

fn main() -> std::io::Result<()> {
    let mut app = App::new();
    app.method("hello.greet", |ctx, _| {
        Ok(json!({"message": format!("Hello, {}", ctx.user)}))
    });
    app.method("hello.save", |ctx, params| {
        let p: Save = params
            .decode()
            .map_err(|_| Error::bad_request("note must be a string"))?;
        let notes = PathBuf::from(std::env::var_os("SYNOPKGLAND_DATA_DIR").unwrap_or_default())
            .join("notes");
        let write = || -> std::io::Result<()> {
            DirBuilder::new()
                .recursive(true)
                .mode(0o700)
                .create(&notes)?;
            OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(notes.join(format!("{}.txt", ctx.request_id)))?
                .write_all(p.note.as_bytes())
        };
        write().map_err(|e| {
            eprintln!("hello.save: {e}");
            Error::new("internal", "internal error")
        })?;
        Ok(json!({"bytes": p.note.len()}))
    });
    app.run()
}
