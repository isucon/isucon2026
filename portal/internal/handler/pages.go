package handler

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"

	pbmisc "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/session"
	"github.com/isucon/isucon2026/portal/internal/token"
)

// shellTemplate は layouts/application.html.erb 相当の SPA シェル。
// meta タグ 8 個（isux:* 6 種 + csrf-param / csrf-token）が契約（CT-2）。
var shellTemplate = template.Must(template.New("shell").Parse(`<!DOCTYPE html>
<html lang='ja'>
  <head>
    <title>ISUCON2026 Portal</title>
    <meta name="viewport" content="width=device-width,initial-scale=1.0">
    <meta name="csrf-param" content="authenticity_token">
    <meta name="csrf-token" content="{{.CSRFToken}}">

    <meta name="isux:github-auth-path" content="{{.GitHubAuthPath}}">
    <meta name="isux:discord-auth-path" content="{{.DiscordAuthPath}}">
    <meta name="isux:api-base-url" content="/">
    <meta name="isux:release" content="{{.Release}}">
{{- if .SentryDSN}}
    <meta name="isux:sentry-dsn" content="{{.SentryDSN}}">
    <meta name="isux:environment" content="{{.Environment}}">
{{- end}}

    <link href="https://fonts.googleapis.com/icon?family=Material+Icons|Material+Icons+Outlined&display=block" rel="stylesheet">
{{- if .ApplicationCSS}}
    <link rel="stylesheet" media="all" href="{{.ApplicationCSS}}">
{{- end}}
{{- if .VendorJS}}
    <script src="{{.VendorJS}}"></script>
{{- end}}
{{- if .ApplicationJS}}
    <script src="{{.ApplicationJS}}"></script>
{{- end}}

    <link rel="shortcut icon" href="/favicon.ico">

    <link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon-180x180.png">
    <link rel="icon" sizes="48x48" href="/icon-48x48.png">
    <link rel="icon" sizes="96x96" href="/icon-96x96.png">
    <link rel="icon" sizes="144x144" href="/icon-144x144.png">
    <link rel="icon" sizes="192x192" href="/icon-192x192.png">
    <link rel="icon" sizes="384x384" href="/icon-384x384.png">
    <link rel="icon" sizes="512x512" href="/icon-512x512.png">

    <meta name="msapplication-square70x70logo" content="/site-tile-70x70.png">
    <meta name="msapplication-square150x150logo" content="/site-tile-150x150.png">
    <meta name="msapplication-square310x310logo" content="/site-tile-310x310.png">
    <meta name="msapplication-wide310x150logo" content="/site-tile-310x150.png">
  </head>

  <body>
    <div id="app"></div>
{{- range .ExtraCSS}}
    <link rel="stylesheet" href="{{.}}">
{{- end}}
{{- range .BundleJS}}
    <script src="{{.}}"></script>
{{- end}}
  </body>
</html>
`))

type shellData struct {
	CSRFToken       string
	GitHubAuthPath  string
	DiscordAuthPath string
	Release         string
	SentryDSN       string
	Environment     string
	ApplicationCSS  string
	VendorJS        string
	ApplicationJS   string
	ExtraCSS        []string
	BundleJS        []string
}

var (
	manifestOnce  sync.Once
	manifestCache map[string]string
)

// packPath はパック名を manifest.json（simpacker 互換、CT-3）でハッシュ付きパスに解決する。
func (h *Handler) packPath(name string) string {
	load := func() map[string]string {
		if h.cfg.PacksManifest == "" {
			return nil
		}
		data, err := os.ReadFile(h.cfg.PacksManifest)
		if err != nil {
			return nil
		}
		m := map[string]string{}
		if err := json.Unmarshal(data, &m); err != nil {
			return nil
		}
		return m
	}
	var m map[string]string
	if h.cfg.TestMode {
		m = load()
	} else {
		manifestOnce.Do(func() { manifestCache = load() })
		m = manifestCache
	}
	if m == nil {
		return ""
	}
	return m[name]
}

