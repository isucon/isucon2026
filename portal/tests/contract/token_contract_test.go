// Package contract_test は外部と共有する契約（CT-*）を DB なしで検証する。
// backend/tests/contract と同じく portal-unit-contract-build ジョブで動く。
package contract_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	pbmisc "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc"
	"github.com/isucon/isucon2026/portal/internal/token"
)

func dummySecret(t *testing.T, s string) []byte {
	t.Helper()
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func bypassSecret(t *testing.T) []byte {
	return dummySecret(t, "5AQQyz/7oFnJYrxnq/HiLKYmh7QrdIrpCDpKlfvkYAg=") //gitleaks:allow isucon14 由来のダミー値
}

func checkerSecret(t *testing.T) []byte {
	return dummySecret(t, "2RHAlLoifERG1EEc/5dwyzcZGitfUn1k9YkHTwk7HiRO") //gitleaks:allow isucon14 由来のダミー値
}

// FR-AUTH-7: bypass token は base64url(HMAC-SHA384):base64url(pb)、パディングなし。
func TestBypassTokenFormatContract(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	tok, err := token.CreateBypass(bypassSecret(t), &pbmisc.BypassTokenPayload{
		Expiry: now.Add(time.Hour).Unix(),
		Usages: []pbmisc.BypassTokenPayload_Usage{pbmisc.BypassTokenPayload_CREATE_TEAM},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Errorf("token must be URL-safe without padding: %q", tok)
	}
	parts := strings.SplitN(tok, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("token must be <sig>:<payload>: %q", tok)
	}
	// HMAC-SHA384 = 48 バイト → パディングなし base64 で 64 文字
	if len(parts[0]) != 64 {
		t.Errorf("signature part must encode 48 bytes (HMAC-SHA384), got %d chars", len(parts[0]))
	}
	if token.VerifyBypass(bypassSecret(t), tok, now) == nil {
		t.Error("round-trip verification failed")
	}
}

// rubyGoldenBypassToken は isucon14-portal 互換の Ruby 実装
// （rspec/spec/support/portal_client.rb の create_bypass_token）で生成した固定トークン。
// filler="Zml4ZWQ=", expiry=4102444800 (2100-01-01), usages=[CREATE_TEAM, LEAVE_TEAM]。
// Ruby → Go の言語間互換を固定ベクタで守る。
const rubyGoldenBypassToken = "X7fJXPoMt51uPPTiKfiQJbNHhxg2UKGvkZA7V2wv8X7WBqQjklp0jHUzmcxsX_kK:CghabWw0WldRPRCArpmkDxoCAAM" //gitleaks:allow ダミー secret による固定テストベクタ

func TestBypassTokenAcceptsRubyGeneratedToken(t *testing.T) {
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	payload := token.VerifyBypass(bypassSecret(t), rubyGoldenBypassToken, now)
	if payload == nil {
		t.Fatal("Ruby-generated token must verify")
	}
	if payload.Expiry != 4102444800 {
		t.Errorf("Expiry = %d, want 4102444800", payload.Expiry)
	}
	if len(payload.Usages) != 2 ||
		payload.Usages[0] != pbmisc.BypassTokenPayload_CREATE_TEAM ||
		payload.Usages[1] != pbmisc.BypassTokenPayload_LEAVE_TEAM {
		t.Errorf("Usages = %v, want [CREATE_TEAM LEAVE_TEAM]", payload.Usages)
	}
}

// FR-E-4: checker token は JSON ペイロード + HMAC-SHA384。
// 期限切れは Rails の感想戦 FIXME を踏襲して許容される。
func TestCheckerTokenContract(t *testing.T) {
	tok, err := token.CreateChecker(checkerSecret(t), token.CheckerPayload{TeamID: 7, Expiry: 1})
	if err != nil {
		t.Fatal(err)
	}
	payload := token.VerifyChecker(checkerSecret(t), tok)
	if payload == nil {
		t.Fatal("expired checker token must still verify (感想戦対応)")
	}
	if payload.TeamID != 7 {
		t.Errorf("TeamID = %d, want 7", payload.TeamID)
	}
	if token.VerifyChecker(bypassSecret(t), tok) != nil {
		t.Error("checker token must be rejected with a different secret")
	}
}
