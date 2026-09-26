package isureal

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
)

type exportPostResponse struct {
	createPostResponse
	ImagePath string `json:"imagePath"`
}

func exportPostsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		userID := GetSession(c).UserID
		author, err := getUser(ctx, h.DB, userID)
		if err != nil {
			return err
		}
		var postIDs []string
		if err := h.DB.SelectContext(ctx, &postIDs, `
   SELECT id FROM posts
   WHERE author_id = $1 AND deleted_at IS NULL
   ORDER BY posted_at, id
  `, userID); err != nil {
			return err
		}

		var body bytes.Buffer
		writer := zip.NewWriter(&body)
		defer writer.Close()
		items := make([]exportPostResponse, 0)
		for _, postID := range postIDs {
			post, err := getPost(ctx, h.DB, postID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			var data []byte
			if err := h.DB.GetContext(ctx, &data, `SELECT data FROM image_objects WHERE id = $1`, post.ImageID); err != nil {
				return err
			}
			imagePath := fmt.Sprintf("images/%06d.jpg", len(items)+1)
			image, err := writer.CreateHeader(&zip.FileHeader{Name: imagePath, Method: zip.Store})
			if err != nil {
				return err
			}
			if _, err := image.Write(data); err != nil {
				return err
			}
			items = append(items, exportPostResponse{createPostResponse: newPostResponse(post, author), ImagePath: imagePath})
		}
		manifest, err := writer.Create("posts.json")
		if err != nil {
			return err
		}
		if err := json.NewEncoder(manifest).Encode(items); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="isureal-posts.zip"`)
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.Blob(http.StatusOK, "application/zip", body.Bytes())
	}
}
