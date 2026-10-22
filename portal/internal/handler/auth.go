package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	pbmisc "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc"
	"github.com/isucon/isucon2026/portal/internal/config"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
	"github.com/isucon/isucon2026/portal/internal/session"
	"github.com/isucon/isucon2026/portal/internal/token"
)

var botUARegexp = regexp.MustCompile(`(?i)Discordbot|Slackbot`)

// safeBackTo は相対パス（ホスト・スキームなしで / 始まり）だけを許す（FR-AUTH-4）。
func safeBackTo(s string) bool {
	if s == "" || !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Host == "" && u.Scheme == ""
}

// sessionNew は /session/new/{provider}。Discordbot / Slackbot の UA を 404 で弾く。
func (h *Handler) sessionNew(w http.ResponseWriter, r *http.Request, provider string) {
	if botUARegexp.MatchString(r.UserAgent()) && r.URL.Query().Get("beepbeep") == "" {
		renderPlain(w, http.StatusNotFound, "Beep beep, boop beep?")
		return
	}
	ri := info(r)
	ri.sess.BackTo = ""
	if backTo := r.URL.Query().Get("back_to"); safeBackTo(backTo) {
		ri.sess.BackTo = backTo
	}
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	http.Redirect(w, r, "/auth/"+provider, http.StatusFound)
}

func (h *Handler) SessionNewGitHub(w http.ResponseWriter, r *http.Request) {
	h.sessionNew(w, r, "github")
}
func (h *Handler) SessionNewDiscord(w http.ResponseWriter, r *http.Request) {
	h.sessionNew(w, r, "discord")
}

// OAuthStart は omniauth の /auth/{provider} 相当（プロバイダへリダイレクト）。
func (h *Handler) OAuthStart(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ri := info(r)
		state := make([]byte, 24)
		if _, err := rand.Read(state); err != nil {
			renderInternalError(w, err)
			return
		}
		ri.sess.OAuthState = base64.RawURLEncoding.EncodeToString(state)
		ri.sess.MarkDirty()
		if err := h.saveSession(w, r); err != nil {
			renderInternalError(w, err)
			return
		}

		p := h.oauthProvider(provider)
		q := url.Values{
			"client_id":     {p.ClientID},
			"redirect_uri":  {h.callbackURL(r, provider)},
			"response_type": {"code"},
			"state":         {ri.sess.OAuthState},
		}
		if provider == "discord" {
			q.Set("scope", "identify")
		}
		http.Redirect(w, r, p.AuthorizeURL+"?"+q.Encode(), http.StatusFound)
	}
}

func (h *Handler) oauthProvider(provider string) config.OAuthProvider {
	if provider == "discord" {
		return h.cfg.Discord.OAuthProvider
	}
	return h.cfg.GitHub
}

func (h *Handler) callbackURL(r *http.Request, provider string) string {
	if path := h.oauthProvider(provider).CallbackPath; path != "" {
		return h.baseURL(r) + path
	}
	return h.baseURL(r) + "/auth/" + provider + "/callback"
}

func (h *Handler) baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// exchangeOAuth は callback の code をアクセストークンに交換する。
func (h *Handler) exchangeOAuth(ctx context.Context, r *http.Request, provider, code string) (string, error) {
	p := h.oauthProvider(provider)
	form := url.Values{
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		// Both callback routes remain valid, including flows started before a configuration change.
		"redirect_uri": {h.baseURL(r) + r.URL.Path},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth token exchange: %d", res.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("oauth token exchange: empty token")
	}
	return body.AccessToken, nil
}

