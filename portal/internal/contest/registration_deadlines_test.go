package contest

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/isucon/isucon2026/portal/internal/config"
)

func TestContestPBRegistrationDeadlines(t *testing.T) {
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	timing := config.Timing{
		RegistrationClose:           timePtr(now),
		RegistrationInvitationClose: timePtr(now.Add(time.Hour)),
		RegistrationUpdateClose:     timePtr(now.Add(2 * time.Hour)),
	}
	c := ContestPB(timing, now)
	empty := ContestPB(config.Timing{}, now)
	cases := []struct {
		name       string
		got, unset *timestamppb.Timestamp
		want       *time.Time
	}{
		{"registration", c.RegistrationClosesAt, empty.RegistrationClosesAt, timing.RegistrationClose},
		{"invitation", c.RegistrationInvitationClosesAt, empty.RegistrationInvitationClosesAt, timing.RegistrationInvitationClose},
		{"update", c.RegistrationUpdateClosesAt, empty.RegistrationUpdateClosesAt, timing.RegistrationUpdateClose},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == nil || !tc.got.AsTime().Equal(*tc.want) {
				t.Errorf("deadline = %v, want %v", tc.got, tc.want)
			}
			if tc.unset != nil {
				t.Errorf("unset deadline must stay absent: %v", tc.unset)
			}
		})
	}
}
