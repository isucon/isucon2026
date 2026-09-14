// Package config は ISUXPORTAL_* 環境変数を構造体に読み込む。
// キー名は isucon14-portal の config/environments/development.rb:70-137 に合わせる（CT-11）。
// ISUXPORTAL_FINAL は単一開催（P-3）のため実装しない。
package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen        string
	MetricsListen string

	// DatabaseURL は PostgreSQL の接続 URL（ISUXPORTAL_DATABASE_URL）。
	DatabaseURL string

	RedisURL      string
	SessionSecret string

	PublicURLHost string
	Revision      string

	Timing Timing

	MaxTeams int

	AdminLogin    string
	AdminPassword string
	// AdminOnly は ISUXPORTAL_ADMIN_ONLY=1（リハーサル用の全体スイッチ、FR-AUTH-10）
	AdminOnly bool

	BenchToken string

	BypassTokenSecret []byte
	CheckerSecret     []byte
	SSHKeyAPISecret   string

	GitHub  OAuthProvider
	Discord Discord

	SlackWebhookURL string

	VAPIDPrivateKeyJSON string // JSON 文字列化した PEM（CT-10）
	VAPIDSubject        string

	AWSRegion        string
	AvatarBucketName string
	AvatarBasePath   string

	TestAMIID    string
	ContestAMIID string
	// CloudFormationStaging はトークン期限を発行時起点にする（ISUXPORTAL_CLOUDFORMATION_STAGING）
	CloudFormationStaging bool

	IsuconAdminSSHKeyPath string

	TermsURL string
	RulesURL string
	DocsURL  string

	AvailabilityZones []string

	SentryDSN         string
	SentryEnvironment string

	// PacksManifest は public/packs/manifest.json のパス（CT-3）。PublicDir は静的ファイルの根。
	PacksManifest string
	PublicDir     string

	// TestMode は RSpec ハーネス用のフック（時刻上書きヘッダ・セッション注入・
	// /debug エンドポイント・fake OAuth プロバイダ）を有効化する。本番では必ず無効。
	TestMode bool
}

// OAuthProvider は OAuth プロバイダの設定。エンドポイントはテスト時に差し替え可能。
type OAuthProvider struct {
	ClientID     string
	ClientSecret string
	AuthorizeURL string
	TokenURL     string
	APIBaseURL   string
}

type Discord struct {
	OAuthProvider

	BotToken         string
	APIBaseURL       string // REST API（bot ジョブ用）
	ServerID         string
	ChannelID        string
	ContestantRoleID string
	AdminRoleID      string
	// 単一開催（P-3）: qualify / final の 2 系統ロールは 1 系統に統合
	SupportCommChannelRoles map[string]string // channel_id => role_id
}

// Timing は競技時刻の設定。未設定（nil）の扱いは Rails 実装と同じ。
type Timing struct {
	RegistrationOpen            *time.Time
	RegistrationClose           *time.Time
	RegistrationInvitationClose *time.Time
	RegistrationUpdateClose     *time.Time
	ContestStart                *time.Time
	ContestFreeze               *time.Time
	ContestEnd                  *time.Time
}

