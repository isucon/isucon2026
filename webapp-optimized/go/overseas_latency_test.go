package isureal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// Optional diagnostic against an isolated DB and a real delayed TCP connection.
// Does not depend on the challenge benchmark, its workload, or its score rules.
func TestOverseasLatency(t *testing.T) {
	delayedDSN := os.Getenv("ISUREAL_LATENCY_TEST_DSN")
	if delayedDSN == "" {
		t.Skip("ISUREAL_LATENCY_TEST_DSN is unset")
	}
	workers := 8
	if value := os.Getenv("ISUREAL_LATENCY_USERS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 64 {
			t.Fatal("ISUREAL_LATENCY_USERS must be between 1 and 64")
		}
		workers = parsed
	}
	direct := optimizedTestDB(t)
	remote, err := sqlx.Connect("pgx-auth", delayedDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	remote.SetMaxOpenConns(32)
	remote.SetMaxIdleConns(32)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := direct.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	type actor struct{ id, target, token string }
	type sample struct {
		region, operation, kind string
		elapsed                 time.Duration
	}
	var results []sample
	var mu sync.Mutex
	for _, region := range []string{"europe", "america"} {
		exec(`INSERT INTO posting_windows(id,region,local_date,starts_at,on_time_deadline)
   VALUES($1::text,$1::text,'2026-09-24',clock_timestamp()-interval '1 minute',clock_timestamp()+interval '1 day')`, region)
		actors := make([]actor, workers)
		for i := range actors {
			id, target := fmt.Sprintf("%s-%d", region, i), fmt.Sprintf("%s-target-%d", region, i)
			for _, user := range []string{id, target} {
				exec(`INSERT INTO users(id,username,display_name,secret_key_hash,region,account_type)
     VALUES($1::text,$1::text,$1::text,repeat('ab',32),$2,'certified_public')`, user, region)
				exec(`INSERT INTO image_objects(id,owner_user_id,data,media_type) VALUES($1,$2,'photo','image/jpeg')`, "image-"+user, user)
				exec(`INSERT INTO posts(id,author_id,posting_window_id,image_id,visibility,location_kind,posted_at)
     VALUES($1,$2,$3,$4,'public','none',clock_timestamp())`, "post-"+user, user, region, "image-"+user)
			}
			session, err := CreateLoginSession(direct, id)
			if err != nil {
				t.Fatal(err)
			}
			actors[i] = actor{id: id, target: target, token: session.Token}
		}
		h := &HandlerContext{DB: remote, Config: &Config{Region: region}}
		e := echo.New()
		e.HTTPErrorHandler = APIErrorHandler
		auth := BatchSessionMiddleware(h, true)
		regionCheck := RegionMiddleware(h)
		e.GET("/api/me", getMeHandler(h), BatchSessionMiddleware(h, true), regionCheck)
		e.GET("/api/feeds/:feed", getFeedHandler(h), auth, regionCheck)
		e.GET("/api/posts/:postId", getPostHandler(h), auth, regionCheck)
		e.GET("/api/posts/:postId/comments", getCommentsHandler(h), auth, regionCheck)
		e.GET("/api/posts/:postId/realmojis", getRealMojisHandler(h), auth, regionCheck)
		e.GET("/api/memories", getMemoriesHandler(h), auth, regionCheck)
		e.POST("/api/posts/:postId/comments", createCommentHandler(h), auth, regionCheck)
		e.POST("/api/posts/:postId/realmojis", replaceRealMojiHandler(h), auth, regionCheck)
		e.PATCH("/api/me", updateMeHandler(h), auth)
		e.PUT("/api/follows/:userId", followHandler(h), auth, regionCheck)
		e.DELETE("/api/follows/:userId", unfollowHandler(h), auth, regionCheck)
		e.PUT("/api/blocks/:userId", blockHandler(h), auth, regionCheck)
		e.DELETE("/api/blocks/:userId", unblockHandler(h), auth, regionCheck)
		e.POST("/api/posts", createPostHandler(h), auth, regionCheck)
		e.DELETE("/api/posts/:postId", deletePostHandler(h), auth, regionCheck)
		var photo bytes.Buffer
		multipartWriter := multipart.NewWriter(&photo)
		for k, v := range map[string]string{"visibility": "public", "location_kind": "none", "caption": "overseas", "reactionType": "like"} {
			if err := multipartWriter.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
		part, err := multipartWriter.CreateFormFile("image", "photo.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(testJPEG(t)); err != nil {
			t.Fatal(err)
		}
		if err := multipartWriter.Close(); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, actor := range actors {
			wg.Go(func() {
				request := func(iteration int, operation, method, path, body, media string, want int) bool {
					req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
					req.Header.Set("Authorization", "Bearer "+actor.token)
					req.Header.Set("Content-Type", media)
					rec := httptest.NewRecorder()
					started := time.Now()
					e.ServeHTTP(rec, req)
					elapsed := time.Since(started)
					if rec.Code != want {
						t.Errorf("%s %s: %d %s", region, operation, rec.Code, rec.Body.String())
						return false
					}
					if iteration > 0 {
						kind := "write"
						if method == http.MethodGet {
							kind = "read"
						}
						mu.Lock()
						results = append(results, sample{region, operation, kind, elapsed})
						mu.Unlock()
					}
					return true
				}
				// First cycle warms statement caches for a fair steady-state comparison.
				for iteration := range 3 {
					for _, read := range []struct{ name, path string }{
						{"me", "/api/me"}, {"feed", "/api/feeds/public"}, {"post", "/api/posts/post-" + actor.target},
						{"comments", "/api/posts/post-" + actor.target + "/comments"},
						{"reactions", "/api/posts/post-" + actor.target + "/realmojis"}, {"memories", "/api/memories?month=2026-09"},
					} {
						if !request(iteration, read.name, "GET", read.path, "", "", 200) {
							return
						}
					}
					for _, write := range []struct {
						name, method, path, body, media string
						status                          int
					}{
						{"profile", "PATCH", "/api/me", `{"displayName":"updated"}`, "application/json", 200},
						{"comment", "POST", "/api/posts/post-" + actor.target + "/comments", `{"body":"overseas"}`, "application/json", 201},
						{"reaction", "POST", "/api/posts/post-" + actor.target + "/realmojis", photo.String(), multipartWriter.FormDataContentType(), 201},
						{"follow", "PUT", "/api/follows/" + actor.target, "", "", 204},
						{"unfollow", "DELETE", "/api/follows/" + actor.target, "", "", 204},
						{"block", "PUT", "/api/blocks/" + actor.target, "", "", 204},
						{"unblock", "DELETE", "/api/blocks/" + actor.target, "", "", 204},
					} {
						if !request(iteration, write.name, write.method, write.path, write.body, write.media, write.status) {
							return
						}
					}
				}
				if !request(1, "delete_post", "DELETE", "/api/posts/post-"+actor.id, "", "", 204) {
					return
				}
				request(1, "create_post", "POST", "/api/posts", photo.String(), multipartWriter.FormDataContentType(), 201)
			})
		}
		wg.Wait()
	}
	for _, region := range []string{"europe", "america"} {
		for _, kind := range []string{"read", "write"} {
			var times []float64
			for _, s := range results {
				if s.region == region && s.kind == kind {
					times = append(times, float64(s.elapsed)/float64(time.Millisecond))
				}
			}
			sort.Float64s(times)
			if len(times) == 0 {
				t.Fatalf("no %s samples for %s", kind, region)
			}
			summary := map[string]any{"region": region, "kind": kind, "users": workers, "samples": len(times), "p50_ms": times[(len(times)-1)/2], "p99_ms": times[(len(times)*99-1)/100]}
			data, _ := json.Marshal(summary)
			t.Logf("OVERSEAS %s", data)
		}
	}
	if os.Getenv("ISUREAL_LATENCY_ENFORCE") == "1" {
		// Functional acceptance for this controlled workload, not a score assertion.
		for _, s := range results {
			limit := 3 * time.Second
			if s.kind == "read" {
				limit = time.Second
			}
			if s.elapsed > limit {
				t.Errorf("%s %s took %s (limit %s)", s.region, s.operation, s.elapsed, limit)
			}
		}
	}
}
