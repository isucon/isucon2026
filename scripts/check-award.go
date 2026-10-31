// check-award evaluates the corporate awards described in docs/manual.md.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Run struct {
	At     time.Time `json:"at"`
	Score  *int64    `json:"score"`
	Passed *bool     `json:"passed"`
}
type Team struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	MemberCount  int64  `json:"member_count"`
	FinalRank    int64  `json:"final_rank"`
	Disqualified *bool  `json:"disqualified"`
	Runs         []Run  `json:"runs"`
}
type Input struct {
	Start time.Time `json:"contest_started_at"`
	Close time.Time `json:"scoreboard_closed_at"`
	End   time.Time `json:"contest_ended_at"`
	Teams []Team    `json:"teams"`
}
type Candidate struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	FinalRank  int64    `json:"final_rank"`
	FinalScore int64    `json:"final_score"`
	Metric     []string `json:"metric"`
}
type Award struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Winners        []Candidate `json:"winners"`
	TiedCandidates []Candidate `json:"tied_candidates"`
	NeedsReview    bool        `json:"needs_review"`
	UnfilledPlaces int         `json:"unfilled_places"`
}
type Excluded struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type Result struct {
	Awards        []Award    `json:"awards"`
	ExcludedTeams []Excluded `json:"excluded_teams"`
	Warnings      []string   `json:"warnings"`
}
type teamStats struct {
	Team
	score, first, growth, increase int64
	hasBaseline                    bool
	count                          int64
}

func prepare(in Input) ([]teamStats, []Excluded, error) {
	if in.Start.IsZero() || in.Close.IsZero() || in.End.IsZero() || !in.Start.Before(in.Close) || !in.Close.Before(in.End) {
		return nil, nil, errors.New("contest start < scoreboard close < contest end required")
	}
	if in.Teams == nil {
		return nil, nil, errors.New("teams must be an array")
	}
	teams := []teamStats{}
	excluded := []Excluded{}
	ids, ranks := map[int64]bool{}, map[int64]bool{}
	for _, team := range in.Teams {
		if team.ID <= 0 || ids[team.ID] {
			return nil, nil, fmt.Errorf("invalid or duplicate team id: %d", team.ID)
		}
		ids[team.ID] = true
		if team.Name == "" || team.MemberCount <= 0 || team.Disqualified == nil || team.Runs == nil {
			return nil, nil, fmt.Errorf("team %d: name, positive member_count, disqualified and runs required", team.ID)
		}
		runs := []Run{}
		for _, r := range team.Runs {
			if r.At.IsZero() || r.Score == nil || *r.Score < 0 || r.Passed == nil {
				return nil, nil, fmt.Errorf("team %d: valid at, nonnegative score and passed required", team.ID)
			}
			if !*r.Passed {
				zero := int64(0)
				r.Score = &zero
			}
			if !r.At.Before(in.Start) && r.At.Before(in.End) {
				runs = append(runs, r)
			}
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].At.Before(runs[j].At) })
		for i := 1; i < len(runs); i++ {
			if runs[i].At.Equal(runs[i-1].At) {
				return nil, nil, fmt.Errorf("team %d: duplicate run timestamp", team.ID)
			}
		}
		reason := ""
		switch {
		case *team.Disqualified:
			reason = "disqualified"
		case len(runs) == 0:
			reason = "no contest runs"
		case !*runs[len(runs)-1].Passed:
			reason = "final run failed"
		}
		if reason != "" {
			excluded = append(excluded, Excluded{team.ID, team.Name, reason})
			continue
		}
		if team.FinalRank <= 0 || ranks[team.FinalRank] {
			return nil, nil, fmt.Errorf("team %d: invalid or duplicate final rank", team.ID)
		}
		ranks[team.FinalRank] = true
		s := teamStats{Team: team, score: *runs[len(runs)-1].Score, first: *runs[0].Score, count: int64(len(runs))}
		var baseline int64
		for i, r := range runs {
			if r.At.Before(in.Close) {
				s.hasBaseline = true
				if *r.Score > baseline {
					baseline = *r.Score
				}
			}
			if i > 0 {
				delta := *r.Score - *runs[i-1].Score
				if delta > s.increase {
					s.increase = delta
				}
			}
		}
		s.growth = s.score - baseline
		teams = append(teams, s)
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].FinalRank < teams[j].FinalRank })
	return teams, excluded, nil
}

type metric []*big.Rat

func numbers(values ...int64) metric {
	out := make(metric, len(values))
	for i, v := range values {
		out[i] = big.NewRat(v, 1)
	}
	return out
}
func compare(a, b metric) int {
	for i := range a {
		if c := a[i].Cmp(b[i]); c != 0 {
			return c
		}
	}
	return 0
}
func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func digitCount(v int64, digit string) int64 {
	return int64(strings.Count(strconv.FormatInt(v, 10), digit))
}
func hexCount(t teamStats) int64 { return int64(strings.Count(strconv.FormatInt(t.score, 16), "a")) }
func hasTriple(v int64) bool {
	var counts [10]int
	for _, c := range strconv.FormatInt(v, 10) {
		counts[c-'0']++
		if counts[c-'0'] >= 3 {
			return true
		}
	}
	return false
}

type rule struct {
	id, name string
	limit    int
	key      func(teamStats) metric
}

