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

func TestContestTemplateEmbedsConfiguredRouterAMI(t *testing.T) {
	t.Setenv("ISUXPORTAL_ROUTER_AMI_ID", "ami-069a13171cf378369")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ContestAMIID = "ami-service"
	cfg.CheckerSecret = []byte("test")
	document, err := ContestTemplate(cfg, 1, "apne1-az1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "RouterAMIID:") || strings.Contains(document, "!Ref RouterAMIID") {
		t.Fatal("Router AMI must not be a stack parameter")
	}
	if !strings.Contains(document, "ImageId: ami-069a13171cf378369") {
		t.Fatal("Router must use its configured image directly")
	}
	if !strings.Contains(document, "ImageId: ami-service") {
		t.Fatal("app and database must keep their separate service image")
	}

}

func TestContestTemplateUploadSizeForPortalDevTeam(t *testing.T) {
	cfg := &config.Config{PublicURLHost: "portal-dev.isucon.net", ContestAMIID: "ami-01a0c5844ea355efd", RouterAMIID: "ami-069a13171cf378369", CheckerSecret: []byte("test"), CloudFormationStaging: true}
	document, err := ContestTemplate(cfg, 60001, "apne1-az2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(document) > 51200 {
		t.Fatalf("template exceeds upload limit: %d", len(document))
	}
}
