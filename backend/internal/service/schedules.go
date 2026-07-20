package service

import (
	"context"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"time"
)

type ScheduleStore interface {
	SetSchedule(context.Context, domain.NotificationSchedule) error
	CancelSchedule(context.Context, domain.Region) error
}
type ScheduleService struct {
	repository ScheduleStore
	clock      interface{ Now() time.Time }
}

func NewScheduleService(repository ScheduleStore, clock interface{ Now() time.Time }) *ScheduleService {
	return &ScheduleService{repository: repository, clock: clock}
}
func (s *ScheduleService) Set(ctx context.Context, region domain.Region, at time.Time) error {
	schedule, err := domain.NewNotificationSchedule(region, at, s.clock.Now())
	if err != nil {
		return err
	}
	return s.repository.SetSchedule(ctx, schedule)
}
func (s *ScheduleService) Cancel(ctx context.Context, region domain.Region) error {
	if _, err := domain.NewNotificationSchedule(region, s.clock.Now().Add(time.Second), s.clock.Now()); err != nil {
		return err
	}
	return s.repository.CancelSchedule(ctx, region)
}
