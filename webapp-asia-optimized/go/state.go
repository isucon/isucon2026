package isureal

import (
	"context"
	"github.com/google/uuid"
	"slices"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// 投稿期間・アクティブな投稿・関係をメモリに保持する。
// 書き込みはそれぞれのトランザクションのコミット直前（DB の行ロックを保持している間）に反映する。
type memState struct {
	mu sync.RWMutex
	// friends の書き換えは mu に加えて friendsMu も取る（mu を読みロック中のコールバックから友達の友達を判定できるように）
	friendsMu sync.RWMutex
	loaded    bool

	windows      map[string]PostingWindow // id -> window
	activeWindow map[string]string        // region -> window id

	activePosts   map[string]*Post   // アクティブな期間の削除されていない投稿
	sortedPosts   []*Post            // activePosts を (posted_at, id) 昇順に並べたもの
	postsByAuthor map[string][]*Post // 著者ごとの activePosts（posted_at 昇順）

	minTrackedDate time.Time
	postDateCount  map[string]map[string]int // author -> local_date -> 削除されていない投稿数（minTrackedDate 以降）
	deletedCount   map[string]map[string]int // author -> local_date -> 削除された投稿数（minTrackedDate 以降）

	friends   map[string]map[string]bool
	blocking  map[string]map[string]bool // blocker -> blocked
	blockedBy map[string]map[string]bool // blocked -> blocker
	following map[string]map[string]bool // follower -> followee

	notified map[string]map[string]bool // window id -> 通知対象のユーザー
}

var state = &memState{}

func dateKey(t time.Time) string { return t.Format(time.DateOnly) }

func postLess(a, b *Post) int {
	if c := a.PostedAt.Compare(b.PostedAt); c != 0 {
		return c
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	}
	return 0
}

func addEdge(m map[string]map[string]bool, a, b string) {
	if m[a] == nil {
		m[a] = map[string]bool{}
	}
	m[a][b] = true
}

func removeEdge(m map[string]map[string]bool, a, b string) {
	delete(m[a], b)
}

func (s *memState) reset() {
	s.mu.Lock()
	s.loaded = false
	s.mu.Unlock()
}

func (s *memState) ensureLoaded(ctx context.Context, db *sqlx.DB) error {
	s.mu.RLock()
	loaded := s.loaded
	s.mu.RUnlock()
	if loaded {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return nil
	}
	return s.loadLocked(ctx, db)
}

func (s *memState) loadLocked(ctx context.Context, db *sqlx.DB) error {
	var windows []PostingWindow
	if err := db.SelectContext(ctx, &windows, `SELECT id, region, local_date, starts_at, on_time_deadline, ended_at FROM posting_windows`); err != nil {
		return err
	}
	s.notified = map[string]map[string]bool{}
	s.windows = make(map[string]PostingWindow, len(windows))
	s.activeWindow = map[string]string{}
	// 新しい投稿期間の日付は必ずそのリージョンの既存の最大日付より後なので、
	// リージョンごとの最大日付の最小値以降だけを追跡すれば十分
	maxDate := map[string]time.Time{}
	for _, w := range windows {
		s.windows[w.ID] = w
		if w.EndedAt == nil {
			s.activeWindow[w.Region] = w.ID
		}
		if w.LocalDate.After(maxDate[w.Region]) {
			maxDate[w.Region] = w.LocalDate
		}
	}
	var minDate time.Time
	if len(maxDate) == 3 {
		first := true
		for _, d := range maxDate {
			if first || d.Before(minDate) {
				minDate, first = d, false
			}
		}
	}
	s.minTrackedDate = minDate

	var posts []Post
	if err := db.SelectContext(ctx, &posts, `
  SELECT `+postColumns+`
  FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  WHERE p.deleted_at IS NULL AND w.ended_at IS NULL
 `); err != nil {
		return err
	}
	s.activePosts = make(map[string]*Post, len(posts))
	s.sortedPosts = make([]*Post, 0, len(posts))
	for i := range posts {
		p := &posts[i]
		s.activePosts[p.ID] = p
		s.sortedPosts = append(s.sortedPosts, p)
	}
	slices.SortFunc(s.sortedPosts, postLess)
	s.rebuildAuthorIndexLocked()

	var counts []struct {
		AuthorID  string    `db:"author_id"`
		LocalDate time.Time `db:"local_date"`
		Deleted   bool      `db:"deleted"`
		Count     int       `db:"count"`
	}
	if err := db.SelectContext(ctx, &counts, `
  SELECT p.author_id, w.local_date, p.deleted_at IS NOT NULL AS deleted, count(*) AS count
  FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  WHERE w.local_date >= $1
  GROUP BY 1, 2, 3
 `, minDate); err != nil {
		return err
	}
	s.postDateCount = map[string]map[string]int{}
	s.deletedCount = map[string]map[string]int{}
	for _, c := range counts {
		m := s.postDateCount
		if c.Deleted {
			m = s.deletedCount
		}
		if m[c.AuthorID] == nil {
			m[c.AuthorID] = map[string]int{}
		}
		m[c.AuthorID][dateKey(c.LocalDate)] = c.Count
	}

	var pairs []struct {
		A string `db:"a"`
		B string `db:"b"`
	}
	friends := map[string]map[string]bool{}
	if err := db.SelectContext(ctx, &pairs, `SELECT user_low_id AS a, user_high_id AS b FROM friendships`); err != nil {
		return err
	}
	for _, p := range pairs {
		addEdge(friends, p.A, p.B)
		addEdge(friends, p.B, p.A)
	}
	s.friendsMu.Lock()
	s.friends = friends
	s.friendsMu.Unlock()
	pairs = nil
	s.blocking = map[string]map[string]bool{}
	s.blockedBy = map[string]map[string]bool{}
	if err := db.SelectContext(ctx, &pairs, `SELECT blocker_id AS a, blocked_id AS b FROM block_relations`); err != nil {
		return err
	}
	for _, p := range pairs {
		addEdge(s.blocking, p.A, p.B)
		addEdge(s.blockedBy, p.B, p.A)
	}
	pairs = nil
	s.following = map[string]map[string]bool{}
	if err := db.SelectContext(ctx, &pairs, `SELECT follower_id AS a, followee_id AS b FROM follows`); err != nil {
		return err
	}
	for _, p := range pairs {
		addEdge(s.following, p.A, p.B)
	}
	s.loaded = true
	return nil
}

func (s *memState) rebuildAuthorIndexLocked() {
	s.postsByAuthor = map[string][]*Post{}
	for _, p := range s.sortedPosts {
		s.postsByAuthor[p.AuthorID] = append(s.postsByAuthor[p.AuthorID], p)
	}
}

func removePostPtr(list []*Post, p *Post) []*Post {
	for i, q := range list {
		if q == p {
			return slices.Delete(list, i, i+1)
		}
	}
	return list
}

// ---- 投稿期間 ----

func (s *memState) currentWindow(region string) (PostingWindow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.activeWindow[region]
	if !ok {
		return PostingWindow{}, false
	}
	return s.windows[id], true
}

func (s *memState) openWindow(w PostingWindow, notifiedUserIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notified := make(map[string]bool, len(notifiedUserIDs))
	for _, id := range notifiedUserIDs {
		notified[id] = true
	}
	s.notified[w.ID] = notified
	if oldID, ok := s.activeWindow[w.Region]; ok {
		old := s.windows[oldID]
		endedAt := w.StartsAt
		old.EndedAt = &endedAt
		s.windows[oldID] = old
		kept := s.sortedPosts[:0]
		for _, p := range s.sortedPosts {
			if p.PostingWindowID == oldID {
				delete(s.activePosts, p.ID)
				continue
			}
			kept = append(kept, p)
		}
		clear(s.sortedPosts[len(kept):])
		s.sortedPosts = kept
		s.rebuildAuthorIndexLocked()
	}
	s.windows[w.ID] = w
	s.activeWindow[w.Region] = w.ID
}

// ---- 投稿 ----

func (s *memState) addPostDateCountLocked(authorID string, localDate time.Time, delta int) {
	if localDate.Before(s.minTrackedDate) {
		return
	}
	if s.postDateCount[authorID] == nil {
		s.postDateCount[authorID] = map[string]int{}
	}
	s.postDateCount[authorID][dateKey(localDate)] += delta
}

func (s *memState) addPost(post Post) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.windows[post.PostingWindowID]
	if !ok {
		return
	}
	s.addPostDateCountLocked(post.AuthorID, w.LocalDate, 1)
	if w.EndedAt != nil {
		return
	}
	p := post
	s.activePosts[p.ID] = &p
	i, _ := slices.BinarySearchFunc(s.sortedPosts, &p, postLess)
	s.sortedPosts = slices.Insert(s.sortedPosts, i, &p)
	list := s.postsByAuthor[p.AuthorID]
	j, _ := slices.BinarySearchFunc(list, &p, postLess)
	s.postsByAuthor[p.AuthorID] = slices.Insert(list, j, &p)
}

