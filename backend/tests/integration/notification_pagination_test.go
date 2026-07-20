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

func TestUS5NotificationPaginationAndOneTimeRead(t *testing.T) {
	pool, _ := us1Database(t)
	user := uuid.New()
	addUser(t, pool, user, "asia")
	for index := 0; index < 51; index++ {
		window, local := uuid.New(), us1Now.AddDate(0, 0, -index)
		_, err := pool.Exec(context.Background(), `INSERT INTO posting_windows(id,region,local_date,utc_offset_minutes,starts_at,on_time_deadline,ended_at)VALUES($1,'asia',$2,540,$3,$4,$5)`, window, local.Format("2006-01-02"), local.Add(-time.Minute), local.Add(time.Minute), local.Add(2*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(context.Background(), `INSERT INTO notifications(id,user_id,posting_window_id,region,created_at)VALUES($1,$2,$3,'asia',$4)`, uuid.New(), user, window, us1Now.Add(-time.Duration(index)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	repo := postgres.NewNotificationRepository(&postgres.DB{Pool: pool}, domain.RandomIDGenerator{}, clock.NewFake(us1Now))
	first, err := repo.List(context.Background(), user, nil, nil, 51)
	if err != nil || len(first) != 51 {
		t.Fatalf("first=%d err=%v", len(first), err)
	}
	at, id := first[49].CreatedAt, first[49].ID
	tail, err := repo.List(context.Background(), user, &at, &id, 51)
	if err != nil || len(tail) != 1 {
		t.Fatalf("tail=%d err=%v", len(tail), err)
	}
	if _, _, err := repo.Open(context.Background(), user, first[0].ID); err != nil {
		t.Fatal(err)
	}
	var read int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NOT NULL`, user).Scan(&read)
	if read != 1 {
		t.Fatalf("read notifications=%d", read)
	}
	if _, _, err := repo.Open(context.Background(), uuid.New(), first[1].ID); err == nil {
		t.Fatal("foreign notification opened")
	}
}
