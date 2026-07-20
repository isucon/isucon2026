package service

import (
	"errors"

	"github.com/isucon/isucon2026/backend/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

const BcryptCost = 12

var ErrInvalidCredentials = errors.New("invalid credentials")

// dummyPasswordHash ensures an unknown username follows the same expensive
// bcrypt path as a wrong password. It hashes a fixed, non-secret test value.
var dummyPasswordHash = mustHashDummyPassword()

type CredentialProblem struct{ Code, Message string }

func InvalidCredentialProblem() CredentialProblem {
	return CredentialProblem{Code: "invalid_credentials", Message: "The username or password is incorrect."}
}

func HashPassword(password string) (string, error) {
	if err := domain.ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// AuthenticatePassword accepts an empty hash for a missing account, while
// deliberately returning the same external error as a password mismatch.
func AuthenticatePassword(hash, password string) error {
	if hash == "" {
		hash = dummyPasswordHash
	}
	return VerifyPassword(hash, password)
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

func mustHashDummyPassword() string {
	hash, err := bcrypt.GenerateFromPassword([]byte("not-a-user-password"), BcryptCost)
	if err != nil {
		panic(err)
	}
	return string(hash)
}

func NormalizeDisplayNameForSearch(value string) string { return domain.NormalizeSearchText(value) }
