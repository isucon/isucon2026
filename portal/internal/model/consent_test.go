package model

import (
	"testing"
	"time"
)

func TestSponsorConsentTransitions(t *testing.T) {
	first := time.Date(2026, 9, 1, 0, 0, 0, 123000, time.UTC)
	next := first.Add(time.Hour)
	c := Consent{}
	c.SetSponsor(false, first)
	if c.SponsorWithdrawnAt != nil {
		t.Fatal("unknown is not a withdrawal")
	}
	c.SetSponsor(true, first)
	c.SetSponsor(true, next)
	if c.SponsorAgreedAt == nil || !c.SponsorAgreedAt.Equal(first) {
		t.Fatal("repeat agreement changed timestamp")
	}
	c.SetSponsor(false, next)
	if c.SponsorAgreedAt != nil || c.SponsorWithdrawnAt == nil || !c.SponsorWithdrawnAt.Equal(next) {
		t.Fatal("withdrawal not recorded")
	}
	c.SetSponsor(true, next.Add(time.Hour))
	if c.SponsorWithdrawnAt != nil || !c.SponsorAgreedAt.Equal(next.Add(time.Hour)) {
		t.Fatal("reagreement not recorded")
	}
}
