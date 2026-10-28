package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/zeebo/xxh3"
)

const (
	apiPrefix          = "/api"
	requestTimeout     = 15 * time.Second
	maxErrorBodyBytes  = 1 << 20
	maxImageBytes      = 16 << 20
	maxExportBytes     = 256 << 20
	multipartImageName = "photo.jpg"
	// maxIdleConnsPerUser は 1 人の端末が使い回すために残す接続の数。履歴の画像を並行に取る
	// 接続（6 本）と通知のストリームが同時に開くので、Go の既定の 2 本では残りを閉じてしまい、
	// 次の操作のたびに接続し直すことになる。
	maxIdleConnsPerUser = 8
)

// RetryPolicy は 503 の再試行の仕方を決める（P-115 §6.4）。すべての
// エンドポイントで同じく、full jitter の指数バックオフで待つ。Retry-After が
// あれば MaxWait で丸めてそれに従う。ほかのステータスと通信エラーは
// 再試行しない。
type RetryPolicy struct {
	MaxRetries int
	Base       time.Duration
	MaxWait    time.Duration
}

// DefaultRetryPolicy は 4 回の再試行で最大およそ 3 秒待つ。値は較正までの暫定値。
var DefaultRetryPolicy = RetryPolicy{MaxRetries: 4, Base: 200 * time.Millisecond, MaxWait: 2 * time.Second}

func (p RetryPolicy) backoff(retry int) time.Duration {
	ceiling := p.Base << retry
	if ceiling <= 0 || ceiling > p.MaxWait {
		ceiling = p.MaxWait
	}
	if ceiling <= 0 {
		return 0
	}
	return rand.N(ceiling + 1)
}

func (p RetryPolicy) wait(retry int, retryAfter string) time.Duration {
	if retryAfter == "" {
		return p.backoff(retry)
	}
	var wait time.Duration
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		wait = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(retryAfter); err == nil {
		wait = time.Until(at)
	} else {
		return p.backoff(retry)
	}
	return min(max(wait, 0), p.MaxWait)
}

// Record は論理的な 1 操作を表す。503 の再試行を含めた、1 リクエストの
// すべての試行をまとめたもの。
type Record struct {
	Route    string // メソッドとルートのテンプレート。例: "GET /api/feeds/:feed"
	Country  string
	Start    time.Time
	Duration time.Duration // 再試行の待ちを含めた所要時間
	Attempts int
	Saw503   bool
	Status   int    // 最後の試行のステータス。レスポンスを受け取れなければ 0
	Code     string // 最後の試行が期待外のステータスのときの応答の error（ip_banned など）
	Err      error  // 最後のステータスが期待どおりなら nil
	Timeout  bool
	// Allowed は、期待したステータスではないが、呼び出し側が許したステータスで終わったこと
	// （MayFailWith）。計測では失敗に数えない。
	Allowed bool
}

type allowedStatusKey struct{}

// MayFailWith は、この操作が statuses で終わってもよいことを示す context を返す。たとえば
// 送っている間に投稿が見えなくなりうる RealMoji は 403 で終わってもよい。操作の結果は
// 通常どおりエラーとして返るので、それが正しいかは呼び出し側が判断する。
func MayFailWith(ctx context.Context, statuses ...int) context.Context {
	return context.WithValue(ctx, allowedStatusKey{}, statuses)
}

// Observer は完了した操作ごとに Record を受け取る。呼び出し側の context で
// キャンセルされた操作は渡さないので、負荷走行の終了時に処理中だった
// リクエストは集計から外れる。
type Observer interface {
	Observe(Record)
}

// InFlightObserver は、操作の完了に加えて開始も知りたい Observer。Begin は操作の開始時に
// 呼ばれ、返した関数は操作が終わったとき（キャンセルを含む）に呼ばれる。
type InFlightObserver interface {
	Begin(route, country string, start time.Time) func()
}

type Config struct {
	Base string
	// Host は HTTP の Host ヘッダを上書きする。ベンチの入口へ IP やポート
	// フォワードで接続しつつ、公開ホスト名で振り分けさせたいときに使う。
	Host      string
	Retry     RetryPolicy
	Observer  Observer
	Transport http.RoundTripper
}

type Client struct {
	base     string
	host     string
	country  string
	clientIP string
	retry    RetryPolicy
	observer Observer
	http     *http.Client
}

// NewTransport は 1 台の端末に相当する Transport を返す。実際の端末は
// HTTP/2 で他人とリクエストを多重化しないので HTTP/1.1 に固定する。
func NewTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.MaxIdleConnsPerHost = maxIdleConnsPerUser
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return transport
}

