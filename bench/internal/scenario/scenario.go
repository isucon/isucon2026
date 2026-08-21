// Package scenario drives the benchmark phases: initialize → pretest → load.
// Phase structure follows spec-bench.md §5.2; the load model is the basic
// posting-cycle described in §3.3 (schedule → window → post → interact →
// advance to the next calendar day).
package scenario

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imagegen"
	"github.com/isucon/isucon2026/bench/internal/score"
	"github.com/isucon/isucon2026/bench/internal/vt"
)

const (
	adminUsername = "root"
	adminPassword = "admin-password"
	seedUsername  = "alice"
	seedPassword  = "password"

	benchUserPassword = "bench-password-1"
	benchUserRegion   = "asia"
	asiaUTCOffset     = 9 * time.Hour

	initializeTimeout = 30 * time.Second // INIT-10 provisional limit
	// loadDuration is fixed at 60 seconds like every past ISUCON (D-008,
	// docs/research/isucon-benchmarker-termination.md).
	loadDuration = 60 * time.Second
	// abortErrorThreshold aborts the load phase as fail once the total
	// error count reaches it (D-009, fixed-count style like ISUCON 11/12/14).
	abortErrorThreshold = 100
	scheduleLead        = 3 * time.Second // virtual delay until the window opens
	windowPollEvery     = 500 * time.Millisecond
	windowPollTimeout   = 20 * time.Second
	offsetSyncWait      = 2 * time.Second // D-006: wait for worker clock sync
	virtualDay          = 24 * time.Hour

	postImageSize       = 64
	realMojiImageSize   = 32
	interactionsPerUser = 2 // posts each user reacts to per cycle

	// 503 (e.g. image_sanitizer_unavailable) is documented as retryable and
	// is never recorded by the idempotency layer, so retrying is safe.
	retryAttempts  = 4
	retryBaseDelay = 300 * time.Millisecond
)

var reactionTypes = []string{"like", "heart", "laugh", "smile", "surprise", "instant"}

type Config struct {
	Target string
	Users  int
	// MaxCycles > 0 runs exactly that many posting cycles instead of the
	// fixed 60-second load duration, so repeated runs score identically.
	MaxCycles int
	Logger    *slog.Logger
}

type Result struct {
	Pass      bool             `json:"pass"`
	Score     int64            `json:"score"`
	Language  string           `json:"language"`
	BaseTime  time.Time        `json:"baseTime"`
	Cycles    int              `json:"cycles"`
	Breakdown map[string]int64 `json:"breakdown"`
	Errors    []string         `json:"errors"`
	Reason    string           `json:"reason,omitempty"`
}

type Runner struct {
	cfg         Config
	api         *client.Client
	clock       *vt.Clock
	board       *score.Board
	adminToken  string
	users       []client.User
	postImage   []byte
	mojiImage   []byte
	language    string
	baseTime    time.Time
	cycles      int
	abortReason string
}

func NewRunner(cfg Config) (*Runner, error) {
	postImage, err := imagegen.JPEG(postImageSize, postImageSize, 1)
	if err != nil {
		return nil, fmt.Errorf("generate post image: %w", err)
	}
	mojiImage, err := imagegen.JPEG(realMojiImageSize, realMojiImageSize, 2)
	if err != nil {
		return nil, fmt.Errorf("generate realmoji image: %w", err)
	}
	return &Runner{
		cfg:       cfg,
		api:       client.New(cfg.Target),
		clock:     vt.NewClock(),
		board:     score.NewBoard(),
		postImage: postImage,
		mojiImage: mojiImage,
	}, nil
}

// Run executes all phases. The returned Result is always usable; err is only
// non-nil for infrastructure-level failures that prevented a verdict.
func (r *Runner) Run(ctx context.Context) Result {
	if reason, ok := r.initialize(ctx); !ok {
		return r.failResult(reason)
	}
	if reason, ok := r.pretest(ctx); !ok {
		return r.failResult(reason)
	}
	r.load(ctx)
	if r.abortReason != "" {
		return r.failResult(r.abortReason)
	}
	return Result{
		Pass:      true,
		Score:     r.board.Total(),
		Language:  r.language,
		BaseTime:  r.baseTime,
		Cycles:    r.cycles,
		Breakdown: r.board.Breakdown(),
		Errors:    r.board.Errors(),
	}
}

