package model

import (
	"fmt"
	"slices"
	"time"
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

// CheckOwnPosts は、userID が bench の開いた投稿期間に投稿したものとして返った投稿 got を、
// 受け付けられた書き込みと突き合わせる。確実に残っている投稿が無い（失われた）、確実に
// 削除された投稿がある、bench の知らない投稿がある（結果のわからない作成がなければ）場合を
// 違反として返す。
func (m *Model) CheckOwnPosts(userID string, got []string, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var violations []string
	for _, id := range got {
		p := m.posts[id]
		switch {
		case p == nil && !m.mayHaveUnknownPost(userID, r):
			violations = append(violations, fmt.Sprintf("bench の知らない投稿 %s があります", id))
		case p != nil && p.AuthorID != userID:
			violations = append(violations, fmt.Sprintf("ほかのユーザーの投稿 %s があります", id))
		case p != nil && m.liveTri(p, r) == False:
			violations = append(violations, fmt.Sprintf("削除した投稿 %s が残っています", id))
		}
	}
	for _, p := range m.byAuthor[userID] {
		if !slices.Contains(got, p.ID) && m.liveTri(p, r) == True {
			violations = append(violations, fmt.Sprintf("受け付けられた投稿 %s がありません", p.ID))
		}
	}
	return violations
}

// CheckFriends は userID の友達の一覧として返ったユーザー got を、友達関係の書き込みと
// 突き合わせる。有効なユーザーとの確実な友達関係が無い、確実に友達でない人がいる場合を
// 違反として返す。
func (m *Model) CheckFriends(userID string, got []string, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var violations []string
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
// 突き合わせる。確実にフォローしている有効なユーザーが無い、確実にフォローしていない人が
// いる場合を返す。
func (m *Model) CheckFollowing(userID string, got []string, r Read) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var problems []string
	for _, id := range got {
		if m.users[id] == nil {
			continue
		}
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
