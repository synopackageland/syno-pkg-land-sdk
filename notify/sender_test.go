package notify

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
	"time"
)

func webhookMessage() Message {
	return Message{DeliveryID: "fixed-delivery-1", DestinationRevision: "revision-1", Channel: "webhook", Payload: []byte(`{"text":"Example"}`)}
}

func TestWebhookIdentityRetryAndUnknownOutcome(t *testing.T) {
	var ids []string
	status := http.StatusServiceUnavailable
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("Idempotency-Key"))
		if r.Header.Get("X-Synopkgland-Delivery-ID") != ids[len(ids)-1] {
			t.Error("missing SDK delivery header")
		}
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(status)
	}))
	defer srv.Close()
	s := Sender{Destination: Destination{Revision: "revision-1", Channel: "webhook", Endpoint: srv.URL}, Transport: srv.Client().Transport}
	m := webhookMessage()
	r, err := s.Notify(context.Background(), m)
	var failure *DeliveryError
	if !errors.As(err, &failure) || !failure.Retryable || failure.RetryAfter != 2*time.Minute || !r.PossiblyDuplicate || r.DeliveryID != m.DeliveryID {
		t.Fatalf("%+v %v", r, err)
	}
	status = http.StatusNoContent
	r, err = s.Notify(context.Background(), m)
	if err != nil || r.State != "handed-off" || len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("%+v %v %v", r, err, ids)
	}
	s.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })
	r, err = s.Notify(context.Background(), m)
	if err == nil || !r.PossiblyDuplicate || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("%+v %v", r, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebhookNeverFollowsRedirectAndReturnsGeneratedID(t *testing.T) {
	calls := 0
	s := Sender{
		Destination: Destination{Revision: "revision-1", Channel: "webhook", Endpoint: "https://receiver.invalid/"},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://other.invalid/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}),
	}
	m := webhookMessage()
	m.DeliveryID = ""
	r, err := s.Notify(context.Background(), m)
	if err == nil || r.DeliveryID == "" || calls != 1 || r.PossiblyDuplicate {
		t.Fatalf("%+v %v calls=%d", r, err, calls)
	}
}

func TestInvalidDestinationsAndMessagesDoNotReachTransport(t *testing.T) {
	called := false
	s := Sender{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not send")
	})}
	for _, endpoint := range []string{"http://receiver.invalid/", "https://user:secret@receiver.invalid/", "https://receiver.invalid/?secret=value", "https://receiver.invalid/#secret"} {
		s.Destination = Destination{Revision: "revision-1", Channel: "webhook", Endpoint: endpoint}
		if _, err := s.Notify(context.Background(), webhookMessage()); err == nil {
			t.Fatal("accepted invalid destination")
		}
	}
	s.Destination.Endpoint = "https://receiver.invalid/"
	m := webhookMessage()
	m.DeliveryID = "bad\r\nInjected: value"
	if _, err := s.Notify(context.Background(), m); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

type secretFunc func(context.Context, string) (string, error)

func (f secretFunc) Resolve(ctx context.Context, ref string) (string, error) { return f(ctx, ref) }

func TestChatProfileChecksApplicationReceiptAndKeepsSecretsOutOfErrors(t *testing.T) {
	reply := `{"success":true}`
	s := Sender{
		Destination: Destination{Revision: "revision-1", Channel: "webhook", Endpoint: "https://receiver.invalid/", Profile: "synology-chat-v1", SecretRef: "credential-reference"},
		Secrets: secretFunc(func(ctx context.Context, ref string) (string, error) {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 40*time.Second || ref != "credential-reference" {
				t.Fatal("secret resolution is outside the bounded attempt")
			}
			return "fixture-secret", nil
		}),
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("token") != "fixture-secret" || req.URL.Query().Get("method") != "incoming" {
				t.Error("missing Chat protocol parameters")
			}
			body, _ := io.ReadAll(req.Body)
			form, _ := url.ParseQuery(string(body))
			if !strings.Contains(form.Get("payload"), "Example") || req.Header.Get("Authorization") != "" || req.Header.Get("Idempotency-Key") != webhookMessage().DeliveryID {
				t.Error("invalid Chat form or identity")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(reply)), Header: http.Header{}}, nil
		}),
	}
	for _, tc := range []struct {
		body      string
		code      string
		duplicate bool
	}{
		{`{"success":true}`, "", false},
		{`{"success":false}`, "chat-refused", false},
		{`{`, "webhook-receipt", true},
	} {
		reply = tc.body
		r, err := s.Notify(context.Background(), webhookMessage())
		if tc.code == "" {
			if err != nil || r.State != "handed-off" {
				t.Fatalf("%+v %v", r, err)
			}
		} else if err == nil || err.Error() != tc.code || r.PossiblyDuplicate != tc.duplicate {
			t.Fatalf("%+v %v", r, err)
		}
	}
}

