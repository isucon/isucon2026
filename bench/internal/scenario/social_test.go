package scenario

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// routeThrough は u の要求を app に送り、handle が true を返した要求だけ handle の応答に
// 差し替える。u の操作は負荷走行と同じく集計に数える。
func routeThrough(t *testing.T, r *Runner, u *user, app *fakeApp, handle func(w http.ResponseWriter, req *http.Request) bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !handle(w, req) {
			app.ServeHTTP(w, req)
		}
	}))
	t.Cleanup(server.Close)
	u.API = client.New(server.URL).WithCountry(u.Country()).WithObserver(&accounting{r: r, slo: true, growth: true})
}

// startRecording は、これからの操作を数える集計を用意する。
func startRecording(r *Runner) *metrics.Recorder {
	r.books.Store(newBook(r.tt, time.Now()))
	rec := metrics.New(time.Now(), time.Minute)
	r.recorder.Store(rec)
	return rec
}

// 友達申請の作成の応答に relationId がない、status が pending でも accepted でもなければ、
// 加点せず失敗に数える。
func TestFriendRequestResponseIsValidated(t *testing.T) {
	for _, mode := range []string{"normal", "empty", "invalid-status"} {
		t.Run(mode, func(t *testing.T) {
			r, app := newTestRunner(t, 3, metrics.DefaultSLO)
			rec := startRecording(r)
			actor, target := r.users["asia"][0], r.users["asia"][2]
			routeThrough(t, r, actor, app, func(w http.ResponseWriter, req *http.Request) bool {
				if mode == "normal" || req.Method != http.MethodPost || req.URL.Path != "/api/friend-requests" {
					return false
				}
				w.WriteHeader(http.StatusCreated)
				if mode == "empty" {
					fmt.Fprint(w, `{}`)
				} else {
					fmt.Fprint(w, `{"relationId":"not-persisted","status":"nonsense","updatedAt":"2026-10-28T08:00:00Z"}`)
				}
				return true
			})
			before := r.board.Count(score.TagFriendRequest)
			r.requestFriend(t.Context(), actor, target, false)
			points := r.board.Count(score.TagFriendRequest) - before
			failures := rec.Total(0).Failures
			if mode == "normal" {
				if points != 1 || failures != 0 || r.board.ErrorCount() != 0 {
					t.Fatalf("points=%d failures=%d errors=%v", points, failures, r.board.Errors())
				}
				return
			}
			if points != 0 || failures != 1 {
				t.Fatalf("malformed response: points=%d failures=%d errors=%v", points, failures, r.board.Errors())
			}
		})
	}
}

// 友達申請の承認・拒否・取り消しの応答が、処理した申請の ID と操作に応じた状態でなければ、
// 加点せず失敗に数える。
func TestFriendTransitionResponseIsValidated(t *testing.T) {
	for _, action := range []string{"accept", "reject", "cancel"} {
		for _, mode := range []string{"normal", "empty", "wrong-status", "wrong-id"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				r, app := newTestRunner(t, 3, metrics.DefaultSLO)
				sender, recipient := r.users["asia"][0], r.users["asia"][2]
				r.requestFriend(t.Context(), sender, recipient, false)
				incoming, err := recipient.API.Relationships(t.Context(), recipient.Token, "incoming-requests", nil)
				if err != nil || len(incoming.Items) != 1 {
					t.Fatalf("incoming=%+v err=%v", incoming, err)
				}
				id := incoming.Items[0].RelationID
				operator := recipient
				if action == "cancel" {
					operator = sender
				}
				rec := startRecording(r)
				routeThrough(t, r, operator, app, func(w http.ResponseWriter, req *http.Request) bool {
					if mode == "normal" || req.Method != http.MethodPost || !strings.HasPrefix(req.URL.Path, "/api/friend-requests/") {
						return false
					}
					switch mode {
					case "empty":
						fmt.Fprint(w, `{}`)
					case "wrong-status":
						fmt.Fprintf(w, `{"relationId":%q,"status":"pending"}`, id)
					default:
						fmt.Fprintf(w, `{"relationId":"other","status":%q}`, map[string]string{"accept": "accepted", "reject": "rejected", "cancel": "cancelled"}[action])
					}
					return true
				})
				tag := score.TagFriendDecline
				if action == "accept" {
					tag = score.TagFriendAccept
				}
				before := r.board.Count(tag)
				switch action {
				case "accept":
					r.acceptRequests(t.Context(), operator)
				case "reject":
					r.declineRequest(t.Context(), operator)
				case "cancel":
					r.cancelRequest(t.Context(), operator)
				}
				points := r.board.Count(tag) - before
				failures := rec.Total(0).Failures
				if mode == "normal" {
					if points != 1 || failures != 0 || r.board.ErrorCount() != 0 {
						t.Fatalf("points=%d failures=%d errors=%v", points, failures, r.board.Errors())
					}
					return
				}
				if points != 0 || failures != 1 {
					t.Fatalf("malformed response: points=%d failures=%d errors=%v", points, failures, r.board.Errors())
				}
				if action == "accept" && slices.Contains(r.model.FriendsOf(sender.ID, r.model.Now()), recipient.ID) {
					t.Fatal("the model counts a friendship from a malformed response as certain")
				}
			})
		}
	}
}
