package isureal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

func TestRelationshipIntegration(t *testing.T) {
	dsn := os.Getenv("ISUREAL_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_SESSION_TEST_DSN is unset")
	}
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("../sql/001-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(string(schema))
	tokens := map[string]string{}
	for _, id := range []string{"a", "b", "c"} {
		exec(`INSERT INTO users (id, username, secret_key_hash, display_name, bio, region) VALUES ($1,$2,'hash',$3,'bio','asia')`, id, id, id)
		session, err := CreateLoginSession(db, id)
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = session.Token
	}
	e := echo.New()
	e.HTTPErrorHandler = APIErrorHandler
	h := &HandlerContext{DB: db}
	e.POST("/api/friend-requests", createFriendRequestHandler(h), SessionMiddleware(h, true))
	e.POST("/api/friend-requests/:requestId/:action", transitionFriendRequestHandler(h), SessionMiddleware(h, true))
	e.GET("/api/relationships/:kind", getRelationshipsHandler(h), SessionMiddleware(h, true))
	e.DELETE("/api/friendships/:userId", deleteFriendshipHandler(h), SessionMiddleware(h, true))
	e.PUT("/api/follows/:userId", followHandler(h), SessionMiddleware(h, true))
	e.DELETE("/api/follows/:userId", unfollowHandler(h), SessionMiddleware(h, true))
	e.PUT("/api/blocks/:userId", blockHandler(h), SessionMiddleware(h, true))
	e.DELETE("/api/blocks/:userId", unblockHandler(h), SessionMiddleware(h, true))
	request := func(actor, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
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
	apply := func(actor, method, path string, status int) {
		t.Helper()
		check(request(actor, method, path, ""), status)
	}
	create := func(actor, target string, status int) createFriendRequestResponse {
		t.Helper()
		rec := request(actor, http.MethodPost, "/api/friend-requests", fmt.Sprintf(`{"recipientId":%q}`, target))
		check(rec, status)
		var response createFriendRequestResponse
		if status == http.StatusCreated {
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return response
	}
	list := func(actor, kind string) getRelationshipsResponse {
		t.Helper()
		rec := request(actor, http.MethodGet, "/api/relationships/"+kind, "")
		check(rec, http.StatusOK)
		var response getRelationshipsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Items == nil {
			t.Fatal("null items")
		}
		return response
	}
	create("", "b", http.StatusUnauthorized)
	create("a", "a", http.StatusBadRequest)
	create("a", "missing", http.StatusNotFound)
	first := create("a", "b", http.StatusCreated)
	if first.Status != "pending" {
		t.Fatal(first.Status)
	}
	incoming := list("b", "incoming-requests")
	if len(incoming.Items) != 1 || incoming.Items[0].User.ID != "a" || incoming.Items[0].User.Bio != "" {
		t.Fatalf("incoming=%+v", incoming)
	}
	if len(list("a", "outgoing-requests").Items) != 1 {
		t.Fatal("missing outgoing request")
	}
	create("a", "b", http.StatusConflict)
	path := "/api/friend-requests/" + first.RelationID
	apply("a", http.MethodPost, path+"/accept", http.StatusForbidden)
	apply("c", http.MethodPost, path+"/reject", http.StatusForbidden)
	apply("b", http.MethodPost, path+"/cancel", http.StatusForbidden)
	apply("b", http.MethodPost, path+"/reject", http.StatusOK)
	apply("b", http.MethodPost, path+"/accept", http.StatusForbidden)
	first = create("a", "b", http.StatusCreated)
	apply("a", http.MethodPost, "/api/friend-requests/"+first.RelationID+"/cancel", http.StatusOK)
	first = create("a", "b", http.StatusCreated)
	path = "/api/friend-requests/" + first.RelationID
	exec(`CREATE FUNCTION reject_friendship() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject friendship'; END $$`)
	exec(`CREATE TRIGGER reject_friendship BEFORE INSERT ON friendships FOR EACH ROW EXECUTE FUNCTION reject_friendship()`)
	apply("b", http.MethodPost, path+"/accept", http.StatusInternalServerError)
	exec(`DROP FUNCTION reject_friendship() CASCADE`)
	if len(list("b", "incoming-requests").Items) != 1 {
		t.Fatal("failed approval consumed request")
	}
	apply("b", http.MethodPost, path+"/accept", http.StatusOK)
	friends := list("a", "friends")
	if len(friends.Items) != 1 || friends.Items[0].User.ID != "b" || friends.Items[0].User.Bio != "bio" {
		t.Fatalf("friends=%+v", friends)
	}
	if len(list("b", "incoming-requests").Items) != 0 {
		t.Fatal("accepted request still pending")
	}
	create("a", "b", http.StatusConflict)
	apply("a", http.MethodDelete, "/api/friendships/b", http.StatusNoContent)
	apply("a", http.MethodDelete, "/api/friendships/b", http.StatusConflict)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, actor := range []string{"a", "b"} {
		wg.Go(func() {
			target := "a"
			if actor == "a" {
				target = "b"
			}
			results <- request(actor, http.MethodPost, "/api/friend-requests", fmt.Sprintf(`{"recipientId":%q}`, target))
		})
	}
	wg.Wait()
	close(results)
	statuses := map[string]int{}
	for rec := range results {
		check(rec, http.StatusCreated)
		var response createFriendRequestResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		statuses[response.Status]++
	}
	if statuses["pending"] != 1 || statuses["accepted"] != 1 || len(list("a", "friends").Items) != 1 {
		t.Fatalf("reverse requests: %v", statuses)
	}
	apply("a", http.MethodPut, "/api/follows/b", http.StatusConflict)
	exec(`UPDATE users SET account_type='certified_public' WHERE id IN ('a','b')`)
	apply("a", http.MethodPut, "/api/follows/b", http.StatusNoContent)
	apply("a", http.MethodPut, "/api/follows/b", http.StatusConflict)
	apply("b", http.MethodPut, "/api/follows/a", http.StatusNoContent)
	if len(list("a", "following").Items) != 1 || len(list("b", "followers").Items) != 1 {
		t.Fatal("missing follow")
	}
	apply("a", http.MethodDelete, "/api/follows/b", http.StatusNoContent)
	apply("a", http.MethodDelete, "/api/follows/b", http.StatusConflict)
	apply("a", http.MethodPut, "/api/follows/b", http.StatusNoContent)
	apply("a", http.MethodPut, "/api/blocks/b", http.StatusNoContent)
	if len(list("a", "friends").Items) != 0 || len(list("a", "following").Items) != 0 || len(list("b", "following").Items) != 0 {
		t.Fatal("block did not remove relationships")
	}
	create("b", "a", http.StatusConflict)
	apply("b", http.MethodPut, "/api/follows/a", http.StatusConflict)
	apply("a", http.MethodPut, "/api/blocks/b", http.StatusConflict)
	apply("b", http.MethodDelete, "/api/blocks/a", http.StatusForbidden)
	apply("a", http.MethodDelete, "/api/blocks/b", http.StatusNoContent)
	if len(list("a", "friends").Items) != 0 {
		t.Fatal("unblock restored friendship")
	}
	first = create("a", "b", http.StatusCreated)
	apply("b", http.MethodPut, "/api/blocks/a", http.StatusNoContent)
	var status string
	if err := db.Get(&status, `SELECT status FROM friend_requests WHERE id=$1`, first.RelationID); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatal("block did not cancel request")
	}
	apply("b", http.MethodPost, "/api/friend-requests/"+first.RelationID+"/accept", http.StatusForbidden)
	apply("b", http.MethodDelete, "/api/blocks/a", http.StatusNoContent)
	first = create("a", "b", http.StatusCreated)
	raceResults := make(chan *httptest.ResponseRecorder, 2)
	wg.Go(func() {
		raceResults <- request("b", http.MethodPost, "/api/friend-requests/"+first.RelationID+"/accept", "")
	})
	wg.Go(func() { raceResults <- request("a", http.MethodPut, "/api/blocks/b", "") })
	wg.Wait()
	close(raceResults)
	for rec := range raceResults {
		if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent && rec.Code != http.StatusForbidden {
			t.Fatalf("race response=%d %s", rec.Code, rec.Body.String())
		}
	}
	if len(list("a", "friends").Items) != 0 {
		t.Fatal("friendship survived block race")
	}
	for i := range 52 {
		id := fmt.Sprintf("friend-%02d", i)
		exec(`INSERT INTO users (id, username, secret_key_hash, display_name, region) VALUES ($1,$2,'hash',$3,'asia')`, id, id, id)
		exec(`INSERT INTO friendships (id, user_low_id, user_high_id, updated_at) VALUES ($1,'c',$2,'2026-09-23T00:00:00Z')`, uuid.NewString(), id)
	}
	page := list("c", "friends")
	if len(page.Items) != 50 || !page.HasMore {
		t.Fatal("invalid first page")
	}
	last := page.Items[49]
	next := list("c", "friends?"+url.Values{"beforeUpdatedAt": {last.UpdatedAt.Format(time.RFC3339Nano)}, "beforeId": {last.RelationID}}.Encode())
	if len(next.Items) != 2 || next.HasMore {
		t.Fatal("invalid second page")
	}
	seen := map[string]bool{}
	for _, item := range append(page.Items, next.Items...) {
		if seen[item.RelationID] {
			t.Fatal("duplicate relation")
		}
		seen[item.RelationID] = true
	}
	apply("a", http.MethodGet, "/api/relationships/unknown", http.StatusBadRequest)
	apply("a", http.MethodGet, "/api/relationships/friends?beforeId=x", http.StatusBadRequest)
}
