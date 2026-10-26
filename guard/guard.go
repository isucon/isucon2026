package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"strconv"
	"time"
)

const statsInterval = 30 * time.Second

// detector は IP ごとに、集計期間内のログイン失敗の時刻を持つ。時刻はジャーナルの記録時刻を使う。
type detector struct {
	threshold int
	window    time.Duration
	failures  map[string][]time.Time
	lastSweep time.Time
}

func newDetector(threshold int, window time.Duration) *detector {
	return &detector{threshold: threshold, window: window, failures: make(map[string][]time.Time)}
}

// observe は ip のログイン失敗を記録し、BAN すべきなら true を返す。BAN したら数え直す。
func (d *detector) observe(ip string, at time.Time) bool {
	d.sweep(at)
	kept := d.failures[ip][:0]
	for _, failedAt := range d.failures[ip] {
		if at.Sub(failedAt) < d.window {
			kept = append(kept, failedAt)
		}
	}
	kept = append(kept, at)
	if len(kept) >= d.threshold {
		delete(d.failures, ip)
		return true
	}
	d.failures[ip] = kept
	return false
}

// sweep は集計期間ごとに、期間内の失敗がなくなった IP を捨てる。
func (d *detector) sweep(now time.Time) {
	if now.Sub(d.lastSweep) < d.window {
		return
	}
	d.lastSweep = now
	for ip, times := range d.failures {
		if now.Sub(times[len(times)-1]) >= d.window {
			delete(d.failures, ip)
		}
	}
}

type journalEntry struct {
	Message  json.RawMessage `json:"MESSAGE"`
	Realtime string          `json:"__REALTIME_TIMESTAMP"`
}

type actionLog struct {
	Action *string `json:"action"`
	Status int     `json:"status"`
	IP     string  `json:"ip"`
}

// parseLoginFailure は journalctl --output json の 1 行を読み、ログイン失敗のアクションログなら
// IP と記録時刻を返す。アクションログでない行は ok=false になる。
func parseLoginFailure(line []byte) (ip string, at time.Time, isAction, ok bool) {
	var entry journalEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return "", time.Time{}, false, false
	}
	// MESSAGE は UTF-8 でないとバイト列の配列になる。アクションログは ASCII なので文字列だけを読む。
	var message string
	if err := json.Unmarshal(entry.Message, &message); err != nil {
		return "", time.Time{}, false, false
	}
	var action actionLog
	if err := json.Unmarshal([]byte(message), &action); err != nil || action.Action == nil {
		return "", time.Time{}, false, false
	}
	if *action.Action != "login" || action.Status != 401 || action.IP == "" {
		return "", time.Time{}, true, false
	}
	micros, err := strconv.ParseInt(entry.Realtime, 10, 64)
	if err != nil {
		return "", time.Time{}, true, false
	}
	return action.IP, time.UnixMicro(micros), true, true
}

// run はジャーナルを 1 行ずつ読み、閾値を超えた IP を ban で BAN する。
func run(ctx context.Context, journal io.Reader, d *detector, ban func(context.Context, string) error, logger *log.Logger) error {
	var lines, actions, failures, bans int
	nextStats := time.Now().Add(statsInterval)
	scanner := bufio.NewScanner(journal)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines++
		ip, at, isAction, ok := parseLoginFailure(scanner.Bytes())
		if isAction {
			actions++
		}
		if ok {
			failures++
			if d.observe(ip, at) {
				if err := ban(ctx, ip); err != nil {
					logger.Printf("ban failed: ip=%s: %v", ip, err)
				} else {
					bans++
					logger.Printf("banned: ip=%s failures=%d window=%s", ip, d.threshold, d.window)
				}
			}
		}
		if now := time.Now(); now.After(nextStats) {
			logger.Printf("stats: lines=%d action_logs=%d login_failures=%d bans=%d", lines, actions, failures, bans)
			nextStats = now.Add(statsInterval)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

type dbBanner struct {
	db       *sql.DB
	duration time.Duration
}

// ban は BAN の期限を DB の時刻で決める。すでに BAN されていれば遅い方の期限を残す。
func (b *dbBanner) ban(ctx context.Context, ip string) error {
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO ip_bans (ip, banned_until)
		VALUES ($1, clock_timestamp() + make_interval(secs => $2))
		ON CONFLICT (ip) DO UPDATE SET banned_until = GREATEST(ip_bans.banned_until, EXCLUDED.banned_until)
	`, ip, b.duration.Seconds())
	return err
}
