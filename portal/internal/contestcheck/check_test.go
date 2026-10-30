package contestcheck

import (
	"testing"
)

func goodSnapshot() Snapshot {
	s := Snapshot{Self: "i1", Instances: []Instance{}}
	for i := 1; i <= 5; i++ {
		s.Instances = append(s.Instances, Instance{ID: string(rune('0' + i)), Number: i, VPC: "vpc-1", AZ: "az-1", State: "running", Type: "c5.large", AMI: "ami-1", InterfaceCount: 1, VolumeCount: 1})
	}
	s.Instances[4].InterfaceCount = 2
	s.Self = "1"
	return s
}
func TestValidateTopologyAndResourceDrift(t *testing.T) {
	baseline := goodSnapshot()
	current := goodSnapshot()
	if failures := Compare(baseline, current); len(failures) != 0 {
		t.Fatal(failures)
	}
	current.Instances[3].VPC = "vpc-other"
	if len(Compare(baseline, current)) == 0 {
		t.Fatal("mixed VPC passed")
	}
	current = goodSnapshot()
	current.Instances[2].Type = "c7i.xlarge"
	if len(Compare(baseline, current)) == 0 {
		t.Fatal("resource change passed")
	}
	current = goodSnapshot()
	current.Instances = current.Instances[:4]
	if len(Compare(baseline, current)) == 0 {
		t.Fatal("missing router passed")
	}
	current = goodSnapshot()
	current.Router = map[string]string{"tc": "changed"}
	if len(Compare(baseline, current)) == 0 {
		t.Fatal("routing change passed")
	}

}
func TestNormalizeRuntimeIgnoresCountersButNotDelay(t *testing.T) {
	a := []byte(`[{"kind":"netem","options":{"delay":100},"bytes":10,"packets":1,"refcnt":2}]`)
	b := []byte(`[{"kind":"netem","options":{"delay":100},"bytes":20,"packets":2,"refcnt":3}]`)
	x, err := NormalizeRuntime(a)
	if err != nil {
		t.Fatal(err)
	}
	y, _ := NormalizeRuntime(b)
	if x != y {
		t.Fatal("counters changed baseline")
	}
	z, _ := NormalizeRuntime([]byte(`[{"kind":"netem","options":{"delay":0}}]`))
	if x == z {
		t.Fatal("delay not checked")
	}
}

func TestRouterSecurityRestrictionChangesFailEvenWithSameEC2Type(t *testing.T) {
	b := goodSnapshot()
	c := goodSnapshot()
	b.Instances[4].Interfaces = "sg-with-no-instance-connect"
	c.Instances[4].Interfaces = "different-security-group"
	if len(Compare(b, c)) == 0 {
		t.Fatal("router ENI/security group modification passed")
	}
}

func TestNFTCounterHandlesDoNotMaskChangedRules(t *testing.T) {
	a, _ := NormalizeRuntime([]byte(`{"nftables":[{"rule":{"family":"inet","table":"isureal_router","handle":11,"expr":[{"counter":{"packets":1,"bytes":5}},{"accept":null}]}}]}`))
	b, _ := NormalizeRuntime([]byte(`{"nftables":[{"rule":{"family":"inet","table":"isureal_router","handle":99,"expr":[{"counter":{"packets":9,"bytes":50}},{"accept":null}]}}]}`))
	if a != b {
		t.Fatal("reboot/counters changed rules")
	}
	c, _ := NormalizeRuntime([]byte(`{"nftables":[{"rule":{"family":"inet","table":"isureal_router","expr":[{"drop":null}]}}]}`))
	if a == c {
		t.Fatal("modified rule passed")
	}
}

func TestAWSResourceSetsIgnoreAPIOrdering(t *testing.T) {
	a := canonicalSet([]string{`{"groups":[{"id":"sg-a"},{"id":"sg-b"}]}`})
	b := canonicalSet([]string{`{"groups":[{"id":"sg-b"},{"id":"sg-a"}]}`})
	if a != b {
		t.Fatal("AWS response order changed the resource baseline")
	}
}