func (s *memState) updatePost(post Post) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.activePosts[post.ID]; ok {
		p.Caption = post.Caption
		p.Visibility = post.Visibility
	}
}

func (s *memState) deletePost(post Post) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.windows[post.PostingWindowID]; ok {
		s.addPostDateCountLocked(post.AuthorID, w.LocalDate, -1)
		if !w.LocalDate.Before(s.minTrackedDate) {
			if s.deletedCount[post.AuthorID] == nil {
				s.deletedCount[post.AuthorID] = map[string]int{}
			}
			s.deletedCount[post.AuthorID][dateKey(w.LocalDate)]++
		}
	}
	p, ok := s.activePosts[post.ID]
	if !ok {
		return
	}
	delete(s.activePosts, post.ID)
	if i, found := slices.BinarySearchFunc(s.sortedPosts, p, postLess); found {
		s.sortedPosts = slices.Delete(s.sortedPosts, i, i+1)
	}
	s.postsByAuthor[p.AuthorID] = removePostPtr(s.postsByAuthor[p.AuthorID], p)
}

// アクティブな投稿を新しい順にたどる（f が false を返したら終了）
func (s *memState) eachActivePostDesc(f func(p Post) bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.sortedPosts) - 1; i >= 0; i-- {
		if !f(*s.sortedPosts[i]) {
			return
		}
	}
}

