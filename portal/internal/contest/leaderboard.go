package contest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	"github.com/isucon/isucon2026/portal/internal/config"
)

// Visibility は benchmark_results の可視性 3 系統（DM-7）。
type Visibility int

const (
	VisibilityAudience Visibility = iota
	VisibilityContestant
	VisibilityAdmin
)

// LeaderboardParams は Contest.leaderboard のキーワード引数に対応する。
type LeaderboardParams struct {
	Admin    bool
	TeamID   int64 // 0 = team なし
	Audience bool
	Solo     bool
	SoloItem bool
	Now      *time.Time // dump_leaderboard 用の任意時点。指定時は history 強制 true
	History  bool
}

func (p LeaderboardParams) visibility() Visibility {
	switch {
	case p.Audience:
		return VisibilityAudience
	case p.Admin:
		return VisibilityAdmin
	case p.TeamID != 0:
		return VisibilityContestant
	default:
		return VisibilityAudience
	}
}

type scoreRow struct {
	score     int64
	passed    sql.NullBool
	createdAt time.Time
	markedAt  time.Time
}

type teamRow struct {
	id                    int64
	name                  string
	leaderID              sql.NullInt64
	finalParticipation    bool
	hidden                bool
	withdrawn             bool
	disqualified          bool
	onsiteSupportExcluded bool
	student               bool
	bestID                sql.NullInt64
	bestScore             sql.NullInt64
	bestCreatedAt         sql.NullTime
	bestMarkedAt          sql.NullTime
}

// condBuilder は $n プレースホルダを採番しながら WHERE 句を組み立てる。
type condBuilder struct {
	conds []string
	args  []any
}

// add は cond 中の "?" を順に $n へ置き換えて追加する。
func (b *condBuilder) add(cond string, args ...any) {
	for _, a := range args {
		b.args = append(b.args, a)
		cond = strings.Replace(cond, "?", "$"+strconv.Itoa(len(b.args)), 1)
	}
	b.conds = append(b.conds, cond)
}

func (b *condBuilder) where() string { return strings.Join(b.conds, " AND ") }

// addVisibility は BenchmarkResult.of_visibility 相当の WHERE 句を追加する。
// successfully_finished は呼び出し側で必ず併用する。
func (b *condBuilder) addVisibility(vis Visibility, t config.Timing, teamID int64) {
	switch vis {
	case VisibilityAdmin:
		return
	case VisibilityAudience:
		teamID = 0 // visible_not_frozen(nil): team_id = NULL は決してマッチしない
	}
	if t.ContestEnd != nil {
		b.add("benchmark_results.marked_at < ?", t.ContestEnd.UTC())
	}
	if t.ContestFreeze != nil {
		b.add("(benchmark_results.team_id = ? OR benchmark_results.marked_at < ?)", teamID, t.ContestFreeze.UTC())
	}
}

const successfullyFinished = "benchmark_results.finished = true AND benchmark_results.exit_status = 0 AND benchmark_results.exit_signal IS NULL"

