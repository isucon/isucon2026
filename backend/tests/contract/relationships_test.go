package contract_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUS2RelationshipOperationsExist(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "..", "specs", "001-isureal-social-app", "contracts", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	wanted := map[string][]string{"/users": {"get"}, "/users/{userId}": {"get"}, "/friend-requests": {"post"}, "/friend-requests/{requestId}/{action}": {"post"}, "/relationships/{kind}": {"get"}, "/friendships/{userId}": {"delete"}, "/follows/{userId}": {"put", "delete"}, "/blocks/{userId}": {"put", "delete"}, "/admin/auth/login": {"post"}, "/admin/users/{userId}/certification": {"put", "delete"}}
	for path, methods := range wanted {
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("missing %s", path)
			continue
		}
		for _, method := range methods {
			if _, ok := item[method]; !ok {
				t.Errorf("missing %s %s", method, path)
			}
		}
	}
}
