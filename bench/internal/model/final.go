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
	seen := make(map[string]bool, len(got))
	for _, item := range got {
		if seen[item.ID] {
			violations = append(violations, fmt.Sprintf("投稿 %s が重複しています", item.ID))
			continue
		}
		seen[item.ID] = true
		p := m.posts[item.ID]
		switch {
		case p == nil && !m.mayHaveUnknownPost(userID, r):
			violations = append(violations, fmt.Sprintf("bench の知らない投稿 %s があります", item.ID))
		case p == nil && item.Author.ID != userID:
			violations = append(violations, fmt.Sprintf("ほかのユーザーの投稿 %s があります", item.ID))
		case p == nil:
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
	for _, p := range m.byAuthor[userID] {
		if !seen[p.ID] && m.liveTri(p, r) == True {
			violations = append(violations, fmt.Sprintf("受け付けられた投稿 %s がありません", p.ID))
		}
	}
	return violations
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