func NewClient(cfg Config) *Client {
	transport := cfg.Transport
	if transport == nil {
		transport = NewTransport()
	}
	return &Client{
		base:     strings.TrimRight(cfg.Base, "/"),
		host:     cfg.Host,
		retry:    cfg.Retry,
		observer: cfg.Observer,
		http: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			// ルーターの外へ出ない。別ホストへのリダイレクトはエミュレートした
			// ネットワークを迂回するうえ、Go はサブドメインへ Authorization を
			// 引き継ぐ。
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func New(base string) *Client {
	return NewWithHost(base, "")
}

func NewWithHost(base, host string) *Client {
	return NewClient(Config{Base: base, Host: host, Retry: DefaultRetryPolicy})
}

// WithCountry は X-Country-Code: country を送るクライアントを返す。
// コピーは接続プールを共有する。
func (c *Client) WithCountry(country string) *Client {
	copied := *c
	copied.country = country
	return &copied
}

// WithClientIP は X-Client-IP: ip を送るクライアントを返す。ルーターはこのヘッダが
// あればそのまま渡すので、ユーザーごとに異なる IP アドレスから来たように見える。
func (c *Client) WithClientIP(ip string) *Client {
	copied := *c
	copied.clientIP = ip
	return &copied
}

// ClientIP は X-Client-IP に送る値。
func (c *Client) ClientIP() string { return c.clientIP }

// WithOwnConnections は新しい Transport を持つクライアントを返す。
// ユーザーごとに呼び、端末ごとに keep-alive 接続を持たせる。接続を
// 共有しないので、サーバーから見た接続数はユーザー数に比例する。
func (c *Client) WithOwnConnections() *Client {
	copied := *c
	httpClient := *c.http
	httpClient.Transport = NewTransport()
	copied.http = &httpClient
	return &copied
}

// CloseIdleConnections は待機中の keep-alive 接続を閉じる。
func (c *Client) CloseIdleConnections() {
	c.http.CloseIdleConnections()
}

// WithObserver は操作を observer へ報告するクライアントを返す。
func (c *Client) WithObserver(observer Observer) *Client {
	copied := *c
	copied.observer = observer
	return &copied
}

func (c *Client) Country() string { return c.country }

// APIError は期待外のステータスを、spec-bench.md DIAG-01 が求める診断項目
// （メソッド、パス、期待値と実際の値）とともに報告する。
type APIError struct {
	Method   string
	Path     string
	Status   int
	Want     int
	Code     string
	Attempts int
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("%s %s: ステータスコード %d が返りました（期待値 %d、%s）", e.Method, e.Path, e.Status, e.Want, e.Code)
	if e.Attempts > 1 {
		message += fmt.Sprintf("（%d 回試行）", e.Attempts)
	}
	return message
}

type request struct {
	method      string
	path        string
	route       string
	token       string
	contentType string
	body        []byte
	want        int
	timeout     time.Duration
	maxBody     int64
	// digest は X-Benchmark-Request-Id（乱数の種）を付け、Router に本体の代わりにその種で計算した
	// XXH3 を返させる（画像とエクスポート）。Router がバイト列を二度転送しないため。
	digest bool
}

func (c *Client) newRequest(ctx context.Context, r request) (*http.Request, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, r.method, c.base+r.path, bytes.NewReader(r.body))
	if err != nil {
		return nil, err
	}
	if c.host != "" {
		httpRequest.Host = c.host
	}
	if c.country != "" {
		httpRequest.Header.Set("X-Country-Code", c.country)
	}
	if c.clientIP != "" {
		httpRequest.Header.Set("X-Client-IP", c.clientIP)
	}
	if r.contentType != "" {
		httpRequest.Header.Set("Content-Type", r.contentType)
	}
	if r.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+r.token)
	}
	if r.digest {
		httpRequest.Header.Set("X-Benchmark-Request-Id", strconv.FormatUint(rand.Uint64(), 10))
	}
	return httpRequest, nil
}

// digestMarker は、Router が本体の代わりにダイジェストを返したときに Image が返す値（エクスポートの
// 画像の項目も同じ）の先頭。続けて種、XXH3-64、長さをビッグエンディアンの uint64 で置く。
var digestMarker = []byte("\x00isureal-xxh3:")

func digestValue(seed, hash, length uint64) []byte {
	out := bytes.Clone(digestMarker)
	out = binary.BigEndian.AppendUint64(out, seed)
	out = binary.BigEndian.AppendUint64(out, hash)
	return binary.BigEndian.AppendUint64(out, length)
}

