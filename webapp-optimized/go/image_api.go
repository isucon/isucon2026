package isureal

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/jmoiron/sqlx"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
)

type getImageRequest struct {
	ImageID string `param:"imageId" json:"-"`
	Exp     string `query:"exp" json:"-"`
	Sig     string `query:"sig" json:"-"`
}

// imageLivenessTTL is how long a confirmed-live image is served without asking
// the database again. Images of deleted posts may be served for up to five
// seconds, so this stays well below that.
const imageLivenessTTL = 3 * time.Second

func getImageHandler(h *HandlerContext) echo.HandlerFunc {
	cache := newImageCache(imageCacheBytes, imageCacheEntries)
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
		cached, hit := cache.get(input.ImageID)
		var object imageObject
		if live, keyHash, known := hot.imageLive(input.ImageID); known && (hit || !live) {
			// Post images of open windows: the hot state knows deletion exactly.
			object = imageObject{SecretKeyHash: keyHash, Version: cached.version, MediaType: cached.mediaType, Live: live, Data: cached.data}
		} else if hit && time.Since(cached.checkedAt) < imageLivenessTTL {
			// Deleted images may still be served for a few seconds, so a recent
			// liveness check is reused without a database round trip.
			object = imageObject{SecretKeyHash: cached.secretKeyHash, Version: cached.version, MediaType: cached.mediaType, Live: true, Data: cached.data}
		} else {
			var version *string
			if hit {
				version = &cached.version
			}
			checkedAt := time.Now()
			object, err = loadImageObject(ctx, h.ReadDB, input.ImageID, version)
			if errors.Is(err, sql.ErrNoRows) {
				return NewAPIError(http.StatusForbidden, "image_forbidden")
			}
			if err != nil {
				return err
			}
			if object.Live {
				if hit && cached.version == object.Version {
					object.Data = cached.data
				}
				cache.put(input.ImageID, object, checkedAt)
			}
		}
		key, err := imageSigningKey(object.SecretKeyHash)
		if err != nil {
			return err
		}
		if !hmac.Equal(sig, imageSignature(key, input.ImageID, exp)) || !object.Live {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		if exp <= time.Now().Unix() {
			return NewAPIError(http.StatusForbidden, "image_forbidden")
		}
		return c.Blob(http.StatusOK, object.MediaType, object.Data)
	}
}

// Metadata, current visibility and (only on a version miss) image bytes are read
// in one database snapshot. No cached authorization decisions are reused.
type imageObject struct {
	SecretKeyHash string `db:"secret_key_hash"`
	Version       string `db:"content_version"`
	MediaType     string `db:"media_type"`
	Live          bool   `db:"live"`
	Data          []byte `db:"data"`
}

func loadImageObject(ctx context.Context, db sqlx.QueryerContext, id string, cachedVersion *string) (imageObject, error) {
	var object imageObject
	err := sqlx.GetContext(ctx, db, &object, `
      WITH metadata AS MATERIALIZED (
        SELECT i.id, i.content_version, i.media_type, u.secret_key_hash,
          (u.profile_image_id = i.id OR EXISTS (
            SELECT 1 FROM posts p WHERE p.image_id=i.id AND p.author_id=u.id AND p.deleted_at IS NULL
          ) OR EXISTS (
            SELECT 1 FROM realmojis r
            JOIN posts p ON p.id=r.post_id AND p.deleted_at IS NULL
            JOIN users author ON author.id=p.author_id AND author.status='active'
            WHERE r.image_id=i.id AND r.reactor_id=u.id AND r.replaced_at IS NULL AND r.deleted_at IS NULL
          )) IS TRUE AS live
        FROM image_objects i JOIN users u ON u.id=i.owner_user_id AND u.status='active'
        WHERE i.id=$1
      )
      SELECT secret_key_hash, content_version::text, media_type, live,
        CASE WHEN live AND content_version IS DISTINCT FROM $2::uuid
          THEN (SELECT data FROM image_objects WHERE id=metadata.id) END AS data
      FROM metadata
    `, id, cachedVersion)
	return object, err
}
