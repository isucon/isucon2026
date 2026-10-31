// Package evil runs abusive users whose requests do not contribute to the score.
package evil

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
	"github.com/isucon/isucon2026/bench/internal/scenario"
	"github.com/isucon/isucon2026/dataset"
)

const arrivalInterval = 5 * time.Second
const type2UploadCount = 8

var _ scenario.EvilFunc = Run

type actorPlan struct {
	at             time.Duration
	kind           int
	username       string
	rate           int
	password       string
	uploadInterval time.Duration
	targetIndex    int
}

// plans gives each actor a separate identity and IP. Type 1 arrives every five seconds;
// two Type 2 actors arrive 1.25 and 2.5 seconds later, then Type 3 at 3.75 seconds.
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
		if time.Duration(n+1)*arrivalInterval >= env.Length {
			continue
		}
		for variant, offset := range []time.Duration{arrivalInterval / 4, arrivalInterval / 2} {
			if at := time.Duration(n)*arrivalInterval + offset; at < env.Length {
				out = append(out, actorPlan{at: at, kind: 2, username: fmt.Sprintf("evil_upload_%x_%d_%d", seed, n, variant), uploadInterval: 500 * time.Millisecond})
			}
		}
		if at := time.Duration(n)*arrivalInterval + 3*arrivalInterval/4; at < env.Length {
			out = append(out, actorPlan{at: at, kind: 3, rate: 50, targetIndex: n})
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
			switch p.kind {
			case 1:
				runLoginActor(ctx, env, api, p)
			case 2:
				runUploadActor(ctx, env, api, p)
			case 3:
				runSignatureActor(ctx, env, api, p)
			}
		}()
	}
	<-ctx.Done()
}

func isBan(err error) bool {
	var e *client.APIError
	return errors.As(err, &e) && e.Status == http.StatusForbidden && e.Code == "ip_banned"
}

// runLoginActor sends fabricated login attempts at the planned rate.
func runLoginActor(parent context.Context, env scenario.EvilEnv, api *client.Client, p actorPlan) {
	runRateActor(parent, env, api, p, func(ctx context.Context, n int) error {
		_, err := api.Login(ctx, fmt.Sprintf("%s_%d", p.username, n), p.password)
		return err
	})
}

// runSignatureActor sends a syntactically valid but incorrect signature for an existing
// image. The valid image ID and expiry make the application perform signature verification.
func runSignatureActor(parent context.Context, env scenario.EvilEnv, api *client.Client, p actorPlan) {
	path, ok := forgedImageURL(env.Dataset, p.targetIndex)
	if !ok {
		return
	}
	runRateActor(parent, env, api, p, func(ctx context.Context, _ int) error {
		_, err := api.Image(ctx, path)
		return err
	})
}

func forgedImageURL(world *dataset.World, index int) (string, bool) {
	if world == nil {
		return "", false
	}
	var targets []*dataset.User
	for _, user := range world.Users {
		if user.Status == "active" && user.ProfileImage != nil {
			targets = append(targets, user)
		}
	}
	if len(targets) == 0 {
		return "", false
	}
	owner := targets[index%len(targets)]
	imageID := owner.ProfileImage.ID
	exp := time.Now().Add(time.Hour).Unix()
	query := url.Values{"exp": {fmt.Sprint(exp)}, "sig": {imageurl.Sign("wrong-"+owner.SecretKey, imageID, exp)}}
	return "/api/images/" + url.PathEscape(imageID) + "?" + query.Encode(), true
}

// runRateActor sends requests at the planned rate and stops when the IP is banned.
// In-flight requests are bounded; missed slots are discarded after congestion.
func runRateActor(parent context.Context, env scenario.EvilEnv, api *client.Client, p actorPlan, request func(context.Context, int) error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var wg sync.WaitGroup
	var sent, skipped atomic.Int64
	var banned atomic.Bool
	defer wg.Wait()
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
			err := request(reqCtx, n)
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
	var sent atomic.Int64
	var banned atomic.Bool
	var invalidImage, tooLarge, otherUploadResult atomic.Int64
	var wg sync.WaitGroup
	defer wg.Wait()
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
	for n := 0; n < type2UploadCount && waitUntil(ctx, start.Add(time.Duration(n)*p.uploadInterval)); n++ {
		wg.Add(1)
		sent.Add(1)
		go func() {
			defer wg.Done()
			reqCtx, done := context.WithTimeout(ctx, 10*time.Second)
			defer done()
			err := api.PutNonJPEGStream(reqCtx, loggedIn.Token)
			var apiErr *client.APIError
			switch {
			case errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest && apiErr.Code == "invalid_profile_image":
				invalidImage.Add(1)
			case errors.As(err, &apiErr) && apiErr.Status == http.StatusRequestEntityTooLarge:
				tooLarge.Add(1)
			default:
				otherUploadResult.Add(1)
			}
			if isBan(err) {
				banned.Store(true)
				cancel()
			}
		}()
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
