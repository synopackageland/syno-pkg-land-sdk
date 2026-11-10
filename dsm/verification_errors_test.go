package dsm

import (
	"context"
	"errors"
	"io"
	"syscall"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/dsmverify"
)

func TestNativeVerificationPreservesTransientEvidence(t *testing.T) {
	for _, cause := range []error{syscall.EAGAIN, syscall.ECONNRESET, context.DeadlineExceeded} {
		_, err := parseNativeRole(nil, cause)
		if !errors.Is(err, cause) || !errors.Is(err, errNativeRole) || dsmverify.Class(err) == "" {
			t.Errorf("lost cause %v: %v", cause, err)
		}
	}
	for _, out := range []string{"", "0", "1"} {
		_, err := parseNativeRole([]byte(out), nil)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("truncated %q: %v", out, err)
		}
	}
	_, err := parseNativeRole([]byte("invalid"), nil)
	if dsmverify.Class(err) != "" {
		t.Fatal("invalid protocol was transient")
	}
}
func TestAuthenticateTimeoutIsNotNoSession(t *testing.T) {
	a := newTestAuth(t, &fakeRunner{})
	a.run = func(ctx context.Context, _ string, _ []string) ([]byte, error) {
		<-ctx.Done()
		return nil, &exitError{}
	}
	a.Timeout = 0
	_, err := a.Authenticate(sessionRequest())
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, api.ErrNoSession) {
		t.Fatalf("timeout treated as session denial: %v", err)
	}
}
