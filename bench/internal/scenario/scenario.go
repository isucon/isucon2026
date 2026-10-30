// Package scenario は ISUReal の負荷走行のシナリオ。
//
// サービスはアジアで流行っている写真共有アプリで、各リージョンでは 1 日に 1 回、全員に
// 「今日の写真を投稿しよう」という通知が届く。ユーザーは通知を受けて写真を投稿し、投稿した
// 人だけがその日の友達の投稿を見られる（ゲート）。投稿してフィードを開けたユーザーが翌日に
// 友達を連れてくるので、サービスが快適ならユーザーは日ごとに増える。最終日は全リージョンで
// 同時に投稿期間が始まるグランプリになる。
package scenario

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"maps"
	"math"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/benchrun"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/bench/internal/initialdata"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/photo"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/dataset"
)

// DefaultVerifyRate は負荷走行で重い検証を行う操作の既定の割合。
const DefaultVerifyRate = 0.1

const (
	initializeTimeout = 60 * time.Second
	// preloadTimeout は事前検証、初期データの検証、負荷ユーザーの準備にかける時間の上限。
	preloadTimeout = 45 * time.Second
	// subscribeTimeout は負荷走行の前に、負荷ユーザーが通知を購読し終えるのを待つ上限。
	subscribeTimeout = 5 * time.Second
	loadDuration     = 60 * time.Second
	sloCheckInterval = time.Second
	maxMessages      = 30
)

type Config struct {
	Target string
	Host   string
	// Users はリージョンあたりの最初の負荷ユーザー数（登録するユーザーの数。初期データの
	// cohort のユーザーも同じ数までログインして加える）。
	Users int
	// Duration は負荷走行の長さ。0 なら 60 秒。
	Duration time.Duration
	Weights  score.Weights
	SLO      metrics.SLO
	Retry    client.RetryPolicy
	// Staleness は書き込みが読み取りに反映されるまでに許す遅れ。
	Staleness time.Duration
	// VerifyRate は負荷走行で重い検証（フィードの欠落、画像 URL の署名、履歴の画像）を行う
	// 操作の割合（0 より大きく 1 以下）。0 なら DefaultVerifyRate。
	VerifyRate float64
	// MoveRate は正なら、好みにかかわらず、移れるユーザーが日を過ごし終えるたびにこの確率で
	// リージョンを移る（開発で移動を確かめる用）。0 なら好み（persona）に従う。
	MoveRate float64
	// SignOffRate は正なら、好みにかかわらず、日を過ごし終えるたびにこの確率でアプリを閉じる
	// （開発でログインし直しを確かめる用）。0 なら好みに従う。
	SignOffRate float64
	// Growth はユーザーの増え方。ゼロ値なら DefaultGrowth。
	Growth GrowthConfig
	// Dataset は対象の DB に入っている初期データ。nil なら dataset.Small() から作る。
	Dataset *dataset.World
	// ImageDigests は Dataset の画像の SHA-256 の表。nil なら初期データの画像を描き直して比べる。
	ImageDigests *dataset.ImageDigests
	// Photos は投稿に使う写真。nil なら生成した小さな画像を使う。
	Photos *photo.Set
	// AwaitPhotos は、写真のアーカイブの読み込みを初期化と並行して進めるためのもの。初期化の
	// あと、写真を使う前に呼び、返った写真を Photos の代わりに使う。nil なら待たない。
	AwaitPhotos func(context.Context) (*photo.Set, error)
	// Seed は走行の種。0 なら無作為に決める。
	Seed uint64
	// Evil は不正な利用者を動かす。nil なら動かさない。
	Evil   EvilFunc
	Logger *slog.Logger
	// OperatorLogger は運営向けのログ。supervisor の下では Logger が参加者に見える標準出力に
	// つながるのに対し、こちらは運営だけが見られる標準エラー出力につなぐ。参加者に不要な
	// すべての phase 行（initialize、pretest、initial data、prepare users、load、final check、
	// final check done。load は usersPerRegion、duration、seed、photos を含む）と、prepare users
	// の内訳、initial user checked、不正な利用者の通信記録の出力先として使う。nil なら Logger を使う。
	OperatorLogger *slog.Logger
	// OnProgress は途中経過（暫定のスコアと内訳）を受け取る。フェーズの境目と、負荷走行の
	// 観測窓が閉じるたびに呼ぶ。スーパーバイザーへの報告に使う。
	OnProgress func(Result)
}