func (r *Runner) failResult(reason string) Result {
	return Result{
		Pass:      false,
		Language:  r.language,
		BaseTime:  r.baseTime,
		Cycles:    r.cycles,
		Breakdown: r.board.Breakdown(),
		Errors:    r.board.Errors(),
		Reason:    reason,
	}
}

// --- phase 1: initialize (spec-bench.md §6) ---

func (r *Runner) initialize(ctx context.Context) (string, bool) {
	r.cfg.Logger.Info("phase: initialize")
	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	result, err := r.api.Initialize(initCtx)
	if err != nil {
		return fmt.Sprintf("initialize failed: %v", err), false
	}
	if result.Language == "" {
		return "initialize response has empty language (INIT-04)", false
	}
	if result.BaseTime.IsZero() {
		return "initialize response has zero base_time (VT-003)", false
	}
	r.language = result.Language
	r.baseTime = result.BaseTime
	r.clock.SetBase(result.BaseTime)
	r.cfg.Logger.Info("initialized", "language", result.Language, "baseTime", result.BaseTime.Format(time.RFC3339))
	// Give the worker time to pick up the reset virtual offset (D-006).
	sleepCtx(ctx, offsetSyncWait)
	return "", true
}

// --- phase 2: pretest (spec-bench.md §7) ---

func (r *Runner) pretest(ctx context.Context) (string, bool) {
	r.cfg.Logger.Info("phase: pretest")

	adminToken, err := r.api.AdminLogin(ctx, adminUsername, adminPassword)
	if err != nil {
		return fmt.Sprintf("admin login failed (PRE-004): %v", err), false
	}
	r.adminToken = adminToken

	seedUser, err := r.api.Login(ctx, seedUsername, seedPassword)
	if err != nil {
		return fmt.Sprintf("seeded user login failed: %v", err), false
	}

	if _, err := r.api.Login(ctx, seedUsername, "wrong-password-000"); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("login with wrong password: expected 401, got %v", err), false
	}

	checkUsername := "bench_pretest_user"
	registered, err := r.api.Register(ctx, checkUsername, benchUserPassword, "Pretest User", benchUserRegion)
	if err != nil {
		return fmt.Sprintf("register failed: %v", err), false
	}
	if _, err := r.api.Register(ctx, checkUsername, benchUserPassword, "Pretest User", benchUserRegion); !isStatus(err, http.StatusConflict) {
		return fmt.Sprintf("duplicate register: expected 409, got %v", err), false
	}

	profile, err := r.api.Me(ctx, registered.Token)
	if err != nil {
		return fmt.Sprintf("GET /me failed: %v", err), false
	}
	if profile.Username != checkUsername || profile.Region != benchUserRegion {
		return fmt.Sprintf("GET /me returned username=%q region=%q, want %q/%q", profile.Username, profile.Region, checkUsername, benchUserRegion), false
	}

	// FR-012: before posting, the feed must not expose other posts.
	feed, err := r.api.Feed(ctx, seedUser.Token, "public", "")
	if err != nil {
		return fmt.Sprintf("feed before posting failed: %v", err), false
	}
	if len(feed.Items) != 0 {
		return fmt.Sprintf("feed before posting: expected 0 items (FR-012), got %d", len(feed.Items)), false
	}

	r.cfg.Logger.Info("pretest passed")
	return "", true
}

// --- phase 3: load (spec-bench.md §8, basic cycle) ---

