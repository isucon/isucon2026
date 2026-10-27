package isureal

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"golang.org/x/sync/errgroup"
)

type exportPostResponse struct {
	createPostResponse
	ImagePath string `json:"imagePath"`
}

// exportImageBatch is how many images one query reads. Images are stored with
// the posting time as created_at, so a query that also names the times is
// pruned to their month partitions instead of probing every partition per
// image; an image not found that way is read by ID alone.
const exportImageBatch = 64

// exportBudget bounds an export. When the database is saturated (the grand
// prix's simultaneous posts), an export that would take longer answers 503
// at once rather than holding the connection past the client's deadline.
const exportBudget = 8 * time.Second

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
		var posts []Post
		if err := h.DB.SelectContext(ctx, &posts, `
   SELECT `+postColumns+`
   FROM posts p
   JOIN posting_windows w ON w.id = p.posting_window_id
   WHERE p.author_id = $1 AND p.deleted_at IS NULL
   ORDER BY p.posted_at, p.id
  `, userID); err != nil {
			return err
		}
		if state != nil {
			author, err = readSessionUser(c, h)
			if err != nil {
				return err
			}
		}
		imageData, err := exportImages(ctx, h.DB, posts, time.Now().Add(exportBudget))
		if errors.Is(err, errExportBusy) {
			return NewAPIError(http.StatusServiceUnavailable, "export_busy")
		}
		if err != nil {
			return err
		}

		var body bytes.Buffer
		writer := zip.NewWriter(&body)
		defer writer.Close()
		items := make([]exportPostResponse, 0, len(posts))
		for i, post := range posts {
			imagePath := fmt.Sprintf("images/%06d.jpg", i+1)
			image, err := writer.CreateHeader(&zip.FileHeader{Name: imagePath, Method: zip.Store})
			if err != nil {
				return err
			}
			if _, err := image.Write(imageData[post.ImageID]); err != nil {
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

// exportImages reads the bodies of the posts' images: in batches pruned by
// posting time, a few batches at a time so the database overlaps the random
// reads, then by ID for any image stored under another time, and from the
// image store for images uploaded during the load.
var errExportBusy = errors.New("export over budget")

func exportImages(ctx context.Context, db *sqlx.DB, posts []Post, deadline time.Time) (map[string][]byte, error) {
	data := make(map[string][]byte, len(posts))
	var mu sync.Mutex
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for start := 0; start < len(posts); start += exportImageBatch {
		batch := posts[start:min(start+exportImageBatch, len(posts))]
		group.Go(func() error {
			if time.Now().After(deadline) {
				return errExportBusy
			}
			ids := make([]string, len(batch))
			times := make([]time.Time, len(batch))
			for i, p := range batch {
				ids[i], times[i] = p.ImageID, p.PostedAt
			}
			var rows []struct {
				ID   string `db:"id"`
				Data []byte `db:"data"`
			}
			// A join with the (id, time) pairs is pruned per row to one month
			// partition; "= ANY(array)" conditions were not.
			if err := db.SelectContext(gctx, &rows, `SELECT i.id, i.data
              FROM unnest($1::text[], $2::timestamptz[]) AS n(id, at)
              JOIN image_objects i ON i.id = n.id AND i.created_at = n.at`, ids, times); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			for _, row := range rows {
				data[row.ID] = row.Data
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	for _, p := range posts {
		body, ok := data[p.ImageID]
		if !ok {
			if time.Now().After(deadline) {
				return nil, errExportBusy
			}
			if err := db.GetContext(ctx, &body, `SELECT data FROM image_objects WHERE id = $1`, p.ImageID); err != nil {
				return nil, err
			}
			data[p.ImageID] = body
		}
		if len(body) == 0 {
			if stored, ok := imageBytes(ctx, p.ImageID); ok {
				data[p.ImageID] = stored
			}
		}
	}
	return data, nil
}