func rules(maxA int64) []rule {
	rank := func(t teamStats) metric { return numbers(t.FinalRank) }
	fixed := func(ranks ...int64) func(teamStats) metric {
		return func(t teamStats) metric {
			for _, r := range ranks {
				if t.FinalRank == r {
					return rank(t)
				}
			}
			return nil
		}
	}
	return []rule{
		{"hatena", "激流賞", 3, func(t teamStats) metric {
			if !t.hasBaseline || t.growth <= 0 {
				return nil
			}
			return numbers(-t.growth)
		}},
		{"layerx", "チームメンバー、何人いるんで賞", 1, func(t teamStats) metric { return metric{big.NewRat(-t.score, t.MemberCount)} }},
		{"m3", "M3賞", 3, fixed(3, 33, 333)},
		{"lineyahoo", "スピード10倍賞 by LINEヤフー", 1, func(t teamStats) metric {
			if t.first == 0 {
				return nil
			}
			v := big.NewRat(t.score, t.first)
			v.Sub(v, big.NewRat(10, 1))
			return metric{v.Abs(v)}
		}},
		{"kokorozashi", "ココロザシ賞", 1, func(t teamStats) metric { return numbers(abs(t.score - 55634)) }},
		{"forcia", "フォルシア賞", 1, fixed(25)},
		{"kauche", "カウシェ800万ダウンロード記念賞", 1, func(t teamStats) metric {
			return numbers(-digitCount(t.score, "8"), -digitCount(t.FinalRank, "8"), t.FinalRank)
		}},
		{"enechain", "Fly High賞 by enechain", 1, func(t teamStats) metric {
			if t.increase <= 0 {
				return nil
			}
			return numbers(-t.increase)
		}},
		{"freee", "freee賞", 3, func(t teamStats) metric {
			if !hasTriple(t.score) {
				return nil
			}
			return rank(t)
		}},
		{"splunk", "Splunk O11y賞", 1, fixed(110)},
		{"algoartis", "A（エース）がいっぱい賞", 3, func(t teamStats) metric {
			if hexCount(t) != maxA {
				return nil
			}
			return rank(t)
		}},
		{"cyberagent", "0xCA賞", 1, fixed(202)},
		{"kakehashi", "カケハシ10周年賞", 1, func(t teamStats) metric { return numbers(abs(t.score-20260), t.count) }},
	}
}
func selectAward(teams []teamStats, r rule) Award {
	type entry struct {
		team teamStats
		key  metric
	}
	entries := []entry{}
	for _, t := range teams {
		if key := r.key(t); key != nil {
			entries = append(entries, entry{t, key})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		c := compare(entries[i].key, entries[j].key)
		if c == 0 {
			return entries[i].team.FinalRank < entries[j].team.FinalRank
		}
		return c < 0
	})
	result := Award{ID: r.id, Name: r.name, Winners: []Candidate{}, TiedCandidates: []Candidate{}}
	var cutoff metric
	if len(entries) > r.limit && compare(entries[r.limit-1].key, entries[r.limit].key) == 0 {
		cutoff = entries[r.limit-1].key
	}
	for i, e := range entries {
		c := Candidate{ID: e.team.ID, Name: e.team.Name, FinalRank: e.team.FinalRank, FinalScore: e.team.score, Metric: []string{}}
		for _, v := range e.key {
			c.Metric = append(c.Metric, v.RatString())
		}
		if cutoff != nil {
			switch compare(e.key, cutoff) {
			case -1:
				result.Winners = append(result.Winners, c)
			case 0:
				result.TiedCandidates = append(result.TiedCandidates, c)
			}
		} else if i < r.limit {
			result.Winners = append(result.Winners, c)
		}
	}
	result.NeedsReview = len(result.TiedCandidates) > 0
	result.UnfilledPlaces = r.limit - len(result.Winners)
	return result
}
func evaluate(reader io.Reader) (Result, error) {
	var in Input
	dec := json.NewDecoder(reader)
	if err := dec.Decode(&in); err != nil {
		return Result{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			err = errors.New("expected exactly one JSON document")
		}
		return Result{}, err
	}
	teams, excluded, err := prepare(in)
	if err != nil {
		return Result{}, err
	}
	result := Result{Awards: []Award{}, ExcludedTeams: excluded, Warnings: []string{}}
	var maxA int64
	for _, t := range teams {
		if n := hexCount(t); n > maxA {
			maxA = n
		}
		if t.first == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("team %d: first score is zero; excluded from lineyahoo", t.ID))
		}
		if !t.hasBaseline {
			result.Warnings = append(result.Warnings, fmt.Sprintf("team %d: no pre-close baseline; excluded from hatena", t.ID))
		}
	}
	for _, r := range rules(maxA) {
		result.Awards = append(result.Awards, selectAward(teams, r))
	}
	return result, nil
}
func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "Usage: check-award INPUT\nEvaluate corporate awards from a JSON file, or - for stdin. See scripts/README.md.")
		return 0
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "check-award:", err); return 2 }
	if len(args) != 1 {
		return fail(errors.New("usage: check-award INPUT (JSON file or - for stdin)"))
	}
	reader := stdin
	if args[0] != "-" {
		file, err := os.Open(args[0])
		if err != nil {
			return fail(err)
		}
		defer file.Close()
		reader = file
	}
	result, err := evaluate(reader)
	if err != nil {
		return fail(err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		return fail(err)
	}
	return 0
}
func main() { os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