func (h *Handler) renderShell(w http.ResponseWriter, r *http.Request, bundles []string, extraCSS []string) {
	ri := info(r)
	csrf := ri.sess.EnsureCSRFToken()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}

	backTo := r.URL.RequestURI()
	data := shellData{
		CSRFToken:       csrf,
		GitHubAuthPath:  "/session/new/github?" + url.Values{"back_to": {backTo}}.Encode(),
		DiscordAuthPath: "/session/new/discord?" + url.Values{"back_to": {backTo}}.Encode(),
		Release:         h.cfg.Revision,
		SentryDSN:       h.cfg.SentryDSN,
		Environment:     h.cfg.SentryEnvironment,
		ApplicationCSS:  h.packPath("application.css"),
		VendorJS:        h.packPath("vendor.js"),
		ApplicationJS:   h.packPath("application.js"),
	}
	for _, b := range bundles {
		if p := h.packPath(b + ".js"); p != "" {
			data.BundleJS = append(data.BundleJS, p)
		}
	}
	for _, c := range extraCSS {
		if p := h.packPath(c + ".css"); p != "" {
			data.ExtraCSS = append(data.ExtraCSS, p)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := shellTemplate.Execute(w, data); err != nil {
		renderInternalError(w, err)
	}
}

func (h *Handler) SPAShell(bundle string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.renderShell(w, r, []string{bundle}, nil)
	}
}

// ContestantShell は選手ページ。3 段ガードを HTML 用の応答で通す。
func (h *Handler) ContestantShell(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	c, err := h.currentContestant(r)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	if c == nil && ri.sess.GitHubLogin == nil {
		http.Redirect(w, r, "/session/new/github?"+url.Values{"back_to": {r.URL.RequestURI()}}.Encode(), http.StatusFound)
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
		renderPlain(w, http.StatusNotFound, "contest is not open")
		return
	}
	h.renderShell(w, r, []string{"contestant"}, nil)
}

func (h *Handler) AdminShell(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	h.renderShell(w, r, []string{"admin"}, nil)
}

func (h *Handler) BroadcastShell(w http.ResponseWriter, r *http.Request) {
	h.renderShell(w, r, []string{"broadcast_view", "audience"}, []string{"broadcast_view"})
}

func (h *Handler) redirectTo(dest string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest, http.StatusFound)
	}
}

// DocsRedirect は競技時間中だけ有効（routes.rb の constraints 相当）。
func (h *Handler) DocsRedirect(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	if !contest.ContestRunning(ri.timing, ri.now) {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, h.cfg.DocsURL, http.StatusFound)
}

// --- 運営 ERB 相当 3 画面 + フォーム POST ---

var adminLoginTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="ja"><head><title>Admin Login</title></head><body>
<div class="container px-5 py-5">
{{- if .Error}}<p class="error">sorry</p>{{end}}
<form action="/admin/session" method="post">
  <input type="hidden" name="authenticity_token" value="{{.CSRFToken}}">
{{- if .BackTo}}
  <input type="hidden" name="back_to" value="{{.BackTo}}">
{{- end}}
  <input type="text" name="login" placeholder="login" required class="input">
  <input type="password" name="password" placeholder="our top secret" required class="input">
  <input type="submit" value="Escalate" class="button is-primary">
