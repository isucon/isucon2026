package model

import "testing"

func TestTeamOnsiteSupportExcluded(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		team := &Team{OnsiteSupportExcluded: excluded}
		if !team.Active() {
			t.Fatal("support exclusion must not prevent participation")
		}
		if got := team.ToPB(true, nil, false).GetOnsiteSupportExcluded(); got != excluded {
			t.Fatalf("support exclusion = %v, want %v", got, excluded)
		}
		team.Disqualified = true
		if team.Active() {
			t.Fatal("disqualification must still prevent participation")
		}
	}
}
