package isureal

import (
	"context"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
)

// The hot state decides access to comments and RealMojis like opt_access.
func TestInteractionAllowedMatchesDatabase(t *testing.T) {
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
	// author; friend; fof (a friend of friend); stranger; blocked (a friend
	// who blocked the author); idle (a friend who has not posted today);
	// europe (a friend in another region, posted there).
	for _, u := range []string{"author", "friend", "fof", "stranger", "blocked", "idle", "europe"} {
		region := "asia"
		if u == "europe" {
			region = "europe"
		}
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6',$4)`, u, u, u, region)
	}
	friends := [][2]string{{"author", "friend"}, {"friend", "fof"}, {"author", "blocked"}, {"author", "idle"}, {"author", "europe"}}
	for i, f := range friends {
		low, high := min(f[0], f[1]), max(f[0], f[1])
		exec(`INSERT INTO friendships(id,user_low_id,user_high_id) VALUES($1,$2,$3)`, "f"+string(rune('0'+i)), low, high)
	}
	exec(`INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES('b1','blocked','author')`)
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('asia-w','asia','2026-10-28',clock_timestamp(),clock_timestamp()+interval '1 day')`)
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('europe-w','europe','2026-10-28',clock_timestamp(),clock_timestamp()+interval '1 day')`)
	post := func(id, author, window, visibility string) {
		exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,'x','image/jpeg')`, id+"-image", author)
		exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) VALUES($1,$2,$3,$4,$5,'none',clock_timestamp())`,
			id, author, window, id+"-image", visibility)
	}
	for _, v := range []string{"friends", "friends_of_friends", "public"} {
		post("author-"+v, "author", "asia-w", v)
	}
	for _, u := range []string{"friend", "fof", "stranger", "blocked"} {
		post(u+"-own", u, "asia-w", "friends")
	}
	post("europe-own", "europe", "europe-w", "friends")

	ctx := context.Background()
	if err := hot.load(ctx, db); err != nil {
		t.Fatal(err)
	}
	defer func() {
		hot.mu.Lock()
		hot.reset()
		hot.mu.Unlock()
	}()
	checked := 0
	for _, viewer := range []string{"author", "friend", "fof", "stranger", "blocked", "idle", "europe"} {
		for _, p := range []string{"author-friends", "author-friends_of_friends", "author-public"} {
			for _, comments := range []bool{true, false} {
				var want bool
				if err := db.GetContext(ctx, &want, `SELECT opt_access($1,$2,$3)`, viewer, p, comments); err != nil {
					t.Fatal(err)
				}
				got, ok := hot.interactionAllowed(viewer, p, comments)
				if !ok {
					t.Fatalf("%s %s: the hot state cannot answer", viewer, p)
				}
				if got != want {
					t.Errorf("%s %s comments=%v: hot %v, opt_access %v", viewer, p, comments, got, want)
				}
				if want {
					checked++
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no access granted at all")
	}
}
