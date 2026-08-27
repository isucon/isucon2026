package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"

	pbcontestant "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/contestant"
	"github.com/isucon/isucon2026/portal/internal/cloudformation"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// ContestantDashboard: GET /api/contestant/dashboard
func (h *Handler) ContestantDashboard(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "max-age=20, private")

	digest, wire, cached, err := h.cache.Read(ctx, fmt.Sprintf("dashboard-v2:contestant:team-%d", team.ID))
	if err != nil {
		return err
	}
	if cached {
		if staleCheckETag(w, r, etagFromDigest(digest[:16])) {
			renderRawPB(w, http.StatusOK, "isuxportal.proto.services.contestant.DashboardResponse", wire)
		}
		return nil
	}

	// キャッシュミス時は自チーム最新結果の updated_at を etag にフォールバック
	var latest sql.NullTime
	if err := h.db.QueryRowContext(ctx,
		`SELECT MAX(updated_at) FROM benchmark_results WHERE team_id = ?`, team.ID).Scan(&latest); err != nil {
		return err
	}
	if latest.Valid {
		if !staleCheck(w, r, []byte(latest.Time.UTC().Format("2006-01-02T15:04:05.000000Z"))) {
			return nil
		}
	}

	_, item, err := contest.BuildLeaderboard(ctx, h.db, ri.timing, ri.now, contest.LeaderboardParams{
		TeamID: team.ID, Solo: true, SoloItem: true, History: true,
	})
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbcontestant.DashboardResponse{LeaderboardItem: item})
	return nil
}

// ContestantJobsIndex: GET /api/contestant/benchmark_jobs（FR-C-3）
func (h *Handler) ContestantJobsIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()

	query := `SELECT ` + jobColumnsWithScore + `
		FROM benchmark_jobs LEFT JOIN benchmark_results ON benchmark_results.benchmark_job_id = benchmark_jobs.id
		WHERE benchmark_jobs.post_validation = 0 AND benchmark_jobs.team_id = ?`
	args := []any{team.ID}
	if status := q.Get("status"); status != "" {
		st, err := parseJobStatus(status)
		if err != nil {
			return err
		}
		query += ` AND benchmark_jobs.status = ?`
		args = append(args, st)
	}
	query += ` ORDER BY benchmark_jobs.id DESC`
	if limit := q.Get("limit"); limit != "" {
		n, err := parseInt64(limit)
		if err == nil && n != 0 {
			query += fmt.Sprintf(` LIMIT %d`, n)
		}
	}

	jobs, scores, err := queryJobsWithScore(ctx, h.db, query, args...)
	if err != nil {
		return err
	}
	res := &pbcontestant.ListBenchmarkJobsResponse{}
	for i, j := range jobs {
		res.Jobs = append(res.Jobs, j.ToPB(model.JobToPBOptions{Score: scores[i]}))
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// ContestantJobsShow: GET /api/contestant/benchmark_jobs/:id
func (h *Handler) ContestantJobsShow(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	jobs, err := model.QueryJobs(ctx, h.db, `SELECT `+jobColumns+` FROM benchmark_jobs WHERE id = ? AND team_id = ? AND post_validation = 0`, id, team.ID)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return &model.NotFoundError{}
	}
	pb, err := model.JobToPBFull(ctx, h.db, jobs[0], false, nil)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbcontestant.GetBenchmarkJobResponse{Job: pb})
	return nil
}

// ContestantJobsCreate: POST /api/contestant/benchmark_jobs（FR-C-2: 同時 1 件、DM-4）
func (h *Handler) ContestantJobsCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	req := &pbcontestant.EnqueueBenchmarkJobRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	target, err := model.FindInstanceByTeamAndID(ctx, h.db, team.ID, req.TargetId)
	if err != nil {
		return err
	}
	if target == nil {
		return &model.NotFoundError{}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	enqueuedBy := sql.NullInt64{}
	if c != nil {
		enqueuedBy = sql.NullInt64{Int64: c.ID, Valid: true}
	}
	jobRow, err := model.CreateJob(ctx, tx, team.ID, team.AvailabilityZone.String,
		sql.NullInt64{Int64: target.ID, Valid: true}, enqueuedBy, false)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	pb, err := model.JobToPBFull(ctx, h.db, jobRow, false, nil)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbcontestant.EnqueueBenchmarkJobResponse{Job: pb})
	return nil
}

