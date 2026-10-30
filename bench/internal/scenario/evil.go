package scenario

import (
	"context"
	"log/slog"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/dataset"
)

// EvilFunc は不正な利用者を動かす。負荷走行の時計が動き始めたら呼ばれ、ctx が終わったら
// 戻る。不正な利用者のリクエストは得点にも、打ち切りや成長の判定にも数えない。
type EvilFunc func(ctx context.Context, env EvilEnv)

// EvilEnv は不正な利用者に渡す走行の情報。
type EvilEnv struct {
	// API はルーターへのクライアント。国（WithCountry）と IP（WithClientIP）は付いていない。
	API *client.Client
	// Dataset は初期データ（ユーザー名などを見られる）。
	Dataset *dataset.World
	// NextIP は正常なユーザーと同じ割り当てから、使っていない IP アドレスを返す。
	NextIP func() string
	// Seed は走行の乱数の種。同じ種なら同じ登場時刻・ログイン頻度で動く。
	Seed uint64
	// Start は負荷走行の時計が動き始めた時刻、Length は走行の長さ。
	Start  time.Time
	Length time.Duration
	// Users は今いる正常な負荷ユーザー（ID とユーザー名とリージョン）。
	Users func() []EvilTarget
	// RegisterUser は不正な利用者が作ったユーザーを知らせる。正常なユーザーは、このユーザー
	// からの友達申請などを bench の知らないユーザーとして咎めない。
	RegisterUser func(id string)
	// Logger は運営向けのロガー。不正な利用者の通信や応答の内訳は bench 側の攻撃が
	// 狙いどおり動いたかを確認するための記録で、参加者には見せない。
	Logger *slog.Logger
}

// EvilTarget は正常な負荷ユーザー。
type EvilTarget struct {
	ID       string
	Username string
	Region   string
}

func (r *Runner) evilEnv(start time.Time) EvilEnv {
	return EvilEnv{
		API:     r.api,
		Dataset: r.dataset,
		NextIP:  r.ips.Next,
		Seed:    r.seed,
		Start:   start,
		Length:  r.cfg.Duration,
		Users: func() []EvilTarget {
			var out []EvilTarget
			byRegion := r.usersByRegion()
			for _, region := range geo.Regions {
				for _, u := range byRegion[region] {
					if !u.stopped.Load() {
						out = append(out, EvilTarget{ID: u.ID, Username: u.Username, Region: region})
					}
				}
			}
			return out
		},
		RegisterUser: func(id string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.evilUsers[id] = true
		},
		Logger: r.operatorLogger(),
	}
}

func (r *Runner) isEvilUser(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.evilUsers[id]
}