// SameImage は got（画像のバイト列か Router が返したダイジェスト）が want と同じかを返す。
// ダイジェストは要求ごとの種で計算するので、アプリが画像を持たずに合わせることはできない。
func SameImage(got, want []byte) bool {
	rest, ok := bytes.CutPrefix(got, digestMarker)
	if !ok || len(rest) != 24 {
		return bytes.Equal(got, want)
	}
	seed := binary.BigEndian.Uint64(rest)
	return binary.BigEndian.Uint64(rest[16:]) == uint64(len(want)) && binary.BigEndian.Uint64(rest[8:]) == xxh3.HashSeed(want, seed)
}

// send は r を送り、503 なら再試行ポリシーに従って再試行し、最後の
// レスポンスの本体を返す。
func (c *Client) send(ctx context.Context, r request) ([]byte, error) {
	record := Record{Route: r.route, Country: c.country, Start: time.Now()}
	if record.Route == "" {
		path, _, _ := strings.Cut(r.path, "?")
		record.Route = r.method + " " + path
	}
	if observer, ok := c.observer.(InFlightObserver); ok {
		defer observer.Begin(record.Route, record.Country, record.Start)()
	}
	data, err := c.attempts(ctx, r, &record)
	record.Duration = time.Since(record.Start)
	record.Err = err
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		record.Code = apiErr.Code
	}
	if allowed, ok := ctx.Value(allowedStatusKey{}).([]int); ok && err != nil && slices.Contains(allowed, record.Status) {
		record.Allowed = true
	}
	if err != nil && isTimeout(err) {
		record.Timeout = true
	}
	if c.observer != nil && ctx.Err() == nil {
		c.observer.Observe(record)
	}
	return data, err
}

func (c *Client) attempts(ctx context.Context, r request, record *Record) ([]byte, error) {
	httpClient := c.http
	if r.timeout > 0 {
		copied := *c.http
		copied.Timeout = r.timeout
		httpClient = &copied
	}
	maxBody := r.maxBody
	if maxBody == 0 {
		maxBody = maxErrorBodyBytes
	}
	for retry := 0; ; retry++ {
		httpRequest, err := c.newRequest(ctx, r)
		if err != nil {
			return nil, err
		}
		record.Attempts++
		response, err := httpClient.Do(httpRequest)
		if err != nil {
			record.Status = 0
			return nil, requestFailure(r, err)
		}
		record.Status = response.StatusCode
		if response.StatusCode == http.StatusServiceUnavailable && r.want != http.StatusServiceUnavailable && retry < c.retry.MaxRetries {
			record.Saw503 = true
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBodyBytes))
			_ = response.Body.Close()
			if err := sleep(ctx, c.retry.wait(retry, response.Header.Get("Retry-After"))); err != nil {
				return nil, requestFailure(r, err)
			}
			continue
		}
		if response.StatusCode == http.StatusServiceUnavailable {
			record.Saw503 = true
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
		_ = response.Body.Close()
		if err != nil {
			// 本体の読み取りの時間切れや切断は、応答を受け取れなかったのと同じ扱いにする。
			return nil, requestFailure(r, fmt.Errorf("応答の本体を読めません: %w", err))
		}
		if response.StatusCode != r.want {
			return nil, &APIError{Method: r.method, Path: r.path, Status: response.StatusCode, Want: r.want, Code: problemCode(data), Attempts: record.Attempts}
		}
		if sum := response.Header.Get("X-Body-Xxh3"); r.digest && sum != "" {
			seed, err1 := strconv.ParseUint(response.Request.Header.Get("X-Benchmark-Request-Id"), 10, 64)
			hash, err2 := strconv.ParseUint(sum, 16, 64)
			length, err3 := strconv.ParseUint(response.Header.Get("X-Body-Length"), 10, 64)
			if err := errors.Join(err1, err2, err3); err != nil {
				return nil, requestFailure(r, fmt.Errorf("X-Body-Xxh3 %q を解釈できません: %w", sum, err))
			}
			return digestValue(seed, hash, length), nil
		}
		return data, nil
	}
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	data, err := c.send(ctx, r)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: 応答の JSON を解釈できません: %w", r.method, r.path, err)
		}
	}
	return nil
}

// RequestError は応答を受け取れなかったリクエスト（タイムアウト、通信エラー）。
type RequestError struct {
	Method string
	Path   string
	Err    error
}

func (e *RequestError) Error() string {
	if isTimeout(e.Err) {
		return fmt.Sprintf("%s %s: 応答がタイムアウトしました", e.Method, e.Path)
	}
	return fmt.Sprintf("%s %s: 通信に失敗しました: %v", e.Method, e.Path, e.Err)
}

func (e *RequestError) Unwrap() error { return e.Err }

func requestFailure(r request, err error) error {
	return &RequestError{Method: r.method, Path: r.path, Err: err}
}

