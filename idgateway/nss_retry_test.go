package idgateway

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNSSRetainsTransientLookupErrors(t *testing.T) {
	passwd := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(passwd, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{syscall.EAGAIN, context.DeadlineExceeded, io.ErrUnexpectedEOF, context.Canceled} {
		d := &Directory{Passwd: passwd, lookup: func(context.Context, ...string) (string, error) { return "", cause }}
		if _, err := d.UID(context.Background(), "alice"); !errors.Is(err, cause) {
			t.Errorf("UID lost %v: %v", cause, err)
		}
		if _, err := d.Name(context.Background(), 1026); !errors.Is(err, cause) {
			t.Errorf("Name lost %v: %v", cause, err)
		}
	}
	d := &Directory{Passwd: passwd, lookup: func(context.Context, ...string) (string, error) { return "", errors.New("not found") }}
	if _, err := d.UID(context.Background(), "alice"); !errors.Is(err, ErrNoAccount) {
		t.Fatal("missing account changed", err)
	}
}
func TestNSSGroupsRetainsTransientAndTruncatedErrors(t *testing.T) {
	for _, cause := range []error{syscall.EAGAIN, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
		d := directoryLookup{timeout: 1 << 62, run: func(context.Context, string, []string, []string) ([]byte, error) { return nil, cause }}
		if _, err := d.groups(context.Background(), "alice"); !errors.Is(err, cause) {
			t.Errorf("Groups lost %v: %v", cause, err)
		}
	}
	d := directoryLookup{timeout: 1 << 62, run: func(context.Context, string, []string, []string) ([]byte, error) {
		return []byte("uid=1026(alice) gid=100(users) groups=100("), nil
	}}
	if _, err := d.groups(context.Background(), "alice"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("truncated groups", err)
	}
}
