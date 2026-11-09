package sharedfolder

import (
	"fmt"
	"regexp"
	"strings"
)

var unsupportedSelectionName = regexp.MustCompile(`(?i)^(homes?|users|ftp|root|admin|guest|system|volume[0-9]*|(usb|sata|sd)share[0-9]*|ActiveBackupforBusiness|surveillance|docker|web_packages)$`)

// ValidateSelectionName checks the install picker's supported local share name.
func ValidateSelectionName(name string) error {
	if !validName(name) || len(name) > 32 || strings.TrimSpace(name) != name || strings.ContainsAny(name, `/:*?"<>|{}`) || unsupportedSelectionName.MatchString(name) {
		return fmt.Errorf("invalid or unsupported selected shared folder")
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return fmt.Errorf("invalid selected shared folder")
		}
	}
	return nil
}
