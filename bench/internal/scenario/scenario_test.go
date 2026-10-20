package scenario

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/dataset"
)

// tinyDataset は初期データを使わないテストのための小さな初期データ。
var tinyDataset = func() *dataset.World {
	cfg := dataset.Small()
	cfg.Users, cfg.Days = 20, 1
	return dataset.Generate(cfg)
}()

type fakeUser struct{ id, username, secret, region string }

type fakePost struct {
	client.Post
	date    string
	deleted bool
}

type fakeWindow struct{ id, date string }

type fakeRequest struct {
	from, to string
	accepted bool
}

// fakeApp はアプリの閲覧可否とフィードの規則を、bench のモデルとは別に実装した偽のアプリ。
// トークンはユーザー ID。フィールドで障害や不具合を注入する。
type fakeApp struct {
	t     *testing.T
	image []byte

	mu       sync.Mutex
	users    map[string]fakeUser
	windows  map[string]fakeWindow // リージョン → 開いている投稿期間
	opened   map[string]int        // リージョン → これまでに開いた投稿期間の数
	posts    []*fakePost
	friends  map[[2]string]bool
	blocks   map[[2]string]bool
	requests map[string]*fakeRequest
	follows  map[[2]string]bool // フォローする人とされる人
	nextID   int

	postStatus   int                                  // 0 以外なら POST /api/posts をこのステータスにする
	extraFeed    []client.Post                        // すべてのフィードのページに追加する
	ignoreBlocks bool                                 // ブロックを無視する不具合
	pageSize     int                                  // フィードの 1 ページの件数（0 なら 50）
	keepDeleted  bool                                 // 削除した投稿を Memories に残す不具合
	losePosts    bool                                 // 投稿を Memories から失う不具合
	dropFeeds    bool                                 // ゲート通過のあとのフィードを空で返す不具合
	notify       func(region, windowID string) string // 通知の中身
}

func newFakeApp(t *testing.T, image []byte) *fakeApp {
	app := &fakeApp{
		t: t, image: image, users: map[string]fakeUser{}, windows: map[string]fakeWindow{}, opened: map[string]int{},
		friends: map[[2]string]bool{}, blocks: map[[2]string]bool{}, requests: map[string]*fakeRequest{}, follows: map[[2]string]bool{},
	}
	app.notify = func(region, windowID string) string {
		return fmt.Sprintf(`{"id":"n-%s","postingWindowId":%q,"region":%q}`, region, windowID, region)
	}
	return app
}

func (a *fakeApp) postCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.posts)
}

func key(a, b string) [2]string { return [2]string{min(a, b), max(a, b)} }

func (a *fakeApp) postedOn(viewer fakeUser, date string) bool {
	return slices.ContainsFunc(a.posts, func(p *fakePost) bool { return p.Author.ID == viewer.id && p.date == date && !p.deleted })
}

// canView はアプリの canView と同じ規則。
func (a *fakeApp) canView(viewer fakeUser, p *fakePost) bool {
	author := a.users[p.Author.ID]
	if p.deleted {
		return false
	}
	if viewer.id == author.id {
		return true
	}
	current, ok := a.windows[author.region]
	if !ok || current.id != p.PostingWindowID {
		return false
	}
	own, ok := a.windows[viewer.region]
	if !ok || !a.postedOn(viewer, own.date) || (a.blocks[key(viewer.id, author.id)] && !a.ignoreBlocks) {
		return false
	}
	switch p.Visibility {
	case "public":
		return true
	case "friends":
		return a.friends[key(viewer.id, author.id)]
	default:
		return a.friends[key(viewer.id, author.id)] || a.commonFriend(viewer.id, author.id)
	}
}

func (a *fakeApp) commonFriend(x, y string) bool {
	for id := range a.users {
		if id != x && id != y && a.friends[key(x, id)] && a.friends[key(id, y)] {
			return true
		}
	}
	return false
}

func (a *fakeApp) inFeed(viewer fakeUser, p *fakePost, feed string) bool {
	if p.Author.ID == viewer.id || !a.canView(viewer, p) {
		return false
	}
	friend := a.friends[key(viewer.id, p.Author.ID)]
	switch feed {
	case "public":
		return p.Visibility == "public"
	case "friends":
		return friend
	case "friends-of-friends":
		return !friend && a.commonFriend(viewer.id, p.Author.ID)
	case "following":
		return a.follows[[2]string{viewer.id, p.Author.ID}]
	default:
		return false
	}
}

