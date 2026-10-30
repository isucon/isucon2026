package scenario

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// relationshipsFrom は app の kind の一覧の応答を、edit で書き換えて返すハンドラー。ほかの
// 要求は app に任せる。
func relationshipsFrom(app *fakeApp, kind string, edit func(req *http.Request, page *client.RelationshipPage)) func(w http.ResponseWriter, req *http.Request) bool {
	return func(w http.ResponseWriter, req *http.Request) bool {
		if req.URL.Path != "/api/relationships/"+kind {
			return false
		}
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, req)
		var page client.RelationshipPage
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			panic(err)
		}
		edit(req, &page)
		_ = json.NewEncoder(w).Encode(page)
		return true
	}
}

// フォロワーの一覧も、bench のフォローと突き合わせる。確実なフォロワーが欠けている、フォロー
// していない人がいる一覧には加点しない。
func TestFollowersListIsChecked(t *testing.T) {
	for _, mode := range []string{"normal", "missing", "stranger"} {
		t.Run(mode, func(t *testing.T) {
			r, app := newTestRunner(t, 2, metrics.DefaultSLO)
			follower, influencer := r.users["asia"][0], r.users["asia"][1]
			app.mu.Lock()
			app.users[influencer.ID].certified = true
			app.mu.Unlock()
			r.certify(influencer)
			r.followCertified(t.Context(), follower, 1)
			if !follower.follows[influencer.ID] {
				t.Fatal("follow failed")
			}
			// フォローが確実に反映されたあとで読む。
			time.Sleep(10 * time.Millisecond)
			rec := startRecording(r)
			routeThrough(t, r, influencer, app, relationshipsFrom(app, "followers", func(_ *http.Request, page *client.RelationshipPage) {
				switch mode {
				case "missing":
					page.Items = nil
				case "stranger":
					page.Items = append(page.Items, client.Relationship{RelationID: "x", User: client.UserSummary{ID: r.users["europe"][0].ID}, Status: "followers", UpdatedAt: fakeRelationTime})
				}
			}))
			before := r.board.Count(score.TagRelationships)
			r.readRelationships(t.Context(), influencer, "followers")
			points := r.board.Count(score.TagRelationships) - before
			failures := rec.Total(0).Failures
			if mode == "normal" {
				if points != 1 || failures != 0 {
					t.Fatalf("points=%d failures=%d errors=%v", points, failures, r.board.Errors())
				}
				return
			}
			if points != 0 || failures != 1 {
				t.Fatalf("points=%d failures=%d errors=%v", points, failures, r.board.Errors())
			}
		})
	}
}

// 友達の一覧の項目は友達関係を表し（status が friends）、関係の ID と更新日時を持たなければ
// ならない。ページをまたいで同じ友達が 2 回返るのも誤り。負荷走行の一覧には加点せず、走行後の
// チェックは失敗にする。
func TestFriendListItemsAreChecked(t *testing.T) {
	for _, mode := range []string{"normal", "status", "relation-id", "updated-at", "duplicate-across-pages"} {
		t.Run(mode, func(t *testing.T) {
			r, app := newTestRunner(t, 2, metrics.DefaultSLO)
			u := r.users["asia"][0]
			time.Sleep(10 * time.Millisecond)
			rec := startRecording(r)
			routeThrough(t, r, u, app, relationshipsFrom(app, "friends", func(req *http.Request, page *client.RelationshipPage) {
				if len(page.Items) != 1 {
					panic(fmt.Sprintf("friends=%+v", page.Items))
				}
				item := &page.Items[0]
				switch mode {
				case "status":
					item.Status = "pending"
				case "relation-id":
					item.RelationID = ""
				case "updated-at":
					item.UpdatedAt = time.Time{}
				case "duplicate-across-pages":
					// 1 ページ目の末尾が 2 ページ目にもまた返る。
					page.HasMore = req.URL.Query().Get("beforeId") == ""
				}
			}))
			before := r.board.Count(score.TagRelationships)
			r.readRelationships(t.Context(), u, "friends")
			points := r.board.Count(score.TagRelationships) - before
			failures := rec.Total(0).Failures
			final := r.finalCheckUser(t.Context(), u.Agent, nil)
			if mode == "normal" {
				if points != 1 || failures != 0 || final != nil {
					t.Fatalf("points=%d failures=%d errors=%v final=%v", points, failures, r.board.Errors(), final)
				}
				return
			}
			if points != 0 || failures != 1 || final == nil {
				t.Fatalf("points=%d failures=%d errors=%v final=%v", points, failures, r.board.Errors(), final)
			}
		})
	}
}
