package model

import (
	"context"
	"database/sql"
	"encoding/base64"
	"time"

	"google.golang.org/protobuf/proto"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

type Notification struct {
	ID             int64
	ContestantID   int64
	Read           bool
	EncodedMessage string // base64(Notification の protobuf バイナリ)、CT-4
	CreatedAt      time.Time
}

// EncodeNotificationMessage は Notification#message= 相当（base64 標準エンコード）。
func EncodeNotificationMessage(m *pbresources.Notification) (string, error) {
	b, err := proto.Marshal(m)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// CreateNotification は Notification.create! 相当。
func CreateNotification(ctx context.Context, db DBTX, contestantID int64, m *pbresources.Notification) (*Notification, error) {
	encoded, err := EncodeNotificationMessage(m)
	if err != nil {
		return nil, err
	}
	n := &Notification{ContestantID: contestantID, EncodedMessage: encoded}
	err = db.QueryRowContext(ctx, `
		INSERT INTO notifications (contestant_id, "read", encoded_message, created_at, updated_at)
		VALUES ($1, false, $2, now(), now()) RETURNING id, created_at`, contestantID, encoded).Scan(&n.ID, &n.CreatedAt)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// InsertNotificationsBatch は Notification.insert_all! 相当（NF-4: 150 件バッチ）。
func InsertNotificationsBatch(ctx context.Context, db DBTX, contestantIDs []int64, encodedMessage string, now time.Time) error {
	const batchSize = 150
	for start := 0; start < len(contestantIDs); start += batchSize {
		end := min(start+batchSize, len(contestantIDs))
		// unnest で 1 文にまとめる（プレースホルダ数が件数に依存しない）
		ids := contestantIDs[start:end]
		query := `INSERT INTO notifications (contestant_id, "read", encoded_message, created_at, updated_at)
			SELECT id, false, $2, $3, $3 FROM unnest($1::bigint[]) AS id`
		if _, err := db.ExecContext(ctx, query, ids, encodedMessage, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func QueryNotifications(ctx context.Context, db DBTX, query string, args ...any) ([]*Notification, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Notification
	for rows.Next() {
		n := &Notification{}
		if err := rows.Scan(&n.ID, &n.ContestantID, &n.Read, &n.EncodedMessage, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

const notificationColumns = `notifications.id, notifications.contestant_id, notifications."read", notifications.encoded_message, notifications.created_at`

func ListNotifications(ctx context.Context, db DBTX, contestantID int64, after sql.NullInt64) ([]*Notification, error) {
	if after.Valid {
		return QueryNotifications(ctx, db,
			`SELECT `+notificationColumns+` FROM notifications WHERE contestant_id = $1 AND id > $2 ORDER BY id ASC`,
			contestantID, after.Int64)
	}
	return QueryNotifications(ctx, db,
		`SELECT `+notificationColumns+` FROM notifications WHERE contestant_id = $1 AND "read" = false ORDER BY id ASC`,
		contestantID)
}

// ToPB は Notification#to_pb 相当。デコードして id / created_at を刻む。
func (n *Notification) ToPB() (*pbresources.Notification, error) {
	raw, err := base64.StdEncoding.DecodeString(n.EncodedMessage)
	if err != nil {
		return nil, err
	}
	m := &pbresources.Notification{}
	if err := proto.Unmarshal(raw, m); err != nil {
		return nil, err
	}
	m.Id = n.ID
	m.CreatedAt = ts(n.CreatedAt)
	return m, nil
}