// BuildLeaderboard は Contest.leaderboard 相当。
// SoloItem 指定時は LeaderboardItem を、それ以外は Leaderboard を返す。
func BuildLeaderboard(ctx context.Context, db *sql.DB, t config.Timing, now time.Time, p LeaderboardParams) (*pbresources.Leaderboard, *pbresources.LeaderboardItem, error) {
	if p.Solo && p.TeamID == 0 {
		return nil, nil, fmt.Errorf("solo requested but no team given")
	}
	history := p.History
	if p.Now != nil {
		history = true
	}
	vis := p.visibility()

	byTeam, order, err := fetchScores(ctx, db, t, p, vis, history)
	if err != nil {
		return nil, nil, err
	}

	teamList, err := fetchTeams(ctx, db, vis, p)
	if err != nil {
		return nil, nil, err
	}
	teamMap := make(map[int64]*teamRow, len(teamList))
	for i := range teamList {
		teamMap[teamList[i].id] = &teamList[i]
	}

	teamExists := map[int64]bool{}
	var items []*pbresources.LeaderboardItem
	for _, tid := range order {
		tr, ok := teamMap[tid]
		if !ok {
			continue
		}
		rows := byTeam[tid]
		var best *pbresources.LeaderboardItem_LeaderboardScore
		scores := make([]*pbresources.LeaderboardItem_LeaderboardScore, 0, len(rows))
		for _, r := range rows {
			s := r.score
			if r.passed.Valid && !r.passed.Bool {
				s = 0
			}
			i := &pbresources.LeaderboardItem_LeaderboardScore{
				Score:     s,
				StartedAt: timestamppb.New(r.createdAt),
				MarkedAt:  timestamppb.New(r.markedAt),
			}
			if best == nil || i.Score > best.Score {
				best = i
			}
			scores = append(scores, i)
		}

		item := &pbresources.LeaderboardItem{
			Team:        teamPB(tr),
			LatestScore: scores[len(scores)-1],
		}
		if history {
			item.ScoreHistory = &pbresources.LeaderboardItem_History{Scores: scores}
		}
		if p.Now != nil {
			item.BestScore = best
		} else if tr.bestID.Valid {
			item.BestScore = &pbresources.LeaderboardItem_LeaderboardScore{
				Score:     tr.bestScore.Int64,
				StartedAt: timestamppb.New(tr.bestCreatedAt.Time),
				MarkedAt:  timestamppb.New(tr.bestMarkedAt.Time),
			}
		}
		teamExists[tid] = true
		items = append(items, item)
	}

	// ソート: latest_score の score 降順、同点は marked_at の早い順（contest.rb と同じ）
	sort.SliceStable(items, func(i, j int) bool {
		si, sj := items[i].LatestScore, items[j].LatestScore
		if si.Score != sj.Score {
			return si.Score > sj.Score
		}
		if si.MarkedAt.GetSeconds() != sj.MarkedAt.GetSeconds() {
			return si.MarkedAt.GetSeconds() < sj.MarkedAt.GetSeconds()
		}
		return si.MarkedAt.GetNanos() < sj.MarkedAt.GetNanos()
	})

	// 結果のないチームを id 順で末尾に追加
	for i := range teamList {
		tr := &teamList[i]
		if teamExists[tr.id] {
			continue
		}
		items = append(items, &pbresources.LeaderboardItem{
			Team:         teamPB(tr),
			ScoreHistory: &pbresources.LeaderboardItem_History{Scores: []*pbresources.LeaderboardItem_LeaderboardScore{}},
		})
	}

	if p.SoloItem && p.Solo {
		if len(items) != 1 {
			return nil, nil, fmt.Errorf("solo but %d team items computed", len(items))
		}
		return nil, items[0], nil
	}

	var visible, hidden []*pbresources.LeaderboardItem
	for _, item := range items {
		if item.Team.Disqualified || item.Team.Withdrawn {
			continue
		}
		if item.Team.Hidden {
			hidden = append(hidden, item)
		} else {
			visible = append(visible, item)
		}
	}

	return &pbresources.Leaderboard{
		Teams:       visible,
		HiddenTeams: hidden,
		Progresses:  []*pbresources.LeaderboardItem{},
		Contest:     ContestPB(t, now),
		GeneratedAt: timestamppb.New(now),
	}, nil, nil
}

