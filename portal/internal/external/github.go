package external

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// GitHub は SSH 鍵の取得（SyncSshKeysOfContestantJob 相当）を担う。
type GitHub struct {
	APIBaseURL string
	// KeysBaseURL は https://github.com のフォールバック（<login>.keys）
	KeysBaseURL string
}

var loginSanitizer = regexp.MustCompile(`[^a-zA-Z0-9]`)

// FetchSSHKeys はユーザーの公開鍵一覧を返す。
// まず API（ユーザー token）を試し、失敗したら github.com/<login>.keys にフォールバックする。
func (g *GitHub) FetchSSHKeys(ctx context.Context, githubLogin, accessToken string) ([]string, error) {
	if accessToken != "" {
		keys, err := g.fetchViaAPI(ctx, githubLogin, accessToken)
		if err == nil {
			return keys, nil
		}
	}
	return g.fetchViaKeysURL(ctx, githubLogin)
}

func (g *GitHub) fetchViaAPI(ctx context.Context, login, token string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.APIBaseURL+"/users/"+login+"/keys", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api: %d", res.StatusCode)
	}
	var body []struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(body))
	for _, k := range body {
		keys = append(keys, k.Key)
	}
	return keys, nil
}

func (g *GitHub) fetchViaKeysURL(ctx context.Context, login string) ([]string, error) {
	base := g.KeysBaseURL
	if base == "" {
		base = "https://github.com"
	}
	sanitized := loginSanitizer.ReplaceAllString(login, "")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+sanitized+".keys", nil)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github keys: %d", res.StatusCode)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			keys = append(keys, line)
		}
	}
	return keys, nil
}