type Result struct {
	Pass     bool   `json:"pass"`
	Score    int64  `json:"score"`
	Language string `json:"language"`
	Reason   string `json:"reason,omitempty"`
	// Messages は結果 JSON に出す参加者向けのエラー系のみ。失格の理由、正しさの違反、
	// エラーの順。
	Messages []string `json:"messages"`
	// Story は走行の様子（日ごとの人数）。進行ログでは参加者にも出すが、結果 JSON の
	// Messages には含めない。
	Story []string `json:"story"`

	// 以下は運営向け（D-005）。
	// Agent、Harness、Model は調査用の自己申告値。得点や合否には使用しない。
	Agent     string           `json:"agent"`
	Harness   string           `json:"harness"`
	Model     string           `json:"model"`
	Breakdown map[string]int64 `json:"breakdown"`
	// Scores はリージョンと日と要素ごとの件数と得点。
	Scores []score.Cell `json:"scores"`
	// OpenedWindows はリージョンごとに開いた投稿期間の数、Users は最後の負荷ユーザーの数。
	OpenedWindows map[string]int `json:"openedWindows"`
	Users         map[string]int `json:"users"`
	// GrandPrix はグランプリ（最終日）にゲートを通過した人数。
	GrandPrix  map[string]int  `json:"grandPrix"`
	Days       []DayReport     `json:"days"`
	Reputation int             `json:"reputation"`
	Seed       uint64          `json:"seed"`
	Critical   []string        `json:"critical"`
	Errors     []string        `json:"errors"`
	Windows    []metrics.Stats `json:"windows"`
	// Routes はリージョンとルートごとの所要時間。
	Routes []metrics.RouteStats `json:"routes"`
	// Friends は走行の終わりの、リージョンごとの友達の数と友達の友達の数。
	Friends map[string]FriendStats `json:"friends"`
	// Ranking は友達の友達フィードで確かめたおすすめの点数の記録。
	Ranking model.RankingStats `json:"ranking"`
}

type Runner struct {
	cfg      Config
	api      *client.Client
	admin    *client.Client
	board    *score.Board
	recorder atomic.Pointer[metrics.Recorder]
	books    atomic.Pointer[book]
	initial  *initialdata.Checker
	dataset  *dataset.World
	// model は bench が知っているアプリの状態。フィードの検証に使う。
	model  *model.Model
	photos *photo.Set
	tt     timetable
	seed   uint64
	ips    *ipPool

	adminToken    string
	language      string
	surveyAgent   string
	surveyHarness string
	surveyModel   string
	firstDate     time.Time
	// deleteRate と changeRate は、投稿後に投稿を削除する確率と公開範囲を変える確率。
	deleteRate float64
	changeRate float64
	// visibility は公開範囲を決める（テスト用。nil ならユーザーの種類で決める）。
	visibility func() string
	// peekRate は、通知を受けて投稿する前にフィードを覗くユーザーの割合。
	peekRate float64
	// moveRate は Config.MoveRate。
	moveRate float64
	// signOffRate は Config.SignOffRate。
	signOffRate float64
	// historyImages は履歴を 1 か月読むたびに中身を取得する画像の数。
	historyImages int
	// verifyRate は負荷走行で重い検証を行う操作の割合（Config.VerifyRate）。
	verifyRate float64
	// returningRatio は、思い出を見返すユーザーを負荷ユーザー何人につき 1 人置くか。
	returningRatio int
	// wg は負荷走行のゴルーチン。
	wg *sync.WaitGroup

	// stepName は事前検証で今していること。時間切れの理由に使う。
	stepName atomic.Value

	mu            sync.Mutex
	users         map[string][]*user
	byID          map[string]*user
	certified     []string
	usernames     map[string]bool
	registrations int
	nameRand      *rand.Rand
	historyRand   *rand.Rand
	opened        map[string]int
	windowDay     map[string]int
	lastWindow    map[string]string
	friendCount   map[string]int
	// arriving はリージョンごとの、登録中の新しいユーザーの数。
	arriving       map[string]int
	returningCount map[string]int
	// moves はリージョンを移った人数（"移る前>移った先" ごと）。
	moves      map[string]int
	revisiting map[string]bool
	evilUsers  map[string]bool
	// discoveries はリージョンごとの、フィードで見た人に送った友達申請の数。
	discoveries map[string]int
	// openedAt は投稿期間ごとに、開く要求を送った時刻。
	openedAt        map[string]time.Time
	story           []string
	reputationCount int
}

