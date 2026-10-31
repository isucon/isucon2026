package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runInput(score int64, hour int, passed bool) map[string]any {
	return map[string]any{"at": fmt.Sprintf("2026-10-31T%02d:00:00+09:00", hour), "score": score, "passed": passed}
}
func teamInput(id, score int64, runs ...map[string]any) map[string]any {
	if runs == nil {
		runs = []map[string]any{runInput(score, 16, true)}
	}
	return map[string]any{"id": id, "name": fmt.Sprintf("Team %d", id), "member_count": 2, "final_rank": id, "disqualified": false, "runs": runs}
}
func document(teams ...map[string]any) []byte {
	if teams == nil {
		teams = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"contest_started_at": "2026-10-31T10:00:00+09:00", "scoreboard_closed_at": "2026-10-31T17:00:00+09:00", "contest_ended_at": "2026-10-31T18:00:00+09:00", "teams": teams})
	if err != nil {
		panic(err)
	}
	return b
}
func awardResult(t *testing.T, id string, teams ...map[string]any) Award {
	t.Helper()
	result, err := evaluate(bytes.NewReader(document(teams...)))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range result.Awards {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("missing award %s", id)
	return Award{}
}
func ids(c []Candidate) []int64 {
	out := []int64{}
	for _, v := range c {
		out = append(out, v.ID)
	}
	return out
}
func TestAwards(t *testing.T) {
	member := teamInput(2, 90)
	member["member_count"] = 1
	dq := teamInput(3, 999999)
	dq["disqualified"] = true
	cases := []struct {
		name, award string
		teams       []map[string]any
		want        []int64
	}{
		{"members", "layerx", []map[string]any{teamInput(1, 100), member}, []int64{2}},
		{"final not best and exclusions", "kokorozashi", []map[string]any{teamInput(1, 0, runInput(999999, 15, true), runInput(0, 16, false)), teamInput(2, 0, runInput(99999, 15, true), runInput(55634, 16, true)), dq, teamInput(4, 0, []map[string]any{}...)}, []int64{2}},
		{"boundaries", "hatena", []map[string]any{teamInput(1, 0, runInput(900000, 9, true), runInput(100, 16, true), runInput(500, 17, true), runInput(999999, 18, true)), teamInput(2, 0, runInput(100, 15, true), runInput(200, 16, true), runInput(300, 17, true))}, []int64{1, 2}},
		{"ratio", "lineyahoo", []map[string]any{teamInput(1, 0, runInput(100, 15, true), runInput(1050, 16, true)), teamInput(2, 0, runInput(1, 15, true), runInput(11, 16, true))}, []int64{1}},
		{"zero first", "lineyahoo", []map[string]any{teamInput(1, 0, runInput(0, 14, false), runInput(10, 15, true), runInput(100, 16, true))}, []int64{}},
		{"freee nonconsecutive", "freee", []map[string]any{teamInput(1, 91919), teamInput(2, 8888), teamInput(3, 12121), teamInput(4, 111), teamInput(5, 123)}, []int64{1, 2, 3}},
		{"hex tie", "algoartis", []map[string]any{teamInput(1, 0xAA), teamInput(2, 0xAA), teamInput(3, 0xAA), teamInput(4, 0xAA), teamInput(5, 0xA)}, []int64{1, 2, 3}},
		{"hex maximum only", "algoartis", []map[string]any{teamInput(1, 0xAA), teamInput(2, 0xA)}, []int64{1}},
		{"count failures", "kakehashi", []map[string]any{teamInput(1, 0, runInput(0, 15, false), runInput(20259, 16, true)), teamInput(2, 20261)}, []int64{2}},
		{"no baseline", "hatena", []map[string]any{teamInput(1, 0, runInput(100, 17, true)), teamInput(2, 0, runInput(200, 15, true), runInput(100, 16, true))}, []int64{}},
		{"no increase", "enechain", []map[string]any{teamInput(1, 100), teamInput(2, 0, runInput(200, 15, true), runInput(100, 16, true))}, []int64{}},
		{"best baseline sorted", "hatena", []map[string]any{teamInput(1, 0, runInput(150, 17, true), runInput(200, 14, true), runInput(100, 16, true))}, []int64{}},
		{"failure is zero", "enechain", []map[string]any{teamInput(1, 0, runInput(100, 10, true), runInput(999999, 15, false), runInput(200, 16, true)), teamInput(2, 0, runInput(100, 15, true), runInput(250, 16, true))}, []int64{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := awardResult(t, tc.award, tc.teams...)
			if got := ids(a.Winners); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
func TestFixedRanks(t *testing.T) {
	teams := []map[string]any{}
	for _, r := range []int64{3, 25, 33, 110, 202, 333} {
		teams = append(teams, teamInput(r, 100000-r))
	}
	for name, want := range map[string][]int64{"m3": {3, 33, 333}, "forcia": {25}, "splunk": {110}, "cyberagent": {202}} {
		if got := ids(awardResult(t, name, teams...).Winners); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v", name, got)
		}
	}
}
func TestKauche(t *testing.T) {
	a, b, c := teamInput(1, 1888), teamInput(2, 888), teamInput(3, 88)
	a["final_rank"] = 18
	b["final_rank"] = 28
	c["final_rank"] = 88
	if got := ids(awardResult(t, "kauche", a, b, c).Winners); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatal(got)
	}
}
func TestTies(t *testing.T) {
	a := awardResult(t, "kokorozashi", teamInput(1, 55633), teamInput(2, 55635))
	if !a.NeedsReview || len(a.Winners) != 0 || !reflect.DeepEqual(ids(a.TiedCandidates), []int64{1, 2}) {
		t.Fatal(a)
	}
	teams := []map[string]any{}
	for i, g := range []int64{100, 90, 80, 80} {
		teams = append(teams, teamInput(int64(i+1), 0, runInput(100, 16, true), runInput(100+g, 17, true)))
	}
	a = awardResult(t, "hatena", teams...)
	if !reflect.DeepEqual(ids(a.Winners), []int64{1, 2}) || !reflect.DeepEqual(ids(a.TiedCandidates), []int64{3, 4}) || a.UnfilledPlaces != 1 {
		t.Fatal(a)
	}
}
func TestEmpty(t *testing.T) {
	r, err := evaluate(bytes.NewReader(document()))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Awards) != 13 {
		t.Fatal(r)
	}
	for _, a := range r.Awards {
		if a.Winners == nil || len(a.Winners) != 0 || a.TiedCandidates == nil {
			t.Fatal(a)
		}
	}
}
func TestInvalidInput(t *testing.T) {
	cases := []string{"null", "[]", "{}", string(document()) + " {}"}
	for _, field := range []string{"member_count", "final_rank", "disqualified"} {
		for _, v := range []any{nil, -1, "bad"} {
			a := teamInput(1, 100)
			a[field] = v
			cases = append(cases, string(document(a)))
		}
	}
	for field, values := range map[string][]any{"score": {nil, -1, 1.5, true}, "passed": {nil, "true"}, "at": {nil, "2026-10-31T16:00:00"}} {
		for _, v := range values {
			r := runInput(1, 16, true)
			r[field] = v
			cases = append(cases, string(document(teamInput(1, 0, r))))
		}
	}
	cases = append(cases, string(document(teamInput(1, 10), teamInput(1, 20))), string(document(teamInput(1, 0, runInput(1, 16, true), runInput(2, 16, true)))))
	for i, s := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := evaluate(strings.NewReader(s)); err == nil {
				t.Fatalf("accepted %s", s)
			}
		})
	}
}
func TestCLI(t *testing.T) {
	for _, input := range []string{"-", filepath.Join(t.TempDir(), "input.json")} {
		if input != "-" {
			if err := os.WriteFile(input, document(teamInput(1, 55634)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		var out, stderr bytes.Buffer
		if code := runCLI([]string{"--json", input}, bytes.NewReader(document(teamInput(1, 55634))), &out, &stderr); code != 0 {
			t.Fatalf("%d: %s", code, &stderr)
		}
		var result Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Awards) != 13 {
			t.Fatalf("%s: %v", &out, err)
		}
	}
	for _, args := range [][]string{nil, {"-"}, {"missing.json"}, {"-", "extra"}} {
		var out, stderr bytes.Buffer
		if code := runCLI(args, strings.NewReader("{}"), &out, &stderr); code != 2 {
			t.Fatalf("%v: %d", args, code)
		}
	}
}

func TestLargeScoresRemainExact(t *testing.T) {
	// These scores round to the same float64. The larger ratio must still win.
	a, b := teamInput(1, 9007199254740992), teamInput(2, 9007199254740993)
	if got := ids(awardResult(t, "layerx", a, b).Winners); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatal(got)
	}
	// Subtracting ten times the initial score must not overflow int64.
	a = teamInput(1, 0, runInput(9223372036854775807, 15, true), runInput(9223372036854775807, 16, true))
	b = teamInput(2, 0, runInput(1, 15, true), runInput(2, 16, true))
	if got := ids(awardResult(t, "lineyahoo", a, b).Winners); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatal(got)
	}
}

