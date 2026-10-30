package contestcheck

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRouterInstanceConnectRestriction(t *testing.T) {
	for _, tc := range []struct {
		name, pkg, ssh string
		packageErr     error
		wantOK         bool
	}{
		{"removed", "dpkg-query: no packages found matching ec2-instance-connect", "authorizedkeyscommand none\n", errors.New("exit 1"), true},
		{"reinstalled", "installed", "authorizedkeyscommand none\n", nil, false},
		{"unpacked", "unpacked", "authorizedkeyscommand none\n", nil, false},
		{"hook restored", "not-installed", "authorizedkeyscommand /usr/share/ec2-instance-connect/eic_run_authorized_keys\n", nil, false},
		{"cannot inspect", "", "authorizedkeyscommand none\n", errors.New("missing executable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "dpkg-query" {
					return []byte(tc.pkg), tc.packageErr
				}
				return []byte(tc.ssh), nil
			}
			_, err := CheckRouterAccess(context.Background(), run)
			if (err == nil) != tc.wantOK {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestHAProxyStateIgnoresUptimeButDetectsBackendRerouting(t *testing.T) {
	a := "1\n# be_name srv_name srv_addr srv_port srv_admin_state srv_uweight srv_iweight srv_time_since_last_change\napp1 app1 192.168.1.10 80 0 1 1 5\n"
	b := "1\n# be_name srv_name srv_addr srv_port srv_admin_state srv_uweight srv_iweight srv_time_since_last_change\napp1 app1 192.168.1.10 80 0 1 1 900\n"
	x, err := NormalizeHAProxyState(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := NormalizeHAProxyState(b)
	if err != nil {
		t.Fatal(err)
	}
	if x != y {
		t.Fatal("uptime changed backend")
	}
	z, _ := NormalizeHAProxyState(strings.ReplaceAll(b, "192.168.1.10", "192.168.2.10"))
	if x == z {
		t.Fatal("rerouting ignored")
	}
}

func TestTCClassesUbuntuTextOutput(t *testing.T) {
	first := "class htb 1:33 root leaf 33: prio 0 rate 1Gbit ceil 1Gbit burst 1600b cburst 1600b"
	second := "class htb 1:11 root leaf 11: prio 0 rate 1Gbit ceil 1Gbit burst 1600b cburst 1600b"
	x, err := NormalizeTCClasses([]byte(first + "\n" + second + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	y, err := NormalizeTCClasses([]byte(second + "\n  " + first + "\n"))
	if err != nil || x != y {
		t.Fatalf("class ordering or whitespace changed snapshot: %v", err)
	}
	for _, change := range []string{"rate 2Gbit", "ceil 2Gbit", "burst 3200b", "leaf 44:"} {
		old := map[string]string{"rate 2Gbit": "rate 1Gbit", "ceil 2Gbit": "ceil 1Gbit", "burst 3200b": "burst 1600b", "leaf 44:": "leaf 33:"}[change]
		z, err := NormalizeTCClasses([]byte(strings.Replace(first, old, change, 1) + "\n" + second))
		if err != nil || z == x {
			t.Fatalf("configuration change %q not detected: %v", change, err)
		}
	}
	for _, invalid := range []string{"", "[]", "Error: device missing"} {
		if _, err := NormalizeTCClasses([]byte(invalid)); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}
