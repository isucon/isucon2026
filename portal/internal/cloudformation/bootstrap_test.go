package cloudformation

import (
	"fmt"
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
	document, err := ContestTemplate(&config.Config{IsuconAdminSSHKeyPath: adminKey(t), PublicURLHost: "portal.example", ContestAMIID: "ami-test", CheckerSecret: []byte("test")}, 1, "apne1-az1", time.Now())
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
	cfg.IsuconAdminSSHKeyPath = adminKey(t)
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
	cfg := &config.Config{IsuconAdminSSHKeyPath: adminKey(t), PublicURLHost: "portal-dev.isucon.net", ContestAMIID: "ami-01a0c5844ea355efd", RouterAMIID: "ami-069a13171cf378369", CheckerSecret: []byte("test"), CloudFormationStaging: true}
	document, err := ContestTemplate(cfg, 60001, "apne1-az2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(document) > 51200 {
		t.Fatalf("template exceeds upload limit: %d", len(document))
	}
}

// The image leaves isureal-guard disabled; like Pulumi user-data, only apps start it.
func TestContestTemplateStartsGuardOnApps(t *testing.T) {
	cfg := &config.Config{IsuconAdminSSHKeyPath: adminKey(t), PublicURLHost: "portal-dev.isucon.net", ContestAMIID: "ami-service", RouterAMIID: "ami-router", CheckerSecret: []byte("test")}
	document, err := ContestTemplate(cfg, 60001, "apne1-az2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range []string{"App1", "App2", "App3", "Database"} {
		instance := strings.Split(strings.Split(document, "\n  "+server+":\n")[1], "\n  "+server+"IP:\n")[0]
		guard := strings.Index(instance, "- systemctl enable --now isureal-guard\n")
		if server == "Database" {
			if guard >= 0 {
				t.Error("the database must not run the guard")
			}
			continue
		}
		// The guard reads ISUREAL_DB_HOST from env.sh.
		if env := strings.Index(instance, "ISUREAL_DB_HOST=192.168.4.10"); env < 0 || guard < env {
			t.Errorf("%s must start the guard after configuring env.sh", server)
		}
	}
}

func TestContestRouterInterfacesAttachedAtLaunch(t *testing.T) {
	cfg := &config.Config{IsuconAdminSSHKeyPath: adminKey(t), PublicURLHost: "portal-dev.isucon.net", ContestAMIID: "ami-service", RouterAMIID: "ami-router", CheckerSecret: []byte("test")}
	document, err := ContestTemplate(cfg, 60001, "apne1-az2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, "AWS::EC2::NetworkInterfaceAttachment") || strings.Contains(document, "RouterDataAttachment") {
		t.Fatal("a separate attachment can change Router interfaces during CloudFormation stabilization")
	}
	router := strings.Split(strings.Split(document, "\n  Router:\n")[1], "      MetadataOptions:")[0]
	for index, name := range []string{"RouterPublicInterface", "RouterDataInterface"} {
		want := fmt.Sprintf("- DeviceIndex: '%d'\n          NetworkInterfaceId: !Ref %s", index, name)
		if !strings.Contains(router, want) {
			t.Errorf("Router must attach %s at launch", name)
		}
	}
	for _, server := range []string{"App1", "App2", "App3", "Database"} {
		if strings.Count(document, "DependsOn: ["+server+"Subnet, Router]") != 3 {
			t.Errorf("routes to %s must wait for the Router with both interfaces", server)
		}
	}
}

func TestContestRouterSSHIsolation(t *testing.T) {
	cfg := &config.Config{IsuconAdminSSHKeyPath: adminKey(t), PublicURLHost: "portal.example", ContestAMIID: "ami-service", RouterAMIID: "ami-router", CheckerSecret: []byte("test")}
	document, err := ContestTemplate(cfg, 1, "apne1-az1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	instance := func(id string) string {
		t.Helper()
		_, rest, ok := strings.Cut(document, "\n  "+id+":\n")
		if !ok {
			t.Fatalf("missing instance %s", id)
		}
		body, _, _ := strings.Cut(rest, "\n  "+id+"IP:\n")
		return body
	}
	router := instance("Router")
	if !strings.Contains(router, "bootcmd:\n            - [dpkg, --purge, ec2-instance-connect]") {
		t.Error("Router must remove EC2 Instance Connect during early boot")
	}
	if !strings.Contains(router, "- [systemctl, daemon-reload]\n            - [systemctl, try-restart, ssh.service]") {
		t.Error("Router must refresh SSH after removing Instance Connect configuration")
	}
	for _, forbidden := range []string{"/api/ssh_public_keys", "/home/isucon/.ssh", "KeyName:"} {
		if strings.Contains(router, forbidden) {
			t.Errorf("Router must not import contestant keys: %s", forbidden)
		}
	}
	if !strings.Contains(router, "- name: isuadmin") {
		t.Error("Router must retain administration access")
	}
	for _, id := range []string{"App1", "App2", "App3", "Database"} {
		body := instance(id)
		if strings.Count(body, "/api/ssh_public_keys?token=") != 1 {
			t.Errorf("%s must import contestant keys exactly once", id)
		}
		if strings.Contains(body, "--purge, ec2-instance-connect") {
			t.Errorf("%s must retain EC2 Instance Connect", id)
		}
	}
}
