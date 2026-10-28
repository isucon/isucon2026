package handler

import (
	"context"
	"database/sql"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// decodePB はリクエストボディを protobuf としてデコードする（ProtobufData#pb 相当）。
// GET のクエリパラメータは通常のパラメータで読む（proto の *Query は使われていない、計画書 §11.6）。
func decodePB(r *http.Request, m proto.Message) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if err := proto.Unmarshal(body, m); err != nil {
		return errBadRequest("invalid protobuf payload")
	}
	return nil
}

func chiParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

const jobColumns = `benchmark_jobs.id, benchmark_jobs.team_id, benchmark_jobs.status, benchmark_jobs.instance_name, benchmark_jobs.handle, benchmark_jobs.started_at, benchmark_jobs.finished_at, benchmark_jobs.created_at, benchmark_jobs.updated_at, benchmark_jobs.target_id, benchmark_jobs.enqueued_by_id, benchmark_jobs.post_validation`

// jobListColumns は一覧で表示するスコアと投入者情報を含む。
const jobListColumns = jobColumns + `, benchmark_results.score, contestants.name, contestants.avatar_url`

const jobEnqueuedByJoin = ` LEFT JOIN contestants ON contestants.id = benchmark_jobs.enqueued_by_id`

const clarColumnsSQL = `clarifications.id, clarifications.team_id, clarifications.disclosed, clarifications.question, clarifications.answer, clarifications.original_question, clarifications.answered_at, clarifications.admin, clarifications.created_at, clarifications.updated_at`

// queryJobList は jobListColumns の SELECT を実行し、一覧用の変換オプションとともに返す。
func queryJobList(ctx context.Context, db model.DBTX, query string, args ...any) ([]*model.BenchmarkJob, []model.JobToPBOptions, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var jobs []*model.BenchmarkJob
	var options []model.JobToPBOptions
	for rows.Next() {
		j := &model.BenchmarkJob{}
		var option model.JobToPBOptions
		var name, avatarURL sql.NullString
		if err := rows.Scan(&j.ID, &j.TeamID, &j.Status, &j.InstanceName, &j.Handle, &j.StartedAt, &j.FinishedAt,
			&j.CreatedAt, &j.UpdatedAt, &j.TargetID, &j.EnqueuedByID, &j.PostValidation, &option.Score, &name, &avatarURL); err != nil {
			return nil, nil, err
		}
		jobs = append(jobs, j)
		if name.Valid {
			option.EnqueuedBy = &pbresources.BenchmarkJob_EnqueuedBy{Name: name.String, AvatarUrl: avatarURL.String}
		}
		options = append(options, option)
	}
	return jobs, options, rows.Err()
}

// parseJobStatus はクエリパラメータの status（enum 整数値）を検証する。
func parseJobStatus(s string) (int32, error) {
	v, err := parseInt64(s)
	if err != nil {
		return 0, errBadRequest("Invalid status")
	}
	if _, ok := pbresources.BenchmarkJob_Status_name[int32(v)]; !ok {
		return 0, errBadRequest("Invalid status")
	}
	return int32(v), nil
}