func sleep(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func problemCode(body []byte) string {
	var problem struct {
		Code string `json:"error"`
	}
	if err := json.Unmarshal(body, &problem); err != nil || problem.Code == "" {
		return "unknown"
	}
	return problem.Code
}

func marshalJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal request: %v", err))
	}
	return data
}

// --- 競技運営用のエンドポイント ---

type InitializeResult struct {
	Language string `json:"language"`
	Agent    string `json:"agent"`
	Harness  string `json:"harness"`
	Model    string `json:"model"`
}

func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	var result InitializeResult
	// 前回の走行で増えた大きな写真を消すので時間がかかる。ルーターの上流タイムアウトと同じ 60 秒。
	err := c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/initialize", want: http.StatusOK, timeout: 60 * time.Second}, &result)
	return result, err
}

// --- 認証 ---

type User struct {
	ID        string
	Username  string
	Token     string
	SecretKey string
}

type sessionResponse struct {
	SecretKey string `json:"secretKey"`
	Token     string `json:"token"`
	User      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
}

func (s sessionResponse) user() User {
	return User{ID: s.User.ID, Username: s.User.Username, Token: s.Token, SecretKey: s.SecretKey}
}

func (c *Client) Register(ctx context.Context, username, displayName, region string) (User, error) {
	body := marshalJSON(map[string]string{
		"username": username, "displayName": displayName, "region": region,
	})
	var session sessionResponse
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/auth/register",
		contentType: "application/json", body: body, want: http.StatusCreated,
	}, &session)
	return session.user(), err
}

func (c *Client) Login(ctx context.Context, username, secretKey string) (User, error) {
	body := marshalJSON(map[string]string{"username": username, "secretKey": secretKey})
	var session sessionResponse
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/auth/login",
		contentType: "application/json", body: body, want: http.StatusOK,
	}, &session)
	return session.user(), err
}

func (c *Client) AdminLogin(ctx context.Context, username, password string) (string, error) {
	body := marshalJSON(map[string]string{"username": username, "password": password})
	var result struct {
		Token string `json:"token"`
	}
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/admin/auth/login",
		contentType: "application/json", body: body, want: http.StatusOK,
	}, &result)
	return result.Token, err
}

func (c *Client) OpenWindow(ctx context.Context, token, region, date string) (Window, error) {
	var window Window
	err := c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/admin/posting-window", token: token, contentType: "application/json", body: marshalJSON(map[string]string{"region": region, "localDate": date}), want: http.StatusCreated}, &window)
	return window, err
}

func (c *Client) Logout(ctx context.Context, token string) error {
	return c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/auth/logout", token: token, want: http.StatusNoContent}, nil)
}

// --- 投稿 ---

type Window struct {
	ID        string    `json:"id"`
	Region    string    `json:"region"`
	LocalDate string    `json:"localDate"`
	StartsAt  time.Time `json:"startsAt"`
}

type WindowStatus struct {
	Window    *Window `json:"window"`
	HasPosted bool    `json:"hasPosted"`
	CanPost   bool    `json:"canPost"`
}

func (c *Client) CurrentWindow(ctx context.Context, token string) (WindowStatus, error) {
	var status WindowStatus
	err := c.do(ctx, request{
		method: http.MethodGet, path: apiPrefix + "/posting-window/current", token: token, want: http.StatusOK,
	}, &status)
	return status, err
}

// UserSummary は投稿者、リアクションしたユーザー、関係の相手などとして埋め込まれるユーザー。
type UserSummary struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"displayName"`
	ProfileImageURL *string `json:"profileImageUrl"`
	Bio             string  `json:"bio"`
	AccountType     string  `json:"accountType"`
}

type Location struct {
	Kind      string   `json:"kind"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Locality  *string  `json:"locality"`
}

type Post struct {
	ID              string      `json:"id"`
	Author          UserSummary `json:"author"`
	PostingWindowID string      `json:"postingWindowId"`
	ImageURL        string      `json:"imageUrl"`
	Caption         string      `json:"caption"`
	Visibility      string      `json:"visibility"`
	Location        Location    `json:"location"`
	PostedAt        time.Time   `json:"postedAt"`
	OnTime          bool        `json:"onTime"`
	LateSeconds     int64       `json:"lateSeconds"`
}

type filePart struct {
	field       string
	filename    string
	contentType string
	data        []byte
}

func buildMultipart(fields map[string]string, files []filePart) (string, []byte, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for field, value := range fields {
		if err := writer.WriteField(field, value); err != nil {
			return "", nil, err
		}
	}
	for _, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, file.field, file.filename))
		header.Set("Content-Type", file.contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return "", nil, err
		}
		if _, err := part.Write(file.data); err != nil {
			return "", nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return "", nil, err
	}
	return writer.FormDataContentType(), buffer.Bytes(), nil
}

