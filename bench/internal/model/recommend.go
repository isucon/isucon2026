package model

import (
	"slices"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// おすすめ順（友達の友達フィード）
//
// 閲覧者 v から見た投稿 p（投稿者 a）の点数は 10 × E(v, p) + 3 × M(v, a) + min(C(v, a), 30)。M(v, a) は
// v と a の共通の友達の数、C(v, a) は a の友達それぞれについて v との共通の友達の数を足したもの、
// E(v, p) は v の友達のうち、p に有効な RealMoji かコメントを付けている、退会していない人の数。
// 友達関係は退会した人のものも数える（友達の友達の判定と同じ）。並びは点数の大きい順、同点は
// 投稿日時の新しい順、さらに ID の降順。
//
// bench は友達関係、RealMoji、コメント、退会のそれぞれを「どちらかわからない」ことがある
// として持つので、点数を 1 つに決めず、下限（わからないものをすべて無いとする）と上限
// （すべて有るとする）を出す。点数はどの入力についても、有るほうが大きくなる（単調）ので、
// 本当の点数は必ずこの間にある。

// engagementWeight と mutualWeight は E と M の重み、connectionsCap は C を数える上限（C の重みは 1）。
const (
	engagementWeight = 10
	mutualWeight     = 3
	connectionsCap   = 30
)

// reach は閲覧者から 2 歩でたどれる人ごとの、閲覧者との共通の友達の数の下限と上限。
type reach struct {
	lo, hi map[string]int64
}

// reachOf は viewerID の reach。友達だったかもしれない相手の、友達だったかもしれない相手を
// たどる。
func (m *Model) reachOf(viewerID string, r Read) reach {
	out := reach{lo: map[string]int64{}, hi: map[string]int64{}}
	for f := range m.friendCandidates[viewerID] {
		first := m.friendTri(viewerID, f, r)
		if first == False {
			continue
		}
		for y := range m.friendCandidates[f] {
			if y == viewerID {
				continue
			}
			switch min(first, m.friendTri(f, y, r)) {
			case True:
				out.lo[y]++
				out.hi[y]++
			case Unknown:
				out.hi[y]++
			}
		}
	}
	return out
}

// scoreBounds は点数の各項（M、C、E）の下限と上限。
type scoreBounds struct {
	mutual, connections, engagement [2]int64
}

func (b scoreBounds) lo() int64 {
	return engagementWeight*b.engagement[0] + mutualWeight*b.mutual[0] + min(b.connections[0], connectionsCap)
}

func (b scoreBounds) hi() int64 {
	return engagementWeight*b.engagement[1] + mutualWeight*b.mutual[1] + min(b.connections[1], connectionsCap)
}

// admits は score がこの範囲の入力から出うる点数か。各項を範囲の中の整数から独立に選んで
// 10E + 3M + min(C, 30) = score にできるかを見るので、下限と上限の間でも、どの組み合わせ
// からも出ない点数（M だけが不確かなときの 3 点刻みの間など）は誤りとする。項どうしは同じ
// 友達関係でつながっていて、独立に選べる組み合わせは実際に出うるものを含むので、出うる点数を
// 誤りとすることはない。
func (b scoreBounds) admits(score int64) bool {
	if score < b.lo() || score > b.hi() {
		return false
	}
	cLo, cHi := min(b.connections[0], connectionsCap), min(b.connections[1], connectionsCap)
	for mutual := b.mutual[0]; mutual <= b.mutual[1]; mutual++ {
		// 残りを 10E + C（E は engagement、C は cLo から cHi）で出せるか。
		rest := score - mutualWeight*mutual
		lo := max(b.engagement[0], ceilDiv(rest-cHi, engagementWeight))
		hi := min(b.engagement[1], floorDiv(rest-cLo, engagementWeight))
		if lo <= hi {
			return true
		}
	}
	return false
}

// floorDiv と ceilDiv は b > 0 で割った商を、負の数でも切り下げ、切り上げる。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func ceilDiv(a, b int64) int64 {
	return -floorDiv(-a, b)
}

// recommendationBounds は viewerID から見た p の点数の各項の下限と上限。
func (m *Model) recommendationBounds(viewerID string, p *Post, rc reach, r Read) scoreBounds {
	author := p.AuthorID
	b := scoreBounds{mutual: [2]int64{rc.lo[author], rc.hi[author]}}
	for y := range m.friendCandidates[author] {
		switch m.friendTri(author, y, r) {
		case True:
			b.connections[0] += rc.lo[y]
			b.connections[1] += rc.hi[y]
		case Unknown:
			b.connections[1] += rc.hi[y]
		}
	}
	b.engagement[0], b.engagement[1] = m.engagementBounds(viewerID, p, r)
	return b
}

