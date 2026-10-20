package filestation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTypedOperationsAlwaysUseEachCallersSession(t *testing.T) {
	calls := 0
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		cookie, err := r.Cookie("id")
		if err != nil || (cookie.Value != "alice-session" && cookie.Value != "bob-session") || r.Header.Get("Cookie") != "id="+cookie.Value || r.Header.Get("Authorization") != "" {
			t.Error("caller session was replaced or extra credentials forwarded")
			return
		}
		if r.Header.Get("X-SYNO-TOKEN") != "test-token" {
			t.Error("missing caller token")
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
		} else {
			r.ParseForm()
		}
		if r.Form.Get("_sid") != "" {
			t.Error("unexpected SID")
		}
		method := r.Form.Get("method")
		switch method {
		case "get":
			fmt.Fprintf(w, `{"success":true,"data":{"username":%q}}`, strings.TrimSuffix(cookie.Value, "-session"))
		case "list_share":
			fmt.Fprint(w, `{"success":true,"data":{"shares":[{"path":"/anything","name":"anything","isdir":true}],"total":1}}`)
		case "list":
			if r.Form.Get("offset") != "100" || r.Form.Get("limit") != "25" || r.Form.Get("sort_by") != "size" || r.Form.Get("sort_direction") != "DESC" {
				t.Error("pagination/sort not forwarded")
			}
			fmt.Fprint(w, `{"success":true,"data":{"files":[],"total":100,"offset":100}}`)
		case "getinfo":
			fmt.Fprint(w, `{"success":true,"data":{"files":[{"path":"/anything/a","name":"a","isdir":false}]}}`)
		case "create", "upload":
			fmt.Fprint(w, `{"success":true}`)
		case "download":
			w.Header().Set("Content-Disposition", `attachment; filename="a"`)
			fmt.Fprint(w, strings.Repeat("x", MaxBytes+1))
		default:
			t.Errorf("unexpected DSM operation %q", method)
		}
	})
	s := NewService(client)
	for _, user := range []string{"alice", "bob"} {
		r := sessionRequest()
		r.Header.Set("Cookie", "id="+user+"-session")
		r.Header.Set("Authorization", "package-account-credentials-must-not-travel")
		caller := callerFor(t, user)
		ctx := context.Background()
		roots, err := s.ListShares(ctx, caller, r, 0)
		if err != nil || roots.Shares[0].Path != "/anything" {
			t.Fatalf("%+v %v", roots, err)
		}
		if _, err := s.List(ctx, caller, r, "/anything", ListOptions{Offset: 100, Limit: 25, SortBy: "size", SortDirection: "DESC"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Info(ctx, caller, r, "/anything/a"); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateFolder(ctx, caller, r, "/anything", "new"); err != nil {
			t.Fatal(err)
		}
		if err := s.Upload(ctx, caller, r, "/anything", "a", strings.NewReader("abc"), 3); err != nil {
			t.Fatal(err)
		}
		stream, err := s.Download(ctx, caller, r, "/anything/a")
		if err != nil {
			t.Fatal(err)
		}
		n, err := io.Copy(io.Discard, stream.Body)
		stream.Body.Close()
		if err != nil || n != MaxBytes+1 {
			t.Fatalf("stream was buffered/bounded: %d %v", n, err)
		}
	}
	if calls != 24 {
		t.Fatalf("wanted verification plus operation for every request, got %d", calls)
	}
}

func TestTypedOperationsRejectMissingOrMismatchedSession(t *testing.T) {
	calls := 0
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"success":true,"data":{"username":"mallory"}}`)
	})
	s := NewService(client)
	for _, r := range []*http.Request{sessionRequest(), {Header: make(http.Header)}} {
		if _, err := s.ListShares(context.Background(), alice(t), r, 0); err == nil {
			t.Fatal("unverified caller accepted")
		}
	}
	if calls != 1 {
		t.Fatal("missing session reached DSM")
	}
}

func TestDownloadReturnsBeforeDSMFinishesSendingBytes(t *testing.T) {
	release := make(chan struct{})
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="a"`)
		fmt.Fprint(w, "first")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "last")
	})
	stream, err := client.DownloadStream(context.Background(), sessionRequest(), "/share/a")
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	b, err := io.ReadAll(stream.Body)
	if err != nil || string(b) != "firstlast" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestListValidatesOptionsAndDownloadPropagatesPermissionRefusal(t *testing.T) {
	calls := 0
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"success":false,"error":{"code":408,"secret":"test-token"}}`)
	})
	for _, opts := range []ListOptions{{Offset: -1}, {Limit: 101}, {SortBy: "arbitrary"}, {SortDirection: "bad"}} {
		if _, err := client.List(context.Background(), sessionRequest(), "/share", opts); err == nil {
			t.Fatal("invalid listing option accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid options sent to DSM")
	}
	if _, err := client.DownloadStream(context.Background(), sessionRequest(), "/share/a"); err == nil || strings.Contains(err.Error(), "test-token") || !strings.Contains(err.Error(), "denied") {
		t.Fatal(err)
	}
}

func TestMetadataRoutesUsePathsWithoutPersistedGrants(t *testing.T) {
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("method") == "get" {
			fmt.Fprint(w, `{"success":true,"data":{"username":"alice"}}`)
			return
		}
		if r.Form.Get("method") == "list_share" {
			fmt.Fprint(w, `{"success":true,"data":{"shares":[],"total":0,"offset":0}}`)
			return
		}
		if r.Form.Get("method") == "list" {
			fmt.Fprint(w, `{"success":false,"error":{"code":407}}`)
			return
		}
		fmt.Fprint(w, `{"success":true}`)
	})
	s := NewService(client)
	if out := invoke(t, s, "alice", "nas.files.shares", `{"offset":0}`); out["success"] != true {
		t.Fatal(out)
	}
	if out := invoke(t, s, "alice", "nas.files.list", `{"path":"/team","sortBy":"name"}`); out["error"].(map[string]any)["code"] != "denied" {
		t.Fatal(out)
	}
	if out := invoke(t, s, "alice", "nas.files.create_folder", `{"parent":"/team","name":"new"}`); out["success"] != true {
		t.Fatal(out)
	}
	if out := invoke(t, s, "alice", "nas.files.list", `{"grantId":"legacy","relative":""}`); out["success"] != false {
		t.Fatal("legacy request accepted")
	}
	for _, route := range s.Routes() {
		if strings.Contains(route.Method(), "grants") || route.Method() == "nas.discovery" {
			t.Fatal("retired grant route retained")
		}
	}
}

func TestEmptySharePageSerializesAsAnEmptyListing(t *testing.T) {
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"shares":[],"total":0,"offset":0}}`)
	})
	page, err := client.Discover(context.Background(), sessionRequest(), 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"files":[]`) && !strings.Contains(string(encoded), `"shares":[]`) {
		t.Fatalf("empty share page is indistinguishable from a malformed response: %s", encoded)
	}
}
