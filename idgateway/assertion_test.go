package idgateway

import (
	"strings"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func testAssertion(now time.Time) Assertion {
	return Assertion{Txn: "t1", Origin: "https://nas.example:18443", Subject: Subject{UID: 1026, Name: "alice", Admin: true}, Expires: now.Add(time.Minute).Unix()}
}

func TestAssertionRoundTrips(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sealed, err := SealAssertion(testKey, testAssertion(now))
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenAssertion(testKey, sealed, now)
	if err != nil || got != testAssertion(now) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestAssertionRefusesTamperingExpiryAndOtherKeys(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sealed, _ := SealAssertion(testKey, testAssertion(now))
	payload, sig, _ := strings.Cut(sealed, ".")
	forged, _ := SealAssertion(testKey, Assertion{Txn: "t1", Origin: "x", Subject: Subject{UID: 0, Name: "root", Admin: true}, Expires: now.Add(time.Minute).Unix()})
	forgedPayload, _, _ := strings.Cut(forged, ".")
	for name, tc := range map[string]struct {
		value string
		key   []byte
		at    time.Time
	}{
		"swapped payload": {forgedPayload + "." + sig, testKey, now},
		"other key":       {sealed, []byte("fedcba9876543210fedcba9876543210"), now},
		"expired":         {sealed, testKey, now.Add(61 * time.Second)},
		"truncated":       {payload, testKey, now},
		"empty":           {"", testKey, now},
	} {
		if _, err := OpenAssertion(tc.key, tc.value, tc.at); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSealRefusesShortKeys(t *testing.T) {
	if _, err := SealAssertion([]byte("short"), testAssertion(time.Now())); err == nil {
		t.Fatal("short key accepted")
	}
}
