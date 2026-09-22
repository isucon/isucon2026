package netcontrol

import (
	"strings"
	"testing"

	"github.com/isucon/isucon2026/router/internal/domain"
)

func TestRenderNFTAllowsOnlySixServerDirections(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.Server1: "eth-a", domain.Server2: "eth-b", domain.Server3: "eth-c"}
	rules, err := RenderNFT(interfaces, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"eth-a", "eth-b"}, {"eth-a", "eth-c"}, {"eth-b", "eth-a"}, {"eth-b", "eth-c"}, {"eth-c", "eth-a"}, {"eth-c", "eth-b"}} {
		line := `iifname "` + pair[0] + `" oifname "` + pair[1] + `" counter accept`
		if !strings.Contains(rules, line) {
			t.Errorf("rules missing %q", line)
		}
	}
	if got := strings.Count(rules, " oifname "); got != 6 {
		t.Fatalf("forward pair count = %d, want 6\n%s", got, rules)
	}
	for _, forbidden := range []string{"edge0", "control0", "accept all", "policy accept"} {
		if strings.Contains(rules, forbidden) {
			t.Errorf("rules unexpectedly contain %q", forbidden)
		}
	}
	if !strings.Contains(rules, "policy drop") || !strings.Contains(rules, "ct state established,related accept") {
		t.Fatalf("rules lack default drop/state handling:\n%s", rules)
	}
	if got := strings.Count(rules, " counter accept comment "); got != 6 {
		t.Fatalf("forward counter count = %d, want 6\n%s", got, rules)
	}
}

func TestRenderNFTExistingTableIsIdempotent(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.Server1: "s1", domain.Server2: "s2", domain.Server3: "s3"}
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
		"missing":   {domain.Server1: "s1", domain.Server2: "s2"},
		"duplicate": {domain.Server1: "s1", domain.Server2: "s1", domain.Server3: "s3"},
		"unsafe":    {domain.Server1: "s1\" accept", domain.Server2: "s2", domain.Server3: "s3"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := RenderNFT(interfaces, false); err == nil {
				t.Fatal("RenderNFT unexpectedly succeeded")
			}
		})
	}
}
