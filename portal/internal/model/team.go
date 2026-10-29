package model

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"

	pbresources "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/resources"
)

type Team struct {
	ID                    int64
	Name                  string
	LeaderID              sql.NullInt64
	IsHidden              bool
	FinalParticipation    bool
	EmailAddress          string
	InviteToken           string
	Withdrawn             bool
	Disqualified          bool
	OnsiteSupportExcluded bool
	Student               bool
	AvailabilityZone      sql.NullString
}

const teamColumns = `teams.id, teams.name, teams.leader_id, teams.is_hidden, teams.final_participation, teams.email_address, teams.invite_token, teams.withdrawn, teams.disqualified, teams.onsite_support_excluded, teams.student, teams.availability_zone`

func scanTeam(row interface{ Scan(...any) error }) (*Team, error) {
	t := &Team{}
	err := row.Scan(&t.ID, &t.Name, &t.LeaderID, &t.IsHidden, &t.FinalParticipation, &t.EmailAddress, &t.InviteToken, &t.Withdrawn, &t.Disqualified, &t.OnsiteSupportExcluded, &t.Student, &t.AvailabilityZone)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Team) Active() bool { return !t.Withdrawn && !t.Disqualified }

// Joinable はチーム上限 3 人（DM-6 のとおりハードコード）。
func (t *Team) Joinable(ctx context.Context, db DBTX) (bool, error) {
	n, err := countMembers(ctx, db, t.ID)
	return n < 3, err
}

func countMembers(ctx context.Context, db DBTX, teamID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contestants WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

func FindTeam(ctx context.Context, db DBTX, id int64) (*Team, error) {
	return scanTeam(db.QueryRowContext(ctx, `SELECT `+teamColumns+` FROM teams WHERE id = $1`, id))
}

// FindTeamByInviteToken は招待 URL の解決。辞退・失格したチームは招待を受け付けない
// （isucon14 は withdrawn を見ておらず、辞退後のチームに参加してしまう経路があった）。
func FindTeamByInviteToken(ctx context.Context, db DBTX, id int64, inviteToken string) (*Team, error) {
	return scanTeam(db.QueryRowContext(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE id = $1 AND invite_token = $2 AND withdrawn = false AND disqualified = false`,
		id, inviteToken))
}

// ActiveVisibleTeamCount は Contest.team_count 相当（Team.active.visible.count）。
func ActiveVisibleTeamCount(ctx context.Context, db DBTX) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE withdrawn = false AND disqualified = false AND is_hidden = false`).Scan(&n)
	return n, err
}

// ToPB は Team#to_pb 相当。members / leader は呼び出し側で読み込んで渡す。
func (t *Team) ToPB(detail bool, members []*Contestant, memberDetail bool) *pbresources.Team {
	pb := &pbresources.Team{
		Id:                    t.ID,
		Name:                  t.Name,
		LeaderId:              t.LeaderID.Int64,
		MemberIds:             []int64{},
		FinalParticipation:    t.FinalParticipation,
		Hidden:                t.IsHidden,
		Withdrawn:             t.Withdrawn,
		Disqualified:          t.Disqualified,
		OnsiteSupportExcluded: t.OnsiteSupportExcluded,
		Student:               &pbresources.Team_StudentStatus{Status: t.Student},
	}
	if detail {
		pb.Detail = &pbresources.Team_TeamDetail{
			EmailAddress: t.EmailAddress,
			InviteToken:  t.InviteToken,
		}
	}
	if members != nil {
		for _, m := range members {
			pb.MemberIds = append(pb.MemberIds, m.ID)
			pb.Members = append(pb.Members, m.ToPB(memberDetail))
			if t.LeaderID.Valid && m.ID == t.LeaderID.Int64 {
				pb.Leader = m.ToPB(memberDetail)
			}
		}
		// Rails の has_one :leader は leader_id を見ず先頭のメンバーを返すため、
		// leader_id が未設定でも先頭メンバーで埋める（挙動互換）。
		if pb.Leader == nil && len(members) > 0 {
			pb.Leader = members[0].ToPB(memberDetail)
		}
	}
	return pb
}

// TeamToPBWithMembers はメンバーを読み込んで to_pb(members: true) 相当を返す。
func TeamToPBWithMembers(ctx context.Context, db DBTX, t *Team, detail, memberDetail bool) (*pbresources.Team, error) {
	members, err := TeamMembers(ctx, db, t.ID)
	if err != nil {
		return nil, err
	}
	if memberDetail {
		if err := fillSSHKeyRegistered(ctx, db, members); err != nil {
			return nil, err
		}
	}
	return t.ToPB(detail, members, memberDetail), nil
}

// UpdateStudentStatus は teams.student の導出（DM-5: メンバー 1 人以上かつ全員学生）。
func UpdateTeamStudentStatus(ctx context.Context, db DBTX, teamID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE teams SET student = (
			SELECT COUNT(*) > 0 AND COUNT(*) = COUNT(*) FILTER (WHERE student) FROM contestants WHERE team_id = teams.id
		), updated_at = now() WHERE id = $1`, teamID)
	return err
}

// NewInviteToken は SecureRandom.urlsafe_base64(64) 相当（DM-6: 86 文字）。
func NewInviteToken() string {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
