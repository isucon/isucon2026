package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"time"
)

type MemoryStore interface {
	ListMonth(context.Context, uuid.UUID, string, *time.Time, *uuid.UUID, int) ([]domain.MemoryRecord, error)
	ListDate(context.Context, uuid.UUID, string, *time.Time, *uuid.UUID, int) ([]domain.MemoryRecord, error)
}
type MemoryService struct{ repository MemoryStore }

func NewMemoryService(repository MemoryStore) *MemoryService {
	return &MemoryService{repository: repository}
}
func (s *MemoryService) Month(ctx context.Context, owner uuid.UUID, month string, at *time.Time, id *uuid.UUID, limit int) ([]domain.MemoryRecord, error) {
	if err := domain.ValidateMemoryMonth(month); err != nil {
		return nil, err
	}
	return s.repository.ListMonth(ctx, owner, month, at, id, limit)
}
func (s *MemoryService) Date(ctx context.Context, owner uuid.UUID, date string, at *time.Time, id *uuid.UUID, limit int) ([]domain.MemoryRecord, error) {
	if _, err := domain.ValidateMemoryDate(date); err != nil {
		return nil, err
	}
	return s.repository.ListDate(ctx, owner, date, at, id, limit)
}
