package integration_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/model"
	migration "github.com/isucon/isucon2026/portal/internal/registrationmigration"
	"github.com/isucon/isucon2026/portal/tests/testkit"
)

func pointer[T any](v T) *T { return &v }

func registrationSnapshot() migration.Snapshot {
	stamp := time.Date(2026, 9, 1, 12, 0, 0, 123456000, time.FixedZone("JST", 9*3600))
	return migration.Snapshot{Version: 1, Other: migration.EmptyInventory(), Avatars: map[string]string{"icons/a.png": "https://cdn.example.invalid/a.png"},
		Teams: []migration.Team{{ID: 20, Owner: 10, Name: "imported", IsActive: true, AWSAZ: "apne1-az1", CreatedAt: stamp, UpdatedAt: stamp, WantLocalParticipation: true, IsLocalParticipation: true, AWSCoupon: "aws-code", SacloudCoupon: "sakura-code", CouponAgreedAt: &stamp}},
		Users: []migration.User{
			{ID: 10, TeamID: pointer(int64(20)), Username: "alice", DisplayName: "Alice", GitHubID: "123", IsActive: true, Icon: "icons/a.png", IsStudent: true, RegistrationRecord: model.RegistrationRecord{Consent: model.Consent{TermsAgreedAt: &stamp, PrivacyAgreedAt: &stamp, SponsorWithdrawnAt: &stamp, MinorParentalConsent: pointer(false)}, Email: "alice@example.invalid", TShirtSize: "XL"}},
			{ID: 11, TeamID: pointer(int64(20)), Username: "bob", DisplayName: "Bob", GitHubID: "124", IsActive: true, IsStudent: true},
			{ID: 12, Username: "staff", IsStaff: true},
		},
	}
}

func TestRegistrationImportPreservesRecords(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	s := registrationSnapshot()
	if err := s.Import(ctx, conn); err != nil {
		t.Fatal(err)
	}
	team, err := model.FindTeam(ctx, conn, 20)
	if err != nil {
		t.Fatal(err)
	}
	if team.LeaderID.Int64 != 10 || !team.Student || !team.FinalParticipation || len(team.InviteToken) < 20 {
		t.Fatalf("team=%+v", team)
	}
	for _, id := range []int64{10, 11} {
		c, err := model.FindContestant(ctx, conn, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.TeamID != 20 || !c.InPerson.Bool || c.DiscordID != "" {
			t.Fatalf("contestant=%+v", c)
		}
	}
	c, err := model.FindContestant(ctx, conn, 12)
	if err != nil || c != nil {
		t.Fatal("unassigned staff converted to contestant")
	}
	record, err := model.FindRegistrationRecord(ctx, conn, 10)
	if err != nil {
		t.Fatal(err)
	}
	if record.TermsAgreedAt == nil || !record.TermsAgreedAt.Equal(*s.Users[0].TermsAgreedAt) || record.SponsorAgreedAt != nil || record.SponsorWithdrawnAt == nil || record.MinorParentalConsent == nil || *record.MinorParentalConsent || record.TShirtSize != "XL" {
		t.Fatalf("record=%+v", record)
	}
	coupon, err := model.FindCouponByTeam(ctx, conn, 20)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(coupon.ToPB().Code) != "[aws-code sakura-code]" {
		t.Fatalf("coupon=%+v", coupon)
	}
	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM registration_source_records`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("archive=%d err=%v", count, err)
	}
	if err := s.Import(ctx, conn); err == nil {
		t.Fatal("reimport accepted")
	}
	var next int64
	if err := conn.QueryRow(`SELECT nextval('contestants_id_seq')`).Scan(&next); err != nil || next <= 12 {
		t.Fatalf("next ID can collide with archived user: %d %v", next, err)
	}
}

func TestRegistrationImportRollsBackAndRejectsUnsupportedData(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	s := registrationSnapshot()
	s.Users[1].DisplayName = strings.Repeat("x", 256) // DB failure after teams and first member were inserted.
	if err := s.Import(ctx, conn); err == nil {
		t.Fatal("expected database failure")
	}
	for _, table := range []string{"teams", "contestants", "coupons", "registration_records", "registration_source_records"} {
		var n int
		if err := conn.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial import %s=%d err=%v", table, n, err)
		}
	}
	s = registrationSnapshot()
	s.Other["contact.Ticket"] = 1
	if err := s.Import(ctx, conn); err == nil {
		t.Fatal("unsupported table accepted")
	}
}

func TestRegistrationImportInactiveTeams(t *testing.T) {
	conn := testkit.Database(t)
	ctx := context.Background()
	s := registrationSnapshot()
	stamp := *s.Users[0].TermsAgreedAt
	s.Teams[0].IsActive = false
	s.Teams[0].BannedAt = &stamp
	s.Teams[0].IsGuest = true
	if err := s.Import(ctx, conn); err != nil {
		t.Fatal(err)
	}
	team, err := model.FindTeam(ctx, conn, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !team.Disqualified || !team.IsHidden {
		t.Fatalf("status=%+v", team)
	}
	testkit.Reset(t, conn)
	s = registrationSnapshot()
	s.Teams[0].IsActive = false
	s.Teams[0].DeclinedAt = &stamp
	s.Users[0].TeamID = nil
	s.Users[1].TeamID = nil
	if err := s.Import(ctx, conn); err != nil {
		t.Fatal(err)
	}
	team, err = model.FindTeam(ctx, conn, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !team.Withdrawn || team.LeaderID.Valid || team.Student {
		t.Fatalf("declined=%+v", team)
	}
}
