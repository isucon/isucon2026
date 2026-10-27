package initialdata

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/dataset"
)

var testWorld = func() *dataset.World {
	cfg := dataset.Small()
	cfg.Users, cfg.Days = 120, 35
	return dataset.Generate(cfg)
}()

// fakeApp は初期データどおりに応答するアプリ。mutate で応答を壊して、Checker が
// 見逃さないことを確かめる。
type fakeApp struct {
	c      *Checker
	images map[string]*dataset.Image
	// アプリが返すもの（削除された投稿とコメント、置き換えられた RealMoji、退会した
	// ユーザーの交流を除いたもの）の索引。
	postsByAuthor   map[string][]*dataset.Post
	realMojisByPost map[string][]*dataset.RealMoji
	commentsByPost  map[string][]*dataset.Comment
	// signer は画像 URL に署名するユーザー（nil なら画像の持ち主）。
	signer func(img *dataset.Image) *dataset.User
	mutate func(path string, body any) any
	image  func(img *dataset.Image, data []byte) []byte
}

func newFakeApp(c *Checker) *fakeApp {
	app := &fakeApp{
		c: c, images: map[string]*dataset.Image{}, postsByAuthor: map[string][]*dataset.Post{},
		realMojisByPost: map[string][]*dataset.RealMoji{}, commentsByPost: map[string][]*dataset.Comment{},
	}
	for _, u := range c.world.Users {
		if u.ProfileImage != nil {
			app.images[u.ProfileImage.ID] = u.ProfileImage
		}
	}
	for day := range c.world.Config.Days {
		for _, p := range c.world.PostsOn(day) {
			app.images[p.Image.ID] = p.Image
			if p.DeletedAt == nil {
				app.postsByAuthor[p.AuthorID] = append(app.postsByAuthor[p.AuthorID], p)
			}
			for _, r := range p.RealMojis {
				app.images[r.Image.ID] = r.Image
				if r.ReplacedAt == nil && c.active(r.ReactorID) {
					app.realMojisByPost[p.ID] = append(app.realMojisByPost[p.ID], r)
				}
			}
			for _, cm := range p.Comments {
				if cm.DeletedAt == nil && c.active(cm.AuthorID) {
					app.commentsByPost[p.ID] = append(app.commentsByPost[p.ID], cm)
				}
			}
		}
	}
	return app
}

func (f *fakeApp) signedURL(img *dataset.Image) string {
	signer := f.c.users[img.OwnerID]
	if f.signer != nil {
		signer = f.signer(img)
	}
	exp := time.Now().Add(24 * time.Hour).Unix()
	return fmt.Sprintf("/api/images/%s?exp=%d&sig=%s", img.ID, exp, imageurl.Sign(signer.SecretKey, img.ID, exp))
}

func (f *fakeApp) user(u *dataset.User) client.UserSummary {
	summary := client.UserSummary{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Bio: u.Bio, AccountType: u.AccountType}
	if u.ProfileImage != nil {
		url := f.signedURL(u.ProfileImage)
		summary.ProfileImageURL = &url
	}
	return summary
}

func (f *fakeApp) post(p *dataset.Post) client.Post {
	w := f.c.windows[p.WindowID]
	post := client.Post{
		ID: p.ID, Author: f.user(f.c.users[p.AuthorID]), PostingWindowID: p.WindowID, ImageURL: f.signedURL(p.Image),
		Caption: p.Caption, Visibility: p.Visibility, PostedAt: p.PostedAt, OnTime: !p.PostedAt.After(w.OnTimeDeadline),
		Location: client.Location{Kind: p.Location.Kind, Latitude: p.Location.Latitude, Longitude: p.Location.Longitude, Locality: p.Location.Locality},
	}
	if !post.OnTime {
		post.LateSeconds = int64(p.PostedAt.Sub(w.OnTimeDeadline) / time.Second)
	}
	return post
}

