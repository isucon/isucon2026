package model

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
)

// Comment は bench が書いたコメント。
type Comment struct {
	ID        string
	PostID    string
	AuthorID  string
	Body      string
	CreatedAt time.Time
	exists    *timeline[bool]
}

// pendingComment は ID のわからないコメントの作成。書いた人はわかっている。
type pendingComment struct {
	authorID string
	exists   *timeline[bool]
}

// CommentCreation は ID のわからないコメントの作成。
type CommentCreation struct {
	*Write
	postID string
	exists *timeline[bool]
}

// BeginCreateComment は postID への authorID のコメントの作成を記録する。
func (m *Model) BeginCreateComment(postID, authorID string) *CommentCreation {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	exists := newTimeline(false)
	begin(w, exists, true)
	m.pendingComments[postID] = append(m.pendingComments[postID], pendingComment{authorID: authorID, exists: exists})
	c := &CommentCreation{Write: w, postID: postID, exists: exists}
	// 拒否された作成は反映されていないので、ID の知らないコメントとして残さない。
	w.after = func(o outcome) {
		if o == rejected {
			m.removePendingComment(postID, exists)
		}
	}
	return c
}

func (m *Model) removePendingComment(postID string, exists *timeline[bool]) {
	list := m.pendingComments[postID]
	if i := slices.IndexFunc(list, func(c pendingComment) bool { return c.exists == exists }); i >= 0 {
		m.pendingComments[postID] = slices.Delete(list, i, i+1)
	}
}

// CreatedComment は作成できたコメントを記録する。
func (m *Model) CreatedComment(creation *CommentCreation, comment client.Comment, authorID string) {
	m.mu.Lock()
	c := &Comment{ID: comment.ID, PostID: creation.postID, AuthorID: authorID, Body: comment.Body, CreatedAt: comment.CreatedAt, exists: creation.exists}
	m.comments[c.PostID] = append(m.comments[c.PostID], c)
	m.commentByID[c.ID] = c
	creation.after = func(outcome) { m.removePendingComment(c.PostID, creation.exists) }
	m.mu.Unlock()
	creation.Applied()
}

// BeginDeleteComment はコメントの削除を記録する。
func (m *Model) BeginDeleteComment(commentID string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	if c := m.commentByID[commentID]; c != nil {
		begin(w, c.exists, false)
	}
	return w
}

// CheckComments は viewer が投稿 postID のコメントの一覧で after より後を読んだ 1 ページを、
// bench が書いたコメントと突き合わせる。削除されたコメントや bench の知らないコメント
// （結果のわからない作成がなければ）が返った、確実に残っているコメントがない、古い順に
// 並んでいない場合を返す。
func (m *Model) CheckComments(postID string, after *client.Comment, page client.CommentPage, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var problems []string
	seen := map[string]bool{}
	for i, item := range page.Items {
		if seen[item.ID] {
			problems = append(problems, fmt.Sprintf("コメント %s が重複しています", item.ID))
			continue
		}
		seen[item.ID] = true
		if i > 0 && commentLess(item, page.Items[i-1]) {
			problems = append(problems, fmt.Sprintf("コメント %s が古い順に並んでいません", item.ID))
		}
		c := m.commentByID[item.ID]
		switch {
		case c == nil:
			if !slices.ContainsFunc(m.pendingComments[postID], func(c pendingComment) bool { return boolTri(c.exists, r) != False }) {
				problems = append(problems, fmt.Sprintf("bench の知らないコメント %s が返りました", item.ID))
			}
		case c.PostID != postID:
			problems = append(problems, fmt.Sprintf("ほかの投稿のコメント %s が返りました", item.ID))
		case boolTri(c.exists, r) == False:
			problems = append(problems, fmt.Sprintf("削除したコメント %s が返りました", item.ID))
		case m.activeTri(c.AuthorID, r) == False:
			problems = append(problems, fmt.Sprintf("退会したユーザーのコメント %s が返りました", item.ID))
		case item.Author.ID != c.AuthorID || item.Body != c.Body:
			problems = append(problems, fmt.Sprintf("コメント %s の内容が違います", item.ID))
		}
	}
	var last *client.Comment
	if page.HasMore && len(page.Items) > 0 {
		last = &page.Items[len(page.Items)-1]
	}
	for _, c := range m.comments[postID] {
		if seen[c.ID] || boolTri(c.exists, r) != True || m.activeTri(c.AuthorID, r) != True {
			continue
		}
		key := client.Comment{ID: c.ID, CreatedAt: c.CreatedAt}
		if (after != nil && !commentLess(*after, key)) || (last != nil && commentLess(*last, key)) {
			continue
		}
		problems = append(problems, fmt.Sprintf("コメント %s がありません", c.ID))
	}
	return problems
}

