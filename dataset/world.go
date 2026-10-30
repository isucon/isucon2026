// Package dataset は ISUReal の初期データを決定的に作る。
//
// datagen はこれを seed ファイルに書き出し、bench は同じ設定から同じデータを
// 再計算して、既存ユーザーとしてのログインや履歴の検証に使う。同じ Config からは
// 行も ID も常に同じものができる。
//
// 関係（ユーザー、友達、フォロー、ブロック、投稿期間）は小さいので Generate で一度に作る。
// 投稿とそれに付く RealMoji、コメントは大きいので、World.PostOn で「ユーザー × 日」ごとに
// その場で作る。どの組もほかの組を作らずに作れるので、bench は履歴の一部だけを、全体を
// メモリに持たずに再計算できる。
package dataset

import "time"

// Config は生成するデータの規模と乱数の種。
type Config struct {
	Seed uint64
	// Users は管理者を除くユーザー数。
	Users int
	// Days は履歴の日数。全リージョンが毎日 1 回ずつ投稿期間を開く。
	Days int
	// EndDate は履歴の最終日。bench はこれより後の日付で投稿期間を開く。
	EndDate time.Time

	// 画像の一辺の画素数。画像は 1 枚ごとに中身が違う。
	PostImageSize     int
	RealMojiImageSize int
	ProfileImageSize  int
}

// Small は開発用の小さな規模。seed は数十 MB になる。
func Small() Config {
	return Config{
		Seed:              20261001,
		Users:             1000,
		Days:              30,
		EndDate:           time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
		PostImageSize:     64,
		RealMojiImageSize: 32,
		ProfileImageSize:  48,
	}
}

// Large は本番の規模の暫定値。履歴をアプリと DB のメモリより十分大きくし、すべてを
// メモリに載せる実装をできなくする。投稿約 63 万件、RealMoji 約 121 万件で、画像は
// 投稿が約 15 KB、RealMoji が約 3.5 KB（合わせて約 14 GB）。値は較正で決める。
//
// 履歴は競技当日（2026-10-31）の直前まで伸ばす。初期データの最後の作成時刻は最終日の翌日の
// 4 時（UTC）ごろで、初期化はそれより新しい行を走行の分として消すので、この時刻より前に
// ベンチを走らせてはいけない。webapp/sql/001-schema.sql は 10 月のパーティションを最終日の
// 翌日の 6 時（UTC）で分けていて、最終日を変えるときはそちらも合わせる。
func Large() Config {
	cfg := Small()
	cfg.Users = 5000
	cfg.Days = 365
	cfg.EndDate = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	cfg.PostImageSize = 320
	cfg.RealMojiImageSize = 128
	cfg.ProfileImageSize = 96
	return cfg
}

const (
	Asia    = "asia"
	Europe  = "europe"
	America = "america"
)

var Regions = []string{Asia, Europe, America}

// Role は bench が初期データのユーザーをどう扱ってよいか。
type Role string

const (
	// RoleHistory のユーザーは bench が読むだけで、書き込みはしない。
	RoleHistory Role = "history"
	// RoleCohort のユーザーは bench がログインして書き込んでよい。
	RoleCohort Role = "cohort"
)

const (
	AdministratorID       = "00000000-0000-0000-0000-000000000001"
	AdministratorUsername = "admin"
	AdministratorPassword = "password1234"
)

type ImageKind string

const (
	ImagePost     ImageKind = "post"
	ImageRealMoji ImageKind = "realmoji"
	ImageProfile  ImageKind = "profile"
)

// Image は image_objects の 1 行。中身は ID から World.ImageBytes で作る。
type Image struct {
	ID        string
	OwnerID   string
	Kind      ImageKind
	CreatedAt time.Time
}

type User struct {
	ID          string
	Username    string
	DisplayName string
	Bio         string
	// SecretKey はログインに使う平文のキー。DB には HashCredential(SecretKey) を入れる。
	SecretKey string
	Region    string
	// AccountType は regular か certified_public。
	AccountType string
	// Status は active か deleted。
	Status       string
	DeletedAt    *time.Time
	Role         Role
	ProfileImage *Image
	// Activity は各日に投稿する確率。
	Activity  float64
	CreatedAt time.Time
}

type FriendRequest struct {
	ID          string
	RequesterID string
	RecipientID string
	// Status は pending、accepted、rejected、cancelled のいずれか。
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Friendship struct {
	ID              string
	UserLowID       string
	UserHighID      string
	SourceRequestID string
	CreatedAt       time.Time
}

type Follow struct {
	ID         string
	FollowerID string
	FolloweeID string
	CreatedAt  time.Time
}

type Block struct {
	ID        string
	BlockerID string
	BlockedID string
	CreatedAt time.Time
}

type Window struct {
	ID             string
	Region         string
	LocalDate      time.Time
	StartsAt       time.Time
	OnTimeDeadline time.Time
	EndedAt        time.Time
}

type Location struct {
	// Kind は none、exact、approximate のいずれか。
	Kind      string
	Latitude  *float64
	Longitude *float64
	Locality  *string
}

type Post struct {
	ID       string
	AuthorID string
	WindowID string
	Image    *Image
	Caption  string
	// Visibility は public、friends、friends_of_friends のいずれか。
	Visibility string
	Location   Location
	PostedAt   time.Time
	DeletedAt  *time.Time
	// RealMojis と Comments はこの投稿に付いたもの。削除された投稿には付かない。
	RealMojis []*RealMoji
	Comments  []*Comment
}

type RealMoji struct {
	ID           string
	PostID       string
	ReactorID    string
	Image        *Image
	ReactionType string
	CreatedAt    time.Time
	// ReplacedAt が nil でない行は、同じ人の新しい RealMoji に置き換えられた古い行。
	ReplacedAt *time.Time
}

type Comment struct {
	ID        string
	PostID    string
	AuthorID  string
	Body      string
	CreatedAt time.Time
	DeletedAt *time.Time
}

type Notification struct {
	ID        string
	UserID    string
	WindowID  string
	CreatedAt time.Time
}

// World は関係の初期データ。スライスは DB へ入れる順（外部キーの依存順）に並ぶ。投稿と
// それに付くものは PostOn で作る。
type World struct {
	Config         Config
	Users          []*User
	FriendRequests []*FriendRequest
	Friendships    []*Friendship
	Follows        []*Follow
	Blocks         []*Block
	// Windows は日付の順に、各日の中では Regions の順に並ぶ。
	Windows []*Window

	userIndex map[string]int
	friends   []map[int]bool
	// friendList は friends を番号の順に並べたもの。乱数で選ぶときの順を決める。
	friendList [][]int
	blocked    map[[2]int]bool
}
