package idgateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdoptClientSecretNeverRotatesOrFollowsSeedSymlink(t *testing.T) {
	state := t.TempDir()
	seed := filepath.Join(t.TempDir(), "seed")
	os.WriteFile(seed, []byte(strings.Repeat("a", 64)), 0600)
	first, err := AdoptClientSecret(state, "reader", seed)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := AdoptClientSecret(state, "reader", seed); err != nil || again != first {
		t.Fatal("not idempotent", err)
	}
	os.WriteFile(seed, []byte(strings.Repeat("b", 64)), 0600)
	if _, err := AdoptClientSecret(state, "reader", seed); err == nil {
		t.Fatal("silently rotated retained provider")
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(seed, link)
	if _, err := AdoptClientSecret(t.TempDir(), "reader", link); err == nil {
		t.Fatal("symlink seed accepted")
	}
}
