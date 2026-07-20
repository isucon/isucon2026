package contract_test

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUS3InteractionOperationsExist(t *testing.T) {
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
	for path, methods := range map[string][]string{"/posts/{postId}/realmojis": {"get", "post"}, "/realmojis/{realMojiId}": {"delete"}, "/posts/{postId}/comments": {"get", "post"}, "/comments/{commentId}": {"delete"}} {
		item := paths[path].(map[string]any)
		for _, method := range methods {
			if _, ok := item[method]; !ok {
				t.Errorf("missing %s %s", method, path)
			}
		}
	}
}