func TestNearMisses(t *testing.T) {
	t.Run("next three including boundary ties", func(t *testing.T) {
		a := awardResult(t, "layerx", teamInput(1, 100), teamInput(2, 90), teamInput(3, 80), teamInput(4, 70), teamInput(5, 70), teamInput(6, 60))
		if got := ids(a.NearMisses); !reflect.DeepEqual(got, []int64{2, 3, 4, 5}) {
			t.Fatal(got)
		}
	})
	t.Run("unresolved candidates are not losers", func(t *testing.T) {
		a := awardResult(t, "kokorozashi", teamInput(1, 55633), teamInput(2, 55635), teamInput(3, 55630))
		if got := ids(a.NearMisses); !reflect.DeepEqual(got, []int64{3}) {
			t.Fatal(got)
		}
	})
	t.Run("each fixed rank has both neighbors", func(t *testing.T) {
		teams := []map[string]any{}
		for _, rank := range []int64{2, 3, 4, 32, 33, 34, 332, 333, 334} {
			teams = append(teams, teamInput(rank, 10000-rank))
		}
		a := awardResult(t, "m3", teams...)
		if got := ids(a.NearMisses); !reflect.DeepEqual(got, []int64{2, 4, 32, 34, 332, 334}) {
			t.Fatal(got)
		}
	})
	t.Run("missing winner and disqualified neighbor", func(t *testing.T) {
		dq := teamInput(24, 200)
		dq["disqualified"] = true
		a := awardResult(t, "forcia", teamInput(23, 300), dq, teamInput(26, 100))
		if got := ids(a.NearMisses); !reflect.DeepEqual(got, []int64{23, 26}) {
			t.Fatal(got)
		}
	})
	t.Run("hex lower counts included", func(t *testing.T) {
		a := awardResult(t, "algoartis", teamInput(1, 0xAA), teamInput(2, 0), teamInput(3, 0xA), teamInput(4, 0xA))
		if got := ids(a.NearMisses); !reflect.DeepEqual(got, []int64{3, 4, 2}) {
			t.Fatal(got)
		}
	})
	t.Run("freee still requires three digits", func(t *testing.T) {
		a := awardResult(t, "freee", teamInput(1, 123), teamInput(2, 111), teamInput(3, 222), teamInput(4, 333), teamInput(5, 444))
		if got := ids(a.NearMisses); !reflect.DeepEqual(got, []int64{5}) {
			t.Fatal(got)
		}
	})
	t.Run("empty arrays for every award", func(t *testing.T) {
		result, err := evaluate(bytes.NewReader(document()))
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range result.Awards {
			if a.NearMisses == nil || len(a.NearMisses) != 0 {
				t.Fatal(a)
			}
		}
	})
}

