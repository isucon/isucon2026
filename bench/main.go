// Command bench は ISUReal のベンチマーク（初期化 → 事前検証 → 初期データの検証 → 負荷走行 →
// 走行後の整合性チェック）を実行する。単独でも、スーパーバイザーの子プロセスとしても動く。
// 単独では結果 JSON を標準出力へ出す。スーパーバイザーの下では途中経過と結果を報告用の
// パイプへ送り、詳しい結果（運営向け）は標準エラー出力、参加者に見える標準出力には進行ログと
// pass、score、参加者向けメッセージの JSON を出す。単独実行の終了コードは pass=0、fail=1、設定の誤りなど=2。
// スーパーバイザーの下では通常の fail も終了コード 0 とし、Passed=false で報告する。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/isucon/isucon2026/bench/internal/benchrun"
	"github.com/isucon/isucon2026/bench/internal/evil"
	"github.com/isucon/isucon2026/bench/internal/photo"
	"github.com/isucon/isucon2026/bench/internal/scenario"
	"github.com/isucon/isucon2026/dataset"
	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

// devTarget は Docker Compose の開発環境の入口。
const devTarget = "http://app.127.0.0.1.nip.io:18090"

// defaultGCPercent は GOGC を指定しないときの GC の目標。bench の常駐ヒープ（初期データと
// モデル）は数十 MB と小さく、既定の 100 では負荷走行で HTTP と JSON が割り当てるたびに GC が
// 走り、CPU を取られる。メモリには余裕があるので、GC の回数を減らす。
const defaultGCPercent = 400

