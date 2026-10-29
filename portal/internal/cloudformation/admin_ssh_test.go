package cloudformation

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/config"
)

func adminKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "admin")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("keygen: %v %s", err, out)
	}
	return path
}

func TestAdminSSHBootstrap(t *testing.T) {
	for _, render := range []struct {
		name string
		fn   func(*config.Config, int64, string, time.Time) (string, error)
	}{{"test", TestTemplate}, {"contest", ContestTemplate}} {
		t.Run(render.name, func(t *testing.T) {
			count := 5
			if render.name == "contest" {
				count = 4
			}
			cfg := &config.Config{PublicURLHost: "portal.example", TestAMIID: "ami-test", ContestAMIID: "ami-test", CheckerSecret: []byte("test"), IsuconAdminSSHKeyPath: adminKey(t)}
			document, err := render.fn(cfg, 60001, "apne1-az2", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			public, _ := os.ReadFile(cfg.IsuconAdminSSHKeyPath + ".pub")
			parts := strings.Fields(string(public))
			encoded := parts[0] + " " + parts[1]
			if strings.Count(document, encoded) != count {
				t.Fatal("all five instances must receive the configured administration public key")
			}
			for _, want := range []string{"- default", "- name: isuadmin", "shell: /bin/bash", "lock_passwd: true", "sudo: ['ALL=(ALL) NOPASSWD:ALL']"} {
				if strings.Count(document, want) != count {
					t.Errorf("missing administration bootstrap: %s", want)
				}
			}
			private, _ := os.ReadFile(cfg.IsuconAdminSSHKeyPath)
			if strings.Contains(document, string(private)) || strings.Contains(document, base64.StdEncoding.EncodeToString(private)) || strings.Contains(document, "PRIVATE KEY") {
				t.Fatal("private key leaked into template")
			}
			if len(document) > 51200 {
				t.Fatalf("template upload limit exceeded: %d", len(document))
			}
			cfg.IsuconAdminSSHKeyPath = adminKey(t)
			other, err := render.fn(cfg, 60001, "apne1-az2", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(other, encoded) {
				t.Fatal("template reused another environment's key")
			}
		})
	}
}

func TestAdminSSHKeyRequired(t *testing.T) {
	for _, contents := range []string{"", "not a private key"} {
		cfg := &config.Config{TestAMIID: "ami-test", CheckerSecret: []byte("test")}
		if contents != "" {
			cfg.IsuconAdminSSHKeyPath = filepath.Join(t.TempDir(), "invalid")
			os.WriteFile(cfg.IsuconAdminSSHKeyPath, []byte(contents), 0600)
		}
		for _, render := range []func(*config.Config, int64, string, time.Time) (string, error){TestTemplate, ContestTemplate} {
			document, err := render(cfg, 1, "apne1-az1", time.Now())
			if err == nil || document != "" {
				t.Fatal("missing/invalid admin key must fail closed")
			}
		}
	}
}
