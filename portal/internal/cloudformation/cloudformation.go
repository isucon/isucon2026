// Package cloudformation は競技環境の CloudFormation テンプレート生成（FR-C-7 / FR-S-5）。
// isucon14-portal の app/models/cloud_formation.rb + cf_templates/*.yaml.erb の移植。
package cloudformation

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"strings"
	"text/template"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/token"
)

//go:embed templates/*.yaml.tmpl files/*.py
var templatesFS embed.FS

var templates = template.Must(template.ParseFS(templatesFS, "templates/*.yaml.tmpl"))

type params struct {
	TeamID            int64
	ZoneID            string
	Token             string
	PortalCredentials string
	PortalHost        string
	AMIID             string
	RouterAMIID       string
	AdminSSHPublicKey string
}

type envCheckInstance struct {
	LogicalID    string
	Number       string
	InstanceType string
}

// EnvCheckInstances matches the production defaults in provisioning/aws/config.ts:
// App1 (asia) and the database are c5.large, the overseas apps c7i.large and the
// router c6in.large.
func (params) EnvCheckInstances() []envCheckInstance {
	return []envCheckInstance{
		{"EnvcheckInstance001", "001", "c5.large"},
		{"EnvcheckInstance002", "002", "c7i.large"},
		{"EnvcheckInstance003", "003", "c7i.large"},
		{"EnvcheckInstance004", "004", "c5.large"},
		{"EnvcheckInstance005", "005", "c6in.large"},
	}
}

// TestTemplate は envcheck 用（test.yaml）。
func TestTemplate(cfg *config.Config, teamID int64, zoneID string, now time.Time) (string, error) {
	if cfg.TestAMIID == "" {
		return "", errors.New("AMI ID should be set to test")
	}
	expiry := testTokenExpiry(cfg, now)
	return render(cfg, "test.yaml.tmpl", teamID, zoneID, cfg.TestAMIID, expiry)
}

// ContestTemplate は競技用（contest.yaml）。
func ContestTemplate(cfg *config.Config, teamID int64, zoneID string, now time.Time) (string, error) {
	expiry := contestTokenExpiry(cfg, now)
	return render(cfg, "contest.yaml.tmpl", teamID, zoneID, cfg.ContestAMIID, expiry)
}

func render(cfg *config.Config, name string, teamID int64, zoneID, amiID string, expiry int64) (string, error) {
	if zoneID == "" {
		return "", errors.New("Availability Zone should be set to team")
	}
	key, err := os.ReadFile(cfg.IsuconAdminSSHKeyPath)
	if err != nil {
		return "", fmt.Errorf("administration SSH key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("administration SSH key: %w", err)
	}
	publicKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	tok, err := token.CreateChecker(cfg.CheckerSecret, token.CheckerPayload{TeamID: teamID, Expiry: expiry})
	if err != nil {
		return "", err
	}
	credsJSON, err := json.Marshal(map[string]any{
		"token": tok,
		"dev":   cfg.CloudFormationStaging,
		"host":  cfg.PublicURLHost,
	})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	err = templates.ExecuteTemplate(&sb, name, params{
		TeamID:            teamID,
		ZoneID:            zoneID,
		Token:             tok,
		PortalCredentials: base64.StdEncoding.EncodeToString(credsJSON),
		PortalHost:        cfg.PublicURLHost,
		AMIID:             amiID,
		RouterAMIID:       cfg.RouterAMIID,
		AdminSSHPublicKey: publicKey,
	})
	return sb.String(), err
}

func testTokenExpiry(cfg *config.Config, now time.Time) int64 {
	if cfg.CloudFormationStaging {
		return now.Unix() + 24*60*60
	}
	if cfg.Timing.ContestStart != nil {
		return cfg.Timing.ContestStart.Unix()
	}
	return 0
}

func contestTokenExpiry(cfg *config.Config, now time.Time) int64 {
	if cfg.CloudFormationStaging {
		return now.Unix() + 60*60
	}
	if cfg.Timing.ContestEnd != nil {
		return cfg.Timing.ContestEnd.Unix() + 24*60*60
	}
	return 24 * 60 * 60
}