func fetchScores(ctx context.Context, db *sql.DB, t config.Timing, p LeaderboardParams, vis Visibility, history bool) (map[int64][]scoreRow, []int64, error) {
	b := &condBuilder{}
	b.add(successfullyFinished)
	b.addVisibility(vis, t, p.TeamID)
	if p.Now != nil {
		b.add("benchmark_results.marked_at <= ?", p.Now.UTC())
	}
	if p.Solo {
		b.add("benchmark_results.team_id = ?", p.TeamID)
	}
	where, args := b.where(), b.args

	var query string
	if history {
		query = `SELECT team_id, score, passed, created_at, marked_at FROM benchmark_results WHERE ` + where + ` ORDER BY marked_at ASC`
	} else {
		// Rails: group(:team_id).pluck('max(id)') → その id の行だけを読む
		query = `SELECT b.team_id, b.score, b.passed, b.created_at, b.marked_at
			FROM benchmark_results b
			JOIN (SELECT MAX(id) AS id FROM benchmark_results WHERE ` + where + ` GROUP BY team_id) m ON m.id = b.id`
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	byTeam := map[int64][]scoreRow{}
	var order []int64
	for rows.Next() {
		var tid int64
		var r scoreRow
		if err := rows.Scan(&tid, &r.score, &r.passed, &r.createdAt, &r.markedAt); err != nil {
			return nil, nil, err
		}
		if _, ok := byTeam[tid]; !ok {
			order = append(order, tid)
		}
		byTeam[tid] = append(byTeam[tid], r)
	}
	return byTeam, order, rows.Err()
}

func fetchTeams(ctx context.Context, db *sql.DB, vis Visibility, p LeaderboardParams) ([]teamRow, error) {
	var bestCol string
	var args []any
	switch vis {
	case VisibilityAdmin:
		bestCol = "t.best_benchmark_result_for_admin_id"
	case VisibilityAudience:
		bestCol = "t.best_benchmark_result_for_audience_id"
	case VisibilityContestant:
		// 自チームだけ凍結後も見える best カラムを使う
		bestCol = "CASE WHEN t.id = $1 THEN t.best_benchmark_result_id ELSE t.best_benchmark_result_for_audience_id END"
		args = append(args, p.TeamID)
	}

	query := `SELECT t.id, t.name, t.leader_id, t.final_participation, t.is_hidden, t.withdrawn, t.disqualified, t.onsite_support_excluded, t.student,
		b.id, b.score, b.created_at, b.marked_at
		FROM teams t LEFT JOIN benchmark_results b ON b.id = ` + bestCol
	if p.Solo {
		args = append(args, p.TeamID)
		query += ` WHERE t.id = $` + strconv.Itoa(len(args))
	} else {
		query += ` WHERE t.withdrawn = false AND t.disqualified = false ORDER BY t.id ASC`
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []teamRow
	for rows.Next() {
		var tr teamRow
		if err := rows.Scan(&tr.id, &tr.name, &tr.leaderID, &tr.finalParticipation, &tr.hidden, &tr.withdrawn, &tr.disqualified, &tr.onsiteSupportExcluded, &tr.student,
			&tr.bestID, &tr.bestScore, &tr.bestCreatedAt, &tr.bestMarkedAt); err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// teamPB は Team#to_pb(detail: false, members: false) 相当。
func teamPB(tr *teamRow) *pbresources.Team {
	return &pbresources.Team{
		Id:                    tr.id,
		Name:                  tr.name,
		LeaderId:              tr.leaderID.Int64,
		MemberIds:             []int64{},
		FinalParticipation:    tr.finalParticipation,
		Hidden:                tr.hidden,
		Withdrawn:             tr.withdrawn,
		Disqualified:          tr.disqualified,
		OnsiteSupportExcluded: tr.onsiteSupportExcluded,
		Student:               &pbresources.Team_StudentStatus{Status: tr.student},
	}
}

// MaintainBestBenchmarkResults は MaintainBestBenchmarkResultOfTeamJob 相当を全チームに適用する。
// 非正規化カラム 3 本（DM-8）を更新する。
func MaintainBestBenchmarkResults(ctx context.Context, db *sql.DB, t config.Timing, now *time.Time) error {
	rows, err := db.QueryContext(ctx, `SELECT id FROM teams`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var teamIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		teamIDs = append(teamIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, teamID := range teamIDs {
		if err := MaintainBestForTeam(ctx, db, t, teamID, now); err != nil {
			return err
		}
	}
	return nil
}

// MaintainBestForTeam は MaintainBestBenchmarkResultOfTeamJob.perform(team:) 相当。
func MaintainBestForTeam(ctx context.Context, db *sql.DB, t config.Timing, teamID int64, now *time.Time) error {
	best := map[Visibility]sql.NullInt64{}
	for _, vis := range []Visibility{VisibilityContestant, VisibilityAudience, VisibilityAdmin} {
		b := &condBuilder{}
		b.add(successfullyFinished)
		b.add("benchmark_results.team_id = ?", teamID)
		b.addVisibility(vis, t, teamID)
		if now != nil {
			b.add("benchmark_results.marked_at <= ?", now.UTC())
		}
		var id sql.NullInt64
		err := db.QueryRowContext(ctx,
			`SELECT id FROM benchmark_results WHERE `+b.where()+` ORDER BY score DESC LIMIT 1`,
			b.args...).Scan(&id)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		best[vis] = id
	}
	_, err := db.ExecContext(ctx,
		`UPDATE teams SET best_benchmark_result_id = $1, best_benchmark_result_for_audience_id = $2, best_benchmark_result_for_admin_id = $3, updated_at = now() WHERE id = $4`,
		best[VisibilityContestant], best[VisibilityAudience], best[VisibilityAdmin], teamID)
	return err
}
