// Ported from packages/durable/src/storage/sqlite/storage.ts (pi v1.0.0).

package sqlite

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type sqTableName string

const (
	sqTableConversation sqTableName = "conversation"
	sqTableEntry        sqTableName = "entry"
	sqTableTask         sqTableName = "task"
	sqTableSubmission   sqTableName = "submission"
	sqTableDocument     sqTableName = "document"
)

type sqDocRevisionRow struct {
	seq     durable.Seq
	kind    durable.DocumentContentKind
	version int
	content string
}

type sqDocAction struct {
	create     *durable.DocumentCreate
	copySrc    *durable.DocumentCopySource
	hasContent bool
	content    durable.DocumentContent
	retire     bool
}

type sqScopeColumns struct {
	scopeKind durable.DocumentScopeKind
	ownerId   int64
}

type sqAddressParts struct {
	kind      string
	scopeKind durable.DocumentScopeKind
	ownerId   int64
	family    int
	keyValue  string
}

func sqParseJson[T any](text string) (T, error) {
	var out T
	err := jsonx.Unmarshal([]byte(text), &out)
	return out, err
}

func sqEncodeJson(val any) (string, error) {
	return jsonx.Stringify(val)
}

// sqEncodeIndexedString losslessly preserves UTF-16 code units across SQLite string bindings.
func sqEncodeIndexedString(val string) (string, error) {
	return jsonx.Stringify(val)
}

func sqCursorId[I ~int64](cursor durable.Cursor) (I, bool, error) {
	if cursor == nil {
		return 0, false, nil
	}
	after, ok := cursor.Get("after")
	if !ok || after == nil {
		return 0, false, nil
	}
	num, ok := after.(float64)
	if !ok || math.IsNaN(num) || math.IsInf(num, 0) || math.Trunc(num) != num {
		return 0, false, fmt.Errorf("TypeError: Invalid storage cursor")
	}
	return durable.IdFromNumber[I](int64(num)), true, nil
}

func sqPage[T any, I ~int64](values []T, limit int, idOf func(T) I) durable.Page[T, durable.Cursor] {
	if limit <= 0 {
		return durable.Page[T, durable.Cursor]{Items: []T{}}
	}
	if len(values) <= limit {
		if values == nil {
			values = []T{}
		}
		return durable.Page[T, durable.Cursor]{Items: values}
	}
	items := values[:limit]
	return durable.Page[T, durable.Cursor]{
		Items: items,
		Next:  jsonx.ObjectOf("after", float64(idOf(items[len(items)-1]))),
	}
}

func sqScopeCols(scope durable.DocumentScope) (sqScopeColumns, error) {
	switch scope.Kind {
	case durable.DocumentScopeSession:
		return sqScopeColumns{scopeKind: durable.DocumentScopeSession, ownerId: 0}, nil
	case durable.DocumentScopeConversation:
		return sqScopeColumns{scopeKind: durable.DocumentScopeConversation, ownerId: int64(scope.ConversationId)}, nil
	case durable.DocumentScopeTask:
		return sqScopeColumns{scopeKind: durable.DocumentScopeTask, ownerId: int64(scope.TaskId)}, nil
	default:
		return sqScopeColumns{}, fmt.Errorf("Unsupported document scope %q", scope.Kind)
	}
}

func sqBuildAddressParts(kind string, scope durable.DocumentScope, key *string) (sqAddressParts, error) {
	sc, err := sqScopeCols(scope)
	if err != nil {
		return sqAddressParts{}, err
	}
	encKind, err := sqEncodeIndexedString(kind)
	if err != nil {
		return sqAddressParts{}, err
	}
	family := 0
	rawKey := ""
	if key != nil {
		family = 1
		rawKey = *key
	}
	encKey, err := sqEncodeIndexedString(rawKey)
	if err != nil {
		return sqAddressParts{}, err
	}
	return sqAddressParts{
		kind:      encKind,
		scopeKind: sc.scopeKind,
		ownerId:   sc.ownerId,
		family:    family,
		keyValue:  encKey,
	}, nil
}

func sqAddressKeyFromParts(p sqAddressParts) (string, error) {
	return jsonx.Stringify([]any{p.kind, string(p.scopeKind), float64(p.ownerId), float64(p.family), p.keyValue})
}

func sqIsAliveAt(rec durable.DocumentRecord, at durable.DocumentPoint) bool {
	if at.IsCurrent() {
		return rec.RetiredAt == nil
	}
	seq, ok := at.Seq()
	if !ok || rec.CreatedAt > seq {
		return false
	}
	return rec.RetiredAt == nil || seq < *rec.RetiredAt
}

func sqIsCurrentOnly(rec durable.DocumentRecord) bool {
	return rec.Scope.Kind != durable.DocumentScopeConversation || rec.History == durable.DocumentHistoryLatest
}

func sqWriteId(w durable.StorageWrite) (int64, bool) {
	switch cw := w.(type) {
	case durable.StorageWriteConversation:
		return int64(cw.Value.Id), true
	case *durable.StorageWriteConversation:
		return int64(cw.Value.Id), true
	case durable.StorageWriteEntry:
		return int64(cw.Value.Id), true
	case *durable.StorageWriteEntry:
		return int64(cw.Value.Id), true
	case durable.StorageWriteTask:
		return int64(cw.Value.Id), true
	case *durable.StorageWriteTask:
		return int64(cw.Value.Id), true
	case durable.StorageWriteSubmission:
		return int64(cw.Value.Id), true
	case *durable.StorageWriteSubmission:
		return int64(cw.Value.Id), true
	case durable.StorageWriteDocumentCreate:
		return int64(cw.Record.Id), true
	case *durable.StorageWriteDocumentCreate:
		return int64(cw.Record.Id), true
	case durable.StorageWriteDocumentCopy:
		return int64(cw.Record.Id), true
	case *durable.StorageWriteDocumentCopy:
		return int64(cw.Record.Id), true
	default:
		return 0, false
	}
}

