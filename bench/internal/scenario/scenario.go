package scenario

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/imagegen"
	"github.com/isucon/isucon2026/bench/internal/initialdata"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/dataset"
)

const (
	initializeTimeout = 30 * time.Second
	loadDuration      = 60 * time.Second
	sloCheckInterval  = time.Second
	maxMessages       = 20
)

// schedule は投稿期間の時間割。負荷走行の開始から warmup 後（ユーザーが通知を購読し
// 終えるのを待つ）に始め、リージョンごとに period ごとに開き、リージョンどうしは offset
// ずつずらす（P-102 の 20 秒周期、7 秒ずらし）。
type schedule struct {
	warmup time.Duration
	period time.Duration
	offset time.Duration
}

var defaultSchedule = schedule{warmup: time.Second, period: 10 * time.Second, offset: 3 * time.Second}

type Config struct {
	Target string
	Host   string
	// Users はリージョンあたりの負荷ユーザー数。
	Users int
	// Duration は負荷走行の長さ。0 なら 60 秒。
	Duration time.Duration
	Weights  score.Weights
	SLO      metrics.SLO
	Retry    client.RetryPolicy
	// Staleness は書き込みが読み取りに反映されるまでに許す遅れ。
	Staleness time.Duration
	// Ratchet はレベルの昇格の設定。ゼロ値なら DefaultRatchet。
	Ratchet RatchetConfig
	// Dataset は対象の DB に入っている初期データ。nil なら dataset.Small() から作る。
	Dataset *dataset.World
	Logger  *slog.Logger
	// OnProgress は途中経過（暫定のスコアと内訳）を受け取る。フェーズの境目と、負荷走行の
	// 観測窓が閉じるたびに呼ぶ。スーパーバイザーへの報告に使う。
	OnProgress func(Result)
}

type Result struct {
	Pass     bool   `json:"pass"`
	Score    int64  `json:"score"`
	Language string `json:"language"`
	Reason   string `json:"reason,omitempty"`
	// Messages は参加者に見せる。失格の理由のあとに、正しさの違反とエラーを並べる。
	Messages []string `json:"messages"`

	// 以下は運営向け（D-005）。
	Breakdown map[string]int64 `json:"breakdown"`
	// OpenedWindows はリージョンごとに開いた投稿期間の数。
	OpenedWindows map[string]int `json:"openedWindows"`
	// Levels はリージョンごとに到達したレベル、Users は最後の負荷ユーザーの数、Buzzes は
	// リージョンが話題になった回数。
	Levels   map[string]int  `json:"levels"`
	Users    map[string]int  `json:"users"`
	Buzzes   map[string]int  `json:"buzzes"`
	Critical []string        `json:"critical"`
	Errors   []string        `json:"errors"`
	Windows  []metrics.Stats `json:"windows"`
}

type Runner struct {
	cfg      Config
	api      *client.Client
	admin    *client.Client
	board    *score.Board
	recorder *metrics.Recorder
	initial  *initialdata.Checker
	dataset  *dataset.World
	// model は bench が知っているアプリの状態。フィードの検証に使う。
	model *model.Model

	adminToken string
	agents     map[string][]*agent.Agent
	postImage  []byte
	mojiImage  []byte
	language   string
	firstDate  time.Time
	// deleteRate と changeRate は、投稿後に投稿を削除する確率と公開範囲を変える確率。
	deleteRate float64
	changeRate float64
	// visibility は投稿の公開範囲を選ぶ。
	visibility func() string
	schedule   schedule
	// acceptRate は閲覧の合間に届いた友達申請を確認する確率。
	acceptRate    float64
	ratchetConfig RatchetConfig
	ratchet       *ratchet

	mu          sync.Mutex
	opened      map[string]int
	friendCount map[string]int
	newcomers   int
	// cohort は負荷ユーザーのうち初期データのユーザー。準備のあとは読み取り専用。
	cohort map[string]bool
	// historyRate は cohort のユーザーが閲覧の合間に履歴を読む確率。
	historyRate float64
	// historyImages は履歴を 1 か月読むたびに中身を取得して比べる画像の数。
	historyImages int
	// returningRatio は、思い出を見返すユーザーを負荷ユーザー何人につき 1 人置くか。
	returningRatio int
	returningCount map[string]int
	// revisiting は思い出を見返すユーザーが今ログインしている初期データのユーザー。
	revisiting map[string]bool
	// loadStart は負荷走行の開始時刻、join は流入したユーザーを登録させる（負荷走行の間だけ）。
	loadStart time.Time
	join      func(region string, level, users int, buzz bool)
}

