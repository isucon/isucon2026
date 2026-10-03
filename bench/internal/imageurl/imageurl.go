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

// Verify は rawURL が imageID の画像を指し、期限内で、secretKey の持ち主が署名したもの
// であることを確かめる。
func Verify(rawURL, imageID, secretKey string, now time.Time) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("画像 URL %q を解釈できません: %w", rawURL, err)
	}
	if want := "/api/images/" + imageID; parsed.Path != want || parsed.Host != "" {
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
	if sig := query.Get("sig"); !hmac.Equal([]byte(sig), []byte(Sign(secretKey, imageID, exp))) {
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
	return strings.TrimPrefix(parsed.Path, "/api/images/")
}