func sqNormalizeWrite(w durable.StorageWrite) (durable.StorageWrite, error) {
	switch cw := w.(type) {
	case *durable.StorageWriteConversation:
		return *cw, nil
	case *durable.StorageWriteEntry:
		return *cw, nil
	case *durable.StorageWriteTask:
		return *cw, nil
	case *durable.StorageWriteSubmission:
		return *cw, nil
	case *durable.StorageWriteDocumentCreate:
		return *cw, nil
	case *durable.StorageWriteDocumentCopy:
		return *cw, nil
	case *durable.StorageWriteDocumentChange:
		return *cw, nil
	case *durable.StorageWriteDocumentRetire:
		return *cw, nil
	case durable.StorageWriteConversation,
		durable.StorageWriteEntry,
		durable.StorageWriteTask,
		durable.StorageWriteSubmission,
		durable.StorageWriteDocumentCreate,
		durable.StorageWriteDocumentCopy,
		durable.StorageWriteDocumentChange,
		durable.StorageWriteDocumentRetire:
		return cw, nil
	default:
		return nil, fmt.Errorf("Unsupported storage write %T", w)
	}
}

// SqliteStorage implements durable.Storage over a portable SqliteDatabase facade.
type SqliteStorage struct {
	mu            sync.Mutex
	db            SqliteDatabase
	nextId        int64
	closed        bool
	closing       bool
	closeDone     chan struct{}
	closeErr      error
	admittedReads int
	readsDrained  chan struct{}
}

// OpenSqliteStorage runs pending migrations and initializes SqliteStorage over db.
// If initialization fails, db is closed before returning the error.
func OpenSqliteStorage(db SqliteDatabase) (*SqliteStorage, error) {
	if err := ApplySqliteMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	nextIdText, ok, err := dbQueryOne(db, "SELECT next_id FROM durable_metadata WHERE singleton = 1", func(r SqliteRows) (string, error) {
		var s string
		err := r.Scan(&s)
		return s, err
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if !ok {
		_ = db.Close()
		return nil, fmt.Errorf("Durable SQLite metadata is missing")
	}
	nextId, err := strconv.ParseInt(nextIdText, 10, 64)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("Invalid durable_metadata.next_id %q: %w", nextIdText, err)
	}
	return &SqliteStorage{
		db:     db,
		nextId: nextId,
	}, nil
}

// Commit validates and applies writes atomically in a single SQLite transaction.
func (s *SqliteStorage) Commit(_ chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	if err := s.assertOpen(); err != nil {
		return 0, err
	}
	normWrites := make([]durable.StorageWrite, len(writes))
	for i, w := range writes {
		nw, err := sqNormalizeWrite(w)
		if err != nil {
			return 0, err
		}
		normWrites[i] = nw
	}
	docActions, err := sqPrepareDocumentActions(normWrites)
	if err != nil {
		return 0, err
	}
	candidateNextId := s.candidateNextId(normWrites)

	var committedSeq durable.Seq
	if err := s.db.Transaction(func(tx SqliteExecutor) error {
		nextIdText, nextSeq, ok, err := sqReadMetadata(tx)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("Durable SQLite metadata is missing")
		}
		committedSeq = durable.SeqFromNumber(nextSeq)
		if err := s.checkGlobalIds(tx, normWrites); err != nil {
			return err
		}
		if err := s.checkDocumentActions(tx, docActions); err != nil {
			return err
		}
		for _, w := range normWrites {
			if err := s.applyTableWrite(tx, w, committedSeq); err != nil {
				return err
			}
		}
		if err := s.applyDocumentActions(tx, docActions, committedSeq); err != nil {
			return err
		}
		dbNextId, err := strconv.ParseInt(nextIdText, 10, 64)
		if err != nil {
			return err
		}
		updatedNextId := dbNextId
		if candidateNextId > updatedNextId {
			updatedNextId = candidateNextId
		}
		return tx.Run(
			"UPDATE durable_metadata SET next_id = ?, next_seq = ? WHERE singleton = 1",
			strconv.FormatInt(updatedNextId, 10),
			int64(committedSeq)+1,
		)
	}); err != nil {
		return 0, err
	}

	s.mu.Lock()
	if candidateNextId > s.nextId {
		s.nextId = candidateNextId
	}
	s.mu.Unlock()
	return committedSeq, nil
}

// MintId returns a monotonically increasing ID greater than prior allocated/committed IDs.
func (s *SqliteStorage) MintId() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("SqliteStorage is closed")
	}
	if s.nextId > 9007199254740991 {
		return 0, fmt.Errorf("ID space is exhausted")
	}
	id := s.nextId
	s.nextId++
	return id, nil
}