// The in-memory SMTP peer exercises DATA and RCPT semantics independently of
// TLS certificate provisioning. Production Sender permits only TLS/STARTTLS.
func smtpAttempt(t *testing.T, prior map[string]string, loseReply bool) (Receipt, error, []string) {
	t.Helper()
	client, peer := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	commands := make(chan []string, 1)
	go func() {
		defer peer.Close()
		reader := bufio.NewReader(peer)
		fmt.Fprint(peer, "220 fixture.invalid ESMTP\r\n")
		var seen []string
		defer func() { commands <- seen }()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			seen = append(seen, line)
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(peer, "250 fixture.invalid\r\n")
			case strings.HasPrefix(line, "MAIL FROM"):
				fmt.Fprint(peer, "250 OK\r\n")
			case strings.Contains(line, "temporary@receiver.invalid"):
				fmt.Fprint(peer, "450 retry recipient\r\n")
			case strings.Contains(line, "rejected@receiver.invalid"):
				fmt.Fprint(peer, "550 no recipient\r\n")
			case strings.HasPrefix(line, "RCPT TO"):
				fmt.Fprint(peer, "250 OK\r\n")
			case line == "DATA":
				fmt.Fprint(peer, "354 send data\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					seen = append(seen, strings.TrimSpace(line))
					if line == ".\r\n" {
						break
					}
				}
				if loseReply {
					return
				}
				fmt.Fprint(peer, "250 queued\r\n")
			case line == "QUIT":
				fmt.Fprint(peer, "221 bye\r\n")
				return
			default:
				fmt.Fprint(peer, "500 unsupported\r\n")
			}
		}
	}()
	s := Sender{
		Destination: Destination{Revision: "revision-1", Channel: "email", SMTPHost: "fixture.invalid", Encryption: "tls", From: "sender@sender.invalid", Recipients: []string{"accepted@receiver.invalid", "temporary@receiver.invalid", "rejected@receiver.invalid"}},
		Prior:       prior,
	}
	r := Receipt{DeliveryID: "fixed-delivery-1", State: "failed", Recipients: map[string]string{}}
	var pending []string
	for _, recipient := range s.Destination.Recipients {
		state := prior[recipient]
		if state == "accepted" || state == "permanent-failure" {
			r.Recipients[recipient] = state
		} else {
			pending = append(pending, recipient)
		}
	}
	r, err := s.smtp(context.Background(), client, &tls.Config{}, Message{DeliveryID: r.DeliveryID, Payload: []byte(`{"text":"Example"}`)}, "", r, pending)
	client.Close()
	return r, err, <-commands
}

func TestSMTPMessageIdentityPartialRecipientsAndRetry(t *testing.T) {
	r, err, commands := smtpAttempt(t, nil, false)
	var failure *DeliveryError
	if !errors.As(err, &failure) || !failure.Retryable || r.Recipients["accepted@receiver.invalid"] != "accepted" || r.Recipients["temporary@receiver.invalid"] != "temporary-failure" || r.Recipients["rejected@receiver.invalid"] != "permanent-failure" {
		t.Fatalf("%+v %v", r, err)
	}
	if !strings.Contains(strings.Join(commands, "\n"), "Message-ID: <de0a52ba1f99bbe526fc7a4d1833ae9dab385662deab89455a4dd6db832d197b@notifications.invalid>") {
		t.Fatal(commands)
	}
	_, _, commands = smtpAttempt(t, r.Recipients, false)
	for _, command := range commands {
		if strings.HasPrefix(command, "RCPT TO") && !strings.Contains(command, "temporary@receiver.invalid") {
			t.Fatalf("retried terminal recipient: %s", command)
		}
	}
}

func TestSMTPLostDataReplyIsPossiblyDuplicate(t *testing.T) {
	r, err, first := smtpAttempt(t, nil, true)
	if err == nil || !r.PossiblyDuplicate || r.Recipients["accepted@receiver.invalid"] == "accepted" {
		t.Fatalf("%+v %v", r, err)
	}
	_, _, retry := smtpAttempt(t, r.Recipients, false)
	id := "Message-ID: <de0a52ba1f99bbe526fc7a4d1833ae9dab385662deab89455a4dd6db832d197b@notifications.invalid>"
	if !strings.Contains(strings.Join(first, "\n"), id) || !strings.Contains(strings.Join(retry, "\n"), id) {
		t.Fatal("SMTP retry changed or omitted the message identity")
	}
	for _, code := range []int{450, 550} {
		failure := smtpFailure(&textproto.Error{Code: code, Msg: "private server detail"}, "smtp-accept")
		if failure.Retryable != (code == 450) || strings.Contains(failure.Error(), "private") {
			t.Fatal(failure)
		}
	}
}

func TestSMTPIdentityEncodesHeaderUnsafePunctuation(t *testing.T) {
	id := SMTPMessageID("incident:transition..retry")
	if strings.Count(id, "@") != 1 || !strings.HasSuffix(id, "@notifications.invalid>") || strings.ContainsAny(id, ": \r\n") || id != SMTPMessageID("incident:transition..retry") || id == SMTPMessageID("another") {
		t.Fatal(id)
	}
}
