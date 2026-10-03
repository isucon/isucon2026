package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/isucon/isucon2026/backend/internal/domain"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type CredentialProblem struct{ Code, Message string }

func InvalidCredentialProblem() CredentialProblem {
	return CredentialProblem{Code: "invalid_credentials", Message: "The username or credentials are incorrect."}
}

func HashCredential(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func VerifyCredential(hash, value string) error {
	if hash != HashCredential(value) {
		return ErrInvalidCredentials
	}
	return nil
}

type AuthOperation string

const (
	AuthRegistration AuthOperation = "registration"
	AuthUserLogin    AuthOperation = "user_login"
	AuthAdminLogin   AuthOperation = "admin_login"
)

type CallerScope struct{ Type, Key string }

func UnauthenticatedCallerScope(operation AuthOperation, username string) (CallerScope, error) {
	normalized, err := domain.NormalizeUsername(username)
	if err != nil {
		return CallerScope{}, err
	}
	switch operation {
	case AuthRegistration, AuthUserLogin, AuthAdminLogin:
		return CallerScope{Type: string(operation), Key: normalized}, nil
	default:
		return CallerScope{}, errors.New("unsupported authentication operation")
	}
}

func NormalizeDisplayNameForSearch(value string) string { return domain.NormalizeSearchText(value) }