// Conversation fetches one conversation record by ID.
func (s *SqliteStorage) Conversation(_ chord.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	recJson, ok, err := dbQueryOne(s.db, "SELECT record FROM conversations WHERE id = ?", sqScanString, int64(id))
	if err != nil || !ok {
		return nil, err
	}
	rec, err := sqParseJson[durable.ConversationRecord](recJson)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// ScanConversations lists matching conversations in ascending ID order.
func (s *SqliteStorage) ScanConversations(
	_ chord.Context,
	query durable.ConversationQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	if err := s.assertOpen(); err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	after, hasAfter, err := sqCursorId[durable.ConversationId](cursor)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	minId := int64(-1)
	if hasAfter {
		minId = int64(after)
	}
	clauses := []string{"id > ?"}
	params := []SqliteValue{minId}
	if query.OwnerConversationId != nil {
		clauses = append(clauses, "owner_conversation_id = ?")
		params = append(params, int64(*query.OwnerConversationId))
	}
	if query.OwnerTaskId != nil {
		clauses = append(clauses, "owner_task_id = ?")
		params = append(params, int64(*query.OwnerTaskId))
	}
	fetchLimit := 0
	if limit > 0 {
		fetchLimit = limit + 1
	}
	params = append(params, fetchLimit)
	rawRows, err := dbQueryAll(
		s.db,
		fmt.Sprintf("SELECT record FROM conversations WHERE %s ORDER BY id LIMIT ?", strings.Join(clauses, " AND ")),
		sqScanString,
		params...,
	)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	values := make([]durable.ConversationRecord, len(rawRows))
	for i, raw := range rawRows {
		rec, err := sqParseJson[durable.ConversationRecord](raw)
		if err != nil {
			return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
		}
		values[i] = rec
	}
	return sqPage(values, limit, func(c durable.ConversationRecord) durable.ConversationId { return c.Id }), nil
}

// Entry fetches a transcript entry by global ID along with its commit sequence.
func (s *SqliteStorage) Entry(_ chord.Context, id durable.EntryId) (*durable.StoredEntry, error) {
	return sqAdmitRead(s, func() (*durable.StoredEntry, error) {
		return s.readEntry(nil, id)
	})
}

// EntryInConversation fetches entry id if visible from conversationId's fork-aware history.
func (s *SqliteStorage) EntryInConversation(_ chord.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.StoredEntry, error) {
	return sqAdmitRead(s, func() (*durable.StoredEntry, error) {
		return s.readEntry(&conversationId, id)
	})
}

// FindLatestHeadMarker returns the newest visible entry with a Head marker at or before atOrBeforeEntryId.
func (s *SqliteStorage) FindLatestHeadMarker(
	_ chord.Context,
	conversationId durable.ConversationId,
	atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	return sqAdmitRead(s, func() (*durable.EntryRecord, error) {
		return s.readLatestHeadMarker(conversationId, atOrBeforeEntryId)
	})
}

// ScanEntries lists fork-aware entries for a conversation in descending ID order.
func (s *SqliteStorage) ScanEntries(
	_ chord.Context,
	query durable.EntryQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return sqAdmitRead(s, func() (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		return s.readEntries(query, limit, cursor)
	})
}

func (s *SqliteStorage) readEntry(conversationId *durable.ConversationId, id durable.EntryId) (*durable.StoredEntry, error) {
	var conv *durable.ConversationRecord
	if conversationId != nil {
		c, err := s.readConversation(*conversationId)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, fmt.Errorf("Unknown conversation: %d", *conversationId)
		}
		conv = c
	}
	row, ok, err := dbQueryOne(s.db, "SELECT record, commit_seq FROM entries WHERE id = ?", func(r SqliteRows) (struct {
		record string
		seq    int64
	}, error) {
		var out struct {
			record string
			seq    int64
		}
		err := r.Scan(&out.record, &out.seq)
		return out, err
	}, int64(id))
	if err != nil || !ok {
		return nil, err
	}
	entry, err := sqParseJson[durable.EntryRecord](row.record)
	if err != nil {
		return nil, err
	}
	if conv != nil {
		upper := durable.EntryId(math.MaxInt64)
		for conv.Id != entry.ConversationId {
			if conv.Parent == nil {
				return nil, nil
			}
			if conv.Parent.At < upper {
				upper = conv.Parent.At
			}
			parent, err := s.readConversation(conv.Parent.ConversationId)
			if err != nil {
				return nil, err
			}
			if parent == nil {
				return nil, nil
			}
			conv = parent
		}
		if entry.Id > upper {
			return nil, nil
		}
	}
	return &durable.StoredEntry{
		Entry:     entry,
		CommitSeq: durable.SeqFromNumber(row.seq),
	}, nil
}

func (s *SqliteStorage) readLatestHeadMarker(
	conversationId durable.ConversationId,
	atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	conv, err := s.readConversation(conversationId)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, fmt.Errorf("Unknown conversation: %d", conversationId)
	}
	upper := atOrBeforeEntryId
	for {
		var recJson string
		var ok bool
		if upper == nil {
			recJson, ok, err = dbQueryOne(
				s.db,
				"SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL ORDER BY id DESC LIMIT 1",
				sqScanString,
				int64(conv.Id),
			)
		} else {
			recJson, ok, err = dbQueryOne(
				s.db,
				"SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1",
				sqScanString,
				int64(conv.Id),
				int64(*upper),
			)
		}
		if err != nil {
			return nil, err
		}
		if ok {
			entry, err := sqParseJson[durable.EntryRecord](recJson)
			if err != nil {
				return nil, err
			}
			return &entry, nil
		}
		if conv.Parent == nil {
			return nil, nil
		}
		nextUpper := conv.Parent.At
		if upper != nil && *upper < nextUpper {
			nextUpper = *upper
		}
		upper = &nextUpper
		conv, err = s.readConversation(conv.Parent.ConversationId)
		if err != nil {
			return nil, err
		}
		if conv == nil {
			return nil, nil
		}
	}
}

