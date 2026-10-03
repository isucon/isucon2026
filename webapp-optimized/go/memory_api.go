package isureal

import (
	"context"
	"encoding/json"
	"net/http"
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
		response, err := listMemories(c.Request().Context(), h.ReadDB, GetSession(c).UserID, start, start.AddDate(0, 1, 0), input.BeforePostedAt, input.BeforeID)
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
		response, err := listMemories(c.Request().Context(), h.ReadDB, GetSession(c).UserID, start, start.AddDate(0, 0, 1), input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, getMemoriesByDateResponse(response))
	}
}

func listMemories(ctx context.Context, db sqlx.QueryerContext, userID string, start, end time.Time, beforeAt *time.Time, beforeID string) (getMemoriesResponse, error) {
	var response getMemoriesResponse
	if (beforeAt == nil) != (beforeID == "") {
		return response, NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	var records []struct {
		Item      []byte `db:"item"`
		Comments  []byte `db:"comments"`
		Reactions []byte `db:"reactions"`
	}
	if err := sqlx.SelectContext(ctx, db, &records, memoriesSQL, userID, start, end, beforeAt, beforeID); err != nil {
		return response, err
	}
	response.HasMore = len(records) > 50
	if response.HasMore {
		records = records[:50]
	}
	response.Items = make([]memoryPostResponse, 0, len(records))
	for _, record := range records {
		var post writeResult
		if err := json.Unmarshal(record.Item, &post); err != nil {
			return response, err
		}
		if err := post.prepareUser(); err != nil {
			return response, err
		}
		item := memoryPostResponse{createPostResponse: newPostResponse(post.Post, post.User),
			Comments: make([]createCommentResponse, 0), RealMojis: make([]replaceRealMojiResponse, 0)}
		comments, err := decodeRecords(record.Comments)
		if err != nil {
			return response, err
		}
		for _, comment := range comments {
			item.Comments = append(item.Comments, newCommentResponse(comment.Comment, comment.User))
		}
		reactions, err := decodeRecords(record.Reactions)
		if err != nil {
			return response, err
		}
		for _, reaction := range reactions {
			item.RealMojis = append(item.RealMojis, newRealMojiResponse(reaction.Reaction, reaction.User))
		}
		response.Items = append(response.Items, item)
	}
	return response, nil
}
