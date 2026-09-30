package model

import (
	"slices"
	"time"
)

// outcome は書き込みの結果。
type outcome int8

const (
	// inFlight は応答をまだ受け取っていない。
	inFlight outcome = iota
	// applied はアプリが書き込みを受け付けた。
	applied
	// rejected はアプリが書き込みを拒否した（4xx）。一度も反映されていないものとして扱う。
	rejected
	// unknown は結果がわからない（タイムアウト、5xx、通信エラー）。反映されたかもしれない。
	unknown
)

// change は値を 1 つ書き換える書き込み。sent は送信した時刻、received は応答を受けた時刻。
type change[T comparable] struct {
	value    T
	sent     time.Time
	received time.Time
	outcome  outcome
}

// timeline はある値の変更の履歴。
type timeline[T comparable] struct {
	initial T
	changes []*change[T]
}

func newTimeline[T comparable](initial T) *timeline[T] {
	return &timeline[T]{initial: initial}
}

// Read は 1 回の読み取り。送信した時刻と応答を受けた時刻の区間。Staleness は書き込みが
// 読み取りに反映されるまでに許す遅れ。
type Read struct {
	Sent      time.Time
	Received  time.Time
	Staleness time.Duration
}

// possible は読み取り r が観測しうる値をすべて返す。
//
//   - 応答を受けてから Staleness が経ったあとに送った読み取りには、確実に反映されている。
//   - 読み取りの応答より後に送った書き込みは、確実に反映されていない。
//   - それ以外（重なっている、結果がわからない）は、反映されているかもしれない。
//
// 確実に反映された書き込みのうち最後のものの値（どれが最後か決まらなければそのすべて）と、
// 反映されているかもしれない書き込みの値を返す。
func (t *timeline[T]) possible(r Read) []T {
	var certain, maybe []*change[T]
	for _, c := range t.changes {
		switch {
		case c.outcome == rejected || c.sent.After(r.Received):
		case c.outcome == applied && c.received.Add(r.Staleness).Before(r.Sent):
			certain = append(certain, c)
		default:
			maybe = append(maybe, c)
		}
	}
	var values []T
	add := func(v T) {
		if !slices.Contains(values, v) {
			values = append(values, v)
		}
	}
	if len(certain) == 0 {
		add(t.initial)
	}
	for _, c := range certain {
		// 別の確実な書き込みがこれより確実に後なら、これは最後ではない。
		superseded := slices.ContainsFunc(certain, func(other *change[T]) bool { return c.received.Before(other.sent) })
		if !superseded {
			add(c.value)
		}
	}
	for _, c := range maybe {
		add(c.value)
	}
	return values
}

// Write は記録中の書き込み。リクエストを送る直前に作り、応答を受けたら結果を記録する。
// 送信中の書き込みも読み取りの判定に使うので、結果がわかる前に記録を始める。
type Write struct {
	model   *Model
	changes []func(outcome, time.Time)
	after   func(outcome)
}

func (w *Write) finish(o outcome) {
	w.model.mu.Lock()
	defer w.model.mu.Unlock()
	now := w.model.now()
	for _, set := range w.changes {
		set(o, now)
	}
	if w.after != nil {
		w.after(o)
	}
}

// Applied はアプリが書き込みを受け付けたことを記録する。
func (w *Write) Applied() { w.finish(applied) }

// Rejected はアプリが書き込みを拒否したことを記録する。
func (w *Write) Rejected() { w.finish(rejected) }

// Unknown は結果がわからないことを記録する。
func (w *Write) Unknown() { w.finish(unknown) }

// begin は tl に value への変更を送信中として加え、w に結果の記録方法を足す。
func begin[T comparable](w *Write, tl *timeline[T], value T) {
	c := &change[T]{value: value, sent: w.model.now(), outcome: inFlight}
	tl.changes = append(tl.changes, c)
	w.changes = append(w.changes, func(o outcome, at time.Time) {
		c.outcome, c.received = o, at
	})
}
