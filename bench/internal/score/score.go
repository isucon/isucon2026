// Package score は利用者の行動（ゲート通過、閲覧、交流、関係、アカウントの手入れ、
// 思い出）を数え、重み付きの和に地域の倍率を掛けてスコアにする。減点はない。可用性は
// metrics の SLO で判定し、正しさの違反は Critical として走行を失格にする。
package score

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/isucon/isucon2026/bench/internal/geo"
)

type Tag string

const (
	// TagGatePass は、1 つの投稿期間で投稿が受理され、そのあと友達のフィードを読めた
	// ユーザーを数える。TagGrandPrix は最終日（全リージョン同時のグランプリ）の分。
	TagGatePass  Tag = "gate_pass"
	TagGrandPrix Tag = "grand_prix"

	// 閲覧。
	TagFeed         Tag = "feed"
	TagImage        Tag = "image"
	TagPostDetail   Tag = "post_detail"
	TagReactionList Tag = "reaction_list"
	TagProfileView  Tag = "profile_view"
	TagNotification Tag = "notification"
	TagMemory       Tag = "memory"

	// 交流。
	TagRealMoji       Tag = "realmoji"
	TagComment        Tag = "comment"
	TagDeleteReaction Tag = "delete_reaction"

	// 友達、フォロー、ブロック。
	TagSearch        Tag = "search"
	TagFriendRequest Tag = "friend_request"
	TagFriendAccept  Tag = "friend_accept"
	TagFriendDecline Tag = "friend_decline"
	TagUnfriend      Tag = "unfriend"
	TagRelationships Tag = "relationships"
	TagFollow        Tag = "follow"
	TagUnfollow      Tag = "unfollow"
	TagBlock         Tag = "block"
	TagUnblock       Tag = "unblock"

	// アカウントの手入れ。
	TagProfileUpdate Tag = "profile_update"
	TagProfileImage  Tag = "profile_image"
	TagCaption       Tag = "caption"
	TagDeleteAccount Tag = "delete_account"

	// 思い出を見返す。
	TagHistoryMonth Tag = "history_month"
	TagHistoryDate  Tag = "history_date"
	TagExport       Tag = "export"
	TagSpotCheck    Tag = "spot_check"
)

type Weights map[Tag]int64

// DefaultWeights は較正までの暫定値。重みはアプリ側の処理の重さにおおよそ比例させる。
// ゲート通過は翌日の新しいユーザー（成長）にも効くので、直接の重みは大きくしない。
// グランプリの日の通過は 2 倍にする。
func DefaultWeights() Weights {
	return Weights{
		TagGatePass:       10,
		TagGrandPrix:      20,
		TagFeed:           1,
		TagImage:          2,
		TagPostDetail:     1,
		TagReactionList:   1,
		TagProfileView:    1,
		TagNotification:   1,
		TagMemory:         1,
		TagRealMoji:       5,
		TagComment:        3,
		TagDeleteReaction: 1,
		TagSearch:         2,
		TagFriendRequest:  2,
		TagFriendAccept:   2,
		TagFriendDecline:  1,
		TagUnfriend:       1,
		TagRelationships:  1,
		TagFollow:         1,
		TagUnfollow:       1,
		TagBlock:          1,
		TagUnblock:        1,
		TagProfileUpdate:  1,
		TagProfileImage:   3,
		TagCaption:        1,
		TagDeleteAccount:  5,
		TagHistoryMonth:   3,
		TagHistoryDate:    2,
		TagExport:         20,
		TagSpotCheck:      2,
	}
}

// regionHalves は地域の倍率を 2 倍した値（ここにない地域は 2、つまり 1 倍）。海外（europe と
// america）は 1.5 倍にする。海外のサーバーは asia の DB から遠く、同じ人数でも点を取りにくい
// （webapp-optimized v3 で人数の 47%、点の 35%）ので、海外で応答する工夫に見合う点にする。
// 半分を単位にして整数で足し、端数は最後に 1 回だけ切り捨てる。
var regionHalves = map[string]int64{geo.Europe: 3, geo.America: 3}

func halves(region string) int64 {
	if h, ok := regionHalves[region]; ok {
		return h
	}
	return 2
}

// MaxMessages は保持するメッセージ数の上限。件数は上限を超えても数え続ける。
const MaxMessages = 20

// Cell はリージョンと日と要素ごとの件数と得点（運営向け）。Day はリージョンの日
// （1 始まり）で、日が始まる前の操作は 0。Points は地域の倍率を掛けて切り捨てた値なので、
// 足すと Total より少しだけ小さくなることがある。
type Cell struct {
	Region string `json:"region"`
	Day    int    `json:"day"`
	Tag    Tag    `json:"tag"`
	Count  int64  `json:"count"`
	Points int64  `json:"points"`
}

type cellKey struct {
	region string
	day    int
	tag    Tag
}

type Board struct {
	weights Weights

	mu            sync.Mutex
	counts        map[Tag]int64
	cells         map[cellKey]int64
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
	return &Board{weights: maps.Clone(weights), counts: make(map[Tag]int64), cells: make(map[cellKey]int64)}
}

// Add は region の day 日目の操作 tag を 1 件数える。
func (b *Board) Add(tag Tag, region string, day int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.counts[tag]++
	b.cells[cellKey{region: region, day: day, tag: tag}]++
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

// Total は件数の重み付き和に地域の倍率を掛けたもの。
func (b *Board) Total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total int64
	for key, count := range b.cells {
		total += b.weights[key.tag] * count * halves(key.region)
	}
	return total / 2
}

// Cells はリージョンと日と要素ごとの件数と得点を、日、リージョン、要素の順に並べて返す
// （運営向け）。
func (b *Board) Cells() []Cell {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Cell, 0, len(b.cells))
	for key, count := range b.cells {
		out = append(out, Cell{Region: key.region, Day: key.day, Tag: key.tag, Count: count, Points: b.weights[key.tag] * count * halves(key.region) / 2})
	}
	slices.SortFunc(out, func(a, c Cell) int {
		return cmp.Or(cmp.Compare(a.Day, c.Day), cmp.Compare(a.Region, c.Region), cmp.Compare(a.Tag, c.Tag))
	})
	return out
}

// DayPoints は region の day 日目の得点（地域の倍率を掛けたもの）。
func (b *Board) DayPoints(region string, day int) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total int64
	for key, count := range b.cells {
		if key.region == region && key.day == day {
			total += b.weights[key.tag] * count
		}
	}
	return total * halves(region) / 2
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
