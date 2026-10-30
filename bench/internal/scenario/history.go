package scenario

import (
	"context"
	"errors"
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
// 負荷ユーザーとは別に、しばらくぶりに戻ってきて過去を見返すユーザーがいる。初期データの
// 履歴のユーザー（負荷走行で書き込まない人）から無作為に 1 人選んでログインし、次のどれかを
// して、初期データと比べる。これを待ち時間なしで繰り返す。
//
//   - 履歴のある月を 1 か月分読む（Memories）。
//   - 履歴のある日を 1 日分読む。
//   - 全投稿をエクスポートする。
//   - 古い投稿の画像を直接開く（共有された古いリンクを開く）。
//
// 人も月も履歴全体から一様に選ぶので、最近のデータだけをメモリに置く実装では、古いデータの
// 読み取りが遅くなる。画像を消すか差し替える実装は、エクスポートと古いリンクで見つかる。
//
// このリクエストも打ち切りの判定（エラー率）には数えるが、成長の判定には数えない。
// エクスポートは大きく遅いので、どちらにも数えない（失敗すれば加点しないだけ）。

// returningRatio は、思い出を見返すユーザーを負荷ユーザー何人につき 1 人置くか。
const returningRatio = 8

// ensureReturning は region の思い出を見返すユーザーを、負荷ユーザー returningRatio 人に
// つき 1 人（切り上げ）になるまで増やす。
func (r *Runner) ensureReturning(ctx context.Context, wg *sync.WaitGroup, region string, users int) {
	if r.returningRatio <= 0 {
		return
	}
	// 初期データのユーザーの少ないリージョン（海外）では、候補の人数で頭打ちにする。
	want := min((users+r.returningRatio-1)/r.returningRatio, len(r.initial.HistoryUsers(region)))
	r.mu.Lock()
	// 国は通し番号で散らすので、すでにいる人数から数える。
	first := r.returningCount[region]
	add := want - first
	r.returningCount[region] = max(want, first)
	r.mu.Unlock()
	for i := range add {
		wg.Go(func() { r.runReturning(ctx, region, first+i) })
	}
}

// runReturning は思い出を見返すユーザーのクローズドループ。1 つの端末（接続）を、入れ替わる
// ユーザーが順に使う。
func (r *Runner) runReturning(ctx context.Context, region string, index int) {
	api := r.api.WithCountry(geo.Country(region, index)).WithOwnConnections().WithClientIP(r.ips.Next())
	defer api.CloseIdleConnections()
	slo := api.WithObserver(&accounting{r: r, slo: true})
	free := api.WithObserver(nil)
	candidates := r.initial.HistoryUsers(region)
	for ctx.Err() == nil {
		du := r.claimHistoryUser(candidates)
		if du == nil {
			// 候補がすべて使われている。人数に対して候補は十分多いので、ふつうは起きない。
			if sleepUntil(ctx, time.Now().Add(time.Second)) != nil {
				return
			}
			continue
		}
		a := &agent.Agent{Region: region, API: slo, ID: du.ID, Username: du.Username, SecretKey: du.SecretKey}
		u := &user{Agent: a, returning: true}
		r.revisit(ctx, u, free)
		r.releaseHistoryUser(du)
		if u.stopped.Load() {
			return
		}
	}
}

// claimHistoryUser は candidates から、ほかの思い出を見返すユーザーが使っていない人を
// 無作為に選んで取り置く。同じ人で同時に 2 回ログインすると、先のセッションが無効になる。
func (r *Runner) claimHistoryUser(candidates []*dataset.User) *dataset.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	for range 20 {
		du := candidates[r.historyRand.IntN(len(candidates))]
		if !r.revisiting[du.ID] {
			r.revisiting[du.ID] = true
			return du
		}
	}
	return nil
}

func (r *Runner) releaseHistoryUser(du *dataset.User) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.revisiting, du.ID)
}

