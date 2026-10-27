package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type searchUsersRequest struct {
	Q              string `query:"q"`
	BeforeUsername string `query:"beforeUsername"`
	BeforeID       string `query:"beforeId"`
}

type searchUsersResponse struct {
	Items   []relationshipUserResponse `json:"items"`
	HasMore bool                       `json:"hasMore"`
}

func normalizeSearchText(value string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
}

func searchUsersHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input searchUsersRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if !validUserText(input.Q, 1, 100) {
			return NewAPIError(http.StatusBadRequest, "invalid_relationship")
		}
		if (input.BeforeUsername == "") != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		viewer := GetSession(c).UserID
		rows, err := h.DB.QueryxContext(ctx, `SELECT `+userProjection+` FROM users
          WHERE status='active' AND id<>$1 AND ($2='' OR (username,id)>($2,$3))
            AND NOT EXISTS (SELECT 1 FROM block_relations WHERE blocker_id=$1 AND blocked_id=users.id)
            AND NOT EXISTS (SELECT 1 FROM block_relations WHERE blocked_id=$1 AND blocker_id=users.id)
          ORDER BY username, id`, viewer, input.BeforeUsername, input.BeforeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		query := normalizeSearchText(input.Q)
		items := make([]relationshipUserResponse, 0)
		for rows.Next() {
			var user User
			if err := rows.StructScan(&user); err != nil {
				return err
			}
			if !strings.Contains(normalizeSearchText(user.Username), query) && !strings.Contains(normalizeSearchText(user.DisplayName), query) {
				continue
			}
			if err := user.setImageKey(); err != nil {
				return err
			}
			if user.AccountType != "certified_public" {
				user.Bio = ""
			}
			items = append(items, newRelationshipUserResponse(user))
			if len(items) == 51 {
				break
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		hasMore := len(items) > 50
		if hasMore {
			items = items[:50]
		}
		return c.JSON(http.StatusOK, searchUsersResponse{Items: items, HasMore: hasMore})
	}
}

type getUserRequest struct {
	UserID string `param:"userId"`
}

type getUserResponse relationshipUserResponse

func getUserHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getUserRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		if viewer, ok := hotSessionUser(c, h); ok {
			if user, friend, visible, ok := hot.viewUser(viewer.ID, input.UserID); ok {
				if !visible {
					return NewAPIError(http.StatusNotFound, "relationship_not_found")
				}
				if user.AccountType != "certified_public" && !friend {
					user.Bio = ""
				}
				return c.JSON(http.StatusOK, getUserResponse(newRelationshipUserResponse(user)))
			}
		}
		viewer := GetSession(c).UserID
		var state struct {
			User
			IsBlocked bool `db:"is_blocked"`
			Friend    bool `db:"friend"`
		}
		err := h.DB.GetContext(ctx, &state, `SELECT `+userProjection+`,
          EXISTS(SELECT 1 FROM block_relations
            WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1)) AS is_blocked,
          EXISTS(SELECT 1 FROM friendships
            WHERE (user_low_id=$1 AND user_high_id=$2) OR (user_low_id=$2 AND user_high_id=$1)) AS friend
          FROM users WHERE id=$2 AND status='active'`, viewer, input.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		if err != nil {
			return err
		}
		user := state.User
		if err := user.setImageKey(); err != nil {
			return err
		}
		if state.IsBlocked {
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		if user.AccountType != "certified_public" && !state.Friend {
			user.Bio = ""
		}

		return c.JSON(http.StatusOK, getUserResponse(newRelationshipUserResponse(user)))
	}
}

func newRelationshipUserResponse(user User) relationshipUserResponse {
	return relationshipUserResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, ProfileImageURL: newGetMeResponse(user).ProfileImageURL, Bio: user.Bio, AccountType: user.AccountType}
}
