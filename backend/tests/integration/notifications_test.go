package integration_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"testing"
	"time"
)

func TestUS5RegionalFanoutWindowAndOpen(t *testing.T) {
	pool, _ := us1Database(t)
	asia, europe, america := uuid.New(), uuid.New(), uuid.New()
	addUser(t, pool, asia, "asia")
	addUser(t, pool, europe, "europe")
	addUser(t, pool, america, "america")
	fake := clock.NewFake(us1Now)
	repo := postgres.NewNotificationRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, fake)
	scheduled := us1Now.Add(time.Hour)
	schedule, err := domain.NewNotificationSchedule(domain.RegionAsia, scheduled, us1Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSchedule(context.Background(), schedule); err != nil {
		t.Fatal(err)
	}
	fake.Set(scheduled)
	region, started, err := repo.StartOneDue(context.Background())
	if err != nil || !started || region != domain.RegionAsia {
		t.Fatalf("start region=%s started=%v err=%v", region, started, err)
	}
	var asiaCount, otherCount int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE user_id=$1`, asia).Scan(&asiaCount)
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE user_id IN($1,$2)`, europe, america).Scan(&otherCount)
	if asiaCount != 1 || otherCount != 0 {
		t.Fatalf("asia=%d other=%d", asiaCount, otherCount)
	}
	var notificationID uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM notifications WHERE user_id=$1`, asia).Scan(&notificationID); err != nil {
		t.Fatal(err)
	}
	destination, path, err := repo.Open(context.Background(), asia, notificationID)
	if err != nil || destination != domain.DestinationCreatePost || path != "/posts/new" {
		t.Fatalf("open=%s %s err=%v", destination, path, err)
	}
	newUser := uuid.New()
	addUser(t, pool, newUser, "asia")
	posts := postgres.NewPostRepository(&postgres.DB{Pool: pool})
	createPost(t, posts, newUser, "joined", domain.VisibilityFriends, scheduled)
	var retroactive int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE user_id=$1`, newUser).Scan(&retroactive)
	if retroactive != 0 {
		t.Fatal("retroactive notification created")
	}
}
func TestUS5PendingRegionAppliedAtOldRegionTurnover(t *testing.T) {
	pool, _ := us1Database(t)
	user := uuid.New()
	addUser(t, pool, user, "america")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET pending_region='asia' WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFake(us1Now)
	repo := postgres.NewNotificationRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, fake)
	schedule, _ := domain.NewNotificationSchedule(domain.RegionAmerica, us1Now.Add(time.Hour), us1Now)
	if err := repo.SetSchedule(context.Background(), schedule); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	if _, _, err := repo.StartOneDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var region string
	var pending *string
	if err := pool.QueryRow(context.Background(), `SELECT region,pending_region FROM users WHERE id=$1`, user).Scan(&region, &pending); err != nil {
		t.Fatal(err)
	}
	if region != "asia" || pending != nil {
		t.Fatalf("region=%s pending=%v", region, pending)
	}
}
