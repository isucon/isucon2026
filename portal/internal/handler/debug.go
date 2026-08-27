package handler

import (
	"net/http"
	"time"

	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
)

// DebugLeaderboard: GET /debug/leaderboard （テストモード限定）
// Contest.leaderboard のキーワード引数をクエリパラメータで受け、
// HTTP 化した contest_spec の subject を成立させるための入口。
// 本体は production コードと同一の contest.BuildLeaderboard。
func (h *Handler) DebugLeaderboard(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	q := r.URL.Query()

	p := contest.LeaderboardParams{
		Admin:    q.Get("admin") == "1",
		Audience: q.Get("audience") == "1",
		Solo:     q.Get("solo") == "1",
		SoloItem: q.Get("solo_item") == "1",
		History:  q.Get("history") != "0",
	}
	if v := q.Get("team_id"); v != "" {
		id, err := parseInt64(v)
		if err != nil {
			renderError(w, http.StatusBadRequest, "BadRequest", "team_id: "+err.Error())
			return
		}
		p.TeamID = id
	}
	if v := q.Get("now"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			renderError(w, http.StatusBadRequest, "BadRequest", "now: "+err.Error())
			return
		}
		p.Now = &t
	}

	lb, item, err := contest.BuildLeaderboard(r.Context(), h.db, ri.timing, ri.now, p)
	if err != nil {
		renderInternalError(w, err)
		return
	}
	if item != nil {
		renderPB(w, http.StatusOK, item)
		return
	}
	renderPB(w, http.StatusOK, lb)
}

// DebugUpdateBestScores: POST /debug/update_best_scores （テストモード限定）
// MaintainBestBenchmarkResultOfTeamJob を全チームに適用する。
func (h *Handler) DebugUpdateBestScores(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	var now *time.Time
	if v := r.URL.Query().Get("now"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			renderError(w, http.StatusBadRequest, "BadRequest", "now: "+err.Error())
			return
		}
		now = &t
	}
	if err := contest.MaintainBestBenchmarkResults(r.Context(), h.db, ri.timing, now); err != nil {
		renderInternalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DebugFlushDB: POST /debug/flushdb（テストモード限定）
// RSpec の各 example 前に Redis（セッション + キャッシュ）を空にする。
func (h *Handler) DebugFlushDB(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Redis().FlushDB(r.Context()).Err(); err != nil {
		renderInternalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DebugUpdateContestantDashboard: POST /debug/update_contestant_dashboard（テストモード限定）
// UpdateContestantDashboardJob を明示パラメータで実行する。
func (h *Handler) DebugUpdateContestantDashboard(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	args := job.UpdateDashboardArgs{Frozen: q.Get("frozen") == "1"}
	if v := q.Get("team_id"); v != "" {
		id, err := parseInt64(v)
		if err != nil {
			renderError(w, http.StatusBadRequest, "BadRequest", "team_id: "+err.Error())
			return
		}
		args.TeamID = id
	}
	if err := h.jobs.UpdateContestantDashboard(r.Context(), args); err != nil {
		renderInternalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
