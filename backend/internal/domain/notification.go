package domain

import (
	"errors"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"time"
)

var ErrSchedule = errors.New("invalid notification schedule")

type NotificationSchedule struct {
	Region      Region
	ScheduledAt time.Time
	LocalDate   time.Time
}

func NewNotificationSchedule(region Region, scheduledAt, now time.Time) (NotificationSchedule, error) {
	if !scheduledAt.After(now) {
		return NotificationSchedule{}, ErrSchedule
	}
	local, ok := clock.LocalDate(scheduledAt, clock.Region(region))
	if !ok {
		return NotificationSchedule{}, ErrSchedule
	}
	return NotificationSchedule{Region: region, ScheduledAt: scheduledAt.UTC(), LocalDate: local}, nil
}

type Notification struct {
	ID, UserID, PostingWindowID uuid.UUID
	Region                      Region
	CreatedAt                   time.Time
	ReadAt                      *time.Time
}
type NotificationDestination string

const (
	DestinationCreatePost  NotificationDestination = "create-post"
	DestinationCurrentPost NotificationDestination = "my-current-post"
	DestinationMemory      NotificationDestination = "memory"
)

func Destination(windowCurrent, posted bool) NotificationDestination {
	if !windowCurrent {
		return DestinationMemory
	}
	if posted {
		return DestinationCurrentPost
	}
	return DestinationCreatePost
}

type PushDeliveryState string

const (
	PushPending          PushDeliveryState = "pending"
	PushRetryable        PushDeliveryState = "retryable"
	PushDelivered        PushDeliveryState = "delivered"
	PushPermanentFailure PushDeliveryState = "permanent_failure"
	PushFailed           PushDeliveryState = "failed"
)

func NextPushState(attempt int, transient, permanent bool) PushDeliveryState {
	if permanent {
		return PushPermanentFailure
	}
	if !transient {
		return PushDelivered
	}
	if attempt >= 4 {
		return PushFailed
	}
	return PushRetryable
}

type PushAttempt struct {
	ID, NotificationID, SubscriptionID uuid.UUID
	Endpoint, P256DH, Auth             string
	Attempts                           int
}
