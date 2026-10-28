package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Destination struct {
	Revision   string
	Channel    string // webhook or email
	Endpoint   string // HTTPS without credentials, query or fragment
	Profile    string // generic-json-v1 (or empty), synology-chat-v1
	SecretRef  string
	SMTPHost   string
	SMTPPort   int
	Encryption string // tls or starttls
	From       string
	Username   string
	Recipients []string
}

func (d Destination) Validate() error {
	bad := func() error { return &DeliveryError{Code: "invalid-destination"} }
	if d.Revision == "" || len(d.Revision) > 200 {
		return bad()
	}
	switch d.Channel {
	case "webhook":
		u, err := url.Parse(d.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
			return bad()
		}
		if d.Profile != "" && d.Profile != "generic-json-v1" && d.Profile != "synology-chat-v1" {
			return bad()
		}
		if d.Profile == "synology-chat-v1" && d.SecretRef == "" {
			return bad()
		}
	case "email":
		if d.SMTPHost == "" || strings.ContainsAny(d.SMTPHost, "\r\n /:@") || d.SMTPPort < 1 || d.SMTPPort > 65535 || (d.Encryption != "tls" && d.Encryption != "starttls") || len(d.Recipients) == 0 || len(d.Recipients) > 100 {
			return bad()
		}
		seen := map[string]bool{}
		for _, address := range append([]string{d.From}, d.Recipients...) {
			a, err := mail.ParseAddress(address)
			if err != nil || a.Address != address || strings.ContainsAny(address, "\r\n") {
				return bad()
			}
		}
		for _, recipient := range d.Recipients {
			if seen[recipient] {
				return bad()
			}
			seen[recipient] = true
		}
		if d.Username != "" && d.SecretRef == "" {
			return bad()
		}
	default:
		return bad()
	}
	return nil
}

// Sender performs one bounded attempt. Persist the Message and prior recipient
// results in the App's outbox; reuse both across retry/restart.
type Sender struct {
	Destination Destination
	Secrets     Secrets
	Prior       map[string]string
	// A trusted App may supply its own TLS transport; redirects are still refused.
	Transport http.RoundTripper
}

// SMTPMessageID maps any valid delivery identity to a stable RFC message ID.
func SMTPMessageID(id string) string {
	return fmt.Sprintf("<%x@notifications.invalid>", sha256.Sum256([]byte(id)))
}

func (s Sender) Notify(ctx context.Context, m Message) (Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	r := Receipt{DeliveryID: m.DeliveryID, State: "failed", Recipients: map[string]string{}}
	if r.DeliveryID == "" {
		var err error
		r.DeliveryID, err = NewDeliveryID()
		if err != nil {
			return r, err
		}
		m.DeliveryID = r.DeliveryID
	}
	if err := s.Destination.Validate(); err != nil {
		return r, err
	}
	if !deliveryID.MatchString(m.DeliveryID) || m.Channel != s.Destination.Channel || m.DestinationRevision != s.Destination.Revision || len(m.Payload) > 1<<20 || !json.Valid(m.Payload) {
		return r, &DeliveryError{Code: "invalid-message"}
	}
	secret := ""
	if s.Destination.SecretRef != "" {
		if s.Secrets == nil {
			return r, &DeliveryError{Code: "secret-unavailable", Retryable: true}
		}
		var err error
		secret, err = s.Secrets.Resolve(ctx, s.Destination.SecretRef)
		if err != nil {
			return r, &DeliveryError{Code: "secret-unavailable", Retryable: true}
		}
	}
	if m.Channel == "webhook" {
		return s.webhook(ctx, m, secret, r)
	}
	return s.email(ctx, m, secret, r)
}