// engagementBounds は E(v, p) の下限と上限。
func (m *Model) engagementBounds(viewerID string, p *Post, r Read) (lo, hi int64) {
	// 人ごとに、有効な RealMoji かコメントを付けているか（どちらか一方でよいので Tri の最大）。
	engaged := map[string]Tri{}
	for _, reactor := range m.reactionsFor[p.ID] {
		live := forAll(m.reactions[[2]string{p.ID, reactor}], r, func(id string) Tri { return triOf(id != "") })
		engaged[reactor] = max(engaged[reactor], live)
	}
	for _, c := range m.comments[p.ID] {
		engaged[c.AuthorID] = max(engaged[c.AuthorID], boolTri(c.exists, r))
	}
	for _, c := range m.pendingComments[p.ID] {
		engaged[c.authorID] = max(engaged[c.authorID], boolTri(c.exists, r))
	}
	for user, t := range engaged {
		if t == False {
			continue
		}
		switch min(t, m.friendTri(viewerID, user, r), m.activeTri(user, r)) {
		case True:
			lo++
			hi++
		case Unknown:
			hi++
		}
	}
	return lo, hi
}

// rankedBefore は (score, postedAt, id) が (otherScore, otherPostedAt, otherID) より後に並ぶか。
func rankedBefore(score int64, postedAt time.Time, id string, otherScore int64, otherPostedAt time.Time, otherID string) bool {
	if score != otherScore {
		return score < otherScore
	}
	if !postedAt.Equal(otherPostedAt) {
		return postedAt.Before(otherPostedAt)
	}
	return id < otherID
}

// checkRecommendedLocked は友達の友達フィードの 1 ページを検証する。CheckFeed と同じく、
// 見えてはいけない投稿は失格、内容の誤りはエラーにする。加えて
//
//   - 各投稿の点数は、bench の出した下限と上限の間になければならない。
//   - 返った点数で、おすすめ順（点数、投稿日時、ID の降順）に並び、ページ送りの位置より後になければ
//     ならない。
//   - 見えるべき投稿のうち、点数の上限でもページ送りの位置より後、下限でも末尾の投稿より前（次の
//     ページがあるとき）になる、確実にこのページに入るものは、すべて返っていなければならない
//     （thorough のときだけ）。
//
// 点数は要求のたびに計算し直すので、ページの間で点数が変わった投稿は、ページをまたいで欠落や
// 重複があってもよい。
func (m *Model) checkRecommendedLocked(viewer *User, before *client.FeedPost, page client.FeedPage, r Read, thorough bool) (FeedResult, []signature) {
	const feed = "friends-of-friends"
	var result FeedResult
	var signatures []signature
	if (page.HasMore && len(page.Items) < m.PageSize) || len(page.Items) > m.PageSize {
		result.error("フィード %s: 投稿が %d 件です（1 ページ %d 件、続きがあるか %t）", feed, len(page.Items), m.PageSize, page.HasMore)
		return result, nil
	}
	if before != nil && before.Score == nil {
		before = nil
	}

	v := m.newView(viewer, r)
	rc := m.reachOf(viewer.ID, r)
	seen := make(map[string]bool, len(page.Items))
	for i, item := range page.Items {
		if seen[item.ID] {
			result.error("フィード %s: 投稿 %s が重複しています", feed, item.ID)
			continue
		}
		seen[item.ID] = true
		// 点数がなくても、見えてはいけない投稿かどうかと内容は確かめる。
		if item.Score == nil {
			result.error("フィード %s: 投稿 %s に点数がありません", feed, item.ID)
		} else {
			if before != nil && !rankedBefore(*item.Score, item.PostedAt, item.ID, *before.Score, before.PostedAt, before.ID) {
				result.error("フィード %s: 投稿 %s がページ送りの位置より前にあります", feed, item.ID)
			}
			if i > 0 {
				if previous := page.Items[i-1]; previous.Score != nil && !rankedBefore(*item.Score, item.PostedAt, item.ID, *previous.Score, previous.PostedAt, previous.ID) {
					result.error("フィード %s: 投稿 %s がおすすめ順に並んでいません", feed, item.ID)
				}
			}
		}
		p := m.posts[item.ID]
		if p == nil {
			if !m.mayHaveUnknownPost(item.Author.ID, r) {
				result.violation("フィード %s: 存在しない投稿 %s が返りました", feed, item.ID)
			}
			continue
		}
		if v.inFeedTri(p, feed) == False {
			result.violation("フィード %s: %s に見えてはいけない投稿 %s が返りました", feed, viewer.Username, item.ID)
			continue
		}
		m.checkPostContent(&result, feed, item, p, r)
		b := m.recommendationBounds(viewer.ID, p, rc, r)
		m.ranking.record(b)
		if item.Score != nil && !b.admits(*item.Score) {
			result.error("フィード %s: 投稿 %s の点数が %d です（%d 以上 %d 以下で、共通の友達と 3 歩の道の数から出うる値のはずです）", feed, item.ID, *item.Score, b.lo(), b.hi())
		}
		if thorough {
			signatures = append(signatures, signature{postID: p.ID, url: item.ImageURL, imageID: p.ImageID, secretKey: m.users[p.AuthorID].SecretKey})
		}
	}

	if !thorough {
		return result, nil
	}
	var last *client.FeedPost
	if page.HasMore && len(page.Items) > 0 && page.Items[len(page.Items)-1].Score != nil {
		last = &page.Items[len(page.Items)-1]
	}
	m.feedCandidates(viewer.ID, feed, r, func(p *Post) {
		if seen[p.ID] || v.inFeedTri(p, feed) != True {
			return
		}
		b := m.recommendationBounds(viewer.ID, p, rc, r)
		if before != nil && !rankedBefore(b.hi(), p.PostedAt, p.ID, *before.Score, before.PostedAt, before.ID) {
			m.ranking.skip()
			return
		}
		if last != nil && !rankedBefore(*last.Score, last.PostedAt, last.ID, b.lo(), p.PostedAt, p.ID) {
			if b.lo() != b.hi() {
				m.ranking.skip()
			}
			return
		}
		result.thorough("フィード %s: %s に見えるべき投稿 %s がありません", feed, viewer.Username, p.ID)
	})
	return result, signatures
}

