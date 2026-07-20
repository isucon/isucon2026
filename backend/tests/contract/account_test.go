package contract_test

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUS4AccountContractsAndDeliberateAbsence(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "specs", "001-isureal-social-app", "contracts", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	wanted := map[string][]string{"/me": {"get", "patch"}, "/me/profile-image": {"put", "delete"}, "/me/account-deletion": {"post"}, "/memories": {"get"}, "/memories/{date}": {"get"}}
	for path, methods := range wanted {
		item := paths[path].(map[string]any)
		for _, method := range methods {
			if _, ok := item[method]; !ok {
				t.Errorf("missing %s %s", method, path)
			}
		}
	}
	for _, absent := range []string{"/auth/password", "/auth/password-reset", "/me/username"} {
		if _, ok := paths[absent]; ok {
			t.Errorf("forbidden initial-release operation %s", absent)
		}
	}
}
