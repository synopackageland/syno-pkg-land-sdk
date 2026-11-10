package dsmverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestTransientEvidence(t *testing.T) {
	for _, tc := range []struct {
		err   error
		class string
	}{
		{nil, ""}, {context.Canceled, ""}, {context.DeadlineExceeded, "timeout"},
		{&net.DNSError{IsTimeout: true}, "timeout"},
		{syscall.ECONNREFUSED, "connection"}, {syscall.ECONNRESET, "connection"}, {syscall.EPIPE, "connection"},
		{syscall.EAGAIN, "busy"}, {syscall.EBUSY, "busy"}, {syscall.ENOMEM, "busy"}, {syscall.EMFILE, "busy"}, {syscall.ENFILE, "busy"}, {syscall.EINTR, "busy"},
		{io.ErrUnexpectedEOF, "truncated"}, {io.EOF, "truncated"},
		{os.ErrPermission, ""}, {os.ErrNotExist, ""}, {errors.New("busy 503 timeout"), ""}, {&json.SyntaxError{}, ""},
	} {
		for _, wrapped := range []bool{false, true} {
			err := tc.err
			if wrapped && err != nil {
				err = fmt.Errorf("opaque: %w", err)
			}
			if got := Class(err); got != tc.class {
				t.Errorf("%T: %q want %q", err, got, tc.class)
			}
		}
	}
}
