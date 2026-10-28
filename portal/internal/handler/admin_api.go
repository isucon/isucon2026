package handler

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"time"

	pbadmin "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/admin"
	"github.com/isucon/isucon2026/portal/internal/cloudformation"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

const adminJobsPerPage = 15

// AdminDashboard: GET /api/admin/dashboard（FR-S-1）
func (h *Handler) AdminDashboard(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	w.Header().Set("Cache-Control", "max-age=15, private")

	var lb *pbresources.Leaderboard
	if _, wire, ok, err := h.cache.Read(ctx, "leaderboard-v2:admin"); err != nil {
		return err
	} else if ok {
		lb = &pbresources.Leaderboard{}
		if err := proto.Unmarshal(wire, lb); err != nil {
			return err
		}
	} else {
		lb, _, err = contest.BuildLeaderboard(ctx, h.db, ri.timing, ri.now, contest.LeaderboardParams{Admin: true, History: false})
		if err != nil {
			return err
		}
	}

	var clarCount int64
	var earliestClarAt sql.NullTime
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(created_at) FROM clarifications WHERE answered_at IS NULL AND admin = false`,
	).Scan(&clarCount, &earliestClarAt); err != nil {
		return err
	}

	res := &pbadmin.DashboardResponse{
		Leaderboard:                  lb,
		UnansweredClarificationCount: clarCount,
	}
	if earliestClarAt.Valid {
		res.EarliestUnansweredClarificationAt = timestamppb.New(earliestClarAt.Time)
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminSoloDashboard: GET /api/admin/dashboard/teams/:id
func (h *Handler) AdminSoloDashboard(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	teamID, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	w.Header().Set("Cache-Control", "max-age=20, private")

	digest, wire, cached, err := h.cache.Read(ctx, "dashboard-v2:admin:team-"+chiParam(r, "id"))
	if err != nil {
		return err
	}
	if cached {
		if staleCheckETag(w, r, etagFromDigest(digest[:16])) {
			renderRawPB(w, http.StatusOK, "isuxportal.proto.services.admin.SoloDashboardResponse", wire)
		}
		return nil
	}

	team, err := model.FindTeam(ctx, h.db, teamID)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}
	etag, err := contest.LeaderboardEtag(ctx, h.db, ri.timing, false, teamID)
	if err != nil {
		return err
	}
	if !staleCheck(w, r, etag) {
		return nil
	}
	_, item, err := contest.BuildLeaderboard(ctx, h.db, ri.timing, ri.now, contest.LeaderboardParams{
		Admin: true, TeamID: teamID, Solo: true, SoloItem: true, History: true,
	})
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.SoloDashboardResponse{LeaderboardItem: item})
	return nil
}

// AdminTeamsIndex: GET /api/admin/teams（FR-S-2）
func (h *Handler) AdminTeamsIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	rows, err := h.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.final_participation, t.is_hidden, t.student, t.withdrawn, t.disqualified, t.onsite_support_excluded, c.name
		FROM teams t LEFT JOIN contestants c ON c.team_id = t.id
		ORDER BY t.id ASC, c.id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var items []*pbadmin.ListTeamsResponse_TeamListItem
	var current *pbadmin.ListTeamsResponse_TeamListItem
	for rows.Next() {
		var teamID int64
		var name string
		var finalParticipation, hidden, student, withdrawn, disqualified, onsiteSupportExcluded bool
		var memberName *string
		if err := rows.Scan(&teamID, &name, &finalParticipation, &hidden, &student, &withdrawn, &disqualified, &onsiteSupportExcluded, &memberName); err != nil {
			return err
		}
		if current == nil || current.TeamId != teamID {
			current = &pbadmin.ListTeamsResponse_TeamListItem{
				TeamId:                teamID,
				Name:                  name,
				FinalParticipation:    finalParticipation,
				Hidden:                hidden,
				IsStudent:             student,
				Withdrawn:             withdrawn,
				Disqualified:          disqualified,
				OnsiteSupportExcluded: onsiteSupportExcluded,
			}
			items = append(items, current)
		}
		if memberName != nil {
			current.MemberNames = append(current.MemberNames, *memberName)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.ListTeamsResponse{Teams: items})
	return nil
}

// AdminTeamsShow: GET /api/admin/teams/:id
func (h *Handler) AdminTeamsShow(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	team, err := model.FindTeam(ctx, h.db, id)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}
	pb, err := model.TeamToPBWithMembers(ctx, h.db, team, true, true)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.GetTeamResponse{Team: pb})
	return nil
}

// AdminTeamsUpdate: PUT /api/admin/teams/:id（FR-S-2: 指定 Contestant のみ部分更新）
func (h *Handler) AdminTeamsUpdate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	req := &pbadmin.UpdateTeamRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if req.Team == nil || req.Team.Detail == nil {
		return errBadRequest("")
	}
	for _, c := range req.Contestants {
		if c.Detail == nil {
			return errBadRequest("")
		}
	}
	if req.Team.Id != id {
		return errBadRequest("")
	}
	if err := validateNameLength(req.Team.Name, "チーム名"); err != nil {
		return err
	}
	for _, c := range req.Contestants {
		if err := validateNameLength(c.Name, "選手名"); err != nil {
			return err
		}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	team, err := model.FindTeam(ctx, tx, id)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}
	members, err := model.TeamMembers(ctx, tx, id)
	if err != nil {
		return err
	}
	memberByID := map[int64]*model.Contestant{}
	for _, m := range members {
		memberByID[m.ID] = m
	}
	for _, c := range req.Contestants {
		if _, ok := memberByID[c.Id]; !ok {
			return &model.NotFoundError{}
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE teams SET name = $1, leader_id = $2, final_participation = $3, is_hidden = $4, withdrawn = $5, disqualified = $6, email_address = $7, onsite_support_excluded = $9, updated_at = now()
		WHERE id = $8`,
		req.Team.Name, sql.NullInt64{Int64: req.Team.LeaderId, Valid: req.Team.LeaderId != 0},
		req.Team.FinalParticipation, req.Team.Hidden, req.Team.Withdrawn, req.Team.Disqualified,
		req.Team.Detail.EmailAddress, id, req.Team.OnsiteSupportExcluded); err != nil {
		return err
	}
	for _, c := range req.Contestants {
		if _, err := tx.ExecContext(ctx, `
			UPDATE contestants SET name = $1, student = $2, in_person = $3, github_id = $4, github_login = $5, discord_id = $6, discord_tag = $7, updated_at = now()
			WHERE id = $8`,
			c.Name, c.Detail.IsStudent, c.Detail.IsInPerson, c.Detail.GithubId, c.Detail.GithubLogin,
			c.Detail.DiscordId, c.Detail.DiscordTag, c.Id); err != nil {
			return err
		}
	}
	if err := model.UpdateTeamStudentStatus(ctx, tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.UpdateTeamResponse{})
	return nil
}

