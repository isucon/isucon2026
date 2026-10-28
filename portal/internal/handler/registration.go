package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"unicode/utf8"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
	pbcommon "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/common"
	pbregistration "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/services/registration"
	"github.com/isucon/isucon2026/portal/internal/cloudformation"
	"github.com/isucon/isucon2026/portal/internal/contest"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
)

// registrationOpen / maxTeamsReached は Contest.registration_open? / max_teams_reached? 相当。
func (h *Handler) maxTeamsReached(r *http.Request) (bool, error) {
	n, err := model.ActiveVisibleTeamCount(r.Context(), h.db)
	if err != nil {
		return false, err
	}
	return n >= h.cfg.MaxTeams, nil
}

// RegistrationSessionShow: GET /api/registration/session（FR-REG-1: 7 状態判定）
func (h *Handler) RegistrationSessionShow(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	q := r.URL.Query()

	me, err := h.currentContestantIncludeDisqualified(r)
	if err != nil {
		return err
	}
	meDisqualified := me != nil && h.teamDisqualified(ctx, me)

	var team *model.Team
	switch {
	case me != nil && !meDisqualified:
		team, err = model.FindTeam(ctx, h.db, me.TeamID)
		if err != nil {
			return err
		}
	case q.Get("team_id") != "" && q.Get("invite_token") != "":
		teamID, _ := parseInt64(q.Get("team_id"))
		team, err = model.FindTeamByInviteToken(ctx, h.db, teamID, q.Get("invite_token"))
		if err != nil {
			return err
		}
		if team == nil {
			renderErrorFull(w, http.StatusNotFound, "ActiveRecord::RecordNotFound", "Record not found", []string{"招待URLが無効です"})
			return nil
		}
	}

	joinable := false
	if team != nil {
		joinable, err = team.Joinable(ctx, h.db)
		if err != nil {
			return err
		}
	}
	maxReached, err := h.maxTeamsReached(r)
	if err != nil {
		return err
	}

	var status pbregistration.GetRegistrationSessionResponse_Status
	switch {
	case meDisqualified:
		status = pbregistration.GetRegistrationSessionResponse_DISQUALIFIED
	case me != nil:
		status = pbregistration.GetRegistrationSessionResponse_JOINED
	case team != nil && contest.RegistrationInvitationClosed(ri.timing, ri.now) && !h.currentBypassAllowed(r, "JOIN_TEAM"):
		status = pbregistration.GetRegistrationSessionResponse_CLOSED
	case team != nil && !joinable:
		status = pbregistration.GetRegistrationSessionResponse_NOT_JOINABLE
	case team == nil && (!contest.RegistrationOpen(ri.timing, ri.now) || maxReached) && !h.currentBypassAllowed(r, "CREATE_TEAM"):
		status = pbregistration.GetRegistrationSessionResponse_CLOSED
	case ri.sess.GitHubLogin == nil || ri.sess.DiscordLogin == nil:
		status = pbregistration.GetRegistrationSessionResponse_NOT_LOGGED_IN
	case team != nil:
		status = pbregistration.GetRegistrationSessionResponse_JOINABLE
	default:
		status = pbregistration.GetRegistrationSessionResponse_CREATABLE
	}

	res := &pbregistration.GetRegistrationSessionResponse{Status: status}
	if ri.sess.GitHubLogin != nil {
		res.GithubLogin = ri.sess.GitHubLogin.Login
		res.GithubAvatarUrl = ri.sess.GitHubLogin.AvatarURL
	}
	if ri.sess.DiscordLogin != nil {
		res.DiscordTag = ri.sess.DiscordLogin.Tag
		res.DiscordAvatarUrl = ri.sess.DiscordLogin.AvatarURL
	}
	if team != nil {
		detail := me != nil && team.LeaderID.Valid && me.ID == team.LeaderID.Int64
		pb, err := model.TeamToPBWithMembers(ctx, h.db, team, detail, true)
		if err != nil {
			return err
		}
		res.Team = pb
		res.MemberInviteUrl = h.baseURL(r) + "/registration?" + url.Values{
			"team_id": {fmt.Sprint(team.ID)}, "invite_token": {team.InviteToken},
		}.Encode()

		coupon, err := model.FindCouponByTeam(ctx, h.db, team.ID)
		if err != nil {
			return err
		}
		if coupon != nil {
			res.Coupon = coupon.ToPB()
		}
	}
	if status == pbregistration.GetRegistrationSessionResponse_JOINED {
		res.DiscordServerId = h.cfg.Discord.ServerID
	}
	res.EnvCheckStatus = pbresources.EnvCheckStatus_PREPARING
	if team != nil && h.cfg.TestAMIID != "" {
		status, err := model.EnvCheckStatus(ctx, h.db, team.ID)
		if err != nil {
			return err
		}
		res.EnvCheckStatus = status
	}

	if err := h.saveSession(w, r); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, res)
	return nil
}

