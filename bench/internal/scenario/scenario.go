package scenario

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imagegen"
	"github.com/isucon/isucon2026/bench/internal/score"
)

const (
	initializeTimeout   = 30 * time.Second
	loadDuration        = 60 * time.Second
	notificationTimeout = 10 * time.Second
	abortErrorThreshold = 100
	interactionsPerUser = 2
)

var countries = map[string]string{"asia": "JP", "europe": "DE", "america": "US"}
var reactionTypes = []string{"like", "heart", "laugh", "smile", "surprise", "instant"}

type Config struct {
	Target    string
	Host      string
	Region    string
	Users     int
	MaxCycles int
	Logger    *slog.Logger
}

type Result struct {
	Pass      bool             `json:"pass"`
	Score     int64            `json:"score"`
	Language  string           `json:"language"`
	Cycles    int              `json:"cycles"`
	Breakdown map[string]int64 `json:"breakdown"`
	Errors    []string         `json:"errors"`
	Reason    string           `json:"reason,omitempty"`
}

type Runner struct {
	cfg           Config
	api           *client.Client
	board         *score.Board
	adminToken    string
	users         []client.User
	postImage     []byte
	mojiImage     []byte
	language      string
	firstDate     time.Time
	previousPosts []client.Post
	cycles        int
}

func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Region == "" {
		cfg.Region = "asia"
	}
	if countries[cfg.Region] == "" {
		return nil, fmt.Errorf("invalid region %q", cfg.Region)
	}
	if cfg.Users < 1 || cfg.MaxCycles < 0 {
		return nil, fmt.Errorf("users must be positive and cycles must be nonnegative")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	postImage, err := imagegen.JPEG(64, 64, 1)
	if err != nil {
		return nil, err
	}
	mojiImage, err := imagegen.JPEG(32, 32, 2)
	if err != nil {
		return nil, err
	}
	api := client.NewWithHost(cfg.Target, cfg.Host)
	api.SetCountry(countries[cfg.Region])
	return &Runner{cfg: cfg, api: api, board: score.NewBoard(), postImage: postImage, mojiImage: mojiImage}, nil
}

func (r *Runner) Run(ctx context.Context) Result {
	reason := r.initialize(ctx)
	if reason == "" {
		reason = r.pretest(ctx)
	}
	if reason == "" {
		reason = r.prepareUsers(ctx)
	}
	if reason == "" {
		reason = r.load(ctx)
	}
	if ctx.Err() != nil {
		reason = ctx.Err().Error()
	}
	result := Result{Pass: reason == "", Language: r.language, Cycles: r.cycles, Breakdown: r.board.Breakdown(), Errors: r.board.Errors(), Reason: reason}
	if result.Pass {
		result.Score = r.board.Total()
	}
	return result
}

func (r *Runner) initialize(ctx context.Context) string {
	r.cfg.Logger.Info("phase: initialize")
	ctx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	result, err := r.api.Initialize(ctx)
	if err != nil {
		return fmt.Sprintf("initialize failed: %v", err)
	}
	if result.Language == "" {
		return "initialize response has empty language"
	}
	r.language = result.Language
	// localDate is a posting-window label; it does not advance the server clock.
	now := time.Now().UTC()
	r.firstDate = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return ""
}

func (r *Runner) registerAndLogin(ctx context.Context, name string) (client.User, error) {
	registered, err := r.api.Register(ctx, name, name, r.cfg.Region)
	if err != nil {
		return client.User{}, err
	}
	if registered.ID == "" || registered.SecretKey == "" {
		return client.User{}, fmt.Errorf("register: missing user ID or secretKey")
	}
	user, err := r.api.Login(ctx, name, registered.SecretKey)
	if err != nil {
		return client.User{}, err
	}
	if user.ID != registered.ID || user.Username != name || user.Token == "" {
		return client.User{}, fmt.Errorf("login: user or token mismatch")
	}
	user.SecretKey = registered.SecretKey
	return user, nil
}

