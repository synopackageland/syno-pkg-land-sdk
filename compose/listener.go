package compose

import (
	"net"
	"strconv"

	"github.com/compose-spec/compose-go/v2/types"
)

// Listener injects only NAS IPv4 loopback and the declared web.port. It cannot
// publish/remap a port or change topology. AddressEnv remains the legacy host form.
type Listener struct {
	Env    string `json:"env"`
	Format string `json:"format"`
}

func validateListener(p ServicePolicy) error {
	if p.AddressEnv != "" && !envName.MatchString(p.AddressEnv) || p.Listener != nil && (p.Network != "host" || p.AddressEnv != "" || !envName.MatchString(p.Listener.Env) || p.Listener.Format != "host" && p.Listener.Format != "host-port") {
		return problem("COMPOSE_OIDC_TOPOLOGY_UNSUPPORTED", "listener needs explicit host networking, a valid env and host or host-port format; do not also declare address_env")
	}
	return nil
}

func applyListener(s *types.ServiceConfig, p ServicePolicy, port uint32) {
	key, value := p.AddressEnv, "127.0.0.1"
	if p.Listener != nil {
		key = p.Listener.Env
		if p.Listener.Format == "host-port" {
			value = net.JoinHostPort(value, strconv.Itoa(int(port)))
		}
	}
	setEnv(s, key, value)
}
