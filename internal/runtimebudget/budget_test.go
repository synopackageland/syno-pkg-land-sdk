package runtimebudget

import (
	"testing"
	"time"
)

func TestExecDeadlineLosesElapsedTimeInsteadOfResetting(t *testing.T) {
	until := time.Now().Add(time.Second)
	token, err := EncodeDeadline(until)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	restored, err := DecodeDeadline(token)
	if err != nil {
		t.Fatal(err)
	}
	if delta := restored.Sub(until); delta < -time.Millisecond || delta > time.Millisecond {
		t.Fatalf("exec reset deadline by %v", delta)
	}
	if Inner(time.Second) >= time.Second || Inner(-time.Second) != 0 {
		t.Fatal("invalid reserve")
	}
}
