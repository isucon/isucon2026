package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
)

type getFeedRequest struct {
	Feed           string     `param:"feed"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type getFeedResponse struct {
	Items   []createPostResponse `json:"items"`
	HasMore bool                 `json:"hasMore"`
}

func getFeedHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getFeedRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		switch input.Feed {
		case "friends", "friends-of-friends", "public", "following":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if (input.BeforePostedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		viewer, err := loadViewerContext(ctx, h.DB, GetSession(c).UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []createPostResponse{}})
		}
		if err != nil {
			return err
		}
		following := map[string]bool{}
		if input.Feed == "following" {
			following = state.followees(viewer.ID)
		}
		items := make([][]byte, 0, 51)
		// 当日未投稿の閲覧者は他人の投稿を見られない
		if !viewer.HasPostedToday {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []createPostResponse{}})
		}
		accept := func(post *Post) bool {
			if post.AuthorID == viewer.ID {
				return false
			}
			if input.BeforePostedAt != nil {
				if c := post.PostedAt.Compare(*input.BeforePostedAt); c > 0 || (c == 0 && post.ID >= input.BeforeID) {
					return false
				}
			}
			return viewer.canView(*post) && matchesFeed(viewer, following, *post, input.Feed)
		}
		appendItems := func(candidates []Post) error {
			for _, post := range candidates {
				author, err := getUserCached(ctx, h.DB, post.AuthorID)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				data, err := postJSON(post, author)
				if err != nil {
					return err
				}
				items = append(items, data)
				if len(items) == 51 {
					break
				}
			}
			return nil
		}
		if input.Feed == "public" {
			// 候補を新しい順に集め、著者が退会済みのものを除く。足りなければ候補を増やしてやり直す
			for limit := 64; ; limit *= 4 {
				candidates := make([]Post, 0, limit)
				state.eachActivePostDesc(func(post Post) bool {
					if accept(&post) {
						candidates = append(candidates, post)
					}
					return len(candidates) < limit
				})
				items = items[:0]
				if err := appendItems(candidates); err != nil {
					return err
				}
				if len(items) == 51 || len(candidates) < limit {
					break
				}
			}
		} else {
			// 友達・フォローなどのフィードは候補の著者の投稿だけを見る
			authors := following
			switch input.Feed {
			case "friends":
				authors = viewer.Friends
			case "friends-of-friends":
				state.fillFriendsOfFriends(&viewer)
				authors = viewer.FriendsOfFriends
			}
			if err := appendItems(state.activePostsByAuthors(authors, accept)); err != nil {
				return err
			}
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return writeFeedJSON(c, items, hasMore)
	}
}

func matchesFeed(viewer viewerContext, following map[string]bool, post Post, feed string) bool {
	switch feed {
	case "public":
		return post.Visibility == "public"
	case "following":
		return following[post.AuthorID]
	case "friends":
		return viewer.Friends[post.AuthorID]
	case "friends-of-friends":
		return !viewer.Friends[post.AuthorID] && viewer.isFriendOfFriend(post.AuthorID)
	default:
		return false
	}
}

// getFeedResponse と同じ JSON を投稿ごとのキャッシュから組み立てる
// 書き込みが終われば再利用できるので、組み立て用のバッファを使い回す
var feedBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 64<<10); return &b }}

func writeFeedJSON(c *echo.Context, items [][]byte, hasMore bool) error {
	bp := feedBufPool.Get().(*[]byte)
	buf := appendFeedJSON((*bp)[:0], items, hasMore)
	err := c.JSONBlob(http.StatusOK, buf)
	if cap(buf) <= 1<<20 {
		*bp = buf[:0]
		feedBufPool.Put(bp)
	}
	return err
}

func buildFeedJSON(items [][]byte, hasMore bool) []byte {
	return appendFeedJSON(nil, items, hasMore)
}

func appendFeedJSON(buf []byte, items [][]byte, hasMore bool) []byte {
	buf = append(buf, `{"items":[`...)
	for i, item := range items {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, item...)
	}
	if hasMore {
		buf = append(buf, `],"hasMore":true}`...)
	} else {
		buf = append(buf, `],"hasMore":false}`...)
	}
	return append(buf, '\n')
}
