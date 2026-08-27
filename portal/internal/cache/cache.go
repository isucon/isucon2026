// Package cache はダッシュボードの事前生成キャッシュ（CT-5 / NF-1）を Redis に置く。
// キーは単一開催（P-3）につき round セグメントを落とす:
//
//	dashboard-v2:{visibility}[:team-{id}] / leaderboard-v2:admin
//
// 値は SHA-384 ダイジェスト（48 バイト）+ エンコード済みレスポンスの連結。
// Rails と並走する期間は Redis の DB index を分けること（計画書 §3）。
package cache

import (
	"context"
	"crypto/sha512"
	"errors"

	"github.com/redis/go-redis/v9"
)

const digestLen = sha512.Size384

type Cache struct {
	redis *redis.Client
}

func New(client *redis.Client) *Cache { return &Cache{redis: client} }

// Write はレスポンスの wire バイト列をダイジェスト付きで保存する。
func (c *Cache) Write(ctx context.Context, key string, wire []byte) error {
	digest := sha512.Sum384(wire)
	value := make([]byte, 0, digestLen+len(wire))
	value = append(value, digest[:]...)
	value = append(value, wire...)
	return c.redis.Set(ctx, key, value, 0).Err()
}

// Read は (ダイジェスト, wire, 見つかったか) を返す。
func (c *Cache) Read(ctx context.Context, key string) (digest, wire []byte, ok bool, err error) {
	value, err := c.redis.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	if len(value) < digestLen {
		return nil, nil, false, nil
	}
	return value[:digestLen], value[digestLen:], true, nil
}

func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return c.redis.Del(ctx, keys...).Err()
}
