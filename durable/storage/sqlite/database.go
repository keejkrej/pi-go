// Ported from packages/durable/src/storage/sqlite/database.ts (pi v1.0.0).

package sqlite

// SqliteValue documents the scalar parameter types supported by the portable SQLite storage core:
// nil, int, int64, float64, string, []byte, or bool.
type SqliteValue = any

// SqliteRows iterates rows returned by SqliteExecutor.Query, matching *sql.Rows.
type SqliteRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// SqliteExecutor defines synchronous SQL operations shared by a database and its transaction handles.
//
// Exec runs SQL text without bindings and may contain several statements. Run and Query execute
// one statement with positional bindings.
type SqliteExecutor interface {
	Exec(sql string) error
	Run(sql string, params ...SqliteValue) error
	Query(sql string, params ...SqliteValue) (SqliteRows, error)
}

// SqliteDatabase is the minimal database facade required by SqliteStorage.
//
// Transaction passes a transaction handle to callback. All work inside callback must use that
// handle; the handle becomes invalid once callback returns. Adapters must serialize competing
// operations until the active transaction commits or rolls back.
//
// If callback returns an error, Transaction must roll back before returning that error; if
// rollback itself fails, it must return an error wrapping both so callers do not mistake the
// callback error for a guaranteed rollback.
type SqliteDatabase interface {
	SqliteExecutor
	Transaction(callback func(tx SqliteExecutor) error) error
	Close() error
}

func dbQueryOne[T any](
	exec SqliteExecutor,
	query string,
	scan func(SqliteRows) (T, error),
	params ...SqliteValue,
) (T, bool, error) {
	var zero T
	rows, err := exec.Query(query, params...)
	if err != nil {
		return zero, false, err
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, false, err
		}
		return zero, false, nil
	}
	val, err := scan(rows)
	if err != nil {
		return zero, false, err
	}
	if err := rows.Err(); err != nil {
		return zero, false, err
	}
	return val, true, nil
}

func dbQueryAll[T any](
	exec SqliteExecutor,
	query string,
	scan func(SqliteRows) (T, error),
	params ...SqliteValue,
) ([]T, error) {
	rows, err := exec.Query(query, params...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []T
	for rows.Next() {
		val, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, val)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