func (f *fakeApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var body any
	switch {
	case r.URL.Path == "/api/auth/login":
		var input struct{ Username, SecretKey string }
		_ = json.NewDecoder(r.Body).Decode(&input)
		for _, u := range f.c.world.Users {
			if u.Username == input.Username && u.SecretKey == input.SecretKey {
				body = map[string]any{"token": u.ID, "user": map[string]string{"id": u.ID, "username": u.Username}}
			}
		}
		if body == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	case r.URL.Path == "/api/memories":
		month, _ := time.Parse("2006-01", r.URL.Query().Get("month"))
		items := []client.MemoryPost{}
		posts := slices.Clone(f.postsByAuthor[token])
		slices.SortFunc(posts, newestFirst(func(p *dataset.Post) time.Time { return p.PostedAt }, func(p *dataset.Post) string { return p.ID }))
		for _, p := range posts {
			date := f.c.windows[p.WindowID].LocalDate
			if date.Year() != month.Year() || date.Month() != month.Month() {
				continue
			}
			if before := r.URL.Query().Get("beforeId"); before != "" && !posts[slices.IndexFunc(posts, func(q *dataset.Post) bool { return q.ID == before })].PostedAt.After(p.PostedAt) {
				continue
			}
			item := client.MemoryPost{Post: f.post(p), RealMojis: []client.RealMoji{}, Comments: []client.Comment{}}
			reactions := slices.Clone(f.realMojisByPost[p.ID])
			slices.SortFunc(reactions, newestFirst(func(r *dataset.RealMoji) time.Time { return r.CreatedAt }, func(r *dataset.RealMoji) string { return r.ID }))
			for _, rm := range reactions[:min(len(reactions), pageSize)] {
				item.RealMojis = append(item.RealMojis, client.RealMoji{ID: rm.ID, Reactor: f.user(f.c.users[rm.ReactorID]), ReactionType: rm.ReactionType, ImageURL: f.signedURL(rm.Image), CreatedAt: rm.CreatedAt})
			}
			comments := slices.Clone(f.commentsByPost[p.ID])
			slices.SortFunc(comments, func(x, y *dataset.Comment) int {
				return cmp.Or(x.CreatedAt.Compare(y.CreatedAt), cmp.Compare(x.ID, y.ID))
			})
			for _, cm := range comments[:min(len(comments), pageSize)] {
				item.Comments = append(item.Comments, client.Comment{ID: cm.ID, Author: f.user(f.c.users[cm.AuthorID]), Body: cm.Body, CreatedAt: cm.CreatedAt})
			}
			items = append(items, item)
		}
		page := client.MemoryPage{Items: items}
		if len(items) > pageSize {
			page.Items, page.HasMore = items[:pageSize], true
		}
		body = page
	case r.URL.Path == "/api/relationships/friends":
		friendships := slices.Clone(f.c.friendshipsByUser[token])
		slices.SortFunc(friendships, newestFirst(func(x *dataset.Friendship) time.Time { return x.CreatedAt }, func(x *dataset.Friendship) string { return x.ID }))
		if before := r.URL.Query().Get("beforeId"); before != "" {
			friendships = friendships[slices.IndexFunc(friendships, func(x *dataset.Friendship) bool { return x.ID == before })+1:]
		}
		page := client.RelationshipPage{Items: []client.Relationship{}}
		for _, x := range friendships {
			friend := x.UserLowID
			if friend == token {
				friend = x.UserHighID
			}
			page.Items = append(page.Items, client.Relationship{RelationID: x.ID, User: f.user(f.c.users[friend]), Status: "friends", UpdatedAt: x.CreatedAt})
		}
		if len(page.Items) > pageSize {
			page.Items, page.HasMore = page.Items[:pageSize], true
		}
		body = page
	case r.URL.Path == "/api/me/export":
		posts := slices.Clone(f.postsByAuthor[token])
		slices.SortFunc(posts, func(x, y *dataset.Post) int { return cmp.Or(x.PostedAt.Compare(y.PostedAt), cmp.Compare(x.ID, y.ID)) })
		var exported []exportedPost
		var buffer bytes.Buffer
		archive := zip.NewWriter(&buffer)
		for i, p := range posts {
			path := fmt.Sprintf("images/%06d.jpg", i+1)
			file, _ := archive.Create(path)
			data := f.c.world.ImageBytes(p.Image)
			if f.image != nil {
				data = f.image(p.Image, data)
			}
			file.Write(data)
			exported = append(exported, exportedPost{Post: f.post(p), ImagePath: path})
		}
		var manifest any = exported
		if f.mutate != nil {
			manifest = f.mutate(r.URL.Path, exported)
		}
		file, _ := archive.Create("posts.json")
		json.NewEncoder(file).Encode(manifest)
		archive.Close()
		w.Write(buffer.Bytes())
		return
	case strings.HasPrefix(r.URL.Path, "/api/images/"):
		img := f.images[strings.TrimPrefix(r.URL.Path, "/api/images/")]
		data := f.c.world.ImageBytes(img)
		if f.image != nil {
			data = f.image(img, data)
		}
		w.Write(data)
		return
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if f.mutate != nil {
		body = f.mutate(r.URL.Path, body)
	}
	json.NewEncoder(w).Encode(body)
}

func TestChecker(t *testing.T) {
	c := New(testWorld)
	c.MemoryMonths = 0
	targets := c.Targets()
	if len(targets) < 3 {
		t.Fatalf("targets=%d", len(targets))
	}
	// 友達の一覧のページ送りと、月をまたぐ Memories を通すユーザーが含まれること。
	if hub := targets[0]; len(c.friendshipsByUser[hub.ID]) <= 5 {
		t.Fatalf("hub user has only %d friends", len(c.friendshipsByUser[hub.ID]))
	}

	for name, tc := range map[string]struct {
		setup   func(*fakeApp)
		wantErr string
	}{
		"matches the initial data": {},
		"caption changed": {setup: func(f *fakeApp) {
			f.mutate = func(path string, body any) any {
				if page, ok := body.(client.MemoryPage); ok && len(page.Items) > 0 {
					page.Items[0].Caption += "!"
				}
				return body
			}
		}, wantErr: "キャプション"},
		"post missing from memories": {setup: func(f *fakeApp) {
			f.mutate = func(path string, body any) any {
				if page, ok := body.(client.MemoryPage); ok && len(page.Items) > 0 {
					page.Items = page.Items[1:]
					return page
				}
				return body
			}
		}, wantErr: "Memories"},
		"image signed by another user": {setup: func(f *fakeApp) {
			f.signer = func(*dataset.Image) *dataset.User { return testWorld.Users[1] }
		}, wantErr: "署名"},
		"image content changed": {setup: func(f *fakeApp) {
			f.image = func(img *dataset.Image, data []byte) []byte {
				if img.Kind == dataset.ImageRealMoji {
					return append(slices.Clone(data), 0)
				}
				return data
			}
		}, wantErr: "中身が初期データと違います"},
		"friend missing": {setup: func(f *fakeApp) {
			f.mutate = func(path string, body any) any {
				if page, ok := body.(client.RelationshipPage); ok && len(page.Items) > 0 {
					page.Items = page.Items[1:]
					return page
				}
				return body
			}
		}, wantErr: "友達の一覧"},
		"export out of order": {setup: func(f *fakeApp) {
			f.mutate = func(path string, body any) any {
				if posts, ok := body.([]exportedPost); ok && len(posts) > 1 {
					posts[0], posts[1] = posts[1], posts[0]
				}
				return body
			}
		}, wantErr: "エクスポート"},
	} {
		t.Run(name, func(t *testing.T) {
			app := newFakeApp(c)
			if tc.setup != nil {
				tc.setup(app)
			}
			server := httptest.NewServer(app)
			defer server.Close()
			var err error
			for _, user := range targets {
				if err = c.Check(t.Context(), client.New(server.URL), user); err != nil {
					break
				}
			}
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestCheckerSampled は、月と画像を抜き出して確かめても初期データどおりの応答を通すことを
// 確かめる。
func TestCheckerSampled(t *testing.T) {
	c := New(testWorld)
	server := httptest.NewServer(newFakeApp(c))
	defer server.Close()
	for _, user := range c.Targets() {
		if err := c.Check(t.Context(), client.New(server.URL), user); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range c.HistoryUsers(dataset.Asia)[:5] {
		a := agent.New(client.New(server.URL), user.Region, 0)
		a.ID, a.Username, a.SecretKey = user.ID, user.Username, user.SecretKey
		if err := a.Login(t.Context()); err != nil {
			t.Fatal(err)
		}
		months := c.HistoryMonths()
		if err := c.CheckMemories(t.Context(), a, months[len(months)-1], func(client.Post) bool { return false }, 2, true); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCheckMemoriesSampledSignatures は、署名を抜き出して確かめても、署名を誤る実装を
// 見逃さないことを確かめる。
func TestCheckMemoriesSampledSignatures(t *testing.T) {
	c := New(testWorld)
	app := newFakeApp(c)
	app.signer = func(*dataset.Image) *dataset.User { return testWorld.Users[1] }
	server := httptest.NewServer(app)
	defer server.Close()
	user := c.Targets()[0]
	a := agent.New(client.New(server.URL), user.Region, 0)
	a.ID, a.Username, a.SecretKey = user.ID, user.Username, user.SecretKey
	if err := a.Login(t.Context()); err != nil {
		t.Fatal(err)
	}
	// thorough でなければ署名は確かめない。
	for _, month := range c.HistoryMonths() {
		if err := c.CheckMemories(t.Context(), a, month, func(client.Post) bool { return false }, 2, false); err != nil {
			t.Fatalf("signatures checked without thorough check: %v", err)
		}
	}
	var err error
	for _, month := range c.HistoryMonths() {
		if err = c.CheckMemories(t.Context(), a, month, func(client.Post) bool { return false }, 2, true); err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "署名") {
		t.Fatalf("want a signature error, got %v", err)
	}
}

// TestCheckExportImages は、エクスポートの画像を比べる枚数の指定（0 は比べない、AllImages は
// すべて）を確かめる。負荷走行では間引いた検証の分だけ画像を描き直して比べる。
func TestCheckExportImages(t *testing.T) {
	c := New(testWorld)
	app := newFakeApp(c)
	app.image = func(_ *dataset.Image, data []byte) []byte { return append(slices.Clone(data), 0) }
	server := httptest.NewServer(app)
	defer server.Close()
	user := c.Targets()[0]
	a := agent.New(client.New(server.URL), user.Region, 0)
	a.ID, a.Username, a.SecretKey = user.ID, user.Username, user.SecretKey
	if err := a.Login(t.Context()); err != nil {
		t.Fatal(err)
	}
	isNew := func(string) bool { return false }
	if err := c.CheckExport(t.Context(), a, 0, isNew); err != nil {
		t.Fatalf("images=0 compared images: %v", err)
	}
	for _, images := range []int{1, AllImages} {
		if err := c.CheckExport(t.Context(), a, images, isNew); err == nil || !strings.Contains(err.Error(), "画像と違います") {
			t.Fatalf("images=%d: err=%v", images, err)
		}
	}
}
