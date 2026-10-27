package isureal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestInitializeSurvey(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		success      bool
	}{
		{"success", "#!/bin/sh\nexit 0\n", true},
		{"failure", "#!/bin/sh\nexit 1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"go", "sql"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "sql", "init.sh"), []byte(tc.script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(filepath.Join(root, "go"))
			recorder := httptest.NewRecorder()
			ctx := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/api/initialize", nil), recorder)
			err := initializeHandler(nil)(ctx)
			if !tc.success {
				if err == nil {
					t.Fatal("initialization failure was ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || body["language"] != "go" || body["agent"] != "human" || body["harness"] != "hand" {
				t.Fatalf("status=%d body=%v", recorder.Code, body)
			}
		})
	}
}
