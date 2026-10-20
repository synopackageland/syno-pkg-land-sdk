package main

import (
	"strings"
	"testing"
)

func portalPolicy(pkg string) Policy {
	return Policy{Version: 1, Package: pkg, App: "SYNO.SDS." + pkg + ".Instance", Operations: []string{"status", "portal.register", "portal.unregister"}, Portal: &PortalPolicy{Application: "Demo App", Alias: "demo"}}
}

func TestPolicyAcceptsAPortalEntry(t *testing.T) {
	for _, pkg := range []string{"Demo", "com.example.demo", "demo_app-2"} {
		if err := validatePolicy(portalPolicy(pkg), pkg); err != nil {
			t.Fatal(pkg, err)
		}
	}
}

func TestPolicyRefusesAnIncompletePortalEntry(t *testing.T) {
	for name, mutate := range map[string]func(*Policy){
		"no entry":             func(p *Policy) { p.Portal = nil },
		"entry without ops":    func(p *Policy) { p.Operations = []string{"status"} },
		"register only":        func(p *Policy) { p.Operations = []string{"status", "portal.register"} },
		"unregister only":      func(p *Policy) { p.Operations = []string{"status", "portal.unregister"} },
		"empty application":    func(p *Policy) { p.Portal.Application = "" },
		"control application":  func(p *Policy) { p.Portal.Application = "Demo\nApp" },
		"long application":     func(p *Policy) { p.Portal.Application = strings.Repeat("a", 65) },
		"empty alias":          func(p *Policy) { p.Portal.Alias = "" },
		"path alias":           func(p *Policy) { p.Portal.Alias = "a/b" },
		"leading dash alias":   func(p *Policy) { p.Portal.Alias = "-demo" },
		"invalid utf8 app":     func(p *Policy) { p.Portal.Application = "\xff" },
		"long alias":           func(p *Policy) { p.Portal.Alias = strings.Repeat("a", 65) },
		"dots in package name": func(p *Policy) { p.Package = "a..b" },
		"trailing dot package": func(p *Policy) { p.Package = "demo." },
	} {
		p := portalPolicy("Demo")
		entry := *p.Portal
		p.Portal = &entry
		mutate(&p)
		if validatePolicy(p, p.Package) == nil {
			t.Error(name, "accepted")
		}
	}
}