// RankingStats は友達の友達フィードで確かめた点数の記録。重みを決めるための各項の分布と、
// 検証がどれだけ確定した点数で行えたかを見る。
type RankingStats struct {
	// Items は点数を確かめた投稿の数、Exact はそのうち下限と上限が一致した数、MeanWidth は
	// 上限と下限の差の平均。
	Items     int     `json:"items"`
	Exact     int     `json:"exact"`
	MeanWidth float64 `json:"meanWidth"`
	// Undecided は、欠落の確認で点数の幅のせいにページに入るかを決められなかった投稿の数。
	Undecided int `json:"undecided"`
	// Mutual、Connections、Engagement、Score は、各項（決まっていたもの）と点数（下限と上限が
	// 一致したもの）の分布。
	Mutual      Distribution `json:"mutual"`
	Connections Distribution `json:"connections"`
	Engagement  Distribution `json:"engagement"`
	Score       Distribution `json:"score"`
}

// Distribution は値の分布。Positive は 1 以上だった割合。
type Distribution struct {
	Count    int     `json:"count"`
	Positive float64 `json:"positive"`
	P50      int64   `json:"p50"`
	P90      int64   `json:"p90"`
	P99      int64   `json:"p99"`
	Max      int64   `json:"max"`
}

// rankingRecorder は RankingStats を集める。フィードの検証はモデルの読み取りロックの中で
// 並行に走るので、別のロックで守る。
type rankingRecorder struct {
	mu        sync.Mutex
	items     int
	exact     int
	width     int64
	undecided int
	values    [4]map[int64]int
}

func (rr *rankingRecorder) record(b scoreBounds) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.items++
	rr.width += b.hi() - b.lo()
	if b.lo() == b.hi() {
		rr.exact++
	}
	for i, v := range [][2]int64{b.mutual, b.connections, b.engagement, {b.lo(), b.hi()}} {
		if v[0] != v[1] {
			continue
		}
		if rr.values[i] == nil {
			rr.values[i] = map[int64]int{}
		}
		rr.values[i][v[0]]++
	}
}

func (rr *rankingRecorder) skip() {
	rr.mu.Lock()
	rr.undecided++
	rr.mu.Unlock()
}

// RankingStats は友達の友達フィードの点数の記録。
func (m *Model) RankingStats() RankingStats {
	rr := &m.ranking
	rr.mu.Lock()
	defer rr.mu.Unlock()
	stats := RankingStats{Items: rr.items, Exact: rr.exact, Undecided: rr.undecided}
	if rr.items > 0 {
		stats.MeanWidth = float64(rr.width) / float64(rr.items)
	}
	for i, d := range []*Distribution{&stats.Mutual, &stats.Connections, &stats.Engagement, &stats.Score} {
		*d = distributionOf(rr.values[i])
	}
	return stats
}

func distributionOf(counts map[int64]int) Distribution {
	var d Distribution
	values := slices.Sorted(func(yield func(int64) bool) {
		for v := range counts {
			if !yield(v) {
				return
			}
		}
	})
	positive := 0
	for _, v := range values {
		d.Count += counts[v]
		if v > 0 {
			positive += counts[v]
		}
	}
	if d.Count == 0 {
		return d
	}
	d.Positive = float64(positive) / float64(d.Count)
	d.Max = values[len(values)-1]
	at := func(q float64) int64 {
		rank := int(q * float64(d.Count-1))
		for _, v := range values {
			if rank < counts[v] {
				return v
			}
			rank -= counts[v]
		}
		return d.Max
	}
	d.P50, d.P90, d.P99 = at(0.5), at(0.9), at(0.99)
	return d
}
