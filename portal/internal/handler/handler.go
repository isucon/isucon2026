package handler

import (
	"database/sql"
	"net/http"
	"path"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/isucon/isucon2026/portal/internal/cache"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/external"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/session"
)

type Handler struct {
	cfg   *config.Config
	db    *sql.DB
	store *session.Store
	cache *cache.Cache
	jobs  *job.Env
	push  *external.WebPush
	aws   *external.AWS

	// fakeOAuth はテストモード限定の疑似 OAuth プロバイダ状態
	fakeOAuth struct {
		mu    sync.Mutex
		codes map[string]map[string]any // code -> user payload
	}
}

func New(cfg *config.Config, db *sql.DB, store *session.Store, c *cache.Cache, jobs *job.Env, push *external.WebPush, aws *external.AWS) *Handler {
	h := &Handler{cfg: cfg, db: db, store: store, cache: c, jobs: jobs, push: push, aws: aws}
	h.fakeOAuth.codes = map[string]map[string]any{}
	return h
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(methodOverride)
	r.Use(h.requestInfo)
	r.Use(h.adminOnlySwitch)
	r.Use(h.verifyCSRF)

	// --- ページ（SPA シェル 26 + ERB 3 + リダイレクト） ---
	for _, route := range []string{
		"/", "/teams", "/registration", "/registration/env_check", "/auth/failure",
	} {
		r.Get(route, h.SPAShell("audience"))
	}
	for _, route := range []string{
		"/contestant", "/contestant/benchmark_jobs", "/contestant/benchmark_jobs/{id}",
		"/contestant/clarifications", "/contestant/contestant_instances", "/contestant/discord",
	} {
		r.Get(route, h.ContestantShell)
	}
	for _, route := range []string{
		"/admin", "/admin/teams", "/admin/teams/{id}", "/admin/teams/{id}/edit",
		"/admin/benchmark_jobs", "/admin/benchmark_jobs/{id}",
		"/admin/clarifications", "/admin/clarifications/{id}",
		"/admin/last_validations", "/admin/last_validations/command_result/{id}",
		"/admin/contestant_instances", "/admin/unprepared_stats",
	} {
		r.Get(route, h.AdminShell)
	}
	for _, route := range []string{"/broadcast_view/leaderboard", "/broadcast_view/score_changes", "/broadcast_view/clock"} {
		r.Get(route, h.BroadcastShell)
	}

	r.Get("/terms", h.redirectTo(h.cfg.TermsURL))
	r.Get("/rules", h.redirectTo(h.cfg.RulesURL))
	r.Get("/docs", h.DocsRedirect) // 競技時間中のみ有効（routes.rb の constraints 相当）
	r.Get("/site/sha", func(w http.ResponseWriter, _ *http.Request) { renderPlain(w, http.StatusOK, h.cfg.Revision) })

	// --- 認証・セッション（8 本） ---
	r.Get("/session/new/github", h.SessionNewGitHub)
	r.Get("/session/new/discord", h.SessionNewDiscord)
	r.Get("/auth/github", h.OAuthStart("github"))
	r.Get("/auth/discord", h.OAuthStart("discord"))
	r.Get("/auth/github/callback", h.GitHubCallback)
	r.Get("/auth/discord/callback", h.DiscordCallback)
	r.Delete("/session", h.SessionDestroy)
	r.Get("/session/bypass/{token}", h.AssumeBypassToken)

	// --- 運営 ERB 3 画面 + フォーム POST 5 本 + デバッグ 4 本 ---
	r.Get("/admin/session/new", h.AdminSessionNew)
	r.Post("/admin/session", h.AdminSessionCreate)
	r.Get("/admin/impersonate", h.ImpersonateIndex)
	r.Post("/admin/impersonate/contestant", h.ImpersonateContestant)
	r.Post("/admin/impersonate/github", h.ImpersonateGitHub)
	r.Post("/admin/impersonate/discord", h.ImpersonateDiscord)
	r.Get("/admin/bypass_token", h.BypassTokenNew)
	r.Post("/admin/bypass_token", h.BypassTokenCreate)
	r.Get("/admin/slacktown", h.DebugSlack)
	r.Get("/admin/sync_all_ssh_key", h.DebugSyncAllSSHKeys)
	r.Get("/admin/sync_all_discord_stats", h.DebugSyncAllDiscordStats)
	r.Get("/admin/long_running_check", h.DebugLongRunningCheck) // 認証なし（notifyloop が叩く）

	// --- protobuf API ---
	r.Get("/api/session", h.api(h.APISession))

	r.Route("/api/registration", func(r chi.Router) {
		r.Get("/session", h.api(h.RegistrationSessionShow))
		r.Get("/record", h.api(h.RegistrationRecordShow))
		r.Put("/record", h.api(h.RegistrationRecordUpdate))
		r.Put("/", h.api(h.RegistrationUpdate))
		r.Delete("/", h.api(h.RegistrationDelete))
		r.Post("/team", h.api(h.RegistrationCreateTeam))
		r.Post("/contestant", h.api(h.RegistrationJoinTeam))
		r.Get("/env_check", h.api(h.RegistrationEnvCheck))
		r.Put("/coupon", h.api(h.RegistrationActivateCoupon))
		r.Get("/avatar/url", h.api(h.RegistrationAvatarURL))
	})

	r.Get("/api/audience/dashboard", h.api(h.AudienceDashboard))
	r.Get("/api/audience/dashboard/teams/{id}", h.api(h.AudienceSoloDashboard))
	r.Get("/api/audience/teams", h.api(h.AudienceTeams))

	r.Route("/api/contestant", func(r chi.Router) {
		r.Use(h.contestantGuards)
		r.Get("/dashboard", h.api(h.ContestantDashboard))
		r.Get("/benchmark_jobs", h.api(h.ContestantJobsIndex))
		r.Post("/benchmark_jobs", h.api(h.ContestantJobsCreate))
		r.Get("/benchmark_jobs/{id}", h.api(h.ContestantJobsShow))
		r.Get("/clarifications", h.api(h.ContestantClarsIndex))
		r.Post("/clarifications", h.api(h.ContestantClarsCreate))
		r.Get("/notifications", h.api(h.ContestantNotificationsIndex))
		r.Post("/notifications", h.api(h.ContestantNotificationsCreate))
		r.Delete("/notifications/{id}", h.api(h.ContestantNotificationsDestroy))
		r.Post("/push_subscriptions", h.api(h.ContestantPushSubscribe))
		r.Delete("/push_subscriptions", h.api(h.ContestantPushUnsubscribe))
		r.Get("/cloud_formation", h.api(h.ContestantCloudFormation))
	})

	r.Route("/api/admin", func(r chi.Router) {
		r.Use(h.adminAPIGuard)
		r.Get("/contestants/{id}/registration_record", h.api(h.AdminRegistrationRecord))
		r.Get("/registration_source/{id}", h.api(h.AdminRegistrationSource))
		r.Get("/dashboard", h.api(h.AdminDashboard))
		r.Get("/dashboard/teams/{id}", h.api(h.AdminSoloDashboard))
		r.Get("/teams", h.api(h.AdminTeamsIndex))
		r.Get("/teams/{id}", h.api(h.AdminTeamsShow))
		r.Put("/teams/{id}", h.api(h.AdminTeamsUpdate))
		r.Get("/teams/{team_id}/cloud_formation", h.api(h.AdminCloudFormation))
		r.Get("/teams/{team_id}/env_checks", h.api(h.AdminEnvChecks))
		r.Get("/benchmark_jobs", h.api(h.AdminJobsIndex))
		r.Post("/benchmark_jobs", h.api(h.AdminJobsCreate))
		r.Get("/benchmark_jobs/{id}", h.api(h.AdminJobsShow))
		r.Delete("/benchmark_jobs/{id}", h.api(h.AdminJobsDestroy))
		r.Get("/clarifications", h.api(h.AdminClarsIndex))
		r.Post("/clarifications", h.api(h.AdminClarsCreate))
		r.Get("/clarifications/{id}", h.api(h.AdminClarsShow))
		r.Put("/clarifications/{id}", h.api(h.AdminClarsUpdate))
		r.Get("/instance_command_execute_requests", h.api(h.AdminCommandRequestsIndex))
		r.Get("/instance_command_execute_requests/{id}", h.api(h.AdminCommandRequestsShow))
		// 変則ルート（CT-9）: :request_id と :id が 2 つ並ぶ
		r.Get("/instance_command_execute_requests/{request_id}/{id}/output", h.api(h.AdminCommandRequestOutput))
		r.Post("/last_validations/env_check", h.api(h.AdminTriggerEnvCheck))
		r.Post("/last_validations/instance_restart", h.api(h.AdminTriggerInstanceRestart))
		r.Post("/last_validations/benchmarks", h.api(h.AdminTriggerBenchmarks))
		r.Get("/contestant_instances", h.api(h.AdminInstancesIndex))
		r.Get("/dump_leaderboard", h.api(h.AdminDumpLeaderboard))
		r.Get("/unprepared_stats/ssh_key_stats", h.api(h.AdminSSHKeyStats))
		r.Get("/unprepared_stats/discord_stats", h.api(h.AdminDiscordStats))
		r.Get("/unprepared_stats/env_check_stats", h.api(h.AdminEnvCheckStats))
	})

	r.Route("/api/bench", func(r chi.Router) {
		r.Post("/receive", h.api(h.BenchReceive))
		r.Post("/cancel_owned", h.api(h.BenchCancelOwned))
		r.Post("/benchmark_results", h.api(h.BenchReportResult))
		r.Post("/benchmark_results/completion", h.api(h.BenchCompleteJob))
	})

	// 非 protobuf 3 本（FR-E: JSON / text/plain / 空ボディ）
	r.Get("/api/env_check_info", h.EnvCheckInfo)
	r.Get("/api/ssh_public_keys", h.EnvCheckSSHPublicKeys)
	r.Post("/api/env_checks", h.EnvCheckCreate)

	if h.cfg.TestMode {
		// RSpec ハーネス用（Phase 0 の HTTP 化 spec が使う）。本番では無効。
		r.Get("/debug/leaderboard", h.DebugLeaderboard)
		r.Post("/debug/update_best_scores", h.DebugUpdateBestScores)
		r.Post("/debug/flushdb", h.DebugFlushDB)
		r.Post("/debug/update_contestant_dashboard", h.DebugUpdateContestantDashboard)
		r.Get("/debug/oauth/{provider}/authorize", h.FakeOAuthAuthorize)
		r.Post("/debug/oauth/{provider}/token", h.FakeOAuthToken)
		r.Get("/debug/oauth/github/user", h.FakeOAuthGitHubUser)
		r.Get("/debug/oauth/discord/users/@me", h.FakeOAuthDiscordUser)
		r.Post("/debug/oauth/{provider}/register", h.FakeOAuthRegister)
	}

	// 静的アセット（CT-3: /packs は長期キャッシュ）
	if h.cfg.PublicDir != "" {
		dir := http.Dir(h.cfg.PublicDir)
		fs := http.FileServer(dir)
		r.Get("/packs/*", func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Cache-Control", "public,max-age=31536000")
			fs.ServeHTTP(w, req)
		})
		// アイコン・ロゴ・sw.js など public 直下のファイルはルート未マッチ時に配信する
		r.NotFound(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet || req.Method == http.MethodHead {
				name := path.Clean(req.URL.Path)
				if f, err := dir.Open(name); err == nil {
					stat, statErr := f.Stat()
					f.Close()
					if statErr == nil && !stat.IsDir() {
						fs.ServeHTTP(w, req)
						return
					}
				}
			}
			http.NotFound(w, req)
		})
	}

	return r
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func pathID(r *http.Request) (int64, bool) {
	return pathIDNamed(r, "id")
}

func pathIDNamed(r *http.Request, name string) (int64, bool) {
	id, err := parseInt64(chi.URLParam(r, name))
	return id, err == nil && id > 0
}