func (c *Client) CreatePost(ctx context.Context, token string, image []byte, caption, visibility string) (Post, error) {
	contentType, body, err := buildMultipart(
		map[string]string{"caption": caption, "visibility": visibility, "location_kind": "none"},
		[]filePart{
			{field: "image", filename: multipartImageName, contentType: "image/jpeg", data: image},
		},
	)
	if err != nil {
		return Post{}, err
	}
	var post Post
	err = c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts",
		token: token, contentType: contentType, body: body, want: http.StatusCreated,
	}, &post)
	return post, err
}

// FeedAuthor はフィードの投稿者のうち、bench が確かめる項目。
type FeedAuthor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// FeedPost はフィードの投稿のうち、bench が確かめて使う項目。フィードは負荷走行で最も
// 多く読むので、使わない項目（投稿者のプロフィール、位置など）は値を作らずに読み飛ばす。
type FeedPost struct {
	ID              string     `json:"id"`
	Author          FeedAuthor `json:"author"`
	PostingWindowID string     `json:"postingWindowId"`
	ImageURL        string     `json:"imageUrl"`
	Caption         string     `json:"caption"`
	Visibility      string     `json:"visibility"`
	PostedAt        time.Time  `json:"postedAt"`
}

// FeedPost は投稿をフィードの投稿の項目に絞る。
func (p Post) FeedPost() FeedPost {
	return FeedPost{
		ID: p.ID, Author: FeedAuthor{ID: p.Author.ID, Username: p.Author.Username}, PostingWindowID: p.PostingWindowID,
		ImageURL: p.ImageURL, Caption: p.Caption, Visibility: p.Visibility, PostedAt: p.PostedAt,
	}
}

type FeedPage struct {
	Items   []FeedPost `json:"items"`
	HasMore bool       `json:"hasMore"`
}

func (c *Client) Feed(ctx context.Context, token, feed string, before *FeedPost) (FeedPage, error) {
	path := apiPrefix + "/feeds/" + feed
	if before != nil {
		path += "?" + url.Values{"beforePostedAt": {before.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {before.ID}}.Encode()
	}
	var page FeedPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, token: token, want: http.StatusOK}, &page)
	return page, err
}

// --- 交流 ---

func (c *Client) CreateComment(ctx context.Context, token, postID, body string) (Comment, error) {
	var comment Comment
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts/" + postID + "/comments", route: "POST /api/posts/:postId/comments",
		token: token, contentType: "application/json",
		body: marshalJSON(map[string]string{"body": body}), want: http.StatusCreated,
	}, &comment)
	return comment, err
}

func (c *Client) ReplaceRealMoji(ctx context.Context, token, postID, reactionType string, image []byte) (RealMoji, error) {
	contentType, body, err := buildMultipart(
		map[string]string{"reactionType": reactionType},
		[]filePart{{field: "image", filename: multipartImageName, contentType: "image/jpeg", data: image}},
	)
	if err != nil {
		return RealMoji{}, err
	}
	var reaction RealMoji
	err = c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/posts/" + postID + "/realmojis", route: "POST /api/posts/:postId/realmojis",
		token: token, contentType: contentType, body: body, want: http.StatusCreated,
	}, &reaction)
	return reaction, err
}

type CommentPage struct {
	Items   []Comment `json:"items"`
	HasMore bool      `json:"hasMore"`
}

// Comments は投稿のコメントを古い順に 1 ページ取る。after があればそれより後から。
func (c *Client) Comments(ctx context.Context, token, postID string, after *Comment) (CommentPage, error) {
	path := apiPrefix + "/posts/" + postID + "/comments"
	if after != nil {
		path += "?" + url.Values{"afterCreatedAt": {after.CreatedAt.Format(time.RFC3339Nano)}, "afterId": {after.ID}}.Encode()
	}
	var page CommentPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, route: "GET /api/posts/:postId/comments", token: token, want: http.StatusOK}, &page)
	return page, err
}

func (c *Client) DeleteComment(ctx context.Context, token, commentID string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/comments/" + commentID, route: "DELETE /api/comments/:commentId", token: token, want: http.StatusNoContent}, nil)
}

type RealMojiPage struct {
	Items   []RealMoji `json:"items"`
	HasMore bool       `json:"hasMore"`
}

// RealMojis は投稿の RealMoji を新しい順に 1 ページ取る。before があればそれより前から。
func (c *Client) RealMojis(ctx context.Context, token, postID string, before *RealMoji) (RealMojiPage, error) {
	path := apiPrefix + "/posts/" + postID + "/realmojis"
	if before != nil {
		path += "?" + url.Values{"beforeCreatedAt": {before.CreatedAt.Format(time.RFC3339Nano)}, "beforeId": {before.ID}}.Encode()
	}
	var page RealMojiPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, route: "GET /api/posts/:postId/realmojis", token: token, want: http.StatusOK}, &page)
	return page, err
}

