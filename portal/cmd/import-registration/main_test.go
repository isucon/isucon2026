package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRehearsalDatabaseName(t *testing.T) {
	for _, name := range []string{"isuxportal", "postgres", "rehearsal_", "rehearsal_bad;DROP DATABASE isuxportal", "rehearsal_../x"} {
		if err := validateRehearsalDatabase(name); err == nil {
			t.Errorf("unsafe database name accepted: %q", name)
		}
	}
	if err := validateRehearsalDatabase("rehearsal_test"); err != nil {
		t.Fatal(err)
	}
}

func TestReadPrivateS3Snapshot(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_REGION", "ap-northeast-1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/private-bucket/rehearsal/fixture/snapshot.json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("S3 request was not signed")
		}
		io.WriteString(w, `{"version":1}`)
	}))
	defer server.Close()
	t.Setenv("AWS_ENDPOINT_URL_S3", server.URL)
	r, err := openSnapshot(context.Background(), "s3://private-bucket/rehearsal/fixture/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil || string(data) != `{"version":1}` {
		t.Fatalf("snapshot read failed: %v", err)
	}
}
