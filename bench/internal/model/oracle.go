package model

// ここではアプリの閲覧可否とフィードの条件を、bench 側で独立に書き直す。どの関数も
// m.mu を（読み取りでよい）持った状態で呼び、モデルを書き換えない。
//
// 判定はフィードを読むたびに投稿の数だけ繰り返すので、偽と決まった時点で残りの条件を
// 求めない（Kleene の論理積は偽が 1 つあれば偽）。

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
	window := m.windows[p.WindowID]
	if window == nil {
		return Unknown
	}
	return forAll(m.currentWindowOf(window.Region), r, func(id string) Tri {
		switch id {
		case p.WindowID:
			return True
		case "?":
			return Unknown
		default:
			return False
		}
	})
}

// postedTodayTri は、閲覧者のリージョンで開いている投稿期間と同じ日付の投稿を、閲覧者が
// 持っているか。持っていないと他人の投稿は見えない。
func (m *Model) postedTodayTri(viewer *User, r Read) Tri {
	pending := False
	if m.mayHaveUnknownPost(viewer.ID, r) {
		pending = Unknown
	}
	return forAll(m.currentWindowOf(viewer.Region), r, func(id string) Tri {
		if id == "" {
			return False
		}
		window := m.windows[id]
		if window == nil {
			return Unknown
		}
		result := pending
		for _, p := range m.byAuthor[viewer.ID] {
			if p.LocalDate.Equal(window.LocalDate) {
				if result = max(result, m.liveTri(p, r)); result == True {
					return True
				}
			}
		}
		return result
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
	for _, w := range v.windows {
		if w.id == p.WindowID {
			return w.open
		}
	}
	open := v.m.windowOpenTri(p, v.r)
	v.windows = append(v.windows, windowOpen{id: p.WindowID, open: open})
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