// 指定した著者たちのアクティブな投稿を新しい順に集める（f が false を返したものは除く）
func (s *memState) activePostsByAuthors(authorIDs map[string]bool, f func(p *Post) bool) []Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Post
	for id := range authorIDs {
		for _, p := range s.postsByAuthor[id] {
			if f(p) {
				result = append(result, *p)
			}
		}
	}
	slices.SortFunc(result, func(a, b Post) int { return postLess(&b, &a) })
	return result
}

// その日の投稿数と削除数（追跡していない古い日付なら ok=false）
func (s *memState) postCounts(userID string, localDate time.Time) (active, deleted int, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if localDate.Before(s.minTrackedDate) {
		return 0, 0, false
	}
	key := dateKey(localDate)
	return s.postDateCount[userID][key], s.deletedCount[userID][key], true
}

func (s *memState) windowByID(id string) (PostingWindow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.windows[id]
	return w, ok
}

// アクティブな期間の削除されていない投稿
func (s *memState) activePost(id string) (Post, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.activePosts[id]
	if !ok {
		return Post{}, false
	}
	return *p, true
}

func (s *memState) hasPostedToday(userID, region string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.activeWindow[region]
	if !ok {
		return false
	}
	return s.postDateCount[userID][dateKey(s.windows[id].LocalDate)] > 0
}