func (r *Runner) pretest(ctx context.Context) string {
	r.cfg.Logger.Info("phase: pretest")
	token, err := r.api.AdminLogin(ctx, "admin", "password1234")
	if err != nil || token == "" {
		return fmt.Sprintf("admin login failed: token empty=%t error=%v", token == "", err)
	}
	r.adminToken = token
	user, err := r.registerAndLogin(ctx, "bench_pretest_user")
	if err != nil {
		return fmt.Sprintf("register/login: %v", err)
	}
	if _, err := r.api.Register(ctx, user.Username, "duplicate", r.cfg.Region); !isStatus(err, http.StatusConflict) {
		return fmt.Sprintf("duplicate registration: want 409, got %v", err)
	}
	if _, err := r.api.Login(ctx, user.Username, "wrong-secret"); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("wrong secret: want 401, got %v", err)
	}
	profile, err := r.api.Me(ctx, user.Token)
	if err != nil {
		return fmt.Sprintf("GET /me: %v", err)
	}
	if profile.ID != user.ID || profile.Username != user.Username || profile.Region != r.cfg.Region {
		return fmt.Sprintf("GET /me: unexpected profile %+v", profile)
	}
	feed, err := r.api.Feed(ctx, user.Token, "public", nil)
	if err != nil || len(feed.Items) != 0 || feed.HasMore {
		return fmt.Sprintf("feed before posting: want empty, got %+v, error=%v", feed, err)
	}
	replacement, err := r.api.Login(ctx, user.Username, user.SecretKey)
	if err != nil {
		return fmt.Sprintf("second login: %v", err)
	}
	if _, err := r.api.Me(ctx, user.Token); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("previous session: want 401, got %v", err)
	}
	if err := r.api.Logout(ctx, replacement.Token); err != nil {
		return fmt.Sprintf("logout: %v", err)
	}
	if _, err := r.api.Me(ctx, replacement.Token); !isStatus(err, http.StatusUnauthorized) {
		return fmt.Sprintf("logged-out session: want 401, got %v", err)
	}
	return ""
}

func (r *Runner) prepareUsers(ctx context.Context) string {
	for index := range r.cfg.Users {
		user, err := r.registerAndLogin(ctx, fmt.Sprintf("bench_user_%03d", index))
		if err != nil {
			return fmt.Sprintf("load user registration: %v", err)
		}
		r.users = append(r.users, user)
	}
	return ""
}

func (r *Runner) load(ctx context.Context) string {
	r.cfg.Logger.Info("phase: load", "users", len(r.users), "region", r.cfg.Region, "maxCycles", r.cfg.MaxCycles)
	if r.cfg.MaxCycles == 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, loadDuration)
		defer cancel()
	}
	for cycle := 0; r.cfg.MaxCycles == 0 || cycle < r.cfg.MaxCycles; cycle++ {
		if ctx.Err() != nil {
			return ""
		}
		reason := r.runCycle(ctx, cycle)
		if ctx.Err() != nil {
			return ""
		}
		if reason != "" {
			return reason
		}
		r.cycles++
		if r.board.ErrorCount() >= abortErrorThreshold {
			return fmt.Sprintf("load aborted: error count reached %d", r.board.ErrorCount())
		}
	}
	return ""
}

func parallel(count int, fn func(int)) {
	var wg sync.WaitGroup
	for index := range count {
		wg.Go(func() { fn(index) })
	}
	wg.Wait()
}

func (r *Runner) recordError(ctx context.Context, format string, args ...any) {
	if ctx.Err() == nil {
		r.board.Errorf(format, args...)
	}
}

