package isureal

import (
	"database/sql"
	"errors"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type createPostRequest struct {
	Image        *multipart.FileHeader `form:"image"`
	Caption      string                `form:"caption"`
	Visibility   string                `form:"visibility"`
	LocationKind string                `form:"location_kind"`
	Latitude     *float64              `form:"latitude"`
	Longitude    *float64              `form:"longitude"`
	Locality     *string               `form:"locality"`
}

type postAuthorResponse struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"displayName"`
	ProfileImageURL *string `json:"profileImageUrl"`
	Bio             string  `json:"bio"`
	AccountType     string  `json:"accountType"`
}

type createPostResponse struct {
	ID              string             `json:"id"`
	Author          postAuthorResponse `json:"author"`
	PostingWindowID string             `json:"postingWindowId"`
	ImageURL        string             `json:"imageUrl"`
	Caption         string             `json:"caption"`
	Visibility      string             `json:"visibility"`
	Location        PostLocation       `json:"location"`
	PostedAt        time.Time          `json:"postedAt"`
	OnTime          bool               `json:"onTime"`
	LateSeconds     int64              `json:"lateSeconds"`
}

func createPostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input createPostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if input.Image == nil || !validUserText(input.Caption, 0, 1000) {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		switch input.Visibility {
		case "friends", "friends_of_friends", "public":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		location := PostLocation{
			Kind: input.LocationKind, Latitude: input.Latitude,
			Longitude: input.Longitude, Locality: input.Locality,
		}
		if !location.valid() {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		imageFile, err := input.Image.Open()
		if err != nil {
			return err
		}
		defer imageFile.Close()
		imageData, ok, err := readJPEG(imageFile, 4096)
		if err != nil {
			return err
		}
		if !ok {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}

		result, err := runWrite(c.Request().Context(), h.DB, "post_create", GetSession(c).UserID,
			map[string]any{"caption": input.Caption, "visibility": input.Visibility, "kind": location.Kind,
				"latitude": location.Latitude, "longitude": location.Longitude, "locality": location.Locality}, imageData)
		if err != nil {
			return err
		}
		post, user := result.Post, result.User
		return c.JSON(http.StatusCreated, newPostResponse(post, user))
	}
}

func newPostResponse(post Post, user User) createPostResponse {
	response := createPostResponse{
		ID: post.ID,
		Author: postAuthorResponse{
			ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
			ProfileImageURL: newGetMeResponse(user).ProfileImageURL,
			Bio:             user.Bio, AccountType: user.AccountType,
		},
		PostingWindowID: post.PostingWindowID, ImageURL: signedImageURL(user, post.ImageID),
		Caption: post.Caption, Visibility: post.Visibility, Location: post.PostLocation,
		PostedAt: post.PostedAt, OnTime: !post.PostedAt.After(post.OnTimeDeadline),
	}
	if !response.OnTime {
		response.LateSeconds = int64(post.PostedAt.Sub(post.OnTimeDeadline) / time.Second)
	}
	return response
}

type getMyCurrentPostResponse createPostResponse

func getMyCurrentPostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		if user, ok := hotSessionUser(c, h); ok {
			if post, found, ok := hot.myCurrentPost(user.ID); ok {
				if !found {
					return NewAPIError(http.StatusNotFound, "post_not_found")
				}
				return c.JSON(http.StatusOK, getMyCurrentPostResponse(newPostResponse(post.Post, post.Author)))
			}
		}
		userID := GetSession(c).UserID
		posts, err := selectPostsWithAuthor(ctx, h.DB, `
          AND p.author_id = $1
          AND EXISTS (SELECT 1 FROM posting_windows current
            WHERE current.region = u.region AND current.ended_at IS NULL
              AND current.local_date = w.local_date)
          LIMIT 1`, userID)
		if err != nil {
			return err
		}
		if len(posts) == 0 {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		post := posts[0]
		return c.JSON(http.StatusOK, getMyCurrentPostResponse(newPostResponse(post.Post, post.Author)))
	}
}

type getPostRequest struct {
	PostID string `param:"postId"`
}

type getPostResponse createPostResponse

func getPostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getPostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		if user, ok := hotSessionUser(c, h); ok {
			if post, visible, ok := hot.viewPost(user.ID, input.PostID); ok {
				if !visible {
					return NewAPIError(http.StatusNotFound, "post_not_found")
				}
				return c.JSON(http.StatusOK, getPostResponse(newPostResponse(post.Post, post.Author)))
			}
		}
		post, viewer, err := loadPostAccess(ctx, h.DB, GetSession(c).UserID, input.PostID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		if err != nil {
			return err
		}
		if !viewer.canView(post.Post) {
			return NewAPIError(http.StatusNotFound, "post_not_found")
		}
		return c.JSON(http.StatusOK, getPostResponse(newPostResponse(post.Post, post.Author)))
	}
}

type updatePostRequest struct {
	PostID     string  `param:"postId" json:"-"`
	Caption    *string `json:"caption"`
	Visibility *string `json:"visibility"`
}

type updatePostResponse createPostResponse

func updatePostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input updatePostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if input.Caption == nil && input.Visibility == nil {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if input.Caption != nil && !validUserText(*input.Caption, 0, 1000) {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if input.Visibility != nil {
			switch *input.Visibility {
			case "friends", "friends_of_friends", "public":
			default:
				return NewAPIError(http.StatusBadRequest, "invalid_post")
			}
		}
		result, err := runWrite(c.Request().Context(), h.DB, "post_update", GetSession(c).UserID,
			map[string]any{"id": input.PostID, "caption": input.Caption, "visibility": input.Visibility}, nil)
		if err != nil {
			return err
		}
		post, user := result.Post, result.User
		return c.JSON(http.StatusOK, updatePostResponse(newPostResponse(post, user)))
	}
}

type deletePostRequest struct {
	PostID string `param:"postId" json:"-"`
}

func deletePostHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input deletePostRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if _, err := runWrite(c.Request().Context(), h.DB, "post_delete", GetSession(c).UserID,
			map[string]any{"id": input.PostID}, nil); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}
