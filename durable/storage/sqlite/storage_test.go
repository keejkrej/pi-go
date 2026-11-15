// Ported from packages/durable/test/sqlite-storage.test.ts (pi v1.0.0).

package sqlite_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/durabletest"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	_ "modernc.org/sqlite"
)

type sstReopeningStorage struct {
	mu      sync.Mutex
	current *sqlite.SqliteStorage
	path    string
	options []sqlite.NodeSqliteStorageOptions
	closed  bool
}

func sstNewReopeningStorage(path string, options ...sqlite.NodeSqliteStorageOptions) (*sstReopeningStorage, error) {
	cur, err := sqlite.OpenNodeSqliteStorage(path, options...)
	if err != nil {
		return nil, err
	}
	return &sstReopeningStorage{current: cur, path: path, options: options}, nil
}

func (r *sstReopeningStorage) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.current.Commit(ctx, writes)
	}
	seq, err := r.current.Commit(ctx, writes)
	_ = r.current.Close(chord.BackgroundContext)
	reopened, reopenErr := sqlite.OpenNodeSqliteStorage(r.path, r.options...)
	if reopenErr != nil {
		if err != nil {
			return 0, err
		}
		return 0, reopenErr
	}
	r.current = reopened
	return seq, err
}

func (r *sstReopeningStorage) MintId() (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.MintId()
}
func (r *sstReopeningStorage) Conversation(ctx chord.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Conversation(ctx, id)
}
func (r *sstReopeningStorage) ScanConversations(ctx chord.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanConversations(ctx, query, limit, cursor)
}
func (r *sstReopeningStorage) Entry(ctx chord.Context, id durable.EntryId) (*durable.StoredEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Entry(ctx, id)
}
func (r *sstReopeningStorage) EntryInConversation(ctx chord.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.StoredEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.EntryInConversation(ctx, conversationId, id)
}
func (r *sstReopeningStorage) FindLatestHeadMarker(ctx chord.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId) (*durable.EntryRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
}
func (r *sstReopeningStorage) ScanEntries(ctx chord.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanEntries(ctx, query, limit, cursor)
}
func (r *sstReopeningStorage) Task(ctx chord.Context, id durable.TaskId) (*durable.TaskRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Task(ctx, id)
}
func (r *sstReopeningStorage) ScanTasks(ctx chord.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanTasks(ctx, query, limit, cursor)
}
func (r *sstReopeningStorage) Submission(ctx chord.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Submission(ctx, id)
}
func (r *sstReopeningStorage) ScanSubmissions(ctx chord.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanSubmissions(ctx, query, limit, cursor)
}
func (r *sstReopeningStorage) SubmissionByRequest(ctx chord.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.SubmissionByRequest(ctx, conversationId, requestId)
}
func (r *sstReopeningStorage) FindDocument(ctx chord.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.FindDocument(ctx, address, at)
}
func (r *sstReopeningStorage) Document(ctx chord.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Document(ctx, id, at)
}
func (r *sstReopeningStorage) ScanDocuments(ctx chord.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanDocuments(ctx, query, limit, cursor)
}
func (r *sstReopeningStorage) Close(ctx chord.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.current.Close(ctx)
}

func sstCreateStorage(t *testing.T, opts ...sqlite.NodeSqliteStorageOptions) (*sqlite.SqliteStorage, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "storage.sqlite")
	store, err := sqlite.OpenNodeSqliteStorage(path, opts...)
	if err != nil {
		t.Fatalf("OpenNodeSqliteStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(chord.BackgroundContext) })
	return store, path
}

func sstOpenRawDB(t *testing.T, path string, readOnly bool) *sql.DB {
	t.Helper()
	dsn := path
	if readOnly {
		dsn = fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", path)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw sqlite failed: %v", err)
	}
	if !readOnly {
		_, _ = db.Exec("PRAGMA busy_timeout = 5000")
	}
	return db
}

func sstScalarInt(t *testing.T, db *sql.DB, sqlText string, args ...any) int64 {
	t.Helper()
	var val int64
	if err := db.QueryRow(sqlText, args...).Scan(&val); err != nil {
		t.Fatalf("scalar query %q failed: %v", sqlText, err)
	}
	return val
}

func sstRevisionCount(t *testing.T, path string, docId durable.DocumentId) int64 {
	t.Helper()
	db := sstOpenRawDB(t, path, true)
	defer func() { _ = db.Close() }()
	return sstScalarInt(t, db, "SELECT count(*) FROM document_revisions WHERE document_id = ?", int64(docId))
}