// RegistrationCreateTeam: POST /api/registration/team（FR-REG-2/3）
func (h *Handler) RegistrationCreateTeam(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()

	me, err := h.currentContestantIncludeDisqualified(r)
	if err != nil {
		return err
	}
	if me != nil && h.teamDisqualified(ctx, me) {
		return errNotFound("disqualified")
	}
	if me != nil {
		return errNotFound("already signed up")
	}
	if ri.sess.GitHubLogin == nil || ri.sess.DiscordLogin == nil {
		return errNotFound("logins are missing")
	}

	req := &pbregistration.CreateTeamRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	maxReached, err := h.maxTeamsReached(r)
	if err != nil {
		return err
	}
	if (!contest.RegistrationOpen(ri.timing, ri.now) || maxReached) && !h.currentBypassAllowed(r, "CREATE_TEAM") {
		return errRegistrationClosed()
	}

	if err := validateTeamInput(ctx, tx, req.TeamName, req.EmailAddress, 0); err != nil {
		return err
	}
	if err := validateContestantInput(ctx, tx, req.Name, ri.sess.GitHubLogin.ID, ri.sess.DiscordLogin.ID); err != nil {
		return err
	}

	isHidden := h.currentBypassAllowed(r, "HIDDEN_TEAM")
	var teamID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO teams (name, email_address, invite_token, is_hidden, final_participation, student, created_at, updated_at)
		VALUES ($1, $2, $3, $4, true, false, now(), now()) RETURNING id`,
		req.TeamName, req.EmailAddress, model.NewInviteToken(), isHidden).Scan(&teamID); err != nil {
		return err
	}

	// AZ は id の剰余で割り当てる（FR-REG-3）
	zones := h.cfg.AvailabilityZones
	zone := zones[teamID%int64(len(zones))]
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET availability_zone = $1 WHERE id = $2`, zone, teamID); err != nil {
		return err
	}

	contestantID, err := insertContestant(ctx, tx, teamID, req.Name, req.IsStudent, req.IsInPerson, req.AvatarUrl, ri.sess.GitHubLogin.ID, ri.sess.GitHubLogin.Login, ri.sess.DiscordLogin.ID, ri.sess.DiscordLogin.Tag)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET leader_id = $1 WHERE id = $2`, contestantID, teamID); err != nil {
		return err
	}
	if err := saveRecordInput(ctx, tx, contestantID, req.RegistrationRecord, ri.now); err != nil {
		return err
	}
	if err := model.UpdateTeamStudentStatus(ctx, tx, teamID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	teamRow, err := model.FindTeam(ctx, h.db, teamID)
	if err != nil {
		return err
	}
	h.jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: fmt.Sprintf(":raising_hand: *New team:* %s (%d) leader=%s (%d)", teamRow.Name, teamID, req.Name, contestantID)})
	h.jobs.Enqueue(ctx, "sync_ssh_keys_of_contestant", job.SyncSSHKeysArgs{ContestantID: contestantID, AccessToken: ri.sess.GitHubLogin.Token})

	ri.sess.ContestantID = contestantID
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbregistration.CreateTeamResponse{TeamId: teamID})
	return nil
}

// RegistrationJoinTeam: POST /api/registration/contestant（FR-REG-4: 招待 URL 参加、上限 3 人）
func (h *Handler) RegistrationJoinTeam(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()

	me, err := h.currentContestantIncludeDisqualified(r)
	if err != nil {
		return err
	}
	if me != nil && h.teamDisqualified(ctx, me) {
		return errNotFound("disqualified")
	}
	if me != nil {
		return errNotFound("already signed up")
	}
	if ri.sess.GitHubLogin == nil || ri.sess.DiscordLogin == nil {
		return &model.InvalidError{Messages: []string{"logins are missing"}}
	}
	if contest.RegistrationInvitationClosed(ri.timing, ri.now) && !h.currentBypassAllowed(r, "JOIN_TEAM") {
		return errRegistrationClosed()
	}

	req := &pbregistration.JoinTeamRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	team, err := model.FindTeamByInviteToken(ctx, h.db, req.TeamId, req.InviteToken)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// チームを行ロックして上限 3 人を保証する
	var memberCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM contestants WHERE team_id = (SELECT id FROM teams WHERE id = $1 FOR UPDATE)`, team.ID).Scan(&memberCount); err != nil {
		return err
	}
	if memberCount >= 3 {
		return &model.InvalidError{Messages: []string{"チームメンバーの上限に達しています"}}
	}
	if err := validateContestantInput(ctx, tx, req.Name, ri.sess.GitHubLogin.ID, ri.sess.DiscordLogin.ID); err != nil {
		return err
	}
	contestantID, err := insertContestant(ctx, tx, team.ID, req.Name, req.IsStudent, req.IsInPerson, req.AvatarUrl, ri.sess.GitHubLogin.ID, ri.sess.GitHubLogin.Login, ri.sess.DiscordLogin.ID, ri.sess.DiscordLogin.Tag)
	if err != nil {
		return err
	}
	if err := saveRecordInput(ctx, tx, contestantID, req.RegistrationRecord, ri.now); err != nil {
		return err
	}
	if err := model.UpdateTeamStudentStatus(ctx, tx, team.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	h.jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: fmt.Sprintf(":raised_hands: *New contestant:* %s (%d) member=%s (%d)", team.Name, team.ID, req.Name, contestantID)})
	h.jobs.Enqueue(ctx, "sync_ssh_keys_of_contestant", job.SyncSSHKeysArgs{ContestantID: contestantID, AccessToken: ri.sess.GitHubLogin.Token})

	ri.sess.ContestantID = contestantID
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbregistration.JoinTeamResponse{})
	return nil
}

