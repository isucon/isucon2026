package isureal

import (
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
		data, ok, err := readJPEG(file, 512)
		if err != nil {
			return err
		}
		if !ok {
			return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
		}
		result, err := runWrite(c.Request().Context(), h.DB, "reaction_create", GetSession(c).UserID,
			map[string]any{"postId": input.PostID, "reactionType": input.ReactionType}, data)
		if err != nil {
			return err
		}
		reaction, user := result.Reaction, result.User
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
		reactions, allowed, err := readInteractions(ctx, h.DB, GetSession(c).UserID, input.PostID, false, input.BeforeCreatedAt, input.BeforeID)
		if err != nil {
			return err
		}
		if !allowed {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		items := make([]replaceRealMojiResponse, 0, len(reactions))
		for _, reaction := range reactions {
			items = append(items, newRealMojiResponse(reaction.Reaction, reaction.User))
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
		if _, err := runWrite(c.Request().Context(), h.DB, "reaction_delete", GetSession(c).UserID,
			map[string]any{"id": input.RealMojiID}, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
