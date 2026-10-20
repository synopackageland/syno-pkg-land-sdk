# The api golden fixture (api/golden_test.go goldenServer) as a Python App.
from synopkgland_app import App, Error

app = App()
app.method("demo.echo")(lambda ctx, p: {"params": p, "user": ctx.user})
app.method("demo.order")(lambda ctx, p: {"zeta": '<a href="x">&</a>', "alpha": "\u2028\u2029\t\x01\u00e9", "empty": None})
app.method("demo.nothing")(lambda ctx, p: None)
app.method("demo.admin")(lambda ctx, p: True)
app.method("demo.ping")(lambda ctx, p: "pong")


@app.method("demo.fail")
def fail(ctx, p):
    raise Error("conflict", "a <b> & c")


# The bridge golden fixture (bridge/golden_test.go bridgeGoApp): services
# and transfers.
def svc(ctx, p):
    try:
        return {"result": ctx.runtime(p["op"], p.get("params"))}
    except Error as e:
        return {"error": {"code": e.code, "message": e.message}}


app.method("demo.svc")(svc)
app.method("demo.svc_public")(svc)


@app.method("demo.upload")
def upload(ctx, p):
    with open(p["upload"]["path"], encoding="utf-8") as f:
        return {"query": p["query"], "size": p["upload"]["size"], "content": f.read()}


@app.method("demo.fetch")
def fetch(ctx, p):
    with open(p["download"]["path"], "w", encoding="utf-8") as f:
        f.write("hello " + ctx.user)
    return {"name": "a b.txt", "content_type": "text/plain; charset=utf-8"}


app.run()
