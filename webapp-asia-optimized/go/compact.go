package isureal

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// キャッシュに長く残すデータをポインタを含まない形で持つ（GC の走査対象から外すため）。
// 文字列は 1 本の blob への位置で、時刻は Unix ナノ秒と場所の番号で持つ。

type strRef struct{ off, n uint32 }

type blobBuilder struct{ buf []byte }

func (b *blobBuilder) add(s string) strRef {
	r := strRef{uint32(len(b.buf)), uint32(len(s))}
	b.buf = append(b.buf, s...)
	return r
}

func (b *blobBuilder) done() string {
	if len(b.buf) == 0 {
		return ""
	}
	return unsafe.String(&b.buf[0], len(b.buf))
}

func (r strRef) in(blob string) string { return blob[r.off : r.off+r.n] }

// time.Time の場所（UTC・Local など）を番号で持つ（追加はまれなのでコピーして差し替える）
var (
	locMu   sync.Mutex
	locList atomic.Pointer[[]*time.Location]
)

func init() {
	l := []*time.Location{time.UTC, time.Local}
	locList.Store(&l)
}

func locIndex(l *time.Location) uint8 {
	for i, x := range *locList.Load() {
		if x == l {
			return uint8(i)
		}
	}
	locMu.Lock()
	defer locMu.Unlock()
	cur := *locList.Load()
	for i, x := range cur {
		if x == l {
			return uint8(i)
		}
	}
	if len(cur) >= 250 {
		// 想定外に場所が増え続ける場合は固定オフセットに置き換える
		_, offset := time.Now().In(l).Zone()
		if offset == 0 {
			return 0
		}
		l = time.FixedZone("", offset)
	}
	next := append(append([]*time.Location(nil), cur...), l)
	locList.Store(&next)
	return uint8(len(next) - 1)
}

func locAt(i uint8) *time.Location { return (*locList.Load())[i] }

type compactTime struct {
	ns  int64
	loc uint8
}

func toCompactTime(t time.Time) compactTime {
	return compactTime{t.UnixNano(), locIndex(t.Location())}
}

func (c compactTime) time() time.Time { return time.Unix(0, c.ns).In(locAt(c.loc)) }

// ---- 著者ごとの投稿（memories 用） ----

type compactPost struct {
	id, window, image, caption, visibility, kind, locality strRef
	hasLocality, hasLat, hasLon                            bool
	lat, lon                                               float64
	postedAt                                               compactTime
}

type authorPostList struct {
	blob  string
	posts []compactPost
}

func newAuthorPostList(posts []Post) *authorPostList {
	n := 0
	for i := range posts {
		p := &posts[i]
		n += len(p.ID) + len(p.PostingWindowID) + len(p.ImageID) + len(p.Caption) + len(p.Visibility) + len(p.PostLocation.Kind)
		if p.Locality != nil {
			n += len(*p.Locality)
		}
	}
	b := blobBuilder{buf: make([]byte, 0, n)}
	list := &authorPostList{posts: make([]compactPost, len(posts))}
	for i := range posts {
		p := &posts[i]
		c := &list.posts[i]
		c.id, c.window, c.image, c.caption = b.add(p.ID), b.add(p.PostingWindowID), b.add(p.ImageID), b.add(p.Caption)
		c.visibility, c.kind = b.add(p.Visibility), b.add(p.PostLocation.Kind)
		if p.Locality != nil {
			c.hasLocality, c.locality = true, b.add(*p.Locality)
		}
		if p.Latitude != nil {
			c.hasLat, c.lat = true, *p.Latitude
		}
		if p.Longitude != nil {
			c.hasLon, c.lon = true, *p.Longitude
		}
		c.postedAt = toCompactTime(p.PostedAt)
	}
	list.blob = b.done()
	return list
}

func (l *authorPostList) windowID(i int) string { return l.posts[i].window.in(l.blob) }
func (l *authorPostList) postID(i int) string   { return l.posts[i].id.in(l.blob) }

// 投稿期間の情報（締め切り・終了時刻）はメモリの状態から補う
func (l *authorPostList) post(i int, authorID string) Post {
	c := &l.posts[i]
	p := Post{
		ID: c.id.in(l.blob), AuthorID: authorID, PostingWindowID: c.window.in(l.blob), ImageID: c.image.in(l.blob),
		Caption: c.caption.in(l.blob), Visibility: c.visibility.in(l.blob),
		PostLocation: PostLocation{Kind: c.kind.in(l.blob)},
		PostedAt:     c.postedAt.time(),
	}
	if c.hasLocality {
		s := c.locality.in(l.blob)
		p.Locality = &s
	}
	if c.hasLat {
		v := c.lat
		p.Latitude = &v
	}
	if c.hasLon {
		v := c.lon
		p.Longitude = &v
	}
	if w, ok := state.windowByID(p.PostingWindowID); ok {
		p.OnTimeDeadline = w.OnTimeDeadline
		p.WindowEndedAt = w.EndedAt
	}
	return p
}

// ---- 投稿の RealMoji とコメント（memories 用） ----

type compactRealMoji struct {
	id, postID, reactorID, imageID, reactionType strRef
	createdAt                                    compactTime
}

type compactComment struct {
	id, postID, authorID, body strRef
	createdAt                  compactTime
}

type compactInteractions struct {
	blob      string
	realMojis []compactRealMoji
	comments  []compactComment
}

func newCompactInteractions(in postInteractions) compactInteractions {
	n := 0
	for _, r := range in.RealMojis {
		n += len(r.ID) + len(r.PostID) + len(r.ReactorID) + len(r.ImageID) + len(r.ReactionType)
	}
	for _, c := range in.Comments {
		n += len(c.ID) + len(c.PostID) + len(c.AuthorID) + len(c.Body)
	}
	b := blobBuilder{buf: make([]byte, 0, n)}
	out := compactInteractions{}
	if len(in.RealMojis) > 0 {
		out.realMojis = make([]compactRealMoji, len(in.RealMojis))
	}
	if len(in.Comments) > 0 {
		out.comments = make([]compactComment, len(in.Comments))
	}
	for i, r := range in.RealMojis {
		out.realMojis[i] = compactRealMoji{b.add(r.ID), b.add(r.PostID), b.add(r.ReactorID), b.add(r.ImageID), b.add(r.ReactionType), toCompactTime(r.CreatedAt)}
	}
	for i, c := range in.Comments {
		out.comments[i] = compactComment{b.add(c.ID), b.add(c.PostID), b.add(c.AuthorID), b.add(c.Body), toCompactTime(c.CreatedAt)}
	}
	out.blob = b.done()
	return out
}

func (c *compactInteractions) expand() postInteractions {
	var in postInteractions
	if len(c.realMojis) > 0 {
		in.RealMojis = make([]RealMoji, len(c.realMojis))
	}
	if len(c.comments) > 0 {
		in.Comments = make([]Comment, len(c.comments))
	}
	for i, r := range c.realMojis {
		in.RealMojis[i] = RealMoji{ID: r.id.in(c.blob), PostID: r.postID.in(c.blob), ReactorID: r.reactorID.in(c.blob), ImageID: r.imageID.in(c.blob),
			ReactionType: r.reactionType.in(c.blob), CreatedAt: r.createdAt.time()}
	}
	for i, x := range c.comments {
		in.Comments[i] = Comment{ID: x.id.in(c.blob), PostID: x.postID.in(c.blob), AuthorID: x.authorID.in(c.blob), Body: x.body.in(c.blob),
			CreatedAt: x.createdAt.time()}
	}
	return in
}
