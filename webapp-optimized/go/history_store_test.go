package isureal

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// The history store answers Memories exactly like the database, and leaves a
// post that a write touched to the database.
func TestHistoryStoreMatchesDatabase(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	if os.Getenv("ISUREAL_HOT_STATE") == "off" {
		t.Skip("the hot state is off")
	}
	db, err := sqlx.Connect("pgx-auth", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(schema))
	exec(`DROP TABLE IF EXISTS initial_data_boundary`)

	id := func() string { return uuid.NewString() }
	users := []string{id(), id(), id(), id()}
	for i, u := range users {
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6','asia')`,
			u, "user"+string(rune('a'+i)), "User "+string(rune('A'+i)))
	}
	windows := map[string]string{"2026-08-15": id(), "2026-09-24": id(), "2026-09-25": id()}
	for date, w := range windows {
		exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline,ended_at)
          VALUES($1,'asia',$2,$2::date,$2::date+interval '2 minutes',$2::date+interval '1 day')`, w, date)
	}
	var posts []string
	for _, author := range users[:2] {
		for date, w := range windows {
			p, image := id(), id()
			posts = append(posts, p)
			exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,'photo','image/jpeg')`, image, author)
			exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,caption,visibility,location_kind,latitude,longitude,locality,posted_at)
              VALUES($1,$2,$3,$4,$6,'friends','approximate',35.68,139.76,'Tokyo',$5::date+interval '90 seconds')`, p, author, w, image, date, "caption "+p)
		}
	}
	for i, p := range posts {
		for j, u := range users {
			created := time.Date(2026, 9, 26, 0, i, j, 123456000, time.UTC)
			exec(`INSERT INTO comments(id,post_id,author_id,body,created_at) VALUES($1,$2,$3,'comment',$4)`, id(), p, u, created)
			image := id()
			exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,'moji','image/jpeg')`, image, u)
			exec(`INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at) VALUES($1,$2,$3,$4,'like',$5)`, id(), p, u, image, created)
		}
	}
	exec(`UPDATE comments SET deleted_at=clock_timestamp() WHERE id IN (SELECT id FROM comments ORDER BY id LIMIT 2)`)
	exec(`UPDATE realmojis SET replaced_at=clock_timestamp() WHERE id IN (SELECT id FROM realmojis ORDER BY id LIMIT 2)`)
	exec(`CREATE TABLE initial_data_boundary AS SELECT clock_timestamp() AS created_at`)
	defer exec(`DROP TABLE IF EXISTS initial_data_boundary`)
	exec(`UPDATE users SET status='deleted',deleted_at=clock_timestamp() WHERE id=$1`, users[3])

	ctx := context.Background()
	if err := hot.load(ctx, db); err != nil {
		t.Fatal(err)
	}
	saved := history
	history = &historyStore{runDates: map[time.Time]bool{}, dirty: map[string]bool{}}
	defer func() {
		history = saved
		hot.mu.Lock()
		hot.reset()
		hot.mu.Unlock()
	}()
	if err := history.load(ctx, db, "asia"); err != nil {
		t.Fatal(err)
	}

	compare := func(viewer string, start, end time.Time, wantStore bool) {
		t.Helper()
		user, ok := hot.userState(viewer)
		if !ok {
			t.Fatal("viewer not in the hot state")
		}
		fromDB, err := listMemories(ctx, db, viewer, start, end, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		fromStore, ok := history.memories(user, start, end, nil, "")
		if ok != wantStore {
			t.Fatalf("answered from the store: %v, want %v", ok, wantStore)
		}
		if !ok {
			return
		}
		a, _ := json.Marshal(fromDB)
		b, _ := json.Marshal(fromStore)
		if string(a) != string(b) {
			t.Fatalf("store differs from the database:\n%s\n%s", a, b)
		}
	}
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, viewer := range users[:3] {
		compare(viewer, september, september.AddDate(0, 1, 0), true)
		compare(viewer, september.AddDate(0, -1, 0), september, true)
		compare(viewer, time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), true)
	}
	// A write to a post leaves its pages to the database.
	history.touch(posts[1])
	compare(users[0], september, september.AddDate(0, 1, 0), false)
	compare(users[0], september.AddDate(0, -1, 0), september, true)
	// So does a window opened after the initial data.
	history.windowOpened(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	compare(users[1], september.AddDate(0, -1, 0), september, false)
}
