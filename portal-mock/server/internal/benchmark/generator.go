package benchmark

import (
	"fmt"

	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
)

// Outcome is deterministic for a run sequence and intentionally includes both
// successful and failed benchmark simulations.
type Outcome struct {
	Result domain.BenchmarkResult
	Score  uint64
	Logs   []string
}

type Generator struct{}

var outcomes = []Outcome{
	{
		Result: domain.BenchmarkPass,
		Score:  187642,
		Logs: []string{
			"[setup] benchmark environment initialized",
			"[load] simulated requests completed",
			"[verify] all consistency checks passed",
		},
	},
	{
		Result: domain.BenchmarkFail,
		Score:  0,
		Logs: []string{
			"[setup] benchmark environment initialized",
			"[load] simulated requests completed",
			"[verify] simulated consistency check failed",
		},
	},
	{
		Result: domain.BenchmarkPass,
		Score:  231905,
		Logs: []string{
			"[setup] benchmark environment initialized",
			"[load] simulated requests completed",
			"[verify] all consistency checks passed",
		},
	},
	{
		Result: domain.BenchmarkPass,
		Score:  204311,
		Logs: []string{
			"[setup] benchmark environment initialized",
			"[load] simulated requests completed",
			"[verify] all consistency checks passed",
		},
	},
}

func (Generator) Generate(sequence uint64) Outcome {
	if sequence == 0 {
		panic("benchmark sequence must be positive")
	}
	template := outcomes[(sequence-1)%uint64(len(outcomes))]
	result := template
	result.Logs = append([]string{fmt.Sprintf("[run] deterministic sequence %d", sequence)}, template.Logs...)
	return result
}