func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Users < 1 || cfg.Duration < 0 {
		return nil, fmt.Errorf("users must be positive and duration must be nonnegative")
	}
	if cfg.Duration == 0 {
		cfg.Duration = loadDuration
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.SLO == (metrics.SLO{}) {
		cfg.SLO = metrics.DefaultSLO
	}
	if cfg.Retry == (client.RetryPolicy{}) {
		cfg.Retry = client.DefaultRetryPolicy
	}
	if cfg.Ratchet == (RatchetConfig{}) {
		cfg.Ratchet = DefaultRatchet
	}
	postImage, err := imagegen.JPEG(64, 64, 1)
	if err != nil {
		return nil, err
	}
	mojiImage, err := imagegen.JPEG(32, 32, 2)
	if err != nil {
		return nil, err
	}
	if cfg.Dataset == nil {
		cfg.Dataset = dataset.Generate(dataset.Small())
	}
	m := model.New()
	m.Staleness = cfg.Staleness
	m.LoadDataset(cfg.Dataset)
	api := client.NewClient(client.Config{Base: cfg.Target, Host: cfg.Host, Retry: cfg.Retry})
	return &Runner{
		cfg:            cfg,
		api:            api,
		admin:          api.WithCountry(geo.Country(geo.Asia, 0)),
		board:          score.NewBoard(cfg.Weights),
		initial:        initialdata.New(cfg.Dataset),
		dataset:        cfg.Dataset,
		model:          m,
		agents:         make(map[string][]*agent.Agent),
		postImage:      postImage,
		mojiImage:      mojiImage,
		deleteRate:     0.05,
		changeRate:     0.1,
		visibility:     func() string { return visibilities[rand.IntN(len(visibilities))] },
		schedule:       defaultSchedule,
		acceptRate:     0.1,
		ratchetConfig:  cfg.Ratchet,
		ratchet:        newRatchet(),
		opened:         make(map[string]int),
		friendCount:    make(map[string]int),
		cohort:         make(map[string]bool),
		historyRate:    0.02,
		historyImages:  4,
		returningRatio: 8,
		returningCount: make(map[string]int),
		revisiting:     make(map[string]bool),
	}, nil
}

func (r *Runner) Run(ctx context.Context) Result {
	defer r.closeConnections()
	reason := ""
	for _, phase := range []func(context.Context) string{r.initialize, r.pretest, r.checkInitialData, r.prepareUsers, r.load} {
		r.progress()
		if reason = phase(ctx); reason != "" {
			break
		}
	}
	r.progress()
	if reason == "" && r.board.CriticalCount() == 0 {
		reason = r.finalCheck(ctx)
	}
	if ctx.Err() != nil {
		reason = "ベンチマークが中断されました: " + ctx.Err().Error()
	}
	if reason == "" && r.board.CriticalCount() > 0 {
		reason = "整合性の違反: " + r.board.Critical()[0]
	}
	result := Result{
		Pass:          reason == "",
		Language:      r.language,
		Reason:        reason,
		Breakdown:     r.board.Breakdown(),
		OpenedWindows: r.openedCounts(),
		Levels:        r.ratchet.levels(),
		Users:         r.ratchet.population(),
		Buzzes:        r.ratchet.buzzCounts(),
		Critical:      r.board.Critical(),
		Errors:        r.board.Errors(),
	}
	result.Messages = messages(reason, result.Critical, result.Errors)
	if r.recorder != nil {
		for _, window := range r.recorder.Windows() {
			result.Windows = append(result.Windows, r.recorder.Regions(window)...)
		}
	}
	if result.Pass {
		result.Score = r.board.Total()
	}
	return result
}

// progress は途中経過を OnProgress に渡す。スコアは走行が終わるまで暫定。
func (r *Runner) progress() {
	if r.cfg.OnProgress != nil {
		r.cfg.OnProgress(Result{Score: r.board.Total(), Language: r.language, Breakdown: r.board.Breakdown()})
	}
}

func (r *Runner) closeConnections() {
	for _, agents := range r.agents {
		for _, user := range agents {
			user.API.CloseIdleConnections()
		}
	}
}

