package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/tests/testkit"
)

func TestUS2UserSearchDisclosureAndLimits(t *testing.T) {
	pool, _ := us1Database(t)
	viewer, target := uuid.New(), uuid.New()
	addUser(t, pool, viewer, "america")
	addUser(t, pool, target, "america")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET display_name='Café',display_name_search=$2,bio='private' WHERE id=$1`, target, domain.NormalizeSearchText("Café")); err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewUserRepository(&postgres.DB{Pool: pool})
	users, err := repository.Search(context.Background(), viewer, "CAFE\u0301", nil, nil, 51)
	if err != nil || len(users) != 1 {
		t.Fatalf("normalized search count=%d err=%v", len(users), err)
	}
	if users[0].Bio != "" {
		t.Fatal("regular bio disclosed")
	}
	if _, err := repository.Search(context.Background(), viewer, "", nil, nil, 51); err == nil {
		t.Fatal("empty query accepted")
	}
	if _, err := repository.Search(context.Background(), viewer, string(make([]rune, 101)), nil, nil, 51); err == nil {
		t.Fatal("oversized query accepted")
	}
	if _, err := pool.Exec(context.Background(), `UPDATE users SET account_type='certified_public' WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	profile, err := repository.GetPublic(context.Background(), viewer, target)
	if err != nil || profile.Bio != "private" {
		t.Fatalf("certified disclosure=%q err=%v", profile.Bio, err)
	}
}

func TestUS2SearchAndRelationshipPagination(t *testing.T) {
	pool, _ := us1Database(t)
	viewer := uuid.New()
	addUser(t, pool, viewer, "america")
	usersRepo := postgres.NewUserRepository(&postgres.DB{Pool: pool})
	for index := 0; index < 51; index++ {
		id := uuid.New()
		addUser(t, pool, id, "america")
	}
	first, err := usersRepo.Search(context.Background(), viewer, "u", nil, nil, 51)
	if err != nil || len(first) != 51 {
		t.Fatalf("first=%d err=%v", len(first), err)
	}
	name, id := first[49].UsernameNormalized, first[49].ID
	second, err := usersRepo.Search(context.Background(), viewer, "u", &name, &id, 51)
	if err != nil || len(second) != 1 {
		t.Fatalf("second=%d err=%v", len(second), err)
	}
	for _, user := range first[:50] {
		if user.ID == second[0].ID {
			t.Fatal("duplicate search result")
		}
	}
	relations := postgres.NewRelationshipRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, testkit.FixedClock())
	for _, target := range first {
		if _, err := pool.Exec(context.Background(), `UPDATE users SET account_type='certified_public' WHERE id=$1`, target.ID); err != nil {
			t.Fatal(err)
		}
		if err := relations.Follow(context.Background(), viewer, target.ID); err != nil {
			t.Fatal(err)
		}
	}
	page, err := relations.List(context.Background(), viewer, domain.RelationshipFollowing, nil, nil, 51)
	if err != nil || len(page) != 51 {
		t.Fatalf("relations=%d err=%v", len(page), err)
	}
	at, relationID := page[49].UpdatedAt, page[49].RelationID
	tail, err := relations.List(context.Background(), viewer, domain.RelationshipFollowing, &at, &relationID, 51)
	if err != nil || len(tail) != 1 {
		t.Fatalf("relation tail=%d err=%v", len(tail), err)
	}
}
