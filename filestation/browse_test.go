package filestation

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// browseServer answers the session check and both List methods, and records
// the folder_path DSM was asked to list ("" for the share roots).
func browseServer(t *testing.T, user string, listed *[]string) *Client {
	return fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("method") {
		case "get":
			fmt.Fprintf(w, `{"success":true,"data":{"username":%q}}`, user)
		case "list_share":
			*listed = append(*listed, "")
			fmt.Fprint(w, `{"success":true,"data":{"shares":[{"path":"/share","name":"share","isdir":true}],"total":1,"offset":0}}`)
		case "list":
			*listed = append(*listed, r.Form.Get("folder_path"))
			fmt.Fprint(w, `{"success":true,"data":{"files":[{"path":"/share/a.txt","name":"a.txt","isdir":false,"additional":{"size":3}}],"total":1,"offset":0}}`)
		}
	})
}

func TestBrowseListsShareRootsAndFoldersThroughCurrentSession(t *testing.T) {
	var listed []string
	s, _ := fileService(t, browseServer(t, "alice", &listed))
	roots, err := s.Browse(context.Background(), alice(t), sessionRequest(), "", 0)
	if err != nil || len(roots.Shares) != 1 || roots.Shares[0].Path != "/share" {
		t.Fatalf("%+v %v", roots, err)
	}
	page, err := s.Browse(context.Background(), alice(t), sessionRequest(), "/share", 0)
	if err != nil || len(page.Files) != 1 || page.Files[0].Name != "a.txt" {
		t.Fatalf("%+v %v", page, err)
	}
	if len(listed) != 2 || listed[0] != "" || listed[1] != "/share" {
		t.Fatalf("listed %q", listed)
	}
}

func TestBrowseRefusesAnotherUsersSessionBeforeListing(t *testing.T) {
	var listed []string
	s, _ := fileService(t, browseServer(t, "mallory", &listed))
	if _, err := s.Browse(context.Background(), alice(t), sessionRequest(), "/share", 0); err == nil {
		t.Fatal("a session that resolves to another user must be refused")
	}
	if len(listed) != 0 {
		t.Fatalf("listed %q", listed)
	}
}

func TestBrowseRefusesBadPathsAndOnNilService(t *testing.T) {
	var listed []string
	s, _ := fileService(t, browseServer(t, "alice", &listed))
	for _, folder := range []string{"share", "/share/../etc"} {
		if _, err := s.Browse(context.Background(), alice(t), sessionRequest(), folder, 0); err == nil {
			t.Errorf("%q must be refused", folder)
		}
	}
	var nilService *Service
	if _, err := nilService.Browse(context.Background(), alice(t), sessionRequest(), "", 0); err == nil {
		t.Fatal("nil service must refuse")
	}
}