func (h *Handler) oauthGetJSON(ctx context.Context, urlStr, accessToken string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("oauth user fetch: %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(dst)
}

// oauthCallbackPrelude は state 検証と code 交換を行う。失敗時は /auth/failure へ。
func (h *Handler) oauthCallbackPrelude(w http.ResponseWriter, r *http.Request, provider string) (string, bool) {
	ri := info(r)
	q := r.URL.Query()
	if q.Get("error") != "" || q.Get("code") == "" ||
		ri.sess.OAuthState == "" || q.Get("state") != ri.sess.OAuthState {
		h.authFailure(w, r)
		return "", false
	}
	ri.sess.OAuthState = ""
	ri.sess.MarkDirty()
	accessToken, err := h.exchangeOAuth(r.Context(), r, provider, q.Get("code"))
	if err != nil {
		h.authFailure(w, r)
		return "", false
	}
	return accessToken, true
}

func (h *Handler) authFailure(w http.ResponseWriter, r *http.Request) {
	_ = h.saveSession(w, r)
	http.Redirect(w, r, "/auth/failure", http.StatusFound)
}

func (h *Handler) redirectAfterAuth(w http.ResponseWriter, r *http.Request, fallback string) {
	ri := info(r)
	dest := fallback
	if ri.sess.BackTo != "" {
		dest = ri.sess.BackTo
	}
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// GitHubCallback は sessions#github_callback 相当（FR-AUTH-1）。
func (h *Handler) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	accessToken, ok := h.oauthCallbackPrelude(w, r, "github")
	if !ok {
		return
	}
	var user struct {
		ID        json.Number `json:"id"`
		Login     string      `json:"login"`
		AvatarURL string      `json:"avatar_url"`
	}
	if err := h.oauthGetJSON(r.Context(), h.cfg.GitHub.APIBaseURL+"/user", accessToken, &user); err != nil {
		h.authFailure(w, r)
		return
	}

	if user.ID.String() == "" || user.Login == "" {
		h.authFailure(w, r)
		return
	}

	ri := info(r)
	ri.sess.GitHubLogin = &session.OAuthIdentity{
		ID: user.ID.String(), Login: user.Login, Token: accessToken, AvatarURL: user.AvatarURL,
	}
	ri.sess.MarkDirty()

	ctx := r.Context()
	c, err := model.FindContestantIncludeDisqualifiedBy(ctx, h.db, "github_id", user.ID.String())
	if err != nil {
		renderInternalError(w, err)
		return
	}
	switch {
	case c != nil && h.teamDisqualified(ctx, c):
		ri.sess.ContestantID = c.ID
		h.redirectAfterAuth(w, r, "/registration")
	case c != nil:
		if _, err := h.db.ExecContext(ctx, `UPDATE contestants SET github_login = $1, updated_at = now() WHERE id = $2`, user.Login, c.ID); err != nil {
			renderInternalError(w, err)
			return
		}
		ri.sess.ContestantID = c.ID
		h.jobs.Enqueue(ctx, "sync_ssh_keys_of_contestant", job.SyncSSHKeysArgs{ContestantID: c.ID, AccessToken: accessToken})
		h.jobs.Enqueue(ctx, "sync_discord_member_state_of_contestant", job.ContestantArgs{ContestantID: c.ID})
		open, err := contest.ContestOpenForTeam(ctx, h.db, ri.timing, c.TeamID, ri.now)
		if err != nil {
			renderInternalError(w, err)
			return
		}
		dest := "/"
		if open {
			dest = "/contestant"
		}
		h.redirectAfterAuth(w, r, dest)
	default:
		h.redirectAfterAuth(w, r, "/registration")
	}
}

// DiscordCallback は sessions#discord_callback 相当（FR-AUTH-2/3）。
func (h *Handler) DiscordCallback(w http.ResponseWriter, r *http.Request) {
	accessToken, ok := h.oauthCallbackPrelude(w, r, "discord")
	if !ok {
		return
	}
	var user struct {
		ID            string `json:"id"`
		Username      string `json:"username"`
		Discriminator string `json:"discriminator"`
		Avatar        string `json:"avatar"`
	}
	if err := h.oauthGetJSON(r.Context(), h.cfg.Discord.OAuthProvider.APIBaseURL+"/users/@me", accessToken, &user); err != nil {
		h.authFailure(w, r)
		return
	}

	if user.ID == "" || user.Username == "" {
		h.authFailure(w, r)
		return
	}

	// discriminator の無くなったアカウントは '0'（FR-AUTH-2）
	tag := user.Username
	if user.Discriminator != "0" && user.Discriminator != "" {
		tag += "#" + user.Discriminator
	}
	avatarURL := fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", user.ID, user.Avatar)

	ri := info(r)
	ri.sess.DiscordLogin = &session.OAuthIdentity{ID: user.ID, Tag: tag, Token: accessToken, AvatarURL: avatarURL}
	ri.sess.MarkDirty()

	ctx := r.Context()
	c, err := model.FindContestantIncludeDisqualifiedBy(ctx, h.db, "discord_id", user.ID)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	contestRunning := contest.ContestRunning(ri.timing, ri.now)
	switch {
	case c != nil && h.teamDisqualified(ctx, c):
		ri.sess.ContestantID = c.ID
		h.redirectAfterAuth(w, r, "/registration")
	case c != nil:
		// discord_id 一致のアカウントに切り替え（FR-AUTH-3a）
		if _, err := h.db.ExecContext(ctx, `UPDATE contestants SET discord_tag = $1, updated_at = now() WHERE id = $2`, tag, c.ID); err != nil {
			renderInternalError(w, err)
			return
		}
		ri.sess.ContestantID = c.ID
		h.syncDiscordContestant(ctx, c.ID)
		dest := "/"
		if contestRunning {
			dest = "/contestant"
		}
		h.redirectAfterAuth(w, r, dest)
	case ri.sess.ContestantID != 0:
		// ログイン済み → Discord アカウントの差し替え（FR-AUTH-3b）
		current, err := model.FindActiveContestant(ctx, h.db, ri.sess.ContestantID)
		if err != nil {
			renderInternalError(w, err)
			return
		}
		if current == nil {
			h.redirectAfterAuth(w, r, "/registration")
			return
		}
		if _, err := h.db.ExecContext(ctx, `UPDATE contestants SET discord_id = $1, discord_tag = $2, is_discord_guild_member = false, updated_at = now() WHERE id = $3`, user.ID, tag, current.ID); err != nil {
			renderInternalError(w, err)
			return
		}
		// Publish jobs only after the new identity is visible to workers.
		h.jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: fmt.Sprintf(":left_right_arrow: *Switch discord account:* contestant=%d from=%s(%s) to=%s(%s)",
			current.ID, current.DiscordTag, current.DiscordID, tag, user.ID)})
		if current.DiscordID != "" {
			h.jobs.Enqueue(ctx, "maintain_discord_contestant_roles", job.MaintainRolesArgs{ForceDiscordID: current.DiscordID})
		}
		h.syncDiscordContestant(ctx, current.ID)
		dest := "/"
		if contestRunning {
			dest = "/contestant"
		}
		h.redirectAfterAuth(w, r, dest)
	default:
		h.redirectAfterAuth(w, r, "/registration")
	}
}

