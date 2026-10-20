package main

import (
	"context"
	"testing"
)

func TestCallAsUserRefusesCallsOutsideTheAllowlist(t *testing.T) {
	for _, tc := range [][3]string{
		{"SYNO.Core.User", "get", "1"},
		{"SYNO.Core.Share", "get", "1"},
		{"SYNO.FileStation.List", "list_share", "2"},
		{"SYNO.FileStation.List", "list", "1"},
		{"SYNO.FileStation.Upload", "upload", "2"},
	} {
		c := &cmdStub{reply: `{"success":true,"data":{}}`}
		d := &liveDSM{Command: c}
		if _, err := d.callAsUser(context.Background(), "alice", tc[0], tc[1], tc[2], map[string]any{}); err == nil || c.path != "" {
			t.Errorf("%v ran", tc)
		}
	}
}

func TestCallAsUserRunsAllowlistedCallsAsTheRunner(t *testing.T) {
	for _, tc := range [][3]string{
		{"SYNO.FileStation.List", "list", "2"},
		{"SYNO.FileStation.List", "getinfo", "2"},
		{"SYNO.FileStation.CreateFolder", "create", "2"},
		{"SYNO.FileStation.CheckPermission", "write", "3"},
	} {
		c := &cmdStub{reply: `{"success":true,"data":{"ok":1}}`}
		d := &liveDSM{Command: c}
		if _, err := d.callAsUser(context.Background(), "alice", tc[0], tc[1], tc[2], map[string]any{}); err != nil || !contains(c.args, "runner=alice") {
			t.Errorf("%v: %v %v", tc, err, c.args)
		}
		c.path = ""
		if _, err := d.callAsUser(context.Background(), "", tc[0], tc[1], tc[2], map[string]any{}); err == nil || c.path != "" {
			t.Errorf("%v ran without a runner", tc)
		}
	}
}
