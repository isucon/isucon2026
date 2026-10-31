// Command router-data-proxy-block installs the Router data-interface HTTP(S)
// block on CloudFormation environments created before the template carried it.
//
// Each Router is reached as isuadmin through its team's asia server with
// ProxyJump. envcheck compares the live nftables ruleset with the baseline
// recorded at provisioning, in order, so the tool recreates isureal_router after
// the new table (the order every boot produces) and replaces only the
// baseline's nftables entry. Without -apply it only inspects and reports.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/isucon/isucon2026/portal/internal/contestcheck"
)

const (
	blockTable    = "inet isucon_data_proxy_block"
	routerTable   = "inet isureal_router"
	runningStatus = 4 // ContestantInstance.RUNNING
)

// blockCommand is the command the Router UserData in contest.yaml.tmpl runs.
const blockCommand = `sudo sh -c 'mkdir -p /etc/isucon && printf "%s\n" "table inet isucon_data_proxy_block" "delete table inet isucon_data_proxy_block" "table inet isucon_data_proxy_block {" "  chain input {" "    type filter hook input priority filter; policy accept;" "    ip daddr 192.168.10.10 tcp dport { 8080, 443 } counter drop" "  }" "}" >/etc/isucon/data-proxy-block.nft && printf "%s\n" "[Unit]" "Description=Drop HTTP(S) to the Router data interface" "[Service]" "Type=oneshot" "RemainAfterExit=yes" "ExecStart=/usr/sbin/nft -f /etc/isucon/data-proxy-block.nft" "[Install]" "WantedBy=multi-user.target" >/etc/systemd/system/isucon-data-proxy-block.service && systemctl daemon-reload && systemctl enable isucon-data-proxy-block.service && systemctl restart isucon-data-proxy-block.service && nft list table inet isucon_data_proxy_block'`

// nftRules and unitFile are the files blockCommand writes.
const nftRules = `table inet isucon_data_proxy_block
delete table inet isucon_data_proxy_block
table inet isucon_data_proxy_block {
  chain input {
    type filter hook input priority filter; policy accept;
    ip daddr 192.168.10.10 tcp dport { 8080, 443 } counter drop
  }
}
`

const unitFile = `[Unit]
Description=Drop HTTP(S) to the Router data interface
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/isucon/data-proxy-block.nft
[Install]
WantedBy=multi-user.target
`

// expectedTable is `nft list table` output for nftRules without counter values.
const expectedTable = "table inet isucon_data_proxy_block {\n" +
	"\tchain input {\n" +
	"\t\ttype filter hook input priority filter; policy accept;\n" +
	"\t\tip daddr 192.168.10.10 tcp dport { 443, 8080 } counter drop\n" +
	"\t}\n" +
	"}\n"

const inspectScript = `set -u
b64() { base64 -w0; }
sum() { [ -f "$1" ] && sha256sum "$1" | cut -d' ' -f1; }
echo "instance_id=$(cat /var/lib/cloud/data/instance-id 2>/dev/null)"
echo "registration=$([ -f /etc/isucon/registration.json ] && echo yes)"
echo "data_ip=$(ip -4 -o addr show | grep -q ' 192.168.10.10/' && echo yes)"
echo "nft_file=$(sum /etc/isucon/data-proxy-block.nft)"
echo "unit_file=$(sum /etc/systemd/system/isucon-data-proxy-block.service)"
echo "unit_enabled=$(systemctl is-enabled isucon-data-proxy-block.service 2>/dev/null)"
echo "unit_active=$(systemctl is-active isucon-data-proxy-block.service 2>/dev/null)"
echo "table=$(nft list table inet isucon_data_proxy_block 2>/dev/null | b64)"
echo "baseline=$([ -f /var/lib/isucon-envcheck/baseline.json ] && b64 </var/lib/isucon-envcheck/baseline.json)"
echo "ruleset=$(nft -j list ruleset | b64)"
`

// installScript runs blockCommand, which recreates the block's table last, and
// then recreates isureal_router after it, which is the table order of a boot.
const installScript = `set -euo pipefail
` + blockCommand + `
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
{ echo 'delete table inet isureal_router'; nft list table inet isureal_router; } >"$tmp/router.nft"
nft -c -f "$tmp/router.nft"
nft -f "$tmp/router.nft"
`

