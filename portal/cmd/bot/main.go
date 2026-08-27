// cmd/bot は Discord bot（bot タスク相当）。
// isucon14-portal の bin/isuxportal-discord-bot の移植:
// メンバー参加時のロール同期、#readme のリアクション対応（🪑 / 🆘）、
// .ping / .supportOpen / .support / .supportClose コマンド。
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	pbbot "github.com/isucon/isucon2026/portal/gen/pb/isuxportal/misc/bot"
	"github.com/isucon/isucon2026/portal/internal/app"
	"github.com/isucon/isucon2026/portal/internal/job"
	"github.com/isucon/isucon2026/portal/internal/model"
)

const (
	chairEmoji = "\U0001FA91" // 🪑
	sosEmoji   = "\U0001F198" // 🆘
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer a.DB.Close()

	if a.Cfg.Discord.BotToken == "" {
		return errors.New("ISUXPORTAL_DISCORD_BOT_TOKEN is required for bot")
	}

	b := &bot{app: a, redis: a.Store.Redis()}
	dg, err := discordgo.New("Bot " + a.Cfg.Discord.BotToken)
	if err != nil {
		return err
	}
	dg.Identify.Intents = discordgo.IntentsGuildMembers | discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions | discordgo.IntentsMessageContent
	dg.AddHandler(b.onMemberJoin)
	dg.AddHandler(b.onReactionAdd)
	dg.AddHandler(b.onMessage)

	if err := dg.Open(); err != nil {
		return err
	}
	defer dg.Close()

	slog.Info("bot started", "server_id", a.Cfg.Discord.ServerID)
	<-ctx.Done()
	slog.Info("bot shutting down")
	return nil
}

type bot struct {
	app   *app.App
	redis *redis.Client
}

func (b *bot) expectedServer(guildID string) bool {
	return guildID == b.app.Cfg.Discord.ServerID
}

// onMemberJoin はギルド参加時にロールと参加状態を同期する。
func (b *bot) onMemberJoin(_ *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if !b.expectedServer(e.GuildID) {
		return
	}
	ctx := context.Background()
	c, err := model.FindContestantIncludeDisqualifiedBy(ctx, b.app.DB, "discord_id", e.User.ID)
	if err != nil || c == nil {
		return
	}
	slog.Info("GUILD_MEMBER_ADD", "contestant", c.ID)
	b.app.Jobs.Enqueue(ctx, "maintain_discord_contestant_roles", job.MaintainRolesArgs{ContestantID: c.ID})
	b.app.Jobs.Enqueue(ctx, "sync_discord_member_state_of_contestant", job.ContestantArgs{ContestantID: c.ID})
}

// onReactionAdd は #readme の 🪑（ロール付与）と 🆘（Slack 通知）に反応する。
func (b *bot) onReactionAdd(s *discordgo.Session, e *discordgo.MessageReactionAdd) {
	if !b.expectedServer(e.GuildID) {
		return
	}
	ch, err := s.Channel(e.ChannelID)
	if err != nil || ch.Name != "readme" {
		return
	}
	name := e.Emoji.Name
	if name != chairEmoji && name != sosEmoji {
		return
	}
	ctx := context.Background()
	c, _ := model.FindContestantIncludeDisqualifiedBy(ctx, b.app.DB, "discord_id", e.UserID)

	if name == chairEmoji {
		if c != nil {
			b.app.Jobs.Enqueue(ctx, "maintain_discord_contestant_roles", job.MaintainRolesArgs{ContestantID: c.ID})
			b.app.Jobs.Enqueue(ctx, "sync_discord_member_state_of_contestant", job.ContestantArgs{ContestantID: c.ID})
		}
		_ = s.MessageReactionRemove(e.ChannelID, e.MessageID, chairEmoji, e.UserID)
	} else {
		info := "_unknown_"
		if c != nil {
			info = fmt.Sprintf("%s (%d)", c.Name, c.ID)
		}
		b.app.Jobs.Enqueue(ctx, "slack_webhook", job.TextArgs{Text: ":sos: *Discord SOS reaction:* contestant=" + info})
	}
	_ = s.MessageReactionAdd(e.ChannelID, e.MessageID, name)
}

