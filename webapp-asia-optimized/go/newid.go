package isureal

import "github.com/google/uuid"

// 新しく作る行の ID。時刻順に並ぶ UUID v7 にして、B-tree への挿入をインデックスの末尾に集める
func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}
