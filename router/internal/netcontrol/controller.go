package netcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

type Candidate struct {
	Config     domain.Config
	Interfaces map[domain.ServerID]string
	TCBatches  map[domain.ServerID]string
}

type Controller struct {
	config   config.Config
	executor Executor
	resolver InterfaceResolver
	mu       sync.Mutex
	active   *Candidate
}

func NewController(cfg config.Config, executor Executor, resolver InterfaceResolver) *Controller {
	return &Controller{config: cfg, executor: executor, resolver: resolver}
}

func (c *Controller) Prepare(routerConfig domain.Config) (Candidate, error) {
	if err := routerConfig.Validate(); err != nil {
		return Candidate{}, err
	}
	interfaces := make(map[domain.ServerID]string, len(domain.ServerIDs()))
	for _, server := range domain.ServerIDs() {
		name, err := c.resolver.NameByIPv4(c.config.Servers[server].RouterIP)
		if err != nil {
			return Candidate{}, fmt.Errorf("resolve %s interface: %w", server, err)
		}
		interfaces[server] = name
	}
	if err := validateInterfaces(interfaces); err != nil {
		return Candidate{}, err
	}
	tcBatches, err := RenderTC(interfaces, c.config.Servers, routerConfig, c.config.Traffic)
	if err != nil {
		return Candidate{}, fmt.Errorf("render tc: %w", err)
	}
	return Candidate{Config: routerConfig, Interfaces: interfaces, TCBatches: tcBatches}, nil
}

func (c *Controller) Apply(ctx context.Context, candidate Candidate) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil && c.active.Config == candidate.Config && equalInterfaces(c.active.Interfaces, candidate.Interfaces) {
		return nil
	}
	if err := c.applyRaw(ctx, candidate); err != nil {
		if c.active == nil {
			return err
		}
		rollbackErr := c.applyRaw(ctx, *c.active)
		if rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback network: %w", rollbackErr))
		}
		return err
	}
	copyCandidate := candidate
	c.active = &copyCandidate
	return nil
}

func (c *Controller) Restore(ctx context.Context, candidate *Candidate) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if candidate == nil {
		if c.active == nil {
			return nil
		}
		var restoreErr error
		if err := c.run(ctx, Command{Name: "nft", Args: []string{"delete", "table", "inet", nftTableName}}); err != nil {
			restoreErr = errors.Join(restoreErr, err)
		}
		for _, server := range domain.ServerIDs() {
			if err := c.run(ctx, Command{Name: "tc", Args: []string{"qdisc", "del", "dev", c.active.Interfaces[server], "root"}}); err != nil {
				restoreErr = errors.Join(restoreErr, err)
			}
		}
		c.active = nil
		return restoreErr
	}
	if err := c.applyRaw(ctx, *candidate); err != nil {
		return err
	}
	copyCandidate := *candidate
	c.active = &copyCandidate
	return nil
}

func (c *Controller) Active() *Candidate {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return nil
	}
	copyCandidate := *c.active
	return &copyCandidate
}

func (c *Controller) applyRaw(ctx context.Context, candidate Candidate) error {
	if err := c.ensureIPv4Forwarding(ctx); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	tableExists := c.tableExists(ctx)
	rules, err := RenderNFT(candidate.Interfaces, tableExists)
	if err != nil {
		return err
	}
	if err := c.run(ctx, Command{Name: "nft", Args: []string{"--check", "-f", "-"}, Stdin: rules}); err != nil {
		return fmt.Errorf("validate nft rules: %w", err)
	}
	if err := c.run(ctx, Command{Name: "nft", Args: []string{"-f", "-"}, Stdin: rules}); err != nil {
		return fmt.Errorf("apply nft rules: %w", err)
	}
	for _, server := range domain.ServerIDs() {
		batch, ok := candidate.TCBatches[server]
		if !ok {
			return fmt.Errorf("missing tc batch for %s", server)
		}
		// HTB does not support replacing an existing root once it has child
		// classes/qdiscs. Recreate the dedicated tree for every transaction;
		// the caller reapplies the last-known-good candidate on failure.
		_, _ = c.runOutput(ctx, Command{Name: "tc", Args: []string{"qdisc", "del", "dev", candidate.Interfaces[server], "root"}})
		if err := c.run(ctx, Command{Name: "tc", Args: []string{"-batch", "-"}, Stdin: batch}); err != nil {
			return fmt.Errorf("apply tc for %s: %w", server, err)
		}
		if err := c.run(ctx, Command{Name: "tc", Args: []string{"qdisc", "show", "dev", candidate.Interfaces[server]}}); err != nil {
			return fmt.Errorf("verify tc for %s: %w", server, err)
		}
	}
	return nil
}

func (c *Controller) ensureIPv4Forwarding(ctx context.Context) error {
	output, readErr := c.runOutput(ctx, Command{Name: "sysctl", Args: []string{"-n", "net.ipv4.ip_forward"}})
	if readErr == nil && strings.TrimSpace(string(output)) == "1" {
		return nil
	}
	if err := c.run(ctx, Command{Name: "sysctl", Args: []string{"-w", "net.ipv4.ip_forward=1"}}); err != nil {
		if readErr != nil {
			return errors.Join(fmt.Errorf("read current value: %w", readErr), err)
		}
		return err
	}
	return nil
}

func (c *Controller) tableExists(ctx context.Context) bool {
	commandCtx, cancel := context.WithTimeout(ctx, c.config.CommandTimeout)
	defer cancel()
	_, err := c.executor.Run(commandCtx, Command{Name: "nft", Args: []string{"list", "table", "inet", nftTableName}})
	return err == nil
}

func (c *Controller) run(ctx context.Context, command Command) error {
	_, err := c.runOutput(ctx, command)
	return err
}

func (c *Controller) runOutput(ctx context.Context, command Command) ([]byte, error) {
	timeout := c.config.CommandTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.executor.Run(commandCtx, command)
}

func equalInterfaces(left, right map[domain.ServerID]string) bool {
	for _, server := range domain.ServerIDs() {
		if left[server] != right[server] {
			return false
		}
	}
	return true
}