func messages(reason string, groups ...[]string) []string {
	out := []string{}
	if reason != "" {
		out = append(out, reason)
	}
collect:
	for _, group := range groups {
		for _, message := range group {
			if len(out) >= maxMessages {
				break collect
			}
			if message != reason {
				out = append(out, message)
			}
		}
	}
	// 件数制限は元のメッセージ単位で適用し、複数行の詳細を欠落させない。
	lines := []string{}
	for _, message := range out {
		lines = append(lines, strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")...)
	}
	return lines
}

func (r *Runner) openedCounts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.opened))
	maps.Copy(out, r.opened)
	return out
}

func (r *Runner) initialize(ctx context.Context) string {
	r.cfg.Logger.Info("phase: initialize")
	ctx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	result, err := r.admin.Initialize(ctx)
	if err != nil {
		return fmt.Sprintf("初期化に失敗しました: %v", err)
	}
	if result.Language == "" {
		return "初期化の応答の language が空です"
	}
	r.language = result.Language
	// localDate は投稿期間のラベルで、サーバーの時計は進めない。初期データの投稿期間と
	// 日付が重ならないよう、履歴の最終日より後から始める。
	now := time.Now().UTC()
	r.firstDate = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if next := r.dataset.Config.EndDate.AddDate(0, 0, 1); r.firstDate.Before(next) {
		r.firstDate = next
	}
	return ""
}

// checkInitialData は初期データのユーザーとしてログインし、アプリが初期データどおりの
// 内容を返すことを確かめる。
func (r *Runner) checkInitialData(ctx context.Context) string {
	r.cfg.Logger.Info("phase: initial data")
	targets := r.initial.Targets()
	failures := make([]error, len(targets))
	parallel(len(targets), func(i int) {
		start := time.Now()
		failures[i] = r.initial.Check(ctx, r.api, targets[i])
		r.cfg.Logger.Info("initial user checked", "username", targets[i].Username, "region", targets[i].Region, "elapsed", time.Since(start).Round(time.Millisecond))
	})
	if err := errors.Join(failures...); err != nil {
		return "初期データの検証に失敗しました: " + err.Error()
	}
	return ""
}

func (r *Runner) pretest(ctx context.Context) string {
	r.cfg.Logger.Info("phase: pretest")
	token, err := r.admin.AdminLogin(ctx, "admin", "password1234")
	if err != nil || token == "" {
		return fmt.Sprintf("管理者としてログインできません: %v", err)
	}
	r.adminToken = token
	user := agent.New(r.api, geo.Asia, 0)
	if err := user.Register(ctx, "bench_pretest_user", "bench_pretest_user"); err != nil {
		return fmt.Sprintf("ユーザーの登録またはログインに失敗しました: %v", err)
	}
	if _, err := user.API.Register(ctx, user.Username, "duplicate", user.Region); !isStatus(err, http.StatusConflict) {
		return fmt.Sprintf("登録済みのユーザー名での登録が 409 になりません: %v", err)
	}
	if _, err := user.API.Login(ctx, user.Username, "wrong-secret"); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("誤ったシークレットキーでのログインが 401 になりません: %v", err)
	}
	profile, err := user.API.Me(ctx, user.Token)
	if err != nil {
		return fmt.Sprintf("プロフィールを取得できません: %v", err)
	}
	if profile.ID != user.ID || profile.Username != user.Username || profile.Region != user.Region {
		return fmt.Sprintf("プロフィールの内容が登録した内容と違います: %+v", profile)
	}
	feed, err := user.API.Feed(ctx, user.Token, "public", nil)
	if err != nil || len(feed.Items) != 0 || feed.HasMore {
		return fmt.Sprintf("投稿前のフィードが空ではありません: 投稿 %d 件, %v", len(feed.Items), err)
	}
	previous := user.Token
	if err := user.Login(ctx); err != nil {
		return fmt.Sprintf("2 回目のログインに失敗しました: %v", err)
	}
	if _, err := user.API.Me(ctx, previous); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("再ログイン前のセッションが無効になっていません: %v", err)
	}
	if err := user.API.Logout(ctx, user.Token); err != nil {
		return fmt.Sprintf("ログアウトに失敗しました: %v", err)
	}
	if _, err := user.API.Me(ctx, user.Token); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("ログアウトしたセッションが無効になっていません: %v", err)
	}
	return ""
}

