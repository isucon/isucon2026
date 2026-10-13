package model

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

func envResult(id int64, generation string, n int, name string, passed bool) *EnvCheck {
	instances := []map[string]string{}
	for j := 1; j <= 5; j++ {
		instances = append(instances, map[string]string{"instance_id": fmt.Sprintf("i-%s-%d", generation, j)})
	}
	raw, _ := json.Marshal(map[string]any{"instance_id": fmt.Sprintf("i-%s-%d", generation, n), "instances": instances})
	return &EnvCheck{ID: id, Name: name, Passed: passed, IPAddress: fmt.Sprintf("203.0.113.%d", n), RawData: string(raw)}
}

func TestEnvCheckProgress(t *testing.T) {
	var checks []*EnvCheck
	add := func(generation string, n int, name string, passed bool) {
		checks = append([]*EnvCheck{envResult(int64(len(checks)+1), generation, n, name, passed)}, checks...)
	}
	assertStatus := func(want pb.EnvCheckStatus) {
		t.Helper()
		got := envCheckProgress(checks)
		if got.Status != want {
			t.Fatalf("got %v, want %v", got.Status, want)
		}
	}
	assertStatus(pb.EnvCheckStatus_NOT_STARTED)
	for n := 1; n <= 4; n++ {
		add("a", n, "test-boot", true)
	}
	assertStatus(pb.EnvCheckStatus_NOT_STARTED)
	add("a", 5, "test-boot", false)
	assertStatus(pb.EnvCheckStatus_NOT_STARTED)
	add("a", 5, "test-boot", true)
	assertStatus(pb.EnvCheckStatus_CREATED_INSTANCE)
	if got := envCheckProgress(checks).InstanceIPs; !slices.Equal(got, []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4", "203.0.113.5"}) {
		t.Fatalf("IPs: %v", got)
	}
	for n := 1; n <= 4; n++ {
		add("a", n, "test-ssh", true)
	}
	assertStatus(pb.EnvCheckStatus_CREATED_INSTANCE)
	add("a", 4, "test-ssh", true) // repeated reports do not count as another instance
	assertStatus(pb.EnvCheckStatus_CREATED_INSTANCE)
	add("a", 5, "test-ssh", true)
	assertStatus(pb.EnvCheckStatus_DONE)
	add("a", 1, "test-ssh", false)
	assertStatus(pb.EnvCheckStatus_CREATED_INSTANCE)
	add("a", 1, "test-ssh", true)
	assertStatus(pb.EnvCheckStatus_DONE)
	add("a", 2, "test-boot", false)
	assertStatus(pb.EnvCheckStatus_NOT_STARTED)
	add("a", 2, "test-boot", true) // a fresh boot needs a fresh SSH check
	assertStatus(pb.EnvCheckStatus_CREATED_INSTANCE)
	add("a", 2, "test-ssh", true)
	assertStatus(pb.EnvCheckStatus_DONE)
	add("b", 1, "test-boot", true) // same EIP, different instances: no old successes
	assertStatus(pb.EnvCheckStatus_NOT_STARTED)
	add("a", 5, "test-ssh", true) // a delayed SSH result cannot restore the old environment
	assertStatus(pb.EnvCheckStatus_NOT_STARTED)
}

func TestEnvCheckProgressInvalidReports(t *testing.T) {
	for _, raw := range []string{"", `{`, `null`, `{}`, `{"instance_id":"i-other","instances":[{"instance_id":"i-1"},{"instance_id":"i-2"},{"instance_id":"i-3"},{"instance_id":"i-4"},{"instance_id":"i-5"}]}`, `{"instance_id":"i-1","instances":[{"instance_id":"i-1"},{"instance_id":"i-1"},{"instance_id":"i-1"},{"instance_id":"i-1"},{"instance_id":"i-1"}]}`} {
		t.Run(raw, func(t *testing.T) {
			e := envResult(1, "a", 1, "test-boot", true)
			e.RawData = raw
			p := envCheckProgress([]*EnvCheck{e})
			if p.Status != pb.EnvCheckStatus_NOT_STARTED || len(p.InstanceIPs) != 0 {
				t.Fatalf("invalid report counted: %+v", p)
			}
		})
	}
}
