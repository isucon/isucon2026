// Package agent はベンチマーカーが動かすアプリのユーザー 1 人を表す。
// 身元、リージョン、その国を送るクライアントを持つ。
package agent

import (
	"context"
	"fmt"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
)

type Agent struct {
	ID        string
	Username  string
	SecretKey string
	Token     string
	Region    string
	// API はこのユーザーの国を送るので、ルーターは所属リージョンのサーバーへ
	// 振り分ける。接続は他のユーザーと共有しない。
	API *client.Client
}

// New は region のユーザーを作る。index でリージョン内の国を選ぶ。
// ユーザーは自分専用の接続を持つ。
func New(api *client.Client, region string, index int) *Agent {
	return &Agent{Region: region, API: api.WithCountry(geo.Country(region, index)).WithOwnConnections()}
}

func (a *Agent) Country() string { return a.API.Country() }

// Register は username を所属リージョンで登録し、発行されたシークレットキーで
// ログインする。
func (a *Agent) Register(ctx context.Context, username, displayName string) error {
	registered, err := a.API.Register(ctx, username, displayName, a.Region)
	if err != nil {
		return err
	}
	if registered.ID == "" || registered.SecretKey == "" {
		return fmt.Errorf("%s の登録の応答にユーザー ID かシークレットキーがありません", username)
	}
	a.ID, a.Username, a.SecretKey = registered.ID, username, registered.SecretKey
	return a.Login(ctx)
}

// Login はシークレットキーでログインし直し、セッションを置き換える。
func (a *Agent) Login(ctx context.Context) error {
	user, err := a.API.Login(ctx, a.Username, a.SecretKey)
	if err != nil {
		return err
	}
	if user.ID != a.ID || user.Username != a.Username || user.Token == "" {
		return fmt.Errorf("%s のログインの応答のユーザーかトークンが違います", a.Username)
	}
	a.Token = user.Token
	return nil
}
