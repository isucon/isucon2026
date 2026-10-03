// Package score はゲート通過と通過後の活動を数える（P-115）。減点はない。
// 可用性は metrics の SLO で判定し、正しさの違反は Critical として走行を
// 失格にする。
package score

import (
	"fmt"
	"maps"
	"sync"
)

type Tag string

const (
	// TagGatePass は、1 つのウィンドウで投稿が受理され、そのあと解錠された
	// フィードを読めたユーザーを数える（P-115 §3.1）。
	TagGatePass Tag = "gate_pass"

	// 通過後の活動（P-115 §4.2）。
	TagFeed          Tag = "feed"
	TagImage         Tag = "image"
	TagRealMoji      Tag = "realmoji"
	TagComment       Tag = "comment"
	TagNotification  Tag = "notification"
	TagMemory        Tag = "memory"
	TagFriendRequest Tag = "friend_request"
	TagFriendAccept  Tag = "friend_accept"
	TagFollow        Tag = "follow"
)

type Weights map[Tag]int64

// DefaultWeights は較正までの暫定値（P-115 §9）。
func DefaultWeights() Weights {
	return Weights{
		TagGatePass:      100,
		TagFeed:          1,
		TagImage:         1,
		TagRealMoji:      5,
		TagComment:       3,
		TagNotification:  1,
		TagMemory:        1,
		TagFriendRequest: 2,
		TagFriendAccept:  2,
		TagFollow:        1,
	}
}

// MaxMessages は保持するメッセージ数の上限。件数は上限を超えても数え続ける。
const MaxMessages = 20

type Board struct {
	weights Weights

	mu            sync.Mutex
	counts        map[Tag]int64
	errors        []string
	errorCount    int64
	critical      []string
	criticalCount int64
}

// NewBoard は weights で採点するボードを作る。nil なら DefaultWeights を使う。
func NewBoard(weights Weights) *Board {
	if weights == nil {
		weights = DefaultWeights()
	}
	return &Board{weights: maps.Clone(weights), counts: make(map[Tag]int64)}
}

func (b *Board) Add(tag Tag) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.counts[tag]++
}

// Errorf はエラーを記録する。スコアは変えず、失敗した操作を運営と参加者に
// 説明するために使う。
func (b *Board) Errorf(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.errorCount++
	if len(b.errors) < MaxMessages {
		b.errors = append(b.errors, fmt.Sprintf(format, args...))
	}
}

// Criticalf は正しさの違反を記録する。1 件で走行は失格になる（P-115 §6.1）。
func (b *Board) Criticalf(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.criticalCount++
	if len(b.critical) < MaxMessages {
		b.critical = append(b.critical, fmt.Sprintf(format, args...))
	}
}

func (b *Board) Count(tag Tag) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.counts[tag]
}

func (b *Board) ErrorCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errorCount
}

func (b *Board) Errors() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.errors...)
}

func (b *Board) CriticalCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.criticalCount
}

func (b *Board) Critical() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.critical...)
}

// Total は件数の重み付き和。
func (b *Board) Total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total int64
	for tag, count := range b.counts {
		total += b.weights[tag] * count
	}
	return total
}

// Breakdown はタグごとの件数を返す（運営向け、D-005）。
func (b *Board) Breakdown() map[string]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int64, len(b.counts))
	for tag, count := range b.counts {
		out[string(tag)] = count
	}
	return out
}