// onMessage は `.` プレフィックスのコマンドを処理する。
func (b *bot) onMessage(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e.Author == nil || e.Author.Bot || !b.expectedServer(e.GuildID) {
		return
	}
	fields := strings.Fields(e.Content)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], ".") {
		return
	}
	reply := func(msg string) {
		_, _ = s.ChannelMessageSend(e.ChannelID, msg)
	}
	switch fields[0] {
	case ".ping":
		reply("Pong, " + e.Author.Username)
	case ".supportOpen":
		if len(fields) != 2 {
			reply("**USAGE** .supportOpen team_id")
			return
		}
		b.supportOpen(s, e, fields[1], reply)
	case ".support":
		b.supportStatus(s, e, reply)
	case ".supportClose":
		b.supportClose(s, e, reply)
	}
}

func (b *bot) isAdmin(s *discordgo.Session, e *discordgo.MessageCreate) bool {
	member := e.Member
	if member == nil {
		var err error
		member, err = s.GuildMember(e.GuildID, e.Author.ID)
		if err != nil {
			return false
		}
	}
	for _, role := range member.Roles {
		if role == b.app.Cfg.Discord.AdminRoleID {
			return true
		}
	}
	return false
}

func (b *bot) supportRole(channelID string) string {
	return b.app.Cfg.Discord.SupportCommChannelRoles[channelID]
}

func (b *bot) supportInfoKey(channelID string) string {
	return "isuxportal:support_comm_info:" + channelID
}