func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Users < 1 || cfg.Duration < 0 {
		return nil, fmt.Errorf("users must be positive and duration must be nonnegative")
	}
	if cfg.VerifyRate == 0 {
		cfg.VerifyRate = DefaultVerifyRate
	}
	if !(cfg.VerifyRate > 0 && cfg.VerifyRate <= 1) {
		return nil, fmt.Errorf("verify rate must be in (0, 1]: %v", cfg.VerifyRate)
	}
	if cfg.Duration == 0 {
		cfg.Duration = loadDuration
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.OperatorLogger == nil {
		cfg.OperatorLogger = cfg.Logger
	}
	if cfg.SLO == (metrics.SLO{}) {
		cfg.SLO = metrics.DefaultSLO
	}
	if cfg.Retry == (client.RetryPolicy{}) {
		cfg.Retry = client.DefaultRetryPolicy
	}
	if cfg.Growth == (GrowthConfig{}) {
		cfg.Growth = DefaultGrowth
	}
	if cfg.Dataset == nil {
		cfg.Dataset = dataset.Generate(dataset.Small())
	}
	if cfg.Photos == nil {
		cfg.Photos = photo.Generated()
	}
	if cfg.Seed == 0 {
		cfg.Seed = rand.Uint64()
	}
	m := model.New()
	m.Staleness = cfg.Staleness
	m.LoadDataset(cfg.Dataset)
	api := client.NewClient(client.Config{Base: cfg.Target, Host: cfg.Host, Retry: cfg.Retry})
	usernames := map[string]bool{}
	for _, u := range cfg.Dataset.Users {
		usernames[u.Username] = true
	}
	r := &Runner{
		cfg:            cfg,
		api:            api,
		board:          score.NewBoard(cfg.Weights),
		initial:        initialdata.New(cfg.Dataset),
		dataset:        cfg.Dataset,
		model:          m,
		photos:         cfg.Photos,
		tt:             newTimetable(cfg.Duration),
		seed:           cfg.Seed,
		ips:            newIPPool(cfg.Seed),
		deleteRate:     0.05,
		peekRate:       0.1,
		changeRate:     0.1,
		historyImages:  4,
		verifyRate:     cfg.VerifyRate,
		moveRate:       cfg.MoveRate,
		signOffRate:    cfg.SignOffRate,
		returningRatio: returningRatio,
		users:          map[string][]*user{},
		byID:           map[string]*user{},
		usernames:      usernames,
		nameRand:       rand.New(rand.NewPCG(cfg.Seed, 0x6e616d65)),
		historyRand:    rand.New(rand.NewPCG(cfg.Seed, 0x68697374)),
		opened:         map[string]int{},
		windowDay:      map[string]int{},
		lastWindow:     map[string]string{},
		friendCount:    map[string]int{},
		arriving:       map[string]int{},
		returningCount: map[string]int{},
		moves:          map[string]int{},
		discoveries:    map[string]int{},
		revisiting:     map[string]bool{},
		evilUsers:      map[string]bool{},
		openedAt:       map[string]time.Time{},
	}
	r.initial.ImageDigests = cfg.ImageDigests
	// 管理者は JP から送る。IP は負荷ユーザーと同じく走行ごとに決める。
	r.admin = api.WithCountry(geo.Country(geo.Asia, 0)).WithClientIP(r.ips.Next())
	return r, nil
}

func (r *Runner) Run(ctx context.Context) Result {
	defer r.closeConnections()
	reason := ""
	for _, phase := range []func(context.Context) string{r.initialize, r.awaitPhotos, r.preload, r.load} {
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
		Agent:         r.surveyAgent,
		Harness:       r.surveyHarness,
		Model:         r.surveyModel,
		Reason:        reason,
		Story:         r.storyLines(),
		Breakdown:     r.board.Breakdown(),
		Scores:        r.board.Cells(),
		OpenedWindows: r.openedCounts(),
		Users:         r.populations(),
		Reputation:    r.reputations(),
		Friends:       r.friendStats(),
		Ranking:       r.model.RankingStats(),
		Seed:          r.seed,
		Critical:      r.board.Critical(),
		Errors:        r.board.Errors(),
	}
	if b := r.books.Load(); b != nil {
		result.Days = b.report()
		for i := range result.Days {
			result.Days[i].Points = r.board.DayPoints(result.Days[i].Region, result.Days[i].Day)
		}
		_, result.GrandPrix = b.passes()
	}
	result.Messages = r.resultMessages(reason)
	if rec := r.recorder.Load(); rec != nil {
		for _, window := range rec.Windows() {
			result.Windows = append(result.Windows, rec.Regions(window)...)
		}
		result.Routes = rec.Routes()
	}
	if result.Pass {
		result.Score = r.board.Total()
	}
	return result
}

