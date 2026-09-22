package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/domain"
	isurealimage "github.com/isucon/isucon2026/backend/internal/image"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

var us1Now = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func us1Database(t *testing.T) (*pgxpool.Pool, *postgres.PostRepository) {
	t.Helper()
	pool := testkit.Database(t)
	return pool, postgres.NewPostRepository(&postgres.DB{Pool: pool})
}

func addUser(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, region string) {
	t.Helper()
	name := "u" + id.String()[:8]
	_, err := pool.Exec(context.Background(), `INSERT INTO users(id,username,username_normalized,secret_key_hash,display_name,display_name_search,region) VALUES($1,$2::varchar,$2::varchar,'hash',$2::text,$2::text,$3)`, id, name, region)
	if err != nil {
		t.Fatal(err)
	}
}

func addWindow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, region string, ended bool) {
	t.Helper()
	offset := map[string]int{"america": -300, "europe": 60, "asia": 540}[region]
	var endedAt any
	if ended {
		endedAt = us1Now.Add(time.Hour)
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO posting_windows(id,region,local_date,utc_offset_minutes,starts_at,on_time_deadline,ended_at) VALUES($1,$2,'2026-07-19',$3,$4,$5,$6)`, id, region, offset, us1Now.Add(-time.Minute), us1Now.Add(time.Minute), endedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func createPost(t *testing.T, repo *postgres.PostRepository, author uuid.UUID, caption string, visibility domain.Visibility, at time.Time) domain.PostRecord {
	t.Helper()
	id := uuid.New()
	object := isurealimage.Object{ID: uuid.New(), OwnerUserID: author, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ObjectKey: id.String() + ".jpg", ByteSize: 10, MediaType: "image/jpeg"}
	post, err := repo.Create(context.Background(), author, object, caption, visibility, domain.NoLocation(), at, id)
	if err != nil {
		t.Fatal(err)
	}
	record, err := repo.Get(context.Background(), post.ID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func addFriendship(t *testing.T, pool *pgxpool.Pool, left, right uuid.UUID) {
	t.Helper()
	if left.String() > right.String() {
		left, right = right, left
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO friendships(id,user_low_id,user_high_id) VALUES($1,$2,$3)`, uuid.New(), left, right); err != nil {
		t.Fatal(err)
	}
}
