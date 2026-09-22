package netcontrol

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
)

type Command struct {
	Name  string
	Args  []string
	Stdin string
}

type Executor interface {
	Run(context.Context, Command) ([]byte, error)
}

type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, command Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	if command.Stdin != "" {
		cmd.Stdin = strings.NewReader(command.Stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed: %w: %s", command.Name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type InterfaceResolver interface {
	NameByIPv4(netip.Addr) (string, error)
}

type OSInterfaceResolver struct{}

func (OSInterfaceResolver) NameByIPv4(target netip.Addr) (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list interfaces: %w", err)
	}
	var match string
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return "", fmt.Errorf("list addresses for %s: %w", iface.Name, err)
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil || prefix.Addr() != target {
				continue
			}
			if match != "" {
				return "", fmt.Errorf("IPv4 %s exists on multiple interfaces", target)
			}
			match = iface.Name
		}
	}
	if match == "" {
		return "", fmt.Errorf("no interface has IPv4 %s", target)
	}
	return match, nil
}
