package loadfixture

import "math/rand"

// Each team holds a score level until a change is deployed. Change times and
// magnitudes are independent between teams; noise represents run-to-run variance.
type scoreChange struct{ at, multiplier float64 }
type scoreTrajectory struct {
	profile                               string
	base                                  float64
	changes                               []scoreChange
	failureStart, failureEnd, failureRate float64
	rng                                   *rand.Rand
}

func newScoreTrajectory(seed int64, teamID int) *scoreTrajectory {
	r := rand.New(rand.NewSource(seed ^ int64(teamID)*0x5142026))
	s := &scoreTrajectory{rng: r, base: float64(1500 + r.Intn(15000)), failureRate: 0.01 + r.Float64()*0.10}
	change := func(at, multiplier float64) { s.changes = append(s.changes, scoreChange{at, multiplier}) }
	switch r.Intn(6) {
	case 0:
		s.profile = "stagnant"
		change(0.2+r.Float64()*0.5, 0.95+r.Float64()*0.1)
	case 1:
		s.profile = "stepwise"
		at := 0.05 + r.Float64()*0.10
		for at < 0.95 {
			change(at, 1.25+r.Float64()*0.65)
			at += 0.10 + r.Float64()*0.20
		}
	case 2:
		s.profile = "early_plateau"
		change(0.05+r.Float64()*0.10, 1.8+r.Float64()*1.8)
		change(0.2+r.Float64()*0.15, 1.2+r.Float64()*0.8)
	case 3:
		s.profile = "late_breakthrough"
		change(0.68+r.Float64()*0.16, 3+r.Float64()*4)
		change(0.88+r.Float64()*0.07, 1.1+r.Float64()*0.5)
	case 4:
		s.profile = "regression"
		change(0.15+r.Float64()*0.15, 1.2+r.Float64()*0.4)
		change(0.40+r.Float64()*0.15, 0.20+r.Float64()*0.15)
		if r.Float64() < 0.5 {
			change(0.72+r.Float64()*0.15, 3+r.Float64()*1.5)
		}
	case 5:
		s.profile = "unstable"
		at := 0.05
		for at < 0.95 {
			change(at, 0.55+r.Float64()*1.1)
			at += 0.06 + r.Float64()*0.12
		}
		s.failureRate = 0.15 + r.Float64()*0.20
	}
	// Some teams spend a continuous period debugging failed runs.
	if r.Float64() < 0.35 {
		s.failureStart = 0.1 + r.Float64()*0.65
		s.failureEnd = s.failureStart + 0.06 + r.Float64()*0.10
	}
	return s
}

func (s *scoreTrajectory) sample(progress float64) (raw, deduction int, passed bool) {
	level := s.base
	for _, change := range s.changes {
		if progress >= change.at {
			level *= change.multiplier
		}
	}
	level = max(300, min(level, 1000000))
	raw = int(level * (0.98 + s.rng.Float64()*0.04))
	deduction = int(float64(raw) * s.rng.Float64() * 0.01)
	failureRate := s.failureRate
	if progress >= s.failureStart && progress < s.failureEnd {
		failureRate = 0.9
	}
	return raw, deduction, s.rng.Float64() >= failureRate
}