func sstEntry(id durable.EntryId, convId durable.ConversationId, data durable.JsonValue) durable.EntryRecord {
	return durable.EntryRecord{
		Id:             id,
		ConversationId: convId,
		Kind:           "message",
		Data:           data,
	}
}

func sstPendingTask(id durable.TaskId) durable.TaskRecord {
	return durable.TaskRecord{
		Id:             id,
		ConversationId: durable.RootConversationId,
		Kind:           "test.task",
		Version:        1,
		Input:          nil,
		State: durable.TaskState{
			Status:     durable.TaskStatePending,
			Checkpoint: jsonx.ObjectOf("phase", "ready"),
		},
	}
}

func sstCreateRoot(t *testing.T, store durable.Storage) durable.Seq {
	t.Helper()
	seq, err := store.Commit(chord.BackgroundContext, []durable.StorageWrite{
		durable.StorageWriteConversation{
			Type:  durable.StorageWriteTypeConversation,
			Value: durable.ConversationRecord{Id: durable.RootConversationId},
		},
	})
	if err != nil {
		t.Fatalf("root commit failed: %v", err)
	}
	return seq
}

func TestSqliteStorageConformance(t *testing.T) {
	ctx := chord.BackgroundContext

	durabletest.RegisterStorageConformance(t, "SqliteStorage", func(use func(durable.Storage) error) error {
		dir, err := os.MkdirTemp("", "pi-durable-sqlite-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		store, err := sqlite.OpenNodeSqliteStorage(filepath.Join(dir, "storage.sqlite"))
		if err != nil {
			return err
		}
		defer func() { _ = store.Close(ctx) }()
		return use(store)
	})

	durabletest.RegisterStorageConformance(t, "SqliteStorage across reopen", func(use func(durable.Storage) error) error {
		dir, err := os.MkdirTemp("", "pi-durable-sqlite-reopen-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		path := filepath.Join(dir, "storage.sqlite")
		created, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			return err
		}
		if err := created.Close(ctx); err != nil {
			return err
		}
		reopening, err := sstNewReopeningStorage(path)
		if err != nil {
			return err
		}
		defer func() { _ = reopening.Close(ctx) }()
		return use(reopening)
	})
}

func TestSqliteStorage(t *testing.T) {
	ctx := chord.BackgroundContext

	t.Run("persists records, sequence allocation, and global ID allocation across reopen", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		if seq := sstCreateRoot(t, storage); seq != 1 {
			t.Fatalf("expected seq 1, got %d", seq)
		}
		rawEntryId, err := storage.MintId()
		if err != nil {
			t.Fatalf("MintId failed: %v", err)
		}
		entryId := durable.IdFromNumber[durable.EntryId](rawEntryId)
		seq2, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: sstEntry(entryId, durable.RootConversationId, nil)},
		})
		if err != nil || seq2 != 2 {
			t.Fatalf("commit failed: seq=%d err=%v", seq2, err)
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}

		reopened, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("reopen failed: %v", err)
		}
		defer func() { _ = reopened.Close(ctx) }()

		stored, err := reopened.Entry(ctx, entryId)
		if err != nil || stored == nil {
			t.Fatalf("Entry failed: stored=%v err=%v", stored, err)
		}
		if stored.CommitSeq != 2 || !jsonx.DeepEqual(stored.Entry, sstEntry(entryId, durable.RootConversationId, nil)) {
			t.Fatalf("unexpected stored entry: %+v", stored)
		}
		nextId, err := reopened.MintId()
		if err != nil || nextId != int64(entryId)+1 {
			t.Fatalf("expected nextId %d, got %d err=%v", int64(entryId)+1, nextId, err)
		}
		taskRaw, err := reopened.MintId()
		if err != nil {
			t.Fatalf("MintId failed: %v", err)
		}
		seq3, err := reopened.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{
				Type:  durable.StorageWriteTypeTask,
				Value: sstPendingTask(durable.IdFromNumber[durable.TaskId](taskRaw)),
			},
		})
		if err != nil || seq3 != 3 {
			t.Fatalf("expected seq 3, got %d err=%v", seq3, err)
		}
	})

	t.Run("rejects persisted metadata corruption on reopen", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}
		raw := sstOpenRawDB(t, path, false)
		_, err := raw.Exec("DELETE FROM durable_metadata")
		_ = raw.Close()
		if err != nil {
			t.Fatalf("delete metadata failed: %v", err)
		}

		_, err = sqlite.OpenNodeSqliteStorage(path)
		if err == nil || !strings.Contains(err.Error(), "Durable SQLite metadata is missing") {
			t.Fatalf("expected missing metadata error, got %v", err)
		}
	})

	t.Run("rejects a document whose required base is missing", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		sstCreateRoot(t, storage)
		rawDocId, err := storage.MintId()
		if err != nil {
			t.Fatalf("MintId failed: %v", err)
		}
		docId := durable.IdFromNumber[durable.DocumentId](rawDocId)
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:    docId,
					Kind:  "corrupt",
					Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
				},
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("retained", true),
				},
			},
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}

		raw := sstOpenRawDB(t, path, false)
		_, delErr := raw.Exec("DELETE FROM document_revisions WHERE document_id = ?", int64(docId))
		_ = raw.Close()
		if delErr != nil {
			t.Fatalf("delete revisions failed: %v", delErr)
		}

		_, err = storage.Document(ctx, docId, durable.DocumentPointCurrent())
		wantMsg := fmt.Sprintf("Document %d is missing a required base", docId)
		if err == nil || !strings.Contains(err.Error(), wantMsg) {
			t.Fatalf("expected error %q, got %v", wantMsg, err)
		}
	})

	t.Run("replays detached root replacements and follow-up edits while rejecting corrupt operations", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		sstCreateRoot(t, storage)
		rawDocId, err := storage.MintId()
		if err != nil {
			t.Fatalf("MintId failed: %v", err)
		}
		docId := durable.IdFromNumber[durable.DocumentId](rawDocId)

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:    docId,
					Kind:  "replay",
					Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
				},
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("nested", jsonx.ObjectOf("value", float64(1)), "rows", []any{}),
				},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops: []chord.Op{
						chord.OpReplace{Value: jsonx.ObjectOf(
							"nested", jsonx.ObjectOf("value", float64(2)),
							"rows", []any{jsonx.ObjectOf("id", float64(1))},
						)},
					},
				},
			},
		}); err != nil {
			t.Fatalf("replace op failed: %v", err)
		}
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops: []chord.Op{
						chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("nested"), chord.SegString("value")}, Value: float64(3)},
						chord.OpSplice{Path: chord.NonEmptyPath{chord.SegString("rows")}, Index: 1, Remove: 0, Items: []chord.JsonValue{jsonx.ObjectOf("id", float64(2))}},
						chord.OpMove{Path: chord.NonEmptyPath{chord.SegString("rows")}, Permutation: []int{1, 0}},
					},
				},
			},
		}); err != nil {
			t.Fatalf("delta batch failed: %v", err)
		}
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops: []chord.Op{
						chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("nested"), chord.SegString("value")}, Value: float64(4)},
					},
				},
			},
		}); err != nil {
			t.Fatalf("delta 4 failed: %v", err)
		}

		expected := jsonx.ObjectOf(
			"nested", jsonx.ObjectOf("value", float64(4)),
			"rows", []any{jsonx.ObjectOf("id", float64(2)), jsonx.ObjectOf("id", float64(1))},
		)
		first, err := storage.Document(ctx, docId, durable.DocumentPointCurrent())
		if err != nil || first == nil {
			t.Fatalf("Document read failed: %+v err=%v", first, err)
		}
		if !jsonx.DeepEqual(first.Value, expected) {
			t.Fatalf("expected %#v, got %#v", expected, first.Value)
		}
		nestedAny, _ := first.Value.Get("nested")
		nestedAny.(*jsonx.Object).Set("value", float64(99))
		rowsAny, _ := first.Value.Get("rows")
		rowsAny.([]any)[0].(*jsonx.Object).Set("id", float64(99))

		second, err := storage.Document(ctx, docId, durable.DocumentPointCurrent())
		if err != nil || second == nil || !jsonx.DeepEqual(second.Value, expected) {
			t.Fatalf("expected uncorrupted %#v, got %+v err=%v", expected, second, err)
		}

		raw := sstOpenRawDB(t, path, false)
		_, upErr := raw.Exec(
			`UPDATE document_revisions SET content = ? WHERE document_id = ? AND seq = (SELECT max(seq) FROM document_revisions WHERE document_id = ?)`,
			`[["unknown"]]`, int64(docId), int64(docId),
		)
		_ = raw.Close()
		if upErr != nil {
			t.Fatalf("corrupt update failed: %v", upErr)
		}
		_, err = storage.Document(ctx, docId, durable.DocumentPointCurrent())
		if err == nil || !strings.Contains(err.Error(), "unknown op verb") {
			t.Fatalf("expected unknown op verb error, got %v", err)
		}
	})

	t.Run("rolls SQL rows and sequence allocation back as one transaction", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		sstCreateRoot(t, storage)
		rawTransientEntry, _ := storage.MintId()
		transientEntryId := durable.IdFromNumber[durable.EntryId](rawTransientEntry)
		rawTransientTask, _ := storage.MintId()
		transientTaskId := durable.IdFromNumber[durable.TaskId](rawTransientTask)

		circular := jsonx.NewObject()
		circular.Set("self", circular)
		badTask := sstPendingTask(transientTaskId)
		badTask.Input = circular

		_, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: sstEntry(transientEntryId, durable.RootConversationId, nil)},
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: badTask},
		})
		if err == nil || !strings.Contains(err.Error(), "circular structure") {
			t.Fatalf("expected circular structure error, got %v", err)
		}
		if got, err := storage.Entry(ctx, transientEntryId); err != nil || got != nil {
			t.Fatalf("expected transient entry rolled back, got %+v err=%v", got, err)
		}
		if got, err := storage.Task(ctx, transientTaskId); err != nil || got != nil {
			t.Fatalf("expected transient task rolled back, got %+v err=%v", got, err)
		}

		rawCommittedId, _ := storage.MintId()
		committedId := durable.IdFromNumber[durable.EntryId](rawCommittedId)
		seq, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: sstEntry(committedId, durable.RootConversationId, nil)},
		})
		if err != nil || seq != 2 {
			t.Fatalf("expected seq 2, got %d err=%v", seq, err)
		}

		raw := sstOpenRawDB(t, path, true)
		defer func() { _ = raw.Close() }()
		if got := sstScalarInt(t, raw, "SELECT count(*) FROM entries"); got != 1 {
			t.Fatalf("expected 1 entry row, got %d", got)
		}
		if got := sstScalarInt(t, raw, "SELECT next_seq FROM durable_metadata WHERE singleton = 1"); got != 3 {
			t.Fatalf("expected next_seq=3, got %d", got)
		}
	})

	t.Run("reconstructs recent and ancient rewindable points after reopen", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		sstCreateRoot(t, storage)
		rawDocId, _ := storage.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDocId)

		createdAt, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:      docId,
					Kind:    "history",
					Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: durable.RootConversationId},
					History: durable.DocumentHistoryRewindable,
					Fork:    durable.DocumentForkAsOf,
				},
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("count", float64(0)),
				},
			},
		})
		if err != nil {
			t.Fatalf("create failed: %v", err)
		}

		ancientAt := createdAt
		recentAt := createdAt
		for count := 1; count <= 40; count++ {
			var content durable.DocumentContent
			if count == 20 {
				content = durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("count", float64(count)),
				}
			} else {
				content = durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(count)}},
				}
			}
			recentAt, err = storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      docId,
					Content: content,
				},
			})
			if err != nil {
				t.Fatalf("change %d failed: %v", count, err)
			}
			if count == 5 {
				ancientAt = recentAt
			}
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}

		reopened, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("reopen failed: %v", err)
		}
		defer func() { _ = reopened.Close(ctx) }()

		ancientDoc, err := reopened.Document(ctx, docId, durable.DocumentPointSeq(ancientAt))
		if err != nil || ancientDoc == nil || !jsonx.DeepEqual(ancientDoc.Value, jsonx.ObjectOf("count", float64(5))) {
			t.Fatalf("unexpected ancient doc: %+v err=%v", ancientDoc, err)
		}
		recentDoc, err := reopened.Document(ctx, docId, durable.DocumentPointSeq(recentAt))
		if err != nil || recentDoc == nil || !jsonx.DeepEqual(recentDoc.Value, jsonx.ObjectOf("count", float64(40))) {
			t.Fatalf("unexpected recent doc: %+v err=%v", recentDoc, err)
		}
	})

	t.Run("uses indexes for exact addresses, exact scopes, entry history, and document revision tails", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}
		db := sstOpenRawDB(t, path, true)
		defer func() { _ = db.Close() }()

		explain := func(query string, args ...any) string {
			rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
			if err != nil {
				t.Fatalf("explain %q failed: %v", query, err)
			}
			defer func() { _ = rows.Close() }()
			var details []string
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					t.Fatalf("scan explain failed: %v", err)
				}
				details = append(details, detail)
			}
			return strings.Join(details, "\n")
		}

		d0 := explain(`SELECT record FROM documents WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ? AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`, "kind", "session", 0, 0, "")
		d1 := explain(`SELECT record FROM documents WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ? AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?) ORDER BY created_at DESC LIMIT 1`, "kind", "conversation", 1, 0, "", 10, 10)
		d2 := explain(`SELECT record FROM documents WHERE scope_kind = ? AND owner_id = ? AND kind = ? AND id > ? ORDER BY id LIMIT ?`, "task", 1, "kind", 0, 10)
		d3 := explain(`SELECT record FROM entries WHERE conversation_id = ? AND id <= ? ORDER BY id DESC LIMIT ?`, 1, 10, 10)
		d4 := explain(`SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1`, 1, 10)
		d5 := explain(`SELECT record FROM tasks WHERE status = ? AND id > ? ORDER BY id LIMIT ?`, "pending", 0, 10)
		d6 := explain(`SELECT seq, kind, version, content FROM document_revisions WHERE document_id = ? AND kind = 'base' AND seq <= ? ORDER BY seq DESC LIMIT 1`, 1, 10)
		d7 := explain(`SELECT seq, kind, version, content FROM document_revisions WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`, 1, 5, 10)

		checks := []struct {
			detail, want, forbid string
		}{
			{d0, "documents_by_address", "SCAN documents"},
			{d1, "documents_by_address", "SCAN documents"},
			{d2, "documents_by_scope_kind", "SCAN documents"},
			{d3, "entries_by_conversation", ""},
			{d4, "entry_heads_by_conversation", ""},
			{d5, "tasks_by_status", ""},
			{d6, "document_revisions_by_kind", ""},
			{d7, "sqlite_autoindex_document_revisions_1", "SCAN document_revisions"},
		}
		for i, c := range checks {
			if !strings.Contains(c.detail, c.want) {
				t.Fatalf("plan[%d] = %q, expected to contain %q", i, c.detail, c.want)
			}
			if c.forbid != "" && strings.Contains(c.detail, c.forbid) {
				t.Fatalf("plan[%d] = %q, unexpectedly contains %q", i, c.detail, c.forbid)
			}
		}
	})

	t.Run("reclaims current-only revisions only after a base or retirement", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		sstCreateRoot(t, storage)
		rawDocId, _ := storage.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDocId)

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:    docId,
					Kind:  "latest",
					Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
				},
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("count", float64(0)),
				},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		for count := 1; count <= 10; count++ {
			if _, err := storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   docId,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 1,
						Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(count)}},
					},
				},
			}); err != nil {
				t.Fatalf("delta %d failed: %v", count, err)
			}
		}
		if got := sstRevisionCount(t, path, docId); got != 11 {
			t.Fatalf("expected 11 revisions, got %d", got)
		}
		raw := sstOpenRawDB(t, path, true)
		var contentStr string
		if err := raw.QueryRow("SELECT content FROM document_revisions WHERE document_id = ? AND kind = 'delta' ORDER BY seq DESC LIMIT 1", int64(docId)).Scan(&contentStr); err != nil {
			_ = raw.Close()
			t.Fatalf("query revision content failed: %v", err)
		}
		_ = raw.Close()
		parsed, err := jsonx.ParseString(contentStr)
		if err != nil {
			t.Fatalf("parse revision content failed: %v", err)
		}
		if !jsonx.DeepEqual(parsed, []any{[]any{"s", []any{"count"}, float64(10)}}) {
			t.Fatalf("unexpected delta content %#v", parsed)
		}

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("count", float64(11)),
				},
			},
		}); err != nil {
			t.Fatalf("base change failed: %v", err)
		}
		if got := sstRevisionCount(t, path, docId); got != 1 {
			t.Fatalf("expected 1 revision after base, got %d", got)
		}

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops:     []chord.Op{chord.OpReplace{Value: jsonx.ObjectOf("count", float64(12))}},
				},
			},
		}); err != nil {
			t.Fatalf("replace change failed: %v", err)
		}
		if got := sstRevisionCount(t, path, docId); got != 2 {
			t.Fatalf("expected 2 revisions, got %d", got)
		}

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: docId},
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}
		if got := sstRevisionCount(t, path, docId); got != 0 {
			t.Fatalf("expected 0 revisions after retire, got %d", got)
		}
	})

	t.Run("auto-checkpoints WAL frames and truncates the WAL on close", func(t *testing.T) {
		pages := 1
		storage, path := sstCreateStorage(t, sqlite.NodeSqliteStorageOptions{WalAutoCheckpointPages: &pages})
		sstCreateRoot(t, storage)
		pad := strings.Repeat("x", 32*1024)
		for index := 0; index < 20; index++ {
			id, _ := storage.MintId()
			if _, err := storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: sstEntry(durable.IdFromNumber[durable.EntryId](id), durable.RootConversationId,
						jsonx.ObjectOf("text", pad, "index", float64(index))),
				},
			}); err != nil {
				t.Fatalf("commit %d failed: %v", index, err)
			}
		}

		walPath := path + "-wal"
		info, err := os.Stat(walPath)
		if err != nil {
			t.Fatalf("stat wal failed: %v", err)
		}
		if info.Size() >= 512*1024 {
			t.Fatalf("expected WAL < 512KiB with auto-checkpoint=1, got %d", info.Size())
		}

		observer := sstOpenRawDB(t, path, true)
		defer func() { _ = observer.Close() }()
		if got := sstScalarInt(t, observer, "SELECT count(*) FROM entries"); got != 20 {
			t.Fatalf("expected 20 entries in observer, got %d", got)
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}
		afterInfo, err := os.Stat(walPath)
		if err == nil && afterInfo.Size() != 0 {
			t.Fatalf("expected WAL truncated to 0 bytes, got %d", afterInfo.Size())
		}
	})

	t.Run("reuses pages released by current-only checkpoints", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		sstCreateRoot(t, storage)
		rawDocId, _ := storage.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDocId)
		large := strings.Repeat("x", 512*1024)

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:    docId,
					Kind:  "reuse",
					Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
				},
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("text", large),
				},
			},
		}); err != nil {
			t.Fatalf("large create failed: %v", err)
		}
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("text", "small"),
				},
			},
		}); err != nil {
			t.Fatalf("shrink change failed: %v", err)
		}

		before := sstOpenRawDB(t, path, true)
		pagesAfterDelete := sstScalarInt(t, before, "SELECT page_count FROM pragma_page_count()")
		freeAfterDelete := sstScalarInt(t, before, "SELECT freelist_count FROM pragma_freelist_count()")
		_ = before.Close()
		if freeAfterDelete <= 0 {
			t.Fatalf("expected freelist_count > 0, got %d", freeAfterDelete)
		}

		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("text", large),
				},
			},
		}); err != nil {
			t.Fatalf("large rewrite failed: %v", err)
		}

		after := sstOpenRawDB(t, path, true)
		pagesAfterReuse := sstScalarInt(t, after, "SELECT page_count FROM pragma_page_count()")
		freeAfterReuse := sstScalarInt(t, after, "SELECT freelist_count FROM pragma_freelist_count()")
		_ = after.Close()
		if pagesAfterReuse > pagesAfterDelete+2 {
			t.Fatalf("expected page reuse (<= %d), got %d", pagesAfterDelete+2, pagesAfterReuse)
		}
		if freeAfterReuse >= freeAfterDelete {
			t.Fatalf("expected freelist_count < %d, got %d", freeAfterDelete, freeAfterReuse)
		}
	})

	t.Run("keeps representative row and document storage bounded", func(t *testing.T) {
		storage, path := sstCreateStorage(t)
		sstCreateRoot(t, storage)
		pad := strings.Repeat("x", 1024)
		for index := 0; index < 100; index++ {
			id, _ := storage.MintId()
			if _, err := storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: sstEntry(durable.IdFromNumber[durable.EntryId](id), durable.RootConversationId,
						jsonx.ObjectOf("index", float64(index), "text", pad)),
				},
			}); err != nil {
				t.Fatalf("entry %d failed: %v", index, err)
			}
		}

		rawDocId, _ := storage.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDocId)
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:      docId,
					Kind:    "size.history",
					Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: durable.RootConversationId},
					History: durable.DocumentHistoryRewindable,
					Fork:    durable.DocumentForkAsOf,
				},
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("count", float64(0)),
				},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		for count := 1; count <= 100; count++ {
			if _, err := storage.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   docId,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 1,
						Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(count)}},
					},
				},
			}); err != nil {
				t.Fatalf("delta %d failed: %v", count, err)
			}
		}
		if err := storage.Close(ctx); err != nil {
			t.Fatalf("close failed: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat db failed: %v", err)
		}
		if info.Size() >= 1024*1024 {
			t.Fatalf("expected DB size < 1MiB, got %d", info.Size())
		}
	})
}