func (s Sender) webhook(ctx context.Context, m Message, secret string, r Receipt) (Receipt, error) {
	endpoint := s.Destination.Endpoint
	var body io.Reader = bytes.NewReader(m.Payload)
	contentType := "application/json"
	if s.Destination.Profile == "synology-chat-v1" {
		u, _ := url.Parse(endpoint)
		q := url.Values{"api": {"SYNO.Chat.External"}, "method": {"incoming"}, "version": {"1"}, "token": {secret}}
		u.RawQuery = q.Encode()
		endpoint = u.String()
		chat, _ := json.Marshal(map[string]string{"text": string(m.Payload)})
		body = strings.NewReader(url.Values{"payload": {string(chat)}}.Encode())
		contentType = "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return r, &DeliveryError{Code: "invalid-webhook"}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Synopkgland-Delivery-ID", m.DeliveryID)
	req.Header.Set("Idempotency-Key", m.DeliveryID)
	if secret != "" && s.Destination.Profile != "synology-chat-v1" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	transport := s.Transport
	if transport == nil {
		transport = http.DefaultTransport
		if base, ok := transport.(*http.Transport); ok {
			t := base.Clone()
			defer t.CloseIdleConnections()
			transport = t
		}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		r.PossiblyDuplicate = true
		return r, &DeliveryError{Code: "webhook-transport", Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
		// A timeout/server failure can follow a receiver's side effect.
		r.PossiblyDuplicate = resp.StatusCode == 408 || resp.StatusCode >= 500
		return r, &DeliveryError{Code: "webhook-http-" + strconv.Itoa(resp.StatusCode), Retryable: retry, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	if s.Destination.Profile == "synology-chat-v1" {
		var reply struct {
			Success bool `json:"success"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 65537)).Decode(&reply); err != nil {
			r.PossiblyDuplicate = true
			return r, &DeliveryError{Code: "webhook-receipt", Retryable: true}
		}
		if !reply.Success {
			return r, &DeliveryError{Code: "chat-refused"}
		}
	}
	r.State = "handed-off"
	return r, nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

func (s Sender) email(ctx context.Context, m Message, secret string, r Receipt) (Receipt, error) {
	d := s.Destination
	var pending []string
	for _, recipient := range d.Recipients {
		state := s.Prior[recipient]
		switch state {
		case "", "temporary-failure":
			pending = append(pending, recipient)
		case "accepted", "permanent-failure":
		default:
			return r, &DeliveryError{Code: "invalid-recipient-state"}
		}
		if state != "" {
			r.Recipients[recipient] = state
		}
	}
	if len(pending) == 0 {
		return recipientResult(r)
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(d.SMTPHost, strconv.Itoa(d.SMTPPort)))
	if err != nil {
		return r, &DeliveryError{Code: "smtp-connect", Retryable: true}
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	tlsConfig := &tls.Config{ServerName: d.SMTPHost, MinVersion: tls.VersionTLS12}
	if d.Encryption == "tls" {
		secure := tls.Client(conn, tlsConfig)
		if err = secure.HandshakeContext(ctx); err != nil {
			return r, &DeliveryError{Code: "smtp-tls", Retryable: true}
		}
		// The cancellation callback closes the original connection as well.
		connForSMTP := net.Conn(secure)
		return s.smtp(ctx, connForSMTP, tlsConfig, m, secret, r, pending)
	}
	return s.smtp(ctx, conn, tlsConfig, m, secret, r, pending)
}

func (s Sender) smtp(_ context.Context, conn net.Conn, tlsConfig *tls.Config, m Message, secret string, r Receipt, pending []string) (Receipt, error) {
	d := s.Destination
	client, err := smtp.NewClient(conn, d.SMTPHost)
	if err != nil {
		return r, smtpFailure(err, "smtp-greeting")
	}
	defer client.Close()
	if d.Encryption == "starttls" {
		if err = client.StartTLS(tlsConfig); err != nil {
			return r, smtpFailure(err, "smtp-starttls")
		}
	}
	if d.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", d.Username, secret, d.SMTPHost)); err != nil {
			return r, smtpFailure(err, "smtp-auth")
		}
	}
	if err = client.Mail(d.From); err != nil {
		return r, smtpFailure(err, "smtp-sender")
	}
	var accepted []string
	for _, recipient := range pending {
		if err = client.Rcpt(recipient); err != nil {
			failure := smtpFailure(err, "smtp-recipient")
			state := "permanent-failure"
			if failure.Retryable {
				state = "temporary-failure"
			}
			r.Recipients[recipient] = state
		} else {
			accepted = append(accepted, recipient)
		}
	}
	if len(accepted) > 0 {
		writer, err := client.Data()
		if err != nil {
			return r, smtpFailure(err, "smtp-data")
		}
		_, err = fmt.Fprintf(writer, "From: %s\r\nTo: undisclosed-recipients:;\r\nMessage-ID: %s\r\nSubject: App notification\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", d.From, SMTPMessageID(m.DeliveryID), m.Payload)
		if err != nil {
			r.PossiblyDuplicate = true
			return r, &DeliveryError{Code: "smtp-write", Retryable: true}
		}
		if err = writer.Close(); err != nil {
			failure := smtpFailure(err, "smtp-accept")
			_, definite := err.(*textproto.Error)
			r.PossiblyDuplicate = !definite
			return r, failure
		}
		for _, recipient := range accepted {
			r.Recipients[recipient] = "accepted"
		}
	}
	_ = client.Quit()
	return recipientResult(r)
}

func recipientResult(r Receipt) (Receipt, error) {
	temporary, permanent := false, false
	for _, state := range r.Recipients {
		temporary = temporary || state == "temporary-failure"
		permanent = permanent || state == "permanent-failure"
	}
	if temporary {
		return r, &DeliveryError{Code: "smtp-recipient-temporary", Retryable: true}
	}
	if permanent {
		return r, &DeliveryError{Code: "smtp-recipient-rejected"}
	}
	r.State = "handed-off"
	return r, nil
}

func smtpFailure(err error, code string) *DeliveryError {
	retry := true
	if e, ok := err.(*textproto.Error); ok {
		retry = e.Code >= 400 && e.Code < 500
	}
	return &DeliveryError{Code: code, Retryable: retry}
}
