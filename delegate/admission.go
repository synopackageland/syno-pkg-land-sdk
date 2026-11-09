package delegate

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type admissionQueue struct {
	slots chan struct{}
	refs  int
}

// Share admission across separately constructed clients, but never retain idle
// socket entries. Separate processes still contend at the executor and back off.
var admissions = struct {
	sync.Mutex
	sockets map[string]*admissionQueue
}{sockets: make(map[string]*admissionQueue)}

func needsRootSlot(op string) bool {
	return op == "root.run" || op == "root.start" || op == "webapi.call"
}

func acquireAdmission(ctx context.Context, socket string) (func(), error) {
	key := filepath.Clean(socket)
	admissions.Lock()
	q := admissions.sockets[key]
	if q == nil {
		q = &admissionQueue{slots: make(chan struct{}, executorRootSlots)}
		admissions.sockets[key] = q
	}
	q.refs++
	admissions.Unlock()
	forget := func() {
		admissions.Lock()
		q.refs--
		if q.refs == 0 {
			delete(admissions.sockets, key)
		}
		admissions.Unlock()
	}
	select {
	case q.slots <- struct{}{}:
		if ctx.Err() != nil {
			<-q.slots
			forget()
			return nil, fail("canceled")
		}
		return func() { <-q.slots; forget() }, nil
	case <-ctx.Done():
		forget()
		return nil, fail("canceled")
	}
}

// ROOT_BUSY is an explicit refusal before root/WebAPI execution. A protocol
// retryable flag alone is insufficient: ROOT_TIMEOUT/DSM_FAILED, for example,
// can follow execution. Never retry transport errors or uncertain outcomes.
func rootBusy(err error) bool {
	var refusal *api.Error
	return errors.As(err, &refusal) && refusal.Code == CodeRootBusy
}

func waitRootBusy(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fail("canceled")
	case <-timer.C:
		return nil
	}
}
