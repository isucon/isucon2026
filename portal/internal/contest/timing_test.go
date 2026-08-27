package contest

import (
	"testing"
	"time"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/config"
)

func timePtr(t time.Time) *time.Time { return &t }

func TestContestTimings(t *testing.T) {
	base := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	timing := config.Timing{
		ContestStart:  timePtr(base),
		ContestFreeze: timePtr(base.Add(7 * time.Hour)),
		ContestEnd:    timePtr(base.Add(8 * time.Hour)),
	}

	cases := []struct {
		name    string
		now     time.Time
		running bool
		frozen  bool
		ended   bool
	}{
		{"before start", base.Add(-time.Second), false, false, false},
		{"at start", base, true, false, false},
		{"before freeze", base.Add(7*time.Hour - time.Second), true, false, false},
		{"at freeze", base.Add(7 * time.Hour), true, true, false},
		{"just before end", base.Add(8*time.Hour - time.Second), true, true, false},
		{"at end", base.Add(8 * time.Hour), false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContestRunning(timing, tc.now); got != tc.running {
				t.Errorf("ContestRunning = %v, want %v", got, tc.running)
			}
			if got := ContestFrozen(timing, tc.now); got != tc.frozen {
				t.Errorf("ContestFrozen = %v, want %v", got, tc.frozen)
			}
			if got := ContestEnded(timing, tc.now); got != tc.ended {
				t.Errorf("ContestEnded = %v, want %v", got, tc.ended)
			}
		})
	}
}

func TestContestTimingsUnset(t *testing.T) {
	// 未設定時は Rails と同じく素通し（running=true, frozen/ended=false）
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	var timing config.Timing
	if !ContestRunning(timing, now) {
		t.Error("ContestRunning should be true when timing is unset")
	}
	if ContestFrozen(timing, now) {
		t.Error("ContestFrozen should be false when timing is unset")
	}
	if ContestEnded(timing, now) {
		t.Error("ContestEnded should be false when timing is unset")
	}
	if !RegistrationOpen(timing, now) {
		t.Error("RegistrationOpen should be true when timing is unset")
	}
}

func TestContestPBStatus(t *testing.T) {
	base := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	timing := config.Timing{
		RegistrationOpen:  timePtr(base.Add(-100 * time.Hour)),
		RegistrationClose: timePtr(base.Add(-50 * time.Hour)),
		ContestStart:      timePtr(base),
		ContestFreeze:     timePtr(base.Add(7 * time.Hour)),
		ContestEnd:        timePtr(base.Add(8 * time.Hour)),
	}

	cases := []struct {
		name   string
		now    time.Time
		status pbresources.Contest_Status
		frozen bool
	}{
		{"registration", base.Add(-60 * time.Hour), pbresources.Contest_REGISTRATION, false},
		{"standby", base.Add(-time.Hour), pbresources.Contest_STANDBY, false},
		{"started", base.Add(time.Hour), pbresources.Contest_STARTED, false},
		{"frozen but running", base.Add(7*time.Hour + time.Minute), pbresources.Contest_STARTED, true},
		{"finished", base.Add(9 * time.Hour), pbresources.Contest_FINISHED, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ContestPB(timing, tc.now)
			if got.Status != tc.status {
				t.Errorf("Status = %v, want %v", got.Status, tc.status)
			}
			if got.Frozen != tc.frozen {
				t.Errorf("Frozen = %v, want %v", got.Frozen, tc.frozen)
			}
		})
	}
}
