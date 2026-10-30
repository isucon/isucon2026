package initialdata

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/agent"
	"github.com/isucon/isucon2026/bench/internal/client"
)

// zipOf は name の項目に data を deflate で入れた ZIP。
func zipOf(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// exportAgent は testWorld の最初の検証対象として、base のアプリへ送る Agent。
func exportAgent(c *Checker, base string) *agent.Agent {
	owner := c.Targets(testSeed)[0]
	return &agent.Agent{ID: owner.ID, Username: owner.Username, SecretKey: owner.SecretKey, Token: owner.ID, Region: owner.Region, API: client.New(base)}
}

// TestCheckIsReproducible は、初期データの検証の対象、Memories の月、取得する画像が走行の種で
// 決まり、同じ種なら同じ検証になることを確かめる。内訳（Report）に月と段階ごとの件数が入る。
func TestCheckIsReproducible(t *testing.T) {
	c := New(testWorld)
	app := newFakeApp(c)
	var mu sync.Mutex
	var fetched []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := strings.CutPrefix(r.URL.Path, "/api/images/"); ok {
			mu.Lock()
			fetched = append(fetched, id)
			mu.Unlock()
		}
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	run := func(seed uint64) (string, Report, []string) {
		targets := c.Targets(seed)
		var names []string
		for _, u := range targets {
			names = append(names, u.ID)
		}
		mu.Lock()
		fetched = nil
		mu.Unlock()
		// 友達の最も多いユーザーのあとの、無作為に選んだユーザー。
		report, err := c.Check(t.Context(), client.New(server.URL), targets[1], seed)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		images := slices.Clone(fetched)
		slices.Sort(images)
		return strings.Join(names, ","), report, images
	}
	targets1, report1, images1 := run(7)
	targets2, report2, images2 := run(7)
	if targets1 != targets2 || !slices.Equal(report1.Months, report2.Months) || !slices.Equal(images1, images2) {
		t.Fatalf("the same seed checked differently: %s / %s, %v / %v, %v / %v", targets1, targets2, report1.Months, report2.Months, images1, images2)
	}
	if len(report1.Months) != c.MemoryMonths {
		t.Fatalf("months=%v", report1.Months)
	}
	var steps []string
	for _, s := range report1.Steps {
		steps = append(steps, s.Name)
		if s.Count <= 0 && s.Name != "friends" {
			t.Errorf("step %s counted %d", s.Name, s.Count)
		}
	}
	if !slices.Equal(steps, []string{"login", "memories", "friends", "export", "images"}) {
		t.Fatalf("steps=%v", steps)
	}
	distinct := map[string]bool{}
	for seed := range uint64(10) {
		var names []string
		for _, u := range c.Targets(seed) {
			names = append(names, u.ID)
		}
		distinct[strings.Join(names, ",")] = true
	}
	if len(distinct) < 2 {
		t.Fatal("the targets do not change with the seed")
	}
}

// TestExportDecompressionLimit は、小さな ZIP から上限を超える posts.json を展開させるエクスポートを、
// 全部を展開する前にすぐ拒否することを確かめる。
func TestExportDecompressionLimit(t *testing.T) {
	archive := zipOf(t, "posts.json", append(bytes.Repeat([]byte(" "), 32<<20), "[]"...))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	c := New(testWorld)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	started := time.Now()
	err := c.CheckExport(ctx, exportAgent(c, server.URL), 0, func(string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "バイトを超えます") {
		t.Fatalf("err=%v, want the expansion limit", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("took %s to reject %d compressed bytes", elapsed, len(archive))
	}
}

// TestExportEntryLimits は、画像の項目 1 つの上限と、展開する量の合計の上限を確かめる。初期データ
// どおりのエクスポートは、上限が足りていれば通る。
func TestExportEntryLimits(t *testing.T) {
	c := New(testWorld)
	server := httptest.NewServer(newFakeApp(c))
	defer server.Close()
	a := exportAgent(c, server.URL)
	isNew := func(string) bool { return false }
	if err := c.CheckExport(t.Context(), a, AllImages, isNew); err != nil {
		t.Fatal(err)
	}
	c.exportImageLimit = 100
	if err := c.CheckExport(t.Context(), a, AllImages, isNew); err == nil || !strings.Contains(err.Error(), "展開すると 100 バイトを超えます") {
		t.Fatalf("image limit: err=%v", err)
	}
	c.exportImageLimit, c.exportBudget = maxExportImageBytes, 4096
	if err := c.CheckExport(t.Context(), a, AllImages, isNew); err == nil || !strings.Contains(err.Error(), "合わせて 4096 バイトを超えます") {
		t.Fatalf("budget: err=%v", err)
	}
}

// TestZipBudgetStopsAtDeadline は、ZIP の項目を読んでいる途中で ctx が終われば、読むのをやめて
// ctx のエラーを返すことを確かめる。
func TestZipBudgetStopsAtDeadline(t *testing.T) {
	archive := zipOf(t, "posts.json", bytes.Repeat([]byte(" "), 8<<20))
	r, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b := &zipBudget{total: maxExportExpandedBytes, remaining: maxExportExpandedBytes}
	if _, err := b.read(ctx, r.File[0], maxExportJSONBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	// 読み始めたあとで終わっても、残りを読まずにやめる。
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	reader := ctxReader{ctx: ctx, r: strings.NewReader("data")}
	cancel()
	if _, err := reader.Read(make([]byte, 4)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}
