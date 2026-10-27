package isureal

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
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
//
// The records hold no pointers (IDs as UUID bytes, times as microseconds,
// text in one arena), so the garbage collector does not scan them, and the
// image signatures of the current expiry are computed in the background.
type historyStore struct {
	mu        sync.RWMutex
	loaded    bool
	text      historyText
	posts     []historyPost
	comments  []historyComment
	reactions []historyReaction
	authors   map[string][]uint32 // post indexes, newest first (posted_at, id descending)
	byID      map[[16]byte]uint32
	// runDates are the local dates of windows outside the initial data.
	runDates map[time.Time]bool
	dirty    map[string]bool
}

type textRef struct{ off, n uint32 }

const nullText = ^uint32(0)

type historyPost struct {
	id, author, window, image              [16]byte
	caption, kind, visibility, locality    textRef
	latitude, longitude                    float64
	flags                                  uint8
	postedAt, deadline, endedAt, localDate int64
	firstComment, comments                 uint32
	firstReaction, reactions               uint32
	sigExp                                 int64
	sig                                    [32]byte
}

const (
	hasLatitude = 1 << iota
	hasLongitude
	hasEnded
)

type historyComment struct {
	id, author [16]byte
	body       textRef
	createdAt  int64
	post       uint32
}

type historyReaction struct {
	id, reactor, image [16]byte
	kind               textRef
	createdAt          int64
	post               uint32
	sigExp             int64
	sig                [32]byte
}

var history = &historyStore{runDates: map[time.Time]bool{}, dirty: map[string]bool{}}

// historyLoader builds the arrays of a load.
type historyLoader struct {
	text []byte
	ok   bool
}

func (l *historyLoader) id(s string) [16]byte {
	u, err := uuid.Parse(s)
	if err != nil || u.String() != s {
		l.ok = false
	}
	return u
}

func (l *historyLoader) str(s string) textRef {
	r := textRef{off: uint32(len(l.text)), n: uint32(len(s))}
	l.text = append(l.text, s...)
	return r
}

func (l *historyLoader) nullable(s *string) textRef {
	if s == nil {
		return textRef{n: nullText}
	}
	return l.str(*s)
}

// historyText is the arena of a load; it never changes.
type historyText []byte

func (t historyText) str(r textRef) string {
	return string(t[r.off : r.off+r.n])
}

func (t historyText) nullable(r textRef) *string {
	if r.n == nullText {
		return nil
	}
	v := t.str(r)
	return &v
}

func micros(t time.Time) int64 { return t.UnixMicro() }
func fromMicros(v int64) time.Time {
	return time.UnixMicro(v).UTC()
}