func (s *SqliteStorage) readEntries(
	query durable.EntryQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	conv, err := s.readConversation(query.ConversationId)
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	if conv == nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, fmt.Errorf("Unknown conversation: %d", query.ConversationId)
	}
	after, hasAfter, err := sqCursorId[durable.EntryId](cursor)
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	upper := query.MaxEntryId
	if hasAfter {
		below := after - 1
		if upper == nil || below < *upper {
			upper = &below
		}
	}
	if limit <= 0 {
		return durable.Page[durable.EntryRecord, durable.Cursor]{Items: []durable.EntryRecord{}}, nil
	}
	var values []durable.EntryRecord
	for {
		clauses := []string{"conversation_id = ?"}
		params := []SqliteValue{int64(conv.Id)}
		if query.MinEntryId != nil {
			clauses = append(clauses, "id >= ?")
			params = append(params, int64(*query.MinEntryId))
		}
		if upper != nil {
			clauses = append(clauses, "id <= ?")
			params = append(params, int64(*upper))
		}
		params = append(params, limit+1-len(values))
		rawRows, err := dbQueryAll(
			s.db,
			fmt.Sprintf("SELECT record FROM entries WHERE %s ORDER BY id DESC LIMIT ?", strings.Join(clauses, " AND ")),
			sqScanString,
			params...,
		)
		if err != nil {
			return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
		}
		for _, raw := range rawRows {
			entry, err := sqParseJson[durable.EntryRecord](raw)
			if err != nil {
				return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
			}
			values = append(values, entry)
		}
		if len(values) > limit || conv.Parent == nil {
			break
		}
		nextUpper := conv.Parent.At
		if upper != nil && *upper < nextUpper {
			nextUpper = *upper
		}
		upper = &nextUpper
		if query.MinEntryId != nil && *upper < *query.MinEntryId {
			break
		}
		conv, err = s.readConversation(conv.Parent.ConversationId)
		if err != nil {
			return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
		}
		if conv == nil {
			break
		}
	}
	return sqPage(values, limit, func(e durable.EntryRecord) durable.EntryId { return e.Id }), nil
}

// Task fetches a task record by ID.
func (s *SqliteStorage) Task(_ chord.Context, id durable.TaskId) (*durable.TaskRecord, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	recJson, ok, err := dbQueryOne(s.db, "SELECT record FROM tasks WHERE id = ?", sqScanString, int64(id))
	if err != nil || !ok {
		return nil, err
	}
	rec, err := sqParseJson[durable.TaskRecord](recJson)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// ScanTasks lists matching tasks in ascending ID order.
func (s *SqliteStorage) ScanTasks(
	_ chord.Context,
	query durable.TaskQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.TaskRecord, durable.Cursor], error) {
	if err := s.assertOpen(); err != nil {
		return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
	}
	after, hasAfter, err := sqCursorId[durable.TaskId](cursor)
	if err != nil {
		return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
	}
	minId := int64(-1)
	if hasAfter {
		minId = int64(after)
	}
	clauses := []string{"id > ?"}
	params := []SqliteValue{minId}
	if query.ConversationId != nil {
		clauses = append(clauses, "conversation_id = ?")
		params = append(params, int64(*query.ConversationId))
	}
	if query.Kind != nil {
		encKind, err := sqEncodeIndexedString(*query.Kind)
		if err != nil {
			return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
		}
		clauses = append(clauses, "kind = ?")
		params = append(params, encKind)
	}
	if query.Status != "" {
		clauses = append(clauses, "status = ?")
		params = append(params, string(query.Status))
	}
	if query.AbortRequested != nil {
		v := 0
		if *query.AbortRequested {
			v = 1
		}
		clauses = append(clauses, "abort_requested = ?")
		params = append(params, v)
	}
	if query.Background != nil {
		v := 0
		if *query.Background {
			v = 1
		}
		clauses = append(clauses, "background = ?")
		params = append(params, v)
	}
	fetchLimit := 0
	if limit > 0 {
		fetchLimit = limit + 1
	}
	params = append(params, fetchLimit)
	rawRows, err := dbQueryAll(
		s.db,
		fmt.Sprintf("SELECT record FROM tasks WHERE %s ORDER BY id LIMIT ?", strings.Join(clauses, " AND ")),
		sqScanString,
		params...,
	)
	if err != nil {
		return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
	}
	values := make([]durable.TaskRecord, len(rawRows))
	for i, raw := range rawRows {
		rec, err := sqParseJson[durable.TaskRecord](raw)
		if err != nil {
			return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
		}
		values[i] = rec
	}
	return sqPage(values, limit, func(t durable.TaskRecord) durable.TaskId { return t.Id }), nil
}

