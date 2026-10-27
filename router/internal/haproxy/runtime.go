package haproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

// Runtime sets the default country through the HAProxy runtime API, which
// answers one command per connection.
type Runtime struct {
	address string
	mapPath string
	timeout time.Duration
}

func NewRuntime(cfg config.Config) *Runtime {
	return &Runtime{
		address: cfg.HAProxyRuntimeAddress,
		mapPath: filepath.Join(cfg.HAProxyDir, DefaultCountryMap),
		timeout: cfg.CommandTimeout,
	}
}

// Apply makes HAProxy use the snapshot's default country and reads it back.
func (r *Runtime) Apply(ctx context.Context, routerConfig domain.Config) error {
	if _, ok := domain.CountryRegion(routerConfig.DefaultCountry); !ok {
		return errors.New("default country is unsupported")
	}
	output, err := r.command(ctx, fmt.Sprintf("set map %s %s %s", r.mapPath, defaultCountryKey, routerConfig.DefaultCountry))
	if err != nil {
		return err
	}
	// HAProxy starts with an empty map, so the first snapshot adds the entry.
	if strings.Contains(output, "entry not found") {
		output, err = r.command(ctx, fmt.Sprintf("add map %s %s %s", r.mapPath, defaultCountryKey, routerConfig.DefaultCountry))
		if err != nil {
			return err
		}
	}
	if output = strings.TrimSpace(output); output != "" {
		return fmt.Errorf("HAProxy rejected the default country: %s", bounded(output))
	}
	return r.Serving(ctx, routerConfig)
}

// Serving reports whether HAProxy uses the snapshot's default country.
func (r *Runtime) Serving(ctx context.Context, routerConfig domain.Config) error {
	country, err := r.defaultCountry(ctx)
	if err != nil {
		return err
	}
	if country != routerConfig.DefaultCountry {
		return fmt.Errorf("HAProxy default country is %q, want %q", country, routerConfig.DefaultCountry)
	}
	return nil
}

func (r *Runtime) defaultCountry(ctx context.Context) (string, error) {
	output, err := r.command(ctx, fmt.Sprintf("get map %s %s", r.mapPath, defaultCountryKey))
	if err != nil {
		return "", err
	}
	if strings.Contains(output, "found=no") {
		return "", nil
	}
	_, value, found := strings.Cut(output, `value="`)
	if found {
		value, _, found = strings.Cut(value, `"`)
	}
	if !found {
		return "", fmt.Errorf("unexpected HAProxy answer: %s", bounded(output))
	}
	return value, nil
}

func (r *Runtime) command(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", r.address)
	if err != nil {
		return "", fmt.Errorf("HAProxy runtime API: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if _, err := io.WriteString(conn, command+"\n"); err != nil {
		return "", fmt.Errorf("HAProxy runtime API: %w", err)
	}
	output, err := io.ReadAll(io.LimitReader(conn, 64<<10))
	if err != nil {
		return "", fmt.Errorf("HAProxy runtime API: %w", err)
	}
	return string(output), nil
}

func bounded(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 256 {
		return text[:256]
	}
	return text
}
