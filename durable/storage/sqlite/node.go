// Ported from packages/durable/src/storage/sqlite/node.ts (pi v1.0.0).

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync"

	_ "modernc.org/sqlite"
)

// NodeSqliteStorageOptions configures SQLite connection settings for a durable storage file.
type NodeSqliteStorageOptions struct {
	// WalAutoCheckpointPages sets PRAGMA wal_autocheckpoint (defaults to 1000 pages; 0 disables).
	WalAutoCheckpointPages *int
	// BusyTimeoutMs sets PRAGMA busy_timeout in milliseconds (defaults to 5000 ms).
	BusyTimeoutMs *int
}

const (
	ndDefaultWalAutoCheckpointPages = 1000
	ndDefaultBusyTimeoutMs          = 5000
)

type ndTxScope struct {
	active bool
}

type ndStmtCache struct {
	mu    sync.Mutex
	stmts map[string]*sql.Stmt
}

func ndNewStmtCache() *ndStmtCache {
	return &ndStmtCache{stmts: make(map[string]*sql.Stmt)}
}

func (c *ndStmtCache) prepare(ctx context.Context, conn *sql.Conn, query string) (*sql.Stmt, error) {
	c.mu.Lock()
	stmt := c.stmts[query]
	c.mu.Unlock()
	if stmt != nil {
		return stmt, nil
	}
	prepared, err := conn.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if existing := c.stmts[query]; existing != nil {
		c.mu.Unlock()
		_ = prepared.Close()
		return existing, nil
	}
	c.stmts[query] = prepared
	c.mu.Unlock()
	return prepared, nil
}

func (c *ndStmtCache) closeAll() {
	c.mu.Lock()
	stmts := c.stmts
	c.stmts = make(map[string]*sql.Stmt)
	c.mu.Unlock()
	for _, stmt := range stmts {
		_ = stmt.Close()
	}
}

type ndSqlExecutor struct {
	conn  *sql.Conn
	cache *ndStmtCache
	ctx   context.Context
}

func (e *ndSqlExecutor) execRaw(sqlText string) error {
	_, err := e.conn.ExecContext(e.ctx, sqlText)
	return err
}

func (e *ndSqlExecutor) runRaw(query string, params ...SqliteValue) error {
	stmt, err := e.cache.prepare(e.ctx, e.conn, query)
	if err != nil {
		return err
	}
	_, err = stmt.ExecContext(e.ctx, params...)
	return err
}

func (e *ndSqlExecutor) queryRaw(query string, params ...SqliteValue) (SqliteRows, error) {
	stmt, err := e.cache.prepare(e.ctx, e.conn, query)
	if err != nil {
		return nil, err
	}
	rows, err := stmt.QueryContext(e.ctx, params...)
	if err != nil {
		return nil, err
	}
	return ndMaterializeRows(rows)
}

type ndBufferedRows struct {
	rows   [][]any
	idx    int
	err    error
	closed bool
}

func ndMaterializeRows(rows *sql.Rows) (SqliteRows, error) {
	cols, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	ncols := len(cols)
	var out [][]any
	vals := make([]any, ncols)
	ptrs := make([]any, ncols)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			_ = rows.Close()
			return nil, err
		}
		row := make([]any, ncols)
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				row[i] = slices.Clone(b)
			} else {
				row[i] = v
			}
		}
		out = append(out, row)
	}
	iterErr := rows.Err()
	closeErr := rows.Close()
	if iterErr == nil {
		iterErr = closeErr
	}
	return &ndBufferedRows{
		rows: out,
		idx:  -1,
		err:  iterErr,
	}, nil
}

func (r *ndBufferedRows) Next() bool {
	if r.closed || r.idx+1 >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *ndBufferedRows) Scan(dest ...any) error {
	if r.closed {
		return errors.New("sql: Rows are closed")
	}
	if r.idx < 0 || r.idx >= len(r.rows) {
		return errors.New("sql: Scan called without calling Next")
	}
	row := r.rows[r.idx]
	if len(dest) != len(row) {
		return fmt.Errorf("sql: expected %d destination arguments in Scan, not %d", len(row), len(dest))
	}
	for i, d := range dest {
		if err := ndAssignScan(d, row[i]); err != nil {
			return fmt.Errorf("sql: Scan error on column index %d: %w", i, err)
		}
	}
	return nil
}

