package isureal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const imageURLLifetime = 24 * time.Hour
const imageSignatureIterations = 1000
const imageExpQuantum = 600

func imageSigningKey(secretKeyHash string) ([]byte, error) {
	key, err := hex.DecodeString(secretKeyHash)
	if err != nil || len(key) != sha256.Size {
		return nil, fmt.Errorf("invalid image signing key")
	}
	return key, nil
}

func imageSignature(key []byte, imageID string, exp int64) []byte {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	mac := hmac.New(sha256.New, key)
	buf := make([]byte, 0, sha256.Size)
	for range imageSignatureIterations {
		mac.Reset()
		mac.Write(value)
		buf = mac.Sum(buf[:0])
		value = append(value[:0], buf...)
	}
	return value
}

// 署名は (imageID, exp) ごとに一意（鍵は画像の所有者で固定）なのでキャッシュする。
// キーも値もポインタを含まない map にして GC の走査対象から外す（UUID でない ID はキャッシュしない）
type sigCacheKey struct {
	id  uuidKey
	exp int64
}

type sigCacheShard struct {
	mu sync.RWMutex
	m  map[sigCacheKey][32]byte
}

const sigCacheShards = 64
const imageSignatureCacheLimit = 300000

var imageSignatureCache [sigCacheShards]sigCacheShard

func init() {
	resetImageSignatureCache()
}

func resetImageSignatureCache() {
	for i := range imageSignatureCache {
		c := &imageSignatureCache[i]
		c.mu.Lock()
		c.m = map[sigCacheKey][32]byte{}
		c.mu.Unlock()
	}
}

func cachedImageSignature(key []byte, imageID string, exp int64) []byte {
	id, ok := parseUUIDKey(imageID)
	if !ok {
		return fastImageSignature(key, []byte(strconv.FormatInt(exp, 10)+":"+imageID))
	}
	ck := sigCacheKey{id, exp}
	c := &imageSignatureCache[id[15]%sigCacheShards]
	c.mu.RLock()
	sig, ok := c.m[ck]
	c.mu.RUnlock()
	if ok {
		return sig[:]
	}
	if sig, ok := precomputedSignature(imageID, exp); ok {
		return sig
	}
	computed := fastImageSignature(key, []byte(strconv.FormatInt(exp, 10)+":"+imageID))
	c.mu.Lock()
	if len(c.m) >= imageSignatureCacheLimit/sigCacheShards {
		c.m = map[sigCacheKey][32]byte{}
	}
	c.m[ck] = [32]byte(computed)
	c.mu.Unlock()
	return computed
}

func signedImageURL(user User, imageID string) string {
	// exp を一定の単位に切り下げて署名キャッシュを効かせる
	exp := time.Now().Add(imageURLLifetime).Unix() / imageExpQuantum * imageExpQuantum
	// url.Values.Encode と同じ順序（exp, sig）。base64url と数字はエスケープ不要
	return "/api/images/" + url.PathEscape(imageID) + "?exp=" + strconv.FormatInt(exp, 10) +
		"&sig=" + base64.RawURLEncoding.EncodeToString(cachedImageSignature(user.ImageKey, imageID, exp))
}