// syncDiscordContestant also repairs roles when the user joined before linking.
func (h *Handler) syncDiscordContestant(ctx context.Context, contestantID int64) {
	h.jobs.Enqueue(ctx, "sync_discord_member_state_of_contestant", job.ContestantArgs{ContestantID: contestantID})
	h.jobs.Enqueue(ctx, "maintain_discord_contestant_roles", job.MaintainRolesArgs{ContestantID: contestantID})
}

func (h *Handler) teamDisqualified(ctx context.Context, c *model.Contestant) bool {
	team, err := model.FindTeam(ctx, h.db, c.TeamID)
	return err == nil && team != nil && team.Disqualified
}

// SessionDestroy はログアウト（FR-AUTH-6）。
func (h *Handler) SessionDestroy(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	ri.sess.ContestantID = 0
	ri.sess.Staff = false
	ri.sess.GitHubLogin = nil
	ri.sess.DiscordLogin = nil
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func verifyBypass(cfg *config.Config, tok string, now time.Time) *pbmisc.BypassTokenPayload {
	return token.VerifyBypass(cfg.BypassTokenSecret, tok, now)
}

// AssumeBypassToken は /session/bypass/:token（FR-AUTH-7）。
func (h *Handler) AssumeBypassToken(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	tok := chi.URLParam(r, "token")
	if verifyBypass(h.cfg, tok, ri.now) == nil {
		ri.sess.BypassToken = ""
		ri.sess.MarkDirty()
		_ = h.saveSession(w, r)
		renderPlain(w, http.StatusNotFound, "no")
		return
	}
	ri.sess.BypassToken = tok
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	if backTo := r.URL.Query().Get("back_to"); safeBackTo(backTo) {
		http.Redirect(w, r, backTo, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// --- ガード（4 種、計画書 §3） ---

// contestantGuards は require_contestant → require_promoted_contestant → require_contest_started。
// 単一開催（P-3）のため promoted 判定は active のみ。
func (h *Handler) contestantGuards(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := info(r)
		c, err := h.currentContestant(r)
		if err != nil {
			renderInternalError(w, err)
			return
		}
		if c == nil && ri.sess.GitHubLogin == nil {
			backTo := r.URL.RequestURI()
			http.Redirect(w, r, "/session/new/github?"+url.Values{"back_to": {backTo}}.Encode(), http.StatusFound)
			return
		}
		team, err := h.currentTeam(r)
		if err != nil {
			renderInternalError(w, err)
			return
		}
		if team == nil || !team.Active() {
			renderPlain(w, http.StatusForbidden, "Forbidden")
			return
		}
		open, err := contest.ContestOpenForTeam(r.Context(), h.db, ri.timing, team.ID, ri.now)
		if err != nil {
			renderInternalError(w, err)
			return
		}
		if !open {
			renderErrorFull(w, http.StatusNotFound, "Error::NotFound", "contest is not open", []string{})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminAPIGuard は require_staff。未ログインはログイン画面へリダイレクト（Rails と同じ）。
func (h *Handler) adminAPIGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !info(r).sess.Staff {
			h.redirectToAdminLogin(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// secureCompare は定数時間比較（NF-8）。
func secureCompare(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// --- テストモード限定の疑似 OAuth プロバイダ ---
// RSpec が実プロバイダ無しで OAuth フロー全体（redirect → callback）を検証するための口。

func (h *Handler) FakeOAuthRegister(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		renderPlain(w, http.StatusBadRequest, err.Error())
		return
	}
	var user map[string]any
	if err := json.Unmarshal(body, &user); err != nil {
		renderPlain(w, http.StatusBadRequest, err.Error())
		return
	}
	code := make([]byte, 12)
	if _, err := rand.Read(code); err != nil {
		renderInternalError(w, err)
		return
	}
	codeStr := base64.RawURLEncoding.EncodeToString(code)
	h.fakeOAuth.mu.Lock()
	// 直前のフローの残骸で別ユーザーが返らないよう、登録ごとにリセットする
	h.fakeOAuth.codes = map[string]map[string]any{codeStr: user}
	h.fakeOAuth.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"code": codeStr})
}

func (h *Handler) FakeOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	// 直近に register された code を使う（テストは直列実行なのでこれで足りる）
	h.fakeOAuth.mu.Lock()
	var code string
	for c := range h.fakeOAuth.codes {
		code = c
	}
	h.fakeOAuth.mu.Unlock()
	dest := redirectURI + "?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	http.Redirect(w, r, dest, http.StatusFound)
}

func (h *Handler) FakeOAuthToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	code := r.PostFormValue("code")
	h.fakeOAuth.mu.Lock()
	_, ok := h.fakeOAuth.codes[code]
	h.fakeOAuth.mu.Unlock()
	if !ok {
		renderPlain(w, http.StatusBadRequest, "invalid code")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok-" + code})
}

func (h *Handler) fakeOAuthUser(w http.ResponseWriter, r *http.Request) map[string]any {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	code := strings.TrimPrefix(auth, "tok-")
	h.fakeOAuth.mu.Lock()
	user, ok := h.fakeOAuth.codes[code]
	delete(h.fakeOAuth.codes, code)
	h.fakeOAuth.mu.Unlock()
	if !ok {
		renderPlain(w, http.StatusUnauthorized, "unauthorized")
		return nil
	}
	return user
}

func (h *Handler) FakeOAuthGitHubUser(w http.ResponseWriter, r *http.Request) {
	if user := h.fakeOAuthUser(w, r); user != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(user)
	}
}

func (h *Handler) FakeOAuthDiscordUser(w http.ResponseWriter, r *http.Request) {
	if user := h.fakeOAuthUser(w, r); user != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(user)
	}
}
