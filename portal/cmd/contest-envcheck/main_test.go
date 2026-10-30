package main

import (
	"fmt"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/contestcheck"
)

func TestInitialStateMustMatchDeclaredImagesAndTypes(t *testing.T) {
	cfg := []byte(`{"service_ami":"ami-service","router_ami":"ami-router","types":["c5.large","c7i.large","c7i.large","c5.large","c6in.large"]}`)
	s := contestcheck.Snapshot{Self: "i1"}
	types := []string{"c5.large", "c7i.large", "c7i.large", "c5.large", "c6in.large"}
	for n := 1; n <= 5; n++ {
		i := contestcheck.Instance{ID: fmt.Sprintf("i%d", n), Number: n, AMI: "ami-service", Type: types[n-1], VPC: "vpc", AZ: "az", State: "running", InterfaceCount: 1, VolumeCount: 1}
		if n == 5 {
			i.AMI = "ami-router"
			i.InterfaceCount = 2
		}
		s.Instances = append(s.Instances, i)
	}
	if err := verifyExpected(s, cfg); err != nil {
		t.Fatal(err)
	}
	s.Instances[4].AMI = "ami-unapproved"
	if err := verifyExpected(s, cfg); err == nil {
		t.Fatal("unapproved router AMI used as baseline")
	}
	s.Instances[4].AMI = "ami-router"
	s.Instances[1].Type = "c7i.8xlarge"
	if err := verifyExpected(s, cfg); err == nil {
		t.Fatal("changed type used as baseline")
	}
	if err := verifyExpected(s, []byte(`{}`)); err == nil {
		t.Fatal("empty declaration used as baseline")
	}
}
