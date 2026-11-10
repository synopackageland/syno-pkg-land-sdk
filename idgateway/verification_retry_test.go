package idgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
)

type verifyTransport func(*http.Request) (*http.Response, error)

func (f verifyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func retryFront(t *testing.T, role admissionRoleFunc) *Front {
	t.Helper()
	f, clock := newFront(t, ModeNone, &upstream{Server: &httptest.Server{URL: "http://127.0.0.1:18081"}}, func(c *FrontConfig) { c.Access = "dsm-admins"; c.Membership = role })
	f.sessions["session"] = session{subject: alice, ip: "192.0.2.7", expires: clock.now().Add(time.Hour)}
	f.proxy.Transport = verifyTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("app")), Request: r}, nil
	})
	return f
}
func retryPage(f *Front, ctx context.Context) *httptest.ResponseRecorder {
	r := admissionRequest(f, ctx)
	r.URL.Path = "/"
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Sec-Fetch-Dest", "document")
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

func TestVerificationTransientThenSuccessServesPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		f := retryFront(t, func(context.Context, string) (bool, error) {
			calls++
			if calls < 3 {
				return false, fmt.Errorf("private credentials: %w", syscall.ECONNRESET)
			}
			return true, nil
		})
		var logs strings.Builder
		old := log.Writer()
		log.SetOutput(&logs)
		defer log.SetOutput(old)
		if w := retryPage(f, context.Background()); w.Code != 200 || w.Body.String() != "app" || calls != 3 {
			t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body)
		}
		if strings.Contains(logs.String(), "private credentials") || strings.Count(logs.String(), "class=connection") != 2 || !strings.Contains(logs.String(), "attempt=2") {
			t.Fatalf("retry diagnostics: %s", logs.String())
		}
	})
}
func TestVerificationTransientBudgetReturnsExisting503(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := retryFront(t, func(context.Context, string) (bool, error) { calls.Add(1); return false, syscall.ECONNREFUSED })
		started := time.Now()
		w := retryPage(f, context.Background())
		if w.Code != 503 || !strings.Contains(w.Body.String(), "dsm_verification_failed") || time.Since(started) != runtimebudget.Operation || calls.Load() < 2 {
			t.Fatalf("status=%d calls=%d elapsed=%s", w.Code, calls.Load(), time.Since(started))
		}
	})
}
func TestVerificationDefinitiveNeverRetries(t *testing.T) {
	for _, answer := range []error{api.ErrAdminRequired, ErrGroupRequired, api.ErrAppPrivilegeDenied, errors.New("unknown verification failure")} {
		t.Run(answer.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				f := retryFront(t, func(context.Context, string) (bool, error) { calls++; return false, answer })
				start := time.Now()
				w := retryPage(f, context.Background())
				if calls != 1 || w.Code == 200 || time.Since(start) != 0 {
					t.Fatalf("status=%d calls=%d elapsed=%s", w.Code, calls, time.Since(start))
				}
			})
		})
	}
}
func TestVerificationCancelStopsBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := retryFront(t, func(context.Context, string) (bool, error) { calls.Add(1); return false, syscall.ECONNRESET })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { retryPage(f, ctx); close(done) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("transient failure returned before cancellation")
		default:
		}
		cancel()
		synctest.Wait()
		<-done
		before := calls.Load()
		time.Sleep(runtimebudget.Operation)
		synctest.Wait()
		if before != 1 || calls.Load() != before {
			t.Fatalf("retries survived cancellation: %d -> %d", before, calls.Load())
		}
	})
}
func TestVerificationConcurrentRetriesShareOneStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := retryFront(t, func(context.Context, string) (bool, error) {
			if calls.Add(1) == 1 {
				return false, syscall.ECONNRESET
			}
			return true, nil
		})
		done := make(chan int, 20)
		for range 20 {
			go func() { done <- retryPage(f, context.Background()).Code }()
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("parallel attempts=%d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		for range 20 {
			if status := <-done; status != 200 {
				t.Errorf("status=%d", status)
			}
		}
		if calls.Load() != 2 {
			t.Fatalf("streams duplicated: calls=%d", calls.Load())
		}
	})
}
func TestCGIVerificationRetriesBeforeAssertion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		h := newCGI(&fakeDSM{})
		h.Verifier = verifierFunc(func(*http.Request) (api.Identity, error) {
			calls++
			if calls == 1 {
				return api.Identity{}, io.ErrUnexpectedEOF
			}
			return api.Identity{User: "alice", Admin: true}, nil
		})
		w := cgiPost(t, h, cgiQuery("https://nas.example:18443/_sso/dsm-return"), "fixture")
		if calls != 2 || !strings.Contains(w.Header().Get("Location"), "assertion=") {
			t.Fatalf("calls=%d return=%s", calls, w.Header().Get("Location"))
		}
	})
}