// revisit は u としてログインし、過去の投稿を 1 つの方法で見返して初期データと比べる。
// free はエクスポートに使う、集計に数えないクライアント。
func (r *Runner) revisit(ctx context.Context, u *user, free *client.Client) {
	if err := u.Login(ctx); err != nil {
		switch {
		case ctx.Err() != nil:
		case r.bannedErr(u, err):
		case isStatus(err, http.StatusUnauthorized):
			r.board.Criticalf("初期データのユーザー %s としてログインできません: %v", u.Username, err)
		default:
			r.agentError(ctx, u, err, "%s: ログインできません: %v", u.Username, err)
		}
		return
	}
	r.mu.Lock()
	x := r.historyRand.Float64()
	months := r.initial.HistoryMonths()
	month := months[r.historyRand.IntN(len(months))]
	day := r.historyRand.IntN(r.dataset.Config.Days)
	r.mu.Unlock()
	thorough := r.sample() > 0
	switch {
	case x < 0.65:
		err := r.initial.CheckMemories(ctx, u.Agent, month, func(client.Post) bool { return false }, r.historyImages, thorough)
		r.recordHistory(ctx, u, err, score.TagHistoryMonth)
	case x < 0.85:
		err := r.initial.CheckMemoriesDate(ctx, u.Agent, day, r.historyImages, thorough)
		r.recordHistory(ctx, u, err, score.TagHistoryDate)
	case x < 0.93:
		exporter := *u.Agent
		exporter.API = free
		images := 0
		if thorough {
			images = r.historyImages
		}
		err := r.initial.CheckExport(ctx, &exporter, images, func(string) bool { return false })
		var apiErr *client.APIError
		var requestErr *client.RequestError
		if errors.As(err, &apiErr) || errors.As(err, &requestErr) {
			// エクスポートの失敗は加点しないだけにする（重い処理をあとに回す判断は自由）。
			return
		}
		r.recordHistory(ctx, u, err, score.TagExport)
	default:
		err := r.initial.SpotCheck(ctx, u.Agent, r.historyImages)
		r.recordHistory(ctx, u, err, score.TagSpotCheck)
	}
	if r.historyRandFloat() < 0.2 {
		if err := u.API.Logout(ctx, u.Token); err != nil {
			r.requestError(ctx, u, "%s: ログアウトできません: %v", u.Username, err)
		}
	}
}

// agentError は agent の登録とログインの失敗 err を記録する。応答は届いたが内容が違った
// （agent.ResponseError）なら、Observer は成功として数えているので検証の失敗として数える。
// ほかは requestError と同じ。
func (r *Runner) agentError(ctx context.Context, u *user, err error, format string, args ...any) {
	var invalid *agent.ResponseError
	if errors.As(err, &invalid) {
		r.invalid(ctx, u, format, args...)
		return
	}
	r.requestError(ctx, u, format, args...)
}

func (r *Runner) historyRandFloat() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.historyRand.Float64()
}

// readHistory は cohort のユーザーが、履歴のある月の Memories を読んで初期データと比べる。
// 負荷走行で開いた投稿期間の投稿は比べない（作成の結果がわからず、bench が ID を知らない
// 投稿も含む）。
func (r *Runner) readHistory(ctx context.Context, u *user) {
	months := r.initial.HistoryMonths()
	month := months[u.rng.IntN(len(months))]
	err := r.initial.CheckMemories(ctx, u.Agent, month, func(p client.Post) bool { return r.model.HasWindow(p.PostingWindowID) }, r.historyImages, r.sample() > 0)
	r.recordHistory(ctx, u, err, score.TagHistoryMonth)
}

// recordHistory は履歴の読み取りの結果を記録する。履歴は変わらないので、中身が違えば
// 正しさの違反とする。
func (r *Runner) recordHistory(ctx context.Context, u *user, err error, tag score.Tag) {
	var apiErr *client.APIError
	var requestErr *client.RequestError
	switch {
	case err == nil:
		r.award(u, r.dayNow(u.Region), tag)
	case ctx.Err() != nil:
	case r.bannedErr(u, err):
	case errors.As(err, &apiErr) || errors.As(err, &requestErr):
		r.requestError(ctx, u, "%s: 履歴を読めません: %v", u.Username, err)
	default:
		r.board.Criticalf("%s: 履歴が初期データと違います: %v", u.Username, err)
	}
}