func baselineScript(current, patched []byte) string {
	return fmt.Sprintf(`set -euo pipefail
f=/var/lib/isucon-envcheck/baseline.json
[ "$(sha256sum "$f" | cut -d' ' -f1)" = %s ] || { echo "baseline changed since inspection" >&2; exit 1; }
[ -e "$f.before-data-proxy-block" ] || cp -p "$f" "$f.before-data-proxy-block"
tmp="$(mktemp "$f.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
echo %s | base64 -d >"$tmp"
[ "$(sha256sum "$tmp" | cut -d' ' -f1)" = %s ]
chown --reference="$f" "$tmp"
chmod --reference="$f" "$tmp"
mv -f "$tmp" "$f"
`, digest(current), encode(patched), digest(patched))
}

func main() {
	var o options
	flag.StringVar(&o.targets, "targets", "", "TSV written by list-targets.sh")
	flag.StringVar(&o.keySecret, "key-secret", "portal-production/admin-ssh-key", "Secrets Manager secret holding the isuadmin private key")
	flag.StringVar(&o.key, "key", "", "isuadmin private key file, instead of -key-secret")
	flag.StringVar(&o.state, "state", "router-data-proxy-block-state", "directory for known_hosts and results.jsonl")
	flag.StringVar(&o.teams, "teams", "", "comma-separated team IDs to process (default: every target)")
	flag.IntVar(&o.parallel, "parallel", 8, "Routers processed at once")
	flag.BoolVar(&o.apply, "apply", false, "change Routers (default: inspect and report only)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, o)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type options struct {
	targets, keySecret, key, state, teams string
	parallel                              int
	apply                                 bool
}

func run(ctx context.Context, o options) error {
	if o.targets == "" || o.parallel < 1 {
		return errors.New("usage: router-data-proxy-block -targets targets.tsv [-teams 1,2] [-apply]")
	}
	data, err := os.ReadFile(o.targets)
	if err != nil {
		return err
	}
	targets, err := parseTargets(data)
	if err != nil {
		return err
	}
	if targets, err = selectTeams(targets, o.teams); err != nil {
		return err
	}
	if err := os.MkdirAll(o.state, 0o700); err != nil {
		return err
	}
	state, err := filepath.Abs(o.state)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "router-data-proxy-block-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	key := o.key
	if key == "" {
		if key, err = fetchKey(ctx, o.keySecret, tmp); err != nil {
			return err
		}
	}
	if key, err = filepath.Abs(key); err != nil {
		return err
	}
	config := filepath.Join(tmp, "ssh_config")
	if err := os.WriteFile(config, []byte(sshConfig(targets, key, filepath.Join(state, "known_hosts"))), 0o600); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(state, "results.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()

	r := &runner{config: config, apply: o.apply}
	queue := make(chan target)
	results := make(chan result)
	var wg sync.WaitGroup
	for range o.parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range queue {
				results <- r.process(ctx, t)
			}
		}()
	}
	go func() {
		defer close(queue)
		for _, t := range targets {
			select {
			case queue <- t:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	counts := map[string]int{}
	for res := range results {
		res.At = time.Now().UTC()
		line, _ := json.Marshal(res)
		if _, err := fmt.Fprintf(log, "%s\n", line); err != nil {
			return err
		}
		counts[res.Outcome]++
		fmt.Printf("team %d %s: %s", res.Team, res.Router, res.Outcome)
		if res.Detail != "" {
			fmt.Printf(": %s", res.Detail)
		}
		fmt.Println()
	}
	outcomes := make([]string, 0, len(counts))
	for outcome, n := range counts {
		outcomes = append(outcomes, fmt.Sprintf("%s=%d", outcome, n))
	}
	sort.Strings(outcomes)
	fmt.Println(strings.Join(outcomes, " "))
	if err := ctx.Err(); err != nil {
		return err
	}
	if counts["failed"] > 0 {
		return fmt.Errorf("%d Routers failed", counts["failed"])
	}
	return nil
}

type target struct {
	Team               int64
	RouterID, RouterIP string
	BenchmarkHost      string
	AsiaID, AsiaIP     string
	AsiaStatus         int
}

func parseTargets(data []byte) ([]target, error) {
	var targets []target
	seen := map[int64]bool{}
	for n, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return nil, fmt.Errorf("targets line %d: %d fields, want 7", n+1, len(f))
		}
		team, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || seen[team] {
			return nil, fmt.Errorf("targets line %d: invalid or repeated team %q", n+1, f[0])
		}
		seen[team] = true
		status := -1
		if f[6] != "" {
			if status, err = strconv.Atoi(f[6]); err != nil {
				return nil, fmt.Errorf("targets line %d: invalid status %q", n+1, f[6])
			}
		}
		targets = append(targets, target{team, f[1], f[2], f[3], f[4], f[5], status})
	}
	return targets, nil
}

func selectTeams(targets []target, list string) ([]target, error) {
	if list == "" {
		return targets, nil
	}
	want := map[int64]bool{}
	for _, s := range strings.Split(list, ",") {
		team, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid team %q", s)
		}
		want[team] = true
	}
	var selected []target
	for _, t := range targets {
		if want[t.Team] {
			selected = append(selected, t)
			delete(want, t.Team)
		}
	}
	if len(want) > 0 {
		return nil, fmt.Errorf("teams not in targets: %v", want)
	}
	return selected, nil
}

