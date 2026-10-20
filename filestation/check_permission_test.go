package filestation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckPermissionRequiresVerifiedSessionAndExplicitWriteResult(t *testing.T) {
	for _, body := range []string{`{"blSkip":false}`, `{}`, `{"blSkip":true}`, `{"blSkip":null}`, `{"blSkip":false,"errors":[{"code":407}]}`} {
		t.Run(body, func(t *testing.T) {
			checks := 0
			client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				switch r.Form.Get("api") {
				case "SYNO.Core.NormalUser":
					fmt.Fprint(w, `{"success":true,"data":{"username":"alice"}}`)
				case "SYNO.FileStation.CheckPermission":
					checks++
					if r.Form.Get("version") != "3" || r.Form.Get("method") != "write" || r.Form.Get("path") != "/home" || !probeNameRE.MatchString(r.Form.Get("filename")) || r.Form.Get("overwrite") != "false" || r.Form.Get("size") != "0" || r.Header.Get("Cookie") != "id=test-session" || r.Header.Get("X-SYNO-TOKEN") != "test-token" {
						t.Error("incorrect permission check transport")
					}
					fmt.Fprintf(w, `{"success":true,"data":%s}`, body)
				default:
					t.Error("unexpected API")
				}
			})
			service := NewService(client)
			err := service.CheckPermission(context.Background(), callerFor(t, "alice"), sessionRequest(), "/home")
			if (err == nil) != (body == `{"blSkip":false}`) || checks != 1 {
				t.Fatalf("checks=%d err=%v", checks, err)
			}
			checks = 0
			if err := service.CheckPermission(context.Background(), callerFor(t, "bob"), sessionRequest(), "/home"); err == nil || checks != 0 {
				t.Fatal("mismatched subject checked write permission")
			}
			if err := service.CheckPermission(context.Background(), callerFor(t, "alice"), sessionRequest(), "/home/../etc"); err == nil || checks != 0 {
				t.Fatal("invalid path checked")
			}
		})
	}
}

func TestCheckPermissionValidatesCredentialPairBeforeDSM(t *testing.T) {
	calls := 0
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		if r.Form.Get("api") == "SYNO.Core.NormalUser" {
			fmt.Fprint(w, `{"success":true,"data":{"username":"alice"}}`)
		} else {
			fmt.Fprint(w, `{"success":true,"data":{"blSkip":false}}`)
		}
	})
	service := NewService(client)
	for index, r := range []*http.Request{nil, httptest.NewRequest("POST", "/", nil), sessionRequest(), sessionRequest()} {
		if r != nil && r.Header.Get("X-SYNO-TOKEN") != "" {
			if index == 2 {
				r.Header.Del("X-SYNO-TOKEN")
			} else {
				r.Header.Set("SynoToken", "different")
			}
		}
		before := calls
		if err := service.CheckPermission(context.Background(), callerFor(t, "alice"), r, "/home"); err == nil || calls != before {
			t.Fatal("invalid session reached DSM", err, calls)
		}
	}
	r := sessionRequest()
	r.Header.Del("X-SYNO-TOKEN")
	r.Header.Set("SynoToken", "test-token")
	if err := service.CheckPermission(context.Background(), callerFor(t, "alice"), r, "/home"); err != nil {
		t.Fatal("canonical alias refused", err)
	}
}
