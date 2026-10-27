package cloudformation

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/config"
)

func TestRegistrationBootstrap(t *testing.T) {
	cmd := exec.Command("python3", "-m", "unittest", "discover", "-s", "tests")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("registration script: %v\n%s", err, out)
	}
	document, err := ContestTemplate(&config.Config{PublicURLHost: "portal.example", ContestAMIID: "ami-test", CheckerSecret: []byte("test")}, 1, "apne1-az1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(document, "WantedBy=cloud-init.target") != 6 {
		t.Fatal("units ordered after cloud-final cannot hold multi-user.target open")
	}
	// Starting a unit ordered after cloud-final synchronously from runcmd deadlocks.
	if strings.Contains(document, "systemctl enable --now isucon-register.service") {
		t.Fatal("registration must start asynchronously from cloud-init")
	}
	if strings.Count(document, "After=network-online.target cloud-final.service") != 6 {
		t.Fatal("registration and Router setup must wait for cloud-init")
	}
	if strings.Count(document, "Restart=on-failure") != 6 {
		t.Fatal("EIP and ENI attachment races must retry")
	}
}

func TestContestTemplateRequiresRouterAMIParameter(t *testing.T) {
	cfg := &config.Config{PublicURLHost: "portal.example", ContestAMIID: "ami-service", CheckerSecret: []byte("test")}
	document, err := ContestTemplate(cfg, 1, "apne1-az1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "Default: ami-") {
		t.Fatal("unconfigured Router AMI must be an explicit parameter")
	}
	// The service image is not a Router image and must never be reused as its default.
	if !strings.Contains(document, "RouterAMIID:\n    Type: AWS::EC2::Image::Id") {
		t.Fatal("missing Router image parameter")
	}
	if len(document) > 51200 {
		t.Fatalf("template exceeds CloudFormation upload limit: %d", len(document))
	}
}
