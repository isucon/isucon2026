package handler

import (
	"fmt"
	"net/http"

	pbbench "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/bench"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// BenchReceive: POST /api/bench/receive（FR-B-1）
// 共有トークンを定数時間比較し、pending ジョブを行ロックで排他取得して running へ。
func (h *Handler) BenchReceive(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	req := &pbbench.ReceiveBenchmarkJobRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if !secureCompare(req.Token, h.cfg.BenchToken) {
		return &apiError{status: http.StatusBadRequest, name: "Error::Unauthorized", message: "Unauthorized"}
	}

	for {
		tx, err := h.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}

		query := `SELECT ` + jobColumns + ` FROM benchmark_jobs WHERE status = $1`
		args := []any{model.JobPending}
		if req.TeamId > 0 {
			args = append(args, req.TeamId)
			query += fmt.Sprintf(` AND team_id = $%d`, len(args))
		}
		if req.InstanceName != "" {
			args = append(args, req.InstanceName)
			query += fmt.Sprintf(` AND instance_name = $%d`, len(args))
		}
		// Newly registered contest environments must wait for the Router.
		query += ` AND NOT EXISTS (SELECT 1 FROM team_routers r WHERE r.team_id=benchmark_jobs.team_id AND r.public_ipv4_address IS NULL)`
		query += ` ORDER BY id ASC LIMIT 1 FOR UPDATE`

		jobs, err := model.QueryJobs(ctx, tx, query, args...)
		if err != nil {
			tx.Rollback()
			return err
		}
		if len(jobs) == 0 {
			tx.Rollback()
			renderPB(w, http.StatusOK, &pbbench.ReceiveBenchmarkJobResponse{})
			return nil
		}
		jobRow := jobs[0]
		if jobRow.Status != model.JobPending {
			tx.Rollback()
			continue
		}
		router, err := model.FindTeamRouter(ctx, tx, jobRow.TeamID)
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := model.StartJob(ctx, tx, jobRow, req.InstanceName); err != nil {
			tx.Rollback()
			if err == model.ErrInvalidTransition {
				continue
			}
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}

		// 登録済みの全サーバの public IP と target の IP を返す
		instances, err := model.TeamInstances(ctx, h.db, jobRow.TeamID)
		if err != nil {
			return err
		}
		allAddresses := instancePublicAddresses(instances)

		targetIP := ""
		if jobRow.TargetID.Valid {
			target, err := model.FindInstance(ctx, h.db, jobRow.TargetID.Int64)
			if err != nil {
				return err
			}
			if target != nil {
				targetIP = target.PublicIPv4Address.String
			}
		}

		extraArgs := []string{}
		if router != nil && router.PublicIP.Valid {
			targetIP = router.PublicIP.String
			host := fmt.Sprintf("app.team%d-aws.127.0.0.1.nip.io", jobRow.TeamID)
			if router.BenchmarkHost.Valid {
				host = router.BenchmarkHost.String
			}
			extraArgs = append(extraArgs, "--host", host)
		}
		if jobRow.PostValidation {
			extraArgs = append(extraArgs, "--only-post-validation")
		}
		renderPB(w, http.StatusOK, &pbbench.ReceiveBenchmarkJobResponse{
			JobHandle: &pbbench.ReceiveBenchmarkJobResponse_JobHandle{
				JobId:             jobRow.ID,
				Handle:            jobRow.Handle.String,
				TargetIpv4Address: targetIP,
				DescriptionHuman:  "",
				AllIpv4Addresses:  allAddresses,
				ExtraCommandArgs:  extraArgs,
			},
		})
		return nil
	}
}

