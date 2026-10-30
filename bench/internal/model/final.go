package model

import (
	"fmt"
	"slices"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
)

// LoadMonths は、bench が開いた投稿期間の日付を含む月（各月の 1 日）。
func (m *Model) LoadMonths() []time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var months []time.Time
	for _, w := range m.windows {
		month := time.Date(w.LocalDate.Year(), w.LocalDate.Month(), 1, 0, 0, 0, 0, time.UTC)
		if !slices.ContainsFunc(months, month.Equal) {
			months = append(months, month)
		}
	}
	slices.SortFunc(months, time.Time.Compare)
	return months
}

// CheckOwnPosts は、userID が bench の開いた投稿期間に投稿したものとして Memories に返った
// 投稿 got（全ページ分）を、受け付けられた書き込みと突き合わせる。確実に残っている投稿が
// 無い（失われた）、確実に削除された投稿がある、同じ投稿が 2 度返った、投稿の内容が記録と
// 違う、ほかの人の投稿がある、bench の知らない投稿がある（結果のわからない作成がなければ）
// 場合を違反として返す。
func (m *Model) CheckOwnPosts(userID string, got []client.Post, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var violations []string
	var unknown []client.Post
	seen := make(map[string]bool, len(got))
	for _, item := range got {
		if seen[item.ID] {
			violations = append(violations, fmt.Sprintf("投稿 %s が重複しています", item.ID))
			continue
		}
		seen[item.ID] = true
		p := m.posts[item.ID]
		switch {
		case p == nil && item.Author.ID != userID:
			violations = append(violations, fmt.Sprintf("ほかのユーザーの投稿 %s があります", item.ID))
		case p == nil:
			unknown = append(unknown, item)
		case p.AuthorID != userID:
			violations = append(violations, fmt.Sprintf("ほかのユーザーの投稿 %s があります", item.ID))
		case m.liveTri(p, r) == False:
			violations = append(violations, fmt.Sprintf("削除した投稿 %s が残っています", item.ID))
		default:
			if problem := m.ownPostProblem(item, p, r); problem != "" {
				violations = append(violations, problem)
			}
		}
	}
	violations = append(violations, m.checkUnknownOwnPosts(userID, unknown, r)...)
	for _, p := range m.byAuthor[userID] {
		if !seen[p.ID] && m.liveTri(p, r) == True {
			violations = append(violations, fmt.Sprintf("受け付けられた投稿 %s がありません", p.ID))
		}
	}
	return violations
}

// checkUnknownOwnPosts は、userID の Memories に返った bench の知らない投稿 got を、結果の
// わからない作成と突き合わせる。作成 1 回でできる投稿は 1 件まで、1 人が同じ日付に持てる
// （削除していない）投稿も 1 件まで（bench は知らない投稿を削除しない）。作成の回数より多い、
// 作成が入りえない日付にある、同じ日付にほかの知らない投稿や確実に残っている投稿があるものを
// 違反として返す。
func (m *Model) checkUnknownOwnPosts(userID string, got []client.Post, r Read) []string {
	if len(got) == 0 {
		return nil
	}
	var pending []*pendingPost
	for _, p := range m.pending[userID] {
		if boolTri(p.exists, r) != False {
			pending = append(pending, p)
		}
	}
	var violations []string
	dates := map[string]bool{}
	for i, item := range got {
		if i >= len(pending) {
			violations = append(violations, fmt.Sprintf("bench の知らない投稿 %s があります（結果のわからない作成は %d 回）", item.ID, len(pending)))
			continue
		}
		window := m.windows[item.PostingWindowID]
		if window == nil {
			// bench の知らない投稿期間の投稿は、日付では確かめない。
			continue
		}
		date := window.LocalDate.Format(time.DateOnly)
		switch {
		case dates[date] || m.liveOn(userID, window.LocalDate, r):
			violations = append(violations, fmt.Sprintf("bench の知らない投稿 %s と同じ日付（%s）の投稿がほかにもあります", item.ID, date))
		case !slices.ContainsFunc(pending, func(p *pendingPost) bool { return m.landsOn(p, window.LocalDate, r) }):
			violations = append(violations, fmt.Sprintf("bench の知らない投稿 %s があります（%s に入りえた結果のわからない作成はありません）", item.ID, date))
		}
		dates[date] = true
	}
	return violations
}

// liveOn は、userID が日付 date に確実に残っている（記録のある）投稿を持つか。
func (m *Model) liveOn(userID string, date time.Time, r Read) bool {
	return slices.ContainsFunc(m.byAuthor[userID], func(p *Post) bool {
		return p.LocalDate.Equal(date) && m.liveTri(p, r) == True
	})
}

// CheckOwnPost は、Memories などで返った自分の投稿 got の内容を記録と比べ、違えば理由を返す。
// bench の知らない投稿は確かめない。
func (m *Model) CheckOwnPost(got client.Post, r Read) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p := m.posts[got.ID]; p != nil {
		return m.ownPostProblem(got, p, r)
	}
	return ""
}

