package bridge

import (
	"encoding/json"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// DescribeRuntimeRoutes constructs the exact bridge runtime route set without
// starting the App, opening stores, authenticating or executing any handler.
// The CLI supplies its generated manifest; payload digests are not needed to
// describe routes. Runtime still parses and verifies the complete manifest.
func DescribeRuntimeRoutes(raw, permissions []byte, portalExecutor bool) ([]map[string]string, error) {
	if len(raw) > MaxManifestBytes {
		return nil, fmt.Errorf("bridge manifest exceeds limit")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if err := api.ValidateAppMethods(m.Methods); err != nil {
		return nil, err
	}
	cfg := Config{PackageID: "Description", DataDir: ".", PortalExecutor: portalExecutor}
	session := &Session{Methods: m.Methods, Transfers: m.Transfers}
	return api.DescribeRoutes(permissions, append(Routes(session), cfg.services(m).routes()...)...)
}
