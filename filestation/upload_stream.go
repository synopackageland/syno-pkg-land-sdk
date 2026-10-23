package filestation

import (
	"context"
	"io"
	"net/http"
)

const MaxUploadBytes int64 = 16 << 30

// UploadStream refuses the retired relay without reading source. Browser
// uploads go directly to DSM; approved backend adapters implement DSMClient.
func (*Client) UploadStream(context.Context, *http.Request, string, string, io.Reader, int64) error {
	return fail("unsupported")
}
