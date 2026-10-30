package sharedfolder

import (
	"errors"
	"strings"
	"testing"
)

func TestPermissionMessageNamesFolderAndRecoveryInEveryLocale(t *testing.T) {
	err := &PermissionError{Name: "Media", Account: "Reader", Need: Write}
	for _, locale := range []string{"enu", "cht", "chs", "jpn", "krn", "ger", "fre", "spn", "ita"} {
		message := Message(err, locale)
		if !strings.Contains(message, "Media") || !strings.Contains(message, "Reader") || !strings.Contains(message, "RW") || strings.HasSuffix(message, ".") {
			t.Fatal(locale, message)
		}
	}
	if Message(errors.New("other"), "cht") != "other" {
		t.Fatal("unrelated failure masked")
	}
}
