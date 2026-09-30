package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func TestRegistrationRecordsAndDiscordUpgrade(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	// Simulate the index used by an already deployed database.
	if _, err := conn.Exec(`DROP INDEX index_contestants_on_discord_id; CREATE UNIQUE INDEX index_contestants_on_discord_id ON contestants(discord_id); DELETE FROM schema_migrations WHERE version='001_registration_records.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2} {
		if _, err := conn.Exec(`INSERT INTO contestants(id,team_id,name,github_login,github_id,discord_id,discord_tag,avatar_url,created_at,updated_at) VALUES($1,1,'name','login',$2,'','','',now(),now())`, id, fmt.Sprint(id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(`UPDATE contestants SET discord_id='123' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE contestants SET discord_id='123' WHERE id=2`); err == nil {
		t.Fatal("duplicate Discord ID accepted")
	}
	stamp := time.Date(2026, 9, 1, 12, 34, 56, 123456000, time.FixedZone("JST", 9*3600))
	no := false
	want := model.RegistrationRecord{Consent: model.Consent{TermsAgreedAt: &stamp, MinorParentalConsent: &no}, Email: "person@example.invalid", TShirtSize: "XL"}
	if err := model.SaveRegistrationRecord(ctx, conn, 1, want); err != nil {
		t.Fatal(err)
	}
	got, err := model.FindRegistrationRecord(ctx, conn, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.TermsAgreedAt == nil || !got.TermsAgreedAt.Equal(stamp) || got.PrivacyAgreedAt != nil || got.MinorParentalConsent == nil || *got.MinorParentalConsent || got.Email != want.Email || got.TShirtSize != "XL" {
		t.Fatalf("record lost: %+v", got)
	}
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
}
