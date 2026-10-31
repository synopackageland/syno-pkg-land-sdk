package supervision

import (
	"os/exec"
	"testing"
	"time"
)

func TestOwnedProcessReapedAndStopped(t *testing.T) {
	p, err := Start(exec.Command("sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Alive() {
		t.Fatal("not alive")
	}
	if err := p.Stop(time.Millisecond * 50); err != nil {
		t.Fatal(err)
	}
	if p.Alive() {
		t.Fatal("still alive")
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("not reaped")
	}
	if err := p.Stop(time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
