package filestation

import (
	"context"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"net/http"
	"strings"
)

func validName(n string) bool {
	return n != "" && n != "." && n != ".." && len(n) <= 255 && !strings.ContainsAny(n, "/\\\x00\r\n,\"")
}
func (c *Client) Create(ctx context.Context, r *http.Request, parent, name string) error {
	if !validPath(parent) || !validName(name) {
		return fail("invalid-request")
	}
	v := fields("SYNO.FileStation.CreateFolder", "create", "2")
	v.Set("folder_path", array(parent))
	v.Set("name", array(name))
	v.Set("force_parent", "false")
	return mutationOutcome(c.call(ctx, r, v, nil))
}

// An unreadable response cannot establish whether DSM committed the write.
func mutationOutcome(err error) error {
	var e *api.Error
	if errors.As(err, &e) && (e.Code == "unavailable" || e.Code == "canceled") {
		return fail("commit-uncertain")
	}
	return err
}
