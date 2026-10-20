package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTheRootStateLivesBesideThePackageDataOnItsVolume(t *testing.T) {
	for data, want := range map[string]string{
		"/volume1/@appdata/Demo":    "/volume1/@synopkgland/Demo",
		"/volume12/@appdata/Demo":   "/volume12/@synopkgland/Demo",
		"/volume1/@appdata/Other":   "",
		"/volume1/x/@appdata/Demo":  "",
		"/var/packages/Demo/var":    "",
		"/volumeUSB1/@appdata/Demo": "",
		"/volume1/@appdata/Demo/":   "",
	} {
		got, err := rootStateFor(data, "Demo")
		if got != want || (want == "") != (err != nil) {
			t.Error(data, got, err)
		}
	}
}

func TestRootProgramsSeeTheRootState(t *testing.T) {
	e, _, r, _ := rootSetup(t, `{"probe":"`+digestA+`"}`)
	e.Policy.RootState = true
	if out := request(e, "root.run", map[string]any{"name": "probe"}); out.Code != "ROOT_FAILED" || len(r.started()) != 0 {
		t.Fatal("a root state policy ran without its directory", out)
	}
	e.RootState = "/volume1/@synopkgland/Demo"
	result(t, request(e, "root.run", map[string]any{"name": "probe"}))
	if env(r.started()[0])["SYNOPKGLAND_ROOT_STATE"] != e.RootState {
		t.Fatal(r.started()[0].Env)
	}
	j, _, jr := jobSetup(t, cleanPolicy(""), func(context.Context, rootCall, *bufio.Reader, io.Writer) (int, error) { return 0, nil })
	j.Policy.RootState, j.RootState = true, "/volume1/@synopkgland/Demo"
	awaitJob(t, j, startJob(t, j, nil), "succeeded")
	if env(jr.started()[0])["SYNOPKGLAND_ROOT_STATE"] != j.RootState {
		t.Fatal(jr.started()[0].Env)
	}
}

func TestLifecycleStepsSeeTheRootStateAndDeleteStateRemovesIt(t *testing.T) {
	state := filepath.Join(t.TempDir(), "Demo")
	os.Mkdir(state, 0700)
	os.WriteFile(filepath.Join(state, "db"), []byte("x"), 0600)
	for _, deleteState := range []bool{false, true} {
		r := &purgeRunnerStub{}
		logs := runPurgeStep(rootPurge{Approved: approved, Runner: r, DeleteState: deleteState, State: state}, purgePolicy("Demo"))
		if env(r.call)["SYNOPKGLAND_ROOT_STATE"] != state {
			t.Fatal(r.call.Env)
		}
		_, err := os.Stat(state)
		if deleteState != os.IsNotExist(err) {
			t.Fatal(deleteState, err, logs)
		}
	}
	r := &purgeRunnerStub{}
	rootStop{Approved: approved, Runner: r, State: state}.run(context.Background(), stopPolicy(), "stop", func(string) {})
	if !reflect.DeepEqual(env(r.call)["SYNOPKGLAND_ROOT_STATE"], state) {
		t.Fatal(r.call.Env)
	}
}