</form>
</div>
</body></html>
`))

func (h *Handler) AdminSessionNew(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	backTo := r.URL.Query().Get("back_to")
	if ri.sess.Staff && safeBackTo(backTo) {
		http.Redirect(w, r, backTo, http.StatusFound)
		return
	}
	csrf := ri.sess.EnsureCSRFToken()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = adminLoginTemplate.Execute(w, map[string]any{
		"CSRFToken": csrf,
		"BackTo":    backTo,
		"Error":     r.URL.Query().Get("error") != "",
	})
}

// AdminSessionCreate は運営ログイン（FR-AUTH-8: 定数時間比較）。
func (h *Handler) AdminSessionCreate(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	login := r.PostFormValue("login")
	password := r.PostFormValue("password")
	if h.cfg.AdminLogin != "" && h.cfg.AdminPassword != "" {
		ri.sess.Staff = secureCompare(login, h.cfg.AdminLogin) && secureCompare(password, h.cfg.AdminPassword)
	} else {
		ri.sess.Staff = true
	}
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	backTo := r.PostFormValue("back_to")
	if !ri.sess.Staff {
		q := url.Values{"error": {"1"}}
		if backTo != "" {
			q.Set("back_to", backTo)
		}
		http.Redirect(w, r, "/admin/session/new?"+q.Encode(), http.StatusFound)
		return
	}
	if safeBackTo(backTo) {
		http.Redirect(w, r, backTo, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

var impersonateTemplate = template.Must(template.New("impersonate").Parse(`<!DOCTYPE html>
<html lang="ja"><head><title>Impersonate</title></head><body>
<section><h5>Contestant</h5>
<p>Current: <code>{{.ContestantID}}</code></p>
<form action="/admin/impersonate/contestant" method="post">
  <input type="hidden" name="authenticity_token" value="{{.CSRFToken}}">
  <label>Clear <input type="checkbox" name="clear" value="1"></label>
  <label>ID <input type="number" name="id"></label>
  <input type="submit" value="Submit">
</form></section>
<section><h5>GitHub</h5>
<p>Current: <code>{{.GitHubLogin}}</code></p>
<form action="/admin/impersonate/github" method="post">
  <input type="hidden" name="authenticity_token" value="{{.CSRFToken}}">
  <label>Clear <input type="checkbox" name="clear" value="1"></label>
  <label>ID <input type="number" name="id"></label>
  <label>Login <input type="text" name="login"></label>
  <input type="submit" value="Submit">
</form></section>
<section><h5>Discord</h5>
<p>Current: <code>{{.DiscordLogin}}</code></p>
<form action="/admin/impersonate/discord" method="post">
  <input type="hidden" name="authenticity_token" value="{{.CSRFToken}}">
  <label>Clear <input type="checkbox" name="clear" value="1"></label>
  <label>ID <input type="number" name="id"></label>
  <label>Login <input type="text" name="login"></label>
  <input type="submit" value="Submit">
</form></section>
</body></html>
`))

func (h *Handler) ImpersonateIndex(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	ri := info(r)
	csrf := ri.sess.EnsureCSRFToken()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	github := ""
	if ri.sess.GitHubLogin != nil {
		github = fmt.Sprintf("{id: %s, login: %s}", ri.sess.GitHubLogin.ID, ri.sess.GitHubLogin.Login)
	}
	discord := ""
	if ri.sess.DiscordLogin != nil {
		discord = fmt.Sprintf("{id: %s, tag: %s}", ri.sess.DiscordLogin.ID, ri.sess.DiscordLogin.Tag)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = impersonateTemplate.Execute(w, map[string]any{
		"CSRFToken":    csrf,
		"ContestantID": ri.sess.ContestantID,
		"GitHubLogin":  github,
		"DiscordLogin": discord,
	})
}

// ImpersonateContestant は session[:contestant_id] の任意設定（FR-AUTH-9）。
func (h *Handler) ImpersonateContestant(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	ri := info(r)
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	ri.sess.ContestantID = id
	if r.PostFormValue("clear") != "" {
		ri.sess.ContestantID = 0
	}
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/impersonate", http.StatusFound)
}

func (h *Handler) ImpersonateGitHub(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	ri := info(r)
	ri.sess.GitHubLogin = &session.OAuthIdentity{
		ID:        r.PostFormValue("id"),
		Login:     r.PostFormValue("login"),
		AvatarURL: "https://avatars2.githubusercontent.com/u/10137?s=400&v=4",
	}
	if r.PostFormValue("clear") != "" {
		ri.sess.GitHubLogin = nil
	}
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/impersonate", http.StatusFound)
}

func (h *Handler) ImpersonateDiscord(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	ri := info(r)
	ri.sess.DiscordLogin = &session.OAuthIdentity{
		ID:        r.PostFormValue("id"),
		Tag:       r.PostFormValue("login"),
		AvatarURL: "https://avatars2.githubusercontent.com/u/10137?s=400&v=4",
	}
	if r.PostFormValue("clear") != "" {
		ri.sess.DiscordLogin = nil
	}
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	http.Redirect(w, r, "/admin/impersonate", http.StatusFound)
}

var bypassTokenTemplate = template.Must(template.New("bypass").Parse(`<!DOCTYPE html>
<html lang="ja"><head><title>Bypass Token Factory</title></head><body>
<h2>Bypass Token Factory</h2>
{{- if .URL}}
<p><a href="{{.URL}}" id="bypass-token-url">{{.URL}}</a></p>
{{- end}}
<form action="/admin/bypass_token" method="post">
  <input type="hidden" name="authenticity_token" value="{{.CSRFToken}}">
  <label>Expires in <input type="number" name="expires_in" value="10800"></label>
{{- range .Usages}}
  <label><input type="checkbox" name="usages[]" value="{{.}}"> {{.}}</label>
{{- end}}
  <button type="submit">Go</button>
