package isureal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestRealMojiIntegration(t *testing.T) {
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
	for _, id := range []string{"author", "reactor", "stranger"} {
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'hash','asia')`, id, id, id)
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
	e.POST("/api/posts/:postId/realmojis", replaceRealMojiHandler(h), SessionMiddleware(h, true))
	e.GET("/api/posts/:postId/realmojis", getRealMojisHandler(h), SessionMiddleware(h, true))
	e.DELETE("/api/realmojis/:realMojiId", deleteRealMojiHandler(h), SessionMiddleware(h, true))
	e.GET("/api/images/:imageId", getImageHandler(h), SessionMiddleware(h, true))
	request := func(actor, method, path, media string, data []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", media)
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
	payload := []byte("unchanged image bytes\x00\xff")
	makeBody := func(kind string, includeImage bool) ([]byte, string) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("reactionType", kind); err != nil {
			t.Fatal(err)
		}
		if includeImage {
			file, err := writer.CreateFormFile("image", "image.jpg")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write(payload); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return body.Bytes(), writer.FormDataContentType()
	}
	body, media := makeBody("heart", true)
	path := "/api/posts/author-post/realmojis"
	decode := func(rec *httptest.ResponseRecorder) replaceRealMojiResponse {
		t.Helper()
		var out replaceRealMojiResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	create := func(actor string, status int) replaceRealMojiResponse {
		t.Helper()
		rec := request(actor, http.MethodPost, path, media, body)
		check(rec, status)
		if status == http.StatusCreated {
			return decode(rec)
		}
		return replaceRealMojiResponse{}
	}
	list := func(suffix string) getRealMojisResponse {
		t.Helper()
		rec := request("author", http.MethodGet, path+suffix, "", nil)
		check(rec, http.StatusOK)
		var out getRealMojisResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	imageCheck := func(actor string, r replaceRealMojiResponse, status int) {
		t.Helper()
		rec := request(actor, http.MethodGet, r.ImageURL, "", nil)
		check(rec, status)
		if status == http.StatusOK && !bytes.Equal(rec.Body.Bytes(), payload) {
			t.Fatal("image bytes changed")
		}
	}
	create("", http.StatusUnauthorized)
	create("author", http.StatusForbidden)
	create("stranger", http.StatusForbidden)
	for _, test := range []struct {
		kind  string
		image bool
	}{{"bad", true}, {"like", false}} {
		b, m := makeBody(test.kind, test.image)
		check(request("reactor", http.MethodPost, path, m, b), http.StatusBadRequest)
	}
	if out := list(""); out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("empty list: %+v", out)
	}
	first := create("reactor", http.StatusCreated)
	imageCheck("author", first, http.StatusOK)
	imageCheck("reactor", first, http.StatusOK)
	imageCheck("stranger", first, http.StatusNotFound)
	second := create("reactor", http.StatusCreated)
	imageCheck("author", first, http.StatusNotFound)
	if out := list(""); len(out.Items) != 1 || out.Items[0].ID != second.ID {
		t.Fatalf("replacement list: %+v", out)
	}
	exec(`CREATE FUNCTION reject_realmoji() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject realmoji'; END $$`)
	exec(`CREATE TRIGGER reject_realmoji BEFORE INSERT ON realmojis FOR EACH ROW EXECUTE FUNCTION reject_realmoji()`)
	create("reactor", http.StatusInternalServerError)
	imageCheck("author", second, http.StatusOK)
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM image_objects WHERE owner_user_id='reactor'`); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("image rollback count=%d", count)
	}
	exec(`DROP TRIGGER reject_realmoji ON realmojis`)
	exec(`DROP FUNCTION reject_realmoji()`)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 4)
	for range 4 {
		wg.Go(func() { results <- request("reactor", http.MethodPost, path, media, body) })
	}
	wg.Wait()
	close(results)
	for rec := range results {
		check(rec, http.StatusCreated)
	}
	out := list("")
	if len(out.Items) != 1 {
		t.Fatalf("concurrent replacements: %+v", out)
	}
	active := out.Items[0]
	check(request("stranger", http.MethodDelete, "/api/realmojis/"+active.ID, "", nil), http.StatusForbidden)
	check(request("author", http.MethodDelete, "/api/realmojis/"+active.ID, "", nil), http.StatusNoContent)
	imageCheck("author", active, http.StatusNotFound)
	check(request("author", http.MethodDelete, "/api/realmojis/"+active.ID, "", nil), http.StatusNotFound)
	third := create("reactor", http.StatusCreated)
	check(request("reactor", http.MethodDelete, "/api/realmojis/"+third.ID, "", nil), http.StatusNoContent)
	for _, pair := range [][2]string{{"author", "reactor"}, {"reactor", "author"}} {
		exec(`INSERT INTO block_relations(id,blocker_id,blocked_id) VALUES('00000000-0000-0000-0000-000000000001',$1,$2)`, pair[0], pair[1])
		create("reactor", http.StatusForbidden)
		exec(`DELETE FROM block_relations`)
	}
	exec(`UPDATE posts SET visibility='friends' WHERE id='author-post'`)
	create("reactor", http.StatusForbidden)
	exec(`UPDATE posts SET visibility='public' WHERE id='author-post'`)
	current := create("reactor", http.StatusCreated)
	exec(`UPDATE posting_windows SET ended_at=clock_timestamp()`)
	create("reactor", http.StatusForbidden)
	check(request("reactor", http.MethodGet, path, "", nil), http.StatusForbidden)
	imageCheck("reactor", current, http.StatusNotFound)
	imageCheck("author", current, http.StatusOK)
	exec(`UPDATE posting_windows SET ended_at=NULL`)
	exec(`UPDATE posts SET deleted_at=clock_timestamp() WHERE id='author-post'`)
	create("reactor", http.StatusForbidden)
	imageCheck("author", current, http.StatusNotFound)
	exec(`UPDATE posts SET deleted_at=NULL WHERE id='author-post'`)
	exec(`UPDATE realmojis SET deleted_at=clock_timestamp()`)
	for i := 0; i < 52; i++ {
		id := fmt.Sprintf("page-%02d", i)
		exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region) VALUES($1,$2,$3,'hash','asia')`, id, id, id)
		exec(`INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at) VALUES($1,'author-post',$2,'author-image','like','2026-09-24T00:00:00Z')`, id, id)
	}
	page := list("")
	if len(page.Items) != 50 || !page.HasMore {
		t.Fatalf("first page: %+v", page)
	}
	last := page.Items[49]
	tail := list("?beforeCreatedAt=" + url.QueryEscape(last.CreatedAt.Format(time.RFC3339Nano)) + "&beforeId=" + last.ID)
	if len(tail.Items) != 2 || tail.HasMore || tail.Items[0].ID >= last.ID {
		t.Fatalf("second page: %+v", tail)
	}
	check(request("author", http.MethodGet, path+"?beforeId=x", "", nil), http.StatusBadRequest)
}