func (a *fakeApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	region := geo.RegionOf(r.Header.Get("X-Country-Code"))
	path := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	respond := func(status int, body any) {
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}
	if path[0] == "notifications" {
		// 購読した時点より後に開いた投稿期間を 1 件だけ通知する。
		a.mu.Lock()
		subscribed := a.opened[region]
		a.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		for {
			a.mu.Lock()
			newer := a.opened[region] > subscribed
			a.mu.Unlock()
			if newer {
				break
			}
			select {
			case <-time.After(5 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		a.mu.Lock()
		payload := a.notify(region, a.windows[region].id)
		a.mu.Unlock()
		if payload != "" {
			fmt.Fprintf(w, "data: %s\n\n", payload)
		}
		return
	}
	if path[0] == "posts" && len(path) == 1 && r.Method == http.MethodPost && a.postStatus != 0 {
		respond(a.postStatus, nil)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	viewer := a.users[token]
	if token != "" && token != "admin" && viewer.region != region {
		a.t.Errorf("user %s sent a country of %s, want %s", token, region, viewer.region)
	}
	switch {
	case path[0] == "auth" && path[1] == "register":
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		id := "u-" + input["username"]
		a.users[id] = fakeUser{id: id, username: input["username"], secret: "secret-" + id, region: input["region"]}
		respond(http.StatusCreated, map[string]any{"secretKey": "secret-" + id, "user": map[string]string{"id": id, "username": input["username"]}})
	case path[0] == "auth" && path[1] == "login":
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		id := "u-" + input["username"]
		if a.users[id].secret != input["secretKey"] {
			respond(http.StatusUnauthorized, nil)
			return
		}
		respond(http.StatusOK, map[string]any{"token": id, "user": map[string]string{"id": id, "username": input["username"]}})
	case path[0] == "follows":
		a.follows[[2]string{viewer.id, path[1]}] = true
		respond(http.StatusNoContent, nil)
	case path[0] == "relationships" && path[1] == "friends":
		items := []client.Relationship{}
		for pair := range a.friends {
			for i, id := range pair {
				if id == viewer.id {
					items = append(items, client.Relationship{RelationID: pair[0] + pair[1], User: client.UserSummary{ID: pair[1-i]}, Status: "friends"})
				}
			}
		}
		respond(http.StatusOK, map[string]any{"items": items, "hasMore": false})
	case path[0] == "relationships":
		items := []client.Relationship{}
		for id, request := range a.requests {
			if request.to == viewer.id && !request.accepted {
				items = append(items, client.Relationship{RelationID: id, User: client.UserSummary{ID: request.from}, Status: "pending"})
			}
		}
		respond(http.StatusOK, map[string]any{"items": items, "hasMore": false})
	case path[0] == "admin":
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		a.nextID++
		a.windows[input["region"]] = fakeWindow{id: fmt.Sprintf("w%d", a.nextID), date: input["localDate"]}
		a.opened[input["region"]]++
		respond(http.StatusCreated, map[string]string{"id": a.windows[input["region"]].id, "region": input["region"], "localDate": input["localDate"]})
	case path[0] == "posting-window":
		respond(http.StatusOK, map[string]any{"window": map[string]string{"id": a.windows[region].id}, "canPost": true, "hasPosted": false})
	case path[0] == "posts" && len(path) == 1:
		a.nextID++
		id := fmt.Sprintf("p%d", a.nextID)
		exp := time.Now().Add(time.Hour).Unix()
		p := &fakePost{date: a.windows[region].date}
		p.Post = client.Post{
			ID: id, Author: client.UserSummary{ID: viewer.id, Username: viewer.username}, PostingWindowID: a.windows[region].id,
			ImageURL: fmt.Sprintf("/api/images/img-%s?exp=%d&sig=%s", id, exp, imageurl.Sign(viewer.secret, "img-"+id, exp)),
			Caption:  r.FormValue("caption"), Visibility: r.FormValue("visibility"), PostedAt: time.Now().Truncate(time.Microsecond),
		}
		a.posts = append(a.posts, p)
		respond(http.StatusCreated, p.Post)
	case path[0] == "posts" && len(path) == 2:
		i := slices.IndexFunc(a.posts, func(p *fakePost) bool { return p.ID == path[1] })
		if r.Method == http.MethodDelete {
			a.posts[i].deleted = true
			respond(http.StatusNoContent, nil)
			return
		}
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		a.posts[i].Visibility = input["visibility"]
		respond(http.StatusOK, a.posts[i].Post)
	case path[0] == "posts" && len(path) == 3:
		p := a.posts[slices.IndexFunc(a.posts, func(p *fakePost) bool { return p.ID == path[1] })]
		allowed := a.canView(viewer, p) && p.Author.ID != viewer.id
		if path[2] == "comments" {
			allowed = a.canView(viewer, p) && (p.Visibility == "public" || a.friends[key(viewer.id, p.Author.ID)])
		}
		if !allowed {
			respond(http.StatusForbidden, map[string]string{"error": "interaction_forbidden"})
			return
		}
		respond(http.StatusCreated, nil)
	case path[0] == "images":
		_, _ = w.Write(a.image)
	case path[0] == "memories":
		items := []client.MemoryPost{}
		for _, p := range slices.Backward(a.posts) {
			if a.losePosts {
				break
			}
			if p.Author.ID == viewer.id && (!p.deleted || a.keepDeleted) && strings.HasPrefix(p.date, r.URL.Query().Get("month")) {
				items = append(items, client.MemoryPost{Post: p.Post})
			}
		}
		respond(http.StatusOK, map[string]any{"items": items, "hasMore": false})
	case path[0] == "feeds" && a.dropFeeds && path[1] != "friends":
		respond(http.StatusOK, map[string]any{"items": []client.Post{}, "hasMore": false})
	case path[0] == "feeds":
		size := cmp.Or(a.pageSize, 50)
		var before time.Time
		if value := r.URL.Query().Get("beforePostedAt"); value != "" {
			before, _ = time.Parse(time.RFC3339Nano, value)
		}
		items := []client.Post{}
		for _, p := range slices.Backward(a.posts) {
			if (before.IsZero() || p.PostedAt.Before(before)) && a.inFeed(viewer, p, path[1]) {
				items = append(items, p.Post)
			}
		}
		hasMore := len(items) > size
		if hasMore {
			items = items[:size]
		}
		respond(http.StatusOK, map[string]any{"items": append(items, a.extraFeed...), "hasMore": hasMore})
	case path[0] == "friend-requests" && len(path) == 1:
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		a.nextID++
		id := fmt.Sprintf("fr%d", a.nextID)
		a.requests[id] = &fakeRequest{from: viewer.id, to: input["recipientId"]}
		respond(http.StatusCreated, map[string]string{"relationId": id, "status": "pending"})
	case path[0] == "friend-requests":
		request := a.requests[path[1]]
		request.accepted = true
		a.friends[key(request.from, request.to)] = true
		respond(http.StatusOK, map[string]string{"relationId": path[1], "status": "accepted"})
	case path[0] == "blocks":
		a.blocks[key(viewer.id, path[1])] = true
		delete(a.friends, key(viewer.id, path[1]))
		respond(http.StatusNoContent, nil)
	default:
		a.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		respond(http.StatusNotFound, nil)
	}
}

// testRatchet は短い観測窓で昇格しやすくしたラチェット。
// testRatchet は負荷走行のテストのラチェット。話題になると 1 日（testSchedule の 250 ms）ごとに
// 人数が倍になり、bench が速いほど数千人に増えて接続を使い切るので、話題にはしない（話題は
// TestMaybeBuzz で確かめる）。口コミの流入は残し、途中から参加するユーザーも通す。
var testRatchet = RatchetConfig{
	Window: 100 * time.Millisecond, MinOps: 1, MaxFailRate: 0.01, MaxReadP99: time.Second, MaxOpP99: 3 * time.Second, Max503Rate: 0.05,
	Step: 0.1, BuzzHeadroom: 0.5, BuzzMinOps: math.MaxInt64, BuzzMin: 2, BuzzSpill: 0.05,
}

// TestMaybeBuzz は、1 日の始まりに直前の観測窓の余裕があれば話題になり、現在の人数と同じ数
// （BuzzMin 人以上）が流入し、ほかのリージョンにもそれぞれの人数の BuzzSpill 倍が流入することを
// 確かめる。
func TestMaybeBuzz(t *testing.T) {
	runner, _ := newTestRunner(t, 1, metrics.DefaultSLO)
	runner.ratchetConfig = DefaultRatchet
	joined := map[string]int{}
	buzzed := map[string]bool{}
	runner.join = func(region string, _, users int, buzz bool) {
		joined[region] += users
		buzzed[region] = buzzed[region] || buzz
	}
	runner.ratchet.users = map[string]int{geo.Asia: 300, geo.Europe: 1, geo.America: 30}
	runner.loadStart = time.Now()
	runner.ratchet.roomy[geo.America] = true
	runner.maybeBuzz(geo.America) // america はまだ話題になれない（20 秒から）
	runner.loadStart = time.Now().Add(-time.Minute)
	runner.ratchet.roomy[geo.America] = false

	runner.maybeBuzz(geo.Europe) // 余裕がない
	if len(joined) != 0 {
		t.Fatalf("buzz without room: %v", joined)
	}
	runner.ratchet.roomy[geo.Asia] = true
	runner.maybeBuzz(geo.Asia)
	runner.maybeBuzz(geo.Asia)                                           // 同じ観測窓の余裕では 2 回話題にならない
	want := map[string]int{geo.Asia: 300, geo.Europe: 1, geo.America: 2} // 1 人と 30 人の 5% を切り上げ
	if !maps.Equal(joined, want) || !buzzed[geo.Asia] || buzzed[geo.Europe] || runner.ratchet.buzzes[geo.Asia] != 1 {
		t.Fatalf("joined=%v buzzed=%v, want %v (only asia buzzed)", joined, buzzed, want)
	}

	clear(joined)
	runner.ratchet.roomy[geo.Europe] = true
	runner.maybeBuzz(geo.Europe) // 人数が少なければ BuzzMin 人
	if joined[geo.Europe] != 200 {
		t.Fatalf("europe joined %d, want 200", joined[geo.Europe])
	}
}

// TestRatchetRoomy は、Buzz の余裕の判定を確かめる。
func TestRatchetRoomy(t *testing.T) {
	c := DefaultRatchet
	for _, tc := range []struct {
		ops        int64
		read, op   time.Duration
		roomy      bool
		reasonText string
	}{
		{1000, 400 * time.Millisecond, time.Second, true, "余裕がある"},
		{50, 400 * time.Millisecond, time.Second, false, "操作が少ない"},
		{1000, 600 * time.Millisecond, time.Second, false, "読み取りが上限の半分を超える"},
		{1000, 400 * time.Millisecond, 2 * time.Second, false, "書き込みを含む p99 が上限の半分を超える"},
	} {
		s := metrics.Stats{Ops: tc.ops, Reads: int(tc.ops), ReadP99: tc.read, OpP99: tc.op}
		if got := c.roomy(s); got != tc.roomy {
			t.Errorf("%s: roomy=%t", tc.reasonText, got)
		}
	}
}

func TestRatchetGrowth(t *testing.T) {
	c := DefaultRatchet
	for _, tc := range []struct{ users, want int }{
		{users: 100, want: 10},
		{users: 3, want: 1}, // 最低 1 人
	} {
		if got := c.growth(tc.users); got != tc.want {
			t.Errorf("growth(%d) = %d, want %d", tc.users, got, tc.want)
		}
	}
}

// testSchedule は 1 秒の走行で各リージョンが 4 回ずつ投稿期間を開く時間割。
var testSchedule = schedule{warmup: 20 * time.Millisecond, period: 250 * time.Millisecond, offset: 80 * time.Millisecond}

func newTestRunner(t *testing.T, usersPerRegion int, slo metrics.SLO) (*Runner, *fakeApp) {
	t.Helper()
	runner, err := NewRunner(Config{
		Users:    usersPerRegion,
		Duration: time.Second,
		SLO:      slo,
		Retry:    client.RetryPolicy{MaxRetries: 1, Base: time.Millisecond, MaxWait: time.Millisecond},
		Ratchet:  testRatchet,
		// 偽のアプリとモデルの食い違いを見逃さないよう、テストではすべて検証する。
		VerifyRate: 1,
		Dataset:    tinyDataset,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.deleteRate, runner.changeRate = 0, 0
	// 偽のアプリは初期データのユーザーを知らないので、思い出を見返すユーザーは置かない。
	runner.returningRatio = 0
	runner.schedule = testSchedule
	app := newFakeApp(t, runner.postImage)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	runner.api = client.NewClient(client.Config{Base: server.URL, Retry: runner.cfg.Retry})
	runner.admin = runner.api.WithCountry("JP")
	runner.adminToken = "admin"
	runner.firstDate = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	var users []*agent.Agent
	for _, region := range geo.Regions {
		for index := range usersPerRegion {
			user := agent.New(runner.api, region, index)
			user.ID = fmt.Sprintf("%s-%d", region, index)
			user.Username, user.Token, user.SecretKey = user.ID, user.ID, "secret-"+user.ID
			app.users[user.ID] = fakeUser{id: user.ID, username: user.Username, secret: user.SecretKey, region: region}
			runner.model.AddUser(model.User{ID: user.ID, Username: user.Username, SecretKey: user.SecretKey, Region: region})
			runner.agents[region] = append(runner.agents[region], user)
			users = append(users, user)
		}
	}
	if reason := runner.connect(t.Context(), users); reason != "" {
		t.Fatal(reason)
	}
	return runner, app
}

func TestLoadAllRegions(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.01, MinOps: 1})
	if reason := runner.load(t.Context()); reason != "" {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	if runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("errors=%v critical=%v", runner.board.Errors(), runner.board.Critical())
	}
	// 各リージョンが時間割どおりに投稿期間を開き（最後の 1 回は走行の終わりに間に合わない
	// こともある）、全員が少なくとも 1 回はゲートを通過する。
	if opened := runner.openedCounts(); opened[geo.Asia] < 3 || opened[geo.Europe] < 3 || opened[geo.America] < 3 {
		t.Fatalf("opened=%v", opened)
	}
	if got := runner.board.Count(score.TagGatePass); got < 6 {
		t.Fatalf("gate passes=%d", got)
	}
	if runner.board.Count(score.TagFeed) == 0 || runner.board.Count(score.TagMemory) == 0 {
		t.Fatalf("breakdown=%v", runner.board.Breakdown())
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if len(app.friends) < 5 || len(app.blocks) != 1 {
		t.Fatalf("friends=%d blocks=%d", len(app.friends), len(app.blocks))
	}
	app.mu.Unlock()
	if reason := runner.finalCheck(t.Context()); reason != "" {
		t.Fatalf("final check: %s", reason)
	}
	app.mu.Lock()
}

func TestFinalCheckFindsInconsistencies(t *testing.T) {
	for name, tc := range map[string]struct {
		breakApp func(*fakeApp)
		want     string
	}{
		"deleted post kept":  {func(a *fakeApp) { a.keepDeleted = true }, "削除した投稿"},
		"accepted post lost": {func(a *fakeApp) { a.losePosts = true }, "受け付けられた投稿"},
	} {
		t.Run(name, func(t *testing.T) {
			runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
			runner.deleteRate = 1 // 全員が投稿を削除する
			if name == "accepted post lost" {
				runner.deleteRate = 0
			}
			runner.load(t.Context())
			app.mu.Lock()
			tc.breakApp(app)
			app.mu.Unlock()
			if reason := runner.finalCheck(t.Context()); !strings.Contains(reason, tc.want) {
				t.Fatalf("reason=%q", reason)
			}
		})
	}
}

// TestRatchetAddsUsers は、健全なリージョンが昇格し、流入したユーザーが登録して投稿し、
// 友達申請が承認されることを確かめる。
func TestRatchetAddsUsers(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.01, MinOps: 1})
	runner.acceptRate = 0.5
	if reason := runner.load(t.Context()); reason != "" || runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	levels := runner.ratchet.levels()
	if levels[geo.Asia] == 0 || levels[geo.Europe] == 0 {
		t.Fatalf("levels=%v", levels)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	newcomerPosts := 0
	for _, p := range app.posts {
		if strings.Contains(p.Author.ID, "_n") {
			newcomerPosts++
		}
	}
	accepted := 0
	for _, request := range app.requests {
		if request.accepted {
			accepted++
		}
	}
	if newcomerPosts == 0 || accepted == 0 || len(app.follows) == 0 {
		t.Fatalf("newcomer posts=%d accepted requests=%d follows=%d", newcomerPosts, accepted, len(app.follows))
	}
}

// TestModelAgreesWithApp は、削除と公開範囲の変更を混ぜて何度も投稿期間をまたいでも、
// モデルが偽のアプリ（別の実装）と食い違わないことを確かめる。
func TestModelAgreesWithApp(t *testing.T) {
	runner, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	app.pageSize = 2 // ページ送りも通す
	runner.cfg.Duration = 2 * time.Second
	runner.deleteRate, runner.changeRate = 0.2, 0.3
	if reason := runner.load(t.Context()); reason != "" || runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	if runner.board.Count(score.TagGatePass) == 0 {
		t.Fatal("no gate passes")
	}
}

func TestIgnoredBlockIsCritical(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.visibility = func() string { return "public" }
	// ブロックした人がブロックされた人の投稿を読む機会が確実にあるよう、長めに走らせる。
	// ユーザーが増えると投稿が最初のページからあふれるので、ラチェットは止める。
	runner.cfg.Duration = 2 * time.Second
	runner.ratchetConfig.MinOps = math.MaxInt64
	app.ignoreBlocks = true
	reason := runner.load(t.Context())
	critical := runner.board.Critical()
	if len(critical) == 0 || !strings.Contains(critical[0], "見えてはいけない") {
		t.Fatalf("reason=%q critical=%v", reason, critical)
	}
}

func TestUnknownFeedPostIsCritical(t *testing.T) {
	runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	app.extraFeed = []client.Post{{ID: "not-created-by-bench", Author: client.UserSummary{ID: "stranger"}}}
	runner.load(t.Context())
	critical := runner.board.Critical()
	if len(critical) == 0 || !strings.Contains(critical[0], "存在しない投稿") {
		t.Fatalf("critical=%v", critical)
	}
}

func TestNotificationFaults(t *testing.T) {
	t.Run("missing notification skips posting", func(t *testing.T) {
		runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		// Buzz で流入したユーザーは通知を待たずに投稿するが、testRatchet では話題にならない。
		app.notify = func(string, string) string { return "" }
		runner.load(t.Context())
		if runner.board.ErrorCount() != 0 || app.postCount() != 0 {
			t.Fatalf("errors=%v posts=%d", runner.board.Errors(), app.postCount())
		}
	})
	t.Run("unknown window is an error", func(t *testing.T) {
		runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		app.notify = func(region, _ string) string {
			return fmt.Sprintf(`{"id":"n1","postingWindowId":"wrong","region":%q}`, region)
		}
		runner.load(t.Context())
		errors := runner.board.Errors()
		if len(errors) < 3 || !strings.Contains(errors[0], "開いていない投稿期間") || app.postCount() != 0 {
			t.Fatalf("errors=%v posts=%d", errors, app.postCount())
		}
	})
}

func TestLoadAbortsOnErrorRate(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.1, MinOps: 1})
	app.postStatus = http.StatusServiceUnavailable
	reason := runner.load(t.Context())
	if !strings.Contains(reason, "エラー率") {
		t.Fatalf("reason=%q", reason)
	}
	if runner.board.Count(score.TagGatePass) != 0 {
		t.Fatal("gate passed without a post")
	}
}

func TestCanceledRunFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner, err := NewRunner(Config{Target: "http://127.0.0.1:1", Users: 1, Dataset: tinyDataset, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(ctx)
	if result.Pass || !strings.Contains(result.Reason, "中断") || len(result.Messages) == 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestConfiguration(t *testing.T) {
	for _, cfg := range []Config{{Users: 0}, {Users: 1, Duration: -time.Second}, {Users: 1, VerifyRate: -0.1}, {Users: 1, VerifyRate: 1.5}} {
		if _, err := NewRunner(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
	runner, err := NewRunner(Config{Users: 1, Dataset: tinyDataset})
	if err != nil || runner.verifyRate != DefaultVerifyRate {
		t.Fatalf("verifyRate=%v err=%v, want default %v", runner.verifyRate, err, DefaultVerifyRate)
	}
}

// TestSampleWeight は、重い検証を verifyRate の割合で選び、選んだ検証の失敗を割合の逆数の
// 件数として数えることを確かめる。
func TestSampleWeight(t *testing.T) {
	runner := &Runner{verifyRate: 0.25}
	sampled := 0
	for range 10000 {
		switch w := runner.sample(); w {
		case 0:
		case 4:
			sampled++
		default:
			t.Fatalf("weight=%d, want 0 or 4", w)
		}
	}
	if sampled < 2200 || sampled > 2800 {
		t.Fatalf("sampled %d of 10000, want about 2500", sampled)
	}
	runner.verifyRate = 1
	if w := runner.sample(); w != 1 {
		t.Fatalf("weight=%d with rate 1, want 1", w)
	}
}

// TestSampledMissingPostsKeepErrorRate は、欠落の確認を一部の読み取りだけで行うとき、
// 見つけた欠落を割合の逆数の件数としてエラー率に数えることを確かめる。
func TestSampledMissingPostsKeepErrorRate(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.verifyRate = 0.1
	runner.visibility = func() string { return "public" }
	app.dropFeeds = true
	runner.load(t.Context())
	var failures int64
	for _, window := range runner.recorder.Windows() {
		for _, stats := range runner.recorder.Regions(window) {
			failures += stats.Failures
		}
	}
	errors := runner.board.ErrorCount()
	if errors == 0 || failures != 10*errors {
		t.Fatalf("failures=%d errors=%d, want each missing post counted 10 times", failures, errors)
	}
}