// ownPostProblem は投稿 got の内容（投稿者、投稿期間、キャプション、公開範囲、投稿時刻、画像）を
// 記録した投稿 want と比べる。キャプションと公開範囲は、読み取りの間にありうる値のどれかであればよい。
func (m *Model) ownPostProblem(got client.Post, want *Post, r Read) string {
	author := m.users[want.AuthorID]
	switch {
	case got.Author.ID != want.AuthorID || got.Author.Username != author.Username:
		return fmt.Sprintf("投稿 %s の投稿者が違います", want.ID)
	case got.PostingWindowID != want.WindowID:
		return fmt.Sprintf("投稿 %s の投稿期間が違います", want.ID)
	case !want.caption.mayBe(r, got.Caption):
		return fmt.Sprintf("投稿 %s のキャプションが違います", want.ID)
	case !want.visibility.mayBe(r, got.Visibility):
		return fmt.Sprintf("投稿 %s の公開範囲が違います", want.ID)
	case !got.PostedAt.Equal(want.PostedAt):
		return fmt.Sprintf("投稿 %s の投稿時刻が違います", want.ID)
	case imageurl.ImageID(got.ImageURL) != want.ImageID:
		return fmt.Sprintf("投稿 %s の画像が違います", want.ID)
	}
	return ""
}

// CheckFriends は userID の友達の一覧として返ったユーザー got を、友達関係の書き込みと
// 突き合わせる。有効なユーザーとの確実な友達関係が無い、確実に友達でない人がいる、同じ人が
// 2 回以上いる場合を違反として返す。
func (m *Model) CheckFriends(userID string, got []string, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	violations := duplicates(got, "友達の一覧")
	for _, id := range got {
		if friend := m.users[id]; friend == nil || m.activeTri(id, r) == False || m.friendTri(userID, id, r) == False {
			violations = append(violations, fmt.Sprintf("友達でないユーザー %s が友達の一覧にいます", id))
		}
	}
	for id := range m.friendCandidates[userID] {
		friend := m.users[id]
		if friend != nil && m.activeTri(id, r) == True && !slices.Contains(got, id) && m.friendTri(userID, id, r) == True {
			violations = append(violations, fmt.Sprintf("友達 %s が友達の一覧にいません", id))
		}
	}
	return violations
}

// CheckFollowing は userID のフォローの一覧として返ったユーザー got を、フォローの書き込みと
// 突き合わせる。確実にフォローしている有効なユーザーが無い、確実にフォローしていない人
// （bench の知らない人を含む）がいる、同じ人が 2 回以上いる場合を返す。
func (m *Model) CheckFollowing(userID string, got []string, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	problems := duplicates(got, "フォローの一覧")
	for _, id := range got {
		if m.activeTri(id, r) == False || m.followTri(userID, id, r) == False {
			problems = append(problems, fmt.Sprintf("フォローしていないユーザー %s がフォローの一覧にいます", id))
		}
	}
	for id := range m.followCandidates[userID] {
		if m.activeTri(id, r) == True && !slices.Contains(got, id) && m.followTri(userID, id, r) == True {
			problems = append(problems, fmt.Sprintf("フォローしているユーザー %s がフォローの一覧にいません", id))
		}
	}
	return problems
}

// CheckFollowers は userID のフォロワーの一覧として返ったユーザー got を、フォローの書き込みと
// 突き合わせる。確実にフォローしている有効なユーザーが無い、確実にフォローしていない人
// （bench の知らない人を含む）がいる、同じ人が 2 回以上いる場合を返す。
func (m *Model) CheckFollowers(userID string, got []string, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	problems := duplicates(got, "フォロワーの一覧")
	for _, id := range got {
		if m.activeTri(id, r) == False || m.followTri(id, userID, r) == False {
			problems = append(problems, fmt.Sprintf("フォローしていないユーザー %s がフォロワーの一覧にいます", id))
		}
	}
	for id := range m.followedBy[userID] {
		if m.activeTri(id, r) == True && !slices.Contains(got, id) && m.followTri(id, userID, r) == True {
			problems = append(problems, fmt.Sprintf("フォローしているユーザー %s がフォロワーの一覧にいません", id))
		}
	}
	return problems
}

// duplicates は一覧 got（全ページ）に 2 回以上現れる ID を返す。ページ送りで、返したものより
// 後の関係だけを返していれば、並行に関係が変わっても同じ相手は 2 回現れない。
func duplicates(got []string, list string) []string {
	var problems []string
	seen := make(map[string]int, len(got))
	for _, id := range got {
		if seen[id]++; seen[id] == 2 {
			problems = append(problems, fmt.Sprintf("ユーザー %s が%sに重複しています", id, list))
		}
	}
	return problems
}
