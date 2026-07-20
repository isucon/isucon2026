package concurrency_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/jackc/pgx/v5"
)

var notificationNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func notificationUser(t *testing.T, db *postgres.DB, fake *clock.Fake, region domain.Region) (uuid.UUID, service.SessionResult) {
	t.Helper()
	repository := postgres.NewSessionRepository(db, domain.RandomIDGenerator{}, fake)
	name := "n" + uuid.NewString()[:8]
	result, err := service.NewSessionService(repository).Register(context.Background(), service.RegistrationInput{Username: name, Password: "password 1", DisplayName: name, Region: string(region)})
	if err != nil {
		t.Fatal(err)
	}
	return result.User.ID, result
}

func TestUS5ConcurrentSchedulerClaimsAndRegionalIsolation(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	db := &postgres.DB{Pool: pool}
	fake := clock.NewFake(notificationNow)
	repository := postgres.NewNotificationRepository(db, domain.RandomIDGenerator{}, fake)
	for _, region := range []domain.Region{domain.RegionAmerica, domain.RegionEurope, domain.RegionAsia} {
		for range 2 {
			notificationUser(t, db, fake, region)
		}
		schedule, err := domain.NewNotificationSchedule(region, notificationNow.Add(time.Hour), notificationNow)
		if err != nil || repository.SetSchedule(ctx, schedule) != nil {
			t.Fatalf("schedule %s: %v", region, err)
		}
	}
	fake.Advance(time.Hour)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() { defer group.Done(); _, _, _ = repository.StartOneDue(ctx) }()
	}
	group.Wait()
	var windows, notifications, duplicates int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posting_windows`).Scan(&windows)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&notifications)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT user_id,posting_window_id FROM notifications GROUP BY user_id,posting_window_id HAVING count(*)>1) d`).Scan(&duplicates)
	if windows != 3 || notifications != 6 || duplicates != 0 {
		t.Fatalf("windows=%d notifications=%d duplicates=%d", windows, notifications, duplicates)
	}
}

func TestUS5LockedRegionDoesNotBlockAnotherRegion(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	db := &postgres.DB{Pool: pool}
	fake := clock.NewFake(notificationNow)
	repository := postgres.NewNotificationRepository(db, domain.RandomIDGenerator{}, fake)
	for _, region := range []domain.Region{domain.RegionAsia, domain.RegionEurope} {
		notificationUser(t, db, fake, region)
		schedule, _ := domain.NewNotificationSchedule(region, notificationNow.Add(time.Hour), notificationNow)
		if err := repository.SetSchedule(ctx, schedule); err != nil {
			t.Fatal(err)
		}
	}
	fake.Advance(time.Hour)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM notification_schedules WHERE region='asia' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	region, started, err := repository.StartOneDue(ctx)
	if err != nil || !started || region != domain.RegionEurope {
		t.Fatalf("region=%s started=%v err=%v", region, started, err)
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE region='europe'`).Scan(&count)
	if count != 1 {
		t.Fatalf("europe notifications=%d", count)
	}
}

func TestUS5ConcurrentSubscriptionReplacementAndCrashRecovery(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	db := &postgres.DB{Pool: pool}
	fake := clock.NewFake(notificationNow)
	repository := postgres.NewNotificationRepository(db, domain.RandomIDGenerator{}, fake)
	user, login := notificationUser(t, db, fake, domain.RegionAsia)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_ = repository.ReplacePush(ctx, user, login.Token, "https://push.invalid/"+string(rune('a'+index)), "key", "auth")
		}()
	}
	close(start)
	group.Wait()
	var active int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM browser_push_subscriptions WHERE user_id=$1 AND active`, user).Scan(&active)
	if active != 1 {
		t.Fatalf("active subscriptions=%d", active)
	}
	schedule, _ := domain.NewNotificationSchedule(domain.RegionAsia, notificationNow.Add(time.Hour), notificationNow)
	if err := repository.SetSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	if _, _, err := repository.StartOneDue(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := repository.ClaimPush(ctx, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("initial claim=%d err=%v", len(items), err)
	}
	fake.Advance(2 * time.Minute)
	recovered, err := repository.ClaimPush(ctx, 1)
	if err != nil || len(recovered) != 1 || recovered[0].ID != items[0].ID {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
}

func TestUS5ScheduleCancelVersusStartHasOneTerminalOutcome(t *testing.T) {
	ctx := context.Background()
	pool := testkit.Database(t)
	db := &postgres.DB{Pool: pool}
	fake := clock.NewFake(notificationNow)
	repository := postgres.NewNotificationRepository(db, domain.RandomIDGenerator{}, fake)
	notificationUser(t, db, fake, domain.RegionAsia)
	schedule, _ := domain.NewNotificationSchedule(domain.RegionAsia, notificationNow.Add(time.Hour), notificationNow)
	if err := repository.SetSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); <-start; _, _, _ = repository.StartOneDue(ctx) }()
	go func() { defer group.Done(); <-start; _ = repository.CancelSchedule(ctx, domain.RegionAsia) }()
	close(start)
	group.Wait()
	var windows, schedules int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM posting_windows WHERE region='asia'`).Scan(&windows)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM notification_schedules WHERE region='asia'`).Scan(&schedules)
	if schedules != 0 || windows > 1 {
		t.Fatalf("windows=%d schedules=%d", windows, schedules)
	}
}
