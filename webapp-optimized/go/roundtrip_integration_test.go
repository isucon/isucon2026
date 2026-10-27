package isureal

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// Count DB exchanges: one ordinary query or one extended-protocol batch.
type queryCounter struct{ count atomic.Int64 }

func (q *queryCounter) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	q.count.Add(1)
	return ctx
}
func (*queryCounter) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}
func (*queryCounter) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData)     {}

func (q *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.count.Add(1)
	return ctx
}
func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func tracedTestDB(t *testing.T) (*sqlx.DB, *queryCounter) {
	t.Helper()
	optimizedTestDB(t)
	config, err := pgx.ParseConfig(os.Getenv("ISUREAL_SESSION_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	counter := &queryCounter{}
	config.Tracer = counter
	db := sqlx.NewDb(sql.OpenDB(sessionConnector{stdlib.GetConnector(*config)}), "pgx-auth")
	t.Cleanup(func() { db.Close() })
	return db, counter
}

func seedRoundtripFixture(t *testing.T, db *sqlx.DB) {
	t.Helper()
	if _, err := db.Exec(`
  INSERT INTO users(id,username,display_name,secret_key_hash,region)
   VALUES('owner','owner','Owner',repeat('ab',32),'asia');
  INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline)
   VALUES('w','asia','2026-09-24',clock_timestamp()-interval '1 minute',clock_timestamp()+interval '1 day');
  INSERT INTO image_objects(id,owner_user_id,data,media_type,created_at)
   VALUES('photo','owner','original bytes','image/jpeg','2026-09-24');
  INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at)
   VALUES('post','owner','w','photo','public','none',clock_timestamp());
 `); err != nil {
		t.Fatal(err)
	}
}

func TestReadRoundtripCountsAndSessionFreshness(t *testing.T) {
	db, counter := tracedTestDB(t)
	seedRoundtripFixture(t, db)
	session, err := CreateLoginSession(db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	h := &HandlerContext{DB: db, Config: &Config{Region: "asia"}}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.GET("/api/me", getMeHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.GET("/api/me/export", exportPostsHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.GET("/api/posts/:postId", getPostHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.GET("/api/posts/:postId/comments", getCommentsHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.GET("/api/posts/:postId/realmojis", getRealMojisHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.GET("/api/notifications", getNotificationsHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	request := func(path, token string, status int, queries int64) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		counter.count.Store(0)
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		if got := counter.count.Load(); got != queries {
			t.Fatalf("%s: %d queries, want %d", path, got, queries)
		}
		return rec
	}
	request("/api/me", session.Token, 200, 1)
	// The export reads the posts, then their images in batches pruned by
	// posting time inside a bounded transaction (BEGIN, SET LOCAL, SELECT,
	// COMMIT); this image was stored under another time, so it is read again
	// by ID.
	request("/api/me/export", session.Token, 200, 6)
	request("/api/posts/post", session.Token, 200, 1)
	request("/api/posts/post/comments", session.Token, 200, 1)
	request("/api/posts/post/realmojis", session.Token, 200, 1)
	if _, err := db.Exec(`UPDATE users SET display_name='Fresh',region='europe' WHERE id='owner'`); err != nil {
		t.Fatal(err)
	}
	request("/api/me", session.Token, 403, 1)
	h.Config.Region = "europe"
	if rec := request("/api/me", session.Token, 200, 1); !strings.Contains(rec.Body.String(), `"displayName":"Fresh"`) {
		t.Fatal("stale profile")
	}
	next, err := CreateLoginSession(db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	request("/api/me", session.Token, 401, 1)
	request("/api/me", next.Token, 200, 1)
	if _, err := db.Exec(`UPDATE users SET status='deleted' WHERE id='owner'`); err != nil {
		t.Fatal(err)
	}
	request("/api/me", next.Token, 401, 1)
}

func TestImageCacheRevalidatesAcrossServersAndInitialization(t *testing.T) {
	db, counter := tracedTestDB(t)
	seedRoundtripFixture(t, db)
	ctx := context.Background()
	owner, err := getUser(ctx, db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	link := signedImageURL(owner, "photo")
	servers := []*echo.Echo{echo.New(), echo.New()}
	for _, e := range servers {
		e.HTTPErrorHandler = APIErrorHandler
		e.GET("/api/images/:imageId", getImageHandler(&HandlerContext{DB: db}))
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	check := func(status int, body, media string) {
		t.Helper()
		for _, e := range servers {
			rec := httptest.NewRecorder()
			counter.count.Store(0)
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, link, nil))
			if rec.Code != status {
				t.Fatalf("image: %d %s", rec.Code, rec.Body.String())
			}
			if counter.count.Load() != 1 {
				t.Fatalf("image queries=%d", counter.count.Load())
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("changed caching policy")
			}
			if status == 200 && (rec.Body.String() != body || rec.Header().Get("Content-Type") != media) {
				t.Fatalf("stale image: %s %s", rec.Header(), rec.Body.String())
			}
		}
	}
	check(200, "original bytes", "image/jpeg")
	check(200, "original bytes", "image/jpeg")
	original, err := loadImageObject(ctx, db, "photo", nil)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := loadImageObject(ctx, db, "photo", &original.Version)
	if err != nil || !cached.Live || cached.Data != nil {
		t.Fatalf("cache hit transferred payload: %+v %v", cached, err)
	}
	exec(`UPDATE image_objects SET data='changed on another VM',media_type='image/png' WHERE id='photo'`)
	check(200, "changed on another VM", "image/png")
	exec(`UPDATE users SET secret_key_hash=repeat('cd',32) WHERE id='owner'`)
	check(403, "", "")
	exec(`UPDATE users SET secret_key_hash=repeat('ab',32) WHERE id='owner'`)
	check(200, "changed on another VM", "image/png")
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='post'`)
	check(403, "", "")
	exec(`UPDATE posts SET deleted_at=NULL WHERE id='post'`)
	exec(`UPDATE users SET status='deleted' WHERE id='owner'`)
	check(403, "", "")
	exec(`UPDATE users SET status='active' WHERE id='owner'`)
	exec(`UPDATE image_objects SET data='' WHERE id='photo'`)
	check(200, "", "image/png")
	check(200, "", "image/png")
	// Keep both server caches alive while resetting the DB and reusing every ID,
	// owner key and created_at. The DB-generated version must still be different.
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(schema))
	seedRoundtripFixture(t, db)
	exec(`UPDATE image_objects SET data='after initialization' WHERE id='photo'`)
	restored, err := loadImageObject(ctx, db, "photo", &original.Version)
	if err != nil || restored.Version == original.Version {
		t.Fatalf("reused image version: %v", err)
	}
	check(200, "after initialization", "image/jpeg")
	check(200, "after initialization", "image/jpeg")
}
