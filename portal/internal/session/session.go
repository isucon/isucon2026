// Package session は Redis バックエンドのセッションを提供する。
// Rails（redis-actionpack + Marshal）とは互換しない（計画書 §1: クリーンカットオーバー）。
// Cookie には HMAC-SHA256 で署名したセッション ID のみを載せ、本体は Redis に JSON で置く。
package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	CookieName = "isuxportal_session"
	ttl        = 30 * 24 * time.Hour
)

// OAuthIdentity は session[:github_login] / session[:discord_login] 相当。
// 「ログインしたがチームがまだ無い」状態を表す（FR-AUTH-1/2）。
type OAuthIdentity struct {
	ID        string `json:"id"`
	Login     string `json:"login,omitempty"` // GitHub のみ
	Tag       string `json:"tag,omitempty"`   // Discord のみ
	Token     string `json:"token,omitempty"`
	AvatarURL string `json:"avatar_url"`
}

// Session はセッションに載る値。Rails と同じ意味論を保つ（計画書 §1）。
type Session struct {
	ContestantID int64          `json:"contestant_id,omitempty"`
	Staff        bool           `json:"staff,omitempty"`
	BypassToken  string         `json:"bypass_token,omitempty"`
	GitHubLogin  *OAuthIdentity `json:"github_login,omitempty"`
	DiscordLogin *OAuthIdentity `json:"discord_login,omitempty"`
	BackTo       string         `json:"back_to,omitempty"`
	CSRFToken    string         `json:"csrf_token,omitempty"`
	OAuthState   string         `json:"oauth_state,omitempty"`

	id    string
	dirty bool
}

// MarkDirty は保存が必要なことを記録する。値を書き換えたら必ず呼ぶ。
func (s *Session) MarkDirty() { s.dirty = true }

func (s *Session) Dirty() bool { return s.dirty }

// EnsureCSRFToken は CSRF トークンを保証する（CT-2）。
func (s *Session) EnsureCSRFToken() string {
	if s.CSRFToken == "" {
		s.CSRFToken = randomToken(32)
		s.MarkDirty()
	}
	return s.CSRFToken
}

type Store struct {
	redis  *redis.Client
	secret []byte
}

func NewStore(redisURL, secret string) (*Store, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	return &Store{redis: redis.NewClient(opts), secret: []byte(secret)}, nil
}

func (st *Store) Ping(ctx context.Context) error {
	return st.redis.Ping(ctx).Err()
}

func (st *Store) Redis() *redis.Client { return st.redis }

// Load はリクエストの Cookie からセッションを復元する。無ければ空のセッション。
func (st *Store) Load(ctx context.Context, r *http.Request) *Session {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return &Session{}
	}
	id, ok := st.verifyCookie(cookie.Value)
	if !ok {
		return &Session{}
	}
	data, err := st.redis.Get(ctx, "session:"+id).Bytes()
	if err != nil {
		return &Session{}
	}
	s := &Session{}
	if err := json.Unmarshal(data, s); err != nil {
		return &Session{}
	}
	s.id = id
	return s
}

// Save はセッションを Redis に書き、必要なら Set-Cookie を発行する。
func (st *Store) Save(ctx context.Context, w http.ResponseWriter, s *Session) error {
	if !s.dirty {
		return nil
	}
	if s.id == "" {
		s.id = randomToken(32)
		http.SetCookie(w, &http.Cookie{
			Name:     CookieName,
			Value:    st.signCookie(s.id),
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return st.redis.Set(ctx, "session:"+s.id, data, ttl).Err()
}

func (st *Store) signCookie(id string) string {
	mac := hmac.New(sha256.New, st.secret)
	mac.Write([]byte(id))
	return id + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (st *Store) verifyCookie(v string) (string, bool) {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, st.secret)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	return parts[0], true
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(errors.New("crypto/rand failed: " + err.Error()))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
