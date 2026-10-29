package cloudformation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/config"
)

// Execute the actual cloud-init command with a failing/recovering portal and real
// ssh-keygen validation; never touch a system user's authorized_keys.
func TestSSHKeyBootstrap(t *testing.T) {
	for _, kind := range []string{"test", "contest"} {
		for _, mode := range []string{"http", "empty", "invalid", "mixed", "retry", "valid"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				cfg := &config.Config{IsuconAdminSSHKeyPath: adminKey(t), PublicURLHost: "portal.example.invalid", TestAMIID: "ami-test", ContestAMIID: "ami-test", CheckerSecret: []byte("test")}
				render := TestTemplate
				if kind == "contest" {
					render = ContestTemplate
				}
				document, err := render(cfg, 1, "test-zone", time.Now())
				if err != nil {
					t.Fatal(err)
				}
				rest := strings.SplitN(document, "          runcmd:\n", 2)
				if len(rest) != 2 {
					t.Fatal("missing runcmd")
				}
				lines := strings.Split(rest[1], "\n")
				var command string
				if strings.TrimSpace(lines[0]) == "- |" {
					for _, line := range lines[1:] {
						if !strings.HasPrefix(line, "              ") {
							break
						}
						command += strings.TrimPrefix(line, "              ") + "\n"
					}
				} else {
					command = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(lines[0]), "- '"), "'")
				}
				dir := t.TempDir()
				bin := filepath.Join(dir, "bin")
				keys := filepath.Join(dir, "keys")
				os.Mkdir(bin, 0700)
				os.Mkdir(keys, 0700)
				write := func(path, body string, perm os.FileMode) {
					t.Helper()
					if err := os.WriteFile(path, []byte(body), perm); err != nil {
						t.Fatal(err)
					}
				}
				key := filepath.Join(dir, "fixture")
				if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
					t.Fatalf("keygen: %s %v", out, err)
				}
				authorized := filepath.Join(keys, "authorized_keys")
				write(authorized, "existing key\n", 0600)
				for _, name := range []string{"chown", "install", "sleep"} {
					write(filepath.Join(bin, name), "#!/bin/sh\nexit 0\n", 0700)
				}
				write(filepath.Join(bin, "sudo"), "#!/bin/sh\nshift 2\nexec \"$@\"\n", 0700)
				write(filepath.Join(bin, "curl"), `#!/bin/sh
output=
fail=0
while [ $# -gt 0 ]; do
 case "$1" in
  --output|-o) output=$2; shift ;;
  --fail|-f) fail=1 ;;
 esac
 shift
done
n=0
[ ! -f "$COUNT" ] || n=$(cat "$COUNT")
n=$((n+1)); echo "$n" > "$COUNT"
mode=$MODE
if [ "$mode" = retry ]; then
 if [ "$n" -lt 3 ]; then mode=http; else mode=valid; fi
fi
emit() {
 case "$mode" in
  http) echo '<html>error</html>' ;;
  invalid) echo 'not a public key' ;;
  empty) echo ;;
  mixed) cat "$KEY"; echo 'not a public key' ;;
  valid) cat "$KEY"; cat "$KEY" ;;
 esac
}
if [ -n "$output" ]; then emit > "$output"; else emit; fi
if [ "$mode" = http ] && [ "$fail" = 1 ]; then exit 22; fi
`, 0700)
				command = strings.ReplaceAll(command, "/home/isucon/.ssh", keys)
				cmd := exec.Command("sh", "-c", command+"\nkey_status=$?\nprintf 'next-cloud-init-command\\n'\nexit \"$key_status\"\n")
				cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "MODE="+mode, "KEY="+key+".pub", "COUNT="+filepath.Join(dir, "count"))
				out, err := cmd.CombinedOutput()
				if !strings.Contains(string(out), "next-cloud-init-command") {
					t.Fatal("SSH key setup must not terminate the remaining cloud-init commands")
				}
				actual, _ := os.ReadFile(authorized)
				success := mode == "valid" || mode == "retry"
				if success {
					if err != nil {
						t.Fatalf("bootstrap failed: %v %s", err, out)
					}
					valid, _ := os.ReadFile(key + ".pub")
					if string(actual) != string(valid)+string(valid) {
						t.Fatal("did not install all valid keys")
					}
				} else {
					if err == nil {
						t.Error("failed key fetch must fail cloud-init command")
					}
					if string(actual) != "existing key\n" {
						t.Error("failed fetch replaced existing authorized_keys")
					}
				}
				stat, _ := os.Stat(authorized)
				if stat.Mode().Perm() != 0600 {
					t.Error("authorized_keys must remain 0600")
				}
				entries, _ := os.ReadDir(keys)
				if len(entries) != 1 {
					t.Error("temporary key files must be removed")
				}
			})
		}
	}
}
