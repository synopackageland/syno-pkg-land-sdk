package main

import (
	"testing"
	"time"
)

func TestPurgeTakesTheUninstallAnswerAndWaitsForTheRootStep(t *testing.T) {
	for _, c := range []struct {
		args        []string
		deleteState bool
		upgrade     bool
		wait        time.Duration
	}{
		{[]string{"stop", "--package", "TestApp"}, false, false, 60 * time.Second},
		{[]string{"stop", "--package", "TestApp", "--upgrade"}, false, true, 60 * time.Second},
		{[]string{"start", "--package", "TestApp"}, false, false, 60 * time.Second},
		{[]string{"purge", "--package", "TestApp"}, false, false, 360 * time.Second},
		{[]string{"purge", "--package", "TestApp", "--delete-state"}, true, false, 360 * time.Second},
	} {
		r, err := parse(c.args)
		if err != nil || r.cfg.Package != "TestApp" || r.action != c.args[0] || r.cfg.DeleteState != c.deleteState || r.cfg.Upgrade != c.upgrade || r.wait != c.wait {
			t.Fatal(c.args, r, err)
		}
	}
	for _, args := range [][]string{{}, {"stop", "--package", "TestApp", "--delete-state"}, {"purge", "--package", "TestApp", "extra"}, {"purge", "--package", "TestApp", "--upgrade"}} {
		if _, err := parse(args); err == nil {
			t.Fatal(args, "accepted")
		}
	}
}
