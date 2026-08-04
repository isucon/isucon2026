package benchmark

import (
	"reflect"
	"testing"

	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
)

func TestDeterministicSequenceContainsPassAndFail(t *testing.T) {
	generator := Generator{}
	first := []Outcome{generator.Generate(1), generator.Generate(2), generator.Generate(3)}
	second := []Outcome{generator.Generate(1), generator.Generate(2), generator.Generate(3)}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("sequence was not deterministic")
	}
	var pass, fail bool
	for _, outcome := range first {
		pass = pass || outcome.Result == domain.BenchmarkPass
		fail = fail || outcome.Result == domain.BenchmarkFail
		if len(outcome.Logs) == 0 {
			t.Fatal("logs must be populated")
		}
	}
	if !pass || !fail {
		t.Fatal("sequence must contain PASS and FAIL")
	}
}
