package isureal

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

type getImageRequest struct {
	ImageID string `param:"imageId" json:"-"`
	Exp     string `query:"exp" json:"-"`
	Sig     string `query:"sig" json:"-"`
}

func getImageHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		var input getImageRequest
		if err := c.Bind(&input); err != nil {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		query, err := url.ParseQuery(c.Request().URL.RawQuery)
		if err != nil || len(query["exp"]) != 1 || len(query["sig"]) != 1 {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		exp, err := strconv.ParseInt(input.Exp, 10, 64)
		if err != nil || strconv.FormatInt(exp, 10) != input.Exp || exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		sig, err := base64.RawURLEncoding.Strict().DecodeString(input.Sig)
		if err != nil || len(sig) != sha256.Size || base64.RawURLEncoding.EncodeToString(sig) != input.Sig {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}

		ctx := c.Request().Context()
		meta, ok, err := runtimeImageMeta(ctx, h.DB, input.ImageID)
		if !ok {
			meta, err = getImageMeta(ctx, h, input.ImageID)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if err != nil {
			return err
		}
		if owner, err := getUserAnyStatusCached(ctx, h.DB, meta.OwnerID); err != nil || owner.Status != "active" {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		key, err := imageSigningKey(meta.SecretKeyHash)
		if err != nil {
			return err
		}
		if !hmac.Equal(sig, cachedImageSignature(key, input.ImageID, exp)) {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if !meta.Visible {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		data, err := loadImageData(ctx, h, input.ImageID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if err != nil {
			return err
		}
		if exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		return c.Blob(http.StatusOK, meta.MediaType, data)
	}
}

// 画像データは不変なのでメモリにキャッシュする（上限を超えたら破棄）
const imageCacheLimit = 128 << 20

var (
	imageCacheMu   sync.RWMutex
	imageCache     = map[string][]byte{}
	imageCacheSize int
)

func loadImageData(ctx context.Context, h *HandlerContext, imageID string) ([]byte, error) {
	imageCacheMu.RLock()
	data, ok := imageCache[imageID]
	imageCacheMu.RUnlock()
	if ok {
		return data, nil
	}
	if data, err := os.ReadFile(imageDiskPath(imageID)); err == nil {
		storeImageData(imageID, data)
		return data, nil
	}
	if err := h.DB.GetContext(ctx, &data, `SELECT data FROM image_objects WHERE id = $1`, imageID); err != nil {
		return nil, err
	}
	storeImageData(imageID, data)
	writeImageDisk(imageID, data)
	return data, nil
}

// 画像データは ID ごとに不変なので、ローカルディスクにもキャッシュする（DB が正）
const imageDiskDir = "/home/isucon/imgcache"

func imageDiskPath(imageID string) string {
	if len(imageID) < 2 || strings.ContainsAny(imageID, "/.") {
		return filepath.Join(imageDiskDir, "invalid")
	}
	return filepath.Join(imageDiskDir, imageID[:2], imageID)
}

func writeImageDisk(imageID string, data []byte) {
	path := imageDiskPath(imageID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	os.Rename(tmp, path)
}

func storeImageData(imageID string, data []byte) {
	imageCacheMu.Lock()
	defer imageCacheMu.Unlock()
	if imageCacheSize+len(data) > imageCacheLimit {
		imageCache = map[string][]byte{}
		imageCacheSize = 0
	}
	if _, ok := imageCache[imageID]; !ok {
		imageCache[imageID] = data
		imageCacheSize += len(data)
	}
}

type imageMeta struct {
	OwnerID       string `db:"owner_user_id"`
	SecretKeyHash string `db:"secret_key_hash"`
	MediaType     string `db:"media_type"`
	Visible       bool   `db:"visible"`
	loadedAt      time.Time
}

// 表示可否が変わる操作（投稿削除・RealMoji の置換と削除・プロフィール画像の変更・退会）はすべてこのアプリを通るので、
// そこで無効化すればキャッシュし続けてよい。念のため TTL も設ける
const imageMetaTTL = 60 * time.Second

var imageMetaCache = newGenCache[string, imageMeta]()

func getImageMeta(ctx context.Context, h *HandlerContext, imageID string) (imageMeta, error) {
	load := func() (imageMeta, error) {
		var meta imageMeta
		err := h.DB.GetContext(ctx, &meta, `
   SELECT i.owner_user_id, u.secret_key_hash, i.media_type, COALESCE(
     u.profile_image_id = i.id
     OR EXISTS (
       SELECT 1 FROM posts p
       WHERE p.image_id = i.id AND p.author_id = u.id AND p.deleted_at IS NULL
     )
     OR EXISTS (
       SELECT 1 FROM realmojis r
       JOIN posts p ON p.id = r.post_id AND p.deleted_at IS NULL
       JOIN users author ON author.id = p.author_id AND author.status = 'active'
       WHERE r.image_id = i.id AND r.reactor_id = u.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
     ),
     false
   ) AS visible
   FROM image_objects i JOIN users u ON u.id = i.owner_user_id
   WHERE i.id = $1
  `, imageID)
		meta.loadedAt = time.Now()
		return meta, err
	}
	meta, err := imageMetaCache.get(imageID, load)
	if err == nil && time.Since(meta.loadedAt) > imageMetaTTL {
		imageMetaCache.invalidate(imageID)
		meta, err = imageMetaCache.get(imageID, load)
	}
	return meta, err
}
