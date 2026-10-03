package isureal

import (
	"context"
	"testing"
	"time"
)

func TestNotificationPollBatchIsolation(t *testing.T) {
	db, counter := tracedTestDB(t)
	seedRoundtripFixture(t, db)
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region,status) VALUES
 ('other','other','Other',repeat('ab',32),'asia','active'),
 ('moved','moved','Moved',repeat('ab',32),'europe','active'),
 ('deleted','deleted','Deleted',repeat('ab',32),'asia','deleted'),
 ('bad-key','bad-key','Bad', 'invalid','asia','active');
 INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline,ended_at)
 VALUES('ended','asia','2026-09-23','2026-09-23','2026-09-23','2026-09-24'),
 ('w-eu','europe','2026-09-24','2026-09-24','2026-09-25',NULL),
 ('w-us','america','2026-09-24','2026-09-24','2026-09-25',NULL);
 INSERT INTO notifications(id,user_id,posting_window_id,created_at) VALUES
 ('ended-notification','owner','ended','2026-09-24 00:00:01Z'),
 ('b','owner','w-eu','2026-09-24 00:00:02Z'),
 ('a','owner','w','2026-09-24 00:00:02Z'),
 ('c','owner','w-us','2026-09-24 00:00:03Z');`)
	p := newNotificationPoller(db)
	at := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	var batch []*notificationSubscriber
	add := func(user string, seconds int) *notificationSubscriber {
		s := &notificationSubscriber{ID: int64(len(batch) + 1), UserID: user, Region: "asia", ConnectedAt: at.Add(time.Duration(seconds) * time.Second), result: make(chan notificationResult, 1)}
		batch = append(batch, s)
		p.subscribers[s.ID] = s
		return s
	}
	for range 64 {
		add("owner", 0)
	}
	later := add("owner", 2)
	noNew := add("owner", 3)
	other := add("other", 0)
	moved := add("moved", 0)
	deleted := add("deleted", 0)
	missing := add("missing", 0)
	bad := add("bad-key", 0)
	counter.count.Store(0)
	p.poll(context.Background(), batch)
	if got := counter.count.Load(); got != 1 {
		t.Fatalf("%d queries for %d subscriptions", got, len(batch))
	}
	receive := func(s *notificationSubscriber, want string) {
		t.Helper()
		select {
		case r := <-s.result:
			if r.err != nil {
				t.Fatal(r.err)
			}
			if want == "" {
				if r.notification != nil {
					t.Fatalf("unexpected notification: %+v", r.notification)
				}
			} else if r.notification == nil || r.notification.ID != want {
				t.Fatalf("want %s, got %+v", want, r.notification)
			}
		default:
			t.Fatalf("no result for subscription %d", s.ID)
		}
	}
	for _, s := range batch[:64] {
		receive(s, "a")
	}
	receive(later, "c")
	for _, s := range []*notificationSubscriber{moved, deleted, missing, bad} {
		receive(s, "")
	}
	for _, s := range []*notificationSubscriber{noNew, other} {
		select {
		case r := <-s.result:
			t.Fatalf("unrelated/old notification delivered: %+v", r)
		default:
		}
	}
	// Previously waiting subscribers observe changes on the next batch, without
	// a reconnect. Logout intentionally does not terminate an established stream.
	exec(`UPDATE users SET status='deleted' WHERE id='other'; UPDATE users SET region='europe' WHERE id='owner'`)
	p.poll(context.Background(), []*notificationSubscriber{noNew, other})
	receive(noNew, "")
	receive(other, "")
}

func TestNotificationPollerLifecycle(t *testing.T) {
	db, _ := tracedTestDB(t)
	seedRoundtripFixture(t, db)
	p := newNotificationPoller(db)
	at := time.Now().Add(time.Hour)
	var releases []func()
	for range 64 {
		_, release := p.subscribe("owner", "asia", at)
		releases = append(releases, release)
	}
	p.mu.Lock()
	if len(p.subscribers) != 64 || p.cancel == nil {
		t.Fatal("poller not started")
	}
	p.mu.Unlock()
	for _, release := range releases {
		release()
	}
	p.mu.Lock()
	if len(p.subscribers) != 0 || p.cancel != nil {
		t.Fatal("poller not stopped")
	}
	p.mu.Unlock()
	// A new generation must work even while the old generation is cancelling.
	s, release := p.subscribe("missing", "asia", at)
	defer release()
	select {
	case r := <-s.result:
		if r.err != nil || r.notification != nil {
			t.Fatalf("unexpected result: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new poller did not deliver")
	}
	p.mu.Lock()
	stopped := len(p.subscribers) == 0 && p.cancel == nil
	p.mu.Unlock()
	if !stopped {
		t.Fatal("terminal delivery did not stop polling before unsubscribe")
	}
}
