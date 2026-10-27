package isureal

import (
	"bytes"
	"database/sql"
	"errors"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type replaceRealMojiRequest struct {
	PostID       string                `param:"postId" json:"-"`
	Image        *multipart.FileHeader `form:"image"`
	ReactionType string                `form:"reactionType"`
}

type replaceRealMojiResponse struct {
	ID           string                   `json:"id"`
	Reactor      relationshipUserResponse `json:"reactor"`
	ReactionType string                   `json:"reactionType"`
	ImageURL     string                   `json:"imageUrl"`
	CreatedAt    time.Time                `json:"createdAt"`
}

func newRealMojiResponse(reaction RealMoji, user User) replaceRealMojiResponse {
	registerRealMojiImage(reaction)
	return replaceRealMojiResponse{ID: reaction.ID, Reactor: newRelationshipUserResponse(user), ReactionType: reaction.ReactionType, ImageURL: signedImageURL(user, reaction.ImageID), CreatedAt: reaction.CreatedAt}
}

func replaceRealMojiHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input replaceRealMojiRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if input.Image == nil {
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}
		switch input.ReactionType {
		case "like", "smile", "surprise", "heart", "laugh", "instant":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}
		file, err := input.Image.Open()
		if err != nil {
			return err
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}
		if img.Bounds().Dx() > 512 || img.Bounds().Dy() > 512 {
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}

		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		// RealMoji を送れるのはアクティブな期間の他人の投稿だけ
		post, ok := state.activePost(input.PostID)
		if !ok {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		if actor == post.AuthorID {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		// 同じ投稿への同じユーザーの RealMoji の置き換えは直列化する
		unlockReaction := lockKeys("r:" + post.ID + "|" + actor)
		defer unlockReaction()
		tx, err := beginRelationshipTxWithTargetLock(ctx, h.DB, actor, post.AuthorID, false)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		allowed, err := canViewPost(ctx, h.DB, actor, post.ID)
		if err != nil {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		var replacedImageIDs []string
		if err := tx.SelectContext(ctx, &replacedImageIDs, `UPDATE realmojis SET replaced_at=clock_timestamp() WHERE post_id=$1 AND reactor_id=$2 AND replaced_at IS NULL AND deleted_at IS NULL RETURNING image_id`, post.ID, actor); err != nil {
			return err
		}
		hideRuntimeImages(replacedImageIDs...)
		defer interactionsCache.invalidate(post.ID)
		defer imageMetaCache.invalidate(replacedImageIDs...)
		imageID := newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,$3,'image/jpeg')`, imageID, actor, data); err != nil {
			return err
		}
		reaction := RealMoji{ID: newID(), PostID: post.ID, ReactorID: actor, ImageID: imageID, ReactionType: input.ReactionType}
		if err := tx.GetContext(ctx, &reaction.CreatedAt, `INSERT INTO realmojis(id,post_id,reactor_id,image_id,reaction_type,created_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()) RETURNING created_at`, reaction.ID, post.ID, actor, imageID, input.ReactionType); err != nil {
			return err
		}
		user, err := getUserCached(ctx, h.DB, actor)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		interactionsCache.invalidate(post.ID)
		imageMetaCache.invalidate(replacedImageIDs...)
		storeImageData(imageID, data)
		registerRealMojiImage(reaction)
		return c.JSON(http.StatusCreated, newRealMojiResponse(reaction, user))
	}
}

type getRealMojisRequest struct {
	PostID          string     `param:"postId"`
	BeforeCreatedAt *time.Time `query:"beforeCreatedAt"`
	BeforeID        string     `query:"beforeId"`
}

type getRealMojisResponse struct {
	Items   []replaceRealMojiResponse `json:"items"`
	HasMore bool                      `json:"hasMore"`
}

func getRealMojisHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getRealMojisRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if (input.BeforeCreatedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		allowed, err := canViewPost(ctx, h.DB, GetSession(c).UserID, input.PostID)
		if err != nil {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		reactions, err := selectCurrentRealMojis(ctx, h.DB, `AND r.post_id=$1 AND ($2::timestamptz IS NULL OR (r.created_at,r.id)<($2,$3)) ORDER BY r.created_at DESC,r.id DESC LIMIT 51`, input.PostID, input.BeforeCreatedAt, input.BeforeID)
		if err != nil {
			return err
		}
		items := make([]replaceRealMojiResponse, 0, len(reactions))
		for _, reaction := range reactions {
			items = append(items, newRealMojiResponse(reaction.RealMoji, reaction.Reactor))
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, getRealMojisResponse{Items: items, HasMore: hasMore})
	}
}

type deleteRealMojiRequest struct {
	RealMojiID string `param:"realMojiId" json:"-"`
}

func deleteRealMojiHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input deleteRealMojiRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		unlock := lockUser(actor)
		defer unlock()
		if _, err := getUserAnyStatusCached(ctx, h.DB, actor); err != nil {
			return err
		}
		tx, err := h.DB.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var record struct {
			ReactorID string `db:"reactor_id"`
			AuthorID  string `db:"author_id"`
			PostID    string `db:"post_id"`
		}
		err = tx.GetContext(ctx, &record, `SELECT r.reactor_id,p.author_id,r.post_id FROM realmojis r JOIN posts p ON p.id=r.post_id WHERE r.id=$1 AND r.deleted_at IS NULL FOR UPDATE OF r`, input.RealMojiID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "interaction_not_found")
		}
		if err != nil {
			return err
		}
		if actor != record.ReactorID && actor != record.AuthorID {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		var deletedImageID string
		if err := tx.GetContext(ctx, &deletedImageID, `UPDATE realmojis SET deleted_at=clock_timestamp() WHERE id=$1 RETURNING image_id`, input.RealMojiID); err != nil {
			return err
		}
		hideRuntimeImages(deletedImageID)
		defer imageMetaCache.invalidate(deletedImageID)
		defer interactionsCache.invalidate(record.PostID)
		if err := tx.Commit(); err != nil {
			return err
		}
		interactionsCache.invalidate(record.PostID)
		imageMetaCache.invalidate(deletedImageID)
		return c.NoContent(http.StatusNoContent)
	}
}
