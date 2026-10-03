package netcontrol

import (
	"strings"
	"testing"

	"github.com/isucon/isucon2026/router/internal/domain"
)

func TestRenderNFTAllowsOnlyTwelveServerDirections(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "eth-a", domain.App2: "eth-b", domain.App3: "eth-c", domain.DB1: "eth-d"}
	rules, err := RenderNFT(interfaces, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"eth-a", "eth-b"}, {"eth-a", "eth-c"}, {"eth-b", "eth-a"}, {"eth-b", "eth-c"}, {"eth-c", "eth-a"}, {"eth-c", "eth-b"}, {"eth-a", "eth-d"}, {"eth-b", "eth-d"}, {"eth-c", "eth-d"}, {"eth-d", "eth-a"}, {"eth-d", "eth-b"}, {"eth-d", "eth-c"}} {
		line := `iifname "` + pair[0] + `" oifname "` + pair[1] + `" counter accept`
		if !strings.Contains(rules, line) {
			t.Errorf("rules missing %q", line)
		}
	}
	if got := strings.Count(rules, " oifname "); got != 12 {
		t.Fatalf("forward pair count = %d, want 12\n%s", got, rules)
	}
	for _, forbidden := range []string{"edge0", "control0", "accept all", "policy accept"} {
		if strings.Contains(rules, forbidden) {
			t.Errorf("rules unexpectedly contain %q", forbidden)
		}
	}
	if !strings.Contains(rules, "policy drop") || !strings.Contains(rules, "ct state established,related accept") {
		t.Fatalf("rules lack default drop/state handling:\n%s", rules)
	}
	if got := strings.Count(rules, " counter accept comment "); got != 12 {
		t.Fatalf("forward counter count = %d, want 12\n%s", got, rules)
	}
}

func TestRenderNFTExistingTableIsIdempotent(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "s1", domain.App2: "s2", domain.App3: "s3", domain.DB1: "s4"}
	fresh, err := RenderNFT(interfaces, false)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := RenderNFT(interfaces, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fresh, "flush table") || !strings.Contains(existing, "flush table inet isureal_router") {
		t.Fatalf("unexpected fresh/existing scripts:\nFRESH:\n%s\nEXISTING:\n%s", fresh, existing)
	}
}

func TestRenderNFTRejectsDuplicateOrUnsafeInterfaces(t *testing.T) {
	t.Parallel()
	for name, interfaces := range map[string]map[domain.ServerID]string{
		"missing":   {domain.App1: "s1", domain.App2: "s2"},
		"duplicate": {domain.App1: "s1", domain.App2: "s1", domain.App3: "s3", domain.DB1: "s4"},
		"unsafe":    {domain.App1: "s1\" accept", domain.App2: "s2", domain.App3: "s3", domain.DB1: "s4"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := RenderNFT(interfaces, false); err == nil {
				t.Fatal("RenderNFT unexpectedly succeeded")
			}
		})
	}
}