func (c *Client) DeleteRealMoji(ctx context.Context, token, realMojiID string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/realmojis/" + realMojiID, route: "DELETE /api/realmojis/:realMojiId", token: token, want: http.StatusNoContent}, nil)
}

// --- 読み取り ---

type Notification struct {
	ID              string `json:"id"`
	PostingWindowID string `json:"postingWindowId"`
	Region          string `json:"region"`
}

// notificationDrainTimeout は、通知を受けたストリームの残りを読み切るのを待つ上限。
const notificationDrainTimeout = time.Second

type NotificationStream struct {
	body   io.ReadCloser
	reader *bufio.Reader
	cancel context.CancelFunc
}

// OpenNotifications はサーバーが購読を確立してから返る。
func (c *Client) OpenNotifications(ctx context.Context, token string) (*NotificationStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := c.newRequest(ctx, request{method: http.MethodGet, path: apiPrefix + "/notifications", token: token})
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	streaming := *c.http
	streaming.Timeout = 0
	res, err := streaming.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	stream := &NotificationStream{body: res.Body, reader: bufio.NewReader(res.Body), cancel: cancel}
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(stream.reader, maxErrorBodyBytes))
		_ = stream.Close()
		return nil, &APIError{Method: http.MethodGet, Path: apiPrefix + "/notifications", Status: res.StatusCode, Want: http.StatusOK, Code: problemCode(data)}
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		_ = stream.Close()
		return nil, fmt.Errorf("通知の Content-Type が text/event-stream ではありません: %q", res.Header.Get("Content-Type"))
	}
	return stream, nil
}

// Close はストリームの残りを読み切ってから閉じる。応答を最後まで読まずに閉じると、接続を
// keep-alive で使い回せずに捨てることになる。アプリは通知を 1 件送ると応答を終えるが、
// 終えない実装で止まらないよう、notificationDrainTimeout を過ぎたら打ち切る（その接続は捨てる）。
func (s *NotificationStream) Close() error {
	timer := time.AfterFunc(notificationDrainTimeout, s.cancel)
	_, _ = io.Copy(io.Discard, io.LimitReader(s.reader, maxErrorBodyBytes))
	timer.Stop()
	err := s.body.Close()
	s.cancel()
	return err
}

// Receive は次の通知を 1 件読む。
func (s *NotificationStream) Receive() (Notification, error) {
	var data []string
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			if err == io.EOF {
				return Notification{}, io.EOF
			}
			return Notification{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" && len(data) > 0 {
			var notification Notification
			err := json.Unmarshal([]byte(strings.Join(data, "\n")), &notification)
			return notification, err
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(after, " "))
		}
		if err == io.EOF {
			return Notification{}, io.EOF
		}
	}
}

// Image は署名付きの画像 URL を取得する。署名付き URL には Authorization
// ヘッダが要らないので送らない。Router がダイジェストを返したときはそれを返すので、
// 中身は SameImage で比べる。
func (c *Client) Image(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, apiPrefix+"/images/") {
		return nil, fmt.Errorf("画像 URL %q が /api/images/ で始まりません", path)
	}
	return c.send(ctx, request{method: http.MethodGet, path: path, route: "GET /api/images/:imageId", want: http.StatusOK, maxBody: maxImageBytes, digest: true})
}

