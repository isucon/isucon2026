package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
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
	server := httptest.NewServer(newHandler(serverConfig{ID: "app2", MaxBlobBytes: 1024, MaxFetchBytes: 1024}))
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
	if identity.Server != "app2" || len(identity.CountryValues) != 1 || identity.CountryValues[0] != "JP" {
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
		ID: "app1", MaxBlobBytes: 1024, MaxFetchBytes: 256,
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
		ID: "app1", MaxBlobBytes: 64, MaxFetchBytes: 64,
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
	handler := newHandler(serverConfig{ID: "app1", MaxBlobBytes: 64, MaxFetchBytes: 64})
	for _, target := range []string{"/blob", "/blob?bytes=-1", "/blob?bytes=65", "/blob?bytes=nope"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d", target, recorder.Code)
		}
	}
}

func TestImageAndExportReportRouterHeaders(t *testing.T) {
	t.Parallel()
	handler := newHandler(serverConfig{ID: "app1", MaxBlobBytes: 1 << 20, MaxFetchBytes: 1024})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/images/photo?bytes=5000", nil)
	request.Header.Set("X-Benchmark-Request-Id", "1")
	request.Header.Set("X-Isureal-Limit", "1")
	handler.ServeHTTP(recorder, request)
	if saw := recorder.Header().Get("X-Fixture-Saw"); saw != "X-Benchmark-Request-Id,X-Isureal-Limit" || !bytes.Equal(recorder.Body.Bytes(), fixtureImage("photo", 5000)) {
		t.Fatalf("image: saw %q, %d bytes", saw, recorder.Body.Len())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/images/photo?bytes=5000&encoding=gzip", nil))
	gz, err := gzip.NewReader(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(gz)
	if err != nil || recorder.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(decoded, fixtureImage("photo", 5000)) {
		t.Fatalf("gzip image: %v, Content-Encoding %q, %d bytes", err, recorder.Header().Get("Content-Encoding"), len(decoded))
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/me/export", nil))
	data := recorder.Body.Bytes()
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) != 2 || recorder.Header().Get("X-Fixture-Saw") != "" {
		t.Fatalf("export: %v, saw %q", err, recorder.Header().Get("X-Fixture-Saw"))
	}
}