// progress は途中経過を OnProgress に渡す。スコアは走行が終わるまで暫定。
func (r *Runner) progress() {
	if r.cfg.OnProgress != nil {
		r.cfg.OnProgress(Result{Score: r.board.Total(), Language: r.language, Agent: r.surveyAgent, Harness: r.surveyHarness, Model: r.surveyModel, Breakdown: r.board.Breakdown()})
	}
}

func (r *Runner) closeConnections() {
	// 管理者と不正な利用者もこの接続プールを共有している。
	r.api.CloseIdleConnections()
	for _, u := range r.usersSnapshot() {
		u.API.CloseIdleConnections()
	}
}

// resultMessages は結果 JSON の messages を組み立てる。エラー系（失格の理由、正しさの
// 違反、エラー）だけを入れ、Story（日ごとの人数など）は含めない。Story は進行ログで
// 参加者に出るので、結果 JSON で繰り返す必要はない。
func (r *Runner) resultMessages(reason string) []string {
	return messages(reason, r.board.Critical(), r.board.Errors())
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
	return maps.Clone(r.opened)
}

func (r *Runner) populations() map[string]int {
	out := map[string]int{}
	for _, region := range geo.Regions {
		out[region] = r.population(region)
	}
	return out
}

func (r *Runner) initialize(ctx context.Context) string {
	r.operatorLogger().Info("phase: initialize")
	ctx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	started := time.Now()
	result, err := r.admin.Initialize(ctx)
	if err != nil {
		return fmt.Sprintf("初期化に失敗しました: %v（上限 %s）", err, initializeTimeout)
	}
	// 初期化は前回の走行で増えたデータを消すので、強い実装ほど重くなる。上限に近づく前に
	// 気づけるよう、時間がかかったら運営向けに残す（参加者向けの Story には出さない）。
	if took := time.Since(started); took >= slowInitialize {
		r.operatorLogger().Info(fmt.Sprintf("初期化に %.1f 秒かかりました（上限 %s）", took.Seconds(), initializeTimeout))
	}
	if result.Language == "" {
		return "初期化の応答の language が空です"
	}
	// 申告はポータルへの報告にそのまま載せるので、報告の上限に収める。language は必須の項目
	// なので長すぎれば fail にし、合否に使わない agent、harness、model は切り詰める。
	if len(result.Language) > benchrun.MaxLanguageBytes {
		return fmt.Sprintf("初期化の応答の language が %d バイトを超えています", benchrun.MaxLanguageBytes)
	}
	r.language = result.Language
	r.surveyAgent = r.surveyValue("agent", result.Agent)
	r.surveyHarness = r.surveyValue("harness", result.Harness)
	r.surveyModel = r.surveyValue("model", result.Model)
	// localDate は投稿期間のラベルで、サーバーの時計は進めない。初期データの投稿期間と
	// 日付が重ならないよう、履歴の最終日より後から始める。
	now := time.Now().UTC()
	r.firstDate = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if next := r.dataset.Config.EndDate.AddDate(0, 0, 1); r.firstDate.Before(next) {
		r.firstDate = next
	}
	return ""
}

// surveyValue は初期化の応答の申告 name の値 value を、報告の上限（benchrun.MaxSurveyBytes）に
// 切り詰めて返す。切り詰めたら運営向けに残す。
func (r *Runner) surveyValue(name, value string) string {
	truncated := benchrun.Truncate(value, benchrun.MaxSurveyBytes)
	if truncated != value {
		r.operatorLogger().Info("survey value truncated", "field", name, "bytes", len(value), "limit", benchrun.MaxSurveyBytes)
	}
	return truncated
}

// award は u の操作 tag を得点に数える。day はその操作が属するリージョンの日で、日が
// 始まる前なら 0。
func (r *Runner) award(u *user, day int, tag score.Tag) {
	r.board.Add(tag, u.Region, day)
}

// dayNow は region のいまの日（1 始まり）。負荷走行の前と、1 日目が始まる前は 0。
func (r *Runner) dayNow(region string) int {
	b := r.books.Load()
	if b == nil {
		return 0
	}
	return r.tt.dayAt(region, time.Since(b.start))
}

// step は事前検証で今していることを記録する。時間切れのときに理由として出す。
func (r *Runner) step(name string) {
	r.stepName.Store(name)
}

