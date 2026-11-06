package nativehttp

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Export builds metadata from resolved package settings. Compose/native have
// identical binding semantics and neither requires Go business routes.
func Export(raw []byte, c BuildConfig) (*Artifacts, error) {
	r, e := ParseRegistry(raw, c)
	if e != nil {
		return nil, e
	}
	registryHash, e := Digest(raw)
	if e != nil {
		return nil, e
	}
	// Ensure every accepted wire value survives the public data types.
	roundHash, e := digestValue(r)
	if e != nil || roundHash != registryHash {
		return nil, errors.New("registry serialization changed canonical semantics")
	}
	policyHash := c.Gateway.EntryPolicyDigest()
	authHash, e := digestValue(map[string]any{"basePath": r.NativeHTTP.BasePath, "auth": r.NativeHTTP.Auth, "entryPolicyDigest": policyHash})
	if e != nil {
		return nil, e
	}
	entry := c.Gateway.Entry
	if entry == "" {
		entry = "policy"
	}
	bindings := inventoryBindings(r.Operations)
	inv := Inventory{Format: "synopkgland.http-inventory", Version: 1, Package: c.Package, Entry: entry, EntryPolicyDigest: policyHash, BasePath: r.NativeHTTP.BasePath, RegistrySHA256: registryHash, AuthSHA256: authHash, Bindings: bindings, UpstreamAuthPaths: append([]string{}, c.Gateway.UpstreamAuthPaths...)}
	inventoryHash, e := digestValue(inv)
	if e != nil {
		return nil, e
	}
	aliases := append([]string{}, c.Aliases...)
	d := Descriptor{Format: "synopkgland.api", Version: 3, Payload: Payload{Package: c.Package, Aliases: aliases, Provenance: c.Provenance, Entry: entry, Transport: Transport{Path: "/webman/3rdparty/" + c.Package.ID + "/cgi/api.cgi", Auth: "dsm-session"}, Execution: Execution{Type: "native-http", NativeHTTP: r.NativeHTTP}, Features: Features(), EntryPolicyDigest: policyHash, RegistrySHA256: registryHash, InventorySHA256: inventoryHash, AuthSHA256: authHash, Bindings: bindings, Commands: r.Operations}}
	d.SHA256, e = descriptorDigest(d)
	if e != nil {
		return nil, e
	}
	db, e := json.Marshal(d)
	if e != nil {
		return nil, e
	}
	ib, e := json.Marshal(inv)
	if e != nil {
		return nil, e
	}
	return ValidateArtifacts(db, ib)
}
func inventoryBindings(ops map[string]Operation) map[string]InventoryBinding {
	out := map[string]InventoryBinding{}
	for name, o := range ops {
		out[name] = InventoryBinding{Effect: o.Effect, Permission: o.Permission, Kind: o.Kind, Flow: o.Flow, HTTP: o.HTTP}
	}
	return out
}
func descriptorDigest(d Descriptor) (string, error) {
	return digestValue(struct {
		Format  string  `json:"format"`
		Version int     `json:"version"`
		Payload Payload `json:"payload"`
	}{d.Format, d.Version, d.Payload})
}
func same(a, b any) bool {
	x, e := digestValue(a)
	if e != nil {
		return false
	}
	y, e := digestValue(b)
	return e == nil && x == y
}

// ValidateArtifacts rechecks packaged metadata, digest equality, fixed discovery
// path, features, binding semantics and upstream path coverage without App code.
// Build/inspect must also compare EntryPolicyDigest to the resolved gateway config
// and Package to final INFO; the authenticated descriptor is not a signature.
func ValidateArtifacts(descriptor, inventory []byte) (*Artifacts, error) {
	if e := ValidateShape("descriptor", descriptor); e != nil {
		return nil, e
	}
	if e := ValidateShape("inventory", inventory); e != nil {
		return nil, e
	}
	var a Artifacts
	if e := json.Unmarshal(descriptor, &a.Descriptor); e != nil {
		return nil, e
	}
	if e := json.Unmarshal(inventory, &a.Inventory); e != nil {
		return nil, e
	}
	d, i := a.Descriptor, a.Inventory
	p := d.Payload
	invalid := func() (*Artifacts, error) { return nil, errors.New("native HTTP packaged metadata mismatch") }
	if p.Transport.Path != "/webman/3rdparty/"+p.Package.ID+"/cgi/api.cgi" || !same(p.Package, i.Package) || p.Entry != i.Entry || p.EntryPolicyDigest != i.EntryPolicyDigest || p.Execution.BasePath != i.BasePath || p.RegistrySHA256 != i.RegistrySHA256 || p.AuthSHA256 != i.AuthSHA256 || !same(p.Bindings, i.Bindings) || !same(p.Bindings, inventoryBindings(p.Commands)) {
		return invalid()
	}
	if !same(p.Features, Features()) {
		return invalid()
	}
	r := Registry{Format: "synopkgland.api", Version: 3, NativeHTTP: p.Execution.NativeHTTP, Operations: p.Commands}
	rb, e := json.Marshal(r)
	if e != nil {
		return nil, e
	}
	if e = ValidateShape("registry", rb); e != nil {
		return nil, e
	}
	if e = validateRegistry(&r, p.Entry, i.UpstreamAuthPaths); e != nil {
		return nil, e
	}
	rh, e := Digest(rb)
	if e != nil || rh != p.RegistrySHA256 {
		return invalid()
	}
	ah, e := digestValue(map[string]any{"basePath": r.NativeHTTP.BasePath, "auth": r.NativeHTTP.Auth, "entryPolicyDigest": p.EntryPolicyDigest})
	if e != nil || ah != p.AuthSHA256 {
		return invalid()
	}
	ih, e := Digest(inventory)
	if e != nil || ih != p.InventorySHA256 {
		return invalid()
	}
	dh, e := descriptorDigest(d)
	if e != nil || dh != d.SHA256 {
		return invalid()
	}
	// Detect wire values that a typed decoder might otherwise discard.
	var unsigned map[string]json.RawMessage
	if e = json.Unmarshal(descriptor, &unsigned); e != nil {
		return nil, e
	}
	delete(unsigned, "sha256")
	uh, e := digestValue(unsigned)
	if e != nil || uh != d.SHA256 {
		return invalid()
	}
	return &a, nil
}

// Encode returns stable pretty JSON for build tools. Canonical hashing is separate.
func (a *Artifacts) Encode() (descriptor, inventory []byte, err error) {
	if a == nil {
		return nil, nil, fmt.Errorf("nil metadata artifacts")
	}
	descriptor, err = json.MarshalIndent(a.Descriptor, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	inventory, err = json.MarshalIndent(a.Inventory, "", "  ")
	return
}
