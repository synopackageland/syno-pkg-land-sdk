package filestation

import (
	"context"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"regexp"
	"testing"
)

func TestRequestFilesCheckUsesCurrentSessionAndRefusesMismatchedSubject(t *testing.T) {
	checks := 0
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Header.Get("Cookie") != "id=test-session" {
			t.Error("cookie changed")
		}
		if r.Form.Get("api") == "SYNO.Core.NormalUser" {
			fmt.Fprint(w, `{"success":true,"data":{"username":"alice"}}`)
			return
		}
		checks++
		if r.Form.Get("api") != "SYNO.FileStation.CheckPermission" || r.Form.Get("method") != "write" || r.Form.Get("path") != "/home" {
			t.Error(r.Form)
		}
		fmt.Fprint(w, `{"success":false,"error":{"code":407}}`)
	})
	files := RequestContext{Service: NewService(client), Request: sessionRequest(), Caller: callerFor(t, "alice")}.Files()
	if e := files.CheckPermission(context.Background(), "/home"); e == nil {
		t.Fatal("DSM refusal lost")
	}
	files = RequestContext{Service: NewService(client), Request: sessionRequest(), Caller: callerFor(t, "bob")}.Files()
	if e := files.CheckPermission(context.Background(), "/home"); e == nil {
		t.Fatal("mismatched subject accepted")
	}
	if checks != 1 {
		t.Fatal("mismatched request reached DSM file operation")
	}
	var _ Files = RequestContext{Caller: api.Caller{}}.Files()
}

func TestCheckPermissionRequiresExplicitNonSkipResult(t *testing.T) {
	for _, data := range []string{`{}`, `{"blSkip":true}`} {
		client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"success":true,"data":%s}`, data) })
		if e := client.CheckPermission(context.Background(), sessionRequest(), "/home"); e == nil {
			t.Fatal("permission inferred from success:true")
		}
	}
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if !probeNameRE.MatchString(r.Form.Get("filename")) || r.Form.Get("overwrite") != "false" || r.Form.Get("size") != "0" {
			t.Error("missing fixed permission probe fields")
		}
		fmt.Fprint(w, `{"success":true,"data":{"blSkip":false}}`)
	})
	if e := client.CheckPermission(context.Background(), sessionRequest(), "/home"); e != nil {
		t.Fatal(e)
	}
}

var probeNameRE = regexp.MustCompile(`^\.synopkgland-permission-probe-[0-9a-f]{32}$`)

func TestCheckPermissionProbesWithAFreshName(t *testing.T) {
	seen := map[string]bool{}
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		seen[r.Form.Get("filename")] = true
		fmt.Fprint(w, `{"success":true,"data":{"blSkip":false}}`)
	})
	for range 2 {
		if e := client.CheckPermission(context.Background(), sessionRequest(), "/home"); e != nil {
			t.Fatal(e)
		}
	}
	if len(seen) != 2 {
		t.Fatal("probe name repeated", seen)
	}
}
