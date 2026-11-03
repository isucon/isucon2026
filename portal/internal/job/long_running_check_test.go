package job

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/portal/internal/model"
)

func TestLongRunningCheckBenchmarkThreshold(t *testing.T) {
	var benchmarkCutoff time.Time
	connector := &longRunningConnector{query: func(query string, args []driver.NamedValue) {
		if !strings.Contains(query, "FROM benchmark_jobs") {
			return
		}
		if !strings.Contains(query, "status IN ($1, $2) AND created_at <= $3") {
			t.Errorf("unexpected benchmark filter: %s", query)
		}
		if args[0].Value != int64(model.JobPending) || args[1].Value != int64(model.JobRunning) {
			t.Errorf("unexpected status arguments: %v", args[:2])
		}
		benchmarkCutoff = args[2].Value.(time.Time)
	}}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	before := time.Now().UTC()
	if err := (&Env{DB: db}).LongRunningCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()
	if benchmarkCutoff.Before(before.Add(-5*time.Minute)) || benchmarkCutoff.After(after.Add(-5*time.Minute)) {
		t.Fatalf("benchmark cutoff = %v, want between %v and %v", benchmarkCutoff, before.Add(-5*time.Minute), after.Add(-5*time.Minute))
	}
}

// Return empty results so the check exercises its queries without sending alerts.
type longRunningConnector struct {
	query func(string, []driver.NamedValue)
}

func (c *longRunningConnector) Connect(context.Context) (driver.Conn, error) {
	return &longRunningConn{connector: c}, nil
}
func (c *longRunningConnector) Driver() driver.Driver { return c }
func (c *longRunningConnector) Open(string) (driver.Conn, error) {
	return c.Connect(context.Background())
}

type longRunningConn struct{ connector *longRunningConnector }

func (c *longRunningConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (c *longRunningConn) Close() error { return nil }
func (c *longRunningConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not implemented")
}
func (c *longRunningConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.connector.query(query, args)
	return longRunningEmptyRows{}, nil
}

type longRunningEmptyRows struct{}

func (longRunningEmptyRows) Columns() []string         { return nil }
func (longRunningEmptyRows) Close() error              { return nil }
func (longRunningEmptyRows) Next([]driver.Value) error { return io.EOF }
