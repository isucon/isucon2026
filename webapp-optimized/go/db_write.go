package isureal

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

type storedUser struct {
	User
	StoredSecret  string  `json:"secretKeyHash"`
	StoredProfile *string `json:"profileImageId"`
}

type writeResult struct {
	User     User          `json:"-"`
	UserData *storedUser   `json:"user"`
	Post     Post          `json:"post"`
	Comment  Comment       `json:"comment"`
	Reaction RealMoji      `json:"reaction"`
	Request  FriendRequest `json:"request"`
}

// One statement is also the transaction boundary. Never retry mutations here:
// an ambiguous transport failure must not duplicate a committed operation.
func runWrite(ctx context.Context, db *sqlx.DB, action, actor string, args any, image []byte) (writeResult, error) {
	var result writeResult
	payload, err := json.Marshal(args)
	if err != nil {
		return result, err
	}
	// The version is drawn after optimized_write, in the same transaction, while
	// its row locks are held (see hotState).
	var row struct {
		Data    []byte `db:"r"`
		Version int64  `db:"v"`
	}
	err = db.GetContext(ctx, &row, `SELECT r, nextval('change_seq') AS v FROM (SELECT optimized_write($1,$2,$3::jsonb,$4::bytea) AS r OFFSET 0) s`, action, actor, string(payload), image)
	data := row.Data
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "PZ001" {
			if status, parseErr := strconv.Atoi(pgErr.Detail); parseErr == nil && status >= 400 && status < 500 {
				return result, NewAPIError(status, pgErr.Message)
			}
		}
		return result, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	if err := result.prepareUser(); err != nil {
		return result, err
	}
	events := eventsFor(ctx, db, action, actor, args, result, row.Version)
	if homeRegion != "" && sessionRegion(ctx, action, actor, result) == homeRegion {
		// Only this region trusts the session from the hot state.
		hot.apply(events)
		go sendToPeers(context.WithoutCancel(ctx), "/internal/state-events", events)
		return result, nil
	}
	broadcast(ctx, events...)
	return result, nil
}

func sessionRegion(ctx context.Context, action, actor string, result writeResult) string {
	switch action {
	case "login":
		return result.User.Region
	case "logout":
		if u, ok := hot.userState(realActor(ctx, actor)); ok {
			return u.Region
		}
	}
	return ""
}

func (result *writeResult) prepareUser() error {
	if result.UserData != nil {
		result.User = result.UserData.User
		result.User.SecretKeyHash = result.UserData.StoredSecret
		result.User.ProfileImageID = result.UserData.StoredProfile
		return result.User.setImageKey()
	}
	return nil
}
