package netcontrol

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/isucon/isucon2026/router/internal/domain"
)

const nftTableName = "isureal_router"

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

func RenderNFT(interfaces map[domain.ServerID]string, tableExists bool) (string, error) {
	if err := validateInterfaces(interfaces); err != nil {
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
			fmt.Fprintf(&builder, "        iifname %q oifname %q counter accept comment %q\n", interfaces[source], interfaces[destination], source+"-to-"+destination)
		}
	}
	builder.WriteString("    }\n")
	builder.WriteString("}\n")
	return builder.String(), nil
}

func validateInterfaces(interfaces map[domain.ServerID]string) error {
	seen := make(map[string]struct{}, 3)
	for _, server := range domain.ServerIDs() {
		name, ok := interfaces[server]
		if !ok || !interfaceNamePattern.MatchString(name) {
			return fmt.Errorf("%s interface name is missing or unsafe", server)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("interface %q is assigned more than once", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}
