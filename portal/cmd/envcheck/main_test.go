package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func validEnvironment() environment {
	e := environment{InstanceID: "i-001", PublicIP: "203.0.113.1"}
	for n := 1; n <= 5; n++ {
		typ := envcheckTypes[n-1]
		e.Instances = append(e.Instances, instance{ID: fmt.Sprintf("i-%03d", n), Name: fmt.Sprintf("isucon2026-envcheck-%03d", n), Type: typ, AMI: "ami-test", AZ: "apne1-az1", State: "running", VolumeSize: 8, VolumeType: "gp3", VolumeCount: 1, InterfaceCount: 1})
	}
	return e
}

func TestValidateEnvironment(t *testing.T) {
	info := checkInfo{AMI: "ami-test", AZ: "apne1-az1"}
	for _, tc := range []struct {
		name   string
		mutate func(*environment)
	}{
		{"missing instance", func(e *environment) { e.Instances = e.Instances[:4] }},
		{"wrong type", func(e *environment) { e.Instances[0].Type = "t3.micro" }},
		{"duplicate name", func(e *environment) { e.Instances[1].Name = e.Instances[0].Name }},
		{"wrong ami", func(e *environment) { e.Instances[0].AMI = "ami-other" }},
		{"wrong zone", func(e *environment) { e.Instances[0].AZ = "apne1-az2" }},
		{"stopped instance", func(e *environment) { e.Instances[0].State = "stopped" }},
		{"extra volume", func(e *environment) { e.Instances[0].VolumeCount = 2 }},
		{"wrong disk", func(e *environment) { e.Instances[0].VolumeSize = 20 }},
		{"extra interface", func(e *environment) { e.Instances[0].InterfaceCount = 2 }},
		{"missing self", func(e *environment) { e.InstanceID = "i-unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := validEnvironment()
			tc.mutate(&e)
			if len(validate(e, info)) == 0 {
				t.Fatal("invalid environment passed")
			}
		})
	}
	if failures := validate(validEnvironment(), info); len(failures) > 0 {
		t.Fatalf("valid environment: %v", failures)
	}
}

func TestPortalJSONRoundTrip(t *testing.T) {
	var received result
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/env_check_info":
			if r.URL.Query().Get("token") != "test-token" || r.URL.Query().Get("name") != "test-boot" {
				t.Error("invalid query")
			}
			json.NewEncoder(w).Encode(checkInfo{AMI: "ami-test", AZ: "apne1-az1"})
		case "/api/env_checks":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Error("expected JSON POST")
			}
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	p := portal{client: srv.Client(), credentials: credentials{Host: strings.TrimPrefix(srv.URL, "https://"), Token: "test-token"}}
	info, err := p.info(context.Background(), "test-boot")
	if err != nil || info.AMI != "ami-test" {
		t.Fatalf("info: %+v %v", info, err)
	}
	want := result{Name: "test-boot", Passed: true, IPAddress: "203.0.113.1", Message: "ok", RawData: `{"instance_id":"i-001"}`}
	if err := p.send(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if received.Token != "test-token" || received.Name != want.Name || !received.Passed || received.RawData != want.RawData {
		t.Fatalf("result: %+v", received)
	}
}