func (r *Runner) load(ctx context.Context) {
	r.cfg.Logger.Info("phase: load", "users", r.cfg.Users, "duration", loadDuration.String(), "maxCycles", r.cfg.MaxCycles)
	if reason := r.prepareUsers(ctx); reason != "" {
		r.board.Errorf("%s", reason)
		return
	}
	deadline := time.Now().Add(loadDuration)
	for cycle := 0; r.hasNextCycle(cycle, deadline); cycle++ {
		if ctx.Err() != nil {
			return
		}
		if ok := r.runCycle(ctx, cycle); !ok {
			return
		}
		r.cycles++
		if r.shouldAbort() {
			return
		}
		if !r.hasNextCycle(cycle+1, deadline.Add(-offsetSyncWait)) {
			break
		}
		if !r.advanceToNextDay(ctx, cycle) {
			return
		}
	}
}

// hasNextCycle decides whether cycle should run: a fixed cycle count when
// MaxCycles is set, otherwise the real-time deadline.
func (r *Runner) hasNextCycle(cycle int, deadline time.Time) bool {
	if r.cfg.MaxCycles > 0 {
		return cycle < r.cfg.MaxCycles
	}
	return time.Now().Before(deadline)
}

// shouldAbort marks the run as fail once the total error count reaches the
// threshold (D-009). The abort survives into the final Result.
func (r *Runner) shouldAbort() bool {
	if r.abortReason != "" {
		return true
	}
	if count := r.board.ErrorCount(); count >= abortErrorThreshold {
		r.abortReason = fmt.Sprintf("load aborted: error count reached %d (threshold %d, D-009)", count, abortErrorThreshold)
		r.cfg.Logger.Error("load aborted", "errorCount", count, "threshold", abortErrorThreshold)
		return true
	}
	return false
}

func (r *Runner) prepareUsers(ctx context.Context) string {
	r.users = make([]client.User, 0, r.cfg.Users)
	for index := range r.cfg.Users {
		username := fmt.Sprintf("bench_user_%03d", index)
		user, err := r.api.Register(ctx, username, benchUserPassword, fmt.Sprintf("Bench User %03d", index), benchUserRegion)
		if err != nil {
			return fmt.Sprintf("load user registration failed: %v", err)
		}
		r.users = append(r.users, user)
	}
	return ""
}

func (r *Runner) runCycle(ctx context.Context, cycle int) bool {
	scheduledAt := r.clock.Now().Add(scheduleLead)
	if err := r.api.SetSchedule(ctx, r.adminToken, benchUserRegion, scheduledAt); err != nil {
		r.board.Errorf("set schedule (cycle %d): %v", cycle, err)
		return false
	}
	window, ok := r.waitForWindow(ctx, r.clock.LocalDate(asiaUTCOffset))
	if !ok {
		r.board.Errorf("posting window did not open within %s (cycle %d, FR-003)", windowPollTimeout, cycle)
		return false
	}
	r.cfg.Logger.Info("cycle window open", "cycle", cycle, "localDate", window.LocalDate)

	r.checkNotifications(ctx, window.ID)
	r.fanOutUsers(ctx, cycle)
	if r.shouldAbort() {
		return false
	}
	if cycle > 0 {
		r.checkMemories(ctx)
	}
	return true
}

func (r *Runner) waitForWindow(ctx context.Context, wantLocalDate string) (client.Window, bool) {
	pollCtx, cancel := context.WithTimeout(ctx, windowPollTimeout)
	defer cancel()
	ticker := time.NewTicker(windowPollEvery)
	defer ticker.Stop()
	probe := r.users[0]
	for {
		status, err := r.api.CurrentWindow(pollCtx, probe.Token)
		if err == nil && status.Window != nil && status.Window.LocalDate == wantLocalDate {
			return *status.Window, true
		}
		select {
		case <-pollCtx.Done():
			return client.Window{}, false
		case <-ticker.C:
		}
	}
}

func (r *Runner) checkNotifications(ctx context.Context, windowID string) {
	page, err := r.api.Notifications(ctx, r.users[0].Token)
	if err != nil {
		r.board.Errorf("notifications: %v", err)
		return
	}
	for _, notification := range page.Items {
		if notification.PostingWindowID == windowID {
			r.board.Add(score.TagNotification)
			return
		}
	}
	r.board.Errorf("GET /api/v1/notifications: expected a notification for window %s (FR-002), got none", windowID)
}

