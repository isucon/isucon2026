// Package model は、bench が知っているアプリの状態（初期データと bench 自身の書き込み）を
// 持ち、ある読み取りから何が見えるべきかを判定する。
//
// 書き込みは送信した時刻と応答を受けた時刻の区間付きで記録する。並行する書き込みのせいで
// 決められない条件は「不明」とし、不明な投稿は見えても見えなくてもよいとする。
package model

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/isucon/isucon2026/dataset"
)

type User struct {
	ID        string
	Username  string
	SecretKey string
	// Region は登録したときのリージョン。走行中に移ったあとのリージョンは region に記録する。
	Region string

	// active はユーザーが有効か（退会していないか）。
	active *timeline[bool]
	// region は所属するリージョンの履歴。
	region *timeline[string]
}

// Window は投稿期間。LocalDate が同じ日の投稿で、他人の投稿のゲートが開く。
type Window struct {
	ID        string
	Region    string
	LocalDate time.Time
}

type Post struct {
	ID       string
	AuthorID string
	WindowID string
	ImageID  string
	// ImageBytes は投稿した画像を作り直す。Router が返すダイジェストは要求ごとに種が違うので、
	// 比べるたびに画像そのものが要る（写真のアーカイブから作り直せるので持たない）。
	ImageBytes func() []byte
	Caption    string
	PostedAt   time.Time
	LocalDate  time.Time

	caption    *timeline[string]
	visibility *timeline[string]
	deleted    *timeline[bool]
}

// pendingPost は ID がまだわからない投稿の作成。応答が来れば Post になり、結果が
// わからなければ、ID の知らない投稿として残り続ける。
type pendingPost struct {
	authorID string
	exists   *timeline[bool]
}

type pair [2]string

func pairOf(a, b string) pair {
	return pair{min(a, b), max(a, b)}
}

type Model struct {
	// Staleness は書き込みが読み取りに反映されるまでに許す遅れ。
	Staleness time.Duration
	// PageSize はフィードの 1 ページの件数（既定 FeedPageSize）。テストで小さくする。
	PageSize int

	// now は時刻の取得。テストで差し替える。
	now func() time.Time

	// mu は読み取り（判定）どうしを並行に進めるため、読み書きで分ける。
	mu      sync.RWMutex
	users   map[string]*User
	windows map[string]*Window
	// current はリージョンごとの開いている投稿期間の ID（なければ ""）。
	current map[string]*timeline[string]
	posts   map[string]*Post
	// byAuthor と byWindow は投稿の索引。
	byAuthor map[string][]*Post
	byWindow map[string][]*Post
	// postExists は投稿が作成済みかどうか。作成のリクエストを送ってから応答までは不明。
	postExists map[string]*timeline[bool]
	// pending は投稿者ごとの、ID のわからない投稿の作成。
	pending map[string][]*pendingPost

	friendships map[pair]*timeline[bool]
	// friendCandidates は友達だったことがあるかもしれない相手。友達の友達の判定に使う。
	friendCandidates map[string]map[string]bool
	// blocks はブロックする人とされる人の組（向きあり）。
	blocks  map[[2]string]*timeline[bool]
	follows map[[2]string]*timeline[bool]
	// followCandidates はフォローしたことがあるかもしれない相手。following フィードの判定に使う。
	followCandidates map[string]map[string]bool

	// comments は投稿ごとの bench が書いたコメント、pendingComments は ID のわからない作成。
	comments        map[string][]*Comment
	commentByID     map[string]*Comment
	pendingComments map[string][]*timeline[bool]
	// reactions は投稿とリアクションした人の組ごとの、有効な RealMoji の ID（なければ ""、
	// 置き換えの応答を待つ間は "?"）。
	reactions    map[[2]string]*timeline[string]
	reactionsFor map[string][]string
}

func New() *Model {
	return &Model{
		PageSize:         FeedPageSize,
		now:              time.Now,
		users:            map[string]*User{},
		windows:          map[string]*Window{},
		current:          map[string]*timeline[string]{},
		posts:            map[string]*Post{},
		byAuthor:         map[string][]*Post{},
		byWindow:         map[string][]*Post{},
		postExists:       map[string]*timeline[bool]{},
		pending:          map[string][]*pendingPost{},
		friendships:      map[pair]*timeline[bool]{},
		friendCandidates: map[string]map[string]bool{},
		blocks:           map[[2]string]*timeline[bool]{},
		follows:          map[[2]string]*timeline[bool]{},
		followCandidates: map[string]map[string]bool{},
		comments:         map[string][]*Comment{},
		commentByID:      map[string]*Comment{},
		pendingComments:  map[string][]*timeline[bool]{},
		reactions:        map[[2]string]*timeline[string]{},
		reactionsFor:     map[string][]string{},
	}
}

