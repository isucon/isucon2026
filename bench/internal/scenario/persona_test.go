package scenario

import (
	"fmt"
	"math"
	"testing"
)

// TestPersonaIsStableAndVaried は、好みと癖が同じ人には同じ値になり、人によってばらつき、
// フィードの重みの和が 1 になることを確かめる。
func TestPersonaIsStableAndVaried(t *testing.T) {
	travelers, exact := 0, 0
	appetites := map[int]int{}
	for i := range 2000 {
		name := fmt.Sprintf("user%d", i)
		p := newPersona(1, name)
		if q := newPersona(1, name); fmt.Sprint(p) != fmt.Sprint(q) {
			t.Fatalf("%s: persona differs between calls", name)
		}
		sum := 0.0
		for _, w := range p.feeds {
			sum += w
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Fatalf("%s: feed weights sum to %f", name, sum)
		}
		if p.appetite < 0.45 || p.appetite > 1.55 || p.attendance < 0.85 || p.attendance > 1 || len(p.phrases) != 3 {
			t.Fatalf("%s: out of range: %+v", name, p)
		}
		if p.wanderlust > 0 {
			travelers++
		}
		if p.location == "exact" {
			exact++
		}
		appetites[int(p.appetite*2)]++
	}
	if travelers < 100 || travelers > 220 {
		t.Errorf("travelers = %d, want about 8%% of 2000", travelers)
	}
	if exact < 120 || exact > 280 {
		t.Errorf("exact location = %d, want about 10%% of 2000", exact)
	}
	if len(appetites) < 3 {
		t.Errorf("appetite does not vary: %v", appetites)
	}
}
