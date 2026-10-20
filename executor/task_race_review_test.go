package main

import (
	"context"
	"strings"
	"testing"
)

type reviewEditDuringDelete struct {
	schedulerFixture
	calls []string
}

func (s *reviewEditDuringDelete) Exec(ctx context.Context, path string, args []string) ([]byte, error) {
	for _, a := range args {
		if strings.HasPrefix(a, "method=") {
			s.calls = append(s.calls, a)
		}
	}
	if contains(args, "method=delete") {
		s.changed = true
	}
	return s.schedulerFixture.Exec(ctx, path, args)
}
func TestReviewPurgeAcceptedAdminRaceLogsRemainingTasksWithoutRetry(t *testing.T) {
	cmd := &reviewEditDuringDelete{}
	inv := taskInventory{Package: "TestApp", PolicyDigest: strings.Repeat("b", 64), Tasks: []approvedTask{{Kind: "boot", Name: "TestApp executor boot", ScriptDigest: canonicalTaskDigest(t, "boot")}, {Kind: "control", Name: "TestApp executor control", ScriptDigest: canonicalTaskDigest(t, "control")}}}
	logs := []string{}
	purgeTasks(context.Background(), &liveDSM{Command: cmd}, "TestApp", inv, func(s string) { logs = append(logs, s) })
	if len(cmd.deleted) != 1 {
		t.Fatal("must delete immediately once, skip subsequent changed definition", cmd.deleted)
	}
	found := false
	for _, s := range logs {
		if strings.Contains(s, "boot") && strings.Contains(s, "remains") {
			found = true
		}
	}
	if !found {
		t.Fatal("remaining replaced boot task not logged", logs)
	}
	for i, c := range cmd.calls {
		if c == "method=delete" && (i == 0 || cmd.calls[i-1] != "method=get" || i+1 >= len(cmd.calls) || cmd.calls[i+1] != "method=get") {
			t.Fatal("expected get/delete/readback without retry", cmd.calls)
		}
	}
}
