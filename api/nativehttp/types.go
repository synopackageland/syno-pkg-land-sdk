// Package nativehttp validates and exports native HTTP metadata. It never executes
// bindings, accepts credentials, or creates a listener. All execution is client-side.
package nativehttp

import (
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/idgateway"
)

const Version = 3
const Capability = "native-http-v1"

// Features is the complete matching-client capability marker. Use a copy when exporting.
func Features() []string {
	return []string{Capability, "auth-header-v1", "auth-greader-client-login-v1", "paginate-http-v1", "ndjson-http-v1", "upload-http-v1", "download-file-v1"}
}

type Registry struct {
	Format     string               `json:"format"`
	Version    int                  `json:"version"`
	NativeHTTP NativeHTTP           `json:"nativeHttp"`
	Operations map[string]Operation `json:"operations"`
}
type NativeHTTP struct {
	BasePath string `json:"basePath"`
	Auth     Auth   `json:"auth"`
	Limits   Limits `json:"limits"`
}
type Limits struct {
	MaxResponseBytes int64 `json:"maxResponseBytes"`
	MaxTransferBytes int64 `json:"maxTransferBytes"`
}

// Auth's closed tagged union is enforced by registry.schema.json.
type Auth struct {
	Type            string        `json:"type"`
	Header          string        `json:"header,omitempty"`
	Prefix          string        `json:"prefix,omitempty"`
	CredentialPage  string        `json:"credentialPage"`
	Instructions    string        `json:"instructions"`
	Verify          *Verification `json:"verify,omitempty"`
	ExchangePath    string        `json:"exchangePath,omitempty"`
	IdentityPath    string        `json:"identityPath,omitempty"`
	ActionTokenPath string        `json:"actionTokenPath,omitempty"`
}
type Verification struct {
	Method   string   `json:"method"`
	Path     string   `json:"path"`
	Response Response `json:"response"`
}
type Binding struct {
	Param       string          `json:"param,omitempty"`
	Literal     json.RawMessage `json:"literal,omitempty"`
	ActionToken bool            `json:"actionToken,omitempty"`
}
type Projection struct {
	Pointer *string           `json:"pointer,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
}
type Response struct {
	Statuses     []int       `json:"statuses"`
	Decode       string      `json:"decode"`
	MaxBytes     int64       `json:"maxBytes"`
	ContentTypes []string    `json:"contentTypes"`
	Project      *Projection `json:"project,omitempty"`
}
type HTTPBinding struct {
	Method   string             `json:"method"`
	Path     string             `json:"path"`
	Query    map[string]Binding `json:"query,omitempty"`
	Headers  map[string]string  `json:"headers,omitempty"`
	Body     *Body              `json:"body,omitempty"`
	Response Response           `json:"response"`
}
type Body struct {
	Encoding string             `json:"encoding"`
	Fields   map[string]Binding `json:"fields"`
}
type Flow struct {
	Type         string      `json:"type"`
	Method       string      `json:"method"`
	Pagination   *Pagination `json:"pagination,omitempty"`
	FileParam    string      `json:"fileParam,omitempty"`
	OutputParam  string      `json:"outputParam,omitempty"`
	Encoding     string      `json:"encoding,omitempty"`
	Part         string      `json:"part,omitempty"`
	MaxBytes     int64       `json:"maxBytes,omitempty"`
	ContentTypes []string    `json:"contentTypes,omitempty"`
	MaxLines     int         `json:"maxLines,omitempty"`
	MaxLineBytes int64       `json:"maxLineBytes,omitempty"`
}
type Pagination struct {
	Mode        string `json:"mode"`
	PageParam   string `json:"pageParam,omitempty"`
	CursorParam string `json:"cursorParam,omitempty"`
	SizeParam   string `json:"sizeParam"`
	Items       string `json:"items"`
	Next        string `json:"next"`
	PageSize    int    `json:"pageSize"`
	MaxPages    int    `json:"maxPages"`
	MaxItems    int    `json:"maxItems"`
	MaxBytes    int64  `json:"maxBytes"`
}
type Operation struct {
	Summary      string          `json:"summary"`
	CLI          []string        `json:"cli"`
	Effect       string          `json:"effect"`
	Permission   string          `json:"permission"`
	AI           string          `json:"ai"`
	Kind         string          `json:"kind"`
	ParamsSchema json.RawMessage `json:"paramsSchema"`
	ResultSchema json.RawMessage `json:"resultSchema"`
	Flow         Flow            `json:"flow"`
	HTTP         HTTPBinding     `json:"http"`
	Preview      string          `json:"preview,omitempty"`
}
type Package struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	DisplayName string `json:"displayName"`
}
type Provenance struct {
	DevelopedBy string `json:"developedBy"`
	Generator   string `json:"generator"`
}
type InventoryBinding struct {
	Effect     string      `json:"effect"`
	Permission string      `json:"permission"`
	Kind       string      `json:"kind"`
	Flow       Flow        `json:"flow"`
	HTTP       HTTPBinding `json:"http"`
}
type Inventory struct {
	UpstreamAuthPaths []string                    `json:"upstreamAuthPaths"`
	Format            string                      `json:"format"`
	Version           int                         `json:"version"`
	Package           Package                     `json:"package"`
	Entry             string                      `json:"entry"`
	EntryPolicyDigest string                      `json:"entryPolicyDigest"`
	BasePath          string                      `json:"basePath"`
	RegistrySHA256    string                      `json:"registrySha256"`
	AuthSHA256        string                      `json:"authSha256"`
	Bindings          map[string]InventoryBinding `json:"bindings"`
}
type Execution struct {
	Type string `json:"type"`
	NativeHTTP
}
type Transport struct {
	Path string `json:"path"`
	Auth string `json:"auth"`
}
type Payload struct {
	Package           Package                     `json:"package"`
	Aliases           []string                    `json:"aliases"`
	Provenance        Provenance                  `json:"provenance"`
	Entry             string                      `json:"entry"`
	Transport         Transport                   `json:"transport"`
	Execution         Execution                   `json:"execution"`
	Features          []string                    `json:"features"`
	EntryPolicyDigest string                      `json:"entryPolicyDigest"`
	RegistrySHA256    string                      `json:"registrySha256"`
	InventorySHA256   string                      `json:"inventorySha256"`
	AuthSHA256        string                      `json:"authSha256"`
	Bindings          map[string]InventoryBinding `json:"bindings"`
	Commands          map[string]Operation        `json:"commands"`
}
type Descriptor struct {
	Format  string  `json:"format"`
	Version int     `json:"version"`
	Payload Payload `json:"payload"`
	SHA256  string  `json:"sha256"`
}

// BuildConfig contains trusted resolved package settings, never request data.
// Gateway is the unscoped, validated build config used by the identity gateway.
type BuildConfig struct {
	Package    Package          `json:"package"`
	Aliases    []string         `json:"aliases"`
	Provenance Provenance       `json:"provenance"`
	Backend    string           `json:"backend"`
	Transport  string           `json:"transport"`
	Gateway    idgateway.Config `json:"gateway"`
}
type Artifacts struct {
	Descriptor Descriptor `json:"descriptor"`
	Inventory  Inventory  `json:"inventory"`
}

// MarshalJSON retains explicit empty optional maps, which participate in JCS hashes.
func (h HTTPBinding) MarshalJSON() ([]byte, error) {
	type wire struct {
		Method   string              `json:"method"`
		Path     string              `json:"path"`
		Query    *map[string]Binding `json:"query,omitempty"`
		Headers  *map[string]string  `json:"headers,omitempty"`
		Body     *Body               `json:"body,omitempty"`
		Response Response            `json:"response"`
	}
	w := wire{Method: h.Method, Path: h.Path, Body: h.Body, Response: h.Response}
	if h.Query != nil {
		w.Query = &h.Query
	}
	if h.Headers != nil {
		w.Headers = &h.Headers
	}
	return json.Marshal(w)
}
