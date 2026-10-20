package compose

import (
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/synopackageland/syno-pkg-land-sdk/internal/composeingress"
)

const ingressNetwork = "sdk_ingress"

type Ingress struct {
	composeingress.Allocation
	Candidate int    `json:"candidate"`
	Service   string `json:"service"`
	Port      uint32 `json:"port"`
}

func addDirectIngress(project *types.Project, r Request) (*Ingress, error) {
	if r.Web == nil || r.Services[r.Web.Service].Network == "host" {
		return nil, nil
	}
	if _, ok := project.Networks[ingressNetwork]; ok {
		return nil, problem("COMPOSE_NETWORK_UNSUPPORTED", "sdk_ingress is reserved for SDK ingress")
	}
	a, err := composeingress.Allocate(r.Package, r.Network.IngressCandidate)
	if err != nil {
		return nil, err
	}
	project.Networks[ingressNetwork] = types.NetworkConfig{Name: project.Name + "_" + ingressNetwork, Internal: true, Driver: "bridge", Ipam: types.IPAMConfig{Config: []*types.IPAMPool{{Subnet: a.Subnet}}}}
	web := project.Services[r.Web.Service]
	if web.Networks == nil {
		web.Networks = map[string]*types.ServiceNetworkConfig{}
	}
	web.Networks[ingressNetwork] = &types.ServiceNetworkConfig{Ipv4Address: a.Address}
	project.Services[r.Web.Service] = web
	return &Ingress{a, r.Network.IngressCandidate, r.Web.Service, r.Web.Port}, nil
}