// commentLess は a が b より前（作成時刻、ID の順）か。
func commentLess(a, b client.Comment) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func (m *Model) reaction(postID, reactorID string) *timeline[string] {
	key := [2]string{postID, reactorID}
	if m.reactions[key] == nil {
		m.reactions[key] = newTimeline("")
		m.reactionsFor[postID] = append(m.reactionsFor[postID], reactorID)
	}
	return m.reactions[key]
}

// ReactionWrite は RealMoji の置き換え。新しい ID は応答でわかる。
type ReactionWrite struct {
	*Write
	change *change[string]
}

// BeginReplaceRealMoji は postID への reactorID の RealMoji の置き換えを記録する。
func (m *Model) BeginReplaceRealMoji(postID, reactorID string) *ReactionWrite {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	tl := m.reaction(postID, reactorID)
	begin(w, tl, "?")
	return &ReactionWrite{Write: w, change: tl.changes[len(tl.changes)-1]}
}

// ReplacedRealMoji は置き換えた RealMoji の ID を記録する。
func (m *Model) ReplacedRealMoji(w *ReactionWrite, id string) {
	m.mu.Lock()
	w.change.value = id
	m.mu.Unlock()
	w.Applied()
}

// BeginDeleteRealMoji は postID への reactorID の RealMoji の削除を記録する。
func (m *Model) BeginDeleteRealMoji(postID, reactorID string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.reaction(postID, reactorID), "")
	return w
}

// CheckRealMojis は投稿 postID の RealMoji の一覧の最初のページを、bench が送った RealMoji と
// 突き合わせる。置き換えや削除をした RealMoji が返った、同じ人の RealMoji が 2 件ある、
// 確実に残っている RealMoji がない（続きがないとき）、新しい順に並んでいない場合を返す。
func (m *Model) CheckRealMojis(postID string, page client.RealMojiPage, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var problems []string
	reactors := map[string]bool{}
	for i, item := range page.Items {
		if reactors[item.Reactor.ID] {
			problems = append(problems, fmt.Sprintf("%s の RealMoji が 2 件あります", item.Reactor.ID))
			continue
		}
		reactors[item.Reactor.ID] = true
		if i > 0 && item.CreatedAt.After(page.Items[i-1].CreatedAt) {
			problems = append(problems, fmt.Sprintf("RealMoji %s が新しい順に並んでいません", item.ID))
		}
		tl := m.reactions[[2]string{postID, item.Reactor.ID}]
		switch {
		case tl == nil:
			problems = append(problems, fmt.Sprintf("bench の知らない RealMoji %s が返りました", item.ID))
		case m.activeTri(item.Reactor.ID, r) == False:
			problems = append(problems, fmt.Sprintf("退会したユーザーの RealMoji %s が返りました", item.ID))
		case !tl.mayBe(r, item.ID) && !tl.mayBe(r, "?"):
			problems = append(problems, fmt.Sprintf("置き換えたか削除した RealMoji %s が返りました", item.ID))
		}
	}
	if page.HasMore {
		return problems
	}
	for _, reactor := range m.reactionsFor[postID] {
		if reactors[reactor] || m.activeTri(reactor, r) != True {
			continue
		}
		values := m.reactions[[2]string{postID, reactor}].possible(r)
		if !slices.Contains(values, "") && !slices.Contains(values, "?") {
			problems = append(problems, fmt.Sprintf("%s の RealMoji がありません", reactor))
		}
	}
	return problems
}

// CheckPost は viewer が投稿 postID を 1 件取った結果を判定する。見えてはいけない投稿が
// 返った場合は violation、見えるべき投稿が 404 になった、内容が違う場合は problems に入れる。
func (m *Model) CheckPost(viewerID, postID string, got *client.Post, r Read) (violation string, problems []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.posts[postID]
	if p == nil {
		return "", nil
	}
	v := m.newView(m.users[viewerID], r)
	visible := v.canViewTri(p)
	switch {
	case got == nil && visible == True:
		return "", []string{fmt.Sprintf("見えるはずの投稿 %s が 404 になりました", postID)}
	case got == nil:
		return "", nil
	case visible == False:
		return fmt.Sprintf("見えてはいけない投稿 %s が取得できました", postID), nil
	}
	var result FeedResult
	m.checkPostDetail(&result, "投稿の取得", *got, p, r)
	return "", result.Errors
}