// RegistrationUpdate: PUT /api/registration（FR-REG-5: 開始後と更新締切後は 403、チーム名・メールは代表者のみ）
func (h *Handler) RegistrationUpdate(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	if c == nil {
		return &model.NotFoundError{}
	}
	if contest.ContestStarted(ri.timing, ri.now) {
		return errForbidden("You cannot update registration details after start")
	}
	if contest.RegistrationUpdateClosed(ri.timing, ri.now) {
		return errForbidden("You cannot update at this moment")
	}

	req := &pbregistration.UpdateRegistrationRequest{}
	if err := decodePB(r, req); err != nil {
		return err
	}
	if err := validateNameLength(req.Name, "選手名"); err != nil {
		return err
	}
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE contestants SET name = $1, student = $2, in_person = $3, avatar_url = $4, updated_at = now() WHERE id = $5`,
		req.Name, req.IsStudent, req.IsInPerson, req.AvatarUrl, c.ID); err != nil {
		return err
	}
	if team.LeaderID.Valid && team.LeaderID.Int64 == c.ID {
		if err := validateTeamInput(ctx, tx, req.TeamName, req.EmailAddress, team.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE teams SET name = $1, email_address = $2, updated_at = now() WHERE id = $3`,
			req.TeamName, req.EmailAddress, team.ID); err != nil {
			return err
		}
	}
	if err := saveRecordInput(ctx, tx, c.ID, req.RegistrationRecord, ri.now); err != nil {
		return err
	}
	if err := model.UpdateTeamStudentStatus(ctx, tx, team.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if team.LeaderID.Valid && team.LeaderID.Int64 == c.ID && team.Name != req.TeamName {
		// Keep visibility-specific scores intact if the job runs after the freeze time.
		h.jobs.Enqueue(ctx, "update_contestant_dashboard", job.UpdateDashboardArgs{TeamID: team.ID, Frozen: true})
	}
	renderPB(w, http.StatusOK, &pbregistration.UpdateRegistrationResponse{})
	return nil
}

