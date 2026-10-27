package model

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

// BenchmarkJob.status（DM-2: proto と DB で共通の整数値）
const (
	JobPending   = int32(pbresources.BenchmarkJob_PENDING)
	JobRunning   = int32(pbresources.BenchmarkJob_RUNNING)
	JobErrored   = int32(pbresources.BenchmarkJob_ERRORED)
	JobCancelled = int32(pbresources.BenchmarkJob_CANCELLED)
	JobFinished  = int32(pbresources.BenchmarkJob_FINISHED)
)

// ErrInvalidTransition は BenchmarkJob::InvalidTransition 相当（400）。
var ErrInvalidTransition = errors.New("invalid transition")

type BenchmarkJob struct {
	ID             int64
	TeamID         int64
	Status         int32
	InstanceName   sql.NullString
	Handle         sql.NullString
	StartedAt      sql.NullTime
	FinishedAt     sql.NullTime
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TargetID       sql.NullInt64
	EnqueuedByID   sql.NullInt64
	PostValidation bool
}

const jobColumns = `benchmark_jobs.id, benchmark_jobs.team_id, benchmark_jobs.status, benchmark_jobs.instance_name, benchmark_jobs.handle, benchmark_jobs.started_at, benchmark_jobs.finished_at, benchmark_jobs.created_at, benchmark_jobs.updated_at, benchmark_jobs.target_id, benchmark_jobs.enqueued_by_id, benchmark_jobs.post_validation`

func scanJob(row interface{ Scan(...any) error }) (*BenchmarkJob, error) {
	j := &BenchmarkJob{}
	err := row.Scan(&j.ID, &j.TeamID, &j.Status, &j.InstanceName, &j.Handle, &j.StartedAt, &j.FinishedAt, &j.CreatedAt, &j.UpdatedAt, &j.TargetID, &j.EnqueuedByID, &j.PostValidation)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (j *BenchmarkJob) Closed() bool {
	return j.Status == JobErrored || j.Status == JobCancelled || j.Status == JobFinished
}

func FindJob(ctx context.Context, db DBTX, id int64) (*BenchmarkJob, error) {
	return scanJob(db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM benchmark_jobs WHERE id = $1`, id))
}

func FindJobByHandle(ctx context.Context, db DBTX, id int64, handle string) (*BenchmarkJob, error) {
	return scanJob(db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM benchmark_jobs WHERE id = $1 AND handle = $2`, id, handle))
}

