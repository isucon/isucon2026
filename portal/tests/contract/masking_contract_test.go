package contract_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/model"
)

// CT-6: 非 admin へのマスク値。
// stderr="" / exit_status=-2 / exit_signal=-2 / signaled=false / instance_name="" / post_validation 省略。
func TestBenchmarkResultMaskingContract(t *testing.T) {
	result := &model.BenchmarkResult{
		Score:          100,
		ScoreRaw:       150,
		ScoreDeduction: 50,
		Finished:       true,
		Passed:         sql.NullBool{Bool: true, Valid: true},
		MarkedAt:       time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
		Reason:         sql.NullString{String: "reason", Valid: true},
		Stdout:         sql.NullString{String: "stdout", Valid: true},
		Stderr:         sql.NullString{String: "stderr", Valid: true},
		ExitStatus:     sql.NullInt64{Int64: 0, Valid: true},
	}

	contestant := result.ToPB(false)
	if contestant.Execution.Stderr != "" {
		t.Errorf("non-admin stderr = %q, want empty", contestant.Execution.Stderr)
	}
	if contestant.Execution.ExitStatus != -2 || contestant.Execution.ExitSignal != -2 {
		t.Errorf("non-admin exit_status/signal = %d/%d, want -2/-2",
			contestant.Execution.ExitStatus, contestant.Execution.ExitSignal)
	}
	if contestant.Execution.Signaled {
		t.Error("non-admin signaled must be false")
	}
	if contestant.Execution.Stdout != "stdout" || contestant.Execution.Reason != "reason" {
		t.Error("stdout / reason must not be masked")
	}

	admin := result.ToPB(true)
	if admin.Execution.Stderr != "stderr" {
		t.Errorf("admin stderr = %q, want stderr", admin.Execution.Stderr)
	}
	if admin.Execution.ExitStatus != 0 {
		t.Errorf("admin exit_status = %d, want 0", admin.Execution.ExitStatus)
	}
	// exit_signal が nil のとき admin には -1
	if admin.Execution.ExitSignal != -1 {
		t.Errorf("admin exit_signal = %d, want -1 for NULL", admin.Execution.ExitSignal)
	}
}

// 未確定（finished=false）の結果は execution を含まない。
func TestBenchmarkResultInProgressHasNoExecution(t *testing.T) {
	result := &model.BenchmarkResult{
		Score:    10,
		MarkedAt: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
	}
	if result.ToPB(true).Execution != nil {
		t.Error("in-progress result must not include execution")
	}
}

// instance_name と post_validation は admin のみに出す（CT-6）。
func TestBenchmarkJobMaskingContract(t *testing.T) {
	job := &model.BenchmarkJob{
		ID:             1,
		TeamID:         2,
		Status:         model.JobFinished,
		InstanceName:   sql.NullString{String: "apne1-az1", Valid: true},
		PostValidation: true,
		CreatedAt:      time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
	}

	contestant := job.ToPB(model.JobToPBOptions{Admin: false})
	if contestant.InstanceName != "" {
		t.Errorf("non-admin instance_name = %q, want empty", contestant.InstanceName)
	}
	if contestant.PostValidation {
		t.Error("non-admin post_validation must be false")
	}

	admin := job.ToPB(model.JobToPBOptions{Admin: true})
	if admin.InstanceName != "apne1-az1" {
		t.Errorf("admin instance_name = %q, want apne1-az1", admin.InstanceName)
	}
	if !admin.PostValidation {
		t.Error("admin post_validation must be true")
	}
}
