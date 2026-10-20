# A minimal App: one method that reads the caller, one that writes a file in
# the package's data directory (SYNOPKGLAND_DATA_DIR, set by the runtime).
import os
from typing import Any, Dict

from synopkgland_app import App, Context, Error

app = App()


@app.method("hello.greet")
def greet(ctx: Context, params: Any) -> Dict[str, str]:
    return {"message": "Hello, %s" % ctx.user}


@app.method("hello.save")
def save(ctx: Context, params: Any) -> Dict[str, int]:
    note = (params or {}).get("note")
    if not isinstance(note, str):
        raise Error.bad_request("note must be a string")
    notes = os.path.join(os.environ["SYNOPKGLAND_DATA_DIR"], "notes")
    os.makedirs(notes, mode=0o700, exist_ok=True)
    content = note.encode()
    with open(os.path.join(notes, ctx.request_id + ".txt"), "xb") as f:
        f.write(content)
    return {"bytes": len(content)}


if __name__ == "__main__":
    app.run()