// Submission fetches a submission record by ID.
func (s *SqliteStorage) Submission(_ chord.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	recJson, ok, err := dbQueryOne(s.db, "SELECT record FROM submissions WHERE id = ?", sqScanString, int64(id))
	if err != nil || !ok {
		return nil, err
	}
	rec, err := sqParseJson[durable.SubmissionRecord](recJson)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// ScanSubmissions lists matching submissions in ascending ID order.
func (s *SqliteStorage) ScanSubmissions(
	_ chord.Context,
	query durable.SubmissionQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if err := s.assertOpen(); err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	after, hasAfter, err := sqCursorId[durable.SubmissionId](cursor)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	minId := int64(-1)
	if hasAfter {
		minId = int64(after)
	}
	clauses := []string{"id > ?"}
	params := []SqliteValue{minId}
	if query.ConversationId != nil {
		clauses = append(clauses, "conversation_id = ?")
		params = append(params, int64(*query.ConversationId))
	}
	if query.Status != "" {
		clauses = append(clauses, "status = ?")
		params = append(params, string(query.Status))
	}
	fetchLimit := 0
	if limit > 0 {
		fetchLimit = limit + 1
	}
	params = append(params, fetchLimit)
	rawRows, err := dbQueryAll(
		s.db,
		fmt.Sprintf("SELECT record FROM submissions WHERE %s ORDER BY id LIMIT ?", strings.Join(clauses, " AND ")),
		sqScanString,
		params...,
	)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	values := make([]durable.SubmissionRecord, len(rawRows))
	for i, raw := range rawRows {
		rec, err := sqParseJson[durable.SubmissionRecord](raw)
		if err != nil {
			return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
		}
		values[i] = rec
	}
	return sqPage(values, limit, func(sub durable.SubmissionRecord) durable.SubmissionId { return sub.Id }), nil
}

// SubmissionByRequest finds a submission carrying conversationId and requestId.
func (s *SqliteStorage) SubmissionByRequest(
	_ chord.Context,
	conversationId durable.ConversationId,
	requestId string,
) (*durable.SubmissionRecord, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	encReq, err := sqEncodeIndexedString(requestId)
	if err != nil {
		return nil, err
	}
	recJson, ok, err := dbQueryOne(
		s.db,
		"SELECT record FROM submissions WHERE conversation_id = ? AND request_id = ?",
		sqScanString,
		int64(conversationId),
		encReq,
	)
	if err != nil || !ok {
		return nil, err
	}
	rec, err := sqParseJson[durable.SubmissionRecord](recJson)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// FindDocument locates the document record at address alive at point at.
func (s *SqliteStorage) FindDocument(_ chord.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	parts, err := sqBuildAddressParts(address.Kind, address.Scope, address.Key)
	if err != nil {
		return nil, err
	}
	var sqlText string
	params := []SqliteValue{parts.kind, string(parts.scopeKind), parts.ownerId, parts.family, parts.keyValue}
	if at.IsCurrent() {
		sqlText = `SELECT record FROM documents
			WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?
			AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`
	} else {
		seq, ok := at.Seq()
		if !ok {
			return nil, nil
		}
		sqlText = `SELECT record FROM documents
			WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?
			AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?)
			ORDER BY created_at DESC LIMIT 1`
		params = append(params, int64(seq), int64(seq))
	}
	recJson, ok, err := dbQueryOne(s.db, sqlText, sqScanString, params...)
	if err != nil || !ok {
		return nil, err
	}
	rec, err := sqParseJson[durable.DocumentRecord](recJson)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Document materializes the stored document value for id at point at.
func (s *SqliteStorage) Document(_ chord.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	var out *durable.StoredDocument
	err := s.db.Transaction(func(tx SqliteExecutor) error {
		doc, e := s.materializeDocument(tx, id, at)
		if e != nil {
			return e
		}
		out = doc
		return nil
	})
	return out, err
}

// ScanDocuments lists matching documents alive at query.At in ascending ID order.
func (s *SqliteStorage) ScanDocuments(
	_ chord.Context,
	query durable.DocumentQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	if err := s.assertOpen(); err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	scope, err := sqScopeCols(query.Scope)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	after, hasAfter, err := sqCursorId[durable.DocumentId](cursor)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	minId := int64(-1)
	if hasAfter {
		minId = int64(after)
	}
	clauses := []string{"scope_kind = ?", "owner_id = ?", "id > ?"}
	params := []SqliteValue{string(scope.scopeKind), scope.ownerId, minId}
	if query.Kind != nil {
		encKind, err := sqEncodeIndexedString(*query.Kind)
		if err != nil {
			return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
		}
		clauses = append(clauses, "kind = ?")
		params = append(params, encKind)
	}
	if query.At.IsCurrent() {
		clauses = append(clauses, "retired_at IS NULL")
	} else {
		seq, _ := query.At.Seq()
		clauses = append(clauses, "created_at <= ?", "(retired_at IS NULL OR retired_at > ?)")
		params = append(params, int64(seq), int64(seq))
	}
	fetchLimit := 0
	if limit > 0 {
		fetchLimit = limit + 1
	}
	params = append(params, fetchLimit)
	rawRows, err := dbQueryAll(
		s.db,
		fmt.Sprintf("SELECT record FROM documents WHERE %s ORDER BY id LIMIT ?", strings.Join(clauses, " AND ")),
		sqScanString,
		params...,
	)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	values := make([]durable.DocumentRecord, len(rawRows))
	for i, raw := range rawRows {
		rec, err := sqParseJson[durable.DocumentRecord](raw)
		if err != nil {
			return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
		}
		values[i] = rec
	}
	return sqPage(values, limit, func(d durable.DocumentRecord) durable.DocumentId { return d.Id }), nil
}

// Close waits for in-flight admitted multi-query reads and closes the underlying SQLite database.
func (s *SqliteStorage) Close(_ chord.Context) error {
	s.mu.Lock()
	if s.closing {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return s.closeErr
	}
	s.closed = true
	s.closing = true
	done := make(chan struct{})
	s.closeDone = done
	var waitCh chan struct{}
	if s.admittedReads > 0 {
		waitCh = make(chan struct{})
		s.readsDrained = waitCh
	}
	s.mu.Unlock()

	if waitCh != nil {
		<-waitCh
	}
	err := s.db.Close()
	s.closeErr = err
	close(done)
	return err
}

func (s *SqliteStorage) assertOpen() error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("SqliteStorage is closed")
	}
	return nil
}

func sqAdmitRead[T any](s *SqliteStorage, fn func() (T, error)) (T, error) {
	var zero T
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return zero, fmt.Errorf("SqliteStorage is closed")
	}
	s.admittedReads++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.admittedReads--
		if s.admittedReads == 0 && s.readsDrained != nil {
			close(s.readsDrained)
			s.readsDrained = nil
		}
		s.mu.Unlock()
	}()

	return fn()
}