func (r *ndBufferedRows) Err() error { return r.err }

func (r *ndBufferedRows) Close() error {
	r.closed = true
	return nil
}

func ndAssignScan(dest, src any) error {
	switch d := dest.(type) {
	case *any:
		if b, ok := src.([]byte); ok {
			*d = slices.Clone(b)
		} else {
			*d = src
		}
		return nil
	case *string:
		if src == nil {
			return errors.New("converting NULL to string is unsupported")
		}
		switch s := src.(type) {
		case string:
			*d = s
			return nil
		case []byte:
			*d = string(s)
			return nil
		case int64:
			*d = strconv.FormatInt(s, 10)
			return nil
		case float64:
			*d = strconv.FormatFloat(s, 'g', -1, 64)
			return nil
		case bool:
			*d = strconv.FormatBool(s)
			return nil
		}
	case *[]byte:
		if src == nil {
			*d = nil
			return nil
		}
		switch s := src.(type) {
		case []byte:
			*d = slices.Clone(s)
			return nil
		case string:
			*d = []byte(s)
			return nil
		}
	case *int:
		if src == nil {
			return errors.New("converting NULL to int is unsupported")
		}
		switch s := src.(type) {
		case int64:
			if s < math.MinInt || s > math.MaxInt {
				return fmt.Errorf("int64 %d overflows int", s)
			}
			*d = int(s)
			return nil
		case float64:
			*d = int(s)
			return nil
		case string:
			n, err := strconv.Atoi(s)
			if err != nil {
				return err
			}
			*d = n
			return nil
		}
	case *int64:
		if src == nil {
			return errors.New("converting NULL to int64 is unsupported")
		}
		switch s := src.(type) {
		case int64:
			*d = s
			return nil
		case float64:
			*d = int64(s)
			return nil
		case string:
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return err
			}
			*d = n
			return nil
		}
	case *float64:
		if src == nil {
			return errors.New("converting NULL to float64 is unsupported")
		}
		switch s := src.(type) {
		case float64:
			*d = s
			return nil
		case int64:
			*d = float64(s)
			return nil
		case string:
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return err
			}
			*d = f
			return nil
		}
	case *bool:
		if src == nil {
			return errors.New("converting NULL to bool is unsupported")
		}
		switch s := src.(type) {
		case bool:
			*d = s
			return nil
		case int64:
			*d = s != 0
			return nil
		}
	}
	if scanner, ok := dest.(sql.Scanner); ok {
		return scanner.Scan(src)
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("destination not a pointer")
	}
	elem := rv.Elem()
	if src == nil {
		if elem.Kind() == reflect.Pointer || elem.Kind() == reflect.Slice || elem.Kind() == reflect.Interface {
			elem.SetZero()
			return nil
		}
		return fmt.Errorf("converting NULL to %s is unsupported", elem.Type())
	}
	sv := reflect.ValueOf(src)
	if sv.Type().AssignableTo(elem.Type()) {
		elem.Set(sv)
		return nil
	}
	if sv.Type().ConvertibleTo(elem.Type()) {
		elem.Set(sv.Convert(elem.Type()))
		return nil
	}
	return fmt.Errorf("unsupported Scan, storing driver.Value type %T into type %T", src, dest)
}

type ndSqliteTransaction struct {
	exec  ndSqlExecutor
	scope *ndTxScope
}

func (t *ndSqliteTransaction) Exec(sqlText string) error {
	if !t.scope.active {
		return fmt.Errorf("SQLite transaction handle is no longer active")
	}
	return t.exec.execRaw(sqlText)
}

func (t *ndSqliteTransaction) Run(query string, params ...SqliteValue) error {
	if !t.scope.active {
		return fmt.Errorf("SQLite transaction handle is no longer active")
	}
	return t.exec.runRaw(query, params...)
}

func (t *ndSqliteTransaction) Query(query string, params ...SqliteValue) (SqliteRows, error) {
	if !t.scope.active {
		return nil, fmt.Errorf("SQLite transaction handle is no longer active")
	}
	return t.exec.queryRaw(query, params...)
}

// NodeSqliteDatabase implements SqliteDatabase using pure-Go modernc.org/sqlite.
type NodeSqliteDatabase struct {
	mu     sync.Mutex
	db     *sql.DB
	conn   *sql.Conn
	cache  *ndStmtCache
	closed bool
}