func (r *Runner) runCycle(ctx context.Context, cycle int) string {
	date := r.firstDate.AddDate(0, 0, cycle).Format(time.DateOnly)
	notifyCtx, cancel := context.WithTimeout(ctx, notificationTimeout)
	defer cancel()
	streams := make([]*client.NotificationStream, len(r.users))
	defer func() {
		for _, stream := range streams {
			if stream != nil {
				_ = stream.Close()
			}
		}
	}()
	parallel(len(r.users), func(i int) {
		var err error
		streams[i], err = r.api.OpenNotifications(notifyCtx, r.users[i].Token)
		if err != nil {
			r.recordError(ctx, "connect notifications (%s): %v", r.users[i].Username, err)
		}
	})
	window, err := r.api.OpenWindow(ctx, r.adminToken, r.cfg.Region, date)
	if err != nil {
		return fmt.Sprintf("open posting window: %v", err)
	}
	if window.ID == "" || window.Region != r.cfg.Region || window.LocalDate != date {
		return fmt.Sprintf("open posting window: unexpected response %+v", window)
	}
	r.cfg.Logger.Info("cycle window open", "cycle", cycle, "localDate", date)
	notified := make([]bool, len(r.users))
	parallel(len(r.users), func(i int) {
		if streams[i] == nil {
			return
		}
		notification, err := streams[i].Receive()
		if err != nil {
			if notifyCtx.Err() == nil && !errors.Is(err, io.EOF) {
				r.recordError(ctx, "receive notification: %v", err)
			}
			return
		}
		if notification.ID == "" || notification.PostingWindowID != window.ID || notification.Region != r.cfg.Region {
			r.recordError(ctx, "notification: expected window %s/%s, got %+v", window.ID, r.cfg.Region, notification)
			return
		}
		notified[i] = true
		r.board.Add(score.TagNotification)
	})
	posts := make([]client.Post, len(r.users))
	parallel(len(r.users), func(i int) {
		if !notified[i] {
			return
		}
		user := r.users[i]
		status, err := r.api.CurrentWindow(ctx, user.Token)
		if err != nil || status.Window == nil || status.Window.ID != window.ID || !status.CanPost || status.HasPosted {
			r.recordError(ctx, "current window (%s): expected %s and canPost, got %+v, error=%v", user.Username, window.ID, status, err)
			return
		}
		post, err := r.api.CreatePost(ctx, user.Token, r.postImage, fmt.Sprintf("cycle %d from %s", cycle, user.Username), "public")
		if err != nil {
			r.recordError(ctx, "create post (%s): %v", user.Username, err)
			return
		}
		if post.ID == "" || post.Author.ID != user.ID || post.PostedAt.IsZero() {
			r.recordError(ctx, "create post (%s): unexpected response %+v", user.Username, post)
			return
		}
		image, err := r.api.Image(ctx, user.Token, post.ImageURL)
		if err != nil || !bytes.Equal(image, r.postImage) {
			r.recordError(ctx, "post image (%s): bytes mismatch or error=%v", user.Username, err)
			return
		}
		posts[i] = post
		r.board.Add(score.TagPost)
	})
	parallel(len(r.users), func(i int) {
		if posts[i].ID != "" {
			r.interact(ctx, cycle, i, posts)
		}
		if cycle > 0 && r.previousPosts[i].ID != "" {
			r.checkMemory(ctx, i, r.firstDate.AddDate(0, 0, cycle-1).Format("2006-01"), r.previousPosts[i].ID)
		}
	})
	r.previousPosts = posts
	return ""
}

func (r *Runner) interact(ctx context.Context, cycle, index int, posts []client.Post) {
	user := r.users[index]
	known := make(map[string]client.Post)
	for i, post := range posts {
		if i != index && post.ID != "" {
			known[post.ID] = post
		}
	}
	seen := make(map[string]bool)
	var before *client.Post
	var items []client.Post
	for {
		page, err := r.api.Feed(ctx, user.Token, "public", before)
		if err != nil {
			r.recordError(ctx, "feed (%s): %v", user.Username, err)
			return
		}
		for _, post := range page.Items {
			expected, ok := known[post.ID]
			if !ok || seen[post.ID] || post.Author.ID != expected.Author.ID {
				r.recordError(ctx, "feed (%s): unexpected or duplicate post %s", user.Username, post.ID)
				return
			}
			seen[post.ID] = true
			items = append(items, post)
		}
		if !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			r.recordError(ctx, "feed: hasMore with empty items")
			return
		}
		last := page.Items[len(page.Items)-1]
		before = &last
	}
	if len(seen) != len(known) {
		r.recordError(ctx, "feed (%s): expected %d posts, got %d", user.Username, len(known), len(seen))
		return
	}
	r.board.Add(score.TagFeed)
	for n, post := range items {
		if n >= interactionsPerUser {
			break
		}
		reaction := reactionTypes[(cycle+index+n)%len(reactionTypes)]
		if err := r.api.ReplaceRealMoji(ctx, user.Token, post.ID, reaction, r.mojiImage); err != nil {
			r.recordError(ctx, "realmoji (%s): %v", user.Username, err)
		} else {
			r.board.Add(score.TagRealMoji)
		}
		if err := r.api.CreateComment(ctx, user.Token, post.ID, fmt.Sprintf("cycle %d from %s", cycle, user.Username)); err != nil {
			r.recordError(ctx, "comment (%s): %v", user.Username, err)
		} else {
			r.board.Add(score.TagComment)
		}
	}
}

func (r *Runner) checkMemory(ctx context.Context, index int, month, postID string) {
	var before *client.Post
	seen := make(map[string]bool)
	for {
		page, err := r.api.MemoriesMonth(ctx, r.users[index].Token, month, before)
		if err != nil {
			r.recordError(ctx, "memories: %v", err)
			return
		}
		for _, post := range page.Items {
			if seen[post.ID] {
				r.recordError(ctx, "memories: duplicate post %s", post.ID)
				return
			}
			seen[post.ID] = true
			if post.ID == postID {
				r.board.Add(score.TagMemory)
				return
			}
		}
		if !page.HasMore || len(page.Items) == 0 {
			r.recordError(ctx, "memories month %s: missing previous post %s", month, postID)
			return
		}
		last := page.Items[len(page.Items)-1]
		before = &last
	}
}

func isStatus(err error, status int) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}
