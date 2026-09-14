package handler

import (
	"net/http"

	pbaudience "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/audience"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// AudienceDashboard: GET /api/audience/dashboard
// 事前生成キャッシュがあれば SHA-384 ダイジェストを ETag に使い、
// 無ければ合成 ETag（leaderboard_etag）にフォールバックする（CT-5）。
func (h *Handler) AudienceDashboard(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	w.Header().Set("Cache-Control", "max-age=20, public, s-maxage=30")

	digest, wire, ok, err := h.cache.Read(ctx, "dashboard-v2:audience")
	if err != nil {
		return err
	}
	if ok {
		if staleCheckETag(w, r, etagFromDigest(digest[:16])) {
			renderRawPB(w, http.StatusOK, "isuxportal.proto.services.audience.DashboardResponse", wire)
		}
		return nil
	}

	etag, err := contest.LeaderboardEtag(ctx, h.db, ri.timing, false, 0)
	if err != nil {
		return err
	}
	if !staleCheck(w, r, etag) {
		return nil
	}
	lb, _, err := contest.BuildLeaderboard(ctx, h.db, ri.timing, ri.now, contest.LeaderboardParams{History: false})
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbaudience.DashboardResponse{Leaderboard: lb})
	return nil
}

// AudienceSoloDashboard: GET /api/audience/dashboard/teams/:id
func (h *Handler) AudienceSoloDashboard(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	teamID, ok := pathID(r)
	if !ok {
		return &model.NotFoundError{}
	}
	w.Header().Set("Cache-Control", "max-age=20, public, s-maxage=30")

	digest, wire, cached, err := h.cache.Read(ctx, "dashboard-v2:audience:team-"+chiParam(r, "id"))
	if err != nil {
		return err
	}
	if cached {
		if staleCheckETag(w, r, etagFromDigest(digest[:16])) {
			renderRawPB(w, http.StatusOK, "isuxportal.proto.services.audience.SoloDashboardResponse", wire)
		}
		return nil
	}

	team, err := model.FindTeam(ctx, h.db, teamID)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}
	etag, err := contest.LeaderboardEtag(ctx, h.db, ri.timing, false, teamID)
	if err != nil {
		return err
	}
	if !staleCheck(w, r, etag) {
		return nil
	}
	_, item, err := contest.BuildLeaderboard(ctx, h.db, ri.timing, ri.now, contest.LeaderboardParams{
		Audience: true, TeamID: teamID, Solo: true, SoloItem: true, History: true,
	})
	if err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbaudience.SoloDashboardResponse{LeaderboardItem: item})
	return nil
}

// AudienceTeams: GET /api/audience/teams（FR-A-3: hidden 除外）
func (h *Handler) AudienceTeams(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "max-age=60, public, s-maxage=300")

	rows, err := h.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.final_participation, t.student, c.name
		FROM teams t LEFT JOIN contestants c ON c.team_id = t.id
		WHERE t.withdrawn = false AND t.disqualified = false AND t.is_hidden = false
		ORDER BY t.id ASC, c.id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var items []*pbaudience.ListTeamsResponse_TeamListItem
	var current *pbaudience.ListTeamsResponse_TeamListItem
	for rows.Next() {
		var teamID int64
		var name string
		var finalParticipation, student bool
		var memberName *string
		if err := rows.Scan(&teamID, &name, &finalParticipation, &student, &memberName); err != nil {
			return err
		}
		if current == nil || current.TeamId != teamID {
			current = &pbaudience.ListTeamsResponse_TeamListItem{
				TeamId:             teamID,
				Name:               name,
				FinalParticipation: finalParticipation,
				IsStudent:          student,
			}
			items = append(items, current)
		}
		if memberName != nil {
			current.MemberNames = append(current.MemberNames, *memberName)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbaudience.ListTeamsResponse{Teams: items})
	return nil
}
