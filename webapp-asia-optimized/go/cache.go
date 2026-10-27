package isureal

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// アプリケーションは 1 プロセスで全リージョンを処理するので、書き込み時に無効化すればメモリキャッシュが一貫する。
// 無効化のたびに世代を進め、読み込み中に無効化された古い値は保存しない。
type genCache[K comparable, V any] struct {
	mu    sync.Mutex
	m     map[K]V
	gen   map[K]uint64
	epoch uint64
}

func newGenCache[K comparable, V any]() *genCache[K, V] {
	return &genCache[K, V]{m: map[K]V{}, gen: map[K]uint64{}}
}

func (c *genCache[K, V]) get(key K, load func() (V, error)) (V, error) {
	c.mu.Lock()
	if v, ok := c.m[key]; ok {
		c.mu.Unlock()
		return v, nil
	}
	gen, epoch := c.gen[key], c.epoch
	c.mu.Unlock()
	v, err := load()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	if c.gen[key] == gen && c.epoch == epoch {
		c.m[key] = v
	}
	c.mu.Unlock()
	return v, nil
}

// 新しく作ったデータ（まだ誰も読んでいないキー）を入れる
func (c *genCache[K, V]) set(key K, v V) {
	c.mu.Lock()
	c.m[key] = v
	c.mu.Unlock()
}

func (c *genCache[K, V]) peek(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *genCache[K, V]) generation(key K) (uint64, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen[key], c.epoch
}

// 読み込み前に取得した世代から変わっていなければ保存する
func (c *genCache[K, V]) storeIfGeneration(key K, v V, gen, epoch uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen[key] == gen && c.epoch == epoch {
		c.m[key] = v
	}
}

func (c *genCache[K, V]) invalidate(keys ...K) {
	c.mu.Lock()
	for _, key := range keys {
		delete(c.m, key)
		c.gen[key]++
	}
	c.mu.Unlock()
}

func (c *genCache[K, V]) clear() {
	c.mu.Lock()
	c.m = map[K]V{}
	c.gen = map[K]uint64{}
	c.epoch++
	c.mu.Unlock()
}

var userCache = newGenCache[string, User]()

// 状態にかかわらずユーザーを取得する（キャッシュ経由）
func getUserAnyStatusCached(ctx context.Context, db sqlx.QueryerContext, userID string) (User, error) {
	return userCache.get(userID, func() (User, error) {
		var user User
		if err := sqlx.GetContext(ctx, db, &user, `SELECT `+strings.Join(userColumns, ", ")+` FROM users WHERE id = $1`, userID); err != nil {
			return user, err
		}
		return user, user.setImageKey()
	})
}

// getUser と同じく active なユーザーのみ返す
func getUserCached(ctx context.Context, db sqlx.QueryerContext, userID string) (User, error) {
	user, err := getUserAnyStatusCached(ctx, db, userID)
	if err != nil {
		return user, err
	}
	if user.Status != "active" {
		return User{}, sql.ErrNoRows
	}
	return user, nil
}

type cachedLoginSession struct {
	UserID    string    `db:"user_id"`
	TokenHash string    `db:"token_hash"`
	CreatedAt time.Time `db:"created_at"`
	Active    bool      `db:"active"`
}

var sessionCache = newGenCache[string, cachedLoginSession]()

func getLoginSessionByTokenHashCached(ctx context.Context, db sqlx.QueryerContext, tokenHash string) (cachedLoginSession, error) {
	return sessionCache.get(tokenHash, func() (cachedLoginSession, error) {
		var session cachedLoginSession
		err := sqlx.GetContext(ctx, db, &session, `SELECT user_id, token_hash, created_at, active FROM login_sessions WHERE token_hash = $1 ORDER BY active DESC LIMIT 1`, tokenHash)
		return session, err
	})
}

// username と user id の対応は不変
var usernameToID sync.Map

func userIDByUsername(ctx context.Context, db sqlx.QueryerContext, username string) (string, error) {
	if id, ok := usernameToID.Load(username); ok {
		return id.(string), nil
	}
	var id string
	if err := sqlx.GetContext(ctx, db, &id, `SELECT id FROM users WHERE username = $1`, username); err != nil {
		return "", err
	}
	usernameToID.Store(username, id)
	return id, nil
}

func clearAllCaches() {
	usernameToID.Clear()
	userCache.clear()
	sessionCache.clear()
	imageMetaCache.clear()
	resetRuntimeImages()
	resetImageSignatureCache()
	resetPostJSONCache()
	authorPostsCache.clear()
	interactionsCache.clear()
}
