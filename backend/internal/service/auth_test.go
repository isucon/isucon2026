package service_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/service"
)

func TestCredentialHashIsSHA256(t *testing.T) {
	t.Parallel()
	value := " correct horse battery staple "
	hash := service.HashCredential(value)
	digest := sha256.Sum256([]byte(value))
	if hash != hex.EncodeToString(digest[:]) {
		t.Fatalf("hash = %s", hash)
	}
	if err := service.VerifyCredential(hash, value); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := service.VerifyCredential(hash, "correct horse battery staple"); !errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatalf("different credential error = %v", err)
	}
}

func TestLoginFailuresAreGeneric(t *testing.T) {
	t.Parallel()
	missing := service.InvalidCredentialProblem()
	wrongSecret := service.InvalidCredentialProblem()
	if missing != wrongSecret {
		t.Fatalf("generic failures differ: %#v != %#v", missing, wrongSecret)
	}
	if missing.Code != "invalid_credentials" || missing.Message == "" {
		t.Fatalf("unexpected problem: %#v", missing)
	}
}

func TestUnauthenticatedIdempotencyCallerScopes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind                        service.AuthOperation
		username, wantType, wantKey string
	}{
		{service.AuthRegistration, "Alice", "registration", "alice"},
		{service.AuthUserLogin, "Alice", "user_login", "alice"},
		{service.AuthAdminLogin, "Root.Admin", "admin_login", "root.admin"},
	}
	for _, test := range cases {
		scope, err := service.UnauthenticatedCallerScope(test.kind, test.username)
		if err != nil {
			t.Fatal(err)
		}
		if scope.Type != test.wantType || scope.Key != test.wantKey {
			t.Fatalf("scope = %#v", scope)
		}
	}
}
