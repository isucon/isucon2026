package model

// friendRequest は bench が送って保留中になった友達申請。
type friendRequest struct {
	from, to string
	// pending は申請が保留中か。承認、拒否、取り下げのほか、2 人の友達関係の成立（相手からの
	// 申請で成立する）とブロックでも閉じる。
	pending *timeline[bool]
}

// FriendRequestCreation は、ID がまだわからない友達申請の送信。
type FriendRequestCreation struct {
	*Write
	request *friendRequest
}

// BeginFriendRequest は from から to への友達申請の送信を記録する。保留中になった申請は、
// 応答の ID で RequestedFriend に記録する。
func (m *Model) BeginFriendRequest(from, to string) *FriendRequestCreation {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	request := &friendRequest{from: from, to: to, pending: newTimeline(false)}
	begin(w, request.pending, true)
	return &FriendRequestCreation{Write: w, request: request}
}

// RequestedFriend は保留中になった申請を ID で記録する。
func (m *Model) RequestedFriend(creation *FriendRequestCreation, relationID string) {
	m.mu.Lock()
	request := creation.request
	m.friendRequests[relationID] = request
	key := pairOf(request.from, request.to)
	m.requestsByPair[key] = append(m.requestsByPair[key], request)
	m.mu.Unlock()
	creation.Applied()
}

// BeginCloseFriendRequest は申請 relationID を閉じる（承認、拒否、取り下げ）書き込みを記録する。
// bench の知らない申請なら何も記録しない。
func (m *Model) BeginCloseFriendRequest(relationID string) *Write {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.newWrite()
	if request := m.friendRequests[relationID]; request != nil {
		begin(w, request.pending, false)
	}
	return w
}

// closeRequestsBetween は a と b の間の申請を閉じる変更を w に加える。m.mu を書き込みで持って
// 呼ぶ。
func (m *Model) closeRequestsBetween(w *Write, a, b string) {
	for _, request := range m.requestsByPair[pairOf(a, b)] {
		begin(w, request.pending, false)
	}
}

// FriendRequestPending は申請 relationID が読み取り r の間ずっと承認や拒否のできる保留中の
// 申請か（2 人とも有効で、友達でもブロックの関係でもない）。bench の知らない申請は不明。
func (m *Model) FriendRequestPending(relationID string, r Read) Tri {
	m.mu.RLock()
	defer m.mu.RUnlock()
	request := m.friendRequests[relationID]
	if request == nil {
		return Unknown
	}
	from, to := request.from, request.to
	return And(boolTri(request.pending, r), m.activeTri(from, r), m.activeTri(to, r), Not(m.friendTri(from, to, r)), Not(m.blockedTri(from, to, r)))
}
