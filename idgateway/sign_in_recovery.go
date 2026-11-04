package idgateway

import "net/http"

const recoveryTxnPrefix = "retry_"

func (f *Front) recoverSignIn(w http.ResponseWriter, r *http.Request, origin string, t txn, code string) {
	if !isNavigation(r) {
		f.writeFailure(w, r, code, "", false)
		return
	}
	if t.returnTo == "" {
		t.returnTo = f.route("/")
	}
	if !t.recovery {
		t.recovery = true
		f.bounce(w, r, t, origin)
		return
	}
	// An explicit retry starts a new attempt. Do not reuse the assertion or
	// callback query, and retain a target only when takeTxn verified its owner.
	t.recovery = false
	f.writeFailure(w, r, code, t.returnTo, true, t)
}
