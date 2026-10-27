package status

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Handler serves health and readiness. The Router is ready when it has an
// active snapshot and probe confirms that the proxy serves it.
func Handler(store *Store, probe func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "healthy"})
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, request *http.Request) {
		state := store.Snapshot()
		if state.ActiveSnapshot != "" {
			ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
			err := probe(ctx)
			cancel()
			state.ProxyServing = err == nil
			if err != nil {
				state.ProxyError = bounded(err.Error())
			}
		}
		state.Ready = state.ActiveSnapshot != "" && state.ProxyServing
		code := http.StatusOK
		if !state.Ready {
			code = http.StatusServiceUnavailable
		}
		writeJSON(writer, code, state)
	})
	return mux
}

func writeJSON(writer http.ResponseWriter, statusCode int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(value)
}
