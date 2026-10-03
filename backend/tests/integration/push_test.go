package integration_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/domain"
	"github.com/isucon/isucon2026/backend/internal/repository/postgres"
	"github.com/isucon/isucon2026/backend/internal/service"
	"testing"
	"time"
)

func TestUS5PushSubscriptionRetryAndPermanentFailure(t *testing.T) {
	pool, _ := us1Database(t)
	user := uuid.New()
	addUser(t, pool, user, "asia")
	fake := clock.NewFake(us1Now)
	db := &postgres.DB{Pool: pool}
	sessions := postgres.NewSessionRepository(db, domain.RandomIDGenerator{}, fake)
	hash := service.HashCredential("password-1")
	_, _ = pool.Exec(context.Background(), `UPDATE users SET secret_key_hash=$2 WHERE id=$1`, user, hash)
	login, err := service.NewSessionService(sessions).Login(context.Background(), service.LoginInput{Username: "u" + user.String()[:8], SecretKey: "password-1"})
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewNotificationRepository(db, domain.RandomIDGenerator{}, fake)
	if err := repo.ReplacePush(context.Background(), user, login.Token, "https://push.invalid/one", "p256dh", "auth"); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplacePush(context.Background(), user, login.Token, "https://push.invalid/two", "p256dh2", "auth2"); err != nil {
		t.Fatal(err)
	}
	var active int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM browser_push_subscriptions WHERE user_id=$1 AND active`, user).Scan(&active)
	if active != 1 {
		t.Fatalf("active subscriptions=%d", active)
	}
	schedule, _ := domain.NewNotificationSchedule(domain.RegionAsia, us1Now.Add(time.Hour), us1Now)
	if err := repo.SetSchedule(context.Background(), schedule); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	if _, _, err := repo.StartOneDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var logical uuid.UUID
	for attempt := 1; attempt <= 4; attempt++ {
		items, err := repo.ClaimPush(context.Background(), 10)
		if err != nil || len(items) != 1 {
			t.Fatalf("claim %d count=%d err=%v", attempt, len(items), err)
		}
		if attempt == 1 {
			logical = items[0].NotificationID
		} else if items[0].NotificationID != logical {
			t.Fatal("logical notification ID changed")
		}
		if err := repo.FinishPush(context.Background(), items[0], true, false, "transient"); err != nil {
			t.Fatal(err)
		}
		fake.Advance(time.Minute)
	}
	var state string
	var attempts int
	if err := pool.QueryRow(context.Background(), `SELECT status,attempts FROM push_attempts`).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || attempts != 4 {
		t.Fatalf("state=%s attempts=%d", state, attempts)
	}
}

func TestUS5PushSuccessPermanentInvalidationAndStaleSessionSuppression(t *testing.T) {
	pool, _ := us1Database(t)
	fake := clock.NewFake(us1Now)
	db := &postgres.DB{Pool: pool}
	repo := postgres.NewNotificationRepository(db, domain.RandomIDGenerator{}, fake)
	window := uuid.New()
	addWindow(t, pool, window, "asia", false)
	type fixture struct{ user, notification, subscription, session uuid.UUID }
	create := func(endpoint string) fixture {
		user := uuid.New()
		addUser(t, pool, user, "asia")
		hash := service.HashCredential("password-1")
		_, _ = pool.Exec(context.Background(), `UPDATE users SET secret_key_hash=$2 WHERE id=$1`, user, hash)
		sessions := postgres.NewSessionRepository(db, domain.RandomIDGenerator{}, fake)
		login, err := service.NewSessionService(sessions).Login(context.Background(), service.LoginInput{Username: "u" + user.String()[:8], SecretKey: "password-1"})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.ReplacePush(context.Background(), user, login.Token, endpoint, "p256dh", "auth"); err != nil {
			t.Fatal(err)
		}
		var subscription, session uuid.UUID
		if err := pool.QueryRow(context.Background(), `SELECT id,session_id FROM browser_push_subscriptions WHERE user_id=$1 AND active`, user).Scan(&subscription, &session); err != nil {
			t.Fatal(err)
		}
		notification := uuid.New()
		if _, err := pool.Exec(context.Background(), `INSERT INTO notifications(id,user_id,posting_window_id,region,created_at)VALUES($1,$2,$3,'asia',$4)`, notification, user, window, us1Now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `INSERT INTO push_attempts(id,notification_id,subscription_id,status,next_attempt_at,created_at,updated_at)VALUES($1,$2,$3,'pending',$4,$4,$4)`, uuid.New(), notification, subscription, us1Now); err != nil {
			t.Fatal(err)
		}
		return fixture{user, notification, subscription, session}
	}
	delivered := create("https://push.invalid/delivered")
	permanent := create("https://push.invalid/permanent")
	stale := create("https://push.invalid/stale")
	if _, err := pool.Exec(context.Background(), `UPDATE login_sessions SET active=false,invalidated_at=$2,reason='test' WHERE id=$1`, stale.session, us1Now); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ClaimPush(context.Background(), 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("claim=%d err=%v", len(items), err)
	}
	for _, item := range items {
		switch item.NotificationID {
		case delivered.notification:
			if err := repo.FinishPush(context.Background(), item, false, false, ""); err != nil {
				t.Fatal(err)
			}
		case permanent.notification:
			if err := repo.FinishPush(context.Background(), item, false, true, "gone"); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("stale or unknown notification claimed: %s", item.NotificationID)
		}
	}
	var deliveredState, permanentState string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM push_attempts WHERE notification_id=$1`, delivered.notification).Scan(&deliveredState)
	_ = pool.QueryRow(context.Background(), `SELECT status FROM push_attempts WHERE notification_id=$1`, permanent.notification).Scan(&permanentState)
	var permanentActive bool
	_ = pool.QueryRow(context.Background(), `SELECT active FROM browser_push_subscriptions WHERE id=$1`, permanent.subscription).Scan(&permanentActive)
	var notificationCount int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE id IN($1,$2,$3)`, delivered.notification, permanent.notification, stale.notification).Scan(&notificationCount)
	if deliveredState != "delivered" || permanentState != "permanent_failure" || permanentActive || notificationCount != 3 {
		t.Fatalf("delivered=%s permanent=%s active=%v notifications=%d", deliveredState, permanentState, permanentActive, notificationCount)
	}
}
