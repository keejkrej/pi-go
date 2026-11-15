// Ported from packages/durable/test/sqlite-migrations.test.ts (pi v1.0.0).

package sqlite_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func smtDatabasePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "storage.sqlite")
}

func smtQueryVersion(t *testing.T, db sqlite.SqliteDatabase) int {
	t.Helper()
	rows, err := db.Query("SELECT version FROM durable_schema WHERE singleton = 1")
	if err != nil {
		t.Fatalf("query version failed: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("expected version row, err=%v", rows.Err())
	}
	var v int
	if err := rows.Scan(&v); err != nil {
		t.Fatalf("scan version failed: %v", err)
	}
	return v
}

func smtQueryInt(t *testing.T, db sqlite.SqliteDatabase, sqlText string) int64 {
	t.Helper()
	rows, err := db.Query(sqlText)
	if err != nil {
		t.Fatalf("query %q failed: %v", sqlText, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("expected row for %q, err=%v", sqlText, rows.Err())
	}
	var v int64
	if err := rows.Scan(&v); err != nil {
		t.Fatalf("scan int failed: %v", err)
	}
	return v
}

func smtQueryString(t *testing.T, db sqlite.SqliteDatabase, sqlText string) string {
	t.Helper()
	rows, err := db.Query(sqlText)
	if err != nil {
		t.Fatalf("query %q failed: %v", sqlText, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("expected row for %q, err=%v", sqlText, rows.Err())
	}
	var s string
	if err := rows.Scan(&s); err != nil {
		t.Fatalf("scan string failed: %v", err)
	}
	return s
}

func TestSqliteMigrations(t *testing.T) {
	ctx := chord.BackgroundContext

	t.Run("creates the current schema and can be applied repeatedly", func(t *testing.T) {
		db, err := sqlite.OpenNodeSqliteDatabase(smtDatabasePath(t))
		if err != nil {
			t.Fatalf("open db failed: %v", err)
		}
		defer func() { _ = db.Close() }()

		if err := sqlite.ApplySqliteMigrations(db); err != nil {
			t.Fatalf("first migration failed: %v", err)
		}
		if err := sqlite.ApplySqliteMigrations(db); err != nil {
			t.Fatalf("repeat migration failed: %v", err)
		}
		if got := smtQueryVersion(t, db); got != sqlite.CurrentSqliteSchemaVersion {
			t.Fatalf("expected version %d, got %d", sqlite.CurrentSqliteSchemaVersion, got)
		}
		rows, err := db.Query("SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1")
		if err != nil {
			t.Fatalf("query metadata failed: %v", err)
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			t.Fatalf("expected metadata row")
		}
		var nextId string
		var nextSeq int64
		if err := rows.Scan(&nextId, &nextSeq); err != nil {
			t.Fatalf("scan metadata failed: %v", err)
		}
		if nextId != "2" || nextSeq != 1 {
			t.Fatalf("expected next_id='2', next_seq=1; got next_id=%q, next_seq=%d", nextId, nextSeq)
		}
	})

	t.Run("rejects a database newer than the portable core", func(t *testing.T) {
		path := smtDatabasePath(t)
		db, err := sqlite.OpenNodeSqliteDatabase(path)
		if err != nil {
			t.Fatalf("open db failed: %v", err)
		}
		if err := sqlite.ApplySqliteMigrations(db); err != nil {
			t.Fatalf("apply migrations failed: %v", err)
		}
		if err := db.Run("UPDATE durable_schema SET version = ? WHERE singleton = 1", sqlite.CurrentSqliteSchemaVersion+1); err != nil {
			t.Fatalf("bump version failed: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close db failed: %v", err)
		}

		_, err = sqlite.OpenNodeSqliteStorage(path)
		if err == nil || !strings.Contains(err.Error(), "is newer than supported version") {
			t.Fatalf("expected newer-version error, got %v", err)
		}
	})

	t.Run("rolls initial bootstrap and every pending migration back together", func(t *testing.T) {
		db, err := sqlite.OpenNodeSqliteDatabase(smtDatabasePath(t))
		if err != nil {
			t.Fatalf("open db failed: %v", err)
		}
		defer func() { _ = db.Close() }()

		failed := []sqlite.SqliteMigration{
			{
				Version: 1,
				Statements: []string{
					"CREATE TABLE migration_first (value TEXT) STRICT",
					"INSERT INTO migration_first (value) VALUES ('retained')",
				},
			},
			{
				Version:    2,
				Statements: []string{"CREATE TABLE migration_second (value TEXT) STRICT", "THIS IS NOT SQL"},
			},
		}
		if err := sqlite.ApplySqliteMigrations(db, failed); err == nil {
			t.Fatalf("expected invalid SQL migration to fail")
		}
		if got := smtQueryInt(t, db, "SELECT count(*) FROM sqlite_schema WHERE name IN ('durable_schema', 'migration_first', 'migration_second')"); got != 0 {
			t.Fatalf("expected rollback of all schema changes, count=%d", got)
		}

		fixed := []sqlite.SqliteMigration{
			failed[0],
			{Version: 2, Statements: []string{"CREATE TABLE migration_second (value TEXT) STRICT"}},
		}
		if err := sqlite.ApplySqliteMigrations(db, fixed); err != nil {
			t.Fatalf("fixed migration failed: %v", err)
		}
		if got := smtQueryVersion(t, db); got != 2 {
			t.Fatalf("expected version 2, got %d", got)
		}
		if got := smtQueryString(t, db, "SELECT value FROM migration_first"); got != "retained" {
			t.Fatalf("expected 'retained', got %q", got)
		}
	})

	t.Run("rolls a failed migration back and preserves stored data for a successful retry", func(t *testing.T) {
		path := smtDatabasePath(t)
		storage, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("open storage failed: %v", err)
		}
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteConversation{
				Type:  durable.StorageWriteTypeConversation,
				Value: durable.ConversationRecord{Id: durable.RootConversationId},
			},
			durable.StorageWriteEntry{
				Type: durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{
					Id:             durable.IdFromNumber[durable.EntryId](2),
					ConversationId: durable.RootConversationId,
					Kind:           "retained",
					Data:           jsonx.ObjectOf("retained", true),
				},
			},
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}

		db, err := sqlite.OpenNodeSqliteDatabase(path)
		if err != nil {
			t.Fatalf("reopen db failed: %v", err)
		}
		defer func() { _ = db.Close() }()

		nextVersion := sqlite.CurrentSqliteSchemaVersion + 1
		failedMigrations := append(append([]sqlite.SqliteMigration(nil), sqlite.SqliteMigrations...), sqlite.SqliteMigration{
			Version:    nextVersion,
			Statements: []string{"CREATE TABLE migration_probe (value TEXT) STRICT", "THIS IS NOT SQL"},
		})
		if err := sqlite.ApplySqliteMigrations(db, failedMigrations); err == nil {
			t.Fatalf("expected failed migration error")
		}
		if got := smtQueryVersion(t, db); got != sqlite.CurrentSqliteSchemaVersion {
			t.Fatalf("expected version %d, got %d", sqlite.CurrentSqliteSchemaVersion, got)
		}
		if got := smtQueryInt(t, db, "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'migration_probe'"); got != 0 {
			t.Fatalf("expected migration_probe rolled back, count=%d", got)
		}

		successMigrations := append(append([]sqlite.SqliteMigration(nil), sqlite.SqliteMigrations...), sqlite.SqliteMigration{
			Version:    nextVersion,
			Statements: []string{"CREATE TABLE migration_probe (value TEXT) STRICT"},
		})
		if err := sqlite.ApplySqliteMigrations(db, successMigrations); err != nil {
			t.Fatalf("successful retry failed: %v", err)
		}
		if got := smtQueryVersion(t, db); got != nextVersion {
			t.Fatalf("expected version %d, got %d", nextVersion, got)
		}

		rows, err := db.Query("SELECT record, commit_seq FROM entries WHERE id = 2")
		if err != nil {
			t.Fatalf("query entries failed: %v", err)
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			t.Fatalf("expected retained entry row")
		}
		var rawRecord string
		var commitSeq int64
		if err := rows.Scan(&rawRecord, &commitSeq); err != nil {
			t.Fatalf("scan retained entry failed: %v", err)
		}
		if commitSeq != 1 {
			t.Fatalf("expected commit_seq=1, got %d", commitSeq)
		}
		parsed, err := jsonx.ParseString(rawRecord)
		if err != nil {
			t.Fatalf("parse retained record failed: %v", err)
		}
		wantRecord := jsonx.ObjectOf(
			"id", float64(2),
			"conversationId", float64(durable.RootConversationId),
			"kind", "retained",
			"data", jsonx.ObjectOf("retained", true),
		)
		if !jsonx.DeepEqual(parsed, wantRecord) {
			t.Fatalf("expected retained record %#v, got %#v", wantRecord, parsed)
		}

		metaRows, err := db.Query("SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1")
		if err != nil {
			t.Fatalf("query metadata failed: %v", err)
		}
		defer func() { _ = metaRows.Close() }()
		if !metaRows.Next() {
			t.Fatalf("expected metadata row")
		}
		var nextId string
		var nextSeq int64
		if err := metaRows.Scan(&nextId, &nextSeq); err != nil {
			t.Fatalf("scan metadata failed: %v", err)
		}
		if nextId != "3" || nextSeq != 2 {
			t.Fatalf("expected next_id='3', next_seq=2; got next_id=%q, next_seq=%d", nextId, nextSeq)
		}
	})
}
