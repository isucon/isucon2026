// Package contestcheck verifies a contest environment against its provisioning
// snapshot. It never repairs or refreshes that snapshot during validation.
package contestcheck

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
)

type Instance struct {
	ID             string `json:"id"`
	Attributes     string `json:"attributes"`
	Number         int    `json:"number"`
	Name           string `json:"name"`
	VPC            string `json:"vpc"`
	AZ             string `json:"az"`
	State          string `json:"state"`
	Type           string `json:"type"`
	AMI            string `json:"ami"`
	Subnet         string `json:"subnet"`
	PrivateIP      string `json:"private_ip"`
	Interfaces     string `json:"interfaces"`
	InterfaceCount int    `json:"interface_count"`
	Volumes        string `json:"volumes"`
	VolumeCount    int    `json:"volume_count"`
}
type Snapshot struct {
	Version    int               `json:"version"`
	CapturedAt time.Time         `json:"captured_at"`
	Self       string            `json:"self"`
	Team       string            `json:"team"`
	PublicIP   string            `json:"public_ip"`
	Instances  []Instance        `json:"instances"`
	Routes     string            `json:"routes"`
	ACLs       string            `json:"acls"`
	Router     map[string]string `json:"router,omitempty"`
}

func Topology(s Snapshot) []string {
	var failures []string
	if len(s.Instances) != 5 {
		failures = append(failures, "expected four servers and one router")
	}
	seen := map[int]bool{}
	ids := map[string]bool{}
	self := false
	vpc, az := "", ""
	for _, i := range s.Instances {
		if i.ID == s.Self {
			self = true
		}
		if i.Number < 1 || i.Number > 5 || seen[i.Number] {
			failures = append(failures, "missing or duplicate instance number")
		}
		seen[i.Number] = true
		if i.ID == "" || ids[i.ID] {
			failures = append(failures, "missing or duplicate EC2 instance ID")
		}
		ids[i.ID] = true
		if vpc == "" {
			vpc = i.VPC
			az = i.AZ
		}
		if i.VPC == "" || i.VPC != vpc {
			failures = append(failures, i.ID+": instances are not in the same VPC")
		}
		if i.AZ == "" || i.AZ != az {
			failures = append(failures, i.ID+": instances are not in the same AZ")
		}
		wantInterfaces := 1
		if i.Number == 5 {
			wantInterfaces = 2
		}
		if i.InterfaceCount != wantInterfaces || i.VolumeCount != 1 {
			failures = append(failures, i.ID+": incorrect number of ENIs or volumes")
		}
		if i.State != "running" {
			failures = append(failures, i.ID+": instance is not running")
		}
	}
	if !self {
		failures = append(failures, "reporting instance is not in the team")
	}
	return failures
}
func Compare(baseline, current Snapshot) []string {
	failures := Topology(current)
	expected := map[int]Instance{}
	for _, i := range baseline.Instances {
		expected[i.Number] = i
	}
	for _, i := range current.Instances {
		if !reflect.DeepEqual(expected[i.Number], i) {
			failures = append(failures, fmt.Sprintf("instance %d: AWS resources differ from provisioning", i.Number))
		}
	}
	if baseline.Self != current.Self || baseline.Team != current.Team {
		failures = append(failures, "environment identity changed")
	}
	if baseline.Routes != current.Routes || baseline.ACLs != current.ACLs {
		failures = append(failures, "AWS routes changed")
	}
	if !reflect.DeepEqual(baseline.Router, current.Router) {
		failures = append(failures, "router runtime, access restrictions or protected files changed")
	}

	return failures
}

// NormalizeRuntime removes counters/volatile identifiers, preserving actual
// traffic-control options and filter/class handles. It does not reorder arrays:
// nftables rule order is security relevant.
func NormalizeRuntime(data []byte) (string, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	var normalize func(any)
	normalize = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, key := range []string{"bytes", "packets", "refcnt", "lastuse", "used", "expires", "stats", "stats2", "xstats", "backlog", "drops", "overlimits", "requeues", "qlen", "hw_stats", "installed"} {
				delete(x, key)
			}
			delete(x, "metainfo")
			if _, nft := x["family"]; nft {
				delete(x, "handle")
			}
			for _, child := range x {
				normalize(child)
			}
		case []any:
			for _, child := range x {
				normalize(child)
			}
		}
	}
	normalize(value)
	b, err := json.Marshal(value)
	return string(b), err
}
func canonicalSet(values []string) string {
	for i, value := range values {
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			sortJSONSets(decoded)
			body, _ := json.Marshal(decoded)
			values[i] = string(body)
		}
	}
	sort.Strings(values)
	b, _ := json.Marshal(values)
	return string(b)
}

// AWS describes unordered resource collections. Sort their nested sets as well;
// live nftables rules deliberately do not use this normalization.
func sortJSONSets(value any) {
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			sortJSONSets(child)
		}
	case []any:
		for _, child := range v {
			sortJSONSets(child)
		}
		sort.Slice(v, func(i, j int) bool {
			a, _ := json.Marshal(v[i])
			b, _ := json.Marshal(v[j])
			return string(a) < string(b)
		})
	}
}
