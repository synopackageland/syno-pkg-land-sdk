package idgateway

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
	"time"
)

// GatewayAvailable checks only a local listener. It does not infer DSM sign-in
// from a stopped service, contact an external address or start the application.
func GatewayAvailable(cfg Config) bool {
	network, address := "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Listen.HTTP))
	if cfg.NativeIngress {
		network, address = "unix", nativeSocketPath(cfg.Package)
	} else if cfg.Listen.HTTPS != 0 {
		address = net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Listen.HTTPS))
	}
	c, err := net.DialTimeout(network, address, 250*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func writeAppStopped(w http.ResponseWriter, r *http.Request) {
	locale, m := denialMessages(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", locale)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Accept-Language")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><title>%s</title><p data-error-code="app_not_running">%s</p></html>`, locale, html.EscapeString(m["stopped_title"]), html.EscapeString(m["stopped"]))
}