// AdminCloudFormation: GET /api/admin/teams/:team_id/cloud_formation（type = test | contest）
func (h *Handler) AdminCloudFormation(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	teamID, ok := pathIDNamed(r, "team_id")
	if !ok {
		return &model.NotFoundError{}
	}
	team, err := model.FindTeam(r.Context(), h.db, teamID)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}

	var template string
	switch r.URL.Query().Get("type") {
	case "test":
		template, err = cloudformation.TestTemplate(h.cfg, team.ID, team.AvailabilityZone.String, ri.now)
	case "contest":
		template, err = cloudformation.ContestTemplate(h.cfg, team.ID, team.AvailabilityZone.String, ri.now)
	default:
		return errBadRequest("Unexpected type: " + r.URL.Query().Get("type"))
	}
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.GetCloudFormationResponse{Template: template})
	return nil
}

// AdminEnvChecks: GET /api/admin/teams/:team_id/env_checks（FR-S-5）
func (h *Handler) AdminEnvChecks(w http.ResponseWriter, r *http.Request) error {
	teamID, ok := pathIDNamed(r, "team_id")
	if !ok {
		return &model.NotFoundError{}
	}
	checks, err := model.TeamEnvChecks(r.Context(), h.db, teamID)
	if err != nil {
		return err
	}
	res := &pbadmin.ListEnvChecksResponse{}
	for _, e := range checks {
		res.EnvChecks = append(res.EnvChecks, e.ToPB())
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminJobsIndex: GET /api/admin/benchmark_jobs（FR-S-3: 絞り込み + ページング）
func (h *Handler) AdminJobsIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()

	where := "1=1"
	args := []any{}
	if v := q.Get("team_id"); v != "" {
		teamID, err := parseInt64(v)
		if err != nil {
			return errBadRequest("invalid team_id")
		}
		args = append(args, teamID)
		where += fmt.Sprintf(" AND benchmark_jobs.team_id = $%d", len(args))
	}
	if v := q.Get("status"); v != "" {
		st, err := parseJobStatus(v)
		if err != nil {
			return err
		}
		args = append(args, st)
		where += fmt.Sprintf(" AND benchmark_jobs.status = $%d", len(args))
	}
	join := `LEFT JOIN benchmark_results ON benchmark_results.benchmark_job_id = benchmark_jobs.id`
	if q.Get("failed_only") == "1" {
		join = `JOIN benchmark_results ON benchmark_results.benchmark_job_id = benchmark_jobs.id`
		where += " AND benchmark_results.passed = false"
	}

	var count int64
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM benchmark_jobs `+join+` WHERE `+where, args...).Scan(&count); err != nil {
		return err
	}
	maxPage := int64(math.Ceil(float64(count) / float64(adminJobsPerPage)))

	query := `SELECT ` + jobListColumns + ` FROM benchmark_jobs ` + join + jobEnqueuedByJoin + ` WHERE ` + where + ` ORDER BY benchmark_jobs.id DESC`
	if v := q.Get("page"); v != "" {
		page, err := parseInt64(v)
		if err == nil && page >= 0 {
			query += fmt.Sprintf(` LIMIT %d OFFSET %d`, adminJobsPerPage, page*adminJobsPerPage)
		}
	}

	jobs, options, err := queryJobList(ctx, h.db, query, args...)
	if err != nil {
		return err
	}
	res := &pbadmin.ListBenchmarkJobsResponse{MaxPage: maxPage}
	for i, j := range jobs {
		team, err := model.FindTeam(ctx, h.db, j.TeamID)
		if err != nil {
			return err
		}
		var teamPB *pbresources.Team
		if team != nil {
			teamPB = team.ToPB(false, nil, false)
		}
		options[i].Admin = true
		options[i].Team = teamPB
		res.Jobs = append(res.Jobs, j.ToPB(options[i]))
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminJobsShow: GET /api/admin/benchmark_jobs/:id（enqueued_by を含む）
func (h *Handler) AdminJobsShow(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	jobRow, err := model.FindJob(ctx, h.db, id)
	if err != nil {
		return err
	}
	if jobRow == nil {
		return &model.NotFoundError{}
	}
	pb, err := h.adminJobDetailPB(r, jobRow)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.GetBenchmarkJobResponse{Job: pb})
	return nil
}

func (h *Handler) adminJobDetailPB(r *http.Request, jobRow *model.BenchmarkJob) (*pbresources.BenchmarkJob, error) {
	ctx := r.Context()
	team, err := model.FindTeam(ctx, h.db, jobRow.TeamID)
	if err != nil {
		return nil, err
	}
	var teamPB *pbresources.Team
	if team != nil {
		teamPB = team.ToPB(false, nil, false)
	}
	return model.JobToPBFull(ctx, h.db, jobRow, true, teamPB)
}

// AdminJobsCreate: POST /api/admin/benchmark_jobs（FR-S-3: 代理 enqueue）
func (h *Handler) AdminJobsCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req := &pbadmin.EnqueueBenchmarkJobRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	team, err := model.FindTeam(ctx, h.db, req.TeamId)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}
	target, err := model.FindInstanceByTeamAndNumber(ctx, h.db, team.ID, req.TargetNumber)
	if err != nil {
		return err
	}
	targetID := sql.NullInt64{}
	if target != nil {
		targetID = sql.NullInt64{Int64: target.ID, Valid: true}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	jobRow, err := model.CreateJob(ctx, tx, team.ID, team.AvailabilityZone.String, targetID, sql.NullInt64{}, false)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	pb, err := h.adminJobDetailPB(r, jobRow)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.EnqueueBenchmarkJobResponse{Job: pb})
	return nil
}

// AdminJobsDestroy: DELETE /api/admin/benchmark_jobs/:id（キャンセル）
func (h *Handler) AdminJobsDestroy(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	jobRow, err := model.FindJob(ctx, h.db, id)
	if err != nil {
		return err
	}
	if jobRow == nil {
		return &model.NotFoundError{}
	}
	if err := model.CancelJob(ctx, h.db, jobRow, ri.now); err != nil {
		if err == model.ErrInvalidTransition {
			return errNotFound("cannot cancel a completed job")
		}
		return err
	}
	h.jobs.Enqueue(ctx, "benchmark_completion", job.BenchmarkCompletionArgs{JobID: jobRow.ID})

	pb, err := h.adminJobDetailPB(r, jobRow)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.CancelBenchmarkJobResponse{Job: pb})
	return nil
}

// AdminClarsIndex: GET /api/admin/clarifications
func (h *Handler) AdminClarsIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	query := `SELECT ` + clarColumnsSQL + ` FROM clarifications WHERE 1=1`
	args := []any{}
	if v := q.Get("team_id"); v != "" {
		teamID, err := parseInt64(v)
		if err != nil {
			return errBadRequest("invalid team_id")
		}
		query += ` AND team_id = $1`
		args = append(args, teamID)
	}
	if q.Get("unanswered_only") == "1" {
		query += ` AND answered_at IS NULL`
	}
	query += ` ORDER BY id DESC`

	clars, err := model.QueryClarifications(ctx, h.db, query, args...)
	if err != nil {
		return err
	}
	res := &pbadmin.ListClarificationsResponse{}
	for _, clar := range clars {
		pb, err := model.ClarificationToPBWithTeam(ctx, h.db, clar, true)
		if err != nil {
			return err
		}
		res.Clarifications = append(res.Clarifications, pb)
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminClarsShow: GET /api/admin/clarifications/:id
func (h *Handler) AdminClarsShow(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	clar, err := model.FindClarification(ctx, h.db, id)
	if err != nil {
		return err
	}
	if clar == nil {
		return &model.NotFoundError{}
	}
	pb, err := model.ClarificationToPBWithTeam(ctx, h.db, clar, true)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.GetClarificationResponse{Clarification: pb})
	return nil
}

// AdminClarsCreate: POST /api/admin/clarifications（FR-S-4: 運営発信。team_id 0 は全体公開）
func (h *Handler) AdminClarsCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req := &pbadmin.CreateClarificationRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	teamID := sql.NullInt64{}
	if req.TeamId != 0 {
		team, err := model.FindTeam(ctx, h.db, req.TeamId)
		if err != nil {
			return err
		}
		if team == nil {
			return &model.NotFoundError{}
		}
		teamID = sql.NullInt64{Int64: req.TeamId, Valid: true}
	}
	var id int64
	if err := h.db.QueryRowContext(ctx, `
		INSERT INTO clarifications (team_id, original_question, question, answer, answered_at, disclosed, admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), $5, true, now(), now()) RETURNING id`,
		teamID, req.Question, req.Question, req.Answer, req.TeamId == 0).Scan(&id); err != nil {
		return err
	}
	h.jobs.Enqueue(ctx, "clarification_response", job.ClarificationResponseArgs{ClarificationID: id, WasAnswered: false})

	clar, err := model.FindClarification(ctx, h.db, id)
	if err != nil {
		return err
	}
	pb, err := model.ClarificationToPBWithTeam(ctx, h.db, clar, true)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.CreateClarificationResponse{Clarification: pb})
	return nil
}

// AdminClarsUpdate: PUT /api/admin/clarifications/:id（FR-S-4: 回答・公開・質問文上書き）
func (h *Handler) AdminClarsUpdate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	req := &pbadmin.RespondClarificationRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if req.Id != id {
		return errBadRequest("id must match")
	}
	clar, err := model.FindClarification(ctx, h.db, id)
	if err != nil {
		return err
	}
	if clar == nil {
		return &model.NotFoundError{}
	}
	wasAnswered := clar.Answered()
	wasDisclosed := clar.IsDisclosed()

	question := req.Question
	if question == "" {
		question = clar.OriginalQuestion.String
	}
	answeredAt := clar.AnsweredAt
	if !answeredAt.Valid {
		answeredAt = sql.NullTime{Time: info(r).now, Valid: true}
	}
	if _, err := h.db.ExecContext(ctx, `
		UPDATE clarifications SET disclosed = $1, answer = $2, question = $3, answered_at = $4, updated_at = now() WHERE id = $5`,
		req.Disclose, req.Answer, question, answeredAt, id); err != nil {
		return err
	}
	h.jobs.Enqueue(ctx, "clarification_response", job.ClarificationResponseArgs{
		ClarificationID: id,
		WasAnswered:     wasAnswered && wasDisclosed == req.Disclose,
	})

	clar, err = model.FindClarification(ctx, h.db, id)
	if err != nil {
		return err
	}
	pb, err := model.ClarificationToPBWithTeam(ctx, h.db, clar, true)
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbadmin.RespondClarificationResponse{Clarification: pb})
	return nil
}

// AdminCommandRequestsIndex: GET /api/admin/instance_command_execute_requests（FR-S-6）
func (h *Handler) AdminCommandRequestsIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	requests, err := model.ListCommandRequests(ctx, h.db)
	if err != nil {
		return err
	}
	res := &pbadmin.ListInstanceCommandExecuteRequestsResponse{}
	for _, req := range requests {
		pb, err := req.ToPB(ctx, h.db)
		if err != nil {
			return err
		}
		res.Requests = append(res.Requests, pb)
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminCommandRequestsShow: GET /api/admin/instance_command_execute_requests/:id
func (h *Handler) AdminCommandRequestsShow(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	results, err := model.ListCommandRequestResults(ctx, h.db, id)
	if err != nil {
		return err
	}
	res := &pbadmin.GetInstanceCommandExecuteRequestResponse{}
	for _, result := range results {
		instance, err := model.FindInstance(ctx, h.db, result.ContestantInstanceID)
		if err != nil {
			return err
		}
		var instancePB *pbresources.ContestantInstance
		if instance != nil {
			instancePB = instance.ToPB(nil)
		}
		res.Results = append(res.Results, result.ToPB(instancePB))
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminCommandRequestOutput: GET /api/admin/instance_command_execute_requests/:request_id/:id/output
// パスに id が 2 つ並ぶ変則ルート（CT-9）。実体は結果 id のみで引く（Rails と同じ）。
func (h *Handler) AdminCommandRequestOutput(w http.ResponseWriter, r *http.Request) error {
	id, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	result, err := model.FindCommandRequestResult(r.Context(), h.db, id)
	if err != nil {
		return err
	}
	if result == nil {
		return &model.NotFoundError{}
	}
	renderPB(w, http.StatusOK, &pbadmin.GetInstanceCommandExecuteRequestOutputResponse{
		Output: result.Output.String,
	})
	return nil
}

// AdminTriggerEnvCheck: POST /api/admin/last_validations/env_check（FR-S-7）
func (h *Handler) AdminTriggerEnvCheck(w http.ResponseWriter, r *http.Request) error {
	teamIDs, err := h.triggerTargetTeams(r)
	if err != nil {
		return err
	}
	h.jobs.Enqueue(r.Context(), "execute_on_multiple_contestant_instances", job.ExecuteOnMultipleArgs{
		TeamIDs: teamIDs, Command: "sudo systemctl restart envcheck --no-block",
	})
	renderPB(w, http.StatusOK, &pbadmin.TriggerEnvCheckResponse{})
	return nil
}

// AdminTriggerInstanceRestart: POST /api/admin/last_validations/instance_restart
func (h *Handler) AdminTriggerInstanceRestart(w http.ResponseWriter, r *http.Request) error {
	teamIDs, err := h.triggerTargetTeams(r)
	if err != nil {
		return err
	}
	h.jobs.Enqueue(r.Context(), "execute_on_multiple_contestant_instances", job.ExecuteOnMultipleArgs{
		TeamIDs: teamIDs, Command: "sudo reboot",
	})
	renderPB(w, http.StatusOK, &pbadmin.TriggerInstanceRestartResponse{})
	return nil
}

func (h *Handler) triggerTargetTeams(r *http.Request) ([]int64, error) {
	req := &pbadmin.TriggerEnvCheckRequest{}
	if err := decodePB(r, req); err != nil {
		return nil, err
	}
	if len(req.TeamIds) > 0 {
		return req.TeamIds, nil
	}
	return h.activeTeamIDs(r)
}

func (h *Handler) activeTeamIDs(r *http.Request) ([]int64, error) {
	rows, err := h.db.QueryContext(r.Context(), `SELECT id FROM teams WHERE withdrawn = false AND disqualified = false`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AdminTriggerBenchmarks: POST /api/admin/last_validations/benchmarks
// 直近ジョブの target を引き継いで再 enqueue（post_validation 付き）。
func (h *Handler) AdminTriggerBenchmarks(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req := &pbadmin.TriggerBenchmarksRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	teamIDs := req.TeamIds
	if len(teamIDs) == 0 {
		var err error
		teamIDs, err = h.activeTeamIDs(r)
		if err != nil {
			return err
		}
	}

	for _, teamID := range teamIDs {
		jobs, err := model.QueryJobs(ctx, h.db, `SELECT `+jobColumns+` FROM benchmark_jobs WHERE team_id = $1 ORDER BY id DESC LIMIT 1`, teamID)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			continue
		}
		last := jobs[0]
		tx, err := h.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		_, err = model.CreateJob(ctx, tx, teamID, last.InstanceName.String, last.TargetID, sql.NullInt64{}, req.PostValidation)
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	renderPB(w, http.StatusOK, &pbadmin.TriggerBenchmarksResponse{})
	return nil
}

// AdminInstancesIndex: GET /api/admin/contestant_instances
func (h *Handler) AdminInstancesIndex(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var instances []*model.ContestantInstance
	var err error
	if v := r.URL.Query().Get("team_id"); v != "" {
		teamID, perr := parseInt64(v)
		if perr != nil {
			return errBadRequest("invalid team_id")
		}
		instances, err = model.TeamInstances(ctx, h.db, teamID)
	} else {
		instances, err = model.AllInstancesOrderByCloudID(ctx, h.db)
	}
	if err != nil {
		return err
	}
	res := &pbadmin.ListContestantInstancesResponse{}
	for _, ci := range instances {
		team, err := model.FindTeam(ctx, h.db, ci.TeamID)
		if err != nil {
			return err
		}
		var teamPB *pbresources.Team
		if team != nil {
			teamPB = team.ToPB(false, nil, false)
		}
		res.ContestantInstances = append(res.ContestantInstances, ci.ToPB(teamPB))
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminDumpLeaderboard: GET /api/admin/dump_leaderboard（FR-S-9: 任意時点の確定順位）
// when = ISO8601 または contest-end（単一開催のため qualify-end は廃止、計画書 §11.4）。
func (h *Handler) AdminDumpLeaderboard(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	whenParam := r.URL.Query().Get("when")
	if whenParam == "" {
		return errBadRequest("")
	}
	var when time.Time
	if whenParam == "contest-end" {
		if ri.timing.ContestEnd == nil {
			return errBadRequest("")
		}
		when = *ri.timing.ContestEnd
	} else {
		var err error
		when, err = time.Parse(time.RFC3339Nano, whenParam)
		if err != nil {
			return errBadRequest("")
		}
	}

	lb, _, err := contest.BuildLeaderboard(ctx, h.db, ri.timing, ri.now, contest.LeaderboardParams{
		Admin: true, Now: &when,
	})
	if err != nil {
		return err
	}

	res := &pbadmin.GetLeaderboardDumpResponse{}
	for idx, item := range lb.Teams {
		dumpItem := &pbadmin.GetLeaderboardDumpResponse_LeaderboardDumpItem{
			Position:    int64(idx + 1),
			Team:        item.Team,
			BestScore:   item.BestScore,
			LatestScore: item.LatestScore,
		}
		jobs, err := model.QueryJobs(ctx, h.db, `SELECT `+jobColumns+` FROM benchmark_jobs WHERE team_id = $1 AND post_validation = false ORDER BY id DESC LIMIT 1`, item.Team.Id)
		if err != nil {
			return err
		}
		if len(jobs) > 0 && jobs[0].TargetID.Valid {
			target, err := model.FindInstance(ctx, h.db, jobs[0].TargetID.Int64)
			if err != nil {
				return err
			}
			if target != nil {
				dumpItem.Target = target.ToPB(nil)
			}
		}
		res.Items = append(res.Items, dumpItem)
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminSSHKeyStats: GET /api/admin/unprepared_stats/ssh_key_stats（FR-S-8）
func (h *Handler) AdminSSHKeyStats(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	teams, err := h.visibleActiveTeams(r)
	if err != nil {
		return err
	}
	res := &pbadmin.GetSSHKeyStatsResponse{}
	for _, team := range teams {
		members, err := model.TeamMembers(ctx, h.db, team.ID)
		if err != nil {
			return err
		}
		pb, err := model.TeamToPBWithMembers(ctx, h.db, team, false, true)
		if err != nil {
			return err
		}
		var missing []int64
		for i, m := range pb.Members {
			if m.Detail != nil && !m.Detail.IsSshKeyRegistered {
				missing = append(missing, members[i].ID)
			}
		}
		if len(missing) > 0 {
			res.Items = append(res.Items, &pbadmin.GetSSHKeyStatsResponse_SSHKeyUnregisteredTeam{
				Team:                  pb,
				UnregisteredMemberIds: missing,
			})
		}
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminDiscordStats: GET /api/admin/unprepared_stats/discord_stats
func (h *Handler) AdminDiscordStats(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	teams, err := h.visibleActiveTeams(r)
	if err != nil {
		return err
	}
	res := &pbadmin.GetDiscordStatsResponse{}
	for _, team := range teams {
		members, err := model.TeamMembers(ctx, h.db, team.ID)
		if err != nil {
			return err
		}
		var notJoined []int64
		for _, m := range members {
			if !m.IsDiscordGuildMember {
				notJoined = append(notJoined, m.ID)
			}
		}
		if len(notJoined) > 0 {
			pb, err := model.TeamToPBWithMembers(ctx, h.db, team, false, true)
			if err != nil {
				return err
			}
			res.Items = append(res.Items, &pbadmin.GetDiscordStatsResponse_DiscordNotJoinedTeam{
				Team:               pb,
				NotJoinedMemberIds: notJoined,
			})
		}
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// AdminEnvCheckStats: GET /api/admin/unprepared_stats/env_check_stats
func (h *Handler) AdminEnvCheckStats(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	teams, err := h.visibleActiveTeams(r)
	if err != nil {
		return err
	}
	res := &pbadmin.GetEnvCheckStatsResponse{}
	for _, team := range teams {
		var n int
		if err := h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM env_checks WHERE team_id = $1 AND name = 'test-ssh' AND passed = true`, team.ID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			res.Items = append(res.Items, team.ToPB(false, nil, false))
		}
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

func (h *Handler) visibleActiveTeams(r *http.Request) ([]*model.Team, error) {
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT teams.id, teams.name, teams.leader_id, teams.is_hidden, teams.final_participation, teams.email_address, teams.invite_token, teams.withdrawn, teams.disqualified, teams.onsite_support_excluded, teams.student, teams.availability_zone
		FROM teams WHERE withdrawn = false AND disqualified = false AND is_hidden = false ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Team
	for rows.Next() {
		t := &model.Team{}
		if err := rows.Scan(&t.ID, &t.Name, &t.LeaderID, &t.IsHidden, &t.FinalParticipation, &t.EmailAddress, &t.InviteToken, &t.Withdrawn, &t.Disqualified, &t.OnsiteSupportExcluded, &t.Student, &t.AvailabilityZone); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