func (r *Runner) load(ctx context.Context) string {
	r.cfg.Logger.Info("phase: load", "usersPerRegion", r.cfg.Users, "duration", r.cfg.Duration)
	start := time.Now()
	// ラチェットは観測窓ごとの集計で昇格を判定するので、観測窓の長さをそろえる。
	r.recorder = metrics.New(start, r.ratchetConfig.Window)
	for _, agents := range r.agents {
		for _, user := range agents {
			user.API = user.API.WithObserver(r.recorder)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Duration)
	defer cancel()
	ctx, abort := context.WithCancelCause(ctx)
	defer abort(nil)

	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		r.monitor(ctx, abort)
	}()

	var wg sync.WaitGroup
	r.loadStart = start
	r.join = func(region string, level, users int, buzz bool) {
		r.ensureReturning(ctx, &wg, region, r.ratchet.population()[region])
		// 実際のユーザーは同時には来ないので、次の観測窓のあいだに均等に散らして登録する。
		// まとめて登録すると、その瞬間だけ接続やロックが集中する。
		now := time.Now()
		for i := range users {
			at := now.Add(r.ratchetConfig.Window * time.Duration(i) / time.Duration(users))
			wg.Go(func() {
				if sleepUntil(ctx, at) == nil {
					r.runNewcomer(ctx, region, level, buzz)
				}
			})
		}
	}
	r.ratchet.mu.Lock()
	for _, user := range r.agentsSnapshot() {
		r.ratchet.users[user.Region]++
	}
	r.ratchet.mu.Unlock()
	for index, region := range geo.Regions {
		wg.Go(func() {
			if reason := r.runSchedule(ctx, start.Add(r.schedule.warmup), region, index); reason != "" {
				abort(errors.New(reason))
			}
		})
		for _, user := range r.agentsSnapshot() {
			if user.Region == region {
				wg.Go(func() { r.runUser(ctx, user, false) })
			}
		}
	}
	for region, users := range r.ratchet.population() {
		r.ensureReturning(ctx, &wg, region, users)
	}
	wg.Go(func() { r.runRatchet(ctx, start, r.join) })
	wg.Wait()
	abort(nil)
	<-monitorDone

	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		return cause.Error()
	}
	if reason := r.recorder.Violation(r.cfg.SLO, r.recorder.WindowAt(time.Now())+1); reason != "" {
		return "負荷走行を打ち切りました: " + reason
	}
	return ""
}

// monitor は、閉じた観測窓が SLO を破るか正しさの違反が記録されたら、
// ただちに負荷走行を打ち切る。
func (r *Runner) monitor(ctx context.Context, abort context.CancelCauseFunc) {
	ticker := time.NewTicker(sloCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if reason := r.recorder.Violation(r.cfg.SLO, r.recorder.WindowAt(now)); reason != "" {
				abort(errors.New("負荷走行を打ち切りました: " + reason))
				return
			}
			if r.board.CriticalCount() > 0 {
				abort(errors.New("整合性の違反: " + r.board.Critical()[0]))
				return
			}
		}
	}
}

func parallel(count int, fn func(int)) {
	var wg sync.WaitGroup
	for index := range count {
		wg.Go(func() { fn(index) })
	}
	wg.Wait()
}

// requestError は失敗したリクエストを記録する。失敗としての集計は metrics の
// Observer が済ませている。
func (r *Runner) requestError(ctx context.Context, format string, args ...any) {
	if ctx.Err() == nil {
		r.board.Errorf(format, args...)
	}
}

// invalid は、届いたが検証に失敗したレスポンスを記録し、リージョンの
// エラー率に数える。
func (r *Runner) invalid(ctx context.Context, region, format string, args ...any) {
	if ctx.Err() == nil {
		r.board.Errorf(format, args...)
		r.recorder.AddFailure(region, time.Now())
	}
}

// settle は書き込みの結果をモデルに記録する。4xx は拒否（反映されていない）、それ以外の
// 失敗（5xx、タイムアウト、通信エラー）は結果不明として扱う。
func settle(w *model.Write, err error) {
	var apiErr *client.APIError
	switch {
	case err == nil:
		w.Applied()
	case errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500:
		w.Rejected()
	default:
		w.Unknown()
	}
}

func isStatus(err error, status int) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}
