package isureal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/labstack/echo/v5"
)

type batchSessionKey struct{}
type batchSession struct {
	session     *Session
	marker      string
	region      string
	attempted   bool
	readonly    bool
	includeUser bool
}

func init() { sql.Register("pgx-auth", sessionDriver{}) }

type sessionDriver struct{}

func (sessionDriver) Open(name string) (driver.Conn, error) {
	c, err := (sessionDriver{}).OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}
func (sessionDriver) OpenConnector(name string) (driver.Connector, error) {
	c, err := stdlib.GetDefaultDriver().(driver.DriverContext).OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return sessionConnector{c}, nil
}

type sessionConnector struct{ driver.Connector }

func (c sessionConnector) Driver() driver.Driver { return sessionDriver{} }
func (c sessionConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &sessionConn{conn.(*stdlib.Conn)}, nil
}

type sessionConn struct{ *stdlib.Conn }

func (c *sessionConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.ExecContext(context.WithoutCancel(ctx), query, args)
}

var parameterNumber = regexp.MustCompile(`\$([0-9]+)\b`)

// Only application-owned SQL is rewritten. Values remain bound parameters.
// Authentication sets a transaction-local actor; SendBatch keeps both statements
// in one implicit transaction and skips the second if authentication fails.
func (c *sessionConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	// A client that goes away must not cancel the query: canceling closes the
	// connection, and when many clients leave at once the pool is left with
	// broken connections. The query finishes and the connection is reused.
	ctx = context.WithoutCancel(ctx)
	state, _ := ctx.Value(batchSessionKey{}).(*batchSession)
	if state == nil {
		return c.Conn.QueryContext(ctx, query, args)
	}
	if !state.attempted {
		// Sessions are replicated to every region's hot state before login and
		// logout respond, so a known session needs no database check.
		if user, ok := hot.authenticate(state.session.TokenHash, state.region); ok {
			state.attempted = true
			state.session.UserID, state.session.Region = user.ID, user.Region
			state.session.user = &user
		}
	}
	if state.attempted {
		for i := range args {
			if args[i].Value == state.marker {
				args[i].Value = state.session.UserID
			}
		}
		return c.Conn.QueryContext(ctx, query, args)
	}
	state.attempted = true
	values := make([]any, len(args))
	actorParams := make(map[int]bool)
	for i, a := range args {
		values[i] = a.Value
		if a.Value == state.marker {
			actorParams[i+1] = true
			values[i] = ""
		}
	}
	query = parameterNumber.ReplaceAllStringFunc(query, func(p string) string {
		n, _ := strconv.Atoi(p[1:])
		if actorParams[n] {
			return `(current_setting('isureal.actor') || ` + p + `::text)`
		}
		return p
	})
	batch := &pgx.Batch{}
	batch.Queue(`SELECT user_id,region,created_at,user_data FROM optimized_auth($1,$2,$3)`, state.session.TokenHash, state.region, state.includeUser)
	batch.Queue(query, values...)
	result := c.Conn.Conn().SendBatch(ctx, batch)
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = result.Close()
		}
	}()
	var userData []byte
	if err := result.QueryRow().Scan(&state.session.UserID, &state.session.Region, &state.session.CreatedAt, &userData); err != nil {
		_ = result.Close()
		return nil, c.batchError(state, err)
	}
	if userData != nil {
		var record writeResult
		if err := json.Unmarshal(userData, &record.UserData); err != nil {
			return nil, err
		}
		// Signing key validation happens in readSessionUser, after authentication.
		u := record.UserData.User
		u.SecretKeyHash = record.UserData.StoredSecret
		u.ProfileImageID = record.UserData.StoredProfile
		state.session.user = &u
	}
	rows, err := result.Query()
	if err != nil {
		_ = result.Close()
		return nil, c.batchError(state, err)
	}
	if state.readonly {
		keepOpen = true
		return &streamSessionRows{rows: rows, batch: result, conn: c.Conn.Conn()}, nil
	}
	// Finish the implicit transaction before exposing results. In particular, an
	// error at commit must never produce a successful mutation response.
	buffered := &sessionRows{}
	fields := rows.FieldDescriptions()
	for _, f := range fields {
		buffered.columns = append(buffered.columns, f.Name)
	}
	types := c.Conn.Conn().TypeMap()
	for rows.Next() {
		values := make([]driver.Value, len(fields))
		for i, raw := range rows.RawValues() {
			if raw == nil {
				continue
			}
			typ, ok := types.TypeForOID(fields[i].DataTypeOID)
			if !ok {
				values[i] = string(raw)
				continue
			}
			value, decodeErr := typ.Codec.DecodeDatabaseSQLValue(types, fields[i].DataTypeOID, fields[i].Format, raw)
			if decodeErr != nil {
				rows.Close()
				return nil, decodeErr
			}
			values[i] = value
		}
		buffered.values = append(buffered.values, values)
	}
	if err := rows.Err(); err != nil {
		return nil, sessionBatchError(err)
	}
	if err := result.Close(); err != nil {
		return nil, sessionBatchError(err)
	}
	return buffered, nil
}

type sessionRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *sessionRows) Columns() []string { return r.columns }
func (r *sessionRows) Close() error      { r.values = nil; return nil }
func (r *sessionRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

// batchError lets database/sql retry a read-only batch on another connection
// when this one broke (for example, closed after a canceled request). Reads have
// no effect, so a retry is safe; the retry authenticates again.
func (c *sessionConn) batchError(state *batchSession, err error) error {
	if state.readonly && (pgconn.SafeToRetry(err) || c.Conn.Conn().IsClosed()) {
		state.attempted = false
		state.session.UserID = state.marker
		return driver.ErrBadConn
	}
	return sessionBatchError(err)
}

func sessionBatchError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "PZ001" {
		status, e := strconv.Atoi(pgErr.Detail)
		if e == nil && status >= 400 && status < 500 {
			return NewAPIError(status, pgErr.Message)
		}
	}
	// Never return ErrBadConn for a sent batch: its mutation may have committed.
	if errors.Is(err, driver.ErrBadConn) {
		return errors.New("session batch: " + err.Error())
	}
	return err
}

func sessionBatchEnabled(dbDriver string) bool { return strings.EqualFold(dbDriver, "pgx-auth") }

// Read-only handlers keep streaming (notably searches and image ZIP exports).
// Writes use buffered rows above so commit failures reach the HTTP handler.
type streamSessionRows struct {
	rows   pgx.Rows
	batch  pgx.BatchResults
	conn   *pgx.Conn
	closed bool
}

func (r *streamSessionRows) Columns() []string {
	fields := r.rows.FieldDescriptions()
	columns := make([]string, len(fields))
	for i, f := range fields {
		columns[i] = f.Name
	}
	return columns
}
func (r *streamSessionRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.rows.Close()
	return sessionBatchError(r.batch.Close())
}
func (r *streamSessionRows) Next(dest []driver.Value) error {
	if !r.rows.Next() {
		err := r.rows.Err()
		closeErr := r.Close()
		if err != nil {
			return sessionBatchError(err)
		}
		if closeErr != nil {
			return closeErr
		}
		return io.EOF
	}
	fields := r.rows.FieldDescriptions()
	types := r.conn.TypeMap()
	for i, raw := range r.rows.RawValues() {
		dest[i] = nil
		if raw == nil {
			continue
		}
		typ, ok := types.TypeForOID(fields[i].DataTypeOID)
		if !ok {
			dest[i] = string(raw)
			continue
		}
		value, err := typ.Codec.DecodeDatabaseSQLValue(types, fields[i].DataTypeOID, fields[i].Format, raw)
		if err != nil {
			return err
		}
		dest[i] = value
	}
	return nil
}

// hotSessionUser authenticates a batched session against the regional hot
// state. When the session is known, the user is returned and the handler
// may answer from the state without a database round trip; the session's
// region is checked by the caller's RegionMiddleware contract (the user must
// belong to this app's region). ok is false when the state cannot answer and
// the handler must use the database path.
func hotSessionUser(c *echo.Context, h *HandlerContext) (User, bool) {
	state, _ := c.Request().Context().Value(batchSessionKey{}).(*batchSession)
	if state == nil {
		return User{}, false
	}
	if state.attempted {
		if state.session.user != nil {
			return *state.session.user, true
		}
		return User{}, false
	}
	user, ok := hot.authenticate(state.session.TokenHash, state.region)
	if !ok {
		return User{}, false
	}
	state.attempted = true
	state.session.UserID, state.session.Region = user.ID, user.Region
	state.session.user = &user
	return user, true
}
