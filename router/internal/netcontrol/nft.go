package netcontrol

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

const nftTableName = "isureal_router"

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// RenderNFT allows forwarding between every pair of server subnets. Matching on
// addresses instead of interfaces keeps the rules valid when all servers share
// one router interface and packets leave through the interface they came in on.
func RenderNFT(networks map[domain.ServerID]config.ServerNetwork, tableExists bool) (string, error) {
	if err := validateNetworks(networks); err != nil {
		return "", err
	}
	var builder strings.Builder
	if tableExists {
		fmt.Fprintf(&builder, "flush table inet %s\n\n", nftTableName)
	}
	fmt.Fprintf(&builder, "table inet %s {\n", nftTableName)
	builder.WriteString("    chain forward {\n")
	builder.WriteString("        type filter hook forward priority filter; policy drop;\n")
	builder.WriteString("        ct state established,related accept\n")
	for _, source := range domain.ServerIDs() {
		for _, destination := range domain.ServerIDs() {
			if source == destination {
				continue
			}
			fmt.Fprintf(&builder, "        ip saddr %s ip daddr %s counter accept comment %q\n", networks[source].Subnet, networks[destination].Subnet, source+"-to-"+destination)
		}
	}
	builder.WriteString("    }\n")
	builder.WriteString("}\n")
	return builder.String(), nil
}

// Several servers may share one interface when the VPC routes every server
// subnet through a single router interface.
func validateInterfaces(interfaces map[domain.ServerID]string) error {
	for _, server := range domain.ServerIDs() {
		name, ok := interfaces[server]
		if !ok || !interfaceNamePattern.MatchString(name) {
			return fmt.Errorf("%s interface name is missing or unsafe", server)
		}
	}
	return nil
}

func validateNetworks(networks map[domain.ServerID]config.ServerNetwork) error {
	for _, server := range domain.ServerIDs() {
		network, ok := networks[server]
		if !ok || !network.ServerIP.Is4() || !network.Subnet.IsValid() || !network.Subnet.Addr().Is4() {
			return fmt.Errorf("%s network is missing or invalid", server)
		}
	}
	return nil
}