// NewNodeSqliteDatabase wraps an open *sql.DB on a dedicated serialized connection.
func NewNodeSqliteDatabase(db *sql.DB) (*NodeSqliteDatabase, error) {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	return &NodeSqliteDatabase{
		db:    db,
		conn:  conn,
		cache: ndNewStmtCache(),
	}, nil
}

// Exec runs raw SQL without bindings.
func (d *NodeSqliteDatabase) Exec(sqlText string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("NodeSqliteDatabase is closed")
	}
	return (&ndSqlExecutor{conn: d.conn, cache: d.cache, ctx: context.Background()}).execRaw(sqlText)
}

// Run executes one SQL statement with positional parameters.
func (d *NodeSqliteDatabase) Run(query string, params ...SqliteValue) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("NodeSqliteDatabase is closed")
	}
	return (&ndSqlExecutor{conn: d.conn, cache: d.cache, ctx: context.Background()}).runRaw(query, params...)
}

// Query executes one SQL query and returns rows.
func (d *NodeSqliteDatabase) Query(query string, params ...SqliteValue) (SqliteRows, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, fmt.Errorf("NodeSqliteDatabase is closed")
	}
	return (&ndSqlExecutor{conn: d.conn, cache: d.cache, ctx: context.Background()}).queryRaw(query, params...)
}

// Transaction executes callback atomically inside BEGIN IMMEDIATE ... COMMIT/ROLLBACK.
func (d *NodeSqliteDatabase) Transaction(callback func(tx SqliteExecutor) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("NodeSqliteDatabase is closed")
	}
	exec := ndSqlExecutor{conn: d.conn, cache: d.cache, ctx: context.Background()}
	if err := exec.execRaw("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	scope := &ndTxScope{active: true}
	tx := &ndSqliteTransaction{exec: exec, scope: scope}
	cbErr := callback(tx)
	scope.active = false
	if cbErr != nil {
		if rbErr := exec.execRaw("ROLLBACK"); rbErr != nil {
			return fmt.Errorf("SQLite transaction failed and rollback failed: %w", errors.Join(cbErr, rbErr))
		}
		return cbErr
	}
	return exec.execRaw("COMMIT")
}

// Close truncates the WAL if active and closes all prepared statements, connection, and database.
func (d *NodeSqliteDatabase) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	d.cache.closeAll()
	var checkpointErr error
	if _, err := d.conn.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		checkpointErr = err
	}
	connErr := d.conn.Close()
	dbErr := d.db.Close()
	if checkpointErr != nil && connErr == nil && dbErr == nil {
		// PRAGMA wal_checkpoint can fail on non-WAL handles like :memory: or read-only modes if closed early; ignore unless db/conn failed?
		// Keep same finally-like semantics if db closed cleanly, wait: in TS try { wal_checkpoint } finally { close() } propagates checkpointErr.
		return checkpointErr
	}
	return errors.Join(checkpointErr, connErr, dbErr)
}

// OpenNodeSqliteDatabase opens and configures a pure-Go modernc.org/sqlite database facade.
func OpenNodeSqliteDatabase(path string, options ...NodeSqliteStorageOptions) (*NodeSqliteDatabase, error) {
	checkpointPages := ndDefaultWalAutoCheckpointPages
	busyTimeoutMs := ndDefaultBusyTimeoutMs
	if len(options) > 0 {
		if options[0].WalAutoCheckpointPages != nil {
			checkpointPages = *options[0].WalAutoCheckpointPages
		}
		if options[0].BusyTimeoutMs != nil {
			busyTimeoutMs = *options[0].BusyTimeoutMs
		}
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	adapter, err := NewNodeSqliteDatabase(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	setup := []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMs),
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		fmt.Sprintf("PRAGMA wal_autocheckpoint = %d", checkpointPages),
	}
	for _, stmt := range setup {
		if err := adapter.Exec(stmt); err != nil {
			_ = adapter.Close()
			return nil, err
		}
	}
	return adapter, nil
}

// OpenNodeSqliteStorage opens or creates file-backed durable storage using pure-Go modernc.org/sqlite.
func OpenNodeSqliteStorage(path string, options ...NodeSqliteStorageOptions) (*SqliteStorage, error) {
	db, err := OpenNodeSqliteDatabase(path, options...)
	if err != nil {
		return nil, err
	}
	return OpenSqliteStorage(db)
}
