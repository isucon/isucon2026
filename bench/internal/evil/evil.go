// Package evil は得点に数えない不正な利用者を動かす。
package evil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/scenario"
)

const interval = 500 * time.Millisecond

var _ scenario.EvilFunc = Run

type actorPlan struct {
	at       time.Duration
	username string
	queries  []string
}

// plans は走行開始時刻から登場時刻と検索語を決める。
func plans(env scenario.EvilEnv) []actorPlan {
	seed := env.Seed
	if seed == 0 {
		seed = uint64(env.Start.UnixNano())
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x6576696c))
	queries := []string{"a", "e", "i", "o", "u"}
	if env.Dataset != nil {
		for _, u := range env.Dataset.Users {
			if u != nil && u.Username != "" {
				queries = append(queries, u.Username)
			}
		}
	}
	var out []actorPlan
	for i, fraction := range []int{2, 5, 8} {
		p := actorPlan{at: env.Length * time.Duration(fraction) / 12,
			username: fmt.Sprintf("evil_%x_%d", seed, i)}
		for j := 0; j < 64; j++ {
			p.queries = append(p.queries, queries[rng.IntN(len(queries))])
		}
		out = append(out, p)
	}
	return out
}

// Run は時計に従って利用者を増やし、終了時に通信を取り消す。
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
			runActor(ctx, env, api, p, interval)
		}()
	}
	<-ctx.Done()
}

// invalidImage は許容寸法を超えた JPEG。初期実装は復号してから拒否する。
func invalidImage() []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 513, 513)), nil)
	return b.Bytes()
}

// runActor は応答を待たずに操作を予定する。滞留時は捨て、後からまとめて送らない。
func runActor(parent context.Context, env scenario.EvilEnv, api *client.Client, p actorPlan, tick time.Duration) {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	var sent, skipped, bans atomic.Int64
	defer func() {
		cancel()
		wg.Wait()
		if env.Logger != nil {
			env.Logger.Info("不正な利用者の通信", "ip", api.ClientIP(), "operations", sent.Load(), "skipped", skipped.Load(), "banned", bans.Load() != 0)
		}
	}()
	slots := make(chan struct{}, 8)
	launch := func(f func(context.Context) error) {
		if ctx.Err() != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			skipped.Add(1)
			return
		}
		wg.Add(1)
		sent.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			reqCtx, done := context.WithTimeout(ctx, 2*time.Second)
			defer done()
			err := f(reqCtx)
			var e *client.APIError
			if errors.As(err, &e) && e.Status == http.StatusForbidden && e.Code == "ip_banned" {
				bans.Add(1)
				cancel()
			}
		}()
	}
	var mu sync.Mutex
	var own client.User
	var token string
	var registering, loggingIn bool
	payload := invalidImage()
	start := env.Start.Add(p.at)
	for n := 0; waitUntil(ctx, start.Add(time.Duration(n)*tick)); n++ {
		// 遅れた枠を再送しない。
		if time.Since(start.Add(time.Duration(n)*tick)) >= tick {
			skipped.Add(1)
			continue
		}
		if n == 0 {
			registering = true
			launch(func(ctx context.Context) error {
				u, err := api.Register(ctx, p.username, "Evil user", "asia")
				if u.ID != "" && env.RegisterUser != nil {
					env.RegisterUser(u.ID)
				}
				mu.Lock()
				own = u
				mu.Unlock()
				return err
			})
			continue
		}
		mu.Lock()
		u, session := own, token
		mu.Unlock()
		if registering && !loggingIn && u.ID != "" && u.SecretKey != "" {
			loggingIn = true
			launch(func(ctx context.Context) error {
				loggedIn, err := api.Login(ctx, p.username, u.SecretKey)
				if err == nil {
					mu.Lock()
					token = loggedIn.Token
					mu.Unlock()
				}
				return err
			})
			continue
		}
		// 自分の名前だけを使う。正常なユーザーの認証情報には触れない。
		launch(func(ctx context.Context) error {
			_, err := api.Login(ctx, p.username, "invalid-evil-credential")
			return err
		})
		if n >= 16 && n%2 == 0 && session != "" {
			if n%4 == 0 {
				q := p.queries[(n/4)%len(p.queries)]
				launch(func(ctx context.Context) error { _, err := api.SearchUsers(ctx, session, q); return err })
			} else {
				launch(func(ctx context.Context) error { return api.PutProfileImage(ctx, session, payload) })
			}
		}
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
