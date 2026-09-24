package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type getRelationshipsRequest struct {
	Kind            string     `param:"kind"`
	BeforeUpdatedAt *time.Time `query:"beforeUpdatedAt"`
	BeforeID        string     `query:"beforeId"`
}

type relationshipUserResponse struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"displayName"`
	ProfileImageURL *string `json:"profileImageUrl"`
	Bio             string  `json:"bio"`
	AccountType     string  `json:"accountType"`
}

type relationshipResponse struct {
	RelationID string                   `json:"relationId"`
	User       relationshipUserResponse `json:"user"`
	Status     string                   `json:"status"`
	UpdatedAt  time.Time                `json:"updatedAt"`
}

type getRelationshipsResponse struct {
	Items   []relationshipResponse `json:"items"`
	HasMore bool                   `json:"hasMore"`
}

func getRelationshipsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getRelationshipsRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if (input.BeforeUpdatedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		var query string
		switch input.Kind {
		case "friends":
			query = `SELECT id, user_low_id AS first_id, user_high_id AS second_id, updated_at FROM friendships WHERE user_low_id=$1 OR user_high_id=$1`
		case "following":
			query = `SELECT id, follower_id AS first_id, followee_id AS second_id, updated_at FROM follows WHERE follower_id=$1`
		case "followers":
			query = `SELECT id, follower_id AS first_id, followee_id AS second_id, updated_at FROM follows WHERE followee_id=$1`
		case "incoming-requests":
			query = `SELECT id, requester_id AS first_id, recipient_id AS second_id, updated_at FROM friend_requests WHERE recipient_id=$1 AND status='pending'`
		case "outgoing-requests":
			query = `SELECT id, requester_id AS first_id, recipient_id AS second_id, updated_at FROM friend_requests WHERE requester_id=$1 AND status='pending'`
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_relationship")
		}
		ctx := c.Request().Context()
		actor := GetSession(c).UserID
		var records []struct {
			ID        string    `db:"id"`
			FirstID   string    `db:"first_id"`
			SecondID  string    `db:"second_id"`
			UpdatedAt time.Time `db:"updated_at"`
		}
		if err := h.DB.SelectContext(ctx, &records, `SELECT * FROM (`+query+`) relations WHERE ($2::timestamptz IS NULL OR (updated_at, id)<($2,$3)) ORDER BY updated_at DESC, id DESC`, actor, input.BeforeUpdatedAt, input.BeforeID); err != nil {
			return err
		}
		items := make([]relationshipResponse, 0)
		for _, record := range records {
			target := record.FirstID
			if target == actor {
				target = record.SecondID
			}
			user, err := getUser(ctx, h.DB, target)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if user.AccountType != "certified_public" && input.Kind != "friends" {
				user.Bio = ""
			}
			status := input.Kind
			if input.Kind == "incoming-requests" || input.Kind == "outgoing-requests" {
				status = "pending"
			}
			items = append(items, relationshipResponse{
				RelationID: record.ID, Status: status, UpdatedAt: record.UpdatedAt,
				User: newRelationshipUserResponse(user),
			})
			if len(items) == 51 {
				break
			}
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, getRelationshipsResponse{Items: items, HasMore: hasMore})
	}
}
