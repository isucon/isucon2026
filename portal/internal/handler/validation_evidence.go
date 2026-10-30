package handler

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/isucon/isucon2026/portal/internal/model"
)

// BenchValidation stores private restart evidence outside participant machines.
// A running job can write its own evidence once, or read the source selected
// when its post-validation job was enqueued. Handles never authorize other jobs.
func (h *Handler) BenchValidation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderPlain(w, 400, "invalid job")
		return
	}
	j, err := model.FindJob(r.Context(), h.db, id)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if j == nil || !j.Handle.Valid || tok == "" || !secureCompare(j.Handle.String, tok) {
		renderPlain(w, 401, "invalid job handle")
		return
	}
	if j.Status != model.JobRunning {
		renderPlain(w, 409, "job is not running")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPut {
		if j.PostValidation {
			renderPlain(w, 409, "validation cannot replace evidence")
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil || !json.Valid(data) {
			renderPlain(w, 400, "invalid evidence")
			return
		}
		var envelope struct {
			Version int               `json:"version"`
			Users   []json.RawMessage `json:"users"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.Version != 1 || len(envelope.Users) == 0 || len(envelope.Users) > 12 {
			renderPlain(w, 400, "invalid evidence")
			return
		}
		result, err := h.db.ExecContext(r.Context(), `INSERT INTO benchmark_validation_snapshots(job_id,data) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, string(data))
		if err != nil {
			renderInternalError(w, err)
			return
		}
		n, err := result.RowsAffected()
		if err != nil {
			renderInternalError(w, err)
			return
		}
		if n != 1 {
			renderPlain(w, 409, "evidence already saved")
			return
		}
		w.WriteHeader(204)
		return
	}
	if !j.PostValidation {
		renderPlain(w, 409, "not a validation job")
		return
	}
	var data []byte
	err = h.db.QueryRowContext(r.Context(), `SELECT s.data FROM benchmark_jobs j JOIN benchmark_jobs source ON source.id=j.validation_source_job_id AND source.team_id=j.team_id JOIN benchmark_validation_snapshots s ON s.job_id=source.id JOIN benchmark_results r ON r.benchmark_job_id=source.id WHERE j.id=$1 AND r.passed=true AND r.finished=true AND source.status=4 AND source.post_validation=false`, id).Scan(&data)
	if err == sql.ErrNoRows {
		renderPlain(w, 409, "no successful source evidence")
		return
	}
	if err != nil {
		renderInternalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
