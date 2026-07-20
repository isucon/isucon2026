package service_test

import (
	"errors"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/service"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordHashUsesBcryptCost12(t *testing.T) {
	t.Parallel()
	hash, err := service.HashPassword(" correct horse battery staple ")
	if err != nil {
		t.Fatal(err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatal(err)
	}
	if cost != 12 {
		t.Fatalf("bcrypt cost = %d, want 12", cost)
	}
	if err := service.VerifyPassword(hash, " correct horse battery staple "); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := service.VerifyPassword(hash, "correct horse battery staple"); !errors.Is(err, service.ErrInvalidCredentials) {
		t.Fatalf("trimmed password error = %v", err)
	}
}

func TestLoginFailuresAreGeneric(t *testing.T) {
	t.Parallel()
	missing := service.InvalidCredentialProblem()
	wrongPassword := service.InvalidCredentialProblem()
	if missing != wrongPassword {
		t.Fatalf("generic failures differ: %#v != %#v", missing, wrongPassword)
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
