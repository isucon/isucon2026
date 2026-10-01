// Package loadfixture generates reproducible, offline portal load-test history.
package loadfixture

import (
	"fmt"
	"math/rand"
	"sort"
	"time"
)

type Config struct {
	IDOffset int
	Teams    int
	Duration time.Duration
	Start    time.Time
	Seed     int64
	LogBytes int
}

func DefaultConfig() Config {
	return Config{Teams: 1000, Duration: 8 * time.Hour, Start: time.Date(2026, 10, 31, 1, 0, 0, 0, time.UTC), Seed: 20261031, LogBytes: 2048}
}

type Team struct {
	ID           int
	Members      int
	ScoreProfile string
}
type Job struct {
	ID, TeamID, TargetID, ContestantID int
	Created, Started, Finished         time.Time
	Passed                             bool
	Score, Raw, Deduction              int
	LogSeed                            int64
}
type Dataset struct {
	Config Config
	Teams  []Team
	Jobs   []Job
}

// Generate uses independent timing and result RNGs so payload changes do not
// change the request schedule. Intervals are measured enqueue-to-enqueue.
func Generate(c Config) (*Dataset, error) {
	if c.IDOffset < 0 || c.IDOffset > 1000000000 || c.Teams < 1 || c.Teams > 100000 || c.Duration < 10*time.Minute || c.Duration > 7*24*time.Hour || c.Start.IsZero() || c.LogBytes < 0 || c.LogBytes > 1024*1024 {
		return nil, fmt.Errorf("id-offset must be 0..1000000000, teams must be 1..100000, duration 10m..168h, start nonzero, log-bytes 0..1048576")
	}
	d := &Dataset{Config: c}
	timing := rand.New(rand.NewSource(c.Seed))
	results := rand.New(rand.NewSource(c.Seed ^ 0x5142026))
	end := c.Start.Add(c.Duration)
	for id := 1; id <= c.Teams; id++ {
		members := 1 + results.Intn(3)
		trajectory := newScoreTrajectory(c.Seed, id)
		d.Teams = append(d.Teams, Team{ID: id, Members: members, ScoreProfile: trajectory.profile})
		// Keep legacy metadata RNG draws stable so existing fixtures can update scores in place.
		_ = results.Intn(9000)
		at := c.Start.Add(time.Duration(timing.Int63n(int64(5 * time.Minute))))
		for {
			started := at.Add(time.Duration(1+results.Intn(10)) * time.Second)
			finished := started.Add(time.Duration(60+results.Intn(61)) * time.Second)
			if !finished.Before(end) {
				break
			}
			_ = results.Intn(3000)
			_ = results.Intn(300)
			_ = results.Float64()
			raw, deduction, passed := trajectory.sample(float64(at.Sub(c.Start)) / float64(c.Duration))
			score := raw - deduction
			if !passed {
				score = 0
			}
			d.Jobs = append(d.Jobs, Job{TeamID: id, TargetID: (id-1)*3 + 1 + results.Intn(3), ContestantID: (id-1)*3 + 1 + results.Intn(members), Created: at, Started: started, Finished: finished, Passed: passed, Score: score, Raw: raw, Deduction: deduction, LogSeed: results.Int63()})
			at = at.Add(3*time.Minute + time.Duration(timing.Int63n(int64(2*time.Minute)+1)))
		}
	}
	sort.SliceStable(d.Jobs, func(i, j int) bool { return d.Jobs[i].Created.Before(d.Jobs[j].Created) })
	for i := range d.Jobs {
		d.Jobs[i].ID = i + 1
	}
	return d, nil
}

type Summary struct {
	Profiles      map[string]int `json:"score_profiles"`
	Teams         int            `json:"teams"`
	Contestants   int            `json:"contestants"`
	Jobs          int            `json:"jobs"`
	Notifications int            `json:"notifications"`
	Failed        int            `json:"failed"`
	MinPerTeam    int            `json:"min_per_team"`
	MaxPerTeam    int            `json:"max_per_team"`
	Seed          int64          `json:"seed"`
	Start         time.Time      `json:"start"`
	End           time.Time      `json:"end"`
	Freeze        time.Time      `json:"freeze"`
	LogBytes      int            `json:"log_bytes_per_result"`
}

func (d *Dataset) Summary() Summary {
	s := Summary{Teams: len(d.Teams), Jobs: len(d.Jobs), MinPerTeam: len(d.Jobs), Seed: d.Config.Seed, Start: d.Config.Start, End: d.Config.Start.Add(d.Config.Duration), Freeze: d.freeze(), LogBytes: d.Config.LogBytes}
	s.Profiles = make(map[string]int)
	counts := make([]int, len(d.Teams))
	for _, t := range d.Teams {
		s.Contestants += t.Members
		s.Profiles[t.ScoreProfile]++
	}
	for _, j := range d.Jobs {
		counts[j.TeamID-1]++
		s.Notifications += d.Teams[j.TeamID-1].Members
		if !j.Passed {
			s.Failed++
		}
	}
	for _, n := range counts {
		s.MinPerTeam = min(s.MinPerTeam, n)
		s.MaxPerTeam = max(s.MaxPerTeam, n)
	}
	return s
}
func (d *Dataset) freeze() time.Time { return d.Config.Start.Add(d.Config.Duration * 7 / 8) }
