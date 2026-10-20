package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestAllNamedVolumesAreAppDataByDefault(t *testing.T) {
	req := upstreamProject(t, "immich")
	p, err := Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		AppData *struct {
			Project string                       `json:"project"`
			Volumes []struct{ Key, Name string } `json:"volumes"`
		} `json:"app_data"`
	}
	if json.Unmarshal(b, &got) != nil || got.AppData == nil || got.AppData.Project != p.ProjectName || len(got.AppData.Volumes) != len(p.Effective.Volumes) {
		t.Fatal(string(b))
	}
	for _, v := range got.AppData.Volumes {
		if v.Name != p.Effective.Volumes[v.Key].Name {
			t.Fatal(v)
		}
	}
}

func TestAppDataInventoryIsIdenticalInPlanAndLock(t *testing.T) {
	for _, source := range []string{
		"services:\n  web: {image: alpine, volumes: ['zdata:/z', 'adata:/a']}\nvolumes:\n  zdata: {}\n  adata: {}\n",
		"services:\n  web: {image: alpine}\n",
	} {
		req := Request{Root: t.TempDir(), Package: "Fixture", Files: []string{"compose.yaml"}, Network: Network{Egress: "isolated"}}
		if err := os.WriteFile(filepath.Join(req.Root, "compose.yaml"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		p, err := Build(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := p.LockJSON()
		if err != nil {
			t.Fatal(err)
		}
		var lock struct {
			AppData *AppData `json:"app_data"`
		}
		if err := json.Unmarshal(raw, &lock); err != nil {
			t.Fatal(err)
		}
		if p.AppData == nil || p.AppData.Volumes == nil || !reflect.DeepEqual(p.AppData, lock.AppData) {
			t.Fatalf("plan/lock inventory differs: %s", raw)
		}
		keys := []string{}
		for _, v := range p.AppData.Volumes {
			keys = append(keys, v.Key)
			if v.Name != p.ProjectName+"_"+v.Key {
				t.Fatal(v)
			}
		}
		if !slices.IsSorted(keys) {
			t.Fatal(keys)
		}
	}
}
