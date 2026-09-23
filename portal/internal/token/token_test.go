package token

import (
	"strings"
	"testing"
	"time"

	pbmisc "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc"
)

var secret = []byte("test-secret")

func TestBypassTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	tok, err := CreateBypass(secret, &pbmisc.BypassTokenPayload{
		Expiry: now.Add(time.Hour).Unix(),
		Usages: []pbmisc.BypassTokenPayload_Usage{pbmisc.BypassTokenPayload_CREATE_TEAM, pbmisc.BypassTokenPayload_JOIN_TEAM},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Errorf("token must be URL-safe without padding: %q", tok)
	}

	payload := VerifyBypass(secret, tok, now)
	if payload == nil {
		t.Fatal("VerifyBypass returned nil for a valid token")
	}
	if !BypassAllows(payload, pbmisc.BypassTokenPayload_CREATE_TEAM) {
		t.Error("CREATE_TEAM should be allowed")
	}
	if BypassAllows(payload, pbmisc.BypassTokenPayload_HIDDEN_TEAM) {
		t.Error("HIDDEN_TEAM should not be allowed")
	}
}

func TestBypassTokenRejections(t *testing.T) {
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	tok, err := CreateBypass(secret, &pbmisc.BypassTokenPayload{Expiry: now.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}

	if VerifyBypass(secret, tok, now.Add(2*time.Hour)) != nil {
		t.Error("expired token must be rejected")
	}
	if VerifyBypass([]byte("other-secret"), tok, now) != nil {
		t.Error("token signed with another secret must be rejected")
	}
	if VerifyBypass(secret, "garbage", now) != nil {
		t.Error("malformed token must be rejected")
	}
	if VerifyBypass(secret, strings.Replace(tok, ":", ";", 1), now) != nil {
		t.Error("token without separator must be rejected")
	}
}

func TestCheckerTokenRoundTrip(t *testing.T) {
	tok, err := CreateChecker(secret, CheckerPayload{TeamID: 42, Expiry: 12345})
	if err != nil {
		t.Fatal(err)
	}
	payload := VerifyChecker(secret, tok)
	if payload == nil {
		t.Fatal("VerifyChecker returned nil for a valid token")
	}
	if payload.TeamID != 42 {
		t.Errorf("TeamID = %d, want 42", payload.TeamID)
	}

	if VerifyChecker([]byte("other-secret"), tok) != nil {
		t.Error("token signed with another secret must be rejected")
	}
	if VerifyChecker(secret, "garbage") != nil {
		t.Error("malformed token must be rejected")
	}
}