// awaitPhotos は初期化と並行して読んでいた写真のアーカイブを待つ。読めなかったときは
// 参加者の責任ではないので、ベンチマーカー側の失敗として止める。
func (r *Runner) awaitPhotos(ctx context.Context) string {
	if r.cfg.AwaitPhotos == nil {
		return ""
	}
	photos, err := r.cfg.AwaitPhotos(ctx)
	if err != nil {
		return "ベンチマーカーの準備に失敗しました（写真のアーカイブ）: " + err.Error()
	}
	r.photos = photos
	return ""
}

// preload は事前検証、初期データの検証、負荷ユーザーの準備を preloadTimeout 以内に行う。
func (r *Runner) preload(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, preloadTimeout)
	defer cancel()
	timedOut := func() string {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			step, _ := r.stepName.Load().(string)
			return fmt.Sprintf("負荷走行の前の確認が %s 以内に終わりませんでした（%s）", preloadTimeout, step)
		}
		return ""
	}
	if reason := r.pretest(ctx); reason != "" {
		return cmp.Or(timedOut(), reason)
	}
	// 初期データの検証と負荷ユーザーの準備は互いに関係しないので並行に行う。
	var reasons [2]string
	parallel(2, func(i int) {
		if i == 0 {
			reasons[i] = r.checkInitialData(ctx)
		} else {
			reasons[i] = r.prepareUsers(ctx)
		}
	})
	if reason := timedOut(); reason != "" {
		return reason
	}
	return cmp.Or(reasons[0], reasons[1])
}

// checkInitialData は初期データのユーザーとしてログインし、アプリが初期データどおりの
// 内容を返すことを確かめる。
func (r *Runner) checkInitialData(ctx context.Context) string {
	r.step("初期データの検証")
	// phase 行も含め、初期データの検証の経過は運営向け（参加者には不要）。
	r.operatorLogger().Info("phase: initial data")
	// 対象、Memories の月、取得する画像は走行の種で決まり、同じ種なら同じ検証になる。
	targets := r.initial.Targets(r.seed)
	usernames := make([]string, len(targets))
	for i, target := range targets {
		usernames[i] = target.Username
	}
	r.operatorLogger().Info("initial data targets", "users", strings.Join(usernames, ","), "seed", r.seed)
	failures := make([]error, len(targets))
	api := r.api.WithClientIP(r.ips.Next())
	parallel(len(targets), func(i int) {
		start := time.Now()
		var report initialdata.Report
		report, failures[i] = r.initial.Check(ctx, api, targets[i], r.seed)
		// 誰を検証したか、読んだ月、段階ごとの所要時間と件数も運営向け。遅いときに、どの段階で
		// 時間がかかったかを見分ける。
		attrs := []any{"username", targets[i].Username, "region", targets[i].Region, "elapsed", time.Since(start).Round(time.Millisecond), "months", strings.Join(report.Months, ",")}
		for _, s := range report.Steps {
			attrs = append(attrs, slog.Group(s.Name, "elapsed", s.Elapsed.Round(time.Millisecond), "count", s.Count))
		}
		r.operatorLogger().Info("initial user checked", attrs...)
		if failures[i] != nil {
			r.step(fmt.Sprintf("初期データのユーザー %s の検証", targets[i].Username))
		}
	})
	if err := errors.Join(failures...); err != nil {
		return "初期データの検証に失敗しました: " + err.Error()
	}
	return ""
}

