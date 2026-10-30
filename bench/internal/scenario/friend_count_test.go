package scenario

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/dataset"
)

func (r *Runner) friendCountOf(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.friendCount[id]
}

// 友達申請の相手を選ぶ重み（友達の数）は、友達の解除と退会で減る。
func TestFriendCountFollowsUnfriendAndDeletion(t *testing.T) {
	// asia の 1 人目は 0 人目と 2 人目の友達。
	r, _ := newTestRunner(t, 3, metrics.DefaultSLO)
	u := r.users["asia"][1]
	if got := r.friendCountOf(u.ID); got != 2 {
		t.Fatalf("before: %d", got)
	}
	r.unfriend(t.Context(), u)
	if got := r.friendCountOf(u.ID); got != 1 || r.board.ErrorCount() != 0 {
		t.Fatalf("after unfriend: %d errors=%v", got, r.board.Errors())
	}
	left := 0
	for _, id := range []string{r.users["asia"][0].ID, r.users["asia"][2].ID} {
		left += r.friendCountOf(id)
	}
	if left != 1 {
		t.Fatalf("the unfriended side still counts the friendship: %d", left)
	}
	r.deleteAccount(t.Context(), u)
	if got := r.friendCountOf(r.users["asia"][0].ID) + r.friendCountOf(r.users["asia"][2].ID); got != 0 {
		t.Fatalf("friends of a deleted user: %d", got)
	}
}

// cohort のユーザーの重みは、初期データの友達の数から始まる。
func TestCohortFriendCountFromDataset(t *testing.T) {
	// tinyDataset には cohort のユーザーがいないので、少し大きな初期データを使う。
	cfg := dataset.Small()
	cfg.Users, cfg.Days = 200, 1
	r, err := NewRunner(Config{Users: 10, Duration: time.Second, Dataset: dataset.Generate(cfg), Seed: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	app := newFakeApp(t)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	r.api = client.New(server.URL)
	for _, region := range geo.Regions {
		for _, du := range r.initial.CohortUsers(region) {
			app.addUser(du.ID, du.Username, du.SecretKey, du.Region)
		}
	}
	if reason := r.loginCohort(t.Context()); reason != "" {
		t.Fatal(reason)
	}
	now := r.model.Now()
	withFriends := 0
	for _, u := range r.usersSnapshot() {
		if !u.cohort {
			continue
		}
		want := 0
		for _, id := range r.model.FriendsOf(u.ID, now) {
			if r.model.Visible(u.ID, id, now) == model.True {
				want++
			}
		}
		if got := r.friendCountOf(u.ID); got != want {
			t.Errorf("%s: friendCount=%d, want %d", u.Username, got, want)
		}
		if want > 0 {
			withFriends++
		}
	}
	if withFriends == 0 {
		t.Fatal("no cohort user has initial friends")
	}
}