func (s *SqliteStorage) readConversation(id durable.ConversationId) (*durable.ConversationRecord, error) {
	recJson, ok, err := dbQueryOne(s.db, "SELECT record FROM conversations WHERE id = ?", sqScanString, int64(id))
	if err != nil || !ok {
		return nil, err
	}
	rec, err := sqParseJson[durable.ConversationRecord](recJson)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *SqliteStorage) materializeDocument(
	exec SqliteExecutor,
	id durable.DocumentId,
	at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	recJson, ok, err := dbQueryOne(exec, "SELECT record FROM documents WHERE id = ?", sqScanString, int64(id))
	if err != nil || !ok {
		return nil, err
	}
	rec, err := sqParseJson[durable.DocumentRecord](recJson)
	if err != nil {
		return nil, err
	}
	isCurrent := at.IsCurrent()
	if !isCurrent && sqIsCurrentOnly(rec) {
		return nil, fmt.Errorf("Document %d does not retain historical content", id)
	}
	if !sqIsAliveAt(rec, at) {
		return nil, nil
	}
	upper := int64(math.MaxInt64)
	if seq, hasSeq := at.Seq(); !isCurrent && hasSeq {
		upper = int64(seq)
	}
	base, hasBase, err := dbQueryOne(
		exec,
		`SELECT seq, kind, version, content FROM document_revisions
			WHERE document_id = ? AND kind = 'base' AND seq <= ? ORDER BY seq DESC LIMIT 1`,
		sqScanRevisionRow,
		int64(id),
		upper,
	)
	if err != nil {
		return nil, err
	}
	if !hasBase {
		return nil, fmt.Errorf("Document %d is missing a required base", id)
	}
	val, err := sqParseJson[durable.JsonObject](base.content)
	if err != nil {
		return nil, err
	}
	tail, err := dbQueryAll(
		exec,
		`SELECT seq, kind, version, content FROM document_revisions
			WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`,
		sqScanRevisionRow,
		int64(id),
		int64(base.seq),
		upper,
	)
	if err != nil {
		return nil, err
	}
	for _, rev := range tail {
		if rev.kind != durable.DocumentContentDelta || rev.version != base.version {
			return nil, fmt.Errorf("Document %d crosses a stored version boundary without a base", id)
		}
		ops, err := sqParseJson[chord.OpList](rev.content)
		if err != nil {
			return nil, err
		}
		val, err = chord.Apply(val, ops)
		if err != nil {
			return nil, err
		}
	}
	return &durable.StoredDocument{
		Record:          rec,
		Version:         base.version,
		Value:           val,
		DeltasSinceBase: len(tail),
	}, nil
}

func (s *SqliteStorage) candidateNextId(writes []durable.StorageWrite) int64 {
	s.mu.Lock()
	nextId := s.nextId
	s.mu.Unlock()
	for _, w := range writes {
		if id, ok := sqWriteId(w); ok && id+1 > nextId {
			nextId = id + 1
		}
	}
	return nextId
}

