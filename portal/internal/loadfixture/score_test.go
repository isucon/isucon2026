package loadfixture

import (
	"sort"
	"testing"
	"time"
)

func TestScoreTrajectories(t *testing.T) {
	c := DefaultConfig()
	d, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	var early, late [1000][]int
	longest := make([]int, 1000)
	runs := make([]int, 1000)
	for _, j := range d.Jobs {
		i := j.TeamID - 1
		if !j.Passed {
			runs[i]++
			longest[i] = max(longest[i], runs[i])
			continue
		}
		runs[i] = 0
		elapsed := j.Created.Sub(c.Start)
		if elapsed < time.Hour {
			early[i] = append(early[i], j.Score)
		}
		if elapsed > 7*time.Hour {
			late[i] = append(late[i], j.Score)
		}
	}
	median := func(xs []int) int { sort.Ints(xs); return xs[len(xs)/2] }
	stagnant, strongGrowth, regressed, failureStreaks := 0, 0, 0, 0
	for i := range early {
		if len(early[i]) == 0 || len(late[i]) == 0 {
			continue
		}
		ratio := float64(median(late[i])) / float64(median(early[i]))
		if ratio > 0.8 && ratio < 1.2 {
			stagnant++
		}
		if ratio > 3 {
			strongGrowth++
		}
		if ratio < 0.7 {
			regressed++
		}
		if longest[i] >= 4 {
			failureStreaks++
		}
	}
	if stagnant < 80 || strongGrowth < 80 || regressed < 40 || failureStreaks < 40 {
		t.Fatalf("insufficient diversity: stagnant=%d growth=%d regression=%d failures=%d", stagnant, strongGrowth, regressed, failureStreaks)
	}
	t.Logf("stagnant=%d growth=%d regression=%d failure streaks=%d", stagnant, strongGrowth, regressed, failureStreaks)
}
