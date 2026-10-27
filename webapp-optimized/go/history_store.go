package isureal

import (
	"cmp"
	"context"
	"database/sql"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// historyStore answers Memories of the initial data from memory: the posts of
// this region's users in the windows of the initial data, with their live
// comments and RealMojis as of the load. Users are read from the hot state, so
// names, pictures and deletions are current. A post that a write touched
// since the load (a comment, a RealMoji, an edit or a deletion) is dirty, and
// a page with a dirty post, or a period with a window opened after the initial
// data, is read from the database. Writes mark posts dirty in every region
// before they respond (see eventsFor).
type historyStore struct {
	mu      sync.RWMutex
	loaded  bool
	authors map[string][]*historyPost // posts newest first (posted_at, id descending)
	// runDates are the local dates of windows outside the initial data.
	runDates map[time.Time]bool
	dirty    map[string]bool
	posts    map[string]bool
}

type historyPost struct {
	Post
	LocalDate time.Time
	Comments  []Comment  // live at the load, oldest first
	Reactions []RealMoji // live at the load, newest first
}

var history = &historyStore{runDates: map[time.Time]bool{}, dirty: map[string]bool{}}

func (s *historyStore) load(ctx context.Context, db *sqlx.DB, region string) error {
	started := time.Now()
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var users []string
	if err := tx.SelectContext(ctx, &users, `SELECT id FROM users WHERE region = $1`, region); err != nil {
		return err
	}
	var posts []struct {
		Post
		LocalDate time.Time `db:"local_date"`
	}
	if err := tx.SelectContext(ctx, &posts, `SELECT `+postColumns+`, w.local_date
      FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
      JOIN users u ON u.id = p.author_id AND u.region = $1
      WHERE p.deleted_at IS NULL AND w.created_at <= (SELECT created_at FROM initial_data_boundary)`, region); err != nil {
		return err
	}
	var comments []Comment
	if err := tx.SelectContext(ctx, &comments, `SELECT c.id, c.post_id, c.author_id, c.body, c.created_at, c.deleted_at
      FROM comments c JOIN posts p ON p.id = c.post_id AND p.deleted_at IS NULL
      JOIN posting_windows w ON w.id = p.posting_window_id
      JOIN users u ON u.id = p.author_id AND u.region = $1
      WHERE c.deleted_at IS NULL AND w.created_at <= (SELECT created_at FROM initial_data_boundary)`, region); err != nil {
		return err
	}
	var reactions []RealMoji
	if err := tx.SelectContext(ctx, &reactions, `SELECT r.id, r.post_id, r.reactor_id, r.image_id, r.reaction_type, r.created_at, r.replaced_at, r.deleted_at
      FROM realmojis r JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL
      JOIN posting_windows w ON w.id = p.posting_window_id
      JOIN users u ON u.id = p.author_id AND u.region = $1
      WHERE r.replaced_at IS NULL AND r.deleted_at IS NULL AND w.created_at <= (SELECT created_at FROM initial_data_boundary)`, region); err != nil {
		return err
	}
	var runDates []time.Time
	if err := tx.SelectContext(ctx, &runDates, `SELECT DISTINCT local_date FROM posting_windows
      WHERE created_at > (SELECT created_at FROM initial_data_boundary)`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	authors := make(map[string][]*historyPost, len(users))
	for _, id := range users {
		authors[id] = nil
	}
	byID := make(map[string]*historyPost, len(posts))
	for i := range posts {
		p := &historyPost{Post: posts[i].Post, LocalDate: posts[i].LocalDate}
		byID[p.ID] = p
		authors[p.AuthorID] = append(authors[p.AuthorID], p)
	}
	for _, c := range comments {
		if p := byID[c.PostID]; p != nil {
			p.Comments = append(p.Comments, c)
		}
	}
	for _, r := range reactions {
		if p := byID[r.PostID]; p != nil {
			p.Reactions = append(p.Reactions, r)
		}
	}
	for _, list := range authors {
		slices.SortFunc(list, func(a, b *historyPost) int {
			return cmp.Or(b.PostedAt.Compare(a.PostedAt), cmp.Compare(b.ID, a.ID))
		})
		for _, p := range list {
			slices.SortFunc(p.Comments, func(a, b Comment) int {
				return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
			})
			slices.SortFunc(p.Reactions, func(a, b RealMoji) int {
				return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
			})
		}
	}
	postSet := make(map[string]bool, len(byID))
	for id := range byID {
		postSet[id] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.authors, s.posts, s.loaded = authors, postSet, true
	for _, d := range runDates {
		s.runDates[d] = true
	}
	slog.Info("history store", "users", len(users), "posts", len(posts), "comments", len(comments), "realmojis", len(reactions), "elapsed", time.Since(started).Round(time.Millisecond))
	return nil
}

// refresh follows a reset of the database: the windows opened by the load are
// gone, and so are its posts.
func (s *historyStore) refresh(ctx context.Context, db sqlx.QueryerContext) {
	var runDates []time.Time
	err := sqlx.SelectContext(ctx, db, &runDates, `SELECT DISTINCT local_date FROM posting_windows
      WHERE created_at > (SELECT created_at FROM initial_data_boundary)`)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		// Without knowing the windows, nothing is answered from memory.
		s.loaded = false
		slog.Error("history store refresh", "error", err)
		return
	}
	s.runDates = map[time.Time]bool{}
	for _, d := range runDates {
		s.runDates[d] = true
	}
	if s.posts != nil {
		for id := range s.dirty {
			if !s.posts[id] {
				delete(s.dirty, id)
			}
		}
	}
}

func (s *historyStore) touch(postID string) {
	s.mu.Lock()
	s.dirty[postID] = true
	s.mu.Unlock()
}

func (s *historyStore) windowOpened(date time.Time) {
	s.mu.Lock()
	s.runDates[date] = true
	s.mu.Unlock()
}

// page selects up to 51 posts like memoriesSQL. The posts are immutable, so
// they are used after the lock is released (hot.apply takes the locks in the
// other order).
func (s *historyStore) page(userID string, start, end time.Time, beforeAt *time.Time, beforeID string) ([]*historyPost, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded {
		return nil, false
	}
	posts, known := s.authors[userID]
	if !known {
		return nil, false
	}
	for d := range s.runDates {
		if !d.Before(start) && d.Before(end) {
			return nil, false
		}
	}
	var page []*historyPost
	for _, p := range posts {
		if p.LocalDate.Before(start) || !p.LocalDate.Before(end) {
			continue
		}
		if beforeAt != nil {
			if c := p.PostedAt.Compare(*beforeAt); c > 0 || c == 0 && p.ID >= beforeID {
				continue
			}
		}
		if s.dirty[p.ID] {
			return nil, false
		}
		page = append(page, p)
		if len(page) > 50 {
			break
		}
	}
	return page, true
}

// usersOf returns the current authors of the posts' comments and RealMojis, or
// nil when the hot state is not loaded.
func (s *hotState) usersOf(posts []*historyPost) map[string]*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded {
		return nil
	}
	users := map[string]*User{}
	add := func(id string) {
		if _, ok := users[id]; !ok {
			if u := s.users[id]; u != nil {
				user := u.User
				users[id] = &user
			} else {
				users[id] = nil
			}
		}
	}
	for _, p := range posts {
		for _, c := range p.Comments {
			add(c.AuthorID)
		}
		for _, r := range p.Reactions {
			add(r.ReactorID)
		}
	}
	return users
}

// memories answers listMemories for the viewer from memory; ok is false when
// it must be read from the database.
func (s *historyStore) memories(viewer User, start, end time.Time, beforeAt *time.Time, beforeID string) (response getMemoriesResponse, ok bool) {
	if (beforeAt == nil) != (beforeID == "") {
		return response, false
	}
	page, ok := s.page(viewer.ID, start, end, beforeAt, beforeID)
	if !ok {
		return response, false
	}
	response.HasMore = len(page) > 50
	if response.HasMore {
		page = page[:50]
	}
	users := hot.usersOf(page)
	if users == nil {
		return response, false
	}
	response.Items = make([]memoryPostResponse, 0, len(page))
	for _, p := range page {
		item := memoryPostResponse{createPostResponse: newPostResponse(p.Post, viewer),
			Comments: make([]createCommentResponse, 0), RealMojis: make([]replaceRealMojiResponse, 0)}
		for _, c := range p.Comments {
			if u := users[c.AuthorID]; u != nil && u.Status == "active" && len(item.Comments) < 50 {
				item.Comments = append(item.Comments, newCommentResponse(c, *u))
			}
		}
		for _, r := range p.Reactions {
			if u := users[r.ReactorID]; u != nil && u.Status == "active" && len(item.RealMojis) < 50 {
				item.RealMojis = append(item.RealMojis, newRealMojiResponse(r, *u))
			}
		}
		response.Items = append(response.Items, item)
	}
	return response, true
}
