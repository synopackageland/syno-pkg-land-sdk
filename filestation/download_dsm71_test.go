package filestation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// DSM 7.1.1-42962 (arm-nas.example, the regression test) answers Download v2 mode=download with the
// file bytes and no Content-Disposition header. Its Content-Type was not
// recorded, so every plausible type is covered.
func dsm71Download(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		fmt.Fprint(w, body)
	}
}

func TestDSM71DownloadWithoutDispositionReturnsFileBytes(t *testing.T) {
	for _, tt := range []struct{ contentType, body string }{
		{"text/plain", "t0231 fixture a.txt"},
		{"application/octet-stream", "\x00\x01binary"},
		{"", "no type"},
		{"application/json", `{"name":"a user file"}`},
		{"application/json", `[{"success":false}]`},
		{"text/plain", strings.Repeat("y", downloadPeek+7)},
	} {
		t.Run(tt.contentType, func(t *testing.T) {
			c := fakeClient(t, dsm71Download(tt.contentType, tt.body))
			b, err := c.Read(context.Background(), sessionRequest(), "/home/a")
			if err != nil || string(b) != tt.body {
				t.Fatalf("%v %q", err, b)
			}
		})
	}
}

func TestDSM71DownloadStreamsBeyondThePeek(t *testing.T) {
	body := strings.Repeat("z", MaxBytes*3)
	c := fakeClient(t, dsm71Download("application/octet-stream", body))
	d, err := c.DownloadStream(context.Background(), sessionRequest(), "/home/a")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Body.Close()
	b, err := io.ReadAll(d.Body)
	if err != nil || string(b) != body || d.ContentType != "application/octet-stream" {
		t.Fatal(err, len(b))
	}
}

func TestDSM71DownloadEnvelopeWithoutDispositionIsNeverContent(t *testing.T) {
	for _, tt := range []struct{ contentType, body, want string }{
		{"application/json; charset=utf-8", `{"success":false,"error":{"code":408}}`, "denied"},
		{"text/plain", `{"success":false,"error":{"code":119}}`, "session_required"},
		{"", ` {"error":{"code":407},"success":false}` + "\n", "denied"},
		{"application/json", `{"success":true,"data":{}}`, "unavailable"},
	} {
		t.Run(tt.want, func(t *testing.T) {
			c := fakeClient(t, dsm71Download(tt.contentType, tt.body))
			b, err := c.Read(context.Background(), sessionRequest(), "/home/a")
			if err == nil || len(b) != 0 || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("%v %q", err, b)
			}
		})
	}
}

func TestNonAttachmentDispositionStaysRefused(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `inline; filename="a"`)
		fmt.Fprint(w, "abc")
	})
	if b, err := c.Read(context.Background(), sessionRequest(), "/home/a"); err == nil || len(b) != 0 {
		t.Fatal("inline disposition admitted")
	}
}