func (t target) skipReason() string {
	switch {
	case t.BenchmarkHost != "":
		return "Router registered by tag sync, not CloudFormation"
	case t.AsiaID == "" || t.AsiaIP == "":
		return "asia server has no registered public address"
	case t.AsiaStatus != runningStatus:
		return fmt.Sprintf("asia server status %d is not RUNNING", t.AsiaStatus)
	}
	return ""
}

func (t target) host() string { return fmt.Sprintf("team%d-router", t.Team) }

// Host key aliases include instance IDs, so a recreated stack does not collide
// with the keys recorded for its predecessor.
func sshConfig(targets []target, key, knownHosts string) string {
	var b strings.Builder
	for _, t := range targets {
		if t.skipReason() != "" {
			continue
		}
		fmt.Fprintf(&b, "Host team%d-asia\n  HostName %s\n  HostKeyAlias team%d-%s\n", t.Team, t.AsiaIP, t.Team, t.AsiaID)
		fmt.Fprintf(&b, "Host %s\n  HostName 192.168.10.10\n  HostKeyAlias team%d-%s\n  ProxyJump team%d-asia\n", t.host(), t.Team, t.RouterID, t.Team)
	}
	fmt.Fprintf(&b, `Host *
  User isuadmin
  IdentityFile %q
  IdentitiesOnly yes
  IdentityAgent none
  ForwardAgent no
  ForwardX11 no
  ClearAllForwardings yes
  BatchMode yes
  StrictHostKeyChecking accept-new
  UserKnownHostsFile %q
  ConnectTimeout 15
  ServerAliveInterval 15
  ServerAliveCountMax 4
  LogLevel ERROR
`, key, knownHosts)
	return b.String()
}

func fetchKey(ctx context.Context, secret, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "aws", "secretsmanager", "get-secret-value", "--secret-id", secret, "--query", "SecretString", "--output", "text")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read %s: %w: %s", secret, err, strings.TrimSpace(stderr.String()))
	}
	path := filepath.Join(dir, "isuadmin.key")
	return path, os.WriteFile(path, []byte(strings.TrimSpace(string(out))+"\n"), 0o600)
}

type result struct {
	Team    int64     `json:"team"`
	Router  string    `json:"router"`
	Outcome string    `json:"outcome"` // done, would-apply, applied, skipped or failed
	Detail  string    `json:"detail,omitempty"`
	At      time.Time `json:"at"`
}

type runner struct {
	config string
	apply  bool
}

func (r *runner) process(ctx context.Context, t target) result {
	res := result{Team: t.Team, Router: t.RouterID}
	if reason := t.skipReason(); reason != "" {
		res.Outcome, res.Detail = "skipped", reason
		return res
	}
	before, err := r.inspect(ctx, t)
	if err != nil {
		res.Outcome, res.Detail = "failed", err.Error()
		return res
	}
	d, err := decide(t, before)
	switch {
	case err != nil:
		res.Outcome, res.Detail = "failed", err.Error()
	case d.skip != "":
		res.Outcome, res.Detail = "skipped", d.skip
	case d.done:
		res.Outcome = "done"
	case !r.apply:
		res.Outcome, res.Detail = "would-apply", d.describe()
	default:
		if err := r.install(ctx, t, before, d); err != nil {
			res.Outcome, res.Detail = "failed", err.Error()
		} else {
			res.Outcome, res.Detail = "applied", d.describe()
		}
	}
	return res
}

func (r *runner) ssh(ctx context.Context, t target, script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-F", r.config, t.host(), "sudo -n bash -s")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ssh: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (r *runner) inspect(ctx context.Context, t target) (routerState, error) {
	out, err := r.ssh(ctx, t, inspectScript)
	if err != nil {
		return routerState{}, err
	}
	return parseState(out)
}

