package scenario

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
)

type fakeUser struct {
	id, username, secret, region, bio string
	displayName                       string
	certified, deleted                bool
	profileImage                      string
}

type fakePost struct {
	client.Post
	date    string
	deleted bool
}

type fakeComment struct {
	client.Comment
	postID  string
	deleted bool
}

type fakeReaction struct {
	client.RealMoji
	postID           string
	replaced, gone   bool
	reactorID, image string
}

type fakeWindow struct{ id, date string }

type fakeRequest struct {
	from, to string
	status   string
	at       time.Time
}

// fakeApp はアプリの閲覧可否とフィードの規則を、bench のモデルとは別に実装した偽のアプリ。
// トークンはユーザー ID。フィールドで障害や不具合を注入する。
type fakeApp struct {
	// flakyMoves は、所属リージョンの変更を反映したうえで 500 を返す回数（応答の届かない変更を再現する）。
	flakyMoves int
	// moveReplies は、所属リージョンの変更に順に返す応答。500 は反映したうえで 500、409 は
	// 反映せずに 409 を返す。空になったら flakyMoves に従う。
	moveReplies []int
	t           *testing.T

	mu        sync.Mutex
	users     map[string]*fakeUser
	windows   map[string]fakeWindow // リージョン → 開いている投稿期間
	opened    map[string]int        // リージョン → これまでに開いた投稿期間の数
	posts     []*fakePost
	images    map[string][]byte
	owners    map[string]string // 画像 → 持ち主（署名の鍵）
	comments  []*fakeComment
	reactions []*fakeReaction
	friends   map[[2]string]bool
	blocks    map[[2]string]bool // ブロックする人とされる人
	requests  map[string]*fakeRequest
	follows   map[[2]string]bool // フォローする人とされる人
	nextID    int

	postStatus   int                                  // 0 以外なら POST /api/posts をこのステータスにする
	extraFeed    []client.Post                        // すべてのフィードのページに追加する
	ignoreBlocks bool                                 // ブロックを無視する不具合
	pageSize     int                                  // フィードの 1 ページの件数（0 なら 50）
	keepDeleted  bool                                 // 削除した投稿を Memories に残す不具合
	losePosts    bool                                 // 投稿を Memories から失う不具合
	dropFeeds    bool                                 // ゲート通過のあとのフィードを空で返す不具合
	banned       map[string]bool                      // X-Client-IP ごとの BAN
	lax          bool                                 // 地域外のアクセス、画像の形式と署名を確かめない不具合
	ignoreGate   bool                                 // 今日投稿していない人にも投稿を見せる不具合
	openDelay    time.Duration                        // 投稿期間はすぐ開くが、応答をこれだけ遅らせる
	hang         bool                                 // 投稿期間を開く要求と今の投稿期間の読み取りに応答しない
	deleteFails  bool                                 // 退会を済ませたうえで 500 を返す
	onBlock      func(target string)                  // ブロックの要求を受けたとき（a.mu を持ったまま呼ぶ）
	notify       func(region, windowID string) string // 通知の中身
}

func newFakeApp(t *testing.T) *fakeApp {
	app := &fakeApp{
		t: t, users: map[string]*fakeUser{}, windows: map[string]fakeWindow{}, opened: map[string]int{}, images: map[string][]byte{}, owners: map[string]string{},
		friends: map[[2]string]bool{}, blocks: map[[2]string]bool{}, requests: map[string]*fakeRequest{}, follows: map[[2]string]bool{},
		banned: map[string]bool{},
	}
	app.notify = func(region, windowID string) string {
		return fmt.Sprintf(`{"id":"n-%s","postingWindowId":%q,"region":%q}`, region, windowID, region)
	}
	return app
}

func (a *fakeApp) addUser(id, username, secret, region string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users[id] = &fakeUser{id: id, username: username, secret: secret, region: region}
}

func (a *fakeApp) postCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.posts)
}

func key(a, b string) [2]string { return [2]string{min(a, b), max(a, b)} }

func (a *fakeApp) id() string {
	a.nextID++
	return fmt.Sprintf("x%d", a.nextID)
}

func (a *fakeApp) active(id string) bool {
	u := a.users[id]
	return u != nil && !u.deleted
}

func (a *fakeApp) blocked(x, y string) bool {
	return (a.blocks[[2]string{x, y}] || a.blocks[[2]string{y, x}]) && !a.ignoreBlocks
}

