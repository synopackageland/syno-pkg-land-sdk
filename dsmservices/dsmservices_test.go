package dsmservices

import (
	"reflect"
	"strings"
	"testing"
)

// The fixtures copy the shape of DSM 7 files: DSM's own synow3.sc with
// i18n keys, factory defaults, a package file with bare values, a
// Container Manager project and lists, ranges and mixed protocols.
func TestReadFixtures(t *testing.T) {
	defs := Read("testdata/syno", "testdata/missing", "testdata/defaults", "testdata/local")
	got := map[string]Definition{}
	for _, d := range defs {
		d.Destinations = nil // The legacy single-TCP-port projection is unchanged.
		got[d.File+"/"+d.ID] = d
	}
	want := map[string]Definition{
		"synow3.sc/SYNO.SDS.App.FileStation3.Instance_http": {ID: "SYNO.SDS.App.FileStation3.Instance_http", File: "synow3.sc", Title: "HTTP", Desc: "File Station", Ports: []int{7000}},
		"synow3.sc/dms":                        {ID: "dms", File: "synow3.sc", Title: "HTTP", TitleKey: "firewall:firewall_service_opt_dsm_http", Desc: "DSM", DescKey: "firewall:firewall_service_desc_dsm_http", Ports: []int{5050}},
		"synow3.sc/dms_https":                  {ID: "dms_https", File: "synow3.sc", Title: "HTTPS", TitleKey: "firewall:firewall_service_opt_dsm_https", Desc: "DSM", DescKey: "firewall:firewall_service_desc_dsm_https", Ports: []int{5051}},
		"synow3.sc/https":                      {ID: "https", File: "synow3.sc", Title: "HTTPS", TitleKey: "firewall:firewall_service_opt_https", Desc: "Web Station", Ports: []int{443}},
		"syno_ntp.sc/ntp":                      {ID: "ntp", File: "syno_ntp.sc", Title: "NTP", DescKey: "time:ntp_service"},
		"upnp_igd.sc/upnp_igd":                 {ID: "upnp_igd", File: "upnp_igd.sc", Title: "UPNP IGD", Desc: "UPNP IGD", Ports: []int{55001, 55002}},
		"SynologyDrive.sc/cloudstation":        {ID: "cloudstation", File: "SynologyDrive.sc", Title: "Synology Drive Server", Desc: "Synology Drive Server", Ports: []int{6690}},
		"MailPlus-Server.sc/mail_haproxy_smtp": {ID: "mail_haproxy_smtp", File: "MailPlus-Server.sc", Title: "MailPlus Server (SMTP)", Desc: "MailPlus Server", Ports: []int{25}},
		"SMBService.sc/cifs":                   {ID: "cifs", File: "SMBService.sc", Title: "cifs", TitleKey: "firewall:firewall_service_opt_smb", Desc: "cifs", Ports: []int{137, 138, 139, 445}},
		"Virtualization.sc/synoccc":            {ID: "synoccc", File: "Virtualization.sc", Title: "Virtual Machine Manager", Desc: "Virtual Machine Manager", Ports: []int{2385, 16509}},
		"docker_media.sc/media_tcp":            {ID: "media_tcp", File: "docker_media.sc", Title: "Docker(TCP)", Desc: "Docker media", Ports: []int{8920, 18096}},
		"odd.sc/odd":                           {ID: "odd", File: "odd.sc", Title: `Odd "quoted" title`},
		"odd.sc/odd_all":                       {ID: "odd_all", File: "odd.sc", Ports: []int{8082}},
	}
	if !reflect.DeepEqual(got, want) {
		for k, w := range want {
			if !reflect.DeepEqual(got[k], w) {
				t.Errorf("%s = %+v, want %+v", k, got[k], w)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("unexpected %s", k)
			}
		}
	}
}

// DSM's live synow3.sc hides the factory default of the same name, and a
// default stands in for a file DSM's own directory lacks.
func TestReadPrefersEarlierDirectory(t *testing.T) {
	for _, d := range Read("testdata/syno", "testdata/defaults") {
		if d.ID == "dms" && !reflect.DeepEqual(d.Ports, []int{5050}) {
			t.Fatalf("dms = %v, want the live port", d.Ports)
		}
	}
	if defs := Read("testdata/defaults"); len(defs) != 2 || defs[0].Ports[0] != 5000 {
		t.Fatalf("defaults alone = %+v", defs)
	}
}

func TestParseToleratesOddInput(t *testing.T) {
	raw := "[a]\ntitle=\"x\x01y\"\ndst.ports=\"80/tcp\n[b\ndst.ports=\"81/tcp\"\n" + strings.Repeat("x", 100) + "\n[c]\ndst.ports=\"3240-3259/tcp\"\n"
	defs := Parse("f.sc", []byte(raw))
	if len(defs) != 2 || defs[0].Title != "xy" || defs[1].ID != "c" || len(defs[1].Ports) != 0 {
		t.Fatalf("defs = %+v", defs)
	}
	// An unterminated quote keeps the raw value, so "80/tcp still has no valid port.
	if len(defs[0].Ports) != 0 {
		t.Fatalf("ports = %v", defs[0].Ports)
	}
}