func QueryJobs(ctx context.Context, db DBTX, query string, args ...any) ([]*BenchmarkJob, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BenchmarkJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CreateJob は BenchmarkJob.create! 相当。重複 enqueue は DM-4 のとおり DB ロックで塞ぐ
// （Rails のアプリ層 COUNT より強い保証）。tx 内で呼ぶこと。
// benchmark_jobs のギャップロックはデッドロックを招くため、チーム行のロックで直列化する。
func CreateJob(ctx context.Context, tx DBTX, teamID int64, instanceName string, targetID, enqueuedByID sql.NullInt64, postValidation bool) (*BenchmarkJob, error) {
	var lockedTeamID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM teams WHERE id = $1 FOR UPDATE`, teamID).Scan(&lockedTeamID); err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM benchmark_jobs WHERE team_id = $1 AND status IN ($2, $3)`,
		teamID, JobPending, JobRunning).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, &InvalidError{Messages: []string{"Team must have no other concurrent job"}}
	}

	handle := sql.NullString{}
	if instanceName != "" {
		handle = sql.NullString{String: NewHandle(), Valid: true}
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO benchmark_jobs (team_id, status, instance_name, handle, target_id, enqueued_by_id, post_validation, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now()) RETURNING id`,
		teamID, JobPending, sql.NullString{String: instanceName, Valid: instanceName != ""}, handle, targetID, enqueuedByID, postValidation).Scan(&id); err != nil {
		return nil, err
	}
	return FindJob(ctx, tx, id)
}

// StartJob は BenchmarkJob#start! 相当。pending → running。tx 内・行ロック済みで呼ぶこと。
func StartJob(ctx context.Context, tx DBTX, job *BenchmarkJob, instanceName string) error {
	if job.Status != JobPending {
		return ErrInvalidTransition
	}
	if !job.Handle.Valid {
		job.Handle = sql.NullString{String: NewHandle(), Valid: true}
	}
	job.InstanceName = sql.NullString{String: instanceName, Valid: true}
	job.Status = JobRunning
	_, err := tx.ExecContext(ctx,
		`UPDATE benchmark_jobs SET status = $1, instance_name = $2, handle = $3, updated_at = now() WHERE id = $4`,
		job.Status, job.InstanceName, job.Handle, job.ID)
	return err
}

// CancelJob は BenchmarkJob#cancel! 相当。
func CancelJob(ctx context.Context, db DBTX, job *BenchmarkJob, now time.Time) error {
	if job.Closed() {
		return ErrInvalidTransition
	}
	job.Status = JobCancelled
	job.FinishedAt = sql.NullTime{Time: now.UTC(), Valid: true}
	_, err := db.ExecContext(ctx,
		`UPDATE benchmark_jobs SET status = $1, finished_at = $2, updated_at = now() WHERE id = $3`,
		job.Status, job.FinishedAt, job.ID)
	return err
}

// ErrorJob は BenchmarkJob#error! 相当。失敗結果を作って errored に落とす。
func ErrorJob(ctx context.Context, db DBTX, job *BenchmarkJob, now time.Time) error {
	if job.Closed() {
		return ErrInvalidTransition
	}
	job.Status = JobErrored
	job.FinishedAt = sql.NullTime{Time: now.UTC(), Valid: true}

	reason := "[BUG] Benchmarker didn't successfully finish"
	result, err := FindResultByJob(ctx, db, job.ID)
	if err != nil {
		return err
	}
	if result == nil {
		_, err = db.ExecContext(ctx, `
			INSERT INTO benchmark_results (team_id, benchmark_job_id, score, score_raw, score_deduction, finished, passed, marked_at, reason, created_at, updated_at)
			VALUES ($1, $2, 0, 0, 0, true, false, $3, $4, now(), now())`,
			job.TeamID, job.ID, job.FinishedAt.Time, reason)
	} else {
		// set_score_zero_when_failed: finished && !passed → score 0
		_, err = db.ExecContext(ctx, `
			UPDATE benchmark_results SET finished = true, passed = false, score = 0, marked_at = $1, reason = $2, updated_at = now() WHERE id = $3`,
			job.FinishedAt.Time, reason, result.ID)
	}
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE benchmark_jobs SET status = $1, finished_at = $2, updated_at = now() WHERE id = $3`,
		job.Status, job.FinishedAt, job.ID)
	return err
}

