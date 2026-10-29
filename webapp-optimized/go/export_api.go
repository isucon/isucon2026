package isureal

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

// exportStatementTimeout bounds each image batch on the server.
const exportStatementTimeout = 5 * time.Second

func exportPostsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		// A user of the initial data who has not posted since the load has
		// exactly the history store's posts.
		if user, ok := hotSessionUser(c, h); ok {
			if posted, ok := hot.postedSinceLoad(user.ID); ok && !posted {
				if posts, ok := history.exportPosts(user.ID); ok {
					return writeExport(c, h, user, posts)
				}
			}
		}
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
		return writeExport(c, h, author, posts)
	}
}

// writeExport writes the archive of the author's posts.
func writeExport(c *echo.Context, h *HandlerContext, author User, posts []Post) error {
	ctx := c.Request().Context()
	imageData, err := exportImages(ctx, h.DB, posts)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" {
		return NewAPIError(http.StatusServiceUnavailable, "export_busy")
	}
	if err != nil {
		return err
	}

	items := make([]exportPostResponse, 0, len(posts))
	for i, post := range posts {
		items = append(items, exportPostResponse{createPostResponse: newPostResponse(post, author), ImagePath: fmt.Sprintf("images/%06d.jpg", i+1)})
	}
	// Everything that can fail is done; the archive is written straight to
	// the connection instead of being assembled in memory first.
	c.Response().Header().Set(echo.HeaderContentType, "application/zip")
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="isureal-posts.zip"`)
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	c.Response().WriteHeader(http.StatusOK)
	out := bufio.NewWriterSize(c.Response(), 64<<10)
	writer := zip.NewWriter(out)
	for i, post := range posts {
		image, err := writer.CreateHeader(&zip.FileHeader{Name: items[i].ImagePath, Method: zip.Store})
		if err != nil {
			return nil
		}
		if _, err := image.Write(imageData[post.ImageID]); err != nil {
			return nil
		}
	}
	// The manifest shrinks several times; the images are stored as they are.
	writer.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestSpeed)
	})
	manifest, err := writer.Create("posts.json")
	if err != nil {
		return nil
	}
	if err := json.NewEncoder(manifest).Encode(items); err != nil {
		return nil
	}
	if err := writer.Close(); err != nil {
		return nil
	}
	_ = out.Flush()
	return nil
}

// exportImages reads the bodies of the posts' images: in batches pruned by
// posting time, a few batches at a time so the database overlaps the random
// reads, then by ID for any image stored under another time, and from the
// image store for images uploaded during the load.
func exportImages(ctx context.Context, db *sqlx.DB, posts []Post) (map[string][]byte, error) {
	data := make(map[string][]byte, len(posts))
	if pack != nil {
		ids := make([]string, len(posts))
		for i, p := range posts {
			ids[i] = p.ImageID
		}
		packed, err := pack.readMany(ids)
		if err != nil {
			return nil, err
		}
		data = packed
		rest := make([]Post, 0, len(posts)-len(packed))
		for _, p := range posts {
			if _, ok := packed[p.ImageID]; !ok {
				rest = append(rest, p)
			}
		}
		posts = rest
	}
	var mu sync.Mutex
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for start := 0; start < len(posts); start += exportImageBatch {
		batch := posts[start:min(start+exportImageBatch, len(posts))]
		group.Go(func() error {
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
			// partition; "= ANY(array)" conditions were not. The statement is
			// bounded on the server: when the database is saturated (the grand
			// prix's simultaneous posts) the export answers 503 instead of
			// holding the connection past the client's deadline.
			tx, err := db.BeginTxx(gctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(gctx, `SET LOCAL statement_timeout = `+strconv.Itoa(int(exportStatementTimeout/time.Millisecond))); err != nil {
				return err
			}
			if err := tx.SelectContext(gctx, &rows, `SELECT i.id, i.data
              FROM unnest($1::text[], $2::timestamptz[]) AS n(id, at)
              JOIN image_objects i ON i.id = n.id AND i.created_at = n.at`, ids, times); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
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