// CheckCurrentPost は自分の今日の投稿として返った got を、bench が投稿した postID の投稿と
// 比べる（ID は一致しているものとする）。bench の知らない投稿なら確かめない。
func (m *Model) CheckCurrentPost(postID string, got client.Post, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.posts[postID]
	if p == nil {
		return nil
	}
	var result FeedResult
	m.checkPostDetail(&result, "今日の投稿", got, p, r)
	return result.Errors
}

// checkPostDetail は投稿 1 件の応答 got を、要求した投稿 want と比べる。フィードと同じ項目に
// 加えて、ID、投稿期間、画像 ID、位置情報、時間内かどうかと遅延時間を確かめる。
func (m *Model) checkPostDetail(result *FeedResult, label string, got client.Post, want *Post, r Read) {
	switch {
	case got.ID != want.ID:
		result.error("%s: 投稿 %s を求めたのに投稿 %s が返りました", label, want.ID, got.ID)
	case got.PostingWindowID != want.WindowID:
		result.error("%s: 投稿 %s の投稿期間が違います", label, want.ID)
	case want.ImageID != "" && (imageurl.CheckForm(got.ImageURL) != nil || imageurl.ImageID(got.ImageURL) != want.ImageID):
		result.error("%s: 投稿 %s の画像 URL %q が投稿の画像を指していません", label, want.ID, got.ImageURL)
	case want.Location.Kind != "" && !sameLocation(want.Location, got.Location):
		result.error("%s: 投稿 %s の位置情報が違います", label, want.ID)
	case got.OnTime != want.OnTime || got.LateSeconds != want.LateSeconds:
		result.error("%s: 投稿 %s の時間内かどうかか遅延時間が投稿したときと違います", label, want.ID)
	default:
		m.checkPostContent(result, label, got.FeedPost(), want, r)
	}
}

// sameLocation は応答の位置情報が送ったものと同じか。座標は丸めの差を許す（scenario の
// 投稿の応答の確認と同じ規則）。
func sameLocation(sent, got client.Location) bool {
	if sent.Kind != got.Kind {
		return false
	}
	switch sent.Kind {
	case "approximate":
		return got.Locality != nil && sent.Locality != nil && *got.Locality == *sent.Locality
	case "exact":
		return got.Latitude != nil && got.Longitude != nil && sent.Latitude != nil && sent.Longitude != nil &&
			math.Abs(*got.Latitude-*sent.Latitude) < 1e-6 && math.Abs(*got.Longitude-*sent.Longitude) < 1e-6
	default:
		return true
	}
}

// ReactionID は postID への reactorID の今の RealMoji の ID。確実に決まらなければ ""。
func (m *Model) ReactionID(postID, reactorID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tl := m.reactions[[2]string{postID, reactorID}]
	if tl == nil {
		return ""
	}
	now := m.now()
	values := tl.possible(Read{Sent: now, Received: now})
	if len(values) != 1 || values[0] == "?" {
		return ""
	}
	return values[0]
}

// Visible は viewer から target のプロフィールが見えるか（target が有効で、どちら向きにも
// ブロックしていない）。
func (m *Model) Visible(viewerID, targetID string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return min(m.activeTri(targetID, r), Not(m.blockedTri(viewerID, targetID, r)))
}

// Friend は a と b が友達か。
func (m *Model) Friend(a, b string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.friendTri(a, b, r)
}

// FriendsOf は r の間ずっと id の友達だった人。
func (m *Model) FriendsOf(id string, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for other := range m.friendCandidates[id] {
		if m.friendTri(id, other, r) == True {
			out = append(out, other)
		}
	}
	return out
}

// Blocked は a と b のどちらかがもう一方をブロックしているか。
func (m *Model) Blocked(a, b string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blockedTri(a, b, r)
}

// Following は follower が followee をフォローしているか。
func (m *Model) Following(follower, followee string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.followTri(follower, followee, r)
}

// Now は今の時刻の読み取り（送信と応答が同時）。
func (m *Model) Now() Read {
	now := m.now()
	return Read{Sent: now, Received: now, Staleness: m.Staleness}
}
