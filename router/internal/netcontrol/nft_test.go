package netcontrol

import (
	"strings"
	"testing"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

func TestRenderNFTAllowsOnlyTwelveServerDirections(t *testing.T) {
	t.Parallel()
	rules, err := RenderNFT(testNetworks(), false)
	if err != nil {
		t.Fatal(err)
	}
	subnets := []string{"172.31.1.0/24", "172.31.2.0/24", "172.31.3.0/24", "172.31.4.0/24"}
	for _, source := range subnets {
		for _, destination := range subnets {
			if source == destination {
				continue
			}
			line := "ip saddr " + source + " ip daddr " + destination + " counter accept"
			if !strings.Contains(rules, line) {
				t.Errorf("rules missing %q", line)
			}
		}
	}
	if got := strings.Count(rules, " counter accept comment "); got != 12 {
		t.Fatalf("forward pair count = %d, want 12\n%s", got, rules)
	}
	for _, forbidden := range []string{"iifname", "oifname", "accept all", "policy accept"} {
		if strings.Contains(rules, forbidden) {
			t.Errorf("rules unexpectedly contain %q", forbidden)
		}
	}
	if !strings.Contains(rules, "policy drop") || !strings.Contains(rules, "ct state established,related accept") {
		t.Fatalf("rules lack default drop/state handling:\n%s", rules)
	}
}

func TestRenderNFTExistingTableIsIdempotent(t *testing.T) {
	t.Parallel()
	fresh, err := RenderNFT(testNetworks(), false)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := RenderNFT(testNetworks(), true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fresh, "flush table") || !strings.Contains(existing, "flush table inet isureal_router") {
		t.Fatalf("unexpected fresh/existing scripts:\nFRESH:\n%s\nEXISTING:\n%s", fresh, existing)
	}
}

func TestRenderNFTRejectsMissingNetwork(t *testing.T) {
	t.Parallel()
	networks := testNetworks()
	delete(networks, domain.DB1)
	if _, err := RenderNFT(networks, false); err == nil {
		t.Fatal("RenderNFT unexpectedly succeeded without db1")
	}
	if _, err := RenderNFT(map[domain.ServerID]config.ServerNetwork{}, false); err == nil {
		t.Fatal("RenderNFT unexpectedly succeeded without networks")
	}
}
