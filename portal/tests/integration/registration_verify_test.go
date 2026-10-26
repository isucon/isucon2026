package integration_test

import (
	"context"
	"github.com/isucon/isucon2026/portal/internal/registrationmigration"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestVerifyImportedRegistration(t *testing.T) {
	db := testkit.Database(t)
	s := registrationSnapshot()
	s.RegistrationCoupons = []registrationmigration.RegistrationCoupon{{ID: 1, Name: "invitation", Token: "synthetic", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), UpdatedAt: time.Now().UTC().Truncate(time.Microsecond)}}
	s.Other["authentication.RegisterCoupon"] = 1
	ctx := context.Background()
	if err := s.Import(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, db); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE registration_source_records SET payload='{}' WHERE model='authentication.RegisterCoupon'",
		"UPDATE contestants SET github_id='changed' WHERE id=10",
		"UPDATE contestants SET discord_id='changed' WHERE id=10",
		"UPDATE contestants SET avatar_url='changed'",
		"UPDATE teams SET leader_id=NULL",
		"UPDATE teams SET updated_at=now()",
		"UPDATE registration_records SET terms_agreed_at=NULL",
		"UPDATE registration_source_records SET payload='{}'",
	} {
		t.Run(query, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
			if err := s.Verify(ctx, tx); err == nil {
				t.Fatal("changed destination accepted")
			}
		})
	}
}
