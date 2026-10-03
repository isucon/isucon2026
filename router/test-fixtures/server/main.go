package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type serverConfig struct {
	ID               string
	ListenAddress    string
	MaxBlobBytes     int
	MaxFetchBytes    int
	AllowedPeerHosts map[string]struct{}
	HTTPClient       *http.Client
	RouteLookup      func(context.Context, string) (string, error)
}

type identityResponse struct {
	Server        string   `json:"server"`
	CountryValues []string `json:"country_values"`
}

type fetchResponse struct {
	Status         int    `json:"status"`
	Bytes          int    `json:"bytes"`
	SHA256         string `json:"sha256"`
	DurationMillis int64  `json:"duration_millis"`
}

type routeResponse struct {
	Target string `json:"target"`
	Route  string `json:"route"`
}

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           newHandler(config),
		ReadHeaderTimeout: 3 * time.Second,
	}
	log.Printf("fixture server %s listening on %s", config.ID, config.ListenAddress)
	log.Fatal(server.ListenAndServe())
}

func newHandler(config serverConfig) http.Handler {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if config.RouteLookup == nil {
		config.RouteLookup = routeLookup
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /identity", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, identityResponse{
			Server: config.ID, CountryValues: request.Header.Values("X-Country-Code"),
		})
	})
	mux.HandleFunc("GET /blob", func(writer http.ResponseWriter, request *http.Request) {
		size, err := strconv.Atoi(request.URL.Query().Get("bytes"))
		if err != nil || size < 0 || size > config.MaxBlobBytes || request.URL.Query().Get("bytes") == "" {
			http.Error(writer, "invalid bytes", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Header().Set("Content-Length", strconv.Itoa(size))
		chunk := make([]byte, 32*1024)
		for index := range chunk {
			chunk[index] = byte('a' + index%26)
		}
		remaining := size
		for remaining > 0 {
			writeSize := min(remaining, len(chunk))
			if _, err := writer.Write(chunk[:writeSize]); err != nil {
				return
			}
			remaining -= writeSize
		}
	})
	mux.HandleFunc("GET /fetch", func(writer http.ResponseWriter, request *http.Request) {
		target, err := validateFetchTarget(request.URL.Query().Get("url"), config.AllowedPeerHosts)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		started := time.Now()
		peerRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, target.String(), nil)
		if err != nil {
			http.Error(writer, "invalid peer request", http.StatusBadRequest)
			return
		}
		response, err := config.HTTPClient.Do(peerRequest)
		if err != nil {
			http.Error(writer, "peer request failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, int64(config.MaxFetchBytes)+1))
		if err != nil || len(body) > config.MaxFetchBytes {
			http.Error(writer, "peer response exceeded limit", http.StatusBadGateway)
			return
		}
		digest := sha256.Sum256(body)
		writeJSON(writer, http.StatusOK, fetchResponse{
			Status: response.StatusCode, Bytes: len(body), SHA256: fmt.Sprintf("%x", digest[:]), DurationMillis: time.Since(started).Milliseconds(),
		})
	})
	mux.HandleFunc("GET /route", func(writer http.ResponseWriter, request *http.Request) {
		target := request.URL.Query().Get("target")
		address, err := netip.ParseAddr(target)
		if err != nil || !address.Is4() {
			http.Error(writer, "invalid target", http.StatusBadRequest)
			return
		}
		if _, ok := config.AllowedPeerHosts[target]; !ok {
			http.Error(writer, "target is not an approved peer", http.StatusBadRequest)
			return
		}
		route, err := config.RouteLookup(request.Context(), target)
		if err != nil {
			http.Error(writer, "route lookup failed", http.StatusInternalServerError)
			return
		}
		writeJSON(writer, http.StatusOK, routeResponse{Target: target, Route: route})
	})
	return mux
}

func loadConfig() (serverConfig, error) {
	config := serverConfig{
		ID:               os.Getenv("FIXTURE_SERVER_ID"),
		ListenAddress:    envOr("FIXTURE_LISTEN_ADDRESS", "0.0.0.0:8080"),
		MaxBlobBytes:     8 << 20,
		MaxFetchBytes:    8 << 20,
		AllowedPeerHosts: make(map[string]struct{}),
	}
	if config.ID != "app1" && config.ID != "app2" && config.ID != "app3" && config.ID != "db1" {
		return serverConfig{}, fmt.Errorf("FIXTURE_SERVER_ID must be app1, app2, app3, or db1")
	}
	for _, host := range strings.Split(envOr("FIXTURE_PEER_HOSTS", "172.31.1.10,172.31.2.10,172.31.3.10"), ",") {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is4() {
			return serverConfig{}, fmt.Errorf("invalid FIXTURE_PEER_HOSTS value %q", host)
		}
		config.AllowedPeerHosts[host] = struct{}{}
	}
	return config, nil
}

func validateFetchTarget(rawURL string, allowed map[string]struct{}) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "http" || target.Hostname() == "" || target.User != nil {
		return nil, fmt.Errorf("invalid peer URL")
	}
	if _, ok := allowed[target.Hostname()]; !ok {
		return nil, fmt.Errorf("peer host is not approved")
	}
	return target, nil
}

func routeLookup(ctx context.Context, target string) (string, error) {
	command := exec.CommandContext(ctx, "ip", "-4", "route", "get", target)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip route get: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