type Profile struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"displayName"`
	Bio             string  `json:"bio"`
	Region          string  `json:"region"`
	AccountType     string  `json:"accountType"`
	Status          string  `json:"status"`
	ProfileImageURL *string `json:"profileImageUrl"`
}

func (c *Client) Me(ctx context.Context, token string) (Profile, error) {
	var profile Profile
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/me", token: token, want: http.StatusOK}, &profile)
	return profile, err
}

type RealMoji struct {
	ID           string      `json:"id"`
	Reactor      UserSummary `json:"reactor"`
	ReactionType string      `json:"reactionType"`
	ImageURL     string      `json:"imageUrl"`
	CreatedAt    time.Time   `json:"createdAt"`
}

type Comment struct {
	ID        string      `json:"id"`
	Author    UserSummary `json:"author"`
	Body      string      `json:"body"`
	CreatedAt time.Time   `json:"createdAt"`
}

// MemoryPost は Memories の 1 件。投稿に RealMoji とコメントが付く。
type MemoryPost struct {
	Post
	RealMojis []RealMoji `json:"realmojis"`
	Comments  []Comment  `json:"comments"`
}

type MemoryPage struct {
	Items   []MemoryPost `json:"items"`
	HasMore bool         `json:"hasMore"`
}

func (c *Client) MemoriesMonth(ctx context.Context, token, month string, before *Post) (MemoryPage, error) {
	path := apiPrefix + "/memories?month=" + url.QueryEscape(month)
	if before != nil {
		path += "&" + url.Values{"beforePostedAt": {before.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {before.ID}}.Encode()
	}
	var page MemoryPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, token: token, want: http.StatusOK}, &page)
	return page, err
}

type Relationship struct {
	RelationID string      `json:"relationId"`
	User       UserSummary `json:"user"`
	Status     string      `json:"status"`
	UpdatedAt  time.Time   `json:"updatedAt"`
}

type RelationshipPage struct {
	Items   []Relationship `json:"items"`
	HasMore bool           `json:"hasMore"`
}

// Relationships は kind（friends、following、followers、incoming-requests、
// outgoing-requests）の関係を 1 ページ取る。
func (c *Client) Relationships(ctx context.Context, token, kind string, before *Relationship) (RelationshipPage, error) {
	path := apiPrefix + "/relationships/" + kind
	if before != nil {
		path += "?" + url.Values{"beforeUpdatedAt": {before.UpdatedAt.Format(time.RFC3339Nano)}, "beforeId": {before.RelationID}}.Encode()
	}
	var page RelationshipPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, token: token, want: http.StatusOK}, &page)
	return page, err
}

// Export は自分の全投稿の ZIP を取る。Router は JSON 以外の項目をダイジェストに置き換えることがある。
func (c *Client) Export(ctx context.Context, token string) ([]byte, error) {
	return c.send(ctx, request{method: http.MethodGet, path: apiPrefix + "/me/export", token: token, want: http.StatusOK, maxBody: maxExportBytes, digest: true})
}

// --- 関係と投稿の変更 ---

type FriendRequest struct {
	RelationID string    `json:"relationId"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// CreateFriendRequest は recipientID へ友達申請を送る。相手からの申請が保留中なら、
// その申請が承認されて status は accepted になる。
func (c *Client) CreateFriendRequest(ctx context.Context, token, recipientID string) (FriendRequest, error) {
	var result FriendRequest
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/friend-requests", token: token,
		contentType: "application/json", body: marshalJSON(map[string]string{"recipientId": recipientID}), want: http.StatusCreated,
	}, &result)
	return result, err
}

// TransitionFriendRequest は申請を action（accept、reject、cancel）で処理する。
func (c *Client) TransitionFriendRequest(ctx context.Context, token, requestID, action string) (FriendRequest, error) {
	var result FriendRequest
	err := c.do(ctx, request{
		method: http.MethodPost, path: apiPrefix + "/friend-requests/" + requestID + "/" + action,
		route: "POST /api/friend-requests/:requestId/" + action, token: token, want: http.StatusOK,
	}, &result)
	return result, err
}

func (c *Client) Block(ctx context.Context, token, userID string) error {
	return c.do(ctx, request{method: http.MethodPut, path: apiPrefix + "/blocks/" + userID, route: "PUT /api/blocks/:userId", token: token, want: http.StatusNoContent}, nil)
}

func (c *Client) UpdatePostVisibility(ctx context.Context, token, postID, visibility string) (Post, error) {
	var post Post
	err := c.do(ctx, request{
		method: http.MethodPatch, path: apiPrefix + "/posts/" + postID, route: "PATCH /api/posts/:postId", token: token,
		contentType: "application/json", body: marshalJSON(map[string]string{"visibility": visibility}), want: http.StatusOK,
	}, &post)
	return post, err
}

func (c *Client) DeletePost(ctx context.Context, token, postID string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/posts/" + postID, route: "DELETE /api/posts/:postId", token: token, want: http.StatusNoContent}, nil)
}

func (c *Client) Follow(ctx context.Context, token, userID string) error {
	return c.do(ctx, request{method: http.MethodPut, path: apiPrefix + "/follows/" + userID, route: "PUT /api/follows/:userId", token: token, want: http.StatusNoContent}, nil)
}

// --- 投稿の閲覧と編集 ---

// GetPost は投稿を 1 件取る。見えない投稿は 404。
func (c *Client) GetPost(ctx context.Context, token, postID string) (Post, error) {
	var post Post
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/posts/" + postID, route: "GET /api/posts/:postId", token: token, want: http.StatusOK}, &post)
	return post, err
}

// MyCurrentPost は自分の今日の投稿を取る。なければ 404。
func (c *Client) MyCurrentPost(ctx context.Context, token string) (Post, error) {
	var post Post
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/posts/me/current", token: token, want: http.StatusOK}, &post)
	return post, err
}

