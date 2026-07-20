package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
)

var (
	ErrUsernameTaken   = errors.New("username is already in use")
	ErrInvalidSession  = errors.New("invalid session")
	ErrInactiveAccount = errors.New("account is not active")
)

type RegistrationInput struct{ Username, Password, DisplayName, Region string }
type LoginInput struct{ Username, Password string }
type SessionResult struct {
	Token     string
	SessionID uuid.UUID
	User      domain.User
}

type RegistrationRecord struct {
	Username, UsernameNormalized, PasswordHash, DisplayName, DisplayNameSearch, Region string
	TokenHash                                                                          string
}

type SessionRepository interface {
	CreateUserAndSession(context.Context, RegistrationRecord) (domain.User, uuid.UUID, error)
	CredentialByUsername(context.Context, string) (domain.User, string, bool, error)
	ReplaceSession(context.Context, uuid.UUID, string) (uuid.UUID, error)
	LogoutSession(context.Context, string) error
}

type SessionService struct{ repository SessionRepository }

func NewSessionService(repository SessionRepository) *SessionService {
	return &SessionService{repository: repository}
}

func (s *SessionService) Register(ctx context.Context, input RegistrationInput) (SessionResult, error) {
	normalized, err := domain.NormalizeUsername(input.Username)
	if err != nil {
		return SessionResult{}, err
	}
	if err := domain.ValidatePassword(input.Password); err != nil {
		return SessionResult{}, err
	}
	if err := domain.ValidateDisplayName(input.DisplayName); err != nil {
		return SessionResult{}, err
	}
	if err := domain.ValidateRegion(input.Region); err != nil {
		return SessionResult{}, err
	}
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return SessionResult{}, err
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return SessionResult{}, err
	}
	user, sessionID, err := s.repository.CreateUserAndSession(ctx, RegistrationRecord{
		Username: input.Username, UsernameNormalized: normalized, PasswordHash: passwordHash,
		DisplayName: input.DisplayName, DisplayNameSearch: domain.NormalizeSearchText(input.DisplayName), Region: input.Region, TokenHash: tokenHash,
	})
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Token: token, SessionID: sessionID, User: user}, nil
}

func (s *SessionService) Login(ctx context.Context, input LoginInput) (SessionResult, error) {
	normalized, normalizeErr := domain.NormalizeUsername(input.Username)
	if normalizeErr != nil {
		_ = AuthenticatePassword("", input.Password)
		return SessionResult{}, ErrInvalidCredentials
	}
	user, passwordHash, exists, err := s.repository.CredentialByUsername(ctx, normalized)
	if err != nil {
		return SessionResult{}, err
	}
	if !exists {
		passwordHash = ""
	}
	if err := AuthenticatePassword(passwordHash, input.Password); err != nil || !exists {
		return SessionResult{}, ErrInvalidCredentials
	}
	if user.Status != "active" && user.Status != "deletion_pending" {
		return SessionResult{}, ErrInactiveAccount
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return SessionResult{}, err
	}
	sessionID, err := s.repository.ReplaceSession(ctx, user.ID, tokenHash)
	if err != nil {
		return SessionResult{}, err
	}
	user.Status = "active"
	return SessionResult{Token: token, SessionID: sessionID, User: user}, nil
}

func (s *SessionService) Logout(ctx context.Context, token string) error {
	if token == "" {
		return ErrInvalidSession
	}
	return s.repository.LogoutSession(ctx, HashSessionToken(token))
}

func HashSessionToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func newSessionToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, HashSessionToken(token), nil
}
