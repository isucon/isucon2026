package handler

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/internal/session"
)

type ctxKey int

const reqInfoKey ctxKey = iota

// reqInfo はリクエスト単位の状態。セッション・競技時刻・メモ化した現在の選手/チーム。
type reqInfo struct {
	timing config.Timing
	now    time.Time
	sess   *session.Session

	// テストモード限定のセッション注入（RSpec ハーネス用フック）
	overrideTeamID int64
	csrfSkip       bool

	memo struct {
		contestant        *model.Contestant
		contestantLoaded  bool
		contestantAll     *model.Contestant // include_disqualified
		contestantAllLoad bool
		team              *model.Team
		teamLoaded        bool
	}
}

func info(r *http.Request) *reqInfo {
	v, _ := r.Context().Value(reqInfoKey).(*reqInfo)
	return v
}

// requestInfo はセッションと timing / now を整えてコンテキストに積む。
// テストモード時のみ X-Isux-* ヘッダによる上書きを許可する。
func (h *Handler) requestInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := &reqInfo{timing: h.cfg.Timing, now: time.Now().UTC()}
		ri.sess = h.store.Load(r.Context(), r)

		if h.cfg.TestMode {
			parse := func(name string, dst **time.Time) bool {
				v := r.Header.Get(name)
				if v == "" {
					return true
				}
				t, err := time.Parse(time.RFC3339Nano, v)
				if err != nil {
					renderError(w, http.StatusBadRequest, "BadRequest", name+": "+err.Error())
					return false
				}
				*dst = &t
				return true
			}
			var now *time.Time
			ok := parse("X-Isux-Timing-Registration-Open", &ri.timing.RegistrationOpen) &&
				parse("X-Isux-Timing-Registration-Close", &ri.timing.RegistrationClose) &&
				parse("X-Isux-Timing-Registration-Invitation-Close", &ri.timing.RegistrationInvitationClose) &&
				parse("X-Isux-Timing-Registration-Update-Close", &ri.timing.RegistrationUpdateClose) &&
				parse("X-Isux-Timing-Contest-Start", &ri.timing.ContestStart) &&
				parse("X-Isux-Timing-Contest-Freeze", &ri.timing.ContestFreeze) &&
				parse("X-Isux-Timing-Contest-End", &ri.timing.ContestEnd) &&
				parse("X-Isux-Now", &now)
			if !ok {
				return
			}
			if now != nil {
				ri.now = now.UTC()
			}
			if v := r.Header.Get("X-Isux-Session-Contestant-Id"); v != "" {
				if id, err := parseInt64(v); err == nil {
					ri.sess.ContestantID = id
					ri.csrfSkip = true
				}
			}
			if v := r.Header.Get("X-Isux-Session-Team-Id"); v != "" {
				if id, err := parseInt64(v); err == nil {
					ri.overrideTeamID = id
					ri.csrfSkip = true
				}
			}
			if r.Header.Get("X-Isux-Session-Staff") == "1" {
				ri.sess.Staff = true
				ri.csrfSkip = true
			}
		}

		ctx := context.WithValue(r.Context(), reqInfoKey, ri)
		ctx = contest.WithTiming(ctx, ri.timing)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// saveSession は変更のあったセッションを永続化する。レスポンスを書く前に呼ぶこと。
func (h *Handler) saveSession(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	return h.store.Save(r.Context(), w, ri.sess)
}

// currentContestant は Contestant.active（FR-AUTH: 有効チーム所属のみ）。
func (h *Handler) currentContestant(r *http.Request) (*model.Contestant, error) {
	ri := info(r)
	if ri.memo.contestantLoaded {
		return ri.memo.contestant, nil
	}
	ri.memo.contestantLoaded = true
	if ri.sess.ContestantID != 0 {
		c, err := model.FindActiveContestant(r.Context(), h.db, ri.sess.ContestantID)
		if err != nil {
			return nil, err
		}
		ri.memo.contestant = c
	} else if ri.overrideTeamID != 0 {
		// テストモードのチーム注入: チームの先頭メンバーとして振る舞う
		members, err := model.TeamMembers(r.Context(), h.db, ri.overrideTeamID)
		if err != nil {
			return nil, err
		}
		if len(members) > 0 {
			ri.memo.contestant = members[0]
		}
	}
	return ri.memo.contestant, nil
}

// currentContestantIncludeDisqualified は失格チームのメンバーも含める。
func (h *Handler) currentContestantIncludeDisqualified(r *http.Request) (*model.Contestant, error) {
	ri := info(r)
	if ri.memo.contestantAllLoad {
		return ri.memo.contestantAll, nil
	}
	ri.memo.contestantAllLoad = true
	if ri.sess.ContestantID != 0 {
		c, err := model.FindContestantIncludeDisqualified(r.Context(), h.db, ri.sess.ContestantID)
		if err != nil {
			return nil, err
		}
		ri.memo.contestantAll = c
	}
	return ri.memo.contestantAll, nil
}

