package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
)

type AdminAuthRepository interface {
	AdministratorCredential(context.Context, string) (domain.Administrator, string, bool, error)
	ReplaceAdministratorSession(context.Context, uuid.UUID, string) (uuid.UUID, error)
}
type UserCertifier interface {
	Certify(context.Context, uuid.UUID) error
}
type AdminService struct {
	repository AdminAuthRepository
	certifier  UserCertifier
}
type AdminSessionResult struct {
	Token                      string
	SessionID, AdministratorID uuid.UUID
}

func NewAdminService(repository AdminAuthRepository, certifier UserCertifier) *AdminService {
	return &AdminService{repository: repository, certifier: certifier}
}
func (s *AdminService) Login(ctx context.Context, login, password string) (AdminSessionResult, error) {
	normalized, err := domain.NormalizeUsername(login)
	if err != nil {
		return AdminSessionResult{}, ErrInvalidCredentials
	}
	administrator, hash, exists, err := s.repository.AdministratorCredential(ctx, normalized)
	if err != nil {
		return AdminSessionResult{}, err
	}
	if !exists || !administrator.Enabled || VerifyCredential(hash, password) != nil {
		return AdminSessionResult{}, ErrInvalidCredentials
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return AdminSessionResult{}, err
	}
	sessionID, err := s.repository.ReplaceAdministratorSession(ctx, administrator.ID, tokenHash)
	return AdminSessionResult{Token: token, SessionID: sessionID, AdministratorID: administrator.ID}, err
}
func (s *AdminService) Certify(ctx context.Context, userID uuid.UUID) error {
	return s.certifier.Certify(ctx, userID)
}