func Load() (*Config, error) {
	c := &Config{
		Listen:        getenv("PORTAL_LISTEN", ":8081"),
		MetricsListen: os.Getenv("PORTAL_METRICS_LISTEN"),
		DatabaseURL:   getenv("ISUXPORTAL_DATABASE_URL", "postgres://isuxportal:isuxportal@127.0.0.1:15432/isuxportal?sslmode=disable"),
		RedisURL:      getenv("REDIS_URL", "redis://127.0.0.1:16379/0"),
		SessionSecret: getenv("ISUXPORTAL_SESSION_SECRET", "insecure-development-secret"),
		PublicURLHost: getenv("DEFAULT_URL_HOST", "localhost:8081"),
		Revision:      readRevision(),

		AdminLogin:    os.Getenv("ISUXPORTAL_ADMIN_LOGIN"),
		AdminPassword: os.Getenv("ISUXPORTAL_ADMIN_PASSWORD"),
		AdminOnly:     os.Getenv("ISUXPORTAL_ADMIN_ONLY") == "1",

		BenchToken:      getenv("ISUXPORTAL_BENCH_TOKEN", "token"),
		SSHKeyAPISecret: getenv("ISUXPORTAL_SSH_KEY_API_SECRET", "himitsudayo"),

		SlackWebhookURL: os.Getenv("ISUXPORTAL_SLACK_WEBHOOK_URL"),

		VAPIDPrivateKeyJSON: os.Getenv("ISUXPORTAL_VAPID_PRIVATE_KEY"),
		VAPIDSubject:        getenv("ISUXPORTAL_VAPID_SUBJECT", "isucon10@googlegroups.com"),

		AWSRegion:        getenv("AWS_REGION", "ap-northeast-1"),
		AvatarBucketName: os.Getenv("ISUXPORTAL_AVATAR_BUCKET"),
		AvatarBasePath:   getenv("ISUXPORTAL_AVATAR_BASE_PATH", "avatars/"),

		TestAMIID:             os.Getenv("ISUXPORTAL_TEST_AMI_ID"),
		ContestAMIID:          getenv("ISUXPORTAL_QUALIFY_AMI_ID", "ami-y"),
		CloudFormationStaging: os.Getenv("ISUXPORTAL_CLOUDFORMATION_STAGING") == "1",

		IsuconAdminSSHKeyPath: os.Getenv("ISUXPORTAL_ISUCON_ADMIN_SSH_PRIVATE_KEY_PATH"),

		TermsURL: getenv("ISUXPORTAL_TERMS_URL", "https://isucon.net/archives/58657108.html"),
		RulesURL: getenv("ISUXPORTAL_RULES_URL", "https://isucon.net/archives/58657116.html"),
		DocsURL:  getenv("ISUXPORTAL_DOCS_URL", "https://gist.github.com/progfay/25edb2a9ede4ca478cb3e2422f1f12f6"),

		AvailabilityZones: []string{"apne1-az1", "apne1-az2", "apne1-az4"},

		SentryDSN:         os.Getenv("SENTRY_DSN"),
		SentryEnvironment: getenv("MONITOR_ENV", "development"),

		PacksManifest: os.Getenv("ISUXPORTAL_PACKS_MANIFEST"),
		PublicDir:     os.Getenv("ISUXPORTAL_PUBLIC_DIR"),

		TestMode: os.Getenv("PORTAL_TEST_MODE") == "1",
	}

	c.GitHub = OAuthProvider{
		ClientID:     os.Getenv("ISUXPORTAL_GITHUB_CLIENT_ID"),
		ClientSecret: os.Getenv("ISUXPORTAL_GITHUB_CLIENT_SECRET"),
		AuthorizeURL: getenv("ISUXPORTAL_GITHUB_AUTHORIZE_URL", "https://github.com/login/oauth/authorize"),
		TokenURL:     getenv("ISUXPORTAL_GITHUB_TOKEN_URL", "https://github.com/login/oauth/access_token"),
		APIBaseURL:   getenv("ISUXPORTAL_GITHUB_API_BASE_URL", "https://api.github.com"),
	}
	c.Discord = Discord{
		OAuthProvider: OAuthProvider{
			ClientID:     os.Getenv("ISUXPORTAL_DISCORD_CLIENT_ID"),
			ClientSecret: os.Getenv("ISUXPORTAL_DISCORD_CLIENT_SECRET"),
			AuthorizeURL: getenv("ISUXPORTAL_DISCORD_AUTHORIZE_URL", "https://discord.com/api/oauth2/authorize"),
			TokenURL:     getenv("ISUXPORTAL_DISCORD_TOKEN_URL", "https://discord.com/api/oauth2/token"),
			APIBaseURL:   getenv("ISUXPORTAL_DISCORD_API_BASE_URL", "https://discord.com/api/v10"),
		},
		BotToken:                os.Getenv("ISUXPORTAL_DISCORD_BOT_TOKEN"),
		APIBaseURL:              getenv("ISUXPORTAL_DISCORD_API_BASE_URL", "https://discord.com/api/v10"),
		ServerID:                getenv("ISUXPORTAL_DISCORD_SERVER_ID", "721578039779262486"),          //gitleaks:allow isucon14 の公開 snowflake
		ChannelID:               getenv("ISUXPORTAL_DISCORD_CHANNEL_ID", "729697803982602286"),         //gitleaks:allow isucon14 の公開 snowflake
		ContestantRoleID:        getenv("ISUXPORTAL_DISCORD_CONTESTANT_ROLE_ID", "721583193270255617"), //gitleaks:allow isucon14 の公開 snowflake
		AdminRoleID:             getenv("ISUXPORTAL_DISCORD_ADMIN_ROLE_ID", "721583068724592640"),      //gitleaks:allow isucon14 の公開 snowflake
		SupportCommChannelRoles: parsePairs(os.Getenv("ISUXPORTAL_DISCORD_SUPPORT_COMM_CHANNEL_ROLES")),
	}

	maxTeams, err := strconv.Atoi(getenv("ISUXPORTAL_MAX_TEAMS", "600"))
	if err != nil {
		return nil, fmt.Errorf("ISUXPORTAL_MAX_TEAMS: %w", err)
	}
	c.MaxTeams = maxTeams

	for _, secret := range []struct {
		key      string
		fallback string
		dst      *[]byte
	}{
		{"ISUXPORTAL_BYPASS_SECRET", "5AQQyz/7oFnJYrxnq/HiLKYmh7QrdIrpCDpKlfvkYAg=", &c.BypassTokenSecret}, //gitleaks:allow isucon14 由来のダミー値
		{"ISUXPORTAL_CHECKER_SECRET", "2RHAlLoifERG1EEc/5dwyzcZGitfUn1k9YkHTwk7HiRO", &c.CheckerSecret},    //gitleaks:allow isucon14 由来のダミー値
	} {
		raw, err := base64.StdEncoding.WithPadding(base64.StdPadding).DecodeString(padBase64(getenv(secret.key, secret.fallback)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", secret.key, err)
		}
		*secret.dst = raw
	}

	for _, e := range []struct {
		key string
		dst **time.Time
	}{
		{"ISUXPORTAL_TIMING_REGISTRATION_OPEN", &c.Timing.RegistrationOpen},
		{"ISUXPORTAL_TIMING_REGISTRATION_CLOSE", &c.Timing.RegistrationClose},
		{"ISUXPORTAL_TIMING_REGISTRATION_INVITATION_CLOSE", &c.Timing.RegistrationInvitationClose},
		{"ISUXPORTAL_TIMING_REGISTRATION_UPDATE_CLOSE", &c.Timing.RegistrationUpdateClose},
		{"ISUXPORTAL_TIMING_CONTEST_START", &c.Timing.ContestStart},
		{"ISUXPORTAL_TIMING_CONTEST_FREEZE", &c.Timing.ContestFreeze},
		{"ISUXPORTAL_TIMING_CONTEST_END", &c.Timing.ContestEnd},
	} {
		v := os.Getenv(e.key)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.key, err)
		}
		*e.dst = &t
	}

	return c, nil
}

// VAPIDPrivateKeyPEM は ISUXPORTAL_VAPID_PRIVATE_KEY（JSON 文字列化した PEM、CT-10）を PEM に戻す。
func (c *Config) VAPIDPrivateKeyPEM() (string, error) {
	if c.VAPIDPrivateKeyJSON == "" {
		return "", nil
	}
	var pem string
	if err := json.Unmarshal([]byte(c.VAPIDPrivateKeyJSON), &pem); err != nil {
		return "", fmt.Errorf("ISUXPORTAL_VAPID_PRIVATE_KEY: %w", err)
	}
	return pem, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func readRevision() string {
	if v := os.Getenv("ISUXPORTAL_REVISION"); v != "" {
		return v
	}
	b, err := os.ReadFile("REVISION")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

// parsePairs は "channel=role,channel=role" 形式を map にする。
func parsePairs(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	for _, pair := range strings.Split(s, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = kv[1]
		}
	}
	return out
}

func padBase64(s string) string {
	if m := len(s) % 4; m != 0 {
		return s + strings.Repeat("=", 4-m)
	}
	return s
}