func (r *Runner) pretest(ctx context.Context) string {
	r.step("事前検証")
	r.operatorLogger().Info("phase: pretest")
	token, err := r.admin.AdminLogin(ctx, "admin", "password1234")
	if err != nil || token == "" {
		return fmt.Sprintf("管理者としてログインできません: %v", err)
	}
	r.adminToken = token
	a := agent.New(r.api, geo.Asia, 0)
	a.API = a.API.WithClientIP(r.ips.Next())
	username, _ := r.newName()
	displayName := username
	if err := a.Register(ctx, username, displayName); err != nil {
		return fmt.Sprintf("ユーザーの登録またはログインに失敗しました: %v", err)
	}
	if _, err := a.API.Register(ctx, a.Username, "duplicate", a.Region); !isStatus(err, http.StatusConflict) {
		return fmt.Sprintf("登録済みのユーザー名での登録が 409 になりません: %v", err)
	}
	if _, err := a.API.Login(ctx, a.Username, "wrong-secret"); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("誤ったシークレットキーでのログインが 401 になりません: %v", err)
	}
	profile, err := a.API.Me(ctx, a.Token)
	if err != nil {
		return fmt.Sprintf("プロフィールを取得できません: %v", err)
	}
	if profile.ID != a.ID || profile.Username != a.Username || profile.DisplayName != displayName || profile.Region != a.Region {
		return fmt.Sprintf("プロフィールの内容が登録した内容と違います: %+v", profile)
	}
	feed, err := a.API.Feed(ctx, a.Token, "public", nil)
	if err != nil || len(feed.Items) != 0 || feed.HasMore {
		return fmt.Sprintf("投稿前のフィードが空ではありません: 投稿 %d 件, %v", len(feed.Items), err)
	}
	if _, err := a.API.MyCurrentPost(ctx, a.Token); !isStatus(err, http.StatusNotFound) {
		return fmt.Sprintf("投稿前の今日の投稿が 404 になりません: %v", err)
	}
	if reason := r.pretestRules(ctx, a); reason != "" {
		return reason
	}
	if err := r.admin.DeleteCertification(ctx, r.adminToken, a.ID); !isStatus(err, http.StatusConflict) {
		return fmt.Sprintf("認定の取り消しが 409 になりません: %v", err)
	}
	previous := a.Token
	if err := a.Login(ctx); err != nil {
		return fmt.Sprintf("2 回目のログインに失敗しました: %v", err)
	}
	if _, err := a.API.Me(ctx, previous); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("再ログイン前のセッションが無効になっていません: %v", err)
	}
	if err := a.API.Logout(ctx, a.Token); err != nil {
		return fmt.Sprintf("ログアウトに失敗しました: %v", err)
	}
	if _, err := a.API.Me(ctx, a.Token); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("ログアウトしたセッションが無効になっていません: %v", err)
	}
	return ""
}

// load は負荷走行。負荷ユーザーが通知を購読し終えたら時計を進め始め、時間割どおりに
// 投稿期間を開く。
// operatorLogger は運営向けのログ。NewRunner を経ない Runner でも nil にならないよう
// ここでもフォールバックする。
func (r *Runner) operatorLogger() *slog.Logger {
	if r.cfg.OperatorLogger != nil {
		return r.cfg.OperatorLogger
	}
	if r.cfg.Logger != nil {
		return r.cfg.Logger
	}
	return slog.Default()
}

func (r *Runner) load(ctx context.Context) string {
	// phase: load は実行条件（seed を含む）を持つ運営向けの記録で、参加者には不要。
	// supervisor の下では運営向けロガー（標準エラー出力）にだけ出す。単独実行ではどちらも
	// 標準エラー出力につながるので、bench.log を読む report.py の負荷区間の特定は従来どおり。
	r.operatorLogger().Info("phase: load", "usersPerRegion", r.cfg.Users, "duration", r.cfg.Duration, "seed", r.seed, "photos", r.photos.Count())
	ctx, abort := context.WithCancelCause(ctx)
	defer abort(nil)
	var wg sync.WaitGroup
	r.wg = &wg
	users := r.usersSnapshot()
	subscribed := make(chan struct{}, len(users))
	r.chooseLeavers()
	for _, u := range users {
		u.API = u.API.WithObserver(&accounting{r: r, slo: true, growth: true})
		wg.Go(func() { r.runUser(ctx, u, false, subscribed) })
	}
	deadline := time.NewTimer(subscribeTimeout)
wait:
	for range users {
		select {
		case <-subscribed:
		case <-deadline.C:
			break wait
		}
	}
	deadline.Stop()

	start := time.Now()
	r.recorder.Store(metrics.New(start, metrics.DefaultWidth))
	r.books.Store(newBook(r.tt, start))
	stop := time.AfterFunc(r.cfg.Duration, func() { abort(context.DeadlineExceeded) })
	defer stop.Stop()

	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		r.monitor(ctx, abort)
	}()
	for _, region := range geo.Regions {
		wg.Go(func() {
			for day := 1; day <= r.tt.days(); day++ {
				if sleepUntil(ctx, start.Add(r.tt.start(region, day))) != nil {
					return
				}
				if _, err := r.openDay(ctx, region, day); err != nil {
					abort(err)
					return
				}
				r.ensureReturning(ctx, &wg, region, r.population(region))
			}
		})
		r.ensureReturning(ctx, &wg, region, r.population(region))
	}
	if r.cfg.Evil != nil {
		wg.Go(func() { r.cfg.Evil(ctx, r.evilEnv(start)) })
	}
	wg.Go(func() {
		// 観測窓が閉じるたびに途中経過を報告する。
		ticker := time.NewTicker(metrics.DefaultWidth)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.progress()
			}
		}
	})
	<-ctx.Done()
	wg.Wait()
	<-monitorDone
	r.tellGrandPrix()
	if err := r.admin.AdminLogout(context.WithoutCancel(ctx), r.adminToken); err != nil {
		r.board.Errorf("管理者としてログアウトできません: %v", err)
	}

	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		return cause.Error()
	}
	rec := r.recorder.Load()
	if reason := rec.Violation(r.cfg.SLO, rec.WindowAt(time.Now())+1); reason != "" {
		return "負荷走行を打ち切りました: " + reason
	}
	return ""
}

