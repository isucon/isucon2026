package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"time"
)

type NotificationStore interface {
	List(context.Context, uuid.UUID, *time.Time, *uuid.UUID, int) ([]domain.Notification, error)
	Open(context.Context, uuid.UUID, uuid.UUID) (domain.NotificationDestination, string, error)
}
type NotificationService struct{ repository NotificationStore }

func NewNotificationService(repository NotificationStore) *NotificationService {
	return &NotificationService{repository: repository}
}
func (s *NotificationService) List(ctx context.Context, user uuid.UUID, at *time.Time, id *uuid.UUID, limit int) ([]domain.Notification, error) {
	return s.repository.List(ctx, user, at, id, limit)
}
func (s *NotificationService) Open(ctx context.Context, user, id uuid.UUID) (domain.NotificationDestination, string, error) {
	return s.repository.Open(ctx, user, id)
}
