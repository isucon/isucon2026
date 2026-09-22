package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentityAndBlob(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newHandler(serverConfig{ID: "server2", MaxBlobBytes: 1024, MaxFetchBytes: 1024}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Add("X-Country-Code", "JP")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var identity identityResponse
	if err := json.NewDecoder(response.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identity.Server != "server2" || len(identity.CountryValues) != 1 || identity.CountryValues[0] != "JP" {
		t.Fatalf("unexpected identity: %+v", identity)
	}

	blob, err := http.Get(server.URL + "/blob?bytes=257")
	if err != nil {
		t.Fatal(err)
	}
	defer blob.Body.Close()
	body, err := io.ReadAll(blob.Body)
	if err != nil {
		t.Fatal(err)
	}
	if blob.StatusCode != http.StatusOK || len(body) != 257 {
		t.Fatalf("blob status=%d bytes=%d", blob.StatusCode, len(body))
	}
}

func TestFetchAndRouteAreBounded(t *testing.T) {
	t.Parallel()
	peer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("z", 128)))
	}))
	defer peer.Close()
	handler := newHandler(serverConfig{
		ID: "server1", MaxBlobBytes: 1024, MaxFetchBytes: 256,
		AllowedPeerHosts: map[string]struct{}{"127.0.0.1": {}, "172.31.2.10": {}},
		RouteLookup: func(_ context.Context, target string) (string, error) {
			return target + " via 172.31.1.2 dev eth1", nil
		},
	})

	fetch := httptest.NewRecorder()
	handler.ServeHTTP(fetch, httptest.NewRequest(http.MethodGet, "/fetch?url="+peer.URL, nil))
	if fetch.Code != http.StatusOK {
		t.Fatalf("fetch status=%d body=%s", fetch.Code, fetch.Body.String())
	}
	var result fetchResponse
	if err := json.Unmarshal(fetch.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != http.StatusOK || result.Bytes != 128 || result.DurationMillis < 0 {
		t.Fatalf("unexpected fetch response: %+v", result)
	}

	route := httptest.NewRecorder()
	handler.ServeHTTP(route, httptest.NewRequest(http.MethodGet, "/route?target=172.31.2.10", nil))
	if route.Code != http.StatusOK || !strings.Contains(route.Body.String(), "via 172.31.1.2") {
		t.Fatalf("route status=%d body=%s", route.Code, route.Body.String())
	}
}

func TestFetchAndRouteRejectUnapprovedTargets(t *testing.T) {
	t.Parallel()
	handler := newHandler(serverConfig{
		ID: "server1", MaxBlobBytes: 64, MaxFetchBytes: 64,
		AllowedPeerHosts: map[string]struct{}{"172.31.2.10": {}},
		RouteLookup:      func(context.Context, string) (string, error) { return "", nil },
	})
	for _, target := range []string{
		"/fetch?url=https://172.31.2.10/",
		"/fetch?url=http://192.0.2.1/",
		"/fetch?url=not-a-url",
		"/route?target=192.0.2.1",
		"/route?target=not-an-ip",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d body=%s", target, recorder.Code, recorder.Body.String())
		}
	}
}

func TestBlobRejectsInvalidSize(t *testing.T) {
	t.Parallel()
	handler := newHandler(serverConfig{ID: "server1", MaxBlobBytes: 64, MaxFetchBytes: 64})
	for _, target := range []string{"/blob", "/blob?bytes=-1", "/blob?bytes=65", "/blob?bytes=nope"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d", target, recorder.Code)
		}
	}
}
