package contract_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/backend/internal/httpapi"
)

func TestOpenAPIStateChangesAndProblemTaxonomy(t *testing.T) {
	validator := loadContract(t)
	document := validator.Document()
	for path, item := range document.Paths.Map() {
		for method, operation := range item.Operations() {
			hasKey := false
			for _, parameter := range operation.Parameters {
				if parameter.Ref == "#/components/parameters/IdempotencyKey" {
					hasKey = true
				}
			}
			stateChanging := method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
			if stateChanging && !hasKey {
				t.Errorf("%s %s does not require Idempotency-Key", method, path)
			}
			if !stateChanging && hasKey {
				t.Errorf("%s %s unexpectedly accepts Idempotency-Key", method, path)
			}
			if stateChanging {
				if operation.Responses.Value("409") == nil {
					t.Errorf("%s %s does not declare 409", method, path)
				}
			}
		}
	}
	problem := document.Components.Schemas["Problem"].Value
	for _, required := range []string{"type", "title", "status", "code", "correlationId"} {
		if !contains(problem.Required, required) {
			t.Errorf("Problem does not require %s", required)
		}
	}
	for name, status := range map[string]string{"BadRequest": "400", "Unauthorized": "401", "Forbidden": "403", "NotFound": "404", "Conflict": "409", "PayloadTooLarge": "413", "UnsupportedMedia": "415", "InternalError": "500", "ServiceUnavailable": "503"} {
		response := document.Components.Responses[name]
		if response == nil || response.Value == nil || response.Value.Content.Get("application/problem+json") == nil {
			t.Errorf("%s response (%s) is not an RFC 9457 problem", name, status)
		}
	}
}

func loadContract(t *testing.T) *httpapi.OpenAPIValidator {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(filename), "..", "..", "..", "specs", "001-isureal-social-app", "contracts", "openapi.yaml")
	validator, err := httpapi.LoadOpenAPI(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
