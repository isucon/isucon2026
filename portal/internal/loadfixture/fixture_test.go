package loadfixture

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	c := DefaultConfig()
	d, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Teams) != 1000 {
		t.Fatalf("teams=%d", len(d.Teams))
	}
	again, _ := Generate(c)
	if !reflect.DeepEqual(d, again) {
		t.Fatal("seed is not reproducible")
	}
	c.Seed++
	other, _ := Generate(c)
	if reflect.DeepEqual(d, other) {
		t.Fatal("different seeds produced same data")
	}
	counts := make(map[int]int)
	last := make(map[int]time.Time)
	previous := make(map[int]time.Time)
	first := make(map[time.Time]bool)
	hourly := [8]int{}
	failed := 0
	for i, j := range d.Jobs {
		if j.ID != i+1 || j.TeamID < 1 || j.TeamID > 1000 {
			t.Fatal("invalid reference")
		}
		if j.Created.Before(c.Start) || !j.Finished.Before(c.Start.Add(c.Duration)) || !j.Started.After(j.Created) || !j.Finished.After(j.Started) {
			t.Fatal("invalid time range")
		}
		if j.Created.Before(last[j.TeamID]) {
			t.Fatal("overlapping team jobs")
		}
		if i > 0 && j.Created.Before(d.Jobs[i-1].Created) {
			t.Fatal("IDs not in enqueue order")
		}
		if prev, ok := previous[j.TeamID]; ok {
			gap := j.Created.Sub(prev)
			if gap < 3*time.Minute || gap > 5*time.Minute {
				t.Fatalf("interval=%s", gap)
			}
		} else {
			if j.Created.Sub(c.Start) >= 5*time.Minute {
				t.Fatal("initial offset out of range")
			}
			first[j.Created] = true
		}
		previous[j.TeamID] = j.Created
		last[j.TeamID] = j.Finished
		counts[j.TeamID]++
		hourly[int(j.Created.Sub(c.Start)/time.Hour)]++
		if !j.Passed {
			failed++
			if j.Score != 0 {
				t.Fatal("failed score must be zero")
			}
		} else if j.Score != j.Raw-j.Deduction {
			t.Fatal("score breakdown mismatch")
		}
	}
	minCount, maxCount := len(d.Jobs), 0
	for _, n := range counts {
		minCount = min(minCount, n)
		maxCount = max(maxCount, n)
	}
	if len(counts) != 1000 || minCount == maxCount || failed == 0 || failed == len(d.Jobs) {
		t.Fatal("missing variation")
	}
	if len(first) < 990 {
		t.Fatal("initial runs are synchronized")
	}
	if len(d.Jobs) < 110000 || len(d.Jobs) > 130000 {
		t.Fatal("unexpected total count")
	}
	t.Logf("jobs=%d per team=%d..%d hourly=%v", len(d.Jobs), minCount, maxCount, hourly)
}

func TestInvalidConfig(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Teams = 0 }, func(c *Config) { c.Duration = time.Minute },
		func(c *Config) { c.Start = time.Time{} }, func(c *Config) { c.LogBytes = -1 },
	} {
		c := DefaultConfig()
		change(&c)
		if _, err := Generate(c); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestSQL(t *testing.T) {
	c := DefaultConfig()
	c.Teams = 2
	d, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	if err = d.WriteSQL(&a); err != nil {
		t.Fatal(err)
	}
	if err = d.WriteSQL(&b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("SQL not reproducible")
	}
	if err = d.WriteSQL(brokenWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error lost: %v", err)
	}
}
