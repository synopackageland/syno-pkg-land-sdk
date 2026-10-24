package externalorigin

import "testing"

func TestCanonical(t *testing.T) {
	for _, s := range []string{"https://app.example.com", "https://APP.example.com:443"} {
		if got, err := Canonical(s); err != nil || got != "https://app.example.com" {
			t.Fatal(s, got, err)
		}
	}
	for _, s := range []string{"", "http://app.example.com", "https://app.example.com/", "https://app.example.com/path", "https://app.example.com?", "https://app.example.com#", "https://app.example.com:444", "https://app.example.com:", "https://user@app.example.com", "https://127.0.0.1", "https://[::1]", "https://127.1", "https://app.example.com.", "https://a_b.example.com", "https://a%2eb.example.com", "https://app.example.com\\evil", "https://app.example.com\x00", "https://app.example.com\n", "https://例子.example.com"} {
		if got, err := Canonical(s); err == nil {
			t.Fatal("accepted", s, got)
		}
	}
}

func TestCanonicalWithPort(t *testing.T) {
	for in, want := range map[string]string{
		"https://app.example.com":          "https://app.example.com",
		"https://APP.example.com:443":      "https://app.example.com",
		"https://nas.example.ts.net:5001":  "https://nas.example.ts.net:5001",
		"https://NAS.example.ts.net:65535": "https://nas.example.ts.net:65535",
		"https://nas.example.ts.net:1":     "https://nas.example.ts.net:1",
	} {
		if got, err := CanonicalWithPort(in); err != nil || got != want {
			t.Fatal(in, got, err)
		}
	}
	for _, s := range []string{"https://app.example.com:", "https://app.example.com:0", "https://app.example.com:05001", "https://app.example.com:65536", "https://app.example.com:+1", "https://127.0.0.1:5001", "https://[::1]:5001", "https://app.example.com:5001/", "https://user@app.example.com:5001", "http://app.example.com:5001"} {
		if got, err := CanonicalWithPort(s); err == nil {
			t.Fatal("accepted", s, got)
		}
	}
	if _, err := Canonical("https://nas.example.ts.net:5001"); err == nil {
		t.Fatal("Canonical accepted a port")
	}
}
