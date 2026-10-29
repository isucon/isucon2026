// Package evil runs abusive users whose requests do not contribute to the score.
package evil

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/scenario"
)

const arrivalInterval = 5 * time.Second

var _ scenario.EvilFunc = Run

type actorPlan struct {
	at             time.Duration
	kind           int
	username       string
	rate           int
	password       string
	uploadInterval time.Duration
}

// plans gives each actor a separate identity and IP. Type 1 arrives every five seconds;
// two Type 2 actors arrive 1.25 and 2.5 seconds after each Type 1.
func plans(env scenario.EvilEnv) []actorPlan {
	seed := env.Seed
	if seed == 0 {
		seed = uint64(env.Start.UnixNano())
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x6576696c))
	var out []actorPlan
	for n := 0; time.Duration(n)*arrivalInterval < env.Length; n++ {
		rate := 40 + rng.IntN(121)
		if time.Duration(n+1)*arrivalInterval >= env.Length {
			rate = 300
		}
		out = append(out, actorPlan{
			at: time.Duration(n) * arrivalInterval, kind: 1,
			username: fmt.Sprintf("evil_login_%x_%d", seed, n),
			rate:     rate, password: fmt.Sprintf("password_dummy_%d", rng.IntN(1_000_000)),
		})
		for variant, offset := range []time.Duration{arrivalInterval / 4, arrivalInterval / 2} {
			if at := time.Duration(n)*arrivalInterval + offset; at < env.Length {
				out = append(out, actorPlan{at: at, kind: 2, username: fmt.Sprintf("evil_upload_%x_%d_%d", seed, n, variant), uploadInterval: 2 * time.Second})
			}
		}
	}
	return out
}

// Run starts actors on the load clock and waits for their requests to finish.
func Run(ctx context.Context, env scenario.EvilEnv) {
	ctx, cancel := context.WithDeadline(ctx, env.Start.Add(env.Length))
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, p := range plans(env) {
		if !waitUntil(ctx, env.Start.Add(p.at)) {
			return
		}
		api := env.API.WithCountry("JP").WithClientIP(env.NextIP())
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.kind == 1 {
				runLoginActor(ctx, env, api, p)
			} else {
				runUploadActor(ctx, env, api, p)
			}
		}()
	}
	<-ctx.Done()
}

func isBan(err error) bool {
	var e *client.APIError
	return errors.As(err, &e) && e.Status == http.StatusForbidden && e.Code == "ip_banned"
}

// runLoginActor schedules 40-160 requests per second, or 300 for the last actor. In-flight requests are bounded;
// a missed slot is discarded rather than replayed after congestion clears.
func runLoginActor(parent context.Context, env scenario.EvilEnv, api *client.Client, p actorPlan) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var wg sync.WaitGroup
	var sent, skipped atomic.Int64
	var banned atomic.Bool
	defer func() {
		wg.Wait()
		logActor(env, api, p, sent.Load(), skipped.Load(), banned.Load())
	}()
	slots := make(chan struct{}, 64)
	interval := time.Second / time.Duration(p.rate)
	start := env.Start.Add(p.at)
	for n := 0; waitUntil(ctx, start.Add(time.Duration(n)*interval)); n++ {
		if time.Since(start.Add(time.Duration(n)*interval)) >= interval {
			skipped.Add(1)
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			skipped.Add(1)
			continue
		}
		wg.Add(1)
		sent.Add(1)
		go func(n int) {
			defer wg.Done()
			defer func() { <-slots }()
			reqCtx, done := context.WithTimeout(ctx, 2*time.Second)
			defer done()
			_, err := api.Login(reqCtx, fmt.Sprintf("%s_%d", p.username, n), p.password)
			if isBan(err) {
				banned.Store(true)
				cancel()
			}
		}(n)
	}
}

func runUploadActor(parent context.Context, env scenario.EvilEnv, api *client.Client, p actorPlan) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var sent, skipped atomic.Int64
	var banned atomic.Bool
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		logActor(env, api, p, sent.Load(), skipped.Load(), banned.Load())
	}()
	u, err := api.Register(ctx, p.username, "Evil upload user", "asia")
	sent.Add(1)
	if isBan(err) {
		banned.Store(true)
	}
	if err != nil || u.ID == "" || u.SecretKey == "" {
		return
	}
	if env.RegisterUser != nil {
		env.RegisterUser(u.ID)
	}
	loggedIn, err := api.Login(ctx, p.username, u.SecretKey)
	sent.Add(1)
	if isBan(err) {
		banned.Store(true)
	}
	if err != nil || loggedIn.Token == "" {
		return
	}
	start := time.Now()
	slots := make(chan struct{}, 4)
	for n := 0; waitUntil(ctx, start.Add(time.Duration(n)*p.uploadInterval)); n++ {
		select {
		case slots <- struct{}{}:
		default:
			skipped.Add(1)
			continue
		}
		wg.Add(1)
		sent.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			reqCtx, done := context.WithTimeout(ctx, 10*time.Second)
			defer done()
			if isBan(api.PutNonJPEGStream(reqCtx, loggedIn.Token)) {
				banned.Store(true)
				cancel()
			}
		}()
	}
}

func logActor(env scenario.EvilEnv, api *client.Client, p actorPlan, sent, skipped int64, banned bool) {
	if env.Logger != nil {
		env.Logger.Info("不正な利用者の通信", "type", p.kind, "ip", api.ClientIP(), "operations", sent, "skipped", skipped, "banned", banned)
	}
}

func waitUntil(ctx context.Context, at time.Time) bool {
	if ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
