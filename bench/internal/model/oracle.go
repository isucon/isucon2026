package model

// ここではアプリの閲覧可否とフィードの条件を、bench 側で独立に書き直す。どの関数も
// m.mu を（読み取りでよい）持った状態で呼び、モデルを書き換えない。

func boolTri(values []bool) Tri {
	return forAll(values, func(v bool) Tri { return triOf(v) })
}

func (m *Model) friendTri(a, b string, r Read) Tri {
	if tl := m.friendships[pairOf(a, b)]; tl != nil {
		return boolTri(tl.possible(r))
	}
	return False
}

// friendOfFriendTri は a と b に共通の友達がいるか。
func (m *Model) friendOfFriendTri(a, b string, r Read) Tri {
	var terms []Tri
	for c := range m.friendCandidates[a] {
		if c != b && m.friendCandidates[b][c] {
			terms = append(terms, And(m.friendTri(a, c, r), m.friendTri(c, b, r)))
		}
	}
	return Or(terms...)
}

func (m *Model) blockedTri(a, b string, r Read) Tri {
	var terms []Tri
	for _, key := range [][2]string{{a, b}, {b, a}} {
		if tl := m.blocks[key]; tl != nil {
			terms = append(terms, boolTri(tl.possible(r)))
		}
	}
	return Or(terms...)
}

func (m *Model) followTri(follower, followee string, r Read) Tri {
	if tl := m.follows[[2]string{follower, followee}]; tl != nil {
		return boolTri(tl.possible(r))
	}
	return False
}

// liveTri は投稿が作成済みで、削除されていないか。
func (m *Model) liveTri(p *Post, r Read) Tri {
	return And(boolTri(m.postExists[p.ID].possible(r)), Not(boolTri(p.deleted.possible(r))))
}

// windowOpenTri は投稿の投稿期間がまだ開いているか。同じリージョンで次の期間が開くと終わる。
func (m *Model) windowOpenTri(p *Post, r Read) Tri {
	window := m.windows[p.WindowID]
	if window == nil {
		return Unknown
	}
	return forAll(m.currentWindowOf(window.Region).possible(r), func(id string) Tri {
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
	for _, p := range m.pending {
		if p.authorID == viewer.ID && boolTri(p.exists.possible(r)) != False {
			pending = Unknown
		}
	}
	return forAll(m.currentWindowOf(viewer.Region).possible(r), func(id string) Tri {
		if id == "" {
			return False
		}
		window := m.windows[id]
		if window == nil {
			return Unknown
		}
		terms := []Tri{pending}
		for _, p := range m.byAuthor[viewer.ID] {
			if p.LocalDate.Equal(window.LocalDate) {
				terms = append(terms, m.liveTri(p, r))
			}
		}
		return Or(terms...)
	})
}

// canViewTri はアプリの閲覧可否の条件。
func (m *Model) canViewTri(viewer *User, p *Post, r Read) Tri {
	author := m.users[p.AuthorID]
	if viewer == nil || author == nil || !viewer.Active || !author.Active {
		return False
	}
	if viewer.ID == author.ID {
		return m.liveTri(p, r)
	}
	visible := forAll(p.visibility.possible(r), func(visibility string) Tri {
		switch visibility {
		case "public":
			return True
		case "friends":
			return m.friendTri(viewer.ID, author.ID, r)
		case "friends_of_friends":
			return Or(m.friendTri(viewer.ID, author.ID, r), m.friendOfFriendTri(viewer.ID, author.ID, r))
		default:
			return False
		}
	})
	return And(m.liveTri(p, r), m.windowOpenTri(p, r), m.postedTodayTri(viewer, r), Not(m.blockedTri(viewer.ID, author.ID, r)), visible)
}

// inFeedTri は p が viewer の feed に出るべきか。
func (m *Model) inFeedTri(viewer *User, p *Post, feed string, r Read) Tri {
	if viewer == nil || p.AuthorID == viewer.ID {
		return False
	}
	var member Tri
	switch feed {
	case "public":
		member = forAll(p.visibility.possible(r), func(visibility string) Tri { return triOf(visibility == "public") })
	case "following":
		member = m.followTri(viewer.ID, p.AuthorID, r)
	case "friends":
		member = m.friendTri(viewer.ID, p.AuthorID, r)
	case "friends-of-friends":
		member = And(Not(m.friendTri(viewer.ID, p.AuthorID, r)), m.friendOfFriendTri(viewer.ID, p.AuthorID, r))
	default:
		return False
	}
	return And(m.canViewTri(viewer, p, r), member)
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
	return m.canViewTri(m.users[viewerID], p, r)
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
	if viewer.ID == p.AuthorID {
		return And(m.canViewTri(viewer, p, r), m.windowOpenTri(p, r), m.postedTodayTri(viewer, r))
	}
	public := forAll(p.visibility.possible(r), func(visibility string) Tri { return triOf(visibility == "public") })
	return And(m.canViewTri(viewer, p, r), m.windowOpenTri(p, r), Or(public, m.friendTri(viewer.ID, p.AuthorID, r)))
}
