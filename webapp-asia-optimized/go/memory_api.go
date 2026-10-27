package isureal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getMemoriesRequest struct {
	Month          string     `query:"month"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type memoryPostResponse struct {
	createPostResponse
	RealMojis []replaceRealMojiResponse `json:"realmojis"`
	Comments  []createCommentResponse   `json:"comments"`
}

type getMemoriesResponse struct {
	Items   []memoryPostResponse `json:"items"`
	HasMore bool                 `json:"hasMore"`
}

type getMemoriesByDateRequest struct {
	Date           string     `param:"date"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type getMemoriesByDateResponse getMemoriesResponse

func getMemoriesHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getMemoriesRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		start, err := time.Parse("2006-01", input.Month)
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
		}
		response, err := listMemories(c.Request().Context(), h.DB, GetSession(c).UserID, start, start.AddDate(0, 1, 0), input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, response)
	}
}

func getMemoriesByDateHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getMemoriesByDateRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		start, err := time.Parse(time.DateOnly, input.Date)
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
		}
		response, err := listMemories(c.Request().Context(), h.DB, GetSession(c).UserID, start, start.AddDate(0, 0, 1), input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, getMemoriesByDateResponse(response))
	}
}

// 著者ごとの削除されていない投稿（投稿・編集・削除のたびに無効化する）
var authorPostsCache = newGenCache[string, *authorPostList]()

func getAuthorPosts(ctx context.Context, db sqlx.QueryerContext, authorID string) (*authorPostList, error) {
	return authorPostsCache.get(authorID, func() (*authorPostList, error) {
		var posts []Post
		if err := sqlx.SelectContext(ctx, db, &posts, `
  SELECT id, author_id, posting_window_id, image_id, caption, visibility,
         location_kind, latitude, longitude, locality, posted_at
  FROM posts WHERE author_id = $1 AND deleted_at IS NULL
 `, authorID); err != nil {
			return nil, err
		}
		return newAuthorPostList(posts), nil
	})
}

func listMemories(ctx context.Context, db sqlx.QueryerContext, userID string, start, end time.Time, beforeAt *time.Time, beforeID string) (getMemoriesResponse, error) {
	var response getMemoriesResponse
	if (beforeAt == nil) != (beforeID == "") {
		return response, NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	author, err := getUserCached(ctx, db, userID)
	if errors.Is(err, sql.ErrNoRows) {
		response.Items = make([]memoryPostResponse, 0)
		return response, nil
	}
	if err != nil {
		return response, err
	}
	all, err := getAuthorPosts(ctx, db, userID)
	if err != nil {
		return response, err
	}
	type candidate struct {
		index    int
		postedAt int64
		id       string
	}
	var candidates []candidate
	var beforeNs int64
	if beforeAt != nil {
		beforeNs = beforeAt.UnixNano()
	}
	for i := range all.posts {
		w, ok := state.windowByID(all.windowID(i))
		if !ok || w.LocalDate.Before(start) || !w.LocalDate.Before(end) {
			continue
		}
		ns, id := all.posts[i].postedAt.ns, all.postID(i)
		if beforeAt != nil && (ns > beforeNs || (ns == beforeNs && id >= beforeID)) {
			continue
		}
		candidates = append(candidates, candidate{i, ns, id})
	}
	// 新しい順（postLess の逆順）
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.postedAt != b.postedAt {
			if a.postedAt > b.postedAt {
				return -1
			}
			return 1
		}
		return strings.Compare(b.id, a.id)
	})
	if len(candidates) > 51 {
		candidates = candidates[:51]
	}
	posts := make([]Post, len(candidates))
	for i, c := range candidates {
		posts[i] = all.post(c.index, userID)
	}
	items := make([]memoryPostResponse, 0, len(posts))
	for _, post := range posts {
		items = append(items, memoryPostResponse{
			createPostResponse: newPostResponse(post, author),
			RealMojis:          make([]replaceRealMojiResponse, 0),
			Comments:           make([]createCommentResponse, 0),
		})
	}
	response.HasMore = len(items) > 50
	if response.HasMore {
		items = items[:50]
		posts = posts[:50]
	}
	if err := loadMemoryInteractions(ctx, db, posts, items); err != nil {
		return response, err
	}
	response.Items = items
	return response, nil
}

// 投稿に付いた現在の RealMoji とコメント（ユーザー情報は描画時にキャッシュから引く）
type postInteractions struct {
	RealMojis []RealMoji
	Comments  []Comment
}

// RealMoji・コメントの作成・置換・削除のたびに無効化する（読み込み中に無効化された値は世代で弾く）
var interactionsCache = newGenCache[string, compactInteractions]()

func loadMemoryInteractions(ctx context.Context, db sqlx.QueryerContext, posts []Post, items []memoryPostResponse) error {
	if len(items) == 0 {
		return nil
	}
	interactions := make([]postInteractions, len(posts))
	var missing []string
	missingIndex := map[string]int{}
	gens := map[string][2]uint64{}
	for i, post := range posts {
		if cached, ok := interactionsCache.peek(post.ID); ok {
			interactions[i] = cached.expand()
			continue
		}
		missing = append(missing, post.ID)
		missingIndex[post.ID] = i
		g, e := interactionsCache.generation(post.ID)
		gens[post.ID] = [2]uint64{g, e}
	}
	if len(missing) > 0 {
		var reactions []RealMoji
		query, args, err := sqlx.In(`SELECT id, post_id, reactor_id, image_id, reaction_type, created_at, replaced_at, deleted_at FROM realmojis WHERE replaced_at IS NULL AND deleted_at IS NULL AND post_id IN (?) ORDER BY created_at DESC, id DESC`, missing)
		if err != nil {
			return err
		}
		if err := sqlx.SelectContext(ctx, db, &reactions, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
			return err
		}
		for _, r := range reactions {
			i := missingIndex[r.PostID]
			interactions[i].RealMojis = append(interactions[i].RealMojis, r)
		}
		var comments []Comment
		query, args, err = sqlx.In(`SELECT id, post_id, author_id, body, created_at, deleted_at FROM comments WHERE deleted_at IS NULL AND post_id IN (?) ORDER BY created_at, id`, missing)
		if err != nil {
			return err
		}
		if err := sqlx.SelectContext(ctx, db, &comments, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
			return err
		}
		for _, c := range comments {
			i := missingIndex[c.PostID]
			interactions[i].Comments = append(interactions[i].Comments, c)
		}
		for _, id := range missing {
			g := gens[id]
			interactionsCache.storeIfGeneration(id, newCompactInteractions(interactions[missingIndex[id]]), g[0], g[1])
		}
	}
	for i := range items {
		item := &items[i]
		for _, r := range interactions[i].RealMojis {
			if len(item.RealMojis) >= 50 {
				break
			}
			reactor, err := getUserCached(ctx, db, r.ReactorID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			item.RealMojis = append(item.RealMojis, newRealMojiResponse(r, reactor))
		}
		for _, c := range interactions[i].Comments {
			if len(item.Comments) >= 50 {
				break
			}
			author, err := getUserCached(ctx, db, c.AuthorID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			item.Comments = append(item.Comments, newCommentResponse(c, author))
		}
	}
	return nil
}
