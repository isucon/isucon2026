package isureal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
)

type FriendRequest struct {
	ID          string    `db:"id"`
	RequesterID string    `db:"requester_id"`
	RecipientID string    `db:"recipient_id"`
	Status      string    `db:"status"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func getFriendRequest(ctx context.Context, db sqlx.QueryerContext, id string) (FriendRequest, error) {
	var request FriendRequest
	err := sqlx.GetContext(ctx, db, &request, `SELECT id, requester_id, recipient_id, status, created_at, updated_at FROM friend_requests WHERE id=$1`, id)
	return request, err
}

// ユーザーのロックを保持したトランザクション。Rollback でロックも解放する
type relationshipTx struct {
	*sqlx.Tx
	unlock func()
}

func (t *relationshipTx) Rollback() error {
	err := t.Tx.Rollback()
	t.unlock()
	return err
}

func beginRelationshipTx(ctx context.Context, db *sqlx.DB, actor, target string) (*relationshipTx, error) {
	return beginRelationshipTxWithTargetLock(ctx, db, actor, target, true)
}

// 相手側のユーザーを共有ロックにすると、同じ相手へのリアクションが並行に実行できる
func beginRelationshipTxWithTargetLock(ctx context.Context, db *sqlx.DB, actor, target string, targetExclusive bool) (*relationshipTx, error) {
	if actor == target || target == "" {
		return nil, NewAPIError(http.StatusBadRequest, "invalid_relationship")
	}
	// 関係の変更は 2 人の組の中で直列化すれば十分（人気ユーザーへの申請が集中しても詰まらないように）
	locks := []userLock{{ID: pairKey(actor, target), Exclusive: true}}
	if !targetExclusive {
		// リアクションは共有ロック（関係の変更・投稿削除とだけ排他）
		locks = []userLock{{ID: userKey(actor)}, {ID: userKey(target)}, {ID: pairKey(actor, target)}}
	}
	unlock := lockUsers(locks...)
	for _, id := range []string{actor, target} {
		user, err := getUserAnyStatusCached(ctx, db, id)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && user.Status != "active") {
			unlock()
			return nil, NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		if err != nil {
			unlock()
			return nil, err
		}
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		unlock()
		return nil, err
	}
	return &relationshipTx{Tx: tx, unlock: unlock}, nil
}

func isBlocked(ctx context.Context, db sqlx.QueryerContext, actor, target string) (bool, error) {
	var blocked bool
	err := sqlx.GetContext(ctx, db, &blocked, `SELECT EXISTS(SELECT 1 FROM block_relations WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1))`, actor, target)
	return blocked, err
}

func insertFriendship(ctx context.Context, tx *sqlx.Tx, request FriendRequest) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO friendships (id, user_low_id, user_high_id, source_request_id) VALUES ($1,$2,$3,$4)`, newID(), min(request.RequesterID, request.RecipientID), max(request.RequesterID, request.RecipientID), request.ID)
	if err == nil {
		state.addFriendship(request.RequesterID, request.RecipientID)
	}
	return err
}

func requireRelationshipChange(result sql.Result, err error, status int, code string) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return NewAPIError(status, code)
	}
	return nil
}
