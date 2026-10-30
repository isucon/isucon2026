package model

// ここではアプリの閲覧可否とフィードの条件を、bench 側で独立に書き直す。どの関数も
// m.mu を（読み取りでよい）持った状態で呼び、モデルを書き換えない。
//
// 判定はフィードを読むたびに投稿の数だけ繰り返すので、偽と決まった時点で残りの条件を
// 求めない（Kleene の論理積は偽が 1 つあれば偽）。

import "time"

// boolTri は真偽値の履歴 tl が、読み取り r で真か。
func boolTri(tl *timeline[bool], r Read) Tri {
	return forAll(tl, r, triOf)
}

func (m *Model) friendTri(a, b string, r Read) Tri {
	if tl := m.friendships[pairOf(a, b)]; tl != nil {
		return boolTri(tl, r)
	}
	return False
}

// friendOfFriendTri は a と b に共通の友達がいるか。
func (m *Model) friendOfFriendTri(a, b string, r Read) Tri {
	result := False
	for c := range m.friendCandidates[a] {
		if c != b && m.friendCandidates[b][c] {
			if result = max(result, min(m.friendTri(a, c, r), m.friendTri(c, b, r))); result == True {
				return True
			}
		}
	}
	return result
}

func (m *Model) blockedTri(a, b string, r Read) Tri {
	result := False
	for _, key := range [][2]string{{a, b}, {b, a}} {
		if tl := m.blocks[key]; tl != nil {
			result = max(result, boolTri(tl, r))
		}
	}
	return result
}

// activeTri はユーザーが有効か（退会していないか）。知らないユーザーは無効とする。
func (m *Model) activeTri(userID string, r Read) Tri {
	if u := m.users[userID]; u != nil {
		return boolTri(u.active, r)
	}
	return False
}

func (m *Model) followTri(follower, followee string, r Read) Tri {
	if tl := m.follows[[2]string{follower, followee}]; tl != nil {
		return boolTri(tl, r)
	}
	return False
}

// liveTri は投稿が作成済みで、削除されていないか。
func (m *Model) liveTri(p *Post, r Read) Tri {
	exists := boolTri(m.postExists[p.ID], r)
	if exists == False {
		return False
	}
	return min(exists, Not(boolTri(p.deleted, r)))
}

// windowOpenTri は投稿の投稿期間がまだ開いているか。同じリージョンで次の期間が開くと終わる。
func (m *Model) windowOpenTri(p *Post, r Read) Tri {
	return m.windowIDOpenTri(p.WindowID, r)
}

// windowIDOpenTri は投稿期間 windowID がまだ開いているか。
func (m *Model) windowIDOpenTri(windowID string, r Read) Tri {
	window := m.windows[windowID]
	if window == nil {
		return Unknown
	}
	return forAll(m.currentWindowOf(window.Region), r, func(id string) Tri {
		switch id {
		case windowID:
			return True
		case "?":
			return Unknown
		default:
			return False
		}
	})
}

// pendingWindows は、ID のわからない投稿の作成 p が投稿を作ったかもしれない投稿期間を、読み取り
// r の時点までについて f に渡す（同じ ID を何度も渡すことがある。"?" は開いた結果のわからない
// 投稿期間）。作成はアプリが処理した時点で投稿者のリージョンに開いている投稿期間に入る。5xx を
// 返した作成は応答までに処理されているが、送信中やタイムアウトした作成は、あとで処理されて
// その後の投稿期間に入ることがある。
func (m *Model) pendingWindows(p *pendingPost, r Read, f func(id string)) {
	c := p.exists.changes[0]
	author := m.users[p.authorID]
	if author == nil || c.outcome == rejected || c.sent.After(r.Received) {
		return
	}
	span := Read{Sent: c.sent, Received: r.Received, Staleness: m.Staleness}
	if c.ended && c.received.Before(span.Received) {
		span.Received = c.received
	}
	author.region.each(span, func(region string) {
		m.currentWindowOf(region).each(span, func(id string) {
			if id != "" {
				f(id)
			}
		})
	})
}

// unknownPostOnTri は、userID に、結果のわからない作成でできた日付 date の投稿があるかもしれないか
// （不明か偽）。
func (m *Model) unknownPostOnTri(userID string, date time.Time, r Read) Tri {
	for _, p := range m.pending[userID] {
		if boolTri(p.exists, r) == False {
			continue
		}
		on := false
		m.pendingWindows(p, r, func(id string) {
			window := m.windows[id]
			on = on || window == nil || window.LocalDate.Equal(date)
		})
		if on {
			return Unknown
		}
	}
	return False
}