// LoadDataset は初期データのユーザーと関係を読み込む。初期データの投稿期間はすべて終了して
// いて、その投稿がフィードに出ることはないので、投稿は読み込まない。
func (m *Model) LoadDataset(world *dataset.World) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range world.Users {
		m.users[u.ID] = &User{ID: u.ID, Username: u.Username, SecretKey: u.SecretKey, Region: u.Region, active: newTimeline(u.Status == "active"), region: newTimeline(u.Region)}
	}
	for _, f := range world.Friendships {
		m.friendship(f.UserLowID, f.UserHighID).initial = true
	}
	for _, b := range world.Blocks {
		m.block(b.BlockerID, b.BlockedID).initial = true
	}
	for _, f := range world.Follows {
		m.follow(f.FollowerID, f.FolloweeID).initial = true
	}
}

// AddUser は bench が登録したユーザーを加える。
func (m *Model) AddUser(u User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u.active = newTimeline(true)
	u.region = newTimeline(u.Region)
	m.users[u.ID] = &u
}

// BeginMoveUser はユーザーが所属するリージョンを region に変える書き込みを始める。
func (m *Model) BeginMoveUser(userID, region string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.users[userID].region, region)
	return w
}

func (m *Model) User(id string) *User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.users[id]
}

func (m *Model) friendship(a, b string) *timeline[bool] {
	key := pairOf(a, b)
	if m.friendships[key] == nil {
		m.friendships[key] = newTimeline(false)
		for _, x := range [][2]string{{a, b}, {b, a}} {
			if m.friendCandidates[x[0]] == nil {
				m.friendCandidates[x[0]] = map[string]bool{}
			}
			m.friendCandidates[x[0]][x[1]] = true
		}
	}
	return m.friendships[key]
}

func (m *Model) block(blocker, blocked string) *timeline[bool] {
	key := [2]string{blocker, blocked}
	if m.blocks[key] == nil {
		m.blocks[key] = newTimeline(false)
	}
	return m.blocks[key]
}

func (m *Model) follow(follower, followee string) *timeline[bool] {
	key := [2]string{follower, followee}
	if m.follows[key] == nil {
		m.follows[key] = newTimeline(false)
		if m.followCandidates[follower] == nil {
			m.followCandidates[follower] = map[string]bool{}
		}
		m.followCandidates[follower][followee] = true
	}
	return m.follows[key]
}

// noWindow は投稿期間を一度も開いていないリージョンの、現在の投稿期間。読み取り専用。
var noWindow = newTimeline("")

// currentWindowOf は region の現在の投稿期間の履歴を読む。読み取りのロックの中で使えるよう、
// まだなければ作らずに noWindow を返す。
func (m *Model) currentWindowOf(region string) *timeline[string] {
	if tl := m.current[region]; tl != nil {
		return tl
	}
	return noWindow
}

func (m *Model) currentWindow(region string) *timeline[string] {
	if m.current[region] == nil {
		m.current[region] = newTimeline("")
	}
	return m.current[region]
}

func (m *Model) newWrite() *Write {
	return &Write{model: m}
}

// BeginOpenWindow は投稿期間を開く書き込みを記録する。開いた期間の ID は応答でわかるので、
// 記録は応答のあとに OpenedWindow で行う。送信中は、そのリージョンの期間が切り替わった
// かもしれないものとして "?" を加えておく。
func (m *Model) BeginOpenWindow(region string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.currentWindow(region), "?")
	return w
}

// OpenedWindow は開いた投稿期間を記録し、送信中の "?" を実際の ID に置き換える。
func (m *Model) OpenedWindow(w *Write, window Window) {
	m.mu.Lock()
	m.windows[window.ID] = &window
	for _, c := range m.currentWindow(window.Region).changes {
		if c.value == "?" && c.outcome == inFlight {
			c.value = window.ID
		}
	}
	m.mu.Unlock()
	w.Applied()
}

// HasWindow は投稿期間 id を bench が開いたか。
func (m *Model) HasWindow(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.windows[id] != nil
}

// ErrUnknownWindow は、bench が開いていない投稿期間であることを表す。
var ErrUnknownWindow = errors.New("unknown posting window")

// ErrMaybeWindow は、bench が開こうとして結果のわからなかった（タイムアウトなど）投稿期間
// かもしれないことを表す。bench はその ID を知らないので、その投稿期間では何もしない。
var ErrMaybeWindow = errors.New("posting window whose opening timed out")

// WaitForWindow は region の投稿期間 id が記録されるまで待つ。通知は、投稿期間を開いた
// 管理者への応答より先に届くことがあるので、その投稿期間に投稿する前に呼ぶ。id を知らず、
// region で開いている最中の投稿期間もなければ ErrUnknownWindow を返す。region で最後に
// 開こうとした投稿期間か、待ち始めたときに開いている最中だった投稿期間の結果がわからなければ
// ErrMaybeWindow を返す。
func (m *Model) WaitForWindow(ctx context.Context, region, id string) (*Window, error) {
	return m.WaitForNotifiedWindow(ctx, region, id, time.Time{})
}

