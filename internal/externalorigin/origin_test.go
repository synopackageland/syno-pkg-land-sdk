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
