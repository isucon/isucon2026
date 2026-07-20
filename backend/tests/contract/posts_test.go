package contract_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestUS1PostAndFeedContract(t *testing.T) {
	document := loadContract(t).Document()
	expected := map[string][]string{
		"/posting-window/current": {"GET"},
		"/posts":                  {"POST"},
		"/posts/me/current":       {"GET"},
		"/posts/{postId}":         {"GET", "PATCH", "DELETE"},
		"/feeds/{feed}":           {"GET"},
	}
	for path, methods := range expected {
		item := document.Paths.Value(path)
		if item == nil {
			t.Fatalf("missing path %s", path)
		}
		for _, method := range methods {
			operation := item.GetOperation(method)
			if operation == nil || operation.OperationID == "" {
				t.Errorf("missing %s %s operation", method, path)
			}
			assertMutationContract(t, method, operation)
		}
	}
	feed := document.Paths.Value("/feeds/{feed}").Get.Parameters.GetByInAndName("path", "feed")
	if feed == nil || feed.Schema == nil || feed.Schema.Value == nil {
		t.Fatal("feed path enum is missing")
	}
	wantFeeds := map[string]bool{"friends": true, "friends-of-friends": true, "public": true, "following": true}
	for _, value := range feed.Schema.Value.Enum {
		if text, ok := value.(string); ok {
			delete(wantFeeds, text)
		}
	}
	if len(wantFeeds) != 0 {
		t.Errorf("missing feed variants: %v", wantFeeds)
	}
	post := document.Components.Schemas["Post"]
	for _, field := range []string{"id", "author", "postingWindowId", "imageUrl", "caption", "visibility", "location", "postedAt", "onTime", "lateSeconds"} {
		if post == nil || post.Value == nil || !contains(post.Value.Required, field) {
			t.Errorf("Post does not require %s", field)
		}
	}
}

func assertMutationContract(t *testing.T, method string, operation *openapi3.Operation) {
	t.Helper()
	mutation := method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
	hasKey := false
	for _, parameter := range operation.Parameters {
		if parameter.Ref == "#/components/parameters/IdempotencyKey" {
			hasKey = true
		}
	}
	if mutation && (!hasKey || operation.Responses.Value("409") == nil) {
		t.Errorf("%s %s lacks idempotency/409", method, operation.OperationID)
	}
	if !mutation && hasKey {
		t.Errorf("read %s unexpectedly has Idempotency-Key", operation.OperationID)
	}
}
