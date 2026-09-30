package isureal

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// hotState is a regional in-memory copy of everything a feed needs: users,
// relationships, sessions, the open posting windows and their posts.
//
// Every committed write that changes this state is pushed, with a version, to
// the hot state of every region before the write responds (see broadcast). A
// version is drawn from change_seq inside the writing transaction while its
// row locks are held, so versions of writes to the same key follow commit
// order. Each key keeps the state of the highest version seen, so events may
// arrive in any order. A read that starts after a write's response therefore
// observes the write in every region, without a round trip to the database.
type hotState struct {
	mu     sync.RWMutex
	loaded bool

	users    map[string]*hotUser
	sessions map[string]hotSession // token hash -> session
	// userSession is the token hash of each user's active session.
	userSession map[string]hotSession

	friends  map[string]map[string]bool
	blocks   map[[2]string]bool // blocker, blocked
	follows  map[[2]string]bool // follower, followee
	versions map[relationKey]int64

	windows map[string]*hotWindow // region -> open window
	closed  map[string]bool       // windows seen to close
	posts   map[string]*hotPost
	byImage map[string]string // post image -> post
	// sorted holds posts newest first (posted_at, id descending), and byAuthor
	// each author's posts, so a feed page stops after the first matches.
	sorted   []*hotPost
	byAuthor map[string][]*hotPost
}

type hotUser struct {
	User
	Version int64
}

type hotSession struct {
	UserID    string
	TokenHash string
	Version   int64
}

type hotWindow struct {
	ID             string
	Region         string
	LocalDate      time.Time
	OnTimeDeadline time.Time
	Version        int64
}

type hotPost struct {
	Post
	Deleted bool
	Version int64
}

type relationKey struct {
	Kind string
	A, B string
}

var hot = newHotState()

func newHotState() *hotState {
	s := &hotState{}
	s.reset()
	return s
}

func (s *hotState) reset() {
	s.loaded = false
	s.users = map[string]*hotUser{}
	s.sessions = map[string]hotSession{}
	s.userSession = map[string]hotSession{}
	s.friends = map[string]map[string]bool{}
	s.blocks = map[[2]string]bool{}
	s.follows = map[[2]string]bool{}
	s.versions = map[relationKey]int64{}
	s.windows = map[string]*hotWindow{}
	s.closed = map[string]bool{}
	s.posts = map[string]*hotPost{}
	s.byImage = map[string]string{}
	s.sorted = nil
	s.byAuthor = map[string][]*hotPost{}
}

func newerPost(a, b *hotPost) int {
	return cmp.Or(b.PostedAt.Compare(a.PostedAt), cmp.Compare(b.ID, a.ID))
}

// addPost indexes a post that is not yet in the state.
func (s *hotState) addPost(p *hotPost) {
	s.posts[p.ID] = p
	s.byImage[p.ImageID] = p.ID
	s.byAuthor[p.AuthorID] = append(s.byAuthor[p.AuthorID], p)
	i, _ := slices.BinarySearchFunc(s.sorted, p, newerPost)
	s.sorted = slices.Insert(s.sorted, i, p)
}

// dropClosedPosts forgets the posts of closed windows.
func (s *hotState) dropClosedPosts() {
	keep := s.sorted[:0]
	for _, p := range s.sorted {
		if s.closed[p.PostingWindowID] {
			delete(s.posts, p.ID)
			delete(s.byImage, p.ImageID)
			continue
		}
		keep = append(keep, p)
	}
	clear(s.sorted[len(keep):])
	s.sorted = keep
	for author, posts := range s.byAuthor {
		posts = slices.DeleteFunc(posts, func(p *hotPost) bool { return s.closed[p.PostingWindowID] })
		if len(posts) == 0 {
			delete(s.byAuthor, author)
		} else {
			s.byAuthor[author] = posts
		}
	}
}

func friendKey(a, b string) relationKey {
	return relationKey{Kind: "friend", A: min(a, b), B: max(a, b)}
}

