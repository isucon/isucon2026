package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/contestcheck"
)

var testTarget = target{Name: "team7", Team: 7, RouterID: "i-router", RouterIP: "203.0.113.5", AsiaID: "i-asia", AsiaIP: "203.0.113.1", AsiaStatus: runningStatus}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func normalized(t *testing.T, name string) string {
	t.Helper()
	s, err := contestcheck.NormalizeRuntime(fixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// baseline is what envcheck --record-baseline writes for a Router with the ruleset.
func baseline(t *testing.T, nft string) []byte {
	t.Helper()
	b, err := json.MarshalIndent(contestcheck.Snapshot{
		Version: 1, CapturedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), Self: "i-router", Team: "7",
		Instances: []contestcheck.Instance{{ID: "i-router", Number: 5}}, Routes: `[{"r":"<&>"}]`, ACLs: "[]",
		Router: map[string]string{"nft": nft, "qdisc": `[{"kind":"htb"}]`, "file:/etc/isucon/router.env": "abc"},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func installedState(t *testing.T, ruleset, base []byte) routerState {
	t.Helper()
	return routerState{
		instanceID: "i-router", registration: true, dataIP: true,
		nftFile: digest([]byte(nftRules)), unitFile: digest([]byte(unitFile)),
		unitEnabled: "enabled", unitActive: "active",
		table: string(fixture(t, "table.txt")), baseline: base, ruleset: ruleset,
	}
}

func TestTemplateRunsBlockCommand(t *testing.T) {
	template, err := os.ReadFile("../../internal/cloudformation/templates/contest.yaml.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(template), "\n              "+blockCommand+"\n") != 1 {
		t.Fatal("contest.yaml.tmpl must run blockCommand on the Router")
	}
}

// The command runs with /etc moved into a temporary directory and stubs for
// sudo, systemctl and nft, so the files it writes can be compared.
func TestBlockCommandWritesFiles(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(filepath.Join(dir, "etc/systemd/system"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nexec \"$@\"\n"), 0o755)
	for _, name := range []string{"systemctl", "nft"} {
		os.WriteFile(filepath.Join(bin, name), []byte(fmt.Sprintf("#!/bin/sh\necho %s \"$@\" >>%s/calls\n", name, dir)), 0o755)
	}
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(blockCommand, "/etc/", dir+"/etc/"))
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	nft, _ := os.ReadFile(filepath.Join(dir, "etc/isucon/data-proxy-block.nft"))
	unit, _ := os.ReadFile(filepath.Join(dir, "etc/systemd/system/isucon-data-proxy-block.service"))
	if string(nft) != nftRules || string(nft) != string(fixture(t, "block.nft")) {
		t.Errorf("nft file:\n%s", nft)
	}
	if string(unit) != strings.ReplaceAll(unitFile, "/etc/", dir+"/etc/") {
		t.Errorf("unit file:\n%s", unit)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	want := "systemctl daemon-reload\nsystemctl enable isucon-data-proxy-block.service\nsystemctl restart isucon-data-proxy-block.service\nnft list table inet isucon_data_proxy_block\n"
	if string(calls) != want {
		t.Errorf("calls:\n%s", calls)
	}
}

func TestScriptsParse(t *testing.T) {
	for name, script := range map[string]string{
		"inspect":  inspectScript,
		"install":  installScript,
		"baseline": baselineScript([]byte("old"), []byte("new")),
	} {
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", name, err, out)
		}
	}
}

func TestExpectedTable(t *testing.T) {
	if got := counterValues.ReplaceAllString(string(fixture(t, "table.txt")), "counter"); got != expectedTable {
		t.Fatalf("nft list table:\n%s", got)
	}
}

// The install reproduces a boot's ruleset, so a baseline moved to it also
// matches the Router after every later reboot.
func TestInstallMatchesBoot(t *testing.T) {
	recorded := normalized(t, "before.json")
	base := baseline(t, recorded)
	before := installedState(t, fixture(t, "before.json"), base)
	before.nftFile, before.unitFile, before.unitEnabled, before.unitActive, before.table = "", "", "", "inactive", ""
	d, err := decide(testTarget, before)
	if err != nil || d.done || d.partial || d.skip != "" || d.baseline != recorded {
		t.Fatalf("before install: %+v, %v", d, err)
	}

	after := installedState(t, fixture(t, "installed.json"), base)
	ruleset, err := verifyInstalled(after, d.baseline)
	if err != nil {
		t.Fatal(err)
	}
	if ruleset != normalized(t, "boot.json") {
		t.Fatal("installed ruleset differs from a boot's")
	}
	patched, err := patchBaseline(base, d.baseline, ruleset)
	if err != nil {
		t.Fatal(err)
	}
	var want, got contestcheck.Snapshot
	json.Unmarshal(base, &want)
	json.Unmarshal(patched, &got)
	want.Router["nft"] = normalized(t, "boot.json")
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("patched baseline:\n%s", patched)
	}
	if strings.Count(string(patched), "\n") != strings.Count(string(base), "\n") {
		t.Fatal("patching must keep the baseline layout")
	}

	booted := installedState(t, fixture(t, "boot.json"), patched)
	if d, err := decide(testTarget, booted); err != nil || !d.done {
		t.Fatalf("after reboot: %+v, %v", d, err)
	}
}

func TestDecidePartial(t *testing.T) {
	base := baseline(t, normalized(t, "before.json"))
	d, err := decide(testTarget, installedState(t, fixture(t, "partial.json"), base))
	if err != nil || !d.partial || d.done {
		t.Fatalf("%+v, %v", d, err)
	}
	// Without recreating isureal_router the block stays last, unlike a boot.
	if _, err := verifyInstalled(installedState(t, fixture(t, "partial.json"), base), d.baseline); err == nil {
		t.Fatal("block after isureal_router must not be accepted")
	}
}

func TestDecideRefuses(t *testing.T) {
	base := baseline(t, normalized(t, "before.json"))
	changed := strings.Replace(string(fixture(t, "before.json")), "app1-to-db1", "app1-to-db2", 1)
	cases := map[string]routerState{
		"other instance":   installedState(t, fixture(t, "before.json"), base),
		"changed ruleset":  installedState(t, []byte(changed), base),
		"changed baseline": installedState(t, fixture(t, "before.json"), baseline(t, normalized(t, "boot.json"))),
	}
	s := cases["other instance"]
	s.instanceID = "i-other"
	cases["other instance"] = s
	for name, state := range cases {
		if d, err := decide(testTarget, state); err == nil {
			t.Errorf("%s: %+v", name, d)
		}
	}

	pulumi := installedState(t, fixture(t, "before.json"), base)
	pulumi.registration = false
	if d, err := decide(testTarget, pulumi); err != nil || d.skip == "" {
		t.Errorf("Pulumi Router: %+v, %v", d, err)
	}
	pending := installedState(t, fixture(t, "before.json"), nil)
	if d, err := decide(testTarget, pending); err != nil || d.skip == "" {
		t.Errorf("no baseline: %+v, %v", d, err)
	}
}

func TestParseState(t *testing.T) {
	out := fmt.Sprintf("instance_id=i-router\nregistration=yes\ndata_ip=yes\nnft_file=%s\nunit_file=\nunit_enabled=\nunit_active=inactive\ntable=\nbaseline=\nruleset=%s\n",
		digest([]byte(nftRules)), encode(fixture(t, "before.json")))
	s, err := parseState([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if s.instanceID != "i-router" || !s.registration || !s.dataIP || s.baseline != nil || s.table != "" || string(s.ruleset) != string(fixture(t, "before.json")) {
		t.Fatalf("%+v", s)
	}
	if _, err := parseState([]byte("instance_id=i-router\n")); err == nil {
		t.Fatal("truncated output must fail")
	}
}

func TestTargets(t *testing.T) {
	data := "7\ti-router\t203.0.113.5\t\ti-asia\t203.0.113.1\t4\n8\ti-r8\t203.0.113.6\tapp.team8\ti-a8\t203.0.113.2\t4\n9\ti-r9\t\t\t\t\t\n"
	targets, err := parseTargets([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0] != testTarget {
		t.Fatalf("%+v", targets)
	}
	if targets[0].skipReason() != "" || targets[1].skipReason() == "" || targets[2].skipReason() == "" {
		t.Fatal("only team 7 is a reachable CloudFormation Router")
	}
	config := sshConfig(targets, "/k", "/known_hosts")
	for _, want := range []string{"Host team7-router\n  HostName 192.168.10.10\n  HostKeyAlias team7-i-router\n  ProxyJump team7-asia\n", "Host team7-asia\n  HostName 203.0.113.1\n", "IdentityAgent none", "ForwardAgent no"} {
		if !strings.Contains(config, want) {
			t.Errorf("ssh config lacks %q", want)
		}
	}
	if strings.Contains(config, "team8") || strings.Contains(config, "team9") {
		t.Error("skipped teams must not get ssh hosts")
	}
	if _, err := parseTargets([]byte(data + "7\ta\tb\tc\td\te\t4\n")); err == nil {
		t.Error("repeated team must fail")
	}
	if selected, err := selectTeams(targets, "9, 7"); err != nil || len(selected) != 2 {
		t.Errorf("%v %v", selected, err)
	}
	if _, err := selectTeams(targets, "10"); err == nil {
		t.Error("unknown team must fail")
	}
}

func TestJumpTargets(t *testing.T) {
	targets := jumpTargets("13.115.15.90")
	if len(targets) != 1 || targets[0].skipReason() != "" || targets[0].host() != "via-13.115.15.90-router" {
		t.Fatalf("%+v", targets)
	}
	config := sshConfig(targets, "/k", "/known_hosts")
	if !strings.Contains(config, "Host via-13.115.15.90-router\n  HostName 192.168.10.10\n  HostKeyAlias via-13.115.15.90-router\n  ProxyJump via-13.115.15.90-asia\n") ||
		!strings.Contains(config, "Host via-13.115.15.90-asia\n  HostName 13.115.15.90\n") {
		t.Fatalf("ssh config:\n%s", config)
	}
	// Without the portal database any instance is accepted, but it must be known.
	state := installedState(t, fixture(t, "before.json"), baseline(t, normalized(t, "before.json")))
	state.instanceID = "i-anything"
	if _, err := decide(targets[0], state); err != nil {
		t.Fatal(err)
	}
	state.instanceID = ""
	if _, err := decide(targets[0], state); err == nil {
		t.Fatal("unknown instance must fail")
	}
}