func (a *fakeApp) postedOn(viewer *fakeUser, date string) bool {
	return slices.ContainsFunc(a.posts, func(p *fakePost) bool { return p.Author.ID == viewer.id && p.date == date && !p.deleted })
}

// canView はアプリの canView と同じ規則。
func (a *fakeApp) canView(viewer *fakeUser, p *fakePost) bool {
	author := a.users[p.Author.ID]
	if p.deleted || viewer.deleted || author.deleted {
		return false
	}
	if viewer.id == author.id {
		return true
	}
	// 投稿の投稿期間が終わっていなければ今の投稿（投稿者がリージョンを移っても変わらない）。
	if !a.windowOpen(p) {
		return false
	}
	own, ok := a.windows[viewer.region]
	if !ok || (!a.ignoreGate && !a.postedOn(viewer, own.date)) || a.blocked(viewer.id, author.id) {
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

// windowOpen は投稿の投稿期間がまだ開いているか（投稿者が今いるリージョンとは関係ない）。
func (a *fakeApp) windowOpen(p *fakePost) bool {
	return slices.ContainsFunc(slices.Collect(maps.Values(a.windows)), func(w fakeWindow) bool { return w.id == p.PostingWindowID })
}

func (a *fakeApp) canComment(viewer *fakeUser, p *fakePost) bool {
	if !a.canView(viewer, p) || !a.windowOpen(p) {
		return false
	}
	if viewer.id == p.Author.ID {
		return a.postedOn(viewer, a.windows[viewer.region].date)
	}
	return p.Visibility == "public" || a.friends[key(viewer.id, p.Author.ID)]
}

// commonFriend はアプリと同じく、退会した人も共通の友達に数える（退会しても友達関係は残る）。
func (a *fakeApp) commonFriend(x, y string) bool {
	for id := range a.users {
		if id != x && id != y && a.friends[key(x, id)] && a.friends[key(id, y)] {
			return true
		}
	}
	return false
}

func (a *fakeApp) inFeed(viewer *fakeUser, p *fakePost, feed string) bool {
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

// mutualFriends は x と y の共通の友達の数。
func (a *fakeApp) mutualFriends(x, y string) int64 {
	n := int64(0)
	for id := range a.users {
		if id != x && id != y && a.friends[key(x, id)] && a.friends[key(id, y)] {
			n++
		}
	}
	return n
}

// score はおすすめ順の点数（10 × 友達の反応 + 3 × 共通の友達 + 3 歩の道の数（30 まで））。
func (a *fakeApp) score(viewer string, p *fakePost) int64 {
	author := p.Author.ID
	connections := int64(0)
	for id := range a.users {
		if a.friends[key(author, id)] {
			connections += a.mutualFriends(viewer, id)
		}
	}
	score := 3*a.mutualFriends(viewer, author) + min(connections, 30)
	engaged := map[string]bool{}
	for _, reaction := range a.reactions {
		if reaction.postID == p.ID && !reaction.replaced && !reaction.gone {
			engaged[reaction.reactorID] = true
		}
	}
	for _, c := range a.comments {
		if c.postID == p.ID && !c.deleted {
			engaged[c.Author.ID] = true
		}
	}
	for id := range engaged {
		if a.friends[key(viewer, id)] && a.active(id) {
			score += 10
		}
	}
	return score
}

// recommended は友達の友達フィードの 1 ページ（おすすめ順）。
func (a *fakeApp) recommended(viewer *fakeUser, query url.Values, size int) map[string]any {
	type item struct {
		client.Post
		Score int64 `json:"score"`
	}
	items := []item{}
	for _, p := range a.posts {
		if a.inFeed(viewer, p, "friends-of-friends") {
			items = append(items, item{Post: p.Post, Score: a.score(viewer.id, p)})
		}
	}
	less := func(x, y item) bool {
		if x.Score != y.Score {
			return x.Score < y.Score
		}
		if !x.PostedAt.Equal(y.PostedAt) {
			return x.PostedAt.Before(y.PostedAt)
		}
		return x.ID < y.ID
	}
	slices.SortFunc(items, func(x, y item) int {
		switch {
		case less(y, x):
			return -1
		case less(x, y):
			return 1
		}
		return 0
	})
	if query.Get("beforeScore") != "" {
		score, _ := strconv.ParseInt(query.Get("beforeScore"), 10, 64)
		postedAt, _ := time.Parse(time.RFC3339Nano, query.Get("beforePostedAt"))
		cursor := item{Post: client.Post{ID: query.Get("beforeId"), PostedAt: postedAt}, Score: score}
		items = slices.DeleteFunc(items, func(x item) bool { return !less(x, cursor) })
	}
	hasMore := len(items) > size
	if hasMore {
		items = items[:size]
	}
	for _, extra := range a.extraFeed {
		items = append(items, item{Post: extra})
	}
	return map[string]any{"items": items, "hasMore": hasMore}
}

func (a *fakeApp) signed(owner *fakeUser, imageID string) string {
	exp := time.Now().Add(time.Hour).Unix()
	return fmt.Sprintf("/api/images/%s?exp=%d&sig=%s", imageID, exp, imageurl.Sign(owner.secret, imageID, exp))
}

func (a *fakeApp) summary(u *fakeUser) client.UserSummary {
	s := client.UserSummary{ID: u.id, Username: u.username, Bio: u.bio, AccountType: "regular"}
	if u.certified {
		s.AccountType = "certified_public"
	}
	if u.profileImage != "" {
		url := a.signed(u, u.profileImage)
		s.ProfileImageURL = &url
	}
	return s
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
	fail := func(status int, code string) { respond(status, map[string]string{"error": code}) }
	a.mu.Lock()
	banned := a.banned[r.Header.Get("X-Client-IP")]
	a.mu.Unlock()
	if banned {
		fail(http.StatusForbidden, "ip_banned")
		return
	}
	if path[0] == "notifications" {
		a.serveNotifications(w, r, region)
		return
	}
	a.mu.Lock()
	openDelay, hang := a.openDelay, a.hang
	a.mu.Unlock()
	if hang && (path[0] == "posting-window" || (path[0] == "admin" && path[1] == "posting-window")) {
		// 本文を読み切らないと、サーバーはクライアントの切断に気づかず、要求の context が
		// 終わらない。
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		return
	}
	if openDelay > 0 && path[0] == "admin" && path[1] == "posting-window" {
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		a.mu.Lock()
		a.windows[input["region"]] = fakeWindow{id: a.id(), date: input["localDate"]}
		a.opened[input["region"]]++
		id := a.windows[input["region"]].id
		a.mu.Unlock()
		select {
		case <-time.After(openDelay):
		case <-r.Context().Done():
			return
		}
		respond(http.StatusCreated, map[string]string{"id": id, "region": input["region"], "localDate": input["localDate"]})
		return
	}
	if path[0] == "posts" && len(path) == 1 && r.Method == http.MethodPost && a.postStatus != 0 {
		respond(a.postStatus, nil)
		return
	}
	var image []byte
	if path[0] == "posts" && r.Method == http.MethodPost {
		_ = r.ParseMultipartForm(32 << 20)
		if f, _, err := r.FormFile("image"); err == nil {
			image, _ = io.ReadAll(f)
			_ = f.Close()
		}
	}
	if path[0] == "me" && len(path) == 2 && path[1] == "profile-image" && r.Method == http.MethodPut {
		image, _ = io.ReadAll(r.Body)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	viewer := a.users[token]
	if token != "" && token != "admin" && path[0] != "images" {
		if viewer == nil || viewer.deleted {
			fail(http.StatusUnauthorized, "invalid_session")
			return
		}
		// 所属リージョンの変更（PATCH /api/me）は、アプリと同じくどのリージョンからでも受け付ける。
		if viewer.region != region && !(path[0] == "me" && len(path) == 1 && r.Method == http.MethodPatch) {
			// アプリは地域外のサーバーからのアクセスを 403 で拒否する。bench は事前検証でだけ
			// わざと送るので、負荷走行のテストでは来ないはず。
			if !a.lax && path[0] == "me" && len(path) == 1 {
				fail(http.StatusForbidden, "region_mismatch")
				return
			}
			if !a.lax {
				a.t.Errorf("user %s sent a country of %s, want %s (%s %s)", token, region, viewer.region, r.Method, r.URL.Path)
			}
		}
	}
	method := r.Method
	switch {
	case path[0] == "auth" && path[1] == "register":
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		id := "u-" + input["username"]
		if a.users[id] != nil {
			fail(http.StatusConflict, "username_taken")
			return
		}
		a.users[id] = &fakeUser{id: id, username: input["username"], secret: "secret-" + id, region: input["region"]}
		respond(http.StatusCreated, map[string]any{"secretKey": "secret-" + id, "user": map[string]string{"id": id, "username": input["username"]}})
	case path[0] == "auth" && path[1] == "login":
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		id := "u-" + input["username"]
		if u := a.users[id]; u == nil || u.secret != input["secretKey"] || u.deleted {
			fail(http.StatusUnauthorized, "invalid_credentials")
			return
		}
		respond(http.StatusOK, map[string]any{"token": id, "user": map[string]string{"id": id, "username": input["username"]}})
	case path[0] == "auth" && path[1] == "logout":
		respond(http.StatusNoContent, nil)
	case path[0] == "admin" && path[1] == "auth":
		respond(http.StatusNoContent, nil)
	case path[0] == "admin" && path[1] == "users":
		u := a.users[path[2]]
		if method == http.MethodDelete || u == nil || u.certified {
			fail(http.StatusConflict, "certification_conflict")
			return
		}
		u.certified = true
		respond(http.StatusNoContent, nil)
	case path[0] == "admin":
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		a.windows[input["region"]] = fakeWindow{id: a.id(), date: input["localDate"]}
		a.opened[input["region"]]++
		respond(http.StatusCreated, map[string]string{"id": a.windows[input["region"]].id, "region": input["region"], "localDate": input["localDate"]})
	case path[0] == "me" && len(path) == 1 && method == http.MethodGet:
		respond(http.StatusOK, a.profile(viewer))
	case path[0] == "me" && len(path) == 1:
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		if bio, ok := input["bio"]; ok {
			viewer.bio = bio
		}
		if name, ok := input["displayName"]; ok {
			viewer.displayName = name
		}
		if region, ok := input["region"]; ok {
			if len(a.moveReplies) > 0 {
				reply := a.moveReplies[0]
				a.moveReplies = a.moveReplies[1:]
				if reply == http.StatusConflict {
					fail(http.StatusConflict, "rejected")
					return
				}
				viewer.region = region
				fail(reply, "flaky")
				return
			}
			viewer.region = region
			if a.flakyMoves > 0 {
				a.flakyMoves--
				fail(http.StatusInternalServerError, "flaky")
				return
			}
		}
		respond(http.StatusOK, a.profile(viewer))
	case path[0] == "me" && path[1] == "profile-image" && method == http.MethodPut:
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(image)); !a.lax && (err != nil || cfg.Width > 512 || cfg.Height > 512) {
			fail(http.StatusBadRequest, "invalid_profile_image")
			return
		}
		id := "pimg-" + a.id()
		a.images[id] = image
		a.owners[id] = viewer.id
		viewer.profileImage = id
		respond(http.StatusNoContent, nil)
	case path[0] == "me" && path[1] == "profile-image":
		if viewer.profileImage == "" {
			fail(http.StatusNotFound, "account_not_found")
			return
		}
		viewer.profileImage = ""
		respond(http.StatusNoContent, nil)
	case path[0] == "me" && path[1] == "account-deletion":
		viewer.deleted = true
		if a.deleteFails {
			respond(http.StatusInternalServerError, nil)
			return
		}
		respond(http.StatusNoContent, nil)
	case path[0] == "users" && len(path) == 1:
		q := strings.ToLower(r.URL.Query().Get("q"))
		items := []client.UserSummary{}
		for _, u := range a.users {
			if u.id != viewer.id && !u.deleted && !a.blocked(viewer.id, u.id) && strings.Contains(strings.ToLower(u.username), q) {
				items = append(items, a.summary(u))
			}
		}
		slices.SortFunc(items, func(x, y client.UserSummary) int { return strings.Compare(x.Username, y.Username) })
		respond(http.StatusOK, map[string]any{"items": items, "hasMore": false})
	case path[0] == "users":
		u := a.users[path[1]]
		if u == nil || u.deleted || a.blocked(viewer.id, u.id) {
			fail(http.StatusNotFound, "relationship_not_found")
			return
		}
		respond(http.StatusOK, a.summary(u))
	case path[0] == "follows":
		pair := [2]string{viewer.id, path[1]}
		target := a.users[path[1]]
		switch {
		case method == http.MethodDelete && !a.follows[pair]:
			fail(http.StatusConflict, "relationship_conflict")
		case method == http.MethodDelete:
			delete(a.follows, pair)
			respond(http.StatusNoContent, nil)
		case target == nil || !target.certified || a.follows[pair] || a.blocked(viewer.id, target.id):
			fail(http.StatusConflict, "relationship_conflict")
		default:
			a.follows[pair] = true
			respond(http.StatusNoContent, nil)
		}
	case path[0] == "blocks":
		if a.onBlock != nil {
			a.onBlock(path[1])
		}
		pair := [2]string{viewer.id, path[1]}
		switch {
		case a.users[path[1]] == nil || a.users[path[1]].deleted:
			fail(http.StatusNotFound, "relationship_not_found")
		case method == http.MethodDelete && !a.blocks[pair]:
			fail(http.StatusConflict, "relationship_conflict")
		case method == http.MethodDelete:
			delete(a.blocks, pair)
			respond(http.StatusNoContent, nil)
		case a.blocks[pair]:
			fail(http.StatusConflict, "relationship_conflict")
		default:
			a.blocks[pair] = true
			delete(a.friends, key(viewer.id, path[1]))
			delete(a.follows, [2]string{viewer.id, path[1]})
			delete(a.follows, [2]string{path[1], viewer.id})
			respond(http.StatusNoContent, nil)
		}
	case path[0] == "friendships":
		if target := a.users[path[1]]; target == nil || target.deleted {
			fail(http.StatusNotFound, "relationship_not_found")
			return
		}
		if !a.friends[key(viewer.id, path[1])] {
			fail(http.StatusConflict, "relationship_conflict")
			return
		}
		delete(a.friends, key(viewer.id, path[1]))
		respond(http.StatusNoContent, nil)
	case path[0] == "relationships":
		a.serveRelationships(respond, viewer, path[1])
	case path[0] == "posting-window":
		respond(http.StatusOK, map[string]any{"window": map[string]string{"id": a.windows[region].id, "region": region, "localDate": a.windows[region].date}, "canPost": true, "hasPosted": a.postedOn(viewer, a.windows[region].date)})
	case path[0] == "posts" && len(path) == 1:
		// アプリと同じく、同じ日付に有効な投稿があるか、2 回削除していれば受け付けない。
		date, active, deleted := a.windows[region].date, 0, 0
		for _, p := range a.posts {
			if p.Author.ID == viewer.id && p.date == date {
				if p.deleted {
					deleted++
				} else {
					active++
				}
			}
		}
		if active > 0 || deleted >= 2 {
			fail(http.StatusConflict, "post_slot_unavailable")
			return
		}
		id := a.id()
		imageID := "img-" + id
		a.images[imageID] = image
		a.owners[imageID] = viewer.id
		p := &fakePost{date: a.windows[region].date}
		p.Post = client.Post{
			ID: id, Author: client.UserSummary{ID: viewer.id, Username: viewer.username}, PostingWindowID: a.windows[region].id,
			ImageURL: a.signed(viewer, imageID), Caption: r.FormValue("caption"), Visibility: r.FormValue("visibility"), PostedAt: time.Now().Truncate(time.Microsecond),
			Location: fakeLocation(r),
		}
		a.posts = append(a.posts, p)
		respond(http.StatusCreated, p.Post)
	case path[0] == "posts" && path[1] == "me":
		for _, p := range a.posts {
			if p.Author.ID == viewer.id && !p.deleted && p.date == a.windows[viewer.region].date {
				respond(http.StatusOK, p.Post)
				return
			}
		}
		fail(http.StatusNotFound, "post_not_found")
	case path[0] == "posts" && len(path) == 2:
		p := a.post(path[1])
		switch {
		case p == nil:
			fail(http.StatusNotFound, "post_not_found")
		case method == http.MethodGet && !a.canView(viewer, p):
			fail(http.StatusNotFound, "post_not_found")
		case method == http.MethodGet:
			respond(http.StatusOK, p.Post)
		case method == http.MethodDelete:
			p.deleted = true
			respond(http.StatusNoContent, nil)
		case !a.windowOpen(p):
			fail(http.StatusForbidden, "post_not_editable")
		default:
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			if v, ok := input["visibility"]; ok {
				p.Visibility = v
			}
			if c, ok := input["caption"]; ok {
				p.Caption = c
			}
			respond(http.StatusOK, p.Post)
		}
	case path[0] == "posts" && len(path) == 3:
		a.serveInteraction(respond, fail, r, viewer, a.post(path[1]), path[2], image)
	case path[0] == "comments":
		i := slices.IndexFunc(a.comments, func(c *fakeComment) bool { return c.ID == path[1] && !c.deleted })
		if i < 0 {
			fail(http.StatusNotFound, "interaction_not_found")
			return
		}
		a.comments[i].deleted = true
		respond(http.StatusNoContent, nil)
	case path[0] == "realmojis":
		i := slices.IndexFunc(a.reactions, func(x *fakeReaction) bool { return x.ID == path[1] && !x.replaced && !x.gone })
		if i < 0 {
			fail(http.StatusNotFound, "interaction_not_found")
			return
		}
		a.reactions[i].gone = true
		respond(http.StatusNoContent, nil)
	case path[0] == "images":
		data, ok := a.images[path[1]]
		if owner := a.users[a.owners[path[1]]]; ok && owner != nil && !a.lax {
			if imageurl.Verify(r.URL.String(), path[1], owner.secret, time.Now()) != nil {
				ok = false
			}
		}
		for _, p := range a.posts {
			if p.deleted && strings.Contains(p.ImageURL, "/"+path[1]+"?") {
				ok = false
			}
		}
		if !ok {
			fail(http.StatusForbidden, "image_forbidden")
			return
		}
		_, _ = w.Write(data)
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
	case path[0] == "feeds" && path[1] == "friends-of-friends":
		respond(http.StatusOK, a.recommended(viewer, r.URL.Query(), cmp.Or(a.pageSize, 50)))
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
		target := input["recipientId"]
		if !a.active(target) {
			fail(http.StatusNotFound, "relationship_not_found")
			return
		}
		if a.blocked(viewer.id, target) || a.friends[key(viewer.id, target)] {
			fail(http.StatusConflict, "relationship_conflict")
			return
		}
		for id, request := range a.requests {
			if request.status != "pending" {
				continue
			}
			if request.from == viewer.id && request.to == target {
				fail(http.StatusConflict, "relationship_conflict")
				return
			}
			if request.from == target && request.to == viewer.id {
				request.status = "accepted"
				a.friends[key(viewer.id, target)] = true
				respond(http.StatusCreated, map[string]string{"relationId": id, "status": "accepted"})
				return
			}
		}
		id := a.id()
		a.requests[id] = &fakeRequest{from: viewer.id, to: target, status: "pending", at: time.Now()}
		respond(http.StatusCreated, map[string]string{"relationId": id, "status": "pending"})
	case path[0] == "friend-requests":
		request := a.requests[path[1]]
		action := path[2]
		switch {
		case request == nil:
			fail(http.StatusNotFound, "relationship_not_found")
		case request.status != "pending" || (action == "cancel") != (viewer.id == request.from):
			fail(http.StatusForbidden, "relationship_action_forbidden")
		case action == "accept" && a.blocked(request.from, request.to):
			fail(http.StatusConflict, "relationship_conflict")
		default:
			request.status = map[string]string{"accept": "accepted", "reject": "rejected", "cancel": "cancelled"}[action]
			if action == "accept" {
				a.friends[key(request.from, request.to)] = true
			}
			respond(http.StatusOK, map[string]string{"relationId": path[1], "status": request.status})
		}
	default:
		a.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		respond(http.StatusNotFound, nil)
	}
}

func (a *fakeApp) post(id string) *fakePost {
	if i := slices.IndexFunc(a.posts, func(p *fakePost) bool { return p.ID == id }); i >= 0 {
		return a.posts[i]
	}
	return nil
}

func (a *fakeApp) profile(u *fakeUser) map[string]any {
	s := a.summary(u)
	return map[string]any{"id": u.id, "username": u.username, "displayName": u.displayName, "region": u.region, "bio": u.bio, "profileImageUrl": s.ProfileImageURL}
}

// serveNotifications は購読した時点より後に開いた投稿期間を 1 件だけ通知する。
func (a *fakeApp) serveNotifications(w http.ResponseWriter, r *http.Request, region string) {
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
}

func (a *fakeApp) serveRelationships(respond func(int, any), viewer *fakeUser, kind string) {
	items := []client.Relationship{}
	add := func(id, other, status string) {
		if a.active(other) {
			items = append(items, client.Relationship{RelationID: id, User: a.summary(a.users[other]), Status: status})
		}
	}
	switch kind {
	case "friends":
		for pair := range a.friends {
			for i, id := range pair {
				if id == viewer.id {
					add(pair[0]+pair[1], pair[1-i], "friends")
				}
			}
		}
	case "following", "followers":
		for pair := range a.follows {
			switch {
			case kind == "following" && pair[0] == viewer.id:
				add(pair[0]+pair[1], pair[1], kind)
			case kind == "followers" && pair[1] == viewer.id:
				add(pair[0]+pair[1], pair[0], kind)
			}
		}
	case "incoming-requests", "outgoing-requests":
		for id, request := range a.requests {
			switch {
			case request.status != "pending":
			case kind == "incoming-requests" && request.to == viewer.id:
				add(id, request.from, "pending")
			case kind == "outgoing-requests" && request.from == viewer.id:
				add(id, request.to, "pending")
			}
		}
	}
	respond(http.StatusOK, map[string]any{"items": items, "hasMore": false})
}

func (a *fakeApp) serveInteraction(respond func(int, any), fail func(int, string), r *http.Request, viewer *fakeUser, p *fakePost, kind string, image []byte) {
	if p == nil {
		fail(http.StatusNotFound, "post_not_found")
		return
	}
	switch {
	case kind == "comments" && r.Method == http.MethodPost:
		if !a.canComment(viewer, p) {
			fail(http.StatusForbidden, "interaction_forbidden")
			return
		}
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		c := &fakeComment{postID: p.ID}
		c.Comment = client.Comment{ID: a.id(), Author: client.UserSummary{ID: viewer.id}, Body: input["body"], CreatedAt: time.Now().Truncate(time.Microsecond)}
		a.comments = append(a.comments, c)
		respond(http.StatusCreated, c.Comment)
	case kind == "comments":
		if !a.canComment(viewer, p) {
			fail(http.StatusForbidden, "interaction_forbidden")
			return
		}
		items := []client.Comment{}
		for _, c := range a.comments {
			if c.postID == p.ID && !c.deleted && a.active(c.Author.ID) {
				items = append(items, c.Comment)
			}
		}
		// アプリと同じく作成時刻、ID の昇順に並べる。
		slices.SortFunc(items, func(x, y client.Comment) int {
			return cmp.Or(x.CreatedAt.Compare(y.CreatedAt), strings.Compare(x.ID, y.ID))
		})
		respond(http.StatusOK, map[string]any{"items": items, "hasMore": false})
	case r.Method == http.MethodPost:
		if !a.canView(viewer, p) || viewer.id == p.Author.ID {
			fail(http.StatusForbidden, "interaction_forbidden")
			return
		}
		for _, x := range a.reactions {
			if x.postID == p.ID && x.reactorID == viewer.id {
				x.replaced = true
			}
		}
		id := a.id()
		a.images["rimg-"+id] = image
		a.owners["rimg-"+id] = viewer.id
		x := &fakeReaction{postID: p.ID, reactorID: viewer.id}
		x.RealMoji = client.RealMoji{ID: id, Reactor: client.UserSummary{ID: viewer.id}, ReactionType: r.FormValue("reactionType"), ImageURL: a.signed(viewer, "rimg-"+id), CreatedAt: time.Now().Truncate(time.Microsecond)}
		a.reactions = append(a.reactions, x)
		respond(http.StatusCreated, x.RealMoji)
	default:
		if !a.canView(viewer, p) {
			fail(http.StatusForbidden, "interaction_forbidden")
			return
		}
		items := []client.RealMoji{}
		for _, x := range a.reactions {
			if x.postID == p.ID && !x.replaced && !x.gone && a.active(x.reactorID) {
				items = append(items, x.RealMoji)
			}
		}
		// アプリと同じく作成時刻、ID の降順に並べる。
		slices.SortFunc(items, func(x, y client.RealMoji) int {
			return cmp.Or(y.CreatedAt.Compare(x.CreatedAt), strings.Compare(y.ID, x.ID))
		})
		respond(http.StatusOK, map[string]any{"items": items, "hasMore": false})
	}
}

// fakeLocation は投稿の位置情報のフォームを、アプリと同じ形の応答に直す。
func fakeLocation(r *http.Request) client.Location {
	location := client.Location{Kind: r.FormValue("location_kind")}
	if v, err := strconv.ParseFloat(r.FormValue("latitude"), 64); err == nil {
		location.Latitude = &v
	}
	if v, err := strconv.ParseFloat(r.FormValue("longitude"), 64); err == nil {
		location.Longitude = &v
	}
	if v := r.FormValue("locality"); v != "" {
		location.Locality = &v
	}
	return location
}
