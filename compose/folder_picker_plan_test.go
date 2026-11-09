package compose

import (
	"context"
	"strings"
	"testing"
)

func TestFolderPickerPlanRequiresMatchingDeclaration(t *testing.T) {
	r := nativeRequest(t)
	r.InstallSharedFolder.Picker = "folder"
	s := r.Shares["media"]
	s.Picker = "folder"
	r.Shares["media"] = s
	p, err := Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.InstallSharedFolder.Picker != "folder" {
		t.Fatal("picker lost")
	}
	s.Picker = ""
	r.Shares["media"] = s
	if _, err = Build(context.Background(), r); err == nil || !strings.Contains(err.Error(), "COMPOSE_SHARE_INVALID") {
		t.Fatal(err)
	}
}