// monitor は、閉じた観測窓が SLO を破るか正しさの違反が記録されたら、
// ただちに負荷走行を打ち切る。
func (r *Runner) monitor(ctx context.Context, abort context.CancelCauseFunc) {
	ticker := time.NewTicker(sloCheckInterval)
	defer ticker.Stop()
	rec := r.recorder.Load()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if reason := rec.Violation(r.cfg.SLO, rec.WindowAt(now)); reason != "" {
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

// chooseLeavers は退会するユーザーを決める。各リージョンで bench が登録したユーザー
// （認定公開アカウントを除く）が 2 人以上いれば 1 人が、最後の staggered 日に投稿したあとで
// 退会する。初期データのユーザーは退会させない（過去の RealMoji やコメントが消え、ほかの
// 人の履歴が変わる）。
func (r *Runner) chooseLeavers() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, region := range geo.Regions {
		var candidates []*user
		for _, u := range r.users[region] {
			if u.registered && !u.certified.Load() {
				candidates = append(candidates, u)
			}
		}
		if len(candidates) >= 2 {
			candidates[hashOf(r.seed, "leave", region)%uint64(len(candidates))].leaveOn = r.tt.staggered
		}
	}
}

// deleteAccount は u を退会させ、古いセッションが使えなくなったことを確かめる。
func (r *Runner) deleteAccount(ctx context.Context, u *user) {
	w := r.model.BeginDeleteAccount(u.ID)
	err := u.API.DeleteAccount(ctx, u.Token)
	settle(w, err)
	if err != nil {
		r.requestError(ctx, u, "%s: 退会できません: %v", u.Username, err)
		// 応答が届かなくても退会は済んでいることがある。結果のわからないユーザーはもう
		// 使わず、走行後の整合性チェックでも確かめない。
		if !isRejection(err) {
			u.stopped.Store(true)
		}
		return
	}
	u.stopped.Store(true)
	r.dropFriendsOf(u)
	r.award(u, r.dayNow(u.Region), score.TagDeleteAccount)
	_, err = u.API.Me(client.MayFailWith(ctx, http.StatusUnauthorized), u.Token)
	switch {
	case isStatus(err, http.StatusUnauthorized):
	case err == nil:
		r.invalid(ctx, u, "%s: 退会したユーザーのセッションが無効になっていません", u.Username)
	case ctx.Err() == nil && !r.bannedErr(u, err):
		// 期待外のステータスや読めない応答は Observer が失敗として数えているので、ログだけ残す
		// （u は止めたので requestError は使えない）。
		r.board.Errorf("%s: 退会したユーザーのセッションが無効になっていません: %v", u.Username, err)
	}
}

func parallel(count int, fn func(int)) {
	var wg sync.WaitGroup
	for index := range count {
		wg.Go(func() { fn(index) })
	}
	wg.Wait()
}

func sleepUntil(ctx context.Context, at time.Time) error {
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// requestError は失敗したリクエストを記録する。失敗としての集計は Observer が済ませて
// いる。BAN による拒否は悪評として別に記録する。
func (r *Runner) requestError(ctx context.Context, u *user, format string, args ...any) {
	if ctx.Err() != nil || u.stopped.Load() {
		return
	}
	for _, arg := range args {
		if err, ok := arg.(error); ok && r.bannedErr(u, err) {
			return
		}
	}
	r.board.Errorf(format, args...)
}

// adminError は管理者の操作の失敗を記録する。
func (r *Runner) adminError(ctx context.Context, format string, args ...any) {
	if ctx.Err() == nil {
		r.board.Errorf(format, args...)
	}
}

// invalid は、届いたが検証に失敗したレスポンスを記録し、エラー率と前日の失敗率に数える。
func (r *Runner) invalid(ctx context.Context, u *user, format string, args ...any) {
	r.invalidN(ctx, u, 1, format, args...)
}

// invalidN は invalid と同じく記録し、打ち切りのエラー率には n 件として数える（間引いた
// 検証で見つけた失敗）。前日の失敗率には 1 件として数える。
func (r *Runner) invalidN(ctx context.Context, u *user, n int64, format string, args ...any) {
	if ctx.Err() != nil {
		return
	}
	r.board.Errorf(format, args...)
	now := time.Now()
	if rec := r.recorder.Load(); rec != nil {
		rec.AddFailure(u.Region, now, n)
	}
	if b := r.books.Load(); b != nil && !u.returning {
		b.fail(u.Region, now, n)
	}
}

// sample は、負荷走行のこの操作で重い検証を行うかを verifyRate の確率で決める。行うなら、
// 見つけた失敗 1 件をエラー率に何件として数えるか（割合の逆数）を返し、行わなければ 0 を
// 返す。一部しか検証しなくても、ときどき誤る実装のエラー率の期待値は全件を検証したときと
// 変わらない。
func (r *Runner) sample() int64 {
	if rand.Float64() >= r.verifyRate {
		return 0
	}
	return max(1, int64(math.Round(1/r.verifyRate)))
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

// windowOpenedAt は投稿期間 windowID を開く要求を送った時刻。知らなければ fallback。
func (r *Runner) windowOpenedAt(windowID string, fallback time.Time) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if at, ok := r.openedAt[windowID]; ok {
		return at
	}
	return fallback
}

// pretestRules は、負荷走行では起きにくい拒否の規則を確かめる。地域外のサーバーからの
// アクセス、JPEG でない画像や寸法の大きすぎる画像、署名の違う画像 URL や期限切れの画像 URL。
// どれも拒否を省けば速くなるので、省いていないことを確かめる。
func (r *Runner) pretestRules(ctx context.Context, a *agent.Agent) string {
	other := a.API.WithCountry(geo.Country(geo.Europe, 0))
	if _, err := other.Me(client.MayFailWith(ctx, http.StatusForbidden), a.Token); !isStatus(err, http.StatusForbidden) {
		return fmt.Sprintf("地域外のサーバーからのアクセスが 403 になりません: %v", err)
	}
	if err := a.API.PutProfileImage(client.MayFailWith(ctx, http.StatusBadRequest), a.Token, []byte("ISUReal: this is not a JPEG")); !isStatus(err, http.StatusBadRequest) {
		return fmt.Sprintf("JPEG でないプロフィール画像が 400 になりません: %v", err)
	}
	if err := a.API.PutProfileImage(client.MayFailWith(ctx, http.StatusBadRequest), a.Token, grayJPEG(513, 513)); !isStatus(err, http.StatusBadRequest) {
		return fmt.Sprintf("513x513 のプロフィール画像が 400 になりません: %v", err)
	}
	image := r.photos.Profile(hashOf(r.seed, "pretest", a.Username))
	if err := a.API.PutProfileImage(ctx, a.Token, image); err != nil {
		return fmt.Sprintf("プロフィール画像を設定できません: %v", err)
	}
	profile, err := a.API.Me(ctx, a.Token)
	if err != nil || profile.ProfileImageURL == nil {
		return fmt.Sprintf("プロフィール画像の URL を取得できません: %v", err)
	}
	url := *profile.ProfileImageURL
	if data, err := a.API.Image(ctx, url); err != nil || !client.SameImage(data, image) {
		return fmt.Sprintf("プロフィール画像を取得できません: %v", err)
	}
	imageID := imageurl.ImageID(url)
	exp := time.Now().Add(time.Hour).Unix()
	forged := fmt.Sprintf("/api/images/%s?exp=%d&sig=%s", imageID, exp, imageurl.Sign("wrong-"+a.SecretKey, imageID, exp))
	if _, err := a.API.Image(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), forged); !isStatus(err, http.StatusForbidden) && !isStatus(err, http.StatusNotFound) {
		return fmt.Sprintf("署名の違う画像 URL が拒否されません: %v", err)
	}
	past := time.Now().Add(-time.Minute).Unix()
	expired := fmt.Sprintf("/api/images/%s?exp=%d&sig=%s", imageID, past, imageurl.Sign(a.SecretKey, imageID, past))
	if _, err := a.API.Image(client.MayFailWith(ctx, http.StatusForbidden, http.StatusNotFound), expired); !isStatus(err, http.StatusForbidden) && !isStatus(err, http.StatusNotFound) {
		return fmt.Sprintf("期限切れの画像 URL が拒否されません: %v", err)
	}
	return ""
}

// grayJPEG は w x h の灰色の JPEG を返す。
func grayJPEG(w, h int) []byte {
	var buf bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 128
	}
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}
