package idgateway

import (
	"errors"
	"fmt"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

// UnixUpstreamVersion identifies the unix socket upstream contract: the
// gateway "upstream" may be "unix:/var/packages/<Package>/var/run/<name>.sock",
// this package's private SDK IPC socket (see ipc.ServeUnix).
const UnixUpstreamVersion = 1

const unixUpstreamPrefix = "unix:"

// parseUnixUpstream returns the socket path of a unix: upstream, or "" for any
// other upstream (validated by the caller). A unix upstream names exactly this
// package's var/run socket and never combines with Compose ingress.
func parseUnixUpstream(pkg, upstream string, compose bool) (string, error) {
	path, ok := strings.CutPrefix(upstream, unixUpstreamPrefix)
	if !ok {
		return "", nil
	}
	if compose {
		return "", errors.New("unix upstream cannot be combined with compose_ingress")
	}
	if err := ipc.ValidPackageSocket(pkg, path); err != nil {
		return "", fmt.Errorf("upstream %q must be unix:/var/packages/%s/var/run/<name>.sock: %w", upstream, pkg, err)
	}
	return path, nil
}