</form>
</body></html>
`))

func (h *Handler) renderBypassTokenPage(w http.ResponseWriter, r *http.Request, tokenURL string) {
	ri := info(r)
	csrf := ri.sess.EnsureCSRFToken()
	if err := h.saveSession(w, r); err != nil {
		renderInternalError(w, err)
		return
	}
	usages := make([]string, 0, len(pbmisc.BypassTokenPayload_Usage_name))
	for i := 0; i < len(pbmisc.BypassTokenPayload_Usage_name); i++ {
		usages = append(usages, pbmisc.BypassTokenPayload_Usage_name[int32(i)])
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = bypassTokenTemplate.Execute(w, map[string]any{
		"CSRFToken": csrf,
		"URL":       tokenURL,
		"Usages":    usages,
	})
}

func (h *Handler) BypassTokenNew(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	h.renderBypassTokenPage(w, r, "")
}

// BypassTokenCreate は bypass token の発行（FR-S-10）。
func (h *Handler) BypassTokenCreate(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	ri := info(r)
	expiresIn, _ := strconv.ParseInt(r.PostFormValue("expires_in"), 10, 64)
	payload := &pbmisc.BypassTokenPayload{Expiry: ri.now.Unix() + expiresIn}
	_ = r.ParseForm()
	for _, u := range r.PostForm["usages[]"] {
		v, ok := pbmisc.BypassTokenPayload_Usage_value[u]
		if !ok {
			renderPlain(w, http.StatusBadRequest, "400")
			return
		}
		payload.Usages = append(payload.Usages, pbmisc.BypassTokenPayload_Usage(v))
	}
	tok, err := token.CreateBypass(h.cfg.BypassTokenSecret, payload)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	h.renderBypassTokenPage(w, r, h.baseURL(r)+"/session/bypass/"+tok)
}

// --- 運営デバッグ 4 本（FR-S-12） ---

func (h *Handler) DebugSlack(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	h.jobs.Enqueue(r.Context(), "slack_webhook", job.TextArgs{Text: "test test test"})
	renderPlain(w, http.StatusOK, "slacktown")
}

func (h *Handler) DebugSyncAllSSHKeys(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	ri := info(r)
	accessToken := ""
	if ri.sess.GitHubLogin != nil {
		accessToken = ri.sess.GitHubLogin.Token
	}
	h.jobs.Enqueue(r.Context(), "sync_ssh_keys_of_all_contestants", job.SyncSSHKeysArgs{AccessToken: accessToken})
	renderPlain(w, http.StatusOK, "enqueued")
}

func (h *Handler) DebugSyncAllDiscordStats(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	h.jobs.Enqueue(r.Context(), "sync_discord_member_state_of_all_contestants", struct{}{})
	renderPlain(w, http.StatusOK, "enqueued")
}

// DebugLongRunningCheck だけ認証なし（notifyloop サービスが外から叩く）。
func (h *Handler) DebugLongRunningCheck(w http.ResponseWriter, r *http.Request) {
	h.jobs.Enqueue(r.Context(), "long_running_check", struct{}{})
	renderPlain(w, http.StatusOK, "enqueud")
}