// ---- 関係 ----

func (s *memState) addFriendship(a, b string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.friendsMu.Lock()
	defer s.friendsMu.Unlock()
	addEdge(s.friends, a, b)
	addEdge(s.friends, b, a)
}

func (s *memState) removeFriendship(a, b string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.friendsMu.Lock()
	defer s.friendsMu.Unlock()
	removeEdge(s.friends, a, b)
	removeEdge(s.friends, b, a)
}

func (s *memState) addFollow(follower, followee string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addEdge(s.following, follower, followee)
}

func (s *memState) removeFollow(follower, followee string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removeEdge(s.following, follower, followee)
}

// ブロックすると友達関係と双方向のフォローも消える
func (s *memState) block(blocker, blocked string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addEdge(s.blocking, blocker, blocked)
	addEdge(s.blockedBy, blocked, blocker)
	s.friendsMu.Lock()
	removeEdge(s.friends, blocker, blocked)
	removeEdge(s.friends, blocked, blocker)
	s.friendsMu.Unlock()
	removeEdge(s.following, blocker, blocked)
	removeEdge(s.following, blocked, blocker)
}

func (s *memState) unblock(blocker, blocked string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removeEdge(s.blocking, blocker, blocked)
	removeEdge(s.blockedBy, blocked, blocker)
}

func (s *memState) followees(userID string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]bool, len(s.following[userID]))
	for id := range s.following[userID] {
		result[id] = true
	}
	return result
}

// 閲覧者のブロック・友達・友達の友達を集める
func (s *memState) fillViewerRelations(viewer *viewerContext) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	viewer.Blocked = make(map[string]bool, len(s.blocking[viewer.ID])+len(s.blockedBy[viewer.ID]))
	for id := range s.blocking[viewer.ID] {
		viewer.Blocked[id] = true
	}
	for id := range s.blockedBy[viewer.ID] {
		viewer.Blocked[id] = true
	}
	for friend := range s.friends[viewer.ID] {
		viewer.Friends[friend] = true
	}
}

// 友達の友達の集合（friends-of-friends フィードで候補の著者を列挙するときだけ作る）
func (s *memState) fillFriendsOfFriends(viewer *viewerContext) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	viewer.FriendsOfFriends = map[string]bool{}
	for friend := range viewer.Friends {
		for fof := range s.friends[friend] {
			viewer.FriendsOfFriends[fof] = true
		}
	}
}

// userID が viewer の友達の誰かの友達か（mu は取らないので、mu を読みロック中のコールバックからも呼べる）
func (s *memState) isFriendOfFriend(viewer *viewerContext, userID string) bool {
	s.friendsMu.RLock()
	defer s.friendsMu.RUnlock()
	for friend := range viewer.Friends {
		if s.friends[friend][userID] {
			return true
		}
	}
	return false
}

var notificationNamespace = uuid.MustParse("5c1b6f3e-6d0e-4c2a-9a55-6b0d8f7a2c11")

// connectedAt より後に開始され、まだ終わっていない投稿期間の通知のうち最も古いもの
func (s *memState) pendingNotification(userID string, connectedAt time.Time) (getNotificationsResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found *PostingWindow
	for _, id := range s.activeWindow {
		w := s.windows[id]
		if !w.StartsAt.After(connectedAt) || !s.notified[id][userID] {
			continue
		}
		if found == nil || w.StartsAt.Before(found.StartsAt) {
			found = &w
		}
	}
	if found == nil {
		return getNotificationsResponse{}, false
	}
	return getNotificationsResponse{
		ID:              uuid.NewSHA1(notificationNamespace, []byte(found.ID+":"+userID)).String(),
		PostingWindowID: found.ID,
		Region:          found.Region,
		CreatedAt:       found.StartsAt,
	}, true
}
