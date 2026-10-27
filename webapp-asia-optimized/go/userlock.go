package isureal

import (
	"slices"
	"sync"
)

// ユーザー単位の排他。アプリは 1 プロセスなので DB の行ロック（SELECT ... FOR NO KEY UPDATE）の代わりに使う。
// デッドロックを避けるため、常にユーザー ID の昇順で取得する。
// 使用中のキーだけを持つ（解放されたら消す）。キーは RealMoji ごとなどに増えるので残さない。
type keyLock struct {
	mu   sync.RWMutex
	refs int
}

const keyLockShards = 64

type keyLockShard struct {
	mu sync.Mutex
	m  map[string]*keyLock
}

var keyLocks [keyLockShards]keyLockShard

func init() {
	for i := range keyLocks {
		keyLocks[i].m = map[string]*keyLock{}
	}
}

func keyLockShardOf(key string) *keyLockShard {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h = (h ^ uint32(key[i])) * 16777619
	}
	return &keyLocks[h%keyLockShards]
}

func acquireKeyLock(key string) *keyLock {
	s := keyLockShardOf(key)
	s.mu.Lock()
	l := s.m[key]
	if l == nil {
		l = &keyLock{}
		s.m[key] = l
	}
	l.refs++
	s.mu.Unlock()
	return l
}

func releaseKeyLock(key string, l *keyLock) {
	s := keyLockShardOf(key)
	s.mu.Lock()
	l.refs--
	if l.refs == 0 {
		delete(s.m, key)
	}
	s.mu.Unlock()
}

type userLock struct {
	ID        string
	Exclusive bool
}

func lockUsers(locks ...userLock) (unlock func()) {
	merged := map[string]bool{}
	for _, l := range locks {
		if l.ID == "" {
			continue
		}
		merged[l.ID] = merged[l.ID] || l.Exclusive
	}
	ids := make([]string, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	held := make([]*keyLock, len(ids))
	for i, id := range ids {
		l := acquireKeyLock(id)
		if merged[id] {
			l.mu.Lock()
		} else {
			l.mu.RLock()
		}
		held[i] = l
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(ids) - 1; i >= 0; i-- {
				if merged[ids[i]] {
					held[i].mu.Unlock()
				} else {
					held[i].mu.RUnlock()
				}
				releaseKeyLock(ids[i], held[i])
			}
		})
	}
}

func lockUser(id string) (unlock func()) {
	return lockUsers(userLock{ID: userKey(id), Exclusive: true})
}

// ロックのキー。まとめて lockUsers に渡して ID 順に取得する。
// u: リアクション・関係・投稿削除など閲覧可否に関わる操作、s: セッション、P: 投稿の作成と編集、m: プロフィール
func userKey(id string) string    { return "u:" + id }
func sessionKey(id string) string { return "s:" + id }
func postingKey(id string) string { return "P:" + id }
func profileKey(id string) string { return "m:" + id }

func lockKeys(keys ...string) (unlock func()) {
	locks := make([]userLock, len(keys))
	for i, k := range keys {
		locks[i] = userLock{ID: k, Exclusive: true}
	}
	return lockUsers(locks...)
}

func pairKey(a, b string) string { return "p:" + min(a, b) + "|" + max(a, b) }