func main() {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(defaultGCPercent)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(parent context.Context, args []string, stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	reporter, closer, err := benchrun.FromEnv()
	if err != nil {
		logger.Error("supervisor reporting setup failed", "error", err)
		return 2
	}
	if closer != nil {
		defer closer.Close()
	}
	progressLogger := logger
	if reporter != nil {
		progressLogger = slog.New(slog.NewTextHandler(stdout, nil))
	}
	flags := flag.NewFlagSet("bench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	defaultTarget := os.Getenv("ISUXBENCH_TARGET")
	if defaultTarget == "" {
		defaultTarget = devTarget
	}
	target := flags.String("target", defaultTarget, "target URL or supervisor IP")
	host := flags.String("host", "", "HTTP Host header override (empty uses the target host)")
	port := flags.Int("target-port", 80, "HTTP port for targets without an explicit port")
	users := flags.Int("users", 8, "initial load users per region (cohort users of the initial data join up to the same number)")
	images := flags.String("images", os.Getenv("ISUXBENCH_IMAGES"), "photo archive (zip path or http(s) URL) for posts; empty uses small generated images (env ISUXBENCH_IMAGES)")
	seed := flags.Uint64("seed", 0, "run seed (0 picks one at random)")
	evilUsers := flags.Bool("evil", true, "run the abusive users during the load phase")
	quickShare := flags.Float64("quick-share", scenario.DefaultGrowth.QuickShare, "share of a day at which a gate pass invites QuickInvites newcomers; the invite curve scales with it (calibration)")
	duration := flags.Duration("duration", 60*time.Second, "length of the load phase (shorter values are for development)")
	scale := flags.String("scale", "small", "scale of the initial data in the target DB: small or large (must match datagen -scale)")
	timeout := flags.Duration("timeout", 190*time.Second, "overall benchmark deadline; supervisor hard timeout must be longer")
	postValidation := flags.Bool("only-post-validation", false, "reserved: report unsupported without initializing the application")
	verifyRate := flags.Float64("verify-rate", scenario.DefaultVerifyRate, "fraction of load-phase operations that get heavy verification, in (0, 1] (env ISUXBENCH_VERIFY_RATE)")
	signOffRate := flags.Float64("signoff-rate", 0, "development: if positive, every user logs out after each day with this probability and logs in again the next day (default follows each user's persona)")
	moveRate := flags.Float64("move-rate", 0, "development: if positive, every registered user moves to another region with this probability after each day (default follows each user's persona)")
	cpuProfile := flags.String("cpuprofile", "", "write a CPU profile of the whole run to this file")
	memProfile := flags.String("memprofile", "", "write an allocation profile to this file at the end of the run")
	report := func(result scenario.Result, finished bool, reason string) error {
		if reporter == nil {
			return nil
		}
		// 減点はないので、素点がそのままスコアになる。
		p := &pb.BenchmarkResult{Finished: finished, Passed: finished && result.Pass, Score: result.Score,
			ScoreBreakdown: &pb.BenchmarkResult_ScoreBreakdown{Raw: result.Score}}
		if result.Language != "" {
			p.SurveyResponse = &pb.SurveyResponse{Language: result.Language, Agent: result.Agent, Harness: result.Harness, Model: result.Model}
		}
		if finished {
			p.Execution = &pb.BenchmarkResult_Execution{Reason: reason}
		}
		return reporter.Report(p)
	}
	fail := func(reason string, code int) int {
		logger.Error(reason)
		if err := report(scenario.Result{}, true, reason); err != nil {
			logger.Error("final report failed", "error", err)
			return 2
		}
		return code
	}
	if value := os.Getenv("ISUXBENCH_VERIFY_RATE"); value != "" {
		if err := flags.Set("verify-rate", value); err != nil {
			return fail("invalid ISUXBENCH_VERIFY_RATE", 2)
		}
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return fail("invalid benchmark arguments", 2)
	}
	datasetConfig, ok := map[string]func() dataset.Config{"small": dataset.Small, "large": dataset.Large}[*scale]
	if flags.NArg() != 0 || *users < 1 || *duration <= 0 || *timeout <= 0 || !ok || !(*verifyRate > 0 && *verifyRate <= 1) {
		return fail("invalid benchmark arguments", 2)
	}
	if !(*quickShare > 0 && *quickShare <= 1) {
		return fail("invalid benchmark arguments", 2)
	}
	growth := scenario.DefaultGrowth
	if scale := *quickShare / growth.QuickShare; scale != 1 {
		growth.FastShare *= scale
		growth.QuickShare *= scale
		growth.SlowShare *= scale
	}
	if *postValidation {
		return fail("post-validation is not implemented; application was not initialized", 1)
	}
	normalized, err := normalizeTarget(*target, *port)
	if err != nil {
		return fail("invalid benchmark target", 2)
	}
	if *cpuProfile != "" {
		stopProfile, err := startCPUProfile(*cpuProfile)
		if err != nil {
			logger.Error("cpu profile setup failed", "error", err)
			return fail("benchmarker setup failed", 2)
		}
		defer stopProfile()
	}
	if *memProfile != "" {
		defer writeAllocProfile(*memProfile, logger)
	}
	// 写真のアーカイブは初期化と並行して読み、初期化のあと、対象に写真を送る前に待つ。
	// 読めなかったときはベンチマーカー側の失敗として扱う。
	photos := photo.Generated()
	var awaitPhotos func(context.Context) (*photo.Set, error)
	var photoErr error
	if *images != "" {
		type loaded struct {
			set *photo.Set
			err error
		}
		done := make(chan loaded, 1)
		go func() {
			started := time.Now()
			set, err := photo.Load(parent, *images)
			if err == nil {
				logger.Info("photo archive loaded", "photos", set.Count(), "markable", set.Markable(), "elapsed", time.Since(started).Round(time.Millisecond))
			}
			done <- loaded{set: set, err: err}
		}()
		awaitPhotos = func(ctx context.Context) (*photo.Set, error) {
			select {
			case l := <-done:
				done <- l
				photoErr = l.err
				return l.set, l.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	var evilFunc scenario.EvilFunc
	if *evilUsers {
		evilFunc = evil.Run
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	var reportErr error
	runner, err := scenario.NewRunner(scenario.Config{
		Photos: photos, AwaitPhotos: awaitPhotos, Seed: *seed, Evil: evilFunc, Growth: growth,
		Target: normalized, Host: *host, Users: *users, Duration: *duration, VerifyRate: *verifyRate, MoveRate: *moveRate, SignOffRate: *signOffRate,
		Dataset: dataset.Generate(datasetConfig()), Logger: progressLogger,
		OnProgress: func(result scenario.Result) {
			if reportErr != nil {
				return
			}
			if err := report(result, false, ""); err != nil {
				reportErr = err
				cancel()
			}
		},
	})
	if err != nil {
		return fail("benchmarker setup failed", 2)
	}
	result := runner.Run(ctx)
	if photoErr != nil {
		logger.Error("photo archive", "error", photoErr)
		return fail("benchmarker setup failed: photo archive", 2)
	}
	if reportErr != nil {
		logger.Error("progress report failed", "error", reportErr)
		return 2
	}
	// スーパーバイザーの下では詳しい結果は運営だけが見る。標準出力は参加者に見える。
	output := stdout
	if reporter != nil {
		output = stderr
	}
	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fail("encode result failed", 2)
	}
	if err := report(result, true, reportReason(result.Reason)); err != nil {
		logger.Error("final report failed", "error", err)
		return 2
	}
	if reporter != nil {
		publicEncoder := json.NewEncoder(stdout)
		publicEncoder.SetIndent("", "  ")
		if err := publicEncoder.Encode(struct {
			Pass     bool     `json:"pass"`
			Score    int64    `json:"score"`
			Messages []string `json:"messages"`
		}{result.Pass, result.Score, result.Messages}); err != nil {
			logger.Error("write public result failed", "error", err)
			return 2
		}
	}
	// 非ゼロ終了はポータルで運営向け障害として扱われる。
	if reporter == nil && !result.Pass {
		return 1
	}
	return 0
}

// startCPUProfile は path へ CPU プロファイルを書き始め、止める関数を返す。
func startCPUProfile(path string) (func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}, nil
}

// writeAllocProfile は起動からの割り当てのプロファイルを path へ書く。
func writeAllocProfile(path string, logger *slog.Logger) {
	f, err := os.Create(path)
	if err != nil {
		logger.Error("memory profile failed", "error", err)
		return
	}
	defer f.Close()
	if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
		logger.Error("memory profile failed", "error", err)
	}
}

// reportReason は結果の要約を1行で返す。詳細は stdout の messages に残す。
func reportReason(reason string) string {
	if reason == "" {
		return "実行終了"
	}
	if end := strings.IndexAny(reason, "\r\n"); end >= 0 {
		reason = reason[:end]
	}
	summary, _, _ := strings.Cut(reason, ": ")
	return summary
}

func normalizeTarget(target string, port int) (string, error) {
	if port < 1 || port > 65535 || target == "" {
		return "", fmt.Errorf("invalid target or port")
	}
	if !strings.Contains(target, "://") {
		if net.ParseIP(target) != nil {
			target = net.JoinHostPort(target, strconv.Itoa(port))
		} else if _, _, err := net.SplitHostPort(target); err != nil {
			return "", fmt.Errorf("bare target must be an IP or host:port")
		}
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("invalid target URL")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid port")
		}
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}
