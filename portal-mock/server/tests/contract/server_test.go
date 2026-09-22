package contract

import (
	"io"
	"net/http"
	"testing"

	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestHealthCORSAndUnsupportedRoutes(t *testing.T) {
	server := testkit.NewServer(t, nil)
	response, err := server.HTTP.Client().Get(server.HTTP.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("health response: status=%d body=%q", response.StatusCode, body)
	}

	request, err := http.NewRequest(http.MethodOptions, server.HTTP.URL+"/portal_mock.v1.PortalMockService/GetState", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://localhost:5173")
	response, err = server.HTTP.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent ||
		response.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("CORS response: status=%d headers=%v", response.StatusCode, response.Header)
	}

	request, err = http.NewRequest(http.MethodPost, server.HTTP.URL+"/portal.v2.PortalRouterService/StreamDesired", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = server.HTTP.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unsupported version returned %d", response.StatusCode)
	}
}
