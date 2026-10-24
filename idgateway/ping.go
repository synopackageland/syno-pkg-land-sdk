package idgateway

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// PingPath identifies a package for routing diagnostics. It is public metadata,
// not proof of ownership, and is answered before HTTP authority checks. Any
// origin may read it without credentials: the DSM menu launcher probes it from
// the DSM origin before opening an external App address.
const PingPath = "/_spl/ping"

func servePackagePing(w http.ResponseWriter, r *http.Request, pkg string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := json.Marshal(struct {
		Package string `json:"package"`
	}{pkg})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