func (s *SqliteStorage) checkGlobalIds(exec SqliteExecutor, writes []durable.StorageWrite) error {
	claimed := make(map[int64]sqTableName)
	for _, w := range writes {
		var table sqTableName
		var id int64
		switch cw := w.(type) {
		case durable.StorageWriteDocumentChange, durable.StorageWriteDocumentRetire:
			continue
		case durable.StorageWriteDocumentCreate:
			table = sqTableDocument
			id = int64(cw.Record.Id)
		case durable.StorageWriteDocumentCopy:
			table = sqTableDocument
			id = int64(cw.Record.Id)
		case durable.StorageWriteConversation:
			table = sqTableConversation
			id = int64(cw.Value.Id)
		case durable.StorageWriteEntry:
			table = sqTableEntry
			id = int64(cw.Value.Id)
		case durable.StorageWriteTask:
			table = sqTableTask
			id = int64(cw.Value.Id)
		case durable.StorageWriteSubmission:
			table = sqTableSubmission
			id = int64(cw.Value.Id)
		}
		existingRaw, hasExisting, err := dbQueryOne(exec, "SELECT record_type FROM record_ids WHERE id = ?", sqScanString, id)
		if err != nil {
			return err
		}
		existing := sqTableName(existingRaw)
		earlier, hasEarlier := claimed[id]
		if table == sqTableConversation || table == sqTableEntry || table == sqTableDocument {
			if hasExisting {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if hasEarlier {
				return fmt.Errorf("ID %d is written more than once", id)
			}
		} else {
			if hasExisting && existing != table {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if hasEarlier && earlier != table {
				return fmt.Errorf("ID %d is written as two record types", id)
			}
		}
		claimed[id] = table
	}
	return nil
}

func sqPrepareDocumentActions(writes []durable.StorageWrite) (map[durable.DocumentId]*sqDocAction, error) {
	actions := make(map[durable.DocumentId]*sqDocAction)
	for _, w := range writes {
		var id durable.DocumentId
		switch cw := w.(type) {
		case durable.StorageWriteDocumentCreate:
			id = cw.Record.Id
		case durable.StorageWriteDocumentCopy:
			id = cw.Record.Id
		case durable.StorageWriteDocumentChange:
			id = cw.Id
		case durable.StorageWriteDocumentRetire:
			id = cw.Id
		default:
			continue
		}
		act := actions[id]
		if act == nil {
			act = &sqDocAction{}
			actions[id] = act
		}
		switch cw := w.(type) {
		case durable.StorageWriteDocumentCreate:
			if act.create != nil || act.hasContent || act.copySrc != nil {
				return nil, fmt.Errorf("Document %d has more than one content command", id)
			}
			rec := cw.Record
			act.create = &rec
			act.hasContent = true
			act.content = cw.Content
		case durable.StorageWriteDocumentCopy:
			if act.create != nil || act.hasContent || act.copySrc != nil {
				return nil, fmt.Errorf("Document %d has more than one content command", id)
			}
			rec := cw.Record
			src := cw.Source
			act.create = &rec
			act.copySrc = &src
		case durable.StorageWriteDocumentChange:
			if act.hasContent || act.copySrc != nil {
				return nil, fmt.Errorf("Document %d has more than one content command", id)
			}
			act.hasContent = true
			act.content = cw.Content
		case durable.StorageWriteDocumentRetire:
			if act.retire {
				return nil, fmt.Errorf("Document %d is retired more than once", id)
			}
			act.retire = true
		}
	}
	return actions, nil
}

func (s *SqliteStorage) checkDocumentActions(exec SqliteExecutor, actions map[durable.DocumentId]*sqDocAction) error {
	liveCounts := make(map[string]int)
	for id, act := range actions {
		if act.copySrc != nil {
			if _, changed := actions[act.copySrc.Id]; changed {
				return durable.NewStorageRejected(
					fmt.Sprintf("Document copy %d source is changed in the copy batch", id),
					nil,
				)
			}
		}
		recJson, hasRow, err := dbQueryOne(exec, "SELECT record FROM documents WHERE id = ?", sqScanString, int64(id))
		if err != nil {
			return err
		}
		var existing *durable.DocumentRecord
		if hasRow {
			rec, err := sqParseJson[durable.DocumentRecord](recJson)
			if err != nil {
				return err
			}
			existing = &rec
		}
		if act.create == nil && existing == nil {
			return fmt.Errorf("Unknown document: %d", id)
		}
		if act.create != nil && existing != nil {
			return fmt.Errorf("Document %d already exists", id)
		}
		if existing != nil && existing.RetiredAt != nil {
			return fmt.Errorf("Document %d is retired", id)
		}
		if act.hasContent && act.content.Kind == durable.DocumentContentDelta {
			prevVer, hasPrev, err := dbQueryOne(
				exec,
				"SELECT version FROM document_revisions WHERE document_id = ? ORDER BY seq DESC LIMIT 1",
				sqScanInt,
				int64(id),
			)
			if err != nil {
				return err
			}
			if !hasPrev {
				return fmt.Errorf("Document %d delta has no base", id)
			}
			if prevVer != act.content.Version {
				return fmt.Errorf("Document %d version transition requires a base", id)
			}
		}
		var parts sqAddressParts
		if act.create != nil {
			parts, err = sqBuildAddressParts(act.create.Kind, act.create.Scope, act.create.Key)
		} else {
			parts, err = sqBuildAddressParts(existing.Kind, existing.Scope, existing.Key)
		}
		if err != nil {
			return err
		}
		addrKey, err := sqAddressKeyFromParts(parts)
		if err != nil {
			return err
		}
		live, seen := liveCounts[addrKey]
		if !seen {
			_, hasCur, err := s.currentDocumentIdFromParts(exec, parts)
			if err != nil {
				return err
			}
			if hasCur {
				live = 1
			}
		}
		if act.retire && existing != nil {
			live--
		}
		if act.create != nil && !act.retire {
			live++
		}
		liveCounts[addrKey] = live
	}
	for _, live := range liveCounts {
		if live > 1 {
			return fmt.Errorf("Document address already has a current incarnation")
		}
	}
	return nil
}

func (s *SqliteStorage) currentDocumentIdFromParts(exec SqliteExecutor, p sqAddressParts) (durable.DocumentId, bool, error) {
	idVal, ok, err := dbQueryOne(
		exec,
		`SELECT id FROM documents
			WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ? AND retired_at IS NULL
			LIMIT 1`,
		sqScanInt64,
		p.kind,
		string(p.scopeKind),
		p.ownerId,
		p.family,
		p.keyValue,
	)
	if err != nil || !ok {
		return 0, false, err
	}
	return durable.IdFromNumber[durable.DocumentId](idVal), true, nil
}

func (s *SqliteStorage) applyTableWrite(exec SqliteExecutor, w durable.StorageWrite, seq durable.Seq) error {
	switch cw := w.(type) {
	case durable.StorageWriteConversation:
		if err := s.claimId(exec, int64(cw.Value.Id), sqTableConversation); err != nil {
			return err
		}
		encoded, err := sqEncodeJson(cw.Value)
		if err != nil {
			return err
		}
		var ownerConv any
		var ownerTask any
		if cw.Value.Owner != nil {
			ownerConv = int64(cw.Value.Owner.ConversationId)
			ownerTask = int64(cw.Value.Owner.TaskId)
		}
		return exec.Run(
			"INSERT INTO conversations (id, owner_conversation_id, owner_task_id, record) VALUES (?, ?, ?, ?)",
			int64(cw.Value.Id),
			ownerConv,
			ownerTask,
			encoded,
		)

	case durable.StorageWriteEntry:
		if err := s.claimId(exec, int64(cw.Value.Id), sqTableEntry); err != nil {
			return err
		}
		encoded, err := sqEncodeJson(cw.Value)
		if err != nil {
			return err
		}
		var headVal any
		if cw.Value.Head != nil {
			headVal = int64(*cw.Value.Head)
		}
		return exec.Run(
			"INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)",
			int64(cw.Value.Id),
			int64(cw.Value.ConversationId),
			headVal,
			int64(seq),
			encoded,
		)

	case durable.StorageWriteTask:
		if err := s.claimId(exec, int64(cw.Value.Id), sqTableTask); err != nil {
			return err
		}
		encKind, err := sqEncodeIndexedString(cw.Value.Kind)
		if err != nil {
			return err
		}
		encoded, err := sqEncodeJson(cw.Value)
		if err != nil {
			return err
		}
		abortInt := 0
		if cw.Value.AbortRequested {
			abortInt = 1
		}
		bgInt := 0
		if cw.Value.Background {
			bgInt = 1
		}
		return exec.Run(
			`INSERT INTO tasks (id, conversation_id, kind, status, abort_requested, background, record)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id, kind = excluded.kind,
				status = excluded.status, abort_requested = excluded.abort_requested,
				background = excluded.background, record = excluded.record`,
			int64(cw.Value.Id),
			int64(cw.Value.ConversationId),
			encKind,
			string(cw.Value.State.Status),
			abortInt,
			bgInt,
			encoded,
		)

	case durable.StorageWriteSubmission:
		if err := s.claimId(exec, int64(cw.Value.Id), sqTableSubmission); err != nil {
			return err
		}
		var reqVal any
		if cw.Value.RequestId != nil {
			encReq, err := sqEncodeIndexedString(*cw.Value.RequestId)
			if err != nil {
				return err
			}
			reqVal = encReq
		}
		encoded, err := sqEncodeJson(cw.Value)
		if err != nil {
			return err
		}
		return exec.Run(
			`INSERT INTO submissions (id, conversation_id, request_id, status, record) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id,
				request_id = excluded.request_id, status = excluded.status, record = excluded.record`,
			int64(cw.Value.Id),
			int64(cw.Value.ConversationId),
			reqVal,
			string(cw.Value.Status),
			encoded,
		)
	}
	return nil
}

func (s *SqliteStorage) claimId(exec SqliteExecutor, id int64, table sqTableName) error {
	return exec.Run("INSERT OR IGNORE INTO record_ids (id, record_type) VALUES (?, ?)", id, string(table))
}

func (s *SqliteStorage) applyDocumentActions(
	exec SqliteExecutor,
	actions map[durable.DocumentId]*sqDocAction,
	seq durable.Seq,
) error {
	docIds := make([]durable.DocumentId, 0, len(actions))
	for id := range actions {
		docIds = append(docIds, id)
	}
	sort.Slice(docIds, func(i, j int) bool { return docIds[i] < docIds[j] })

	for _, id := range docIds {
		act := actions[id]
		hasContent := act.hasContent
		content := act.content
		if act.copySrc != nil {
			reject := func(cause error) error {
				if rej, ok := cause.(*durable.StorageRejected); ok {
					return rej
				}
				return durable.NewStorageRejected(fmt.Sprintf("Document copy %d was rejected", id), cause)
			}
			stored, err := s.materializeDocument(exec, act.copySrc.Id, act.copySrc.At)
			if err != nil {
				return reject(err)
			}
			if stored == nil {
				return reject(fmt.Errorf("Fork source document %d cannot be read", act.copySrc.Id))
			}
			create := *act.create
			if stored.Record.Scope.Kind != durable.DocumentScopeConversation ||
				create.Scope.Kind != durable.DocumentScopeConversation ||
				stored.Record.Kind != create.Kind ||
				!sqEqualStringPtr(stored.Record.Key, create.Key) ||
				stored.Record.History != create.History ||
				stored.Record.Fork != create.Fork {
				return reject(fmt.Errorf("Fork source document %d does not match the copied record", act.copySrc.Id))
			}
			hasContent = true
			content = durable.DocumentContent{
				Kind:    durable.DocumentContentBase,
				Version: stored.Version,
				Value:   stored.Value,
			}
		}

		var rec durable.DocumentRecord
		if act.create != nil {
			rec = act.create.AsRecord(seq)
			var retiredVal any
			if act.retire {
				r := seq
				rec.RetiredAt = &r
				retiredVal = int64(r)
			}
			parts, err := sqBuildAddressParts(rec.Kind, rec.Scope, rec.Key)
			if err != nil {
				return err
			}
			if err := s.claimId(exec, int64(id), sqTableDocument); err != nil {
				return err
			}
			encoded, err := sqEncodeJson(rec)
			if err != nil {
				return err
			}
			if err := exec.Run(
				`INSERT INTO documents
					(id, kind, family, key_value, scope_kind, owner_id, created_at, retired_at, record)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				int64(id),
				parts.kind,
				parts.family,
				parts.keyValue,
				string(parts.scopeKind),
				parts.ownerId,
				int64(seq),
				retiredVal,
				encoded,
			); err != nil {
				return err
			}
		} else {
			recJson, _, err := dbQueryOne(exec, "SELECT record FROM documents WHERE id = ?", sqScanString, int64(id))
			if err != nil {
				return err
			}
			rec, err = sqParseJson[durable.DocumentRecord](recJson)
			if err != nil {
				return err
			}
		}

		if hasContent {
			if content.Kind == durable.DocumentContentBase && sqIsCurrentOnly(rec) {
				if err := exec.Run("DELETE FROM document_revisions WHERE document_id = ?", int64(id)); err != nil {
					return err
				}
			}
			var encodedContent string
			var err error
			if content.Kind == durable.DocumentContentBase {
				encodedContent, err = sqEncodeJson(content.Value)
			} else {
				encodedContent, err = sqEncodeJson(content.Ops)
			}
			if err != nil {
				return err
			}
			if err := exec.Run(
				"INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)",
				int64(id),
				int64(seq),
				string(content.Kind),
				content.Version,
				encodedContent,
			); err != nil {
				return err
			}
		}

		if act.retire {
			if act.create == nil {
				r := seq
				rec.RetiredAt = &r
				encoded, err := sqEncodeJson(rec)
				if err != nil {
					return err
				}
				if err := exec.Run(
					"UPDATE documents SET retired_at = ?, record = ? WHERE id = ?",
					int64(seq),
					encoded,
					int64(id),
				); err != nil {
					return err
				}
			}
			if sqIsCurrentOnly(rec) {
				if err := exec.Run("DELETE FROM document_revisions WHERE document_id = ?", int64(id)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func sqReadMetadata(exec SqliteExecutor) (string, int64, bool, error) {
	rows, err := exec.Query("SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1")
	if err != nil {
		return "", 0, false, err
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		return "", 0, false, rows.Err()
	}
	var nextId string
	var nextSeq int64
	if err := rows.Scan(&nextId, &nextSeq); err != nil {
		return "", 0, false, err
	}
	return nextId, nextSeq, true, rows.Err()
}

func sqScanString(r SqliteRows) (string, error) {
	var s string
	err := r.Scan(&s)
	return s, err
}

func sqScanInt(r SqliteRows) (int, error) {
	var v int
	err := r.Scan(&v)
	return v, err
}

func sqScanInt64(r SqliteRows) (int64, error) {
	var v int64
	err := r.Scan(&v)
	return v, err
}

func sqScanRevisionRow(r SqliteRows) (sqDocRevisionRow, error) {
	var seqVal int64
	var kindStr string
	var ver int
	var content string
	if err := r.Scan(&seqVal, &kindStr, &ver, &content); err != nil {
		return sqDocRevisionRow{}, err
	}
	return sqDocRevisionRow{
		seq:     durable.SeqFromNumber(seqVal),
		kind:    durable.DocumentContentKind(kindStr),
		version: ver,
		content: content,
	}, nil
}

func sqEqualStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

var (
	_ durable.Storage = (*SqliteStorage)(nil)
	_                 = sql.ErrNoRows
)
