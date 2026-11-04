package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/secret"
)

func TestFileRecorderRefusesRequestSecretScopeBeforeOpeningStore(t *testing.T) {
	dir := t.TempDir() + "/events"
	ctx := secret.NewRedactor(json.RawMessage(`{"credential":"canary"}`), "credential").WithScope(context.Background())
	_, _, err := (FileRecorder{Dir: dir}).Admit(ctx, Record{DeliveryID: "request", Event: "demo", Key: "canary"}, time.Minute)
	if !errors.Is(err, secret.ErrPersistence) {
		t.Fatalf("credential persisted: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("store touched disk")
	}
}
