package haproxy

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/isucon/isucon2026/router/internal/domain"
)

// fakeRuntimeAPI answers the map commands like HAProxy's runtime API, one
// command per connection.
type fakeRuntimeAPI struct {
	mu       sync.Mutex
	maps     map[string]map[string]string
	commands []string
	answer   string
}

func startFakeRuntimeAPI(t *testing.T, mapPath string) (*fakeRuntimeAPI, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	api := &fakeRuntimeAPI{maps: map[string]map[string]string{mapPath: {}}}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadString('\n')
			_, _ = conn.Write([]byte(api.handle(strings.TrimSpace(line))))
			_ = conn.Close()
		}
	}()
	return api, listener.Addr().String()
}

func (a *fakeRuntimeAPI) handle(command string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commands = append(a.commands, command)
	if a.answer != "" {
		return a.answer
	}
	fields := strings.Fields(command)
	if len(fields) < 4 || fields[1] != "map" {
		return "Unknown command.\n"
	}
	entries, ok := a.maps[fields[2]]
	if !ok {
		return "Unknown map identifier. Please use #<id> or <file>.\n"
	}
	switch fields[0] {
	case "get":
		value, found := entries[fields[3]]
		if !found {
			return "type=str, case=sensitive, found=no\n\n"
		}
		return fmt.Sprintf("type=str, case=sensitive, found=yes, idx=tree, key=%q, value=%q, type=\"str\"\n\n", fields[3], value)
	case "set":
		if _, found := entries[fields[3]]; !found {
			return "entry not found.\n\n"
		}
		entries[fields[3]] = fields[4]
	case "add":
		entries[fields[3]] = fields[4]
	}
	return "\n"
}

func (a *fakeRuntimeAPI) log() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.commands...)
}

func testRuntime(t *testing.T, address string) *Runtime {
	cfg := testConfig(t, map[string]string{"ROUTER_HAPROXY_DIR": "/run/test-haproxy", "ROUTER_HAPROXY_RUNTIME_ADDRESS": address})
	return NewRuntime(cfg)
}

func TestRuntimeAddsThenSetsDefaultCountry(t *testing.T) {
	t.Parallel()
	api, address := startFakeRuntimeAPI(t, "/run/test-haproxy/default-country.map")
	runtime := testRuntime(t, address)
	ctx := context.Background()
	jp := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	if err := runtime.Serving(ctx, jp); err == nil {
		t.Fatal("empty map reported as serving")
	}
	if err := runtime.Apply(ctx, jp); err != nil {
		t.Fatal(err)
	}
	us := jp
	us.DefaultCountry = "US"
	if err := runtime.Apply(ctx, us); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Serving(ctx, us); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"get map /run/test-haproxy/default-country.map default",
		"set map /run/test-haproxy/default-country.map default JP",
		"add map /run/test-haproxy/default-country.map default JP",
		"get map /run/test-haproxy/default-country.map default",
		"set map /run/test-haproxy/default-country.map default US",
		"get map /run/test-haproxy/default-country.map default",
		"get map /run/test-haproxy/default-country.map default",
	}
	if got := api.log(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands:\n%s", strings.Join(got, "\n"))
	}
}

func TestRuntimeReportsErrors(t *testing.T) {
	t.Parallel()
	cfg := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	api, address := startFakeRuntimeAPI(t, "/somewhere/else.map")
	if err := testRuntime(t, address).Apply(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "Unknown map identifier") {
		t.Fatalf("unknown map: %v", err)
	}
	api.answer = "Permission denied\n"
	if err := testRuntime(t, address).Serving(context.Background(), cfg); err == nil {
		t.Fatal("unexpected answer accepted")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	_ = listener.Close()
	if err := testRuntime(t, closed).Serving(context.Background(), cfg); err == nil {
		t.Fatal("stopped HAProxy reported as serving")
	}
	cfg.DefaultCountry = "XX"
	if err := testRuntime(t, address).Apply(context.Background(), cfg); err == nil {
		t.Fatal("unsupported country accepted")
	}
}