// RegistrationDelete: DELETE /api/registration（FR-REG-6: 辞退）
func (h *Handler) RegistrationDelete(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	if c == nil {
		return &model.NotFoundError{}
	}
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	wasLeader := team.LeaderID.Valid && team.LeaderID.Int64 == c.ID

	if wasLeader || !h.currentBypassAllowed(r, "LEAVE_TEAM") {
		if contest.ContestStarted(ri.timing, ri.now) {
			return errForbidden("You cannot withdraw after start")
		}
		if contest.RegistrationUpdateClosed(ri.timing, ri.now) {
			return errForbidden("You cannot withdraw at this moment")
		}
	}

	var removed []*model.Contestant
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if wasLeader {
		if _, err := tx.ExecContext(ctx, `UPDATE teams SET withdrawn = true, leader_id = NULL, updated_at = now() WHERE id = $1`, team.ID); err != nil {
			return err
		}
		removed, err = model.TeamMembers(ctx, tx, team.ID)
		if err != nil {
			return err
		}
	} else {
		removed = []*model.Contestant{c}
	}
	for _, m := range removed {
		for _, q := range []string{
			`DELETE FROM ssh_public_keys WHERE contestant_id = $1`,
			`DELETE FROM notifications WHERE contestant_id = $1`,
			`DELETE FROM push_subscriptions WHERE contestant_id = $1`,
			`DELETE FROM contestants WHERE id = $1`,
		} {
			if _, err := tx.ExecContext(ctx, q, m.ID); err != nil {
				return err
			}
		}
	}
	if !wasLeader {
		if err := model.UpdateTeamStudentStatus(ctx, tx, team.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	for _, m := range removed {
		h.jobs.Enqueue(ctx, "maintain_discord_contestant_roles", job.MaintainRolesArgs{ForceDiscordID: m.DiscordID})
	}
	if wasLeader {
		names, ids := joinContestants(removed)
		h.jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: fmt.Sprintf(":wave: *Team withdrawal:* %s (%d), members=%s (%s)", team.Name, team.ID, names, ids)})
	} else {
		h.jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: fmt.Sprintf(":wave: *Contestant withdrawal:* %s (%d), member=%s (%d)", team.Name, team.ID, c.Name, c.ID)})
	}

	ri.sess.ContestantID = 0
	ri.sess.MarkDirty()
	if err := h.saveSession(w, r); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbregistration.DeleteRegistrationResponse{})
	return nil
}

// RegistrationEnvCheck: GET /api/registration/env_check
func (h *Handler) RegistrationEnvCheck(w http.ResponseWriter, r *http.Request) error {
	ri := info(r)
	ctx := r.Context()
	c, err := h.currentContestant(r)
	if err != nil {
		return err
	}
	if c == nil {
		return &model.NotFoundError{}
	}
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}

	if h.cfg.TestAMIID == "" {
		renderPB(w, http.StatusOK, &pbregistration.GetEnvCheckInformationResponse{Status: pbresources.EnvCheckStatus_PREPARING})
		return nil
	}
	template, err := cloudFormationTestTemplate(h, team, ri)
	if err != nil {
		return errNotFound(err.Error())
	}
	progress, err := model.GetEnvCheckProgress(ctx, h.db, team.ID)
	if err != nil {
		return err
	}
	res := &pbregistration.GetEnvCheckInformationResponse{Template: template, Status: progress.Status, InstanceIps: progress.InstanceIPs}
	if len(progress.InstanceIPs) > 0 {
		res.InstanceIp = progress.InstanceIPs[0]
	}

	renderPB(w, http.StatusOK, res)
	return nil
}