// load replaces the state with a consistent snapshot of the database. Events
// that arrive while loading are applied afterwards by the caller's lock order:
// load holds the write lock, so they wait and then apply on top.
func (s *hotState) load(ctx context.Context, db *sqlx.DB) error {
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var users []User
	if err := tx.SelectContext(ctx, &users, `SELECT `+userProjection+` FROM users`); err != nil {
		return err
	}
	var friendships []struct {
		Low  string `db:"user_low_id"`
		High string `db:"user_high_id"`
	}
	if err := tx.SelectContext(ctx, &friendships, `SELECT user_low_id, user_high_id FROM friendships`); err != nil {
		return err
	}
	var pairs []struct {
		Kind string `db:"kind"`
		A    string `db:"a"`
		B    string `db:"b"`
	}
	if err := tx.SelectContext(ctx, &pairs, `SELECT 'block' AS kind, blocker_id AS a, blocked_id AS b FROM block_relations
      UNION ALL SELECT 'follow', follower_id, followee_id FROM follows`); err != nil {
		return err
	}
	var sessions []struct {
		UserID    string `db:"user_id"`
		TokenHash string `db:"token_hash"`
	}
	if err := tx.SelectContext(ctx, &sessions, `SELECT user_id, token_hash FROM login_sessions WHERE active = true`); err != nil {
		return err
	}
	var windows []hotWindow
	if err := tx.SelectContext(ctx, &windows, `SELECT id AS "id", region AS "region", local_date AS "localdate", on_time_deadline AS "ontimedeadline"
      FROM posting_windows WHERE ended_at IS NULL`); err != nil {
		return err
	}
	var posts []struct {
		Post
		Deleted bool `db:"deleted"`
	}
	if err := tx.SelectContext(ctx, &posts, `SELECT `+postColumns+`, p.deleted_at IS NOT NULL AS deleted
      FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id WHERE w.ended_at IS NULL`); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
	for _, u := range users {
		if err := u.setImageKey(); err != nil {
			return err
		}
		s.users[u.ID] = &hotUser{User: u}
	}
	for _, f := range friendships {
		s.setFriend(f.Low, f.High, true)
	}
	for _, p := range pairs {
		if p.Kind == "block" {
			s.blocks[[2]string{p.A, p.B}] = true
		} else {
			s.follows[[2]string{p.A, p.B}] = true
		}
	}
	for _, session := range sessions {
		hs := hotSession{UserID: session.UserID, TokenHash: session.TokenHash}
		s.sessions[session.TokenHash] = hs
		s.userSession[session.UserID] = hs
	}
	for i := range windows {
		s.windows[windows[i].Region] = &windows[i]
	}
	for _, p := range posts {
		s.addPost(&hotPost{Post: p.Post, Deleted: p.Deleted})
	}
	s.loaded = true
	return nil
}

func (s *hotState) setFriend(a, b string, exists bool) {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if exists {
			if s.friends[pair[0]] == nil {
				s.friends[pair[0]] = map[string]bool{}
			}
			s.friends[pair[0]][pair[1]] = true
		} else if s.friends[pair[0]] != nil {
			delete(s.friends[pair[0]], pair[1])
		}
	}
}

// newer reports whether version is newer than what key holds, and records it.
func (s *hotState) newer(key relationKey, version int64) bool {
	if version <= s.versions[key] {
		return false
	}
	s.versions[key] = version
	return true
}

// apply applies events. It is idempotent and order independent per key.
func (s *hotState) apply(events []stateEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		switch e.Kind {
		case "user":
			u := e.User.user()
			if current := s.users[u.ID]; current == nil || (e.Version > current.Version && current.Status != "deleted") {
				if u.setImageKey() == nil {
					s.users[u.ID] = &hotUser{User: u, Version: e.Version}
				}
			}
		case "user_deleted":
			if current := s.users[e.A]; current != nil {
				deleted := *current
				deleted.Status = "deleted"
				deleted.Version = max(e.Version, current.Version)
				s.users[e.A] = &deleted
			}
		case "session":
			if current := s.userSession[e.A]; e.Version > current.Version {
				delete(s.sessions, current.TokenHash)
				session := hotSession{UserID: e.A, TokenHash: e.B, Version: e.Version}
				s.userSession[e.A] = session
				if e.B != "" {
					s.sessions[e.B] = session
				}
			}
		case "friend":
			if s.newer(friendKey(e.A, e.B), e.Version) {
				s.setFriend(e.A, e.B, e.Exists)
			}
		case "block", "follow":
			if s.newer(relationKey{Kind: e.Kind, A: e.A, B: e.B}, e.Version) {
				pair := [2]string{e.A, e.B}
				target := s.blocks
				if e.Kind == "follow" {
					target = s.follows
				}
				if e.Exists {
					target[pair] = true
				} else {
					delete(target, pair)
				}
			}
		case "window":
			w := e.Window
			if current := s.windows[w.Region]; current == nil || e.Version > current.Version {
				if current != nil && current.ID != w.ID {
					s.closed[current.ID] = true
				}
				w.Version = e.Version
				s.windows[w.Region] = w
				s.dropClosedPosts()
			}
		case "post":
			p := e.Post
			if s.closed[p.PostingWindowID] {
				continue
			}
			switch current := s.posts[p.ID]; {
			case current == nil:
				s.addPost(&hotPost{Post: *p, Version: e.Version})
			case e.Version > current.Version && !current.Deleted:
				// Only the caption and visibility change; the order keys do not.
				current.Caption, current.Visibility, current.Version = p.Caption, p.Visibility, e.Version
			}
		case "post_deleted":
			if current := s.posts[e.A]; current != nil {
				current.Deleted = true
				current.Version = max(e.Version, current.Version)
			}
		}
	}
}

