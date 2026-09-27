package isureal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommentIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx", dsn)
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
	tokens := map[string]string{}
	for _, id := range []string{"author", "reactor", "stranger", "mutual"} {
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'9caf06bb4436cdbfa20af9121a626bc1093c4f54b31c0fa937957856135345b6','asia')`, id, id, id)
		session, err := CreateLoginSession(db, id)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = session.Token
	}
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('window','asia','2026-09-24',clock_timestamp(),clock_timestamp()+interval '1 day')`)
	for _, id := range []string{"author", "reactor"} {
		exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,'photo','image/jpeg')`, id+"-image", id)
		exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at) VALUES($1,$2,'window',$3,'public','none',clock_timestamp())`, id+"-post", id, id+"-image")
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/api/posts/:postId/comments", createCommentHandler(h), SessionMiddleware(h, true))
	e.GET("/api/posts/:postId/comments", getCommentsHandler(h), SessionMiddleware(h, true))
	e.DELETE("/api/comments/:commentId", deleteCommentHandler(h), SessionMiddleware(h, true))
	request := func(actor, method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokens[actor])
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	check := func(rec *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
		}
	}
	path := "/api/posts/author-post/comments"
	create := func(actor, body string, status int) createCommentResponse {
		t.Helper()
		data, err := json.Marshal(map[string]string{"body": body})
		if err != nil {
			t.Fatal(err)
		}
		rec := request(actor, http.MethodPost, path, data)
		check(rec, status)
		var out createCommentResponse
		if status == http.StatusCreated {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	list := func(actor, suffix string, status int) getCommentsResponse {
		t.Helper()
		rec := request(actor, http.MethodGet, path+suffix, nil)
		check(rec, status)
		var out getCommentsResponse
		if status == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	denied := func(actor string) {
		t.Helper()
		create(actor, "denied", http.StatusForbidden)
		list(actor, "", http.StatusForbidden)
	}
	create("", "body", http.StatusUnauthorized)
	list("", "", http.StatusUnauthorized)
	denied("stranger")
	create("reactor", "", http.StatusBadRequest)
	create("reactor", strings.Repeat("あ", 1001), http.StatusBadRequest)
	if out := list("author", "", http.StatusOK); out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("empty list=%+v", out)
	}
	first := create("reactor", strings.Repeat("あ", 1000), http.StatusCreated)
	if first.Author.ID != "reactor" || first.Body != strings.Repeat("あ", 1000) {
		t.Fatalf("response=%+v", first)
	}
	second := create("author", "self", http.StatusCreated)
	out := list("reactor", "", http.StatusOK)
	if len(out.Items) != 2 || out.Items[0].ID != first.ID {
		t.Fatalf("list=%+v", out)
	}
	check(request("stranger", http.MethodDelete, "/api/comments/"+first.ID, nil), http.StatusForbidden)
	check(request("", http.MethodDelete, "/api/comments/"+first.ID, nil), http.StatusUnauthorized)
	check(request("author", http.MethodDelete, "/api/comments/"+first.ID, nil), http.StatusNoContent)
	check(request("author", http.MethodDelete, "/api/comments/"+first.ID, nil), http.StatusNotFound)
	own := create("reactor", "own", http.StatusCreated)
	check(request("reactor", http.MethodDelete, "/api/comments/"+own.ID, nil), http.StatusNoContent)
	if out := list("author", "", http.StatusOK); len(out.Items) != 1 || out.Items[0].ID != second.ID {
		t.Fatalf("deleted comments listed: %+v", out)
	}
	exec(`UPDATE posts SET visibility='friends_of_friends' WHERE id='author-post'`)
	exec(`INSERT INTO friendships(id,user_low_id,user_high_id) VALUES ('00000000-0000-0000-0000-000000000001','author','mutual'),('00000000-0000-0000-0000-000000000002','mutual','reactor')`)
	allowed, err := canViewPost(context.Background(), db, "reactor", "author-post")
	if err != nil || !allowed {
		t.Fatalf("fof should see post: %v %v", allowed, err)
	}
	denied("reactor")
	exec(`INSERT INTO friendships(id,user_low_id,user_high_id) VALUES ('00000000-0000-0000-0000-000000000003','author','reactor')`)
	create("reactor", "friend", http.StatusCreated)
	list("reactor", "", http.StatusOK)
	exec(`UPDATE posts SET visibility='friends' WHERE id='author-post'`)
	create("reactor", "friend", http.StatusCreated)
	list("reactor", "", http.StatusOK)
	for _, pair := range [][2]string{{"author", "reactor"}, {"reactor", "author"}} {
		exec(`INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES('00000000-0000-0000-0000-000000000004',$1,$2)`, pair[0], pair[1])
		denied("reactor")
		exec(`DELETE FROM block_relations`)
	}
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='reactor-post'`)
	denied("reactor")
	exec(`UPDATE posts SET deleted_at=NULL WHERE id='reactor-post'`)
	exec(`UPDATE posting_windows SET ended_at=clock_timestamp()`)
	denied("reactor")
	denied("author")
	check(request("author", http.MethodDelete, "/api/comments/"+second.ID, nil), http.StatusNoContent)
	exec(`UPDATE posting_windows SET ended_at=NULL`)
	exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline) VALUES('europe-window','europe','2026-09-25',clock_timestamp(),clock_timestamp()+interval '1 day')`)
	exec(`UPDATE users SET region='europe' WHERE id='author'`)
	denied("author")
	exec(`UPDATE posting_windows SET local_date='2026-09-24' WHERE id='europe-window'`)
	create("author", "same date across regions", http.StatusCreated)
	exec(`UPDATE users SET region='asia' WHERE id='author'`)
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='author-post'`)
	denied("author")
	denied("reactor")
	exec(`UPDATE posts SET deleted_at=NULL WHERE id='author-post'`)
	exec(`CREATE FUNCTION reject_comment() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject comment'; END $$`)
	exec(`CREATE TRIGGER reject_comment BEFORE INSERT ON comments FOR EACH ROW EXECUTE FUNCTION reject_comment()`)
	create("reactor", "rollback", http.StatusInternalServerError)
	exec(`DROP TRIGGER reject_comment ON comments`)
	exec(`DROP FUNCTION reject_comment()`)
	exec(`UPDATE comments SET deleted_at=clock_timestamp()`)
	for i := 0; i < 52; i++ {
		exec(`INSERT INTO comments(id,post_id,author_id,body,created_at) VALUES($1,'author-post','reactor','page','2026-09-24T00:00:00Z')`, fmt.Sprintf("page-%02d", i))
	}
	page := list("author", "", http.StatusOK)
	if len(page.Items) != 50 || !page.HasMore {
		t.Fatalf("page=%+v", page)
	}
	last := page.Items[49]
	tail := list("author", "?afterCreatedAt="+url.QueryEscape(last.CreatedAt.Format(time.RFC3339Nano))+"&afterId="+last.ID, http.StatusOK)
	if len(tail.Items) != 2 || tail.HasMore || tail.Items[0].ID <= last.ID {
		t.Fatalf("tail=%+v", tail)
	}
	list("author", "?afterId=x", http.StatusBadRequest)
}