// SubmitResultFromPB は BenchmarkJob#submit_result_from_pb! 相当。tx 内で呼ぶこと。
// score センチネル（CT-7）と BenchmarkResult の不変条件（DM-3）を含む。
func SubmitResultFromPB(ctx context.Context, tx DBTX, job *BenchmarkJob, pb *pbresources.BenchmarkResult, now time.Time) error {
	if job.Status != JobRunning {
		return ErrInvalidTransition
	}

	existing, err := FindResultByJob(ctx, tx, job.ID)
	if err != nil {
		return err
	}

	// update_from_pb! 相当の値組み立て
	var score int64
	if pb.Score == -1 {
		// CT-7: score == -1 は「raw - deduction で計算せよ」のセンチネル
		if pb.Finished && !pb.Passed {
			score = 0
		} else {
			score = pb.GetScoreBreakdown().GetRaw() - pb.GetScoreBreakdown().GetDeduction()
		}
	} else {
		score = pb.Score
	}
	// set_score_zero_when_failed
	if pb.Finished && !pb.Passed {
		score = 0
	}

	var markedAt time.Time
	if pb.MarkedAt != nil {
		markedAt = pb.MarkedAt.AsTime()
	} else {
		return &InvalidError{Messages: []string{"Marked at can't be blank"}}
	}

	var reason, stdout, stderr sql.NullString
	var exitStatus, exitSignal sql.NullInt64
	if pb.Execution != nil {
		reason = sql.NullString{String: pb.Execution.Reason, Valid: true}
		stdout = sql.NullString{String: pb.Execution.Stdout, Valid: true}
		stderr = sql.NullString{String: pb.Execution.Stderr, Valid: true}
		if pb.Execution.ExitStatus >= 0 {
			exitStatus = sql.NullInt64{Int64: int64(pb.Execution.ExitStatus), Valid: true}
		}
		if pb.Execution.Signaled {
			exitSignal = sql.NullInt64{Int64: int64(pb.Execution.ExitSignal), Valid: true}
		}
	}

	// DM-3: finished 後の変更禁止 / marked_at 単調増加
	if existing != nil {
		if existing.Finished {
			return &InvalidError{Messages: []string{"Finished records must not be changed"}}
		}
		if markedAt.Before(existing.MarkedAt) {
			return &InvalidError{Messages: []string{"Marked at must be greater than the present value"}}
		}
	}

	var passed sql.NullBool
	if pb.Finished {
		passed = sql.NullBool{Bool: pb.Passed, Valid: true}
	} else {
		// proto3 の bool には nil が無い。未確定の passed は Rails の
		// update_from_pb! と同じく pb.passed の値をそのまま入れる。
		passed = sql.NullBool{Bool: pb.Passed, Valid: true}
	}

	if existing == nil {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO benchmark_results (team_id, benchmark_job_id, score, score_raw, score_deduction, finished, passed, marked_at, reason, stdout, stderr, exit_status, exit_signal, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now(), now())`,
			job.TeamID, job.ID, score, pb.GetScoreBreakdown().GetRaw(), pb.GetScoreBreakdown().GetDeduction(),
			pb.Finished, passed, markedAt.UTC(), reason, stdout, stderr, exitStatus, exitSignal)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE benchmark_results SET score = $1, score_raw = $2, score_deduction = $3, finished = $4, passed = $5, marked_at = $6, reason = $7, stdout = $8, stderr = $9, exit_status = $10, exit_signal = $11, updated_at = now()
			WHERE id = $12`,
			score, pb.GetScoreBreakdown().GetRaw(), pb.GetScoreBreakdown().GetDeduction(),
			pb.Finished, passed, markedAt.UTC(), reason, stdout, stderr, exitStatus, exitSignal, existing.ID)
	}
	if err != nil {
		return err
	}

	// ジョブ側の遷移
	// started_at は未設定のときだけ埋める（COALESCE で 1 文にまとめる）
	if !job.StartedAt.Valid {
		job.StartedAt = sql.NullTime{Time: now.UTC(), Valid: true}
	}
	sets := `updated_at = now(), started_at = COALESCE(started_at, $1)`
	args := []any{job.StartedAt}
	if pb.Finished {
		// errored? = finished && (signaled? || exit_status != 0)
		errored := pb.Execution != nil && (pb.Execution.Signaled || pb.Execution.ExitStatus != 0)
		if pb.Execution == nil {
			errored = true // exit_status が無い（nil != 0）
		}
		if errored {
			job.Status = JobErrored
		} else {
			job.Status = JobFinished
		}
		job.FinishedAt = sql.NullTime{Time: markedAt.UTC(), Valid: true}
		sets += `, status = $2, finished_at = $3`
		args = append(args, job.Status, job.FinishedAt)
	}
	args = append(args, job.ID)
	if _, err := tx.ExecContext(ctx, `UPDATE benchmark_jobs SET `+sets+` WHERE id = $`+strconv.Itoa(len(args)), args...); err != nil {
		return err
	}

	// survey_response はチームごとに 1 件で最新結果が上書きする（OQ-3 のとおり Rails の挙動を写す）
	if pb.SurveyResponse != nil && pb.SurveyResponse.Language != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO survey_responses (team_id, benchmark_job_id, language, agent, harness, model, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, now(), now())
			ON CONFLICT (team_id) DO UPDATE SET benchmark_job_id = EXCLUDED.benchmark_job_id, language = EXCLUDED.language, agent = EXCLUDED.agent, harness = EXCLUDED.harness, model = EXCLUDED.model, updated_at = now()`,
			job.TeamID, job.ID, pb.SurveyResponse.Language, pb.SurveyResponse.Agent, pb.SurveyResponse.Harness, pb.SurveyResponse.Model); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO survey_response_history (team_id, benchmark_job_id, language, agent, harness, model, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, now(), now())
			ON CONFLICT (benchmark_job_id) DO UPDATE SET language = EXCLUDED.language, agent = EXCLUDED.agent, harness = EXCLUDED.harness, model = EXCLUDED.model, updated_at = now()`,
			job.TeamID, job.ID, pb.SurveyResponse.Language, pb.SurveyResponse.Agent, pb.SurveyResponse.Harness, pb.SurveyResponse.Model); err != nil {
			return err
		}
	}
	return nil
}