// postedTodayTri は、閲覧者の（今の）リージョンで開いている投稿期間と同じ日付の投稿を、閲覧者が
// 持っているか。持っていないと他人の投稿は見えない。
func (m *Model) postedTodayTri(viewer *User, r Read) Tri {
	// 閲覧者がリージョンを移っている最中なら、移る前と後のどちらの投稿期間でも判定する。
	return forAll(viewer.region, r, func(region string) Tri {
		return forAll(m.currentWindowOf(region), r, func(id string) Tri {
			if id == "" {
				return False
			}
			window := m.windows[id]
			if window == nil {
				return Unknown
			}
			// 結果のわからない作成は、その日付に入ったかもしれない場合だけ不明にする。
			result := m.unknownPostOnTri(viewer.ID, window.LocalDate, r)
			for _, p := range m.byAuthor[viewer.ID] {
				if p.LocalDate.Equal(window.LocalDate) {
					if result = max(result, m.liveTri(p, r)); result == True {
						return True
					}
				}
			}
			return result
		})
	})
}

// view は閲覧者 viewer の読み取り r についての判定。閲覧者と読み取りだけで決まる値
// （閲覧者が今日投稿したか、各投稿期間が開いているか）は一度だけ求めて控え、同じ読み取りで
// 多くの投稿を判定するときに求め直さない。
type view struct {
	m      *Model
	viewer *User
	r      Read

	postedToday    Tri
	hasPostedToday bool
	windows        []windowOpen
}

// windowOpen は投稿期間 id が開いているか（windowOpenTri）の控え。
type windowOpen struct {
	id   string
	open Tri
}

func (m *Model) newView(viewer *User, r Read) view {
	return view{m: m, viewer: viewer, r: r}
}

func (v *view) postedTodayTri() Tri {
	if !v.hasPostedToday {
		v.postedToday, v.hasPostedToday = v.m.postedTodayTri(v.viewer, v.r), true
	}
	return v.postedToday
}

func (v *view) windowOpenTri(p *Post) Tri {
	return v.windowIDOpenTri(p.WindowID)
}

func (v *view) windowIDOpenTri(windowID string) Tri {
	for _, w := range v.windows {
		if w.id == windowID {
			return w.open
		}
	}
	open := v.m.windowIDOpenTri(windowID, v.r)
	v.windows = append(v.windows, windowOpen{id: windowID, open: open})
	return open
}

// canViewTri はアプリの閲覧可否の条件。
func (v *view) canViewTri(p *Post) Tri {
	m, viewer, r := v.m, v.viewer, v.r
	author := m.users[p.AuthorID]
	if viewer == nil || author == nil {
		return False
	}
	active := min(boolTri(viewer.active, r), boolTri(author.active, r))
	if active == False {
		return False
	}
	if viewer.ID == author.ID {
		return min(active, m.liveTri(p, r))
	}
	result := min(active, v.postedTodayTri())
	if result == False {
		return False
	}
	if result = min(result, v.windowOpenTri(p)); result == False {
		return False
	}
	if result = min(result, m.liveTri(p, r)); result == False {
		return False
	}
	if result = min(result, Not(m.blockedTri(viewer.ID, author.ID, r))); result == False {
		return False
	}
	return min(result, forAll(p.visibility, r, func(visibility string) Tri {
		switch visibility {
		case "public":
			return True
		case "friends":
			return m.friendTri(viewer.ID, author.ID, r)
		case "friends_of_friends":
			friend := m.friendTri(viewer.ID, author.ID, r)
			if friend == True {
				return True
			}
			return max(friend, m.friendOfFriendTri(viewer.ID, author.ID, r))
		default:
			return False
		}
	}))
}

// inFeedTri は p が viewer の feed に出るべきか。
func (v *view) inFeedTri(p *Post, feed string) Tri {
	m, viewer, r := v.m, v.viewer, v.r
	if viewer == nil || p.AuthorID == viewer.ID {
		return False
	}
	var member Tri
	switch feed {
	case "public":
		member = forAll(p.visibility, r, func(visibility string) Tri { return triOf(visibility == "public") })
	case "following":
		member = m.followTri(viewer.ID, p.AuthorID, r)
	case "friends":
		member = m.friendTri(viewer.ID, p.AuthorID, r)
	case "friends-of-friends":
		if member = Not(m.friendTri(viewer.ID, p.AuthorID, r)); member != False {
			member = min(member, m.friendOfFriendTri(viewer.ID, p.AuthorID, r))
		}
	default:
		return False
	}
	if member == False {
		return False
	}
	return min(member, v.canViewTri(p))
}