// install applies the block, then moves the baseline to the resulting ruleset
// after checking that nothing else in it changed.
func (r *runner) install(ctx context.Context, t target, before routerState, d decision) error {
	if _, err := r.ssh(ctx, t, installScript); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	after, err := r.inspect(ctx, t)
	if err != nil {
		return err
	}
	if !bytes.Equal(after.baseline, before.baseline) {
		return errors.New("baseline changed during install")
	}
	ruleset, err := verifyInstalled(after, d.baseline)
	if err != nil {
		return fmt.Errorf("after install: %w", err)
	}
	patched, err := patchBaseline(after.baseline, d.baseline, ruleset)
	if err != nil {
		return err
	}
	if _, err := r.ssh(ctx, t, baselineScript(after.baseline, patched)); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	final, err := r.inspect(ctx, t)
	if err != nil {
		return err
	}
	if !bytes.Equal(final.baseline, patched) {
		return errors.New("baseline on the Router differs from the one written")
	}
	if live, err := contestcheck.NormalizeRuntime(final.ruleset); err != nil || live != ruleset {
		return errors.New("live nftables ruleset changed after the baseline was written")
	}
	return nil
}

type routerState struct {
	instanceID              string
	registration, dataIP    bool
	nftFile, unitFile       string
	unitEnabled, unitActive string
	table                   string
	baseline, ruleset       []byte
}

func parseState(out []byte) (routerState, error) {
	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	keys := []string{"instance_id", "registration", "data_ip", "nft_file", "unit_file", "unit_enabled", "unit_active", "table", "baseline", "ruleset"}
	for _, key := range keys {
		if _, ok := values[key]; !ok {
			return routerState{}, fmt.Errorf("inspection output lacks %s", key)
		}
	}
	decoded := map[string][]byte{}
	for _, key := range []string{"table", "baseline", "ruleset"} {
		b, err := base64.StdEncoding.DecodeString(values[key])
		if err != nil {
			return routerState{}, fmt.Errorf("inspection %s: %w", key, err)
		}
		decoded[key] = b
	}
	if len(decoded["ruleset"]) == 0 {
		return routerState{}, errors.New("nft list ruleset returned nothing")
	}
	s := routerState{
		instanceID:   values["instance_id"],
		registration: values["registration"] == "yes",
		dataIP:       values["data_ip"] == "yes",
		nftFile:      values["nft_file"],
		unitFile:     values["unit_file"],
		unitEnabled:  values["unit_enabled"],
		unitActive:   values["unit_active"],
		table:        string(decoded["table"]),
		ruleset:      decoded["ruleset"],
	}
	if len(decoded["baseline"]) > 0 {
		s.baseline = decoded["baseline"]
	}
	return s, nil
}

type decision struct {
	skip     string // the Router is left alone
	done     bool
	partial  bool   // the block is live but the baseline does not have it yet
	baseline string // the baseline's nftables entry
}

func (d decision) describe() string {
	if d.partial {
		return "block present, baseline not updated"
	}
	return "block missing"
}

func decide(t target, s routerState) (decision, error) {
	if s.instanceID != t.RouterID {
		return decision{}, fmt.Errorf("reached instance %q, portal has Router %s", s.instanceID, t.RouterID)
	}
	if !s.registration || !s.dataIP {
		return decision{skip: "not a CloudFormation Router"}, nil
	}
	if s.baseline == nil {
		return decision{skip: "envcheck baseline not recorded yet"}, nil
	}
	recorded, err := baselineNFT(s.baseline)
	if err != nil {
		return decision{}, err
	}
	live, err := contestcheck.NormalizeRuntime(s.ruleset)
	if err != nil {
		return decision{}, err
	}
	others, tables, err := withoutBlock(s.ruleset)
	if err != nil {
		return decision{}, err
	}
	_, recordedTables, err := withoutBlock([]byte(recorded))
	if err != nil {
		return decision{}, err
	}
	d := decision{baseline: recorded}
	switch {
	case !hasBlock(tables):
		if live != recorded {
			return d, errors.New("live nftables ruleset differs from the baseline")
		}
	case hasBlock(recordedTables):
		if live != recorded {
			return d, errors.New("live nftables ruleset differs from the baseline")
		}
		if err := checkInstalled(s); err != nil {
			return d, err
		}
		d.done = true
		return d, nil
	default:
		if others != recorded {
			return d, errors.New("apart from the block, live nftables ruleset differs from the baseline")
		}
		d.partial = true
	}
	// Recreating isureal_router puts it last; any table after it would move.
	if rest := withoutName(tables, blockTable); len(rest) == 0 || rest[len(rest)-1] != routerTable {
		return d, fmt.Errorf("isureal_router is not the last table: %v", tables)
	}
	return d, nil
}

