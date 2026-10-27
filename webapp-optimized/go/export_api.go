package isureal

import (
	"archive/zip"
	"bytes"
	"encoding/json"
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
		var author User
		var err error
		state, _ := ctx.Value(batchSessionKey{}).(*batchSession)
		if state != nil {
			state.includeUser = true
		} else {
			author, err = readSessionUser(c, h)
			if err != nil {
				return err
			}
		}
		rows, err := h.DB.QueryxContext(ctx, `
   SELECT `+postColumns+`, i.data AS image_data
   FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   JOIN image_objects i ON i.id = p.image_id
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
   ORDER BY p.posted_at, p.id
  `, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		if state != nil {
			author, err = readSessionUser(c, h)
			if err != nil {
				return err
			}
		}

		var body bytes.Buffer
		writer := zip.NewWriter(&body)
		defer writer.Close()
		items := make([]exportPostResponse, 0)
		for rows.Next() {
			var post struct {
				Post
				ImageData []byte `db:"image_data"`
			}
			if err := rows.StructScan(&post); err != nil {
				return err
			}
			imagePath := fmt.Sprintf("images/%06d.jpg", len(items)+1)
			image, err := writer.CreateHeader(&zip.FileHeader{Name: imagePath, Method: zip.Store})
			if err != nil {
				return err
			}
			if _, err := image.Write(post.ImageData); err != nil {
				return err
			}
			items = append(items, exportPostResponse{createPostResponse: newPostResponse(post.Post, author), ImagePath: imagePath})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
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
