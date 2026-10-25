package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageVolumeCountIsCappedBeforePlanning(t *testing.T) {
	volumes := make([]string, 300)
	for i := range volumes {
		volumes[i] = fmt.Sprintf(`"/v%d":{}`, i)
	}
	config := []byte(`{"architecture":"amd64","os":"linux","config":{"Volumes":{` + strings.Join(volumes, ",") + `}}}`)
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"config":{"digest":%q}}`, contentDigest(config)))
	const image = "openlistteam/openlist:latest"
	req := upstreamProject(t, "openlist")
	req.Architecture = "x86_64"
	req.ImageProofs = map[string]ImageProof{image: {Reference: "docker.io/openlistteam/openlist@" + contentDigest(manifest), Manifest: manifest, Config: config}}
	_, err := Build(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "too many VOLUME") {
		t.Fatalf("got %v", err)
	}
}

func TestIncludeDiamondFanOutIsWalkedOnce(t *testing.T) {
	root := t.TempDir()
	// Ten layers, each including the next layer's file twenty times: 20^10
	// walks without memoization, ten with it.
	for i := 0; i < 10; i++ {
		var doc strings.Builder
		doc.WriteString("include:\n")
		if i < 9 {
			for j := 0; j < 20; j++ {
				fmt.Fprintf(&doc, "  - l%d.yaml\n", i+1)
			}
		}
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("l%d.yaml", i)), []byte(doc.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := sourceSet{root: root, inputs: map[string]Input{}}
	if _, _, err := s.compose("l0.yaml", 0); err != nil {
		t.Fatal(err)
	}
	if len(s.inputs) != 10 {
		t.Fatalf("inputs: %d", len(s.inputs))
	}
}

func TestIncludeCycleStillFails(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.yaml"), []byte("include:\n  - b.yaml\n"), 0600)
	os.WriteFile(filepath.Join(root, "b.yaml"), []byte("include:\n  - a.yaml\n"), 0600)
	s := sourceSet{root: root, inputs: map[string]Input{}}
	if _, _, err := s.compose("a.yaml", 0); err == nil || !strings.Contains(err.Error(), "recursion") {
		t.Fatalf("got %v", err)
	}
}