func (r *Runner) fanOutUsers(ctx context.Context, cycle int) {
	var wg sync.WaitGroup
	wg.Add(len(r.users))
	for index, user := range r.users {
		go func() {
			defer wg.Done()
			r.runUserTurn(ctx, cycle, index, user)
		}()
	}
	wg.Wait()
}

func (r *Runner) runUserTurn(ctx context.Context, cycle, index int, user client.User) {
	caption := fmt.Sprintf("cycle %d from %s", cycle, user.Username)
	err := retryOn503(ctx, func() error {
		_, postErr := r.api.CreatePost(ctx, user.Token, r.postImage, caption, "public")
		return postErr
	})
	if err != nil {
		r.board.Errorf("create post (%s): %v", user.Username, err)
		return
	}
	r.board.Add(score.TagPost)

	page, err := r.api.Feed(ctx, user.Token, "public", "")
	if err != nil {
		r.board.Errorf("feed (%s): %v", user.Username, err)
		return
	}
	r.board.Add(score.TagFeed)

	interacted := 0
	for _, post := range page.Items {
		if post.Author.ID == user.ID {
			r.board.Errorf("GET /api/v1/feeds/public: expected feed to exclude own posts, got post %s by %s", post.ID, user.Username)
			continue
		}
		if interacted >= interactionsPerUser {
			break
		}
		reaction := reactionTypes[(cycle+index+interacted)%len(reactionTypes)]
		mojiErr := retryOn503(ctx, func() error {
			return r.api.ReplaceRealMoji(ctx, user.Token, post.ID, reaction, r.mojiImage)
		})
		if mojiErr != nil {
			r.board.Errorf("realmoji (%s): %v", user.Username, mojiErr)
		} else {
			r.board.Add(score.TagRealMoji)
		}
		comment := fmt.Sprintf("nice one! (cycle %d, %s)", cycle, user.Username)
		if err := r.api.CreateComment(ctx, user.Token, post.ID, comment); err != nil {
			r.board.Errorf("comment (%s): %v", user.Username, err)
		} else {
			r.board.Add(score.TagComment)
		}
		interacted++
	}
}

func (r *Runner) checkMemories(ctx context.Context) {
	month := r.clock.Now().Add(asiaUTCOffset).Format("2006-01")
	page, err := r.api.MemoriesMonth(ctx, r.users[0].Token, month)
	if err != nil {
		r.board.Errorf("memories: %v", err)
		return
	}
	if len(page.Items) == 0 {
		r.board.Errorf("GET /api/v1/memories?month=%s: expected past windows to appear (FR-016), got 0 items", month)
		return
	}
	r.board.Add(score.TagMemory)
}

// advanceToNextDay moves the virtual clock so the next cycle lands on the
// following calendar day at 00:00 UTC (VT-API-05: only between cycles).
func (r *Runner) advanceToNextDay(ctx context.Context, cycle int) bool {
	nextBase := r.baseTime.Add(time.Duration(cycle+1) * virtualDay)
	delta := nextBase.Sub(r.clock.Now())
	offsetSeconds := int64(math.Ceil(delta.Seconds()))
	if offsetSeconds <= 0 {
		offsetSeconds = 1
	}
	if _, err := r.api.AdvanceVirtualTime(ctx, offsetSeconds); err != nil {
		r.board.Errorf("advance virtual time (cycle %d): %v", cycle, err)
		return false
	}
	r.clock.Advance(time.Duration(offsetSeconds) * time.Second)
	// D-006: wait until every application process reflects the new offset.
	sleepCtx(ctx, offsetSyncWait)
	return ctx.Err() == nil
}

func isStatus(err error, status int) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// retryOn503 retries fn with linear backoff while it fails with 503.
func retryOn503(ctx context.Context, fn func() error) error {
	var err error
	for attempt := range retryAttempts {
		err = fn()
		if err == nil || !isStatus(err, http.StatusServiceUnavailable) {
			return err
		}
		sleepCtx(ctx, retryBaseDelay*time.Duration(attempt+1))
		if ctx.Err() != nil {
			return err
		}
	}
	return err
}

func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