type BenchmarkResult struct {
	ID             int64
	TeamID         int64
	BenchmarkJobID int64
	Score          int64
	ScoreRaw       int64
	ScoreDeduction int64
	Finished       bool
	Passed         sql.NullBool
	MarkedAt       time.Time
	Reason         sql.NullString
	Stdout         sql.NullString
	Stderr         sql.NullString
	ExitStatus     sql.NullInt64
	ExitSignal     sql.NullInt64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const resultColumns = `benchmark_results.id, benchmark_results.team_id, benchmark_results.benchmark_job_id, benchmark_results.score, benchmark_results.score_raw, benchmark_results.score_deduction, benchmark_results.finished, benchmark_results.passed, benchmark_results.marked_at, benchmark_results.reason, benchmark_results.stdout, benchmark_results.stderr, benchmark_results.exit_status, benchmark_results.exit_signal, benchmark_results.created_at, benchmark_results.updated_at`

func scanResult(row interface{ Scan(...any) error }) (*BenchmarkResult, error) {
	r := &BenchmarkResult{}
	err := row.Scan(&r.ID, &r.TeamID, &r.BenchmarkJobID, &r.Score, &r.ScoreRaw, &r.ScoreDeduction, &r.Finished, &r.Passed, &r.MarkedAt, &r.Reason, &r.Stdout, &r.Stderr, &r.ExitStatus, &r.ExitSignal, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func FindResultByJob(ctx context.Context, db DBTX, jobID int64) (*BenchmarkResult, error) {
	return scanResult(db.QueryRowContext(ctx, `SELECT `+resultColumns+` FROM benchmark_results WHERE benchmark_job_id = $1`, jobID))
}

func (r *BenchmarkResult) Signaled() bool {
	return r.Finished && r.ExitSignal.Valid
}

func (r *BenchmarkResult) SuccessfullyExited() bool {
	return r.Finished && r.ExitStatus.Valid && r.ExitStatus.Int64 == 0
}

func (r *BenchmarkResult) Errored() bool {
	return r.Finished && (r.Signaled() || !r.SuccessfullyExited())
}

// ToPB は BenchmarkResult#to_pb 相当。非 admin へのマスク値は CT-6 のとおり。
func (r *BenchmarkResult) ToPB(admin bool) *pbresources.BenchmarkResult {
	pb := &pbresources.BenchmarkResult{
		Finished: r.Finished,
		Passed:   r.Passed.Valid && r.Passed.Bool,
		Score:    r.Score,
		ScoreBreakdown: &pbresources.BenchmarkResult_ScoreBreakdown{
			Raw:       r.ScoreRaw,
			Deduction: r.ScoreDeduction,
		},
		MarkedAt: ts(r.MarkedAt),
	}
	if r.Finished {
		exec := &pbresources.BenchmarkResult_Execution{
			Reason: nullStr(r.Reason),
			Stdout: nullStr(r.Stdout),
		}
		if admin {
			exec.Stderr = nullStr(r.Stderr)
			exec.ExitStatus = int32(orInt64(r.ExitStatus, -1))
			exec.ExitSignal = int32(orInt64(r.ExitSignal, -1))
			exec.Signaled = r.Signaled()
		} else {
			exec.Stderr = ""
			exec.ExitStatus = -2
			exec.ExitSignal = -2
			exec.Signaled = false
		}
		pb.Execution = exec
	}
	return pb
}

func orInt64(v sql.NullInt64, fallback int64) int64 {
	if v.Valid {
		return v.Int64
	}
	return fallback
}

// JobToPBOptions は BenchmarkJob#to_pb のキーワード引数。
type JobToPBOptions struct {
	Admin  bool
	Team   *pbresources.Team
	Detail bool
	// Score は joins_score 済みの値。nil なら Detail 時に result から採る。
	Score      *int64
	Target     *pbresources.ContestantInstance
	Result     *pbresources.BenchmarkResult
	EnqueuedBy *pbresources.BenchmarkJob_EnqueuedBy
}

func (j *BenchmarkJob) ToPB(o JobToPBOptions) *pbresources.BenchmarkJob {
	pb := &pbresources.BenchmarkJob{
		Id:         j.ID,
		TeamId:     j.TeamID,
		TargetId:   j.TargetID.Int64,
		Status:     pbresources.BenchmarkJob_Status(j.Status),
		CreatedAt:  ts(j.CreatedAt),
		UpdatedAt:  ts(j.UpdatedAt),
		StartedAt:  tsn(j.StartedAt),
		FinishedAt: tsn(j.FinishedAt),
		Team:       o.Team,
		Target:     o.Target,
		Result:     o.Result,
		EnqueuedBy: o.EnqueuedBy,
	}
	if o.Score != nil {
		pb.Score = *o.Score
	}
	if o.Admin {
		pb.InstanceName = nullStr(j.InstanceName)
		pb.PostValidation = j.PostValidation
	}
	return pb
}

// JobToPBFull は target / result / enqueued_by を読み込んで to_pb(detail: true) 相当を返す。
func JobToPBFull(ctx context.Context, db DBTX, j *BenchmarkJob, admin bool, teamPB *pbresources.Team) (*pbresources.BenchmarkJob, error) {
	o := JobToPBOptions{Admin: admin, Team: teamPB, Detail: true}

	result, err := FindResultByJob(ctx, db, j.ID)
	if err != nil {
		return nil, err
	}
	if result != nil {
		o.Result = result.ToPB(admin)
		o.Score = &result.Score
	}
	if j.TargetID.Valid {
		target, err := FindInstance(ctx, db, j.TargetID.Int64)
		if err != nil {
			return nil, err
		}
		if target != nil {
			o.Target = target.ToPB(nil)
		}
	}
	if j.EnqueuedByID.Valid {
		by, err := FindContestant(ctx, db, j.EnqueuedByID.Int64)
		if err != nil {
			return nil, err
		}
		if by != nil {
			o.EnqueuedBy = &pbresources.BenchmarkJob_EnqueuedBy{Name: by.Name, AvatarUrl: by.AvatarURL}
		}
	}
	return j.ToPB(o), nil
}

// NewHandle は SecureRandom.urlsafe_base64(64) 相当（DM-6）。
func NewHandle() string {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
