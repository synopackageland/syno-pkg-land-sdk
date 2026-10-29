package delegate

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/background"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/runtimebudget"
)

func TestDownloadBudgetsAllowSlowBackgroundWork(t *testing.T) {
	client := downloadClient()
	if DownloadTimeout != 30*time.Minute || client.Timeout != DownloadTimeout {
		t.Fatalf("download budgets: %v %v", DownloadTimeout, client.Timeout)
	}
	if DownloadTimeout > background.JobTimeout {
		t.Fatal("download exceeds the default job budget")
	}
	if client.Transport.(*http.Transport).ResponseHeaderTimeout != runtimebudget.Operation {
		t.Fatal("download header wait must tolerate slow NAS work")
	}
}

type slowDownloadTransport struct{}

func (slowDownloadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case <-time.After(90 * time.Second):
		return &http.Response{StatusCode: http.StatusOK, ContentLength: 4, Body: io.NopCloser(strings.NewReader("data"))}, nil
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
}

func TestSlowDownloadCompletesButEarlierParentStillCancels(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		t.Run(map[bool]string{false: "slow", true: "earlier-parent"}[earlier], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := service(t, nil)
				s.HTTP = &http.Client{Transport: slowDownloadTransport{}}
				ctx := context.Background()
				if earlier {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
				}
				writes := 0
				job := JobContext{JobID: "j", DelegationID: "d", StagingDir: s.StagingDir,
					callExecutor: func(_ context.Context, op string, p map[string]any, _ any) error {
						writes++
						data, err := os.ReadFile(filepath.Join(s.StagingDir, p["staging"].(string)))
						if err != nil || op != "files.write" || string(data) != "data" {
							t.Fatalf("authorized write: %s %q %v", op, data, err)
						}
						return nil
					}}
				err := s.download(ctx, DownloadInput{URL: "http://nas.invalid/file", Dest: "/home", Name: "file"}, job)
				if earlier {
					if err == nil || writes != 0 {
						t.Fatalf("canceled transfer wrote: %d %v", writes, err)
					}
				} else if err != nil || writes != 1 {
					t.Fatalf("slow transfer: writes=%d err=%v", writes, err)
				}
				files, _ := os.ReadDir(s.StagingDir)
				if len(files) != 0 {
					t.Fatal("download staging leaked")
				}
			})
		})
	}
}