func (b *bot) getSupportInfo(ctx context.Context, channelID string) (*pbbot.SupportCommInfo, error) {
	data, err := b.redis.Get(ctx, b.supportInfoKey(channelID)).Result()
	if errors.Is(err, redis.Nil) {
		return &pbbot.SupportCommInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	info := &pbbot.SupportCommInfo{}
	if err := proto.Unmarshal(raw, info); err != nil {
		return nil, err
	}
	return info, nil
}

func (b *bot) setSupportInfo(ctx context.Context, channelID string, info *pbbot.SupportCommInfo) error {
	raw, err := proto.Marshal(info)
	if err != nil {
		return err
	}
	return b.redis.Set(ctx, b.supportInfoKey(channelID), base64.StdEncoding.EncodeToString(raw), 0).Err()
}

// supportOpen は選手チームへのサポートチャンネル開設。
// clarification を作り、チームメンバーにチャンネルのロールを付与する。
func (b *bot) supportOpen(s *discordgo.Session, e *discordgo.MessageCreate, teamIDStr string, reply func(string)) {
	if !b.isAdmin(s, e) {
		reply("**FORBIDDEN** You're not an admin :lock:")
		return
	}
	roleID := b.supportRole(e.ChannelID)
	if roleID == "" {
		reply("**Error** This channel is not designated for support comm.")
		return
	}
	ctx := context.Background()
	info, err := b.getSupportInfo(ctx, e.ChannelID)
	if err != nil {
		reply("something went wrong")
		return
	}
	if info.Usage != nil {
		reply("**Error** This channel is currently in use :closed_lock_with_key:")
		return
	}
	var teamID int64
	fmt.Sscan(teamIDStr, &teamID)
	team, err := model.FindTeam(ctx, b.app.DB, teamID)
	if err != nil || team == nil {
		reply(fmt.Sprintf("**Error** team ID #%s does not exist.", teamIDStr))
		return
	}

	question := "Discord Support Channel Log\nLog will be posted after channel has closed"
	answer := "Support channel has been opened. Check discord for the details\nサポートチャンネルが Discord 上に作成されました。Discordを確認してください。"
	res, err := b.app.DB.ExecContext(ctx, `
		INSERT INTO clarifications (team_id, disclosed, original_question, question, answer, answered_at, admin, created_at, updated_at)
		VALUES (?, 0, ?, ?, ?, ?, 1, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`,
		team.ID, question, question, answer, time.Now().UTC())
	if err != nil {
		reply("something went wrong")
		return
	}
	clarID, _ := res.LastInsertId()

	info.Usage = &pbbot.SupportCommInfo_Usage{
		MessageIdSince:  parseUint(e.ID),
		TeamId:          team.ID,
		ClarificationId: clarID,
	}

	members, err := model.TeamMembers(ctx, b.app.DB, team.ID)
	if err != nil {
		reply("something went wrong")
		return
	}
	var warnings []string
	for _, m := range members {
		if err := s.GuildMemberRoleAdd(e.GuildID, m.DiscordID, roleID); err != nil {
			warnings = append(warnings, fmt.Sprintf("- warning: %s (#%d) was not found", m.Name, m.ID))
		}
	}
	if err := b.setSupportInfo(ctx, e.ChannelID, info); err != nil {
		reply("something went wrong")
		return
	}
	msg := fmt.Sprintf(":telephone: **Support comm opened** for %s (#%d)\nhttps://%s/contestant/clarifications", team.Name, team.ID, b.app.Cfg.PublicURLHost)
	if len(warnings) > 0 {
		msg += "\n" + strings.Join(warnings, "\n")
	}
	reply(msg)
}

func (b *bot) supportStatus(s *discordgo.Session, e *discordgo.MessageCreate, reply func(string)) {
	if !b.isAdmin(s, e) {
		reply("**FORBIDDEN** You're not an admin :lock:")
		return
	}
	if b.supportRole(e.ChannelID) == "" {
		reply("**Error** This channel is not designated for support comm.")
		return
	}
	info, err := b.getSupportInfo(context.Background(), e.ChannelID)
	if err != nil {
		reply("something went wrong")
		return
	}
	reply(fmt.Sprintf("**USAGE**\n.supportOpen team_id\n.supportClose\n\n**STATUS**\n```\n%v```", info))
}

// supportClose はサポートチャンネルのログを clarification に書き戻してロールを剥奪する。
func (b *bot) supportClose(s *discordgo.Session, e *discordgo.MessageCreate, reply func(string)) {
	if !b.isAdmin(s, e) {
		reply("**FORBIDDEN** You're not an admin :lock:")
		return
	}
	roleID := b.supportRole(e.ChannelID)
	if roleID == "" {
		reply("**Error** This channel is not designated for support comm.")
		return
	}
	ctx := context.Background()
	info, err := b.getSupportInfo(ctx, e.ChannelID)
	if err != nil || info.Usage == nil {
		reply("**Error** This channel is currently not in use :unlock:")
		return
	}
	team, err := model.FindTeam(ctx, b.app.DB, info.Usage.TeamId)
	if err != nil || team == nil {
		reply(fmt.Sprintf("**Error** team ID #%d does not exist.", info.Usage.TeamId))
		return
	}

	log, err := b.channelLogSince(s, e.ChannelID, fmt.Sprint(info.Usage.MessageIdSince))
	if err != nil {
		reply("something went wrong")
		return
	}
	if _, err := b.app.DB.ExecContext(ctx, `
		UPDATE clarifications SET original_question = 'Discord Support Channel Log', question = 'Discord Support Channel Log',
			answer = ?, answered_at = ?, updated_at = UTC_TIMESTAMP(6) WHERE id = ?`,
		log, time.Now().UTC(), info.Usage.ClarificationId); err != nil {
		reply("something went wrong")
		return
	}

	members, _ := model.TeamMembers(ctx, b.app.DB, team.ID)
	for _, m := range members {
		_ = s.GuildMemberRoleRemove(e.GuildID, m.DiscordID, roleID)
	}
	info.Usage = nil
	if err := b.setSupportInfo(ctx, e.ChannelID, info); err != nil {
		reply("something went wrong")
		return
	}
	reply(":construction: **Support comm closed**\n*(if you have manually added people to the role, please remove them manually)*")
}

func (b *bot) channelLogSince(s *discordgo.Session, channelID, afterID string) (string, error) {
	var lines []string
	after := afterID
	for {
		msgs, err := s.ChannelMessages(channelID, 100, "", after, "")
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			break
		}
		// API は新しい順で返すので古い順に直す
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			lines = append(lines, fmt.Sprintf("%s: [%s]\n%s", m.Author.Username, m.Timestamp.Format(time.RFC3339), m.Content))
		}
		after = msgs[0].ID
	}
	return strings.Join(lines, "\n\n"), nil
}

func parseUint(s string) uint64 {
	var v uint64
	fmt.Sscan(s, &v)
	return v
}
