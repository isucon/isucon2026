package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/isucon/isucon2026/bench/internal/scenario"
)

type validationStore struct{ path, url, token string }

func (s validationStore) save(ctx context.Context, snapshot scenario.ValidationSnapshot) error {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if s.url != "" {
		_, err = s.request(ctx, http.MethodPut, body)
		return err
	}
	if s.path == "" {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".validation-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
func (s validationStore) load(ctx context.Context) (scenario.ValidationSnapshot, error) {
	var body []byte
	var err error
	if s.url != "" {
		body, err = s.request(ctx, http.MethodGet, nil)
	} else if s.path != "" {
		body, err = os.ReadFile(s.path)
	} else {
		return scenario.ValidationSnapshot{}, fmt.Errorf("post-validation evidence is required")
	}
	var saved scenario.ValidationSnapshot
	if err != nil {
		return saved, err
	}
	err = json.Unmarshal(body, &saved)
	return saved, err
}
func (s validationStore) request(ctx context.Context, method string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("invalid evidence endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("evidence request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("evidence HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20+1))
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("evidence too large")
	}
	return data, err
}