func (h *Handler) currentTeam(r *http.Request) (*model.Team, error) {
	ri := info(r)
	if ri.memo.teamLoaded {
		return ri.memo.team, nil
	}
	ri.memo.teamLoaded = true
	c, err := h.currentContestant(r)
	if err != nil {
		return nil, err
	}
	if c != nil {
		team, err := model.FindTeam(r.Context(), h.db, c.TeamID)
		if err != nil {
			return nil, err
		}
		ri.memo.team = team
	} else if ri.overrideTeamID != 0 {
		team, err := model.FindTeam(r.Context(), h.db, ri.overrideTeamID)
		if err != nil {
			return nil, err
		}
		ri.memo.team = team
	}
	return ri.memo.team, nil
}

// currentBypassAllowed は session[:bypass_token] を検証して usage を確認する。
// 無効なトークンはセッションから消す（Rails と同じ）。
func (h *Handler) currentBypassAllowed(r *http.Request, usage string) bool {
	ri := info(r)
	if ri.sess.BypassToken == "" {
		return false
	}
	payload := verifyBypass(h.cfg, ri.sess.BypassToken, ri.now)
	if payload == nil {
		ri.sess.BypassToken = ""
		ri.sess.MarkDirty()
		return false
	}
	for _, u := range payload.Usages {
		if u.String() == usage {
			return true
		}
	}
	return false
}

// requireStaffWhenAlwaysRequired は ISUXPORTAL_ADMIN_ONLY の全体スイッチ（FR-AUTH-10）。
// 対象外: 運営ログイン画面、/api/session（配信画面用）、bench / envcheck / debug。
func (h *Handler) adminOnlySwitch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.AdminOnly && !info(r).sess.Staff && !adminOnlyExempt(r.URL.Path) {
			h.redirectToAdminLogin(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func adminOnlyExempt(path string) bool {
	switch {
	case path == "/site/sha" || path == "/admin/long_running_check",
		path == "/admin/session/new" || path == "/admin/session",
		path == "/api/session",
		path == "/api/env_check_info" || path == "/api/ssh_public_keys" || path == "/api/env_checks",
		strings.HasPrefix(path, "/api/bench/"),
		strings.HasPrefix(path, "/broadcast_view/"),
		strings.HasPrefix(path, "/debug/"),
		strings.HasPrefix(path, "/packs/"):
		return true
	}
	return false
}

func (h *Handler) redirectToAdminLogin(w http.ResponseWriter, r *http.Request) {
	backTo := r.URL.RequestURI()
	http.Redirect(w, r, "/admin/session/new?"+url.Values{"back_to": {backTo}}.Encode(), http.StatusFound)
}

// requireStaffHTML は運営画面のガード。未ログインならログイン画面へ。
func (h *Handler) requireStaffHTML(w http.ResponseWriter, r *http.Request) bool {
	if !info(r).sess.Staff {
		h.redirectToAdminLogin(w, r)
		return false
	}
	return true
}

// methodOverride は Rails の Rack::MethodOverride 相当。
// SPA のログアウト（Navbar.tsx）は `POST /session` + hidden `_method=delete` のフォーム送信で、
// これを DELETE として扱わないと 405 になる。フォーム本体はここで一度パースされ、
// 後段の CSRF 検証（authenticity_token）はキャッシュされた PostForm を読む。
func methodOverride(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && isFormContentType(r.Header.Get("Content-Type")) {
			switch override := strings.ToUpper(r.PostFormValue("_method")); override {
			case http.MethodDelete, http.MethodPut, http.MethodPatch:
				r.Method = override
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isFormContentType(ct string) bool {
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data")
}

// verifyCSRF は mutating リクエストの CSRF 検証（CT-2）。
// bench / envcheck / debug / OAuth 系は対象外（共有トークン・署名で認証）。
func (h *Handler) verifyCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/bench/") || path == "/api/env_checks" || strings.HasPrefix(path, "/debug/") {
			next.ServeHTTP(w, r)
			return
		}
		ri := info(r)
		if ri.csrfSkip {
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-Csrf-Token")
		if token == "" {
			token = r.PostFormValue("authenticity_token")
		}
		if ri.sess.CSRFToken == "" || token != ri.sess.CSRFToken {
			renderError(w, 422, "ActionController::InvalidAuthenticityToken", "Can't verify CSRF token authenticity.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