// ContestantClarsIndex: GET /api/contestant/clarifications（FR-C-4: 自チーム分 + 公開分）
func (h *Handler) ContestantClarsIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	clars, err := model.QueryClarifications(ctx, h.db, `
		SELECT `+clarColumnsSQL+` FROM clarifications
		WHERE team_id = ? OR (answered_at IS NOT NULL AND disclosed = 1)
		ORDER BY created_at DESC`, team.ID)
	if err != nil {
		return err
	}
	res := &pbcontestant.ListClarificationsResponse{}
	for _, clar := range clars {
		showOriginal := !clar.Admin && clar.TeamID.Valid && clar.TeamID.Int64 == team.ID
		pb, err := model.ClarificationToPBWithTeam(ctx, h.db, clar, showOriginal)
		if err != nil {
			return err
		}
		res.Clarifications = append(res.Clarifications, pb)
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// ContestantClarsCreate: POST /api/contestant/clarifications（FR-C-4）
// isucon14 の実装は感想戦向けに投稿を封じていたが、要件（FR-C-4）に従い投稿を受け付ける。
func (h *Handler) ContestantClarsCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	req := &pbcontestant.RequestClarificationRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if req.Question == "" {
		return &model.InvalidError{Messages: []string{"Question can't be blank"}}
	}
	res, err := h.db.ExecContext(ctx, `
		INSERT INTO clarifications (team_id, question, original_question, admin, created_at, updated_at)
		VALUES (?, ?, ?, 0, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`, team.ID, req.Question, req.Question)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	h.jobs.Enqueue(ctx, "clarification_acceptance", job.ClarificationAcceptanceArgs{ClarificationID: id})

	clar, err := model.FindClarification(ctx, h.db, id)
	if err != nil {
		return err
	}
	pb, err := model.ClarificationToPBWithTeam(ctx, h.db, clar, true)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbcontestant.RequestClarificationResponse{Clarification: pb})
	return nil
}

// ContestantNotificationsIndex: GET /api/contestant/notifications（FR-C-5）
// after 指定があれば id > after、無ければサーバ側の read を使い、返したものは既読化する。
func (h *Handler) ContestantNotificationsIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	after := sql.NullInt64{}
	if v := r.URL.Query().Get("after"); v != "" {
		if id, err := parseInt64(v); err == nil {
			after = sql.NullInt64{Int64: id, Valid: true}
		}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	contestantID := int64(0)
	if c != nil {
		contestantID = c.ID
	}
	notifications, err := model.ListNotifications(ctx, tx, contestantID, after)
	if err != nil {
		return err
	}
	res := &pbcontestant.ListNotificationsResponse{}
	for _, n := range notifications {
		pb, err := n.ToPB()
		if err != nil {
			return err
		}
		res.Notifications = append(res.Notifications, pb)
	}
	if len(notifications) > 0 {
		ids := make([]any, 0, len(notifications))
		ph := ""
		for i, n := range notifications {
			if i > 0 {
				ph += ","
			}
			ph += "?"
			ids = append(ids, n.ID)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE notifications SET `read` = 1 WHERE id IN ("+ph+")", ids...); err != nil {
			return err
		}
	}

	var lastClarID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(id) FROM clarifications
		WHERE answered_at IS NOT NULL AND (team_id = ? OR (answered_at IS NOT NULL AND disclosed = 1))`,
		team.ID).Scan(&lastClarID); err != nil {
		return err
	}
	res.LastAnsweredClarificationId = lastClarID.Int64

	if err := tx.Commit(); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// ContestantNotificationsCreate: POST /api/contestant/notifications（テスト通知の発火）
func (h *Handler) ContestantNotificationsCreate(w http.ResponseWriter, r *http.Request) error {
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	if c == nil {
		return &model.NotFoundError{}
	}
	h.jobs.Enqueue(r.Context(), "fire_test_notification", job.ContestantArgs{ContestantID: c.ID})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ContestantNotificationsDestroy: DELETE /api/contestant/notifications/:id（既読化）
func (h *Handler) ContestantNotificationsDestroy(w http.ResponseWriter, r *http.Request) error {
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	if c == nil {
		return &model.NotFoundError{}
	}
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	if _, err := h.db.ExecContext(r.Context(),
		"UPDATE notifications SET `read` = 1 WHERE id = ? AND contestant_id = ?", id, c.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ContestantPushSubscribe: POST /api/contestant/push_subscriptions（FR-C-6）
func (h *Handler) ContestantPushSubscribe(w http.ResponseWriter, r *http.Request) error {
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	if c == nil {
		return &model.NotFoundError{}
	}
	req := &pbcontestant.SubscribeNotificationRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if req.Endpoint == "" || req.P256Dh == "" || req.Auth == "" {
		return &model.InvalidError{Messages: []string{"Endpoint can't be blank"}}
	}
	if u, err := url.Parse(req.Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return &model.InvalidError{Messages: []string{"Endpoint must be a valid URI"}}
	}
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO push_subscriptions (contestant_id, endpoint, p256dh, auth, created_at, updated_at)
		VALUES (?, ?, ?, ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		c.ID, req.Endpoint, req.P256Dh, req.Auth); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbcontestant.SubscribeNotificationResponse{})
	return nil
}

// ContestantPushUnsubscribe: DELETE /api/contestant/push_subscriptions
func (h *Handler) ContestantPushUnsubscribe(w http.ResponseWriter, r *http.Request) error {
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	if c == nil {
		return &model.NotFoundError{}
	}
	req := &pbcontestant.UnsubscribeNotificationRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM push_subscriptions WHERE contestant_id = ? AND endpoint = ? LIMIT 1`,
		c.ID, req.Endpoint); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbcontestant.UnsubscribeNotificationResponse{})
	return nil
}

// ContestantCloudFormation: GET /api/contestant/cloud_formation（FR-C-7）
func (h *Handler) ContestantCloudFormation(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	template, err := cloudformation.ContestTemplate(h.cfg, team.ID, team.AvailabilityZone.String, ri.now)
	if err != nil {
		return errNotFound(err.Error())
	}
	renderPB(w, http.StatusOK, &pbcontestant.GetCloudFormationResponse{Template: template})
	return nil
}
