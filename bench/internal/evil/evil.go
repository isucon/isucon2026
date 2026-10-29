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
	at       time.Duration
	kind     int
	username string
	rate     int
	password string
}

// plans gives each actor a separate identity and IP. Type 1 arrives every five seconds;
// Type 2 arrives halfway between those arrivals.
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
		if at := time.Duration(n)*arrivalInterval + arrivalInterval/2; at < env.Length {
			out = append(out, actorPlan{at: at, kind: 2, username: fmt.Sprintf("evil_upload_%x_%d", seed, n)})
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
	var sent int64
	var banned bool
	defer func() { logActor(env, api, p, sent, 0, banned) }()
	u, err := api.Register(ctx, p.username, "Evil upload user", "asia")
	sent++
	if isBan(err) {
		banned = true
	}
	if err != nil || u.ID == "" || u.SecretKey == "" {
		return
	}
	if env.RegisterUser != nil {
		env.RegisterUser(u.ID)
	}
	loggedIn, err := api.Login(ctx, p.username, u.SecretKey)
	sent++
	if isBan(err) {
		banned = true
	}
	if err != nil || loggedIn.Token == "" {
		return
	}
	for ctx.Err() == nil {
		reqCtx, done := context.WithTimeout(ctx, 2*time.Second)
		err := api.PutNonJPEGStream(reqCtx, loggedIn.Token)
		done()
		sent++
		if isBan(err) {
			banned = true
			return
		}
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
