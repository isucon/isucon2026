package external

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GitHub のログイン名にはハイフンが使える。フォールバックの
// github.com/<login>.keys でハイフンを落とすと別人の鍵を取り込んでしまう。
func TestFetchSSHKeysKeepsHyphenInLogin(t *testing.T) {
	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		_, _ = w.Write([]byte("ssh-ed25519 AAAA one\n\nssh-ed25519 BBBB two\n"))
	}))
	defer srv.Close()

	g := &GitHub{KeysBaseURL: srv.URL}
	keys, err := g.FetchSSHKeys(context.Background(), "john-doe", "")
	if err != nil {
		t.Fatal(err)
	}
	if requested != "/john-doe.keys" {
		t.Errorf("requested path = %q, want /john-doe.keys", requested)
	}
	if len(keys) != 2 || keys[0] != "ssh-ed25519 AAAA one" || keys[1] != "ssh-ed25519 BBBB two" {
		t.Errorf("keys = %v", keys)
	}
}

// 不正なログイン名（GitHub のユーザー名規則に合わない）は外に取りにいかず失敗する。
func TestFetchSSHKeysRejectsInvalidLogin(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("ssh-ed25519 AAAA\n"))
	}))
	defer srv.Close()

	g := &GitHub{KeysBaseURL: srv.URL}
	for _, login := range []string{"", "../evil", "john--doe", "-john", "john-", "with space", "日本語"} {
		if _, err := g.FetchSSHKeys(context.Background(), login, ""); err == nil {
			t.Errorf("login %q must be rejected", login)
		}
	}
	if requests != 0 {
		t.Errorf("no request must be sent for invalid logins, got %d", requests)
	}
}

func TestValidGitHubLogin(t *testing.T) {
	for _, ok := range []string{"a", "john-doe", "John1", "a-b-c", "x0123456789012345678901234567890123456789"[:39]} {
		if !validGitHubLogin(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, ng := range []string{"", "-a", "a-", "a--b", "a_b", "a.b", "x0123456789012345678901234567890123456789"} {
		if validGitHubLogin(ng) {
			t.Errorf("%q should be invalid", ng)
		}
	}
}