var counterValues = regexp.MustCompile(`counter packets \d+ bytes \d+`)

func checkInstalled(s routerState) error {
	switch {
	case s.nftFile != digest([]byte(nftRules)):
		return errors.New("data-proxy-block.nft differs from the template")
	case s.unitFile != digest([]byte(unitFile)):
		return errors.New("isucon-data-proxy-block.service differs from the template")
	case s.unitEnabled != "enabled" || s.unitActive != "active":
		return fmt.Errorf("isucon-data-proxy-block.service is %s/%s", s.unitEnabled, s.unitActive)
	case counterValues.ReplaceAllString(s.table, "counter") != expectedTable:
		return fmt.Errorf("unexpected live table:\n%s", s.table)
	}
	return nil
}

// verifyInstalled returns the normalized ruleset the baseline should hold.
func verifyInstalled(s routerState, recorded string) (string, error) {
	if err := checkInstalled(s); err != nil {
		return "", err
	}
	others, tables, err := withoutBlock(s.ruleset)
	if err != nil {
		return "", err
	}
	if others != recorded {
		return "", errors.New("apart from the block, live nftables ruleset differs from the baseline")
	}
	if n := len(tables); n < 2 || tables[n-2] != blockTable || tables[n-1] != routerTable {
		return "", fmt.Errorf("table order is %v, want the block just before isureal_router", tables)
	}
	return contestcheck.NormalizeRuntime(s.ruleset)
}

func baselineNFT(baseline []byte) (string, error) {
	var snapshot struct {
		Router map[string]string `json:"router"`
	}
	if err := json.Unmarshal(baseline, &snapshot); err != nil {
		return "", fmt.Errorf("baseline: %w", err)
	}
	nft, ok := snapshot.Router["nft"]
	if !ok {
		return "", errors.New("baseline has no Router nftables entry")
	}
	return nft, nil
}

// patchBaseline swaps the nftables entry in place, keeping every other byte.
func patchBaseline(baseline []byte, recorded, replacement string) ([]byte, error) {
	from, _ := json.Marshal(recorded)
	to, _ := json.Marshal(replacement)
	if n := bytes.Count(baseline, from); n != 1 {
		return nil, fmt.Errorf("baseline holds its nftables entry %d times", n)
	}
	patched := bytes.Replace(baseline, from, to, 1)
	var a, b map[string]json.RawMessage
	if err := json.Unmarshal(baseline, &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(patched, &b); err != nil {
		return nil, err
	}
	var ra, rb map[string]string
	if err := json.Unmarshal(a["router"], &ra); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b["router"], &rb); err != nil {
		return nil, err
	}
	if rb["nft"] != replacement {
		return nil, errors.New("patched baseline lost the new nftables entry")
	}
	delete(a, "router")
	delete(b, "router")
	delete(ra, "nft")
	delete(rb, "nft")
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(ra, rb) {
		return nil, errors.New("patching changed more than the nftables entry")
	}
	return patched, nil
}

// withoutBlock normalizes a ruleset without the block's table, chains and rules,
// and lists every table in order.
func withoutBlock(ruleset []byte) (string, []string, error) {
	var doc struct {
		Nftables []json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(ruleset, &doc); err != nil {
		return "", nil, fmt.Errorf("nftables JSON: %w", err)
	}
	var kept []json.RawMessage
	var tables []string
	for _, item := range doc.Nftables {
		var object map[string]struct {
			Family string `json:"family"`
			Name   string `json:"name"`
			Table  string `json:"table"`
		}
		if err := json.Unmarshal(item, &object); err != nil {
			return "", nil, fmt.Errorf("nftables JSON: %w", err)
		}
		table := ""
		for kind, v := range object {
			if kind == "table" {
				table = v.Family + " " + v.Name
				tables = append(tables, table)
			} else if v.Table != "" {
				table = v.Family + " " + v.Table
			}
		}
		if table != blockTable {
			kept = append(kept, item)
		}
	}
	body, err := json.Marshal(map[string]any{"nftables": kept})
	if err != nil {
		return "", nil, err
	}
	normalized, err := contestcheck.NormalizeRuntime(body)
	return normalized, tables, err
}

func hasBlock(tables []string) bool { return len(withoutName(tables, blockTable)) != len(tables) }

func withoutName(tables []string, name string) []string {
	var rest []string
	for _, table := range tables {
		if table != name {
			rest = append(rest, table)
		}
	}
	return rest
}

func encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
