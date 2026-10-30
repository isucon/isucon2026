package scenario

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
	"github.com/isucon/isucon2026/bench/internal/photo"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/dataset"
)

// tinyDataset は初期データを使わないテストのための小さな初期データ。
var tinyDataset = func() *dataset.World {
	cfg := dataset.Small()
	cfg.Users, cfg.Days = 20, 1
	return dataset.Generate(cfg)
}()

// newTestRunner は偽のアプリに向けた Runner を作り、各リージョンに usersPerRegion 人の
// 負荷ユーザーを登録済みとして用意する（友達とブロックも作る）。
func newTestRunner(t *testing.T, usersPerRegion int, slo metrics.SLO) (*Runner, *fakeApp) {
	t.Helper()
	runner, err := NewRunner(Config{
		Users:    usersPerRegion,
		Duration: time.Second,
		SLO:      slo,
		Retry:    client.RetryPolicy{MaxRetries: 1, Base: time.Millisecond, MaxWait: time.Millisecond},
		// 偽のアプリとモデルの食い違いを見逃さないよう、テストではすべて検証する。
		VerifyRate: 1,
		Dataset:    tinyDataset,
		Seed:       1,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.deleteRate, runner.changeRate = 0, 0
	// 偽のアプリは初期データのユーザーを知らないので、思い出を見返すユーザーは置かない。
	runner.returningRatio = 0
	app := newFakeApp(t)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	runner.api = client.NewClient(client.Config{Base: server.URL, Retry: runner.cfg.Retry})
	runner.admin = runner.api.WithCountry("JP")
	runner.adminToken = "admin"
	runner.firstDate = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for _, region := range geo.Regions {
		var users []*user
		for index := range usersPerRegion {
			a := agent.New(runner.api, region, index)
			a.ID = fmt.Sprintf("u-%s%d", region, index)
			a.Username, a.Token, a.SecretKey = strings.TrimPrefix(a.ID, "u-"), a.ID, "secret-"+a.ID
			u := runner.newUser(a, browser)
			u.registered = true
			app.addUser(u.ID, u.Username, u.SecretKey, region)
			runner.model.AddUser(model.User{ID: u.ID, Username: u.Username, SecretKey: u.SecretKey, Region: region})
			runner.addUser(u)
			users = append(users, u)
		}
		if reason := runner.connect(t.Context(), users); reason != "" {
			t.Fatal(reason)
		}
	}
	return runner, app
}

// TestLoadPhaseGoesToOperatorLogger は、phase: load（seed など実行条件を含む）が参加者向け
// ロガーに出ず、運営向けロガーにだけ出ることを確かめる。
func TestLoadPhaseGoesToOperatorLogger(t *testing.T) {
	var participant, operator bytes.Buffer
	runner, err := NewRunner(Config{
		Target:         "http://127.0.0.1:1",
		Users:          1,
		Duration:       time.Second,
		Dataset:        tinyDataset,
		Logger:         slog.New(slog.NewTextHandler(&participant, nil)),
		OperatorLogger: slog.New(slog.NewTextHandler(&operator, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_ = runner.load(ctx)
	if strings.Contains(participant.String(), "phase: load") {
		t.Fatalf("phase: load went to the participant logger: %s", participant.String())
	}
	if !strings.Contains(operator.String(), "phase: load") || !strings.Contains(operator.String(), "seed=") {
		t.Fatalf("phase: load missing from the operator logger: %s", operator.String())
	}
}

// TestPhaseLinesGoToOperatorLogger は、すべての phase 行（initialize、pretest、
// initial data、prepare users、final check、final check done）と、prepare users の内訳、
// initial user checked が、参加者向けロガーに出ず運営向けロガーにだけ出ることを確かめる。
func TestPhaseLinesGoToOperatorLogger(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.01, MinOps: 1})
	// prepareUsers が cohort のユーザーでログインできるよう、偽のアプリにも登録しておく。
	for _, region := range geo.Regions {
		for _, du := range runner.initial.CohortUsers(region) {
			app.addUser(du.ID, du.Username, du.SecretKey, du.Region)
		}
	}
	var participant, operator bytes.Buffer
	runner.cfg.Logger = slog.New(slog.NewTextHandler(&participant, nil))
	runner.cfg.OperatorLogger = slog.New(slog.NewTextHandler(&operator, nil))
	// 初期化は専用の応答で成功させ、事前検証は偽のアプリでは失敗するが、phase 行は出る。
	initServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"language":"go"}`)
	}))
	defer initServer.Close()
	origAdmin := runner.admin
	runner.admin = client.New(initServer.URL)
	_ = runner.initialize(t.Context())
	runner.admin = origAdmin
	_ = runner.pretest(t.Context())
	if reason := runner.prepareUsers(t.Context()); reason != "" {
		t.Fatalf("prepare users: %s", reason)
	}
	// 偽のアプリは初期データを持たないので検証自体は失敗するが、checked のログは出る。
	_ = runner.checkInitialData(t.Context())
	_ = runner.finalCheck(t.Context())
	for _, msg := range []string{`msg="phase: initialize"`, `msg="phase: pretest"`, `msg="phase: prepare users"`, `msg="phase: initial data"`, `msg="prepare users"`, `msg="initial user checked"`, `msg="phase: final check"`, `msg="final check done"`} {
		if strings.Contains(participant.String(), msg) {
			t.Fatalf("%s went to the participant logger: %s", msg, participant.String())
		}
		if !strings.Contains(operator.String(), msg) {
			t.Fatalf("%s missing from the operator logger: %s", msg, operator.String())
		}
	}
}

// TestEvilEnvLoggerIsOperatorLogger は、不正な利用者の通信記録が参加者向けロガーに
// 出ず、運営向けロガーにだけ出ることを確かめる。
func TestEvilEnvLoggerIsOperatorLogger(t *testing.T) {
	var participant, operator bytes.Buffer
	runner, err := NewRunner(Config{
		Target:         "http://127.0.0.1:1",
		Users:          1,
		Duration:       time.Second,
		Dataset:        tinyDataset,
		Logger:         slog.New(slog.NewTextHandler(&participant, nil)),
		OperatorLogger: slog.New(slog.NewTextHandler(&operator, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.evilEnv(time.Now()).Logger.Info("不正な利用者の通信")
	if strings.Contains(participant.String(), "不正な利用者の通信") {
		t.Fatalf("evil log went to the participant logger: %s", participant.String())
	}
	if !strings.Contains(operator.String(), "不正な利用者の通信") {
		t.Fatalf("evil log missing from the operator logger: %s", operator.String())
	}
}

// TestTellIsSimplePopulation は、日ごとの Story が理由の説明を持たない人数だけの
// 形（asia 1 日目: 19 人）になることを確かめる。
func TestTellIsSimplePopulation(t *testing.T) {
	var operator bytes.Buffer
	runner := &Runner{
		cfg: Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OperatorLogger: slog.New(slog.NewTextHandler(&operator, nil))},
		tt:  newTimetable(time.Minute),
	}
	runner.tell(geo.Asia, 1, &dayStats{population: 19}, 0)
	// 投稿期間を開くのに時間がかかっても、Story には人数だけを出し、所要時間は運営向けにだけ残す。
	runner.tell(geo.Asia, 2, &dayStats{population: 19, newcomers: 1}, 2*time.Second)
	// 前日が不調でも、理由は Story に出さない（運営向けの DayReport に残る）。
	runner.tell(geo.Asia, 3, &dayStats{population: 20, healthy: false, failRate: 0.5}, 0)
	runner.tell(geo.Asia, 6, &dayStats{population: 25}, 0)
	want := []string{
		"asia 1 日目: 19 人",
		"asia 2 日目: 20 人",
		"asia 3 日目: 20 人",
		"asia 最終日（グランプリ）: 25 人",
	}
	if got := runner.storyLines(); !slices.Equal(got, want) {
		t.Fatalf("story = %q, want %q", got, want)
	}
	if !strings.Contains(operator.String(), "投稿期間を開くのに時間がかかりました") {
		t.Fatalf("slow open missing from the operator logger: %s", operator.String())
	}
}

// TestTellGrandPrixHasNoTotal は、グランプリの Story が合計を出さず、地域ごとの
// 人数だけ（グランプリ: アジア 2 人, ヨーロッパ 1 人, アメリカ 4 人）になることを確かめる。
func TestTellGrandPrixHasNoTotal(t *testing.T) {
	runner := &Runner{
		cfg: Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		tt:  newTimetable(time.Minute),
	}
	book := newBook(runner.tt, time.Now())
	runner.books.Store(book)
	gp := runner.tt.days()
	for _, id := range []string{"a1", "a2"} {
		book.pass(geo.Asia, gp, id, 0)
	}
	book.pass(geo.Europe, gp, "e1", 0)
	for _, id := range []string{"m1", "m2", "m3", "m4"} {
		book.pass(geo.America, gp, id, 0)
	}
	runner.tellGrandPrix()
	want := []string{"グランプリ: アジア 2 人, ヨーロッパ 1 人, アメリカ 4 人"}
	if got := runner.storyLines(); !slices.Equal(got, want) {
		t.Fatalf("story = %q, want %q", got, want)
	}
}

// TestResultMessagesAreErrorsOnly は、結果 JSON の messages に Story（日ごとの人数）が
// 入らず、エラー系（理由・Critical・Errors）だけが入ることを確かめる。
func TestResultMessagesAreErrorsOnly(t *testing.T) {
	runner := &Runner{
		board: score.NewBoard(nil),
		story: []string{"asia 1 日目: 19 人", "グランプリ: アジア 2 人, ヨーロッパ 1 人, アメリカ 4 人"},
	}
	runner.board.Criticalf("見えてはいけない投稿が返りました")
	runner.board.Errorf("フィードを取得できません")
	want := []string{"失格の理由", "見えてはいけない投稿が返りました", "フィードを取得できません"}
	if got := runner.resultMessages("失格の理由"); !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
	if got := runner.resultMessages(""); len(got) != 2 {
		t.Fatalf("messages without reason = %q, want only critical and errors", got)
	}
}

// setDuration は走行の長さと時間割を変える。
func (r *Runner) setDuration(d time.Duration) {
	r.cfg.Duration = d
	r.tt = newTimetable(d)
}

func TestLoadAllRegions(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.01, MinOps: 1})
	if reason := runner.load(t.Context()); reason != "" {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	if runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("errors=%v critical=%v", runner.board.Errors(), runner.board.Critical())
	}
	// 各リージョンが時間割どおりに 6 日（最後はグランプリ）の投稿期間を開く。
	if opened := runner.openedCounts(); opened[geo.Asia] != 6 || opened[geo.Europe] != 6 || opened[geo.America] != 6 {
		t.Fatalf("opened=%v", opened)
	}
	if got := runner.board.Count(score.TagGatePass); got < 6 {
		t.Fatalf("gate passes=%d", got)
	}
	if runner.board.Count(score.TagGrandPrix) == 0 || runner.board.Count(score.TagFeed) == 0 || runner.board.Count(score.TagMemory) == 0 {
		t.Fatalf("breakdown=%v", runner.board.Breakdown())
	}
	if reason := runner.finalCheck(t.Context()); reason != "" {
		t.Fatalf("final check: %s", reason)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if len(app.friends) < 3 {
		t.Fatalf("friends=%d", len(app.friends))
	}
}

// TestGrowth は、前日にゲートを通過した人数に応じて新しいユーザーが来て、登録して投稿し、
// 友達申請やフォローをすることを確かめる。
func TestGrowth(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.01, MinOps: 1})
	if reason := runner.load(t.Context()); reason != "" || runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	grew := false
	for _, day := range runner.books.Load().report() {
		if day.Newcomers > 0 {
			grew = true
		}
	}
	if !grew {
		t.Fatalf("no newcomers: %+v", runner.books.Load().report())
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	newcomerPosts := 0
	for _, p := range app.posts {
		if !strings.HasPrefix(p.Author.ID, "u-asia") && !strings.HasPrefix(p.Author.ID, "u-europe") && !strings.HasPrefix(p.Author.ID, "u-america") {
			newcomerPosts++
		}
	}
	if newcomerPosts == 0 || len(app.requests) == 0 {
		t.Fatalf("newcomer posts=%d requests=%d", newcomerPosts, len(app.requests))
	}
}

// TestModelAgreesWithApp は、削除と公開範囲の変更を混ぜて何度も投稿期間をまたいでも、
// モデルが偽のアプリ（別の実装）と食い違わないことを確かめる。
func TestModelAgreesWithApp(t *testing.T) {
	runner, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	app.pageSize = 2 // ページ送りも通す
	runner.model.PageSize = 2
	runner.setDuration(2 * time.Second)
	runner.deleteRate, runner.changeRate = 0.2, 0.3
	if reason := runner.load(t.Context()); reason != "" || runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	if runner.board.Count(score.TagGatePass) == 0 {
		t.Fatal("no gate passes")
	}
}

func TestIgnoredBlockIsCritical(t *testing.T) {
	runner, app := newTestRunner(t, 4, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.visibility = func() string { return "public" }
	// ブロックした人がブロックされた人の投稿を読む機会が確実にあるよう、長めに走らせ、
	// 新しいユーザーは来させない（投稿が最初のページからあふれる）。
	runner.setDuration(2 * time.Second)
	runner.cfg.Growth.FastInvites, runner.cfg.Growth.QuickInvites = 0, 0
	app.ignoreBlocks = true
	reason := runner.load(t.Context())
	critical := runner.board.Critical()
	if len(critical) == 0 || !strings.Contains(strings.Join(critical, "\n"), "見えてはいけない") {
		t.Fatalf("reason=%q critical=%v", reason, critical)
	}
}

func TestUnknownFeedPostIsCritical(t *testing.T) {
	runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	app.extraFeed = []client.Post{{ID: "not-created-by-bench", Author: client.UserSummary{ID: "stranger"}}}
	runner.load(t.Context())
	critical := runner.board.Critical()
	if len(critical) == 0 || !strings.Contains(critical[0], "存在しない投稿") {
		t.Fatalf("critical=%v", critical)
	}
}

func TestNotificationFaults(t *testing.T) {
	t.Run("missing notification skips posting", func(t *testing.T) {
		runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		app.notify = func(string, string) string { return "" }
		runner.load(t.Context())
		if runner.board.ErrorCount() != 0 || app.postCount() != 0 {
			t.Fatalf("errors=%v posts=%d", runner.board.Errors(), app.postCount())
		}
	})
	t.Run("unknown window is an error", func(t *testing.T) {
		runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		app.notify = func(region, _ string) string {
			return fmt.Sprintf(`{"id":"n1","postingWindowId":"wrong","region":%q}`, region)
		}
		runner.load(t.Context())
		errors := runner.board.Errors()
		if len(errors) < 3 || !strings.Contains(errors[0], "開いていない投稿期間") || app.postCount() != 0 {
			t.Fatalf("errors=%v posts=%d", errors, app.postCount())
		}
	})
}

func TestLoadAbortsOnErrorRate(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.1, MinOps: 1})
	app.postStatus = http.StatusServiceUnavailable
	reason := runner.load(t.Context())
	if !strings.Contains(reason, "エラー率") {
		t.Fatalf("reason=%q", reason)
	}
	if runner.board.Count(score.TagGatePass) != 0 {
		t.Fatal("gate passed without a post")
	}
}

// TestBannedUserIsReputation は、正常なユーザーが BAN されると、そのユーザーは止まり、
// 悪評として記録され、失敗には数えないことを確かめる。
func TestBannedUserIsReputation(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 0.01, MinOps: 1})
	victim := runner.users[geo.Asia][0]
	app.mu.Lock()
	app.banned[victim.API.ClientIP()] = true
	app.mu.Unlock()
	if reason := runner.load(t.Context()); reason != "" {
		t.Fatalf("reason=%q errors=%v", reason, runner.board.Errors())
	}
	if !victim.stopped.Load() || runner.reputations() != 1 {
		t.Fatalf("stopped=%t reputation=%d", victim.stopped.Load(), runner.reputations())
	}
	// BAN の悪評は参加者向けの Story に出す。
	if !strings.Contains(strings.Join(runner.storyLines(), "\n"), "悪評が立ちました") {
		t.Fatalf("reputation missing from story: %v", runner.storyLines())
	}
	if runner.board.ErrorCount() != 0 {
		t.Fatalf("errors=%v", runner.board.Errors())
	}
}

func TestFinalCheckFindsInconsistencies(t *testing.T) {
	for name, tc := range map[string]struct {
		breakApp func(*fakeApp)
		want     string
	}{
		"deleted post kept":  {func(a *fakeApp) { a.keepDeleted = true }, "削除した投稿"},
		"accepted post lost": {func(a *fakeApp) { a.losePosts = true }, "受け付けられた投稿"},
	} {
		t.Run(name, func(t *testing.T) {
			runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
			runner.cfg.Growth.FastInvites, runner.cfg.Growth.QuickInvites = 0, 0
			runner.deleteRate = 1 // 全員が投稿を削除する
			if name == "accepted post lost" {
				runner.deleteRate = 0
			}
			runner.load(t.Context())
			app.mu.Lock()
			tc.breakApp(app)
			app.mu.Unlock()
			if reason := runner.finalCheck(t.Context()); !strings.Contains(reason, tc.want) {
				t.Fatalf("reason=%q", reason)
			}
		})
	}
}

func TestCanceledRunFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner, err := NewRunner(Config{Target: "http://127.0.0.1:1", Users: 1, Dataset: tinyDataset, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(ctx)
	if result.Pass || !strings.Contains(result.Reason, "中断") || len(result.Messages) == 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestConfiguration(t *testing.T) {
	for _, cfg := range []Config{{Users: 0}, {Users: 1, Duration: -time.Second}, {Users: 1, VerifyRate: -0.1}, {Users: 1, VerifyRate: 1.5}} {
		if _, err := NewRunner(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
	runner, err := NewRunner(Config{Users: 1, Dataset: tinyDataset})
	if err != nil || runner.verifyRate != DefaultVerifyRate || runner.cfg.Growth != DefaultGrowth || runner.seed == 0 {
		t.Fatalf("verifyRate=%v growth=%+v seed=%d err=%v", runner.verifyRate, runner.cfg.Growth, runner.seed, err)
	}
}

// TestSampleWeight は、重い検証を verifyRate の割合で選び、選んだ検証の失敗を割合の逆数の
// 件数として数えることを確かめる。
func TestSampleWeight(t *testing.T) {
	runner := &Runner{verifyRate: 0.25}
	sampled := 0
	for range 10000 {
		switch w := runner.sample(); w {
		case 0:
		case 4:
			sampled++
		default:
			t.Fatalf("weight=%d, want 0 or 4", w)
		}
	}
	if sampled < 2200 || sampled > 2800 {
		t.Fatalf("sampled %d of 10000, want about 2500", sampled)
	}
	runner.verifyRate = 1
	if w := runner.sample(); w != 1 {
		t.Fatalf("weight=%d with rate 1, want 1", w)
	}
}

// TestSampledMissingPostsKeepErrorRate は、欠落の確認を一部の読み取りだけで行うとき、
// 見つけた欠落を割合の逆数の件数としてエラー率に数えることを確かめる。
func TestSampledMissingPostsKeepErrorRate(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.verifyRate = 0.1
	runner.visibility = func() string { return "public" }
	runner.cfg.Growth.FastInvites, runner.cfg.Growth.QuickInvites = 0, 0
	app.dropFeeds = true
	runner.load(t.Context())
	var failures int64
	rec := runner.recorder.Load()
	for _, window := range rec.Windows() {
		for _, stats := range rec.Regions(window) {
			failures += stats.Failures
		}
	}
	missing := 0
	for _, message := range runner.board.Errors() {
		if strings.Contains(message, "見えるべき投稿") {
			missing++
		}
	}
	if missing == 0 || failures < int64(10*missing) {
		t.Fatalf("failures=%d missing=%d, want each missing post counted 10 times", failures, missing)
	}
}

func TestTimetable(t *testing.T) {
	tt := newTimetable(60 * time.Second)
	for _, tc := range []struct {
		region string
		day    int
		start  time.Duration
	}{
		{geo.Asia, 1, 0}, {geo.Europe, 1, 3 * time.Second}, {geo.America, 1, 6 * time.Second},
		{geo.Asia, 5, 36 * time.Second}, {geo.America, 5, 42 * time.Second},
		{geo.Asia, 6, 51 * time.Second}, {geo.America, 6, 51 * time.Second},
	} {
		if got := tt.start(tc.region, tc.day); got != tc.start {
			t.Errorf("start(%s, %d)=%v, want %v", tc.region, tc.day, got, tc.start)
		}
	}
	if !tt.isGrandPrix(6) || tt.isGrandPrix(5) || tt.dayAt(geo.Europe, 2*time.Second) != 0 || tt.dayAt(geo.Europe, 50*time.Second) != 5 || tt.end(geo.Asia, 6) != 60*time.Second {
		t.Fatal("unexpected timetable")
	}
	if d := tt.postDelay(0.5); d < 900*time.Millisecond || d > 1100*time.Millisecond {
		t.Fatalf("median delay=%v, want about 1s", d)
	}
	if d := tt.postDelay(0.999999); d != 8*time.Second {
		t.Fatalf("delay cap=%v, want 8s", d)
	}
}

func TestBookDecide(t *testing.T) {
	tt := newTimetable(60 * time.Second)
	b := newBook(tt, time.Now())
	// 1 日目: 8 人が快適に（2 秒以内に）、2 人が遅れて通過し、2 人は通過できなかった。
	for i := range 10 {
		took := time.Second
		if i >= 8 {
			took = 5 * time.Second
		}
		b.pass(geo.Asia, 1, fmt.Sprint(i), took)
	}
	b.stats(geo.Asia, 1).ops, b.stats(geo.Asia, 1).failures = 50, 1
	// 8 × 1.5 − 2 × 1 = 10 人。失敗が 1 件でも、操作が MinOps に満たなければ健全とみなす。
	if s := b.decide(DefaultGrowth, geo.Asia, 2, 12); !s.healthy || s.newcomers != 10 || s.quick != 8 || s.missed != 2 {
		t.Fatalf("healthy=%t newcomers=%d quick=%d missed=%d, want 10, 8 and 2", s.healthy, s.newcomers, s.quick, s.missed)
	}
	// 通過できなかった人が多ければ、不満が誘いを打ち消して誰も来ない。
	if s := b.decide(DefaultGrowth, geo.Asia, 2, 30); !s.healthy || s.newcomers != 0 || s.missed != 20 {
		t.Fatalf("healthy=%t newcomers=%d missed=%d, want 0 and 20", s.healthy, s.newcomers, s.missed)
	}
	// 失敗率が 1% を超えた日の翌日は誰も来ない。
	b.stats(geo.Asia, 2).ops, b.stats(geo.Asia, 2).failures = 1000, 20
	for i := range 30 {
		b.pass(geo.Asia, 2, fmt.Sprint(i), time.Second)
	}
	if s := b.decide(DefaultGrowth, geo.Asia, 3, 30); s.healthy || s.newcomers != 0 {
		t.Fatalf("healthy=%t newcomers=%d, want none after a 2%% failure day", s.healthy, s.newcomers)
	}
	if b.pass(geo.Asia, 2, "0", time.Second) {
		t.Fatal("the same user passed twice in a day")
	}
	// 最終日（グランプリ）には新しい人は来ない。
	for i := range 30 {
		b.pass(geo.Asia, 5, fmt.Sprint(i), time.Second)
	}
	if s := b.decide(DefaultGrowth, geo.Asia, 6, 30); !s.healthy || s.newcomers != 0 || s.quick != 30 {
		t.Fatalf("grand prix: healthy=%t newcomers=%d quick=%d", s.healthy, s.newcomers, s.quick)
	}
	report := b.report()
	if report[0].GateP50 != time.Second || report[0].GateP90 != 5*time.Second {
		t.Fatalf("gate p50=%v p90=%v", report[0].GateP50, report[0].GateP90)
	}
}

func TestIPPool(t *testing.T) {
	pool := newIPPool(1)
	seen := map[string]bool{}
	for range 10000 {
		ip := pool.Next()
		addr, err := netip.ParseAddr(ip)
		if err != nil || seen[ip] || addr.IsPrivate() || addr.IsLoopback() || addr.IsMulticast() || addr.IsUnspecified() {
			t.Fatalf("bad or repeated ip %s", ip)
		}
		seen[ip] = true
	}
}

// TestPretestRules は、事前検証が地域外のアクセス、画像の形式、署名と期限の拒否を確かめ、
// 拒否を省く実装を見逃さないことを確かめる。
func TestPretestRules(t *testing.T) {
	runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	a := runner.users[geo.Asia][0].Agent
	if reason := runner.pretestRules(t.Context(), a); reason != "" {
		t.Fatalf("strict app: %s", reason)
	}
	app.mu.Lock()
	app.lax = true
	app.mu.Unlock()
	if reason := runner.pretestRules(t.Context(), a); !strings.Contains(reason, "地域外") {
		t.Fatalf("lax app: reason=%q", reason)
	}
}

// TestIgnoredGateIsCritical は、投稿する前にフィードを覗くユーザーが、今日投稿していない
// 人に投稿を見せる実装を見逃さないことを確かめる。
func TestIgnoredGateIsCritical(t *testing.T) {
	runner, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.peekRate = 1
	app.ignoreGate = true
	runner.load(t.Context())
	critical := runner.board.Critical()
	if len(critical) == 0 || !strings.Contains(strings.Join(critical, "\n"), "見えてはいけない") {
		t.Fatalf("critical=%v", critical)
	}
}

// TestInvites は、通過までの時間が短いほど多くの友達を誘う折れ線を確かめる（1 日 9 秒）。
func TestInvites(t *testing.T) {
	period := 9 * time.Second
	for _, tc := range []struct {
		took time.Duration
		want float64
	}{
		{100 * time.Millisecond, 2}, {250 * time.Millisecond, 2}, {500 * time.Millisecond, 1.8333},
		{time.Second, 1.5}, {1500 * time.Millisecond, 0.75}, {2 * time.Second, 0}, {5 * time.Second, 0},
	} {
		if got := DefaultGrowth.invites(tc.took, period); math.Abs(got-tc.want) > 0.001 {
			t.Errorf("invites(%v)=%.4f, want %.4f", tc.took, got, tc.want)
		}
	}
}

// TestOpenWindowRecovery は、投稿期間を開く要求の応答が遅れても、今の投稿期間を読んで
// その日を続け、アプリが応答しなければ次の日の始まりまでに諦めることを確かめる。
func TestOpenWindowRecovery(t *testing.T) {
	t.Run("late response", func(t *testing.T) {
		runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		var operator bytes.Buffer
		runner.cfg.OperatorLogger = slog.New(slog.NewTextHandler(&operator, nil))
		runner.setDuration(18 * time.Second) // 1 日 2.7 秒、開く要求は 2.4 秒で打ち切る
		runner.books.Store(newBook(runner.tt, time.Now()))
		app.mu.Lock()
		app.openDelay = 3 * time.Second
		app.mu.Unlock()
		id, err := runner.openDay(t.Context(), geo.Asia, 1)
		if err != nil || id == "" || !runner.model.HasWindow(id) || runner.board.ErrorCount() != 0 {
			t.Fatalf("id=%q err=%v errors=%v", id, err, runner.board.Errors())
		}
		// 回復できたことは参加者向けの Story に出さず、運営向けにだけ残す。
		if strings.Contains(strings.Join(runner.story, "\n"), "開いていることを確かめました") {
			t.Fatalf("recovery leaked to story: %v", runner.story)
		}
		if !strings.Contains(operator.String(), "開いていることを確かめました") {
			t.Fatalf("recovery missing from the operator logger: %s", operator.String())
		}
	})
	t.Run("hung app", func(t *testing.T) {
		runner, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		runner.setDuration(18 * time.Second)
		start := time.Now()
		runner.books.Store(newBook(runner.tt, start))
		app.mu.Lock()
		app.hang = true
		app.mu.Unlock()
		id, _ := runner.openDay(t.Context(), geo.Asia, 1)
		if took, end := time.Since(start), runner.tt.end(geo.Asia, 1); id != "" || took > end+500*time.Millisecond {
			t.Fatalf("id=%q took=%v, want to give up by the end of the day (%v)", id, took, end)
		}
		if runner.board.ErrorCount() == 0 {
			t.Fatal("no error for a window that could not be opened")
		}
	})
}

// 退会の応答が届かなくても退会は済んでいることがある。そのユーザーを走行後の整合性チェックで
// 確かめると、正しいアプリでも 401 で fail になる。
func TestUncertainDeletionStopsUser(t *testing.T) {
	runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
	app.mu.Lock()
	app.deleteFails = true
	app.mu.Unlock()
	u := runner.usersSnapshot()[0]
	runner.deleteAccount(t.Context(), u)
	if !u.stopped.Load() {
		t.Fatal("a user whose deletion may have succeeded is still used")
	}
	if reason := runner.finalCheck(t.Context()); reason != "" {
		t.Fatalf("final check: %s", reason)
	}
}

// アプリを閉じたあと、ログインし直す前に走行が終わったユーザーはトークンを持たない。
// そのユーザーを走行後の整合性チェックで確かめると、正しいアプリでも 401 で fail になる。
func TestLoggedOutUserSkippedInFinalCheck(t *testing.T) {
	runner, _ := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
	for _, u := range runner.usersSnapshot() {
		runner.mu.Lock()
		u.Token = ""
		runner.mu.Unlock()
	}
	if reason := runner.finalCheck(t.Context()); reason != "" {
		t.Fatalf("final check: %s", reason)
	}
}

// 友達を解除する間に相手が退会すれば、アプリは 404 を返す（webapp/go の beginRelationshipTx）。
// 退会の結果がわからない間の 404 は正しく、確実に有効な相手への 404 は違反とする。
func TestUnfriendDuringDeletion(t *testing.T) {
	for _, deleting := range []bool{true, false} {
		runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
		users := runner.usersSnapshot()
		u := users[0]
		var friends []*user
		for _, other := range users[1:] {
			w := runner.model.BeginFriendship(u.ID, other.ID, true)
			w.Applied()
			friends = append(friends, other)
		}
		app.mu.Lock()
		for _, other := range friends {
			app.friends[key(u.ID, other.ID)] = true
			app.users[other.ID].deleted = true
		}
		app.mu.Unlock()
		if deleting {
			for _, other := range friends {
				runner.model.BeginDeleteAccount(other.ID)
			}
		}
		runner.unfriend(t.Context(), u)
		if got := runner.board.ErrorCount(); (got != 0) == deleting {
			t.Fatalf("deleting=%v: errors=%v", deleting, runner.board.Errors())
		}
	}
}

// ブロックも退会と重なれば 404 が正しい。退会を終えた人はブロックの相手に選ばない。
func TestBlockDuringDeletion(t *testing.T) {
	for _, state := range []string{"deleting", "deleted", "active-in-model"} {
		runner, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
		runner.model.Staleness = 0
		var u, target *user
		for _, c := range runner.usersSnapshot() {
			if c.Region == geo.Asia {
				if u == nil {
					u = c
				} else {
					target = c
				}
			}
		}
		runner.model.BeginFriendship(u.ID, target.ID, false).Applied()
		requested := false
		app.mu.Lock()
		delete(app.friends, key(u.ID, target.ID))
		app.onBlock = func(string) { requested = true }
		switch state {
		case "deleting":
			// 相手は要求を送ったあとで退会を始め、アプリでは先に済む。
			app.onBlock = func(string) {
				requested = true
				runner.model.BeginDeleteAccount(target.ID)
				app.users[target.ID].deleted = true
			}
		case "deleted":
			app.users[target.ID].deleted = true
		case "active-in-model":
			app.users[target.ID].deleted = true
		}
		app.mu.Unlock()
		if state == "deleted" {
			runner.model.BeginDeleteAccount(target.ID).Applied()
		}
		runner.toggleBlock(t.Context(), u)
		if requested != (state != "deleted") {
			t.Fatalf("%s: requested=%v", state, requested)
		}
		wantErrors := state == "active-in-model"
		if got := runner.board.ErrorCount(); (got != 0) != wantErrors {
			t.Fatalf("%s: errors=%v", state, runner.board.Errors())
		}
		if len(u.blocking) != 0 {
			t.Fatalf("%s: blocked a user who had left: %v", state, u.blocking)
		}
	}
}

// TestAwaitPhotosAfterInitialize は、写真のアーカイブを初期化のあと、対象に写真を送る前に
// 待ち、読めなかったときはベンチマーカー側の失敗として止めることを確かめる。
func TestAwaitPhotosAfterInitialize(t *testing.T) {
	var initialized, others atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/initialize" {
			initialized.Add(1)
			_, _ = w.Write([]byte(`{"language":"go"}`))
			return
		}
		others.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	awaited := false
	runner, err := NewRunner(Config{
		Target: server.URL, Users: 1, Dataset: tinyDataset,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AwaitPhotos: func(context.Context) (*photo.Set, error) {
			awaited = true
			if initialized.Load() != 1 {
				t.Errorf("initialize calls before awaiting photos = %d, want 1", initialized.Load())
			}
			return nil, errors.New("archive unreachable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(t.Context())
	if !awaited || result.Pass || !strings.Contains(result.Reason, "写真のアーカイブ") {
		t.Fatalf("awaited=%v result=%+v", awaited, result)
	}
	if n := others.Load(); n != 0 {
		t.Fatalf("requests other than initialize before the photos were ready = %d", n)
	}
}

// TestUsersMoveBetweenRegions は、ユーザーが日を過ごし終えたあとで所属リージョンを変えても、
// 移った先の国から使い続け、モデルが偽のアプリと食い違わないことを確かめる。
func TestUsersMoveBetweenRegions(t *testing.T) {
	runner, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.setDuration(2 * time.Second)
	runner.moveRate = 1
	if reason := runner.load(t.Context()); reason != "" || runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	moves := 0
	for _, n := range runner.moves {
		moves += n
	}
	if moves == 0 {
		t.Fatal("no user moved")
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	for _, u := range runner.usersSnapshot() {
		if fake := app.users[u.Token]; fake != nil && !u.cohort && fake.region != u.Region {
			t.Errorf("%s: bench thinks %s, app has %s", u.Username, u.Region, fake.region)
		}
	}
}

// TestUsersSignOffOvernight は、日の終わりにログアウトして次の日にログインし直すユーザーが、
// 通知を待たずに投稿してエラーにならないことを確かめる。
func TestUsersSignOffOvernight(t *testing.T) {
	runner, _ := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.setDuration(2 * time.Second)
	runner.signOffRate = 1
	if reason := runner.load(t.Context()); reason != "" || runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	if runner.board.Count(score.TagGatePass) == 0 {
		t.Fatal("no gate passes")
	}
}

// TestUncertainMoveIsReconciled は、所属リージョンの変更が反映されたのに応答がエラーだった
// とき、同じ変更を送り直して移った先のリージョンから使い続ける（前のリージョンから送り続けない）
// ことを確かめる。偽のアプリは、所属と違う国からの要求でテストを失敗させる。
func TestUncertainMoveIsReconciled(t *testing.T) {
	runner, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.setDuration(2 * time.Second)
	runner.moveRate = 1
	app.mu.Lock()
	app.flakyMoves = 4
	app.mu.Unlock()
	runner.load(t.Context())
	for _, e := range runner.board.Errors() {
		if !strings.Contains(e, "所属リージョンを") {
			t.Errorf("unexpected error: %s", e)
		}
	}
	if runner.board.CriticalCount() != 0 {
		t.Fatalf("critical=%v", runner.board.Critical())
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.flakyMoves != 0 {
		t.Fatalf("flaky moves left: %d", app.flakyMoves)
	}
	for _, u := range runner.usersSnapshot() {
		if fake := app.users[u.Token]; fake != nil && !u.cohort && !u.stopped.Load() && fake.region != u.Region {
			t.Errorf("%s: bench thinks %s, app has %s", u.Username, u.Region, fake.region)
		}
	}
}

// TestRejectedRetryAfterAppliedMove は、反映されたのに 500 だった変更のあとで送り直しが拒否
// されても、前のリージョンに戻ったとみなさず、そのユーザーを止めて使えなかった人として数え
// 続けることを確かめる。偽のアプリは、所属と違う国からの要求でテストを失敗させる。
func TestRejectedRetryAfterAppliedMove(t *testing.T) {
	runner, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	runner.setDuration(2 * time.Second)
	runner.moveRate = 1
	app.mu.Lock()
	app.moveReplies = []int{http.StatusInternalServerError, http.StatusConflict}
	app.mu.Unlock()
	runner.load(t.Context())
	for _, e := range runner.board.Errors() {
		if !strings.Contains(e, "所属リージョンを") {
			t.Errorf("unexpected error: %s", e)
		}
	}
	stranded := 0
	for _, u := range runner.usersSnapshot() {
		if u.stranded.Load() {
			stranded++
			if !u.stopped.Load() {
				t.Errorf("%s: stranded but still running", u.Username)
			}
		}
	}
	if stranded != 1 {
		t.Fatalf("stranded users = %d, want 1", stranded)
	}
}

// TestUsersMakeFriendsOverRun は、登録したあとも毎日の投稿のあとやフィードで見た人に友達申請を
// 送り、走行の終わりの友達の数を数えられることを確かめる。
func TestUsersMakeFriendsOverRun(t *testing.T) {
	runner, _ := newTestRunner(t, 4, metrics.SLO{FailRate: 1000, MinOps: 1})
	if reason := runner.load(t.Context()); reason != "" || runner.board.ErrorCount() != 0 || runner.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q errors=%v critical=%v", reason, runner.board.Errors(), runner.board.Critical())
	}
	stats := runner.friendStats()
	if len(stats) == 0 {
		t.Fatal("no friend stats")
	}
	discoveries := 0
	for region, s := range stats {
		t.Logf("%s: %+v", region, s)
		if s.FriendsMax <= 2 || s.FoFAvg == 0 {
			t.Errorf("%s: %+v", region, s)
		}
		discoveries += s.Discoveries
	}
	if discoveries == 0 {
		t.Error("no friend request from feeds")
	}
}