func (s *historyStore) load(ctx context.Context, db *sqlx.DB, region string) error {
	started := time.Now()
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	l := &historyLoader{ok: true}
	authors := map[string][]uint32{}
	var users []string
	if err := tx.SelectContext(ctx, &users, `SELECT id FROM users WHERE region = $1`, region); err != nil {
		return err
	}
	for _, id := range users {
		authors[id] = nil
	}
	var posts []historyPost
	byID := map[[16]byte]uint32{}
	rows, err := tx.QueryxContext(ctx, `SELECT `+postColumns+`, w.local_date
      FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
      JOIN users u ON u.id = p.author_id AND u.region = $1
      WHERE p.deleted_at IS NULL AND w.created_at <= (SELECT created_at FROM initial_data_boundary)`, region)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row struct {
			Post
			LocalDate time.Time `db:"local_date"`
		}
		if err := rows.StructScan(&row); err != nil {
			rows.Close()
			return err
		}
		p := historyPost{id: l.id(row.ID), author: l.id(row.AuthorID), window: l.id(row.PostingWindowID), image: l.id(row.ImageID),
			caption: l.str(row.Caption), kind: l.str(row.Kind), visibility: l.str(row.Visibility), locality: l.nullable(row.Locality),
			postedAt: micros(row.PostedAt), deadline: micros(row.OnTimeDeadline), localDate: row.LocalDate.Unix()}
		if row.Latitude != nil {
			p.latitude, p.flags = *row.Latitude, p.flags|hasLatitude
		}
		if row.Longitude != nil {
			p.longitude, p.flags = *row.Longitude, p.flags|hasLongitude
		}
		if row.WindowEndedAt != nil {
			p.endedAt, p.flags = micros(*row.WindowEndedAt), p.flags|hasEnded
		}
		byID[p.id] = uint32(len(posts))
		authors[row.AuthorID] = append(authors[row.AuthorID], uint32(len(posts)))
		posts = append(posts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var comments []historyComment
	rows, err = tx.QueryxContext(ctx, `SELECT c.id, c.post_id, c.author_id, c.body, c.created_at, c.deleted_at
      FROM comments c JOIN posts p ON p.id = c.post_id AND p.deleted_at IS NULL
      JOIN posting_windows w ON w.id = p.posting_window_id
      JOIN users u ON u.id = p.author_id AND u.region = $1
      WHERE c.deleted_at IS NULL AND w.created_at <= (SELECT created_at FROM initial_data_boundary)`, region)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c Comment
		if err := rows.StructScan(&c); err != nil {
			rows.Close()
			return err
		}
		post, found := byID[l.id(c.PostID)]
		if !found {
			continue
		}
		comments = append(comments, historyComment{id: l.id(c.ID), author: l.id(c.AuthorID), body: l.str(c.Body), createdAt: micros(c.CreatedAt), post: post})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var reactions []historyReaction
	rows, err = tx.QueryxContext(ctx, `SELECT r.id, r.post_id, r.reactor_id, r.image_id, r.reaction_type, r.created_at, r.replaced_at, r.deleted_at
      FROM realmojis r JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL
      JOIN posting_windows w ON w.id = p.posting_window_id
      JOIN users u ON u.id = p.author_id AND u.region = $1
      WHERE r.replaced_at IS NULL AND r.deleted_at IS NULL AND w.created_at <= (SELECT created_at FROM initial_data_boundary)`, region)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r RealMoji
		if err := rows.StructScan(&r); err != nil {
			rows.Close()
			return err
		}
		post, found := byID[l.id(r.PostID)]
		if !found {
			continue
		}
		reactions = append(reactions, historyReaction{id: l.id(r.ID), reactor: l.id(r.ReactorID), image: l.id(r.ImageID),
			kind: l.str(r.ReactionType), createdAt: micros(r.CreatedAt), post: post})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
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
	if !l.ok {
		// IDs that are not canonical UUIDs cannot be written back as stored.
		slog.Warn("history store not used: IDs are not canonical UUIDs")
		return nil
	}

	slices.SortFunc(comments, func(a, b historyComment) int {
		return cmp.Or(cmp.Compare(a.post, b.post), cmp.Compare(a.createdAt, b.createdAt), bytes.Compare(a.id[:], b.id[:]))
	})
	for i := range comments {
		p := &posts[comments[i].post]
		if p.comments == 0 {
			p.firstComment = uint32(i)
		}
		p.comments++
	}
	slices.SortFunc(reactions, func(a, b historyReaction) int {
		return cmp.Or(cmp.Compare(a.post, b.post), cmp.Compare(b.createdAt, a.createdAt), bytes.Compare(b.id[:], a.id[:]))
	})
	for i := range reactions {
		p := &posts[reactions[i].post]
		if p.reactions == 0 {
			p.firstReaction = uint32(i)
		}
		p.reactions++
	}
	for _, list := range authors {
		slices.SortFunc(list, func(a, b uint32) int {
			return cmp.Or(cmp.Compare(posts[b].postedAt, posts[a].postedAt), bytes.Compare(posts[b].id[:], posts[a].id[:]))
		})
	}

	s.mu.Lock()
	s.text, s.posts, s.comments, s.reactions, s.authors, s.byID, s.loaded = l.text, posts, comments, reactions, authors, byID, true
	for _, d := range runDates {
		s.runDates[d] = true
	}
	s.mu.Unlock()
	slog.Info("history store", "users", len(users), "posts", len(posts), "comments", len(comments), "realmojis", len(reactions), "text", len(l.text), "elapsed", time.Since(started).Round(time.Millisecond))
	go s.sign()
	return nil
}

// signLead is how long before an expiry step begins its signatures are
// computed, so the step starts with them.
const signLead = 15 * time.Minute

// sign keeps the image signatures of the posts and RealMojis computed for the
// expiry that is current signLead from now.
func (s *historyStore) sign() {
	workers := runtime.NumCPU()
	for {
		exp := imageURLExpiry(time.Now().Add(signLead))
		started := time.Now()
		s.mu.RLock()
		posts, reactions := len(s.posts), len(s.reactions)
		s.mu.RUnlock()
		var wg sync.WaitGroup
		for w := range workers {
			wg.Go(func() {
				for start := w * 256; start < posts+reactions; start += workers * 256 {
					s.signRange(start, min(start+256, posts+reactions), posts, exp)
				}
			})
		}
		wg.Wait()
		slog.Info("history signatures", "exp", exp, "elapsed", time.Since(started).Round(time.Second))
		// Later steps are signed by one worker, leaving the other CPU to requests.
		workers = 1
		next := time.Unix(exp, 0).Add(imageURLExpiryStep - imageURLLifetime - signLead)
		time.Sleep(max(time.Until(next), time.Minute))
	}
}

func (s *historyStore) signRange(start, end, posts int, exp int64) {
	type job struct {
		i            int
		owner, image [16]byte
	}
	jobs := make([]job, 0, end-start)
	s.mu.RLock()
	for i := start; i < end; i++ {
		if i < posts {
			if p := &s.posts[i]; p.sigExp != exp {
				jobs = append(jobs, job{i, p.author, p.image})
			}
		} else if r := &s.reactions[i-posts]; r.sigExp != exp {
			jobs = append(jobs, job{i, r.reactor, r.image})
		}
	}
	s.mu.RUnlock()
	// Keys are read from the hot state without holding this store's lock
	// (hot.apply takes the locks in the other order).
	sigs := make([][32]byte, len(jobs))
	signed := make([]bool, len(jobs))
	for k, j := range jobs {
		if u, ok := hot.userState(uuid.UUID(j.owner).String()); ok && len(u.ImageKey) > 0 {
			sigs[k], signed[k] = computeImageSignature(u.ImageKey, uuid.UUID(j.image).String(), exp), true
		}
	}
	s.mu.Lock()
	for k, j := range jobs {
		if !signed[k] {
			continue
		}
		if j.i < posts {
			s.posts[j.i].sig, s.posts[j.i].sigExp = sigs[k], exp
		} else {
			s.reactions[j.i-posts].sig, s.reactions[j.i-posts].sigExp = sigs[k], exp
		}
	}
	s.mu.Unlock()
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
	if s.byID != nil {
		for id := range s.dirty {
			if u, err := uuid.Parse(id); err != nil || !s.known(u) {
				delete(s.dirty, id)
			}
		}
	}
}

func (s *historyStore) known(id [16]byte) bool {
	_, ok := s.byID[id]
	return ok
}

// postImageURL returns the image URL of a post of the store when its
// signature for the current expiry is computed.
func (s *historyStore) postImageURL(postID, imageID string) (string, bool) {
	id, ok := parseImageID(postID)
	if !ok {
		return "", false
	}
	exp := imageURLExpiry(time.Now())
	s.mu.RLock()
	i, found := s.byID[id]
	var p historyPost
	if found {
		p = s.posts[i]
	}
	s.mu.RUnlock()
	if !found || p.sigExp != exp || uuid.UUID(p.image).String() != imageID {
		return "", false
	}
	return imageURLWith(imageID, exp, p.sig), true
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

// historyItem is a selected post with copies of its records, usable after the
// store's lock is released.
type historyItem struct {
	post      historyPost
	comments  []historyComment
	reactions []historyReaction
}

// page selects up to 51 posts like memoriesSQL. hot.apply takes the locks in
// the other order, so the records are copied out before users are read.
func (s *historyStore) page(userID string, start, end time.Time, beforeAt *time.Time, beforeID string) ([]historyItem, historyText, bool) {
	var before [16]byte
	if beforeAt != nil {
		u, err := uuid.Parse(beforeID)
		if err != nil || u.String() != beforeID {
			return nil, nil, false
		}
		before = u
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded {
		return nil, nil, false
	}
	indexes, known := s.authors[userID]
	if !known {
		return nil, nil, false
	}
	for d := range s.runDates {
		if !d.Before(start) && d.Before(end) {
			return nil, nil, false
		}
	}
	from, to := start.Unix(), end.Unix()
	var page []historyItem
	for _, i := range indexes {
		p := &s.posts[i]
		if p.localDate < from || p.localDate >= to {
			continue
		}
		if beforeAt != nil {
			if c := cmp.Compare(p.postedAt, micros(*beforeAt)); c > 0 || c == 0 && bytes.Compare(p.id[:], before[:]) >= 0 {
				continue
			}
		}
		if s.dirty[uuid.UUID(p.id).String()] {
			return nil, nil, false
		}
		page = append(page, historyItem{post: *p})
		if len(page) > 50 {
			break
		}
	}
	for k := range page {
		p := &page[k].post
		page[k].comments = slices.Clone(s.comments[p.firstComment : p.firstComment+p.comments])
		page[k].reactions = slices.Clone(s.reactions[p.firstReaction : p.firstReaction+p.reactions])
	}
	return page, s.text, true
}

// usersOf returns the current authors of the items' comments and RealMojis,
// or nil when the hot state is not loaded.
func (s *hotState) usersOf(items []historyItem) map[[16]byte]*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded {
		return nil
	}
	users := map[[16]byte]*User{}
	add := func(id [16]byte) {
		if _, ok := users[id]; !ok {
			if u := s.users[uuid.UUID(id).String()]; u != nil {
				user := u.User
				users[id] = &user
			} else {
				users[id] = nil
			}
		}
	}
	for _, item := range items {
		for _, c := range item.comments {
			add(c.author)
		}
		for _, r := range item.reactions {
			add(r.reactor)
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
	page, text, ok := s.page(viewer.ID, start, end, beforeAt, beforeID)
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
	exp := imageURLExpiry(time.Now())
	response.Items = make([]memoryPostResponse, 0, len(page))
	for _, item := range page {
		p := item.post
		post := Post{ID: uuid.UUID(p.id).String(), AuthorID: viewer.ID, PostingWindowID: uuid.UUID(p.window).String(),
			ImageID: uuid.UUID(p.image).String(), Caption: text.str(p.caption), Visibility: text.str(p.visibility),
			PostLocation: PostLocation{Kind: text.str(p.kind), Locality: text.nullable(p.locality)},
			PostedAt:     fromMicros(p.postedAt), OnTimeDeadline: fromMicros(p.deadline)}
		if p.flags&hasLatitude != 0 {
			post.Latitude = &p.latitude
		}
		if p.flags&hasLongitude != 0 {
			post.Longitude = &p.longitude
		}
		if p.flags&hasEnded != 0 {
			ended := fromMicros(p.endedAt)
			post.WindowEndedAt = &ended
		}
		var postResponse createPostResponse
		if p.sigExp == exp {
			postResponse = newPostResponseWithURL(post, viewer, imageURLWith(post.ImageID, exp, p.sig))
		} else {
			postResponse = newPostResponseWithURL(post, viewer, signedImageURL(viewer, post.ImageID))
		}
		item0 := memoryPostResponse{createPostResponse: postResponse,
			Comments: make([]createCommentResponse, 0), RealMojis: make([]replaceRealMojiResponse, 0)}
		for _, c := range item.comments {
			if u := users[c.author]; u != nil && u.Status == "active" && len(item0.Comments) < 50 {
				comment := Comment{ID: uuid.UUID(c.id).String(), PostID: post.ID, AuthorID: u.ID, Body: text.str(c.body), CreatedAt: fromMicros(c.createdAt)}
				item0.Comments = append(item0.Comments, newCommentResponse(comment, *u))
			}
		}
		for _, r := range item.reactions {
			if u := users[r.reactor]; u != nil && u.Status == "active" && len(item0.RealMojis) < 50 {
				reaction := RealMoji{ID: uuid.UUID(r.id).String(), PostID: post.ID, ReactorID: u.ID, ImageID: uuid.UUID(r.image).String(),
					ReactionType: text.str(r.kind), CreatedAt: fromMicros(r.createdAt)}
				if r.sigExp == exp {
					response := replaceRealMojiResponse{ID: reaction.ID, Reactor: newRelationshipUserResponse(*u), ReactionType: reaction.ReactionType,
						ImageURL: imageURLWith(reaction.ImageID, exp, r.sig), CreatedAt: reaction.CreatedAt}
					item0.RealMojis = append(item0.RealMojis, response)
				} else {
					item0.RealMojis = append(item0.RealMojis, newRealMojiResponse(reaction, *u))
				}
			}
		}
		response.Items = append(response.Items, item0)
	}
	return response, true
}