func TestCGIDefinitiveVerificationNeverRetries(t *testing.T) {
	for _, answer := range []error{api.ErrNoSession, api.ErrAdminRequired, ErrGroupRequired, api.ErrAppPrivilegeDenied, ErrNoAccount} {
		t.Run(answer.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newCGI(&fakeDSM{})
				calls := 0
				h.Verifier = verifierFunc(func(*http.Request) (api.Identity, error) { calls++; return api.Identity{}, answer })
				start := time.Now()
				w := cgiPost(t, h, cgiQuery("https://nas.example:18443/_sso/dsm-return"), "fixture")
				if calls != 1 || time.Since(start) != 0 || !strings.Contains(w.Header().Get("Location"), "error=") {
					t.Fatalf("calls=%d elapsed=%s", calls, time.Since(start))
				}
			})
		})
	}
}

func TestVerificationSlowAttemptKeepsRemainingBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		start := time.Now()
		f := retryFront(t, func(ctx context.Context, _ string) (bool, error) {
			calls++
			deadline, ok := ctx.Deadline()
			if !ok || deadline.After(start.Add(runtimebudget.Operation)) {
				t.Error("attempt reset the operation allowance")
			}
			time.Sleep(2 * time.Minute)
			if calls == 1 {
				return false, context.DeadlineExceeded
			}
			return true, ctx.Err()
		})
		if w := retryPage(f, context.Background()); w.Code != 200 || calls != 2 || time.Since(start) < 4*time.Minute {
			t.Fatalf("slow attempt: status=%d calls=%d elapsed=%s", w.Code, calls, time.Since(start))
		}
	})
}

func TestVerificationWaiterDeadlineDoesNotStopSurvivor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := retryFront(t, func(context.Context, string) (bool, error) {
			if calls.Add(1) == 1 {
				return false, syscall.ECONNRESET
			}
			return true, nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		first, second := make(chan error, 1), make(chan error, 1)
		go func() { first <- f.admitSession(admissionRequest(f, ctx)) }()
		synctest.Wait()
		go func() { second <- f.admitSession(admissionRequest(f, context.Background())) }()
		synctest.Wait()
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if err := <-first; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if err := <-second; err != nil || calls.Load() != 2 {
			t.Fatalf("surviving waiter: %v calls=%d", err, calls.Load())
		}
	})
}

func TestVerificationNSSRecoveryDoesNotClearSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := retryFront(t, func(context.Context, string) (bool, error) { return true, nil })
		calls := 0
		f.cfg.Accounts = admissionNamesFunc(func(context.Context, int) (string, error) {
			calls++
			if calls == 1 {
				return "", syscall.EAGAIN
			}
			return alice.Name, nil
		})
		w := retryPage(f, context.Background())
		if w.Code != 200 || len(f.sessions) != 1 || calls < 2 {
			t.Fatalf("status=%d sessions=%d lookups=%d", w.Code, len(f.sessions), calls)
		}
	})
}

func TestCGIConcurrentVerificationsShareCarriers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCGI(&fakeDSM{})
		var calls atomic.Int32
		h.Verifier = verifierFunc(func(*http.Request) (api.Identity, error) {
			if calls.Add(1) == 1 {
				return api.Identity{}, io.ErrUnexpectedEOF
			}
			return api.Identity{User: "alice", Admin: true}, nil
		})
		done := make(chan *httptest.ResponseRecorder, 12)
		for range 12 {
			go func() { done <- cgiPost(t, h, cgiQuery("https://nas.example:18443/_sso/dsm-return"), "fixture") }()
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("parallel checks=%d", calls.Load())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		for range 12 {
			if w := <-done; !strings.Contains(w.Header().Get("Location"), "assertion=") {
				t.Error("missing assertion")
			}
		}
		if calls.Load() != 2 {
			t.Fatalf("duplicated CGI stream=%d", calls.Load())
		}
	})
}

func TestVerificationDefinitiveAnswerOverridesTransportCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		err := retryVerification(context.Background(), func(context.Context) error {
			calls++
			return errors.Join(api.ErrAppPrivilegeDenied, syscall.ECONNRESET)
		})
		if calls != 1 || !errors.Is(err, api.ErrAppPrivilegeDenied) {
			t.Fatalf("definitive answer retried: calls=%d err=%v", calls, err)
		}
	})
}

func TestVerificationBudgetCancelsSlowNativeAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		f := retryFront(t, func(ctx context.Context, _ string) (bool, error) {
			defer close(done)
			<-ctx.Done()
			return false, ctx.Err()
		})
		start := time.Now()
		w := retryPage(f, context.Background())
		synctest.Wait()
		if w.Code != 503 || time.Since(start) != runtimebudget.Operation {
			t.Fatalf("status=%d elapsed=%s", w.Code, time.Since(start))
		}
		select {
		case <-done:
		default:
			t.Fatal("native attempt survived budget")
		}
	})
}
