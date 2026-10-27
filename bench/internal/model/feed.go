package model

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
)

// FeedPageSize はフィードの 1 ページの件数（アプリの仕様）。
const FeedPageSize = 50

// ReadBetween は sent に送り received に応答を受けた読み取りを表す。
func (m *Model) ReadBetween(sent, received time.Time) Read {
	return Read{Sent: sent, Received: received, Staleness: m.Staleness}
}

// FeedResult はフィードの 1 ページの検証結果。
type FeedResult struct {
	// Violations は見えてはいけない投稿や、存在しない投稿。1 件で失格にする。
	Violations []string
	// Errors は並び順や内容の不一致。エラー率に数える。
	Errors []string
	// Thorough は thorough な検証でだけ見つかるエラー（見えるべき投稿の欠落と、画像 URL の
	// 署名の誤り）。エラー率に数える。
	Thorough []string
}

func (r *FeedResult) violation(format string, args ...any) {
	r.Violations = append(r.Violations, fmt.Sprintf(format, args...))
}

func (r *FeedResult) error(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func (r *FeedResult) thorough(format string, args ...any) {
	r.Thorough = append(r.Thorough, fmt.Sprintf(format, args...))
}

// CheckFeed は viewer が before より古い投稿を feed で読んだ 1 ページを検証する。
//
//   - 返った投稿は、見えてはいけない（判定が偽）ものであってはならない。bench の知らない
//     投稿は、結果のわからない投稿作成のものである場合だけ許す。
//   - ページの範囲（before より古く、次のページがあるなら最後の投稿以降）にある、見える
//     べき（判定が真）投稿は、すべて返っていなければならない。
//   - 投稿時刻の新しい順に並ぶ。同じ時刻の投稿の順は問わない。
//
// 欠落の確認（開いている投稿期間の投稿をユーザー数に比例して調べる）と画像 URL の署名の
// 検証は重いので、thorough のときだけ行う。返った投稿の確認は常に行う。
func (m *Model) CheckFeed(viewerID, feed string, before *client.FeedPost, page client.FeedPage, r Read, thorough bool) FeedResult {
	result, signatures := m.checkFeedLocked(viewerID, feed, before, page, r, thorough)
	if !thorough {
		return result
	}
	// 署名の検証（HMAC を 1000 回）は重いので、ロックの外で、1 ページに signaturesPerPage 件
	// だけ行う。署名の方式の誤りは数件で見つかり、ときどき誤る実装も走行中に何度も引く。
	rand.Shuffle(len(signatures), func(i, j int) { signatures[i], signatures[j] = signatures[j], signatures[i] })
	for _, sig := range signatures[:min(len(signatures), signaturesPerPage)] {
		if err := imageurl.Verify(sig.url, sig.imageID, sig.secretKey, time.Now()); err != nil {
			result.thorough("フィード %s: 投稿 %s の画像 URL が不正です: %v", feed, sig.postID, err)
		}
	}
	return result
}

// signaturesPerPage は、フィードの 1 ページで署名を検証する投稿の数。
const signaturesPerPage = 2

// signature は検証を後回しにした画像 URL の署名。
type signature struct {
	postID, url, imageID, secretKey string
}

func (m *Model) checkFeedLocked(viewerID, feed string, before *client.FeedPost, page client.FeedPage, r Read, thorough bool) (FeedResult, []signature) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result FeedResult
	var signatures []signature
	viewer := m.users[viewerID]
	if viewer == nil {
		result.error("フィード %s: 閲覧者 %s を bench が知りません", feed, viewerID)
		return result, nil
	}
	// 1 ページは FeedPageSize 件。続きがあるのに足りないページを返すと、それより古い投稿の
	// 欠落を確かめられなくなる（最新の 1 件だけ返して軽くすることもできてしまう）。
	if page.HasMore && len(page.Items) < m.PageSize {
		result.error("フィード %s: 続きがあるのに投稿が %d 件です（1 ページ %d 件）", feed, len(page.Items), m.PageSize)
		return result, nil
	}

	v := m.newView(viewer, r)
	seen := make(map[string]bool, len(page.Items))
	for i, item := range page.Items {
		if seen[item.ID] {
			result.error("フィード %s: 投稿 %s が重複しています", feed, item.ID)
			continue
		}
		seen[item.ID] = true
		if before != nil && item.PostedAt.After(before.PostedAt) {
			result.error("フィード %s: 投稿 %s がページ送りの位置より新しいです", feed, item.ID)
		}
		if i > 0 && item.PostedAt.After(page.Items[i-1].PostedAt) {
			result.error("フィード %s: 投稿 %s が新しい順に並んでいません", feed, item.ID)
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
		if thorough {
			signatures = append(signatures, signature{postID: p.ID, url: item.ImageURL, imageID: p.ImageID, secretKey: m.users[p.AuthorID].SecretKey})
		}
	}

	if !thorough {
		return result, nil
	}
	m.feedCandidates(viewer.ID, feed, r, func(p *Post) {
		if seen[p.ID] || (before != nil && !p.PostedAt.Before(before.PostedAt)) {
			return
		}
		if page.HasMore && !p.PostedAt.After(page.Items[len(page.Items)-1].PostedAt) {
			return
		}
		if v.inFeedTri(p, feed) == True {
			result.thorough("フィード %s: %s に見えるべき投稿 %s がありません", feed, viewer.Username, p.ID)
		}
	})
	return result, signatures
}

// mayHaveUnknownPost は authorID に、結果がわからないまま ID の知らない投稿があるかもしれないか。
func (m *Model) mayHaveUnknownPost(authorID string, r Read) bool {
	return slices.ContainsFunc(m.pending[authorID], func(p *pendingPost) bool {
		return boolTri(p.exists, r) != False
	})
}

// openWindows は、読み取りの時点で開いていたかもしれない投稿期間。フィードに出うるのは
// これらの期間の投稿だけ。
func (m *Model) openWindows(r Read) []string {
	var ids []string
	for _, current := range m.current {
		current.each(r, func(id string) {
			if id != "" && id != "?" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		})
	}
	return ids
}

// feedCandidates は、viewerID の feed に出るべき（inFeedTri が真になりうる）投稿を、
// 開いていたかもしれない投稿期間から選んで f に渡す。投稿期間のすべての投稿はユーザー数に
// 比例して増えるので、friends と following は相手の投稿だけを、friends-of-friends は友達の
// 友達の投稿だけを索引から引く。public はすべての投稿が候補になる。
func (m *Model) feedCandidates(viewerID, feed string, r Read, f func(*Post)) {
	open := m.openWindows(r)
	byAuthors := func(authors map[string]bool) {
		for author := range authors {
			for _, p := range m.byAuthor[author] {
				if slices.Contains(open, p.WindowID) {
					f(p)
				}
			}
		}
	}
	all := func() {
		for _, id := range open {
			for _, p := range m.byWindow[id] {
				f(p)
			}
		}
	}
	switch feed {
	case "public":
		all()
	case "following":
		byAuthors(m.followCandidates[viewerID])
	case "friends":
		byAuthors(m.friendCandidates[viewerID])
	case "friends-of-friends":
		// 友達の友達は、友達だったかもしれない相手の、友達だったかもしれない相手に限られる。
		// 初期データの人気のユーザーでは数が投稿より多くなるので、そのときはすべての投稿を見る。
		hops, posts := 0, 0
		for c := range m.friendCandidates[viewerID] {
			hops += len(m.friendCandidates[c])
		}
		for _, id := range open {
			posts += len(m.byWindow[id])
		}
		if hops > posts {
			all()
			return
		}
		authors := make(map[string]bool, hops)
		for c := range m.friendCandidates[viewerID] {
			for author := range m.friendCandidates[c] {
				authors[author] = true
			}
		}
		byAuthors(authors)
	}
}

func (m *Model) checkPostContent(result *FeedResult, feed string, got client.FeedPost, want *Post, r Read) {
	author := m.users[want.AuthorID]
	switch {
	case got.Author.ID != want.AuthorID || got.Author.Username != author.Username:
		result.error("フィード %s: 投稿 %s の投稿者が違います", feed, want.ID)
	case !want.caption.mayBe(r, got.Caption):
		result.error("フィード %s: 投稿 %s のキャプションが違います", feed, want.ID)
	case !got.PostedAt.Equal(want.PostedAt):
		result.error("フィード %s: 投稿 %s の投稿時刻が違います", feed, want.ID)
	case !want.visibility.mayBe(r, got.Visibility):
		result.error("フィード %s: 投稿 %s の公開範囲が違います", feed, want.ID)
	}
}
