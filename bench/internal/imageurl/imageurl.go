// Package imageurl は、アプリが返す署名付き画像 URL を検証する。
//
// URL は /api/images/{imageId}?exp={unix 秒}&sig={署名} の形で、署名は画像の持ち主の
// sha256(シークレットキー) を鍵にして "{exp}:{imageId}" に HMAC-SHA256 を 1000 回
// 重ねたもの（base64url、パディングなし）。bench は持ち主のシークレットキーを知って
// いるので、持ち主が署名したことを確かめられる。
package imageurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	iterations = 1000
	// maxLifetime はアプリが付ける有効期限（24 時間）に、時計のずれの余裕を足したもの。
	maxLifetime = 24*time.Hour + time.Minute
)

// Sign は持ち主のシークレットキーから署名を計算する。
func Sign(secretKey, imageID string, exp int64) string {
	key := sha256.Sum256([]byte(secretKey))
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	// 1000 回の HMAC で同じ鍵を使うので、HMAC と出力の領域を使い回す（負荷走行では 1 秒に
	// 数千件の署名を確かめるので、回ごとに作り直すと割り当てと GC が CPU の大半を占める）。
	mac := hmac.New(sha256.New, key[:])
	var sum [sha256.Size]byte
	for i := range iterations {
		if i > 0 {
			mac.Reset()
		}
		mac.Write(value)
		value = mac.Sum(sum[:0])
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

// prefix は画像 URL のパスの始まり。
const prefix = "/api/images/"

// parse は rawURL を、/api/images/ で始まる相対 URL として解釈する。スキームやホストの
// 付いた URL（invalid:/api/images/... や //host/api/images/... など）は、パスが正しくても
// アプリと同じオリジンの画像として取得できないので拒否する。
func parse(rawURL string) (*url.URL, error) {
	if !strings.HasPrefix(rawURL, prefix) {
		return nil, fmt.Errorf("画像 URL %q が %s で始まる相対 URL ではありません", rawURL, prefix)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("画像 URL %q を解釈できません: %w", rawURL, err)
	}
	return parsed, nil
}

// CheckForm は rawURL が /api/images/{imageId} で始まる相対 URL であることだけを確かめる
// （署名は確かめない）。この形でない URL は取得できない。
func CheckForm(rawURL string) error {
	parsed, err := parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.Path == prefix {
		return fmt.Errorf("画像 URL %q に画像 ID がありません", rawURL)
	}
	return nil
}

// Verify は rawURL が imageID の画像を指し、期限内で、secretKey の持ち主が署名したもの
// であることを確かめる。
func Verify(rawURL, imageID, secretKey string, now time.Time) error {
	parsed, err := parse(rawURL)
	if err != nil {
		return err
	}
	if want := prefix + imageID; parsed.Path != want {
		return fmt.Errorf("画像 URL %q のパスが %s ではありません", rawURL, want)
	}
	query := parsed.Query()
	exp, err := strconv.ParseInt(query.Get("exp"), 10, 64)
	if err != nil {
		return fmt.Errorf("画像 URL %q の exp が不正です", rawURL)
	}
	expiresAt := time.Unix(exp, 0)
	if !expiresAt.After(now) || expiresAt.After(now.Add(maxLifetime)) {
		return fmt.Errorf("画像 URL %q の有効期限 %s が範囲外です", rawURL, expiresAt.UTC().Format(time.RFC3339))
	}
	if sig := query.Get("sig"); !hmac.Equal([]byte(sig), []byte(signatures.sign(secretKey, imageID, exp))) {
		return fmt.Errorf("画像 URL %q の署名が画像の持ち主の鍵と一致しません", rawURL)
	}
	return nil
}

// ImageID は画像 URL の画像 ID を返す。
func ImageID(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(parsed.Path, prefix)
}

// signatures は Verify が計算した署名の控え。負荷走行では同じ投稿の画像 URL を多くの
// ユーザーが何度も確かめる（アプリが URL を作り直しても、同じ秒のうちは exp が同じ）ので、
// 1 回に HMAC を 1000 回重ねる計算を繰り返さない。控えるのは bench が計算した正しい署名
// なので、アプリが返す署名の誤りは毎回見つかる。
var signatures = &signatureCache{limit: 1 << 16}

type signatureKey struct {
	secretKey, imageID string
	exp                int64
}

// signatureCache は 2 世代の控え。今の世代が limit 件になったら前の世代を捨てる。
type signatureCache struct {
	limit             int
	mu                sync.Mutex
	current, previous map[signatureKey]string
}

func (c *signatureCache) sign(secretKey, imageID string, exp int64) string {
	key := signatureKey{secretKey: secretKey, imageID: imageID, exp: exp}
	c.mu.Lock()
	sig, ok := c.current[key]
	if !ok {
		if sig, ok = c.previous[key]; ok {
			c.store(key, sig)
		}
	}
	c.mu.Unlock()
	if ok {
		return sig
	}
	sig = Sign(secretKey, imageID, exp)
	c.mu.Lock()
	c.store(key, sig)
	c.mu.Unlock()
	return sig
}

// store は c.mu を持って呼ぶ。
func (c *signatureCache) store(key signatureKey, sig string) {
	if len(c.current) >= c.limit {
		c.previous, c.current = c.current, nil
	}
	if c.current == nil {
		c.current = make(map[signatureKey]string)
	}
	c.current[key] = sig
}