// WaitForNotifiedWindow は WaitForWindow と同じだが、received に受け取った通知の投稿期間を
// 待つ。受け取るより前に送った開く要求の結果がわからなければ、そのあとで次の投稿期間が
// 開いていても ErrMaybeWindow を返す（ユーザーが通知を受けてから処理するまでに次の日が
// 始まることがある）。
func (m *Model) WaitForNotifiedWindow(ctx context.Context, region, id string, received time.Time) (*Window, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	// 待ち始めたときに開いている最中だった要求。待つ間に次の投稿期間が開いても、これらの
	// どれかの結果がわからなければ、通知はその投稿期間のものだったかもしれない。
	var waitedFor []*change[string]
	for first := true; ; first = false {
		m.mu.RLock()
		window := m.windows[id]
		changes := m.currentWindowOf(region).changes
		if first {
			for _, c := range changes {
				if c.outcome == inFlight {
					waitedFor = append(waitedFor, c)
				}
			}
		}
		opening := slices.ContainsFunc(changes, func(c *change[string]) bool { return c.outcome == inFlight })
		maybe := len(changes) > 0 && changes[len(changes)-1].outcome == unknown ||
			slices.ContainsFunc(waitedFor, func(c *change[string]) bool { return c.outcome == unknown }) ||
			!received.IsZero() && slices.ContainsFunc(changes, func(c *change[string]) bool { return c.outcome == unknown && !c.sent.After(received) })
		m.mu.RUnlock()
		if window != nil {
			return window, nil
		}
		if !opening && maybe {
			return nil, ErrMaybeWindow
		}
		if !opening {
			return nil, ErrUnknownWindow
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// BeginFriendship は a と b の友達関係を作る（true）か消す（false）書き込みを記録する。
func (m *Model) BeginFriendship(a, b string, friends bool) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.friendship(a, b), friends)
	return w
}

// BeginDeleteAccount は userID の退会を記録する。退会したユーザーの投稿、関係、交流は
// ほかの人から見えなくなる。
func (m *Model) BeginDeleteAccount(userID string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.users[userID].active, false)
	return w
}

// BeginBlock は blocker が blocked をブロックする書き込みを記録する。ブロックは 2 人の
// 友達関係とフォローも消す。
func (m *Model) BeginBlock(blocker, blocked string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.block(blocker, blocked), true)
	begin(w, m.friendship(blocker, blocked), false)
	begin(w, m.follow(blocker, blocked), false)
	begin(w, m.follow(blocked, blocker), false)
	return w
}

// BeginUnblock は blocker が blocked のブロックを解除する書き込みを記録する。解除しても
// 友達関係やフォローは戻らない。
func (m *Model) BeginUnblock(blocker, blocked string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.block(blocker, blocked), false)
	return w
}

// BeginFollow は follower が followee をフォローする（true）か解除する（false）書き込みを記録する。
func (m *Model) BeginFollow(follower, followee string, follow bool) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.follow(follower, followee), follow)
	return w
}

// PostCreation は ID のわからない投稿の作成。
type PostCreation struct {
	*Write
	pending *pendingPost
}

// BeginCreatePost は authorID の投稿の作成を記録する。
func (m *Model) BeginCreatePost(authorID string) *PostCreation {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	p := &pendingPost{authorID: authorID, exists: newTimeline(false)}
	begin(w, p.exists, true)
	m.pending[authorID] = append(m.pending[authorID], p)
	// 拒否された作成は一度も反映されていないので、残しておく意味がない。
	w.after = func(o outcome) {
		if o == rejected {
			m.removePending(p)
		}
	}
	return &PostCreation{Write: w, pending: p}
}

// removePending は ID のわからない投稿の作成から p を除く。m.mu を書き込みで持って呼ぶ。
func (m *Model) removePending(p *pendingPost) {
	list := m.pending[p.authorID]
	if i := slices.Index(list, p); i >= 0 {
		list = slices.Delete(list, i, i+1)
	}
	if len(list) == 0 {
		delete(m.pending, p.authorID)
	} else {
		m.pending[p.authorID] = list
	}
}

// Created は作成できた投稿を記録する。以後、この作成は ID のわかる投稿として扱う。
func (m *Model) Created(creation *PostCreation, post Post, visibility string) {
	m.mu.Lock()
	window := m.windows[post.WindowID]
	if window != nil {
		post.LocalDate = window.LocalDate
	}
	post.caption = newTimeline(post.Caption)
	post.visibility = newTimeline(visibility)
	post.deleted = newTimeline(false)
	m.posts[post.ID] = &post
	m.byAuthor[post.AuthorID] = append(m.byAuthor[post.AuthorID], &post)
	m.byWindow[post.WindowID] = append(m.byWindow[post.WindowID], &post)
	m.postExists[post.ID] = creation.pending.exists
	creation.after = func(outcome) { m.removePending(creation.pending) }
	m.mu.Unlock()
	creation.Applied()
}

func (m *Model) Post(id string) *Post {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.posts[id]
}

func (m *Model) BeginUpdateVisibility(postID, visibility string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.posts[postID].visibility, visibility)
	return w
}

func (m *Model) BeginUpdateCaption(postID, caption string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.posts[postID].caption, caption)
	return w
}

func (m *Model) BeginDeletePost(postID string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.posts[postID].deleted, true)
	return w
}
