package cloudformation

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/config"
)

// Volumes restored from the AMI snapshot load lazily, so without an initialization
// rate the database's first reads after launch drop to around 10 MB/s.
func TestContestServerVolumesInitializeAtLaunch(t *testing.T) {
	cfg := &config.Config{IsuconAdminSSHKeyPath: adminKey(t), PublicURLHost: "portal-dev.isucon.net", ContestAMIID: "ami-service", RouterAMIID: "ami-router", CheckerSecret: []byte("test")}
	document, err := ContestTemplate(cfg, 60001, "apne1-az2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	nextResource := regexp.MustCompile(`\n  \S`)
	for _, name := range []string{"ServiceLaunchTemplate", "DatabaseLaunchTemplate"} {
		_, rest, ok := strings.Cut(document, "\n  "+name+":\n")
		if !ok {
			t.Fatalf("missing %s", name)
		}
		launchTemplate := nextResource.Split(rest, 2)[0]
		if !strings.Contains(launchTemplate, "\n            Ebs:\n") || !strings.Contains(launchTemplate, "\n              VolumeInitializationRate: 300\n") {
			t.Errorf("%s must initialize its root volume at 300 MiB/s like provisioning/aws", name)
		}
	}
}
