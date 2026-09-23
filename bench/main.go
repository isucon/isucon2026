// Command bench runs ISUReal either standalone or as a supervisor child.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/isucon/isucon2026/bench/internal/benchrun"
	"github.com/isucon/isucon2026/bench/internal/scenario"
	pb "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

func main() {
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
	flags := flag.NewFlagSet("bench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	defaultTarget := os.Getenv("ISUXBENCH_TARGET")
	if defaultTarget == "" {
		defaultTarget = "http://localhost:8080"
	}
	target := flags.String("target", defaultTarget, "target URL or supervisor IP")
	host := flags.String("host", "", "HTTP Host header override (empty uses the target host)")
	port := flags.Int("target-port", 80, "HTTP port for targets without an explicit port")
	users := flags.Int("users", 8, "concurrent load users")
	cycles := flags.Int("cycles", 0, "fixed cycle count for development (0 = 60s load)")
	timeout := flags.Duration("timeout", 120*time.Second, "overall benchmark deadline; supervisor hard timeout must be longer")
	postValidation := flags.Bool("only-post-validation", false, "reserved: report unsupported without initializing the application")
	report := func(result scenario.Result, finished bool, reason string) error {
		if reporter == nil {
			return nil
		}
		p := &pb.BenchmarkResult{Finished: finished, Passed: finished && result.Pass, Score: result.Score,
			ScoreBreakdown: &pb.BenchmarkResult_ScoreBreakdown{Raw: result.RawScore, Deduction: result.Deduction},
			SurveyResponse: &pb.SurveyResponse{Language: result.Language}}
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
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return fail("invalid benchmark arguments", 2)
	}
	if flags.NArg() != 0 || *users < 1 || *cycles < 0 || *timeout <= 0 {
		return fail("invalid benchmark arguments", 2)
	}
	if *postValidation {
		return fail("post-validation is not implemented; application was not initialized", 1)
	}
	normalized, err := normalizeTarget(*target, *port)
	if err != nil {
		return fail("invalid benchmark target", 2)
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	var reportErr error
	runner, err := scenario.NewRunner(scenario.Config{Target: normalized, Host: *host, Users: *users, MaxCycles: *cycles, Logger: logger,
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
	if reportErr != nil {
		logger.Error("progress report failed", "error", reportErr)
		return 2
	}
	// Full diagnostics are operator-only under supervisor; stdout is visible to contestants.
	output := stdout
	if reporter != nil {
		output = stderr
	}
	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fail("encode result failed", 2)
	}
	reason := ""
	if !result.Pass {
		reason = "benchmark failed; contact the organizers for details"
	}
	if err := report(result, true, reason); err != nil {
		logger.Error("final report failed", "error", err)
		return 2
	}
	if reporter != nil {
		if err := json.NewEncoder(stdout).Encode(struct {
			Pass  bool  `json:"pass"`
			Score int64 `json:"score"`
		}{result.Pass, result.Score}); err != nil {
			logger.Error("write public result failed", "error", err)
			return 2
		}
	}
	if !result.Pass {
		return 1
	}
	return 0
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