// authenticate returns the active user of a session token hash if the session
// is known and the user belongs to region (any region if empty).
func (s *hotState) authenticate(tokenHash, region string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded {
		return User{}, false
	}
	session, ok := s.sessions[tokenHash]
	if !ok {
		return User{}, false
	}
	u := s.users[session.UserID]
	if u == nil || u.Status != "active" || (region != "" && u.Region != region) {
		return User{}, false
	}
	return u.User, true
}

type feedCursor struct {
	PostedAt time.Time
	ID       string
}

// feed computes a feed page like getFeedHandler's SQL. ok is false when the
// state cannot answer and the database must be asked.
func (s *hotState) feed(viewerID, feed string, before *feedCursor) (posts []PostWithAuthor, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	viewer := s.users[viewerID]
	if !s.loaded || viewer == nil {
		return nil, false
	}
	if viewer.Status != "active" {
		return nil, true
	}
	openWindows := map[string]*hotWindow{}
	for _, w := range s.windows {
		openWindows[w.ID] = w
	}
	current := s.windows[viewer.Region]
	if current == nil {
		return nil, true
	}
	postedToday := false
	for _, p := range s.byAuthor[viewerID] {
		if !p.Deleted {
			if w := openWindows[p.PostingWindowID]; w != nil && w.LocalDate.Equal(current.LocalDate) {
				postedToday = true
				break
			}
		}
	}
	if !postedToday {
		return nil, true
	}
	friends := s.friends[viewerID]
	var fof map[string]bool
	inFOF := func(id string) bool {
		if fof == nil {
			fof = map[string]bool{}
			for friend := range friends {
				for other := range s.friends[friend] {
					fof[other] = true
				}
			}
		}
		return fof[id]
	}
	start := 0
	if before != nil {
		start, _ = slices.BinarySearchFunc(s.sorted, before, func(p *hotPost, c *feedCursor) int {
			return cmp.Or(c.PostedAt.Compare(p.PostedAt), cmp.Compare(c.ID, p.ID))
		})
		// Skip the cursor post itself.
		for start < len(s.sorted) && !s.sorted[start].PostedAt.Before(before.PostedAt) &&
			!(s.sorted[start].PostedAt.Equal(before.PostedAt) && s.sorted[start].ID < before.ID) {
			start++
		}
	}
	for _, p := range s.sorted[start:] {
		w := openWindows[p.PostingWindowID]
		if w == nil || p.Deleted || p.AuthorID == viewerID {
			continue
		}
		author := s.users[p.AuthorID]
		if author == nil {
			return nil, false
		}
		if author.Status != "active" || s.blocks[[2]string{viewerID, p.AuthorID}] || s.blocks[[2]string{p.AuthorID, viewerID}] {
			continue
		}
		friend := friends[p.AuthorID]
		var member bool
		switch feed {
		case "public":
			member = p.Visibility == "public"
		case "friends":
			member = friend
		case "friends-of-friends":
			member = !friend && (p.Visibility == "public" || p.Visibility == "friends_of_friends") && inFOF(p.AuthorID)
		case "following":
			member = s.follows[[2]string{viewerID, p.AuthorID}] && (p.Visibility == "public" ||
				(friend && (p.Visibility == "friends" || p.Visibility == "friends_of_friends")) ||
				(p.Visibility == "friends_of_friends" && inFOF(p.AuthorID)))
		}
		if !member {
			continue
		}
		post := p.Post
		post.OnTimeDeadline = w.OnTimeDeadline
		post.WindowEndedAt = nil
		posts = append(posts, PostWithAuthor{Post: post, Author: author.User})
		if len(posts) == 51 {
			break
		}
	}
	return posts, true
}

// imageLive reports whether a post image is live, with its owner's signing
// key hash, when the image belongs to a post of an open window.
func (s *hotState) imageLive(imageID string) (live bool, secretKeyHash string, known bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	postID, ok := s.byImage[imageID]
	if !s.loaded || !ok {
		return false, "", false
	}
	p := s.posts[postID]
	author := s.users[p.AuthorID]
	if author == nil {
		return false, "", false
	}
	return !p.Deleted && author.Status == "active", author.SecretKeyHash, true
}
