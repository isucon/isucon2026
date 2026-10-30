package model

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
)

// InteractionPageSize は RealMoji とコメントの一覧の 1 ページの件数（アプリの仕様）。
const InteractionPageSize = 50

// pageSizeProblem は、1 ページの件数が仕様と合わなければ問題を返す。続きがあるのに足りない
// ページを返すと、それより後ろの交流の欠落を確かめられなくなる（空のページでも続きがあると
// して隠せてしまう）。
func pageSizeProblem(kind string, n int, hasMore bool) string {
	if (hasMore && n < InteractionPageSize) || n > InteractionPageSize {
		return fmt.Sprintf("%sが %d 件です（1 ページ %d 件、続きがあるか %t）", kind, n, InteractionPageSize, hasMore)
	}
	return ""
}

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
//
// viewer とブロックの関係にある（かもしれない）人のコメントは、仕様では見えないが、参照実装は
// 返すので、あってもなくてもよい。
func (m *Model) CheckComments(viewerID, postID string, after *client.Comment, page client.CommentPage, r Read) []string {
	if problem := pageSizeProblem("コメント", len(page.Items), page.HasMore); problem != "" {
		return []string{problem}
	}
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
		if seen[c.ID] || boolTri(c.exists, r) != True || m.activeTri(c.AuthorID, r) != True || m.blockedTri(viewerID, c.AuthorID, r) != False {
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

// realMojiNewer は a が b より新しい（作成時刻、ID の降順で前にある）か。一覧のページ送りも
// この順で区切る。
func realMojiNewer(a, b client.RealMoji) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID > b.ID
}

func (m *Model) reaction(postID, reactorID string) *timeline[string] {
	key := [2]string{postID, reactorID}
	if m.reactions[key] == nil {
		m.reactions[key] = newTimeline("")
		m.reactionsFor[postID] = append(m.reactionsFor[postID], reactorID)
	}
	return m.reactions[key]
}

// RealMoji は bench が送って受け付けられた RealMoji。種類、画像 ID、作成時刻は作成の応答の
// 値で、一覧でも同じでなければならない。
type RealMoji struct {
	ID, PostID, ReactorID string
	ReactionType, ImageID string
	CreatedAt             time.Time
}

// matches は一覧の項目 item が、postID への r と同じ RealMoji か。画像 URL は署名の期限が
// 応答ごとに変わるので、画像 ID だけを比べる。
func (r *RealMoji) matches(postID string, item client.RealMoji) bool {
	return r.PostID == postID && r.ReactorID == item.Reactor.ID && r.ReactionType == item.ReactionType &&
		r.CreatedAt.Equal(item.CreatedAt) && imageurl.CheckForm(item.ImageURL) == nil && imageurl.ImageID(item.ImageURL) == r.ImageID
}

// ReactionWrite は RealMoji の置き換え。新しい ID は応答でわかる。
type ReactionWrite struct {
	*Write
	postID, reactorID string
	change            *change[string]
}

// BeginReplaceRealMoji は postID への reactorID の RealMoji の置き換えを記録する。
func (m *Model) BeginReplaceRealMoji(postID, reactorID string) *ReactionWrite {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	tl := m.reaction(postID, reactorID)
	begin(w, tl, "?")
	return &ReactionWrite{Write: w, postID: postID, reactorID: reactorID, change: tl.changes[len(tl.changes)-1]}
}

// ReplacedRealMoji は置き換えた RealMoji を作成の応答の値で記録する。置き換えは新しい ID を
// 発行するので、ID が空か、すでにほかの RealMoji に使われていれば記録せずに false を返す
// （呼び出し側は結果を不明として記録する）。
func (m *Model) ReplacedRealMoji(w *ReactionWrite, reaction client.RealMoji) bool {
	m.mu.Lock()
	if reaction.ID == "" || reaction.ID == "?" || m.realMojiByID[reaction.ID] != nil {
		m.mu.Unlock()
		return false
	}
	w.change.value = reaction.ID
	m.realMojiByID[reaction.ID] = &RealMoji{
		ID: reaction.ID, PostID: w.postID, ReactorID: w.reactorID,
		ReactionType: reaction.ReactionType, ImageID: imageurl.ImageID(reaction.ImageURL), CreatedAt: reaction.CreatedAt,
	}
	m.mu.Unlock()
	w.Applied()
	return true
}

// BeginDeleteRealMoji は postID への reactorID の RealMoji の削除を記録する。
func (m *Model) BeginDeleteRealMoji(postID, reactorID string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	begin(w, m.reaction(postID, reactorID), "")
	return w
}

// CheckRealMojis は viewer が読んだ投稿 postID の RealMoji の一覧の最初のページを、bench が
// 送った RealMoji と突き合わせる。置き換えや削除をした RealMoji が返った、同じ人の RealMoji や
// 同じ ID が 2 件ある、送った RealMoji と種類・画像・作成時刻が違う、確実に残っている RealMoji
// がない（続きがあるなら最後の項目より新しいもの）、新しい順に並んでいない場合を返す。viewer と
// ブロックの関係にある人の RealMoji は、CheckComments と同じくあってもなくてもよい。
func (m *Model) CheckRealMojis(viewerID, postID string, page client.RealMojiPage, r Read) []string {
	if problem := pageSizeProblem("RealMoji ", len(page.Items), page.HasMore); problem != "" {
		return []string{problem}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var problems []string
	reactors, ids := map[string]bool{}, map[string]bool{}
	for i, item := range page.Items {
		if reactors[item.Reactor.ID] {
			problems = append(problems, fmt.Sprintf("%s の RealMoji が 2 件あります", item.Reactor.ID))
			continue
		}
		reactors[item.Reactor.ID] = true
		if ids[item.ID] {
			problems = append(problems, fmt.Sprintf("RealMoji %s が重複しています", item.ID))
			continue
		}
		ids[item.ID] = true
		if i > 0 && !realMojiNewer(page.Items[i-1], item) {
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
		default:
			// ID のわかっている RealMoji は、送ったときの応答と同じでなければならない（ID の
			// わからない置き換えのものかもしれない項目は、知っている ID でなければ比べられない）。
			if known := m.realMojiByID[item.ID]; known != nil && !known.matches(postID, item) {
				problems = append(problems, fmt.Sprintf("RealMoji %s の内容が送ったものと違います", item.ID))
			}
		}
	}
	for _, reactor := range m.reactionsFor[postID] {
		if reactors[reactor] || m.activeTri(reactor, r) != True || m.blockedTri(viewerID, reactor, r) != False {
			continue
		}
		values := m.reactions[[2]string{postID, reactor}].possible(r)
		if slices.Contains(values, "") || slices.Contains(values, "?") {
			continue
		}
		// 続きがあれば、最後の項目より古いかもしれない RealMoji は次のページにあってよい。
		if page.HasMore && slices.ContainsFunc(values, func(id string) bool {
			known := m.realMojiByID[id]
			return known == nil || !realMojiNewer(client.RealMoji{ID: id, CreatedAt: known.CreatedAt}, page.Items[len(page.Items)-1])
		}) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s の RealMoji がありません", reactor))
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
