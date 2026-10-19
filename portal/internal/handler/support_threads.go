package handler

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
)

// These routes deliberately do not use the contestant clarification API.
func (h *Handler) SupportThreadsIndex(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	rows, err := h.db.QueryContext(r.Context(), `SELECT clarification_id,COALESCE(thread_id,''),status FROM discord_support_threads ORDER BY clarification_id DESC`)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	type entry struct {
		ID             int64
		Thread, Status string
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.ID, &e.Thread, &e.Status); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		entries = append(entries, e)
	}
	if rows.Err() != nil {
		http.Error(w, "database error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = template.Must(template.New("support").Parse(`<!doctype html><meta charset="utf-8"><title>Discord support logs</title><h1>Discordサポート記録（運営専用）</h1><ul>{{range .}}<li>clar #{{.ID}} — {{.Status}} — <a href="/admin/support_threads/{{.ID}}">保存ログ</a></li>{{end}}</ul>`)).Execute(w, entries)
}
func (h *Handler) SupportThreadTranscript(w http.ResponseWriter, r *http.Request) {
	if !h.requireStaffHTML(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var transcript []byte
	err := h.db.QueryRowContext(r.Context(), `SELECT transcript FROM discord_support_threads WHERE clarification_id=$1`, id).Scan(&transcript)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="discord-support-log.json"`)
	_ = json.NewEncoder(w).Encode(json.RawMessage(transcript))
}