func (c *Client) UpdatePostCaption(ctx context.Context, token, postID, caption string) (Post, error) {
	var post Post
	err := c.do(ctx, request{
		method: http.MethodPatch, path: apiPrefix + "/posts/" + postID, route: "PATCH /api/posts/:postId", token: token,
		contentType: "application/json", body: marshalJSON(map[string]string{"caption": caption}), want: http.StatusOK,
	}, &post)
	return post, err
}

// MemoriesDate は date（YYYY-MM-DD）の Memories を 1 ページ取る。
func (c *Client) MemoriesDate(ctx context.Context, token, date string, before *Post) (MemoryPage, error) {
	path := apiPrefix + "/memories/" + date
	if before != nil {
		path += "?" + url.Values{"beforePostedAt": {before.PostedAt.Format(time.RFC3339Nano)}, "beforeId": {before.ID}}.Encode()
	}
	var page MemoryPage
	err := c.do(ctx, request{method: http.MethodGet, path: path, route: "GET /api/memories/:date", token: token, want: http.StatusOK}, &page)
	return page, err
}

// --- ユーザー ---

type UserPage struct {
	Items   []UserSummary `json:"items"`
	HasMore bool          `json:"hasMore"`
}

// SearchUsers はユーザー名か表示名に q を含むユーザーを探す。
func (c *Client) SearchUsers(ctx context.Context, token, q string) (UserPage, error) {
	var page UserPage
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/users?" + url.Values{"q": {q}}.Encode(), route: "GET /api/users", token: token, want: http.StatusOK}, &page)
	return page, err
}

func (c *Client) GetUser(ctx context.Context, token, userID string) (UserSummary, error) {
	var user UserSummary
	err := c.do(ctx, request{method: http.MethodGet, path: apiPrefix + "/users/" + userID, route: "GET /api/users/:userId", token: token, want: http.StatusOK}, &user)
	return user, err
}

// UpdateMe はプロフィールを変える。nil の項目は変えない。
func (c *Client) UpdateMe(ctx context.Context, token string, displayName, bio, region *string) (Profile, error) {
	body := map[string]string{}
	if displayName != nil {
		body["displayName"] = *displayName
	}
	if bio != nil {
		body["bio"] = *bio
	}
	if region != nil {
		body["region"] = *region
	}
	var profile Profile
	err := c.do(ctx, request{method: http.MethodPatch, path: apiPrefix + "/me", token: token, contentType: "application/json", body: marshalJSON(body), want: http.StatusOK}, &profile)
	return profile, err
}

func (c *Client) PutProfileImage(ctx context.Context, token string, image []byte) error {
	return c.do(ctx, request{method: http.MethodPut, path: apiPrefix + "/me/profile-image", token: token, contentType: "image/jpeg", body: image, want: http.StatusNoContent}, nil)
}

func (c *Client) DeleteProfileImage(ctx context.Context, token string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/me/profile-image", token: token, want: http.StatusNoContent}, nil)
}

func (c *Client) DeleteAccount(ctx context.Context, token string) error {
	return c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/me/account-deletion", token: token, want: http.StatusNoContent}, nil)
}

// --- 関係の解消 ---

func (c *Client) Unfollow(ctx context.Context, token, userID string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/follows/" + userID, route: "DELETE /api/follows/:userId", token: token, want: http.StatusNoContent}, nil)
}

func (c *Client) Unblock(ctx context.Context, token, userID string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/blocks/" + userID, route: "DELETE /api/blocks/:userId", token: token, want: http.StatusNoContent}, nil)
}

func (c *Client) Unfriend(ctx context.Context, token, userID string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/friendships/" + userID, route: "DELETE /api/friendships/:userId", token: token, want: http.StatusNoContent}, nil)
}

// --- 管理者 ---

func (c *Client) CertifyUser(ctx context.Context, token, userID string) error {
	return c.do(ctx, request{method: http.MethodPut, path: apiPrefix + "/admin/users/" + userID + "/certification", route: "PUT /api/admin/users/:userId/certification", token: token, want: http.StatusNoContent}, nil)
}

// DeleteCertification は認定を取り消す。アプリは取り消しを認めない（409）。
func (c *Client) DeleteCertification(ctx context.Context, token, userID string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: apiPrefix + "/admin/users/" + userID + "/certification", route: "DELETE /api/admin/users/:userId/certification", token: token, want: http.StatusNoContent}, nil)
}

func (c *Client) AdminLogout(ctx context.Context, token string) error {
	return c.do(ctx, request{method: http.MethodPost, path: apiPrefix + "/admin/auth/logout", token: token, want: http.StatusNoContent}, nil)
}
