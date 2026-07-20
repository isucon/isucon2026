package domain_test

import (
	"github.com/isucon/isucon2026/backend/internal/domain"
	"testing"
	"time"
)

func TestAccountDeletionLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	pending, err := domain.ActiveAccount().RequestDeletion(now)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.CanRestore(now.Add(29 * 24 * time.Hour)) {
		t.Fatal("restore denied before deadline")
	}
	if pending.CanRestore(now.Add(30*24*time.Hour)) || !pending.PurgeDue(now.Add(30*24*time.Hour)) {
		t.Fatal("deadline priority broken")
	}
	if _, err := pending.RequestDeletion(now); err == nil {
		t.Fatal("duplicate deletion accepted")
	}
}
