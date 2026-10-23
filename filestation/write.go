package filestation

import (
	"context"
	"net/http"
	"strings"
)

func validName(n string) bool {
	return n != "" && n != "." && n != ".." && len(n) <= 255 && !strings.ContainsAny(n, "/\\\x00\r\n,\"")
}
func (*Client) Create(context.Context, *http.Request, string, string) error {
	return fail("unsupported")
}
