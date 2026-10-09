package contract_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/cloudformation"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/token"
)

func cfnConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		PublicURLHost:     "portal.test.invalid",
		TestAMIID:         "ami-test",
		ContestAMIID:      "ami-contest",
		CheckerSecret:     checkerSecret(t),
		AvailabilityZones: []string{"apne1-az1", "apne1-az2", "apne1-az4"},
		Timing: config.Timing{
			ContestStart: timePtr(time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)),
			ContestEnd:   timePtr(time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)),
		},
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// FR-C-7 / FR-S-5: CFn テンプレートには AZ・AMI・portal_host と、
// base64(JSON) の portal_credentials（検証可能な checker token 入り）が埋まる。
func TestCloudFormationTemplateContract(t *testing.T) {
	cfg := cfnConfig(t)
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name   string
		render func() (string, error)
		amiID  string
		expiry int64
	}{
		{
			name:   "test template",
			render: func() (string, error) { return cloudformation.TestTemplate(cfg, 42, "apne1-az2", now) },
			amiID:  "ami-test",
			// test は contest_start 起点（非 staging）
			expiry: cfg.Timing.ContestStart.Unix(),
		},
		{
			name:   "contest template",
			render: func() (string, error) { return cloudformation.ContestTemplate(cfg, 42, "apne1-az2", now) },
			amiID:  "ami-contest",
			// contest は contest_end + 24h
			expiry: cfg.Timing.ContestEnd.Unix() + 24*60*60,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			template, err := tc.render()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(template, "AWSTemplateFormatVersion") {
				t.Error("template must be a CloudFormation document")
			}
			if !strings.Contains(template, "apne1-az2") {
				t.Error("template must embed the availability zone")
			}
			if !strings.Contains(template, tc.amiID) {
				t.Errorf("template must embed AMI %s", tc.amiID)
			}
			if !strings.Contains(template, "portal.test.invalid") {
				t.Error("template must embed portal host")
			}

			creds := extractPortalCredentials(t, template)
			var decoded struct {
				Token string `json:"token"`
				Dev   bool   `json:"dev"`
				Host  string `json:"host"`
			}
			if err := json.Unmarshal(creds, &decoded); err != nil {
				t.Fatalf("portal_credentials must be base64(JSON): %v", err)
			}
			if decoded.Host != "portal.test.invalid" {
				t.Errorf("credentials host = %q", decoded.Host)
			}
			payload := token.VerifyChecker(cfg.CheckerSecret, decoded.Token)
			if payload == nil {
				t.Fatal("embedded checker token must verify")
			}
			if payload.TeamID != 42 {
				t.Errorf("token team_id = %d, want 42", payload.TeamID)
			}
			if payload.Expiry != tc.expiry {
				t.Errorf("token expiry = %d, want %d", payload.Expiry, tc.expiry)
			}
		})
	}
}

func TestCloudFormationTestTemplateRequiresAMI(t *testing.T) {
	cfg := cfnConfig(t)
	cfg.TestAMIID = ""
	if _, err := cloudformation.TestTemplate(cfg, 1, "apne1-az1", time.Now()); err == nil {
		t.Error("test template without AMI must fail")
	}
}

func TestEnvCheckTemplateUsesISUCON2026(t *testing.T) {
	cfg := cfnConfig(t)
	template, err := cloudformation.TestTemplate(cfg, 42, "apne1-az2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AWS CloudFormation for ISUCON2026 Environment Checks", "Value: isucon2026-envcheck-001", "InstanceType: c7i.large"} {
		if !strings.Contains(template, want) {
			t.Errorf("envcheck template must contain %q", want)
		}
	}
	if strings.Contains(strings.ToLower(template), "isucon14") {
		t.Error("envcheck template must not retain ISUCON14 branding")
	}
	for text, count := range map[string]int{
		"Type: AWS::EC2::Instance\n":                            5,
		"InstanceType: c7i.large":                               3,
		"InstanceType: c6in.large":                              2,
		"Type: AWS::EC2::EIP\n":                                 5,
		"path: /opt/isucon-env-checker/portal_credentials.json": 5,
	} {
		if got := strings.Count(template, text); got != count {
			t.Errorf("%q count = %d, want %d", text, got, count)
		}
	}
	for _, name := range []string{"isucon2026-envcheck-001", "isucon2026-envcheck-002", "isucon2026-envcheck-003", "isucon2026-envcheck-004", "isucon2026-envcheck-005"} {
		if !strings.Contains(template, "Value: "+name) {
			t.Errorf("missing envcheck instance %s", name)
		}
	}
	for _, line := range strings.Split(template, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Value:") || strings.HasPrefix(trimmed, "Description:") || (strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":")) {
			for _, role := range []string{"app", "db", "router"} {
				if strings.Contains(strings.ToLower(line), role) {
					t.Errorf("envcheck template exposes role %q in %q", role, line)
				}
			}
		}
	}
}

func TestCloudFormationRequiresZone(t *testing.T) {
	cfg := cfnConfig(t)
	if _, err := cloudformation.ContestTemplate(cfg, 1, "", time.Now()); err == nil {
		t.Error("template without availability zone must fail")
	}
}

// extractPortalCredentials はテンプレート中の base64(JSON) を探して返す。
func extractPortalCredentials(t *testing.T, template string) []byte {
	t.Helper()
	for _, line := range strings.Split(template, "\n") {
		for _, word := range strings.Fields(strings.NewReplacer(`"`, " ", `'`, " ").Replace(line)) {
			if len(word) < 32 {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(word)
			if err != nil {
				continue
			}
			if strings.HasPrefix(string(raw), "{") && strings.Contains(string(raw), `"token"`) {
				return raw
			}
		}
	}
	t.Fatal("portal_credentials not found in template")
	return nil
}

func TestContestTemplateProvidesFourServers(t *testing.T) {
	template, err := cloudformation.ContestTemplate(cfnConfig(t), 42, "apne1-az2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(template, "Type: AWS::EC2::Instance\n"); got != 4 {
		t.Errorf("instance count = %d, want 4", got)
	}
	if got := strings.Count(template, "Type: AWS::EC2::EIP\n"); got != 4 {
		t.Errorf("public IP count = %d, want 4", got)
	}
	for n := 1; n <= 4; n++ {
		for _, want := range []string{fmt.Sprintf("  Instance%d:", n), fmt.Sprintf("InstanceId: !Ref Instance%d", n), fmt.Sprintf("PrivateIpAddress: \"192.168.0.1%d\"", n)} {
			if !strings.Contains(template, want) {
				t.Errorf("missing %s", want)
			}
		}
	}
}