// BenchCancelOwned: POST /api/bench/cancel_owned（FR-B-2: 孤児ジョブの回収）
func (h *Handler) BenchCancelOwned(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	req := &pbbench.CancelOwnedBenchmarkJobRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if !secureCompare(req.Token, h.cfg.BenchToken) {
		return &apiError{status: http.StatusBadRequest, name: "Error::Unauthorized", message: "Unauthorized"}
	}

	jobs, err := model.QueryJobs(ctx, h.db,
		`SELECT `+jobColumns+` FROM benchmark_jobs WHERE status = $1 AND instance_name = $2`,
		model.JobRunning, req.InstanceName)
	if err != nil {
		return err
	}
	for _, jobRow := range jobs {
		if err := model.ErrorJob(ctx, h.db, jobRow, ri.now); err != nil {
			return err
		}
		// error! は自分で BenchmarkCompletionJob を enqueue する（計画書 §11.8）
		h.jobs.Enqueue(ctx, "benchmark_completion", job.BenchmarkCompletionArgs{JobID: jobRow.ID})
	}
	renderPB(w, http.StatusOK, &pbbench.CancelOwnedBenchmarkJobResponse{})
	return nil
}

// BenchReportResult: POST /api/bench/benchmark_results（FR-B-3: 途中報告）
// job_id + handle で所有検証。既存より新しい marked_at のみ採用。execution を含むと 400。
func (h *Handler) BenchReportResult(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	req := &pbbench.ReportBenchmarkResultRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if req.Result == nil {
		return errBadRequest("result must contain result")
	}
	if req.Result.Execution != nil {
		return errBadRequest("result must not contain execution")
	}
	if req.Result.MarkedAt == nil {
		return errBadRequest("result.marked_at is mandatory")
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	jobRow, err := model.FindJobByHandle(ctx, tx, req.JobId, req.Handle)
	if err != nil {
		return err
	}
	if jobRow == nil {
		return &model.NotFoundError{}
	}
	existing, err := model.FindResultByJob(ctx, tx, jobRow.ID)
	if err != nil {
		return err
	}
	markedAt := req.Result.MarkedAt.AsTime()
	if existing == nil || markedAt.After(existing.MarkedAt) {
		if err := model.SubmitResultFromPB(ctx, tx, jobRow, req.Result, ri.now); err != nil {
			if err == model.ErrInvalidTransition {
				return errBadRequest("InvalidTransition: invalid transition")
			}
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if jobRow.Closed() {
		h.jobs.Enqueue(ctx, "benchmark_completion", job.BenchmarkCompletionArgs{JobID: jobRow.ID})
	}
	renderPB(w, http.StatusOK, &pbbench.ReportBenchmarkResultResponse{})
	return nil
}

// BenchCompleteJob: POST /api/bench/benchmark_results/completion（FR-B-4: 確定報告）
func (h *Handler) BenchCompleteJob(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	req := &pbbench.CompleteBenchmarkJobRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if req.Result == nil || !req.Result.Finished || req.Result.Execution == nil {
		return errBadRequest("result must be complete")
	}
	if req.Result.MarkedAt == nil {
		return errBadRequest("result.marked_at is mandatory")
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	jobRow, err := model.FindJobByHandle(ctx, tx, req.JobId, req.Handle)
	if err != nil {
		return err
	}
	if jobRow == nil {
		return &model.NotFoundError{}
	}
	if err := model.SubmitResultFromPB(ctx, tx, jobRow, req.Result, ri.now); err != nil {
		if err == model.ErrInvalidTransition {
			return errBadRequest("result must be complete")
		}
		return err
	}
	if !jobRow.Closed() {
		return errBadRequest("result must be complete")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	h.jobs.Enqueue(ctx, "benchmark_completion", job.BenchmarkCompletionArgs{JobID: jobRow.ID})
	renderPB(w, http.StatusOK, &pbbench.CompleteBenchmarkJobResponse{})
	return nil
}

// instancePublicAddresses receives instances ordered by number from TeamInstances.
func instancePublicAddresses(instances []*model.ContestantInstance) []string {
	addresses := make([]string, 0, len(instances))
	for _, ci := range instances {
		addresses = append(addresses, ci.PublicIPv4Address.String)
	}
	return addresses
}
