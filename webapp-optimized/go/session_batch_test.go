package isureal

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestSessionBatchAuthorizationAndCommit(t *testing.T) {
	db, counter := tracedTestDB(t)
	db.SetMaxOpenConns(1)
	seedRoundtripFixture(t, db)
	if _, err := db.Exec(`UPDATE users SET account_type='certified_public';
 INSERT INTO users(id,username,display_name,secret_key_hash,region,account_type)
 VALUES('other','other','Other',repeat('ab',32),'asia','certified_public')`); err != nil {
		t.Fatal(err)
	}
	first, err := CreateLoginSession(db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateLoginSession(db, "other")
	if err != nil {
		t.Fatal(err)
	}
	h := &HandlerContext{DB: db, Config: &Config{Region: "asia"}}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	e.PUT("/follows/:userId", followHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.GET("/posts/:postId", getPostHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	e.PATCH("/me", updateMeHandler(h), BatchSessionMiddleware(h, true), RegionMiddleware(h))
	request := func(method, path, token, body string, status int, code string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		counter.count.Store(0)
		e.ServeHTTP(rec, req)
		if rec.Code != status || (code != "" && !strings.Contains(rec.Body.String(), code)) {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		if counter.count.Load() != 1 {
			t.Fatalf("%d exchanges, expected 1", counter.count.Load())
		}
	}
	request("PUT", "/follows/other", "invalid", "", 401, "invalid_session")
	request("GET", "/posts/missing", "invalid", "", 401, "invalid_session")
	request("PATCH", "/me", "invalid", "{", 401, "invalid_session")
	h.Config.Region = "europe"
	request("PUT", "/follows/other", first.Token, "", 403, "region_mismatch")
	request("PATCH", "/me", first.Token, "{", 403, "region_mismatch")
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM follows`); err != nil || count != 0 {
		t.Fatalf("unauthorized mutation: %d, %v", count, err)
	}
	h.Config.Region = "asia"
	request("PATCH", "/me", first.Token, "{", 400, "")
	request("GET", "/posts/missing", first.Token, "", 404, "post_not_found")
	request("PUT", "/follows/other", first.Token, "", 204, "")
	request("PUT", "/follows/owner", second.Token, "", 204, "")
	if err := db.Get(&count, `SELECT count(*) FROM follows WHERE (follower_id='owner' AND followee_id='other') OR (follower_id='other' AND followee_id='owner')`); err != nil || count != 2 {
		t.Fatalf("actor leaked: %d, %v", count, err)
	}
	var clean bool
	if err := db.Get(&clean, `SELECT nullif(current_setting('isureal.actor',true),'') IS NULL`); err != nil || !clean {
		t.Fatalf("actor retained after commit: %v %v", clean, err)
	}
	// This fails at the batch's commit, after the UPDATE returned its JSON result.
	if _, err := db.Exec(`CREATE FUNCTION reject_batch_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject commit'; END $$;
 CREATE CONSTRAINT TRIGGER reject_batch_commit AFTER UPDATE ON users DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_batch_commit()`); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP FUNCTION reject_batch_commit() CASCADE`)
	request("PATCH", "/me", first.Token, `{"displayName":"must rollback"}`, 500, "reject commit")
	var name string
	if err := db.Get(&name, `SELECT display_name FROM users WHERE id='owner'`); err != nil || name != "Owner" {
		t.Fatalf("failed commit persisted: %s %v", name, err)
	}
}
