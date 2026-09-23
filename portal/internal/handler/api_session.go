package handler

import (
	"net/http"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	pbcommon "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/common"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// APISession: GET /api/session（Api::SessionsController#show 相当）
// チーム・選手・競技時刻・インスタンス・VAPID 公開鍵を一括で返す。
func (h *Handler) APISession(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()

	res := &pbcommon.GetCurrentSessionResponse{
		ContestantInstances: []*pbresources.ContestantInstance{},
		PushVapidKey:        h.push.PublicKeyForPushHeader(),
	}

	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}

	teamID := int64(0)
	if team != nil {
		teamID = team.ID
		res.Team = team.ToPB(false, nil, false)
	}
	if c != nil {
		pb, err := c.ToPBWithSSHKeys(ctx, h.db)
		if err != nil {
			return err
		}
		res.Contestant = pb
		res.DiscordServerId = h.cfg.Discord.ServerID
	}

	contestPB, err := contest.ContestPBForTeam(ctx, h.db, ri.timing, ri.now, teamID)
	if err != nil {
		return err
	}
	res.Contest = contestPB

	if team != nil {
		open, err := contest.ContestOpenForTeam(ctx, h.db, ri.timing, teamID, ri.now)
		if err != nil {
			return err
		}
		if open {
			instances, err := model.TeamInstances(ctx, h.db, team.ID)
			if err != nil {
				return err
			}
			for _, ci := range instances {
				res.ContestantInstances = append(res.ContestantInstances, ci.ToPB(nil))
			}
		}
	}

	if err := h.saveSession(w, r); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, res)
	return nil
}
