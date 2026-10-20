# An App run as the runtime runs it, for test_run.py.
from synopkgland_app import App

app = App()


@app.method("a.noisy")
def noisy(ctx, params):
    print("not a frame")
    return "ok"


@app.method("a.quiet")
def quiet(ctx, params):
    return params


app.run()
