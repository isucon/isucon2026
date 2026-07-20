package domain_test

import (
	"github.com/isucon/isucon2026/backend/internal/domain"
	"testing"
	"time"
)

func TestUS5ScheduleAndDestination(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	schedule, err := domain.NewNotificationSchedule(domain.RegionAsia, now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.LocalDate.Format("2006-01-02") != "2026-07-19" {
		t.Fatal("fixed offset local date")
	}
	if _, err := domain.NewNotificationSchedule(domain.RegionAsia, now, now); err == nil {
		t.Fatal("past schedule accepted")
	}
	if domain.Destination(true, false) != domain.DestinationCreatePost || domain.Destination(true, true) != domain.DestinationCurrentPost || domain.Destination(false, false) != domain.DestinationMemory {
		t.Fatal("destination transition")
	}
	if domain.NextPushState(1, true, false) != domain.PushRetryable || domain.NextPushState(4, true, false) != domain.PushFailed || domain.NextPushState(1, false, true) != domain.PushPermanentFailure {
		t.Fatal("push retry transition")
	}
}
