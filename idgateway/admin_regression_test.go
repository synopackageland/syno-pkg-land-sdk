package idgateway

import (
	"context"
	"errors"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type independentRole struct {
	changingMembership
	admin     bool
	err       error
	principal string
}

func (m *independentRole) IsAdministrator(_ context.Context, name string) (bool, error) {
	m.principal = name
	return m.admin, m.err
}

func TestAdmissionUsesNativeRoleInsteadOfGroupNames(t *testing.T) {
	for _, name := range []string{"operator", `CORP\operator`, "operator@example.test"} {
		m := &independentRole{admin: true}
		if err := admissionError(context.Background(), Subject{Name: name}, "dsm-admins", nil, m); err != nil || m.principal != name {
			t.Fatalf("directory admin denied: %v principal=%q", err, m.principal)
		}
		m.admin = false
		m.groups = []string{"administrators"}
		if err := admissionError(context.Background(), Subject{Name: name, Admin: true}, "dsm-admins", nil, m); !errors.Is(err, api.ErrAdminRequired) {
			t.Fatalf("group string overrode native role: %v", err)
		}
		m.err = context.DeadlineExceeded
		if err := admissionError(context.Background(), Subject{Name: name}, "dsm-admins", nil, m); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, api.ErrAdminRequired) {
			t.Fatalf("unknown role became denial: %v", err)
		}
	}
}
