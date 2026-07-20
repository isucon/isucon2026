package service

import "io"
import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"time"
)

type ProfileStore interface {
	Get(context.Context, uuid.UUID) (domain.User, *string, error)
	Update(context.Context, uuid.UUID, domain.ProfileUpdate) (domain.User, *string, error)
	SetImage(context.Context, uuid.UUID, isurealimage.Object) (string, error)
	DeleteImage(context.Context, uuid.UUID) (string, error)
	ProfileImage(context.Context, uuid.UUID, uuid.UUID) (string, error)
}
type AccountStore interface {
	RequestDeletion(context.Context, uuid.UUID) (time.Time, error)
	DueAccounts(context.Context, int) ([]uuid.UUID, error)
	Purge(context.Context, uuid.UUID) ([]string, error)
}
type AccountService struct {
	profiles ProfileStore
	accounts AccountStore
	images   PostImageStore
}

func NewAccountService(profiles ProfileStore, accounts AccountStore, images PostImageStore) *AccountService {
	return &AccountService{profiles: profiles, accounts: accounts, images: images}
}
func (s *AccountService) Get(ctx context.Context, user uuid.UUID) (domain.User, *string, error) {
	return s.profiles.Get(ctx, user)
}
func (s *AccountService) Update(ctx context.Context, user uuid.UUID, input domain.ProfileUpdate) (domain.User, *string, error) {
	if err := input.Validate(); err != nil {
		return domain.User{}, nil, err
	}
	return s.profiles.Update(ctx, user, input)
}
func (s *AccountService) SetProfileImage(ctx context.Context, user uuid.UUID, input io.Reader) error {
	object, err := s.images.Save(ctx, user, isurealimage.ProfileImage, input)
	if err != nil {
		return err
	}
	old, err := s.profiles.SetImage(ctx, user, object)
	if err != nil {
		_ = s.images.Remove(object.ObjectKey)
		return err
	}
	if old != "" {
		_ = s.images.Remove(old)
	}
	return nil
}
func (s *AccountService) DeleteProfileImage(ctx context.Context, user uuid.UUID) error {
	key, err := s.profiles.DeleteImage(ctx, user)
	if err != nil {
		return err
	}
	return s.images.Remove(key)
}
func (s *AccountService) RequestDeletion(ctx context.Context, user uuid.UUID) (time.Time, error) {
	return s.accounts.RequestDeletion(ctx, user)
}
func (s *AccountService) PurgeDue(ctx context.Context, limit int) error {
	ids, err := s.accounts.DueAccounts(ctx, limit)
	if err != nil {
		return err
	}
	for _, id := range ids {
		keys, err := s.accounts.Purge(ctx, id)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := s.images.Remove(key); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *AccountService) ResolveAuthorizedImage(ctx context.Context, viewer domain.User, imageID uuid.UUID) (string, error) {
	return s.profiles.ProfileImage(ctx, viewer.ID, imageID)
}
