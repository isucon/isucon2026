package handler

import (
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

		query := `SELECT ` + jobColumns + ` FROM benchmark_jobs WHERE status = ?`
		args := []any{model.JobPending}
		if req.TeamId > 0 {
			query += ` AND team_id = ?`
			args = append(args, req.TeamId)
		}
		if req.InstanceName != "" {
			query += ` AND instance_name = ?`
			args = append(args, req.InstanceName)
		}
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

		// 全 3 台の public IP と target の IP を返す
		instances, err := model.TeamInstances(ctx, h.db, jobRow.TeamID)
		if err != nil {
			return err
		}
		byNumber := map[int64]string{}
		for _, ci := range instances {
			byNumber[ci.Number] = ci.PublicIPv4Address.String
		}
		allAddresses := []string{byNumber[1], byNumber[2], byNumber[3]}

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
		`SELECT `+jobColumns+` FROM benchmark_jobs WHERE status = ? AND instance_name = ?`,
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
