package contestcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Command func(context.Context, string, ...string) ([]byte, error)

func RunCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w", name, err)
	}
	return output, nil
}

// CollectRouter only inspects the protected router. It never changes sshd,
// routing or traffic-control state.
func CollectRouter(ctx context.Context, run Command) (map[string]string, error) {
	values := map[string]string{}
	commands := map[string][]string{
		"qdisc":  {"tc", "-j", "qdisc", "show", "dev", "data0"},
		"class":  {"tc", "class", "show", "dev", "data0"},
		"filter": {"tc", "-j", "filter", "show", "dev", "data0", "parent", "1:"},
		"nft":    {"nft", "-j", "list", "ruleset"},
		"routes": {"ip", "-j", "route", "show", "table", "all"},
		"rules":  {"ip", "-j", "rule", "show"},
	}
	for key, args := range commands {
		out, err := run(ctx, args[0], args[1:]...)
		if err != nil {
			return nil, err
		}
		normalize := NormalizeRuntime
		if key == "class" {
			normalize = NormalizeTCClasses
		}
		value, err := normalize(out)
		if err != nil {
			return nil, fmt.Errorf("router %s: %w", key, err)
		}
		values[key] = value
	}
	if !strings.Contains(values["qdisc"], `"netem"`) || !strings.Contains(values["qdisc"], `"htb"`) || !strings.Contains(values["filter"], `"u32"`) || !strings.Contains(values["nft"], "isureal_router") {
		return nil, fmt.Errorf("router traffic control is not ready")
	}
	for _, service := range []string{"isureal-router", "isureal-haproxy"} {
		if _, err := run(ctx, "systemctl", "is-active", "--quiet", service); err != nil {
			return nil, err
		}
	}
	access, err := CheckRouterAccess(ctx, run)
	if err != nil {
		return nil, err
	}
	values["sshd"] = access
	if keys, err := os.ReadFile("/home/isucon/.ssh/authorized_keys"); err == nil && len(strings.TrimSpace(string(keys))) > 0 {
		return nil, fmt.Errorf("participant keys installed on router")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	keyFiles, err := filepath.Glob("/home/*/.ssh/authorized_keys")
	if err != nil {
		return nil, err
	}
	for _, path := range keyFiles {
		if path == "/home/isuadmin/.ssh/authorized_keys" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				return nil, fmt.Errorf("non-administration SSH key on router: %s", path)
			}
		}
	}
	forwarding, err := run(ctx, "sysctl", "-n", "net.ipv4.ip_forward", "net.ipv4.conf.all.rp_filter", "net.ipv4.conf.data0.rp_filter")
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(forwarding), "1\n") {
		return nil, fmt.Errorf("router IP forwarding is disabled")
	}
	values["forwarding"] = string(forwarding)
	state, err := haproxyCommand(ctx, "show servers state")
	if err != nil {
		return nil, err
	}
	values["haproxy_servers"], err = NormalizeHAProxyState(state)
	if err != nil {
		return nil, err
	}
	paths := []string{"/usr/sbin/haproxy", "/usr/sbin/sshd", "/etc/sysctl.d/90-isucon-router.conf", "/run/isureal-haproxy", "/etc/ssh", "/etc/isucon/router.env", "/etc/isucon/portal.env", "/etc/isucon/haproxy", "/etc/netplan", "/opt/isucon/bin", "/etc/systemd/system/isureal-router.service", "/etc/systemd/system/isureal-haproxy.service", "/home/isuadmin/.ssh/authorized_keys"}
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if filepath.Base(path) == "tls" {
					return filepath.SkipDir
				}
				return nil
			}
			// Socket/runtime files are not configuration. Symlinks are inspected through
			// ReadFile so replacing a protected file with a symlink changes its digest.
			if !d.Type().IsRegular() && d.Type()&os.ModeSymlink == 0 {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			values["file:"+path] = hex.EncodeToString(sum[:])
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func CheckRouterAccess(ctx context.Context, run Command) (string, error) {
	pkg, err := run(ctx, "dpkg-query", "-W", "-f=${db:Status-Status}", "ec2-instance-connect")
	if err == nil && strings.TrimSpace(string(pkg)) != "not-installed" {
		return "", fmt.Errorf("EC2 Instance Connect is not fully removed")
	}
	if err != nil { // Command failure must be a genuine missing-package result.
		if !strings.Contains(string(pkg), "no packages found matching") {
			return "", fmt.Errorf("cannot establish EC2 Instance Connect package state")
		}
	}
	ssh, err := run(ctx, "sshd", "-T", "-C", "user=isucon,host=localhost,addr=127.0.0.1")
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(ssh), "authorizedkeyscommand none\n") {
		return "", fmt.Errorf("SSH AuthorizedKeysCommand is enabled")
	}
	return string(ssh), nil
}

func NormalizeHAProxyState(output string) (string, error) {
	var header []string
	var records []string
	required := []string{"be_name", "srv_name", "srv_addr", "srv_port", "srv_admin_state", "srv_uweight", "srv_iweight"}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "# ") {
			header = strings.Fields(strings.TrimPrefix(line, "# "))
			continue
		}
		fields := strings.Fields(line)
		if len(fields) <= 1 {
			continue
		}
		if len(fields) != len(header) {
			return "", fmt.Errorf("invalid HAProxy server state")
		}
		values := map[string]string{}
		for i, key := range header {
			values[key] = fields[i]
		}
		record := map[string]string{}
		for _, key := range required {
			value, ok := values[key]
			if !ok {
				return "", fmt.Errorf("HAProxy server state missing %s", key)
			}
			record[key] = value
		}
		data, _ := json.Marshal(record)
		records = append(records, string(data))
	}
	if len(records) == 0 {
		return "", fmt.Errorf("HAProxy has no backends")
	}
	return canonicalSet(records), nil
}
func haproxyCommand(ctx context.Context, command string) (string, error) {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:18001")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}
	if _, err = io.WriteString(conn, command+"\n"); err != nil {
		return "", err
	}
	output, err := io.ReadAll(io.LimitReader(conn, 1<<20))
	return string(output), err
}

// NormalizeTCClasses accepts tc's configuration-only text output. Ubuntu's tc
// does not emit JSON for HTB classes even when -j is specified.
func NormalizeTCClasses(output []byte) (string, error) {
	var records []string
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 3 || fields[0] != "class" {
			return "", fmt.Errorf("invalid tc class configuration")
		}
		records = append(records, strings.Join(fields, " "))
	}
	if len(records) == 0 {
		return "", fmt.Errorf("router has no traffic-control classes")
	}
	return canonicalSet(records), nil
}
