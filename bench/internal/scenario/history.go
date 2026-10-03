package scenario

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/dataset"
)

// 思い出を見返すユーザー
//
// 負荷ユーザーとは別に、しばらくぶりに戻ってきて過去の Memories を見返すユーザーがいる。
// 初期データの履歴のユーザー（負荷走行で書き込まない人）から無作為に 1 人選んでログインし、
// 履歴のある月を 1 か月分読んで、画像を何枚か取得し、初期データと比べる。これを待ち時間なしで
// 繰り返す。人も月も履歴全体から一様に選ぶので、最近のデータだけをメモリに置く実装では、
// 古いデータの読み取りが遅くなる。
//
// このリクエストも負荷ユーザーと同じくリージョンの観測窓に数えるので、履歴の読み取りが
// 遅ければエラー率や p99 を通してラチェットの昇格を止める。

// ensureReturning は region の思い出を見返すユーザーを、負荷ユーザー returningRatio 人に
// つき 1 人（切り上げ）になるまで増やす。returningRatio が 0 なら置かない。
func (r *Runner) ensureReturning(ctx context.Context, wg *sync.WaitGroup, region string, users int) {
	if r.returningRatio <= 0 {
		return
	}
	// 初期データのユーザーの少ないリージョン（海外）では、候補の人数で頭打ちにする。
	want := min((users+r.returningRatio-1)/r.returningRatio, len(r.initial.HistoryUsers(region)))
	r.mu.Lock()
	add := want - r.returningCount[region]
	r.returningCount[region] = max(want, r.returningCount[region])
	r.mu.Unlock()
	for i := range add {
		wg.Go(func() { r.runReturning(ctx, region, i) })
	}
}

// runReturning は思い出を見返すユーザーのクローズドループ。1 つの端末（接続）を、入れ替わる
// ユーザーが順に使う。
func (r *Runner) runReturning(ctx context.Context, region string, index int) {
	api := r.api.WithCountry(geo.Country(region, index)).WithOwnConnections().WithObserver(r.recorder)
	defer api.CloseIdleConnections()
	candidates := r.initial.HistoryUsers(region)
	months := r.initial.HistoryMonths()
	for ctx.Err() == nil {
		user := r.claimHistoryUser(candidates)
		if user == nil {
			// 候補がすべて使われている。人数に対して候補は十分多いので、ふつうは起きない。
			if sleepUntil(ctx, time.Now().Add(time.Second)) != nil {
				return
			}
			continue
		}
		r.revisit(ctx, &agent.Agent{Region: region, API: api, ID: user.ID, Username: user.Username, SecretKey: user.SecretKey}, months[rand.IntN(len(months))])
		r.releaseHistoryUser(user)
	}
}

// claimHistoryUser は candidates から、ほかの思い出を見返すユーザーが使っていない人を
// 無作為に選んで取り置く。同じ人で同時に 2 回ログインすると、先のセッションが無効になる。
func (r *Runner) claimHistoryUser(candidates []*dataset.User) *dataset.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	for range 20 {
		user := candidates[rand.IntN(len(candidates))]
		if !r.revisiting[user.ID] {
			r.revisiting[user.ID] = true
			return user
		}
	}
	return nil
}

func (r *Runner) releaseHistoryUser(user *dataset.User) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.revisiting, user.ID)
}

// revisit は user としてログインし、month の Memories を初期データと比べる。
func (r *Runner) revisit(ctx context.Context, user *agent.Agent, month time.Time) {
	if err := user.Login(ctx); err != nil {
		switch {
		case ctx.Err() != nil:
		case isStatus(err, http.StatusUnauthorized):
			r.board.Criticalf("初期データのユーザー %s としてログインできません: %v", user.Username, err)
		default:
			r.requestError(ctx, "%s: ログインできません: %v", user.Username, err)
		}
		return
	}
	err := r.initial.CheckMemories(ctx, user, month, func(client.Post) bool { return false }, r.historyImages)
	r.recordHistory(ctx, user, err)
}

// recordHistory は履歴の読み取りの結果を記録する。履歴は変わらないので、中身が違えば
// 正しさの違反とする。
func (r *Runner) recordHistory(ctx context.Context, user *agent.Agent, err error) {
	var apiErr *client.APIError
	var requestErr *client.RequestError
	switch {
	case err == nil:
		r.board.Add(score.TagMemory)
	case ctx.Err() != nil:
	case errors.As(err, &apiErr) || errors.As(err, &requestErr):
		r.requestError(ctx, "%s: 履歴を読めません: %v", user.Username, err)
	default:
		r.board.Criticalf("%s: 履歴が初期データと違います: %v", user.Username, err)
	}
}
