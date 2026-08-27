package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// Discord は Bot トークンによる REST API クライアント（Discordrb::API 相当）。
type Discord struct {
	BotToken   string
	APIBaseURL string
	ServerID   string
}

var ErrDiscordNotFound = errors.New("discord: not found")

func (d *Discord) enabled() bool { return d.BotToken != "" }

func (d *Discord) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, d.APIBaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+d.BotToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrDiscordNotFound
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("discord: %s %s -> %d: %s", method, path, res.StatusCode, data)
	}
	return data, nil
}

// CreateMessage は DiscordAnnounceJob が使う（アナウンスチャンネルへの投稿）。
func (d *Discord) CreateMessage(ctx context.Context, channelID, content string) error {
	if !d.enabled() {
		slog.Info("discord create_message (skipped: no bot token)", "channel_id", channelID, "content", content)
		return nil
	}
	_, err := d.do(ctx, http.MethodPost, "/channels/"+channelID+"/messages", map[string]string{"content": content})
	return err
}

type DiscordMember struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Roles []string `json:"roles"`
	Nick  string   `json:"nick"`
}

func (d *Discord) GetMember(ctx context.Context, userID string) (*DiscordMember, error) {
	if !d.enabled() {
		return nil, ErrDiscordNotFound
	}
	data, err := d.do(ctx, http.MethodGet, "/guilds/"+d.ServerID+"/members/"+userID, nil)
	if err != nil {
		return nil, err
	}
	m := &DiscordMember{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, err
	}
	return m, nil
}

// ListMembers は limit 件ずつページングして全メンバーを列挙する。
func (d *Discord) ListMembers(ctx context.Context, limit int) ([]DiscordMember, error) {
	if !d.enabled() {
		return nil, nil
	}
	var out []DiscordMember
	after := ""
	for {
		q := url.Values{"limit": {fmt.Sprint(limit)}}
		if after != "" {
			q.Set("after", after)
		}
		data, err := d.do(ctx, http.MethodGet, "/guilds/"+d.ServerID+"/members?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var page []DiscordMember
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < limit {
			return out, nil
		}
		after = page[len(page)-1].User.ID
	}
}

func (d *Discord) AddMemberRole(ctx context.Context, userID, roleID string) error {
	if !d.enabled() {
		slog.Info("discord add_member_role (skipped)", "user_id", userID, "role_id", roleID)
		return nil
	}
	_, err := d.do(ctx, http.MethodPut, "/guilds/"+d.ServerID+"/members/"+userID+"/roles/"+roleID, nil)
	return err
}

func (d *Discord) RemoveMemberRole(ctx context.Context, userID, roleID string) error {
	if !d.enabled() {
		slog.Info("discord remove_member_role (skipped)", "user_id", userID, "role_id", roleID)
		return nil
	}
	_, err := d.do(ctx, http.MethodDelete, "/guilds/"+d.ServerID+"/members/"+userID+"/roles/"+roleID, nil)
	return err
}

func (d *Discord) UpdateMemberNick(ctx context.Context, userID, nick string) error {
	if !d.enabled() {
		slog.Info("discord update_member (skipped)", "user_id", userID, "nick", nick)
		return nil
	}
	_, err := d.do(ctx, http.MethodPatch, "/guilds/"+d.ServerID+"/members/"+userID, map[string]string{"nick": nick})
	return err
}

type DiscordMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
}

// MessagesAfter はチャンネルのメッセージを message_id 以降すべて取得する（support comm のログ用）。
func (d *Discord) MessagesAfter(ctx context.Context, channelID, afterMessageID string) ([]DiscordMessage, error) {
	if !d.enabled() {
		return nil, nil
	}
	var out []DiscordMessage
	after := afterMessageID
	for after != "" {
		q := url.Values{"limit": {"100"}, "after": {after}}
		data, err := d.do(ctx, http.MethodGet, "/channels/"+channelID+"/messages?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var page []DiscordMessage
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		// API は新しい順で返すので古い順に直す
		for i, j := 0, len(page)-1; i < j; i, j = i+1, j-1 {
			page[i], page[j] = page[j], page[i]
		}
		out = append(out, page...)
		after = page[len(page)-1].ID
	}
	return out, nil
}
