// check-award evaluates the corporate awards described in docs/manual.md.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
)

type Run struct {
	EnqueuedAt time.Time `json:"enqueued_at"`
	At         time.Time `json:"at"`
	Score      *int64    `json:"score"`
	Passed     *bool     `json:"passed"`
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
	NearMisses     []Candidate `json:"near_misses"`
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
			if r.EnqueuedAt.IsZero() || r.At.IsZero() || r.EnqueuedAt.After(r.At) || r.Score == nil || *r.Score < 0 || r.Passed == nil {
				return nil, nil, fmt.Errorf("team %d: valid enqueued_at <= at, nonnegative score and passed required", team.ID)
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
			if r.EnqueuedAt.Before(in.Close) {
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
		{"m3", "M3賞", 3, fixed(fixedRanks("m3")...)},
		{"lineyahoo", "スピード10倍賞 by LINEヤフー", 1, func(t teamStats) metric {
			if t.first == 0 {
				return nil
			}
			v := big.NewRat(t.score, t.first)
			v.Sub(v, big.NewRat(10, 1))
			return metric{v.Abs(v)}
		}},
		{"kokorozashi", "ココロザシ賞", 1, func(t teamStats) metric { return numbers(abs(t.score - 55634)) }},
		{"forcia", "フォルシア賞", 1, fixed(fixedRanks("forcia")...)},
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
		{"splunk", "Splunk O11y賞", 1, fixed(fixedRanks("splunk")...)},
		{"algoartis", "A（エース）がいっぱい賞", 3, func(t teamStats) metric {
			if hexCount(t) != maxA {
				return nil
			}
			return numbers(-hexCount(t), t.FinalRank)
		}},
		{"cyberagent", "0xCA賞", 1, fixed(fixedRanks("cyberagent")...)},
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
		c := candidate(e.team, e.key)
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
	result.NearMisses = nearMisses(teams, r, result)
	return result
}
func candidate(t teamStats, key metric) Candidate {
	c := Candidate{ID: t.ID, Name: t.Name, FinalRank: t.FinalRank, FinalScore: t.score, Metric: []string{}}
	for _, v := range key {
		c.Metric = append(c.Metric, v.RatString())
	}
	return c
}

func fixedRanks(id string) []int64 {
	switch id {
	case "m3":
		return []int64{3, 33, 333}
	case "forcia":
		return []int64{25}
	case "splunk":
		return []int64{110}
	case "cyberagent":
		return []int64{202}
	}
	return nil
}

// nearMisses keeps unresolved ties separate and never includes ineligible teams.
func nearMisses(teams []teamStats, r rule, award Award) []Candidate {
	out := []Candidate{}
	if len(fixedRanks(r.id)) > 0 {
		return out
	}
	excluded := map[int64]bool{}
	for _, group := range [][]Candidate{award.Winners, award.TiedCandidates} {
		for _, t := range group {
			excluded[t.ID] = true
		}
	}
	type entry struct {
		team teamStats
		key  metric
	}
	entries := []entry{}
	for _, t := range teams {
		if excluded[t.ID] {
			continue
		}
		key := r.key(t)
		// Teams with fewer As are the next candidates even when fewer than three win.
		if r.id == "algoartis" {
			key = numbers(-hexCount(t), t.FinalRank)
		}
		if key != nil {
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
	for i, e := range entries {
		if i >= 3 && compare(e.key, entries[2].key) != 0 {
			break
		}
		out = append(out, candidate(e.team, e.key))
	}
	return out
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
			result.Warnings = append(result.Warnings, fmt.Sprintf("team %d: no run enqueued before close; excluded from hatena", t.ID))
		}
	}
	for _, r := range rules(maxA) {
		result.Awards = append(result.Awards, selectAward(teams, r))
	}
	return result, nil
}

// Names go last so wide Japanese characters do not affect column alignment.
// Strip control characters to keep user-provided names on a single terminal row.
func terminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
func displayMetric(id string, c Candidate) string {
	m := c.Metric
	positive := func(i int) string { return strings.TrimPrefix(m[i], "-") }
	switch id {
	case "hatena", "enechain":
		return "gain=" + positive(0)
	case "layerx":
		return "score/member=" + positive(0)
	case "lineyahoo":
		return "10x-distance=" + m[0]
	case "kokorozashi":
		return "55634-distance=" + m[0]
	case "kakehashi":
		return "20260-distance=" + m[0] + ", runs=" + m[1]
	case "kauche":
		return "8s:score=" + positive(0) + ", rank=" + positive(1)
	case "algoartis":
		return "As=" + positive(0)
	default:
		return "rank=" + m[0]
	}
}
func writeTable(w io.Writer, result Result) error {
	var out strings.Builder
	fmt.Fprintln(&out, "WIN = 受賞 / TIE = 同点候補（要確認） / NEAR = 前後・次点")
	for _, a := range result.Awards {
		fmt.Fprintf(&out, "\n%s [%s]\n", a.Name, a.ID)
		if a.NeedsReview {
			fmt.Fprintln(&out, "REVIEW: 同点候補からの選定が必要")
		}
		if a.UnfilledPlaces > 0 {
			fmt.Fprintf(&out, "未確定・未充足の枠: %d\n", a.UnfilledPlaces)
		}
		type row struct {
			status string
			c      Candidate
		}
		rows := []row{}
		for _, group := range []struct {
			status     string
			candidates []Candidate
		}{{"WIN", a.Winners}, {"TIE", a.TiedCandidates}, {"NEAR", a.NearMisses}} {
			for _, c := range group.candidates {
				rows = append(rows, row{group.status, c})
			}
		}
		if len(rows) == 0 {
			fmt.Fprintln(&out, "(no candidates)")
			continue
		}
		table := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "STATUS\tRANK\tSCORE\tID\tVALUE\tTEAM")
		fmt.Fprintln(table, "------\t----\t-----\t--\t-----\t----")
		for _, r := range rows {
			fmt.Fprintf(table, "%s\t%d\t%d\t%d\t%s\t%s\n", r.status, r.c.FinalRank, r.c.FinalScore, r.c.ID, displayMetric(a.ID, r.c), terminalText(r.c.Name))
		}
		if err := table.Flush(); err != nil {
			return err
		}
	}
	if len(result.ExcludedTeams) > 0 {
		fmt.Fprintln(&out, "\nExcluded:")
		for _, t := range result.ExcludedTeams {
			fmt.Fprintf(&out, "  %d: %s (%s)\n", t.ID, terminalText(t.Name), t.Reason)
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Fprintln(&out, "\nWarnings:")
		for _, s := range result.Warnings {
			fmt.Fprintln(&out, "  "+s)
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, "check-award:", err); return 2 }
	flags := flag.NewFlagSet("check-award", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "output JSON instead of a terminal table")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: check-award [--json] INPUT\nEvaluate corporate awards from a JSON file, or - for stdin. See scripts/README.md.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	args = flags.Args()
	if len(args) != 1 {
		return fail(errors.New("usage: check-award [--json] INPUT (JSON file or - for stdin)"))
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
	if !*jsonOutput {
		if err := writeTable(stdout, result); err != nil {
			return fail(err)
		}
		return 0
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