// RegistrationActivateCoupon: PUT /api/registration/coupon（FR-REG-8）
func (h *Handler) RegistrationActivateCoupon(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	team, err := h.currentTeam(r)
	if err != nil {
		return err
	}
	if team == nil {
		return &model.NotFoundError{}
	}
	coupon, err := model.FindCouponByTeam(ctx, h.db, team.ID)
	if err != nil {
		return err
	}
	if coupon == nil {
		return &model.NotFoundError{}
	}
	if _, err := h.db.ExecContext(ctx, `UPDATE coupons SET activate = true, updated_at = now() WHERE id = $1`, coupon.ID); err != nil {
		return err
	}
	renderPB(w, http.StatusOK, &pbregistration.ActivateCouponResponse{})
	return nil
}

// RegistrationAvatarURL: GET /api/registration/avatar/url（FR-REG-7: presigned PUT 600 秒）
func (h *Handler) RegistrationAvatarURL(w http.ResponseWriter, r *http.Request) error {
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	keyStr := base64.RawURLEncoding.EncodeToString(key)
	objectKey := h.cfg.AvatarBasePath + keyStr

	if h.aws == nil || h.cfg.AvatarBucketName == "" {
		return errNotFound("avatar storage is not configured")
	}
	uploadURL, err := h.aws.PresignAvatarUpload(r.Context(), h.cfg.AvatarBucketName, objectKey)
	if err != nil {
		return err
	}
	downloadURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", h.cfg.AvatarBucketName, h.cfg.AWSRegion, objectKey)
	renderPB(w, http.StatusOK, &pbcommon.GetAvatarUrlResponse{
		UploadPresigned: uploadURL,
		Url:             downloadURL,
	})
	return nil
}

// --- 内部ヘルパ ---

func validateNameLength(name, label string) error {
	if utf8.RuneCountInString(name) > 255 {
		return &model.InvalidError{Messages: []string{label + "は255文字以内で入力してください"}}
	}
	return nil
}

func validateTeamInput(ctx context.Context, tx model.DBTX, name, email string, selfID int64) error {
	if err := validateNameLength(name, "チーム名"); err != nil {
		return err
	}
	var msgs []string
	if name == "" {
		msgs = append(msgs, "Name can't be blank")
	}
	if email == "" {
		msgs = append(msgs, "Email address can't be blank")
	}
	if name != "" {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE name = $1 AND id != $2`, name, selfID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			msgs = append(msgs, "Name has already been taken")
		}
	}
	if len(msgs) > 0 {
		return &model.InvalidError{Messages: msgs}
	}
	return nil
}

func validateContestantInput(ctx context.Context, tx model.DBTX, name, githubID, discordID string) error {
	if err := validateNameLength(name, "選手名"); err != nil {
		return err
	}
	var msgs []string
	if name == "" {
		msgs = append(msgs, "Name can't be blank")
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM contestants WHERE github_id = $1`, githubID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		msgs = append(msgs, "Github has already been taken")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM contestants WHERE discord_id = $1`, discordID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		msgs = append(msgs, "Discord has already been taken")
	}
	if len(msgs) > 0 {
		return &model.InvalidError{Messages: msgs}
	}
	return nil
}

func insertContestant(ctx context.Context, tx model.DBTX, teamID int64, name string, isStudent, isInPerson bool, avatarURL, githubID, githubLogin, discordID, discordTag string) (int64, error) {
	if avatarURL == "" {
		avatarURL = "https://avatars.githubusercontent.com/u/0"
	}
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO contestants (team_id, name, github_login, github_id, discord_id, discord_tag, avatar_url, student, in_person, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now()) RETURNING id`,
		teamID, name, githubLogin, githubID, discordID, discordTag, avatarURL, isStudent, isInPerson).Scan(&id)
	return id, err
}

func cloudFormationTestTemplate(h *Handler, team *model.Team, ri *reqInfo) (string, error) {
	return cloudformation.TestTemplate(h.cfg, team.ID, team.AvailabilityZone.String, ri.now)
}

func joinContestants(cs []*model.Contestant) (names, ids string) {
	for i, c := range cs {
		if i > 0 {
			names += ","
			ids += ","
		}
		names += c.Name
		ids += fmt.Sprint(c.ID)
	}
	return names, ids
}

var _ = sql.ErrNoRows
