package domain

import (
	"errors"
	"time"
)

var ErrAccountLifecycle = errors.New("invalid account lifecycle transition")

const AccountDeletionGrace = 30 * 24 * time.Hour

type AccountLifecycle struct {
	Status                          string
	DeletionRequestedAt, PurgeDueAt *time.Time
}

func ActiveAccount() AccountLifecycle { return AccountLifecycle{Status: "active"} }
func (a AccountLifecycle) RequestDeletion(now time.Time) (AccountLifecycle, error) {
	if a.Status != "active" {
		return a, ErrAccountLifecycle
	}
	due := now.Add(AccountDeletionGrace)
	return AccountLifecycle{Status: "deletion_pending", DeletionRequestedAt: &now, PurgeDueAt: &due}, nil
}
func (a AccountLifecycle) CanRestore(now time.Time) bool {
	return a.Status == "deletion_pending" && a.PurgeDueAt != nil && now.Before(*a.PurgeDueAt)
}
func (a AccountLifecycle) PurgeDue(now time.Time) bool {
	return a.Status == "deletion_pending" && a.PurgeDueAt != nil && !now.Before(*a.PurgeDueAt)
}

type ProfileUpdate struct {
	DisplayName, Bio *string
	Region           *string
}

func (p ProfileUpdate) Validate() error {
	if p.DisplayName != nil && ValidateDisplayName(*p.DisplayName) != nil {
		return ErrInvalidDisplayName
	}
	if p.Bio != nil && ValidateBio(*p.Bio) != nil {
		return ErrInvalidBio
	}
	if p.Region != nil && ValidateRegion(*p.Region) != nil {
		return ErrInvalidRegion
	}
	if p.DisplayName == nil && p.Bio == nil && p.Region == nil {
		return errors.New("empty profile update")
	}
	return nil
}