// unknownPostTri は、authorID に、結果のわからない作成でできて、まだ開いている投稿期間にある
// 投稿があるかもしれないか（不明か偽）。
func (v *view) unknownPostTri(authorID string) Tri {
	for _, p := range v.m.pending[authorID] {
		if boolTri(p.exists, v.r) == False {
			continue
		}
		open := false
		v.m.pendingWindows(p, v.r, func(id string) {
			open = open || id == "?" || v.windowIDOpenTri(id) != False
		})
		if open {
			return Unknown
		}
	}
	return False
}

// unknownInFeedTri は、bench の知らない投稿（authorID の、結果のわからない作成でできたかも
// しれない投稿）が viewer の feed に出てよいか。投稿の公開範囲などは知らないので、作成が開いて
// いる投稿期間に入りえたか、閲覧者と投稿者の状態と関係、閲覧者のゲートといった、投稿を知らなくても
// 決まる条件だけを確かめる（真にはならない）。
func (v *view) unknownInFeedTri(authorID, feed string) Tri {
	m, viewer, r := v.m, v.viewer, v.r
	if viewer == nil || authorID == viewer.ID || m.users[authorID] == nil {
		return False
	}
	result := v.unknownPostTri(authorID)
	if result == False {
		return False
	}
	if result = min(result, boolTri(viewer.active, r), m.activeTri(authorID, r)); result == False {
		return False
	}
	if result = min(result, v.postedTodayTri()); result == False {
		return False
	}
	if result = min(result, Not(m.blockedTri(viewer.ID, authorID, r))); result == False {
		return False
	}
	switch feed {
	case "public":
		// 公開範囲がわからないので、public かどうかも決まらない。
		return result
	case "following":
		return min(result, m.followTri(viewer.ID, authorID, r))
	case "friends":
		return min(result, m.friendTri(viewer.ID, authorID, r))
	case "friends-of-friends":
		if result = min(result, Not(m.friendTri(viewer.ID, authorID, r))); result == False {
			return False
		}
		return min(result, m.friendOfFriendTri(viewer.ID, authorID, r))
	default:
		return False
	}
}

// PostLive は投稿が作成済みで削除されていないか。削除された投稿の画像は取得できない。
func (m *Model) PostLive(postID string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.posts[postID]
	if p == nil {
		return Unknown
	}
	return m.liveTri(p, r)
}

// ImageServable は投稿の画像を返してよいか（投稿が残っていて、投稿者が退会していない）。
func (m *Model) ImageServable(postID string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.posts[postID]
	if p == nil {
		return Unknown
	}
	return min(m.liveTri(p, r), m.activeTri(p.AuthorID, r))
}

// WindowOpen は投稿の投稿期間がまだ開いているか（投稿を編集できるか）。
func (m *Model) WindowOpen(postID string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.posts[postID]
	if p == nil {
		return Unknown
	}
	return m.windowOpenTri(p, r)
}

// CanView は viewer が投稿を見られるか（RealMoji を付けられるか）。
func (m *Model) CanView(viewerID, postID string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.posts[postID]
	if p == nil {
		return Unknown
	}
	v := m.newView(m.users[viewerID], r)
	return v.canViewTri(p)
}

// CanComment は viewer が投稿にコメントできるか。見えることに加え、投稿期間が開いていて、
// 公開の投稿か投稿者の友達である必要がある。
func (m *Model) CanComment(viewerID, postID string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.posts[postID]
	viewer := m.users[viewerID]
	if p == nil || viewer == nil {
		return Unknown
	}
	v := m.newView(viewer, r)
	if viewer.ID == p.AuthorID {
		return And(v.canViewTri(p), v.windowOpenTri(p), v.postedTodayTri())
	}
	public := forAll(p.visibility, r, func(visibility string) Tri { return triOf(visibility == "public") })
	return And(v.canViewTri(p), v.windowOpenTri(p), Or(public, m.friendTri(viewer.ID, p.AuthorID, r)))
}