func TestExample(t *testing.T) {
	f, err := os.Open("testdata/check-award-example.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	result, err := evaluate(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range result.Awards {
		if len(a.Winners) == 0 || len(a.NearMisses) == 0 {
			t.Fatalf("example must show winners and near misses for %s", a.ID)
		}
	}
}

func TestTableCLI(t *testing.T) {
	var out, stderr bytes.Buffer
	teams := []map[string]any{teamInput(24, 65000), teamInput(25, 60000), teamInput(26, 55635)}
	if code := runCLI([]string{"-"}, bytes.NewReader(document(teams...)), &out, &stderr); code != 0 {
		t.Fatalf("%d: %s", code, &stderr)
	}
	text := out.String()
	for _, want := range []string{"フォルシア賞", "STATUS", "RANK", "SCORE", "NEAR", "WIN", "Team 25"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q: %s", want, text)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatal("default output is JSON")
	}
	start := strings.Index(text, "フォルシア賞")
	section := text[start : strings.Index(text[start:], "カウシェ")+start]
	if !(strings.Index(section, "Team 24") < strings.Index(section, "Team 25") && strings.Index(section, "Team 25") < strings.Index(section, "Team 26")) {
		t.Fatal(section)
	}
}
func TestTableTiesWarningsAndEmpty(t *testing.T) {
	var out, stderr bytes.Buffer
	dq := teamInput(4, 1)
	dq["disqualified"] = true
	code := runCLI([]string{"-"}, bytes.NewReader(document(teamInput(1, 55633), teamInput(2, 55635), teamInput(3, 0, runInput(0, 15, false), runInput(100, 17, true)), dq)), &out, &stderr)
	if code != 0 {
		t.Fatal(stderr.String())
	}
	for _, want := range []string{"TIE", "REVIEW", "Excluded", "disqualified", "Warnings", "first score is zero"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
	out.Reset()
	if code := runCLI([]string{"-"}, bytes.NewReader(document()), &out, &stderr); code != 0 || !strings.Contains(out.String(), "(no candidates)") {
		t.Fatal(out.String(), stderr.String())
	}
}

func TestTableExampleAndNames(t *testing.T) {
	f, err := os.Open("testdata/check-award-example.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out, stderr bytes.Buffer
	if code := runCLI([]string{"-"}, f, &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	if strings.Count(out.String(), "STATUS") != 13 {
		t.Fatal("missing award table")
	}
	for _, want := range []string{"gain=50000", "score/member=88888", "10x-distance=299/1996", "8s:score=5, rank=0", "As=1", "20260-distance=0, runs=3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
	team := teamInput(1, 100)
	team["name"] = "日本語\tチーム\n名前\x1b"
	out.Reset()
	if code := runCLI([]string{"-"}, bytes.NewReader(document(team)), &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	if !strings.Contains(out.String(), "日本語 チーム 名前 ") || strings.ContainsRune(out.String(), '\x1b') {
		t.Fatal("team name broke terminal output")
	}
}
