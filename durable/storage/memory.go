// Ported from packages/durable/src/storage/memory.ts (pi v1.0.0).

package storage

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type memTableName string

const (
	memTableConversation memTableName = "conversation"
	memTableEntry        memTableName = "entry"
	memTableTask         memTableName = "task"
	memTableSubmission   memTableName = "submission"
	memTableDocument     memTableName = "document"
)

type memDocRevision struct {
	content durable.DocumentContent
	seq     durable.Seq
}

type memStoredDocumentState struct {
	record    durable.DocumentRecord
	revisions []memDocRevision
}

type memDocAction struct {
	create     *durable.DocumentCreate
	hasContent bool
	content    durable.DocumentContent
	retire     bool
}

type memDocAddressIndex struct {
	ids          []durable.DocumentId
	currentId    durable.DocumentId
	hasCurrentId bool
}

type memState struct {
	recordTypes                        map[int64]memTableName
	conversations                      map[durable.ConversationId]durable.ConversationRecord
	conversationIds                    []durable.ConversationId
	conversationIdsByOwnerConversation map[durable.ConversationId][]durable.ConversationId
	conversationIdsByOwnerTask         map[durable.TaskId][]durable.ConversationId
	entries                            map[durable.EntryId]durable.EntryRecord
	entryIds                           map[durable.ConversationId][]durable.EntryId
	headEntryIds                       map[durable.ConversationId][]durable.EntryId
	entryCommitSeqs                    map[durable.EntryId]durable.Seq
	tasks                              map[durable.TaskId]durable.TaskRecord
	taskIds                            []durable.TaskId
	taskIdsByStatus                    map[durable.TaskStateStatus][]durable.TaskId
	submissions                        map[durable.SubmissionId]durable.SubmissionRecord
	submissionIds                      []durable.SubmissionId
	submissionIdsByStatus              map[durable.SubmissionStatus][]durable.SubmissionId
	submissionIdsByRequest             map[durable.ConversationId]map[string]durable.SubmissionId
	documents                          map[durable.DocumentId]*memStoredDocumentState
	documentAddresses                  map[string]*memDocAddressIndex
	documentIdsByScope                 map[string][]durable.DocumentId
}

func memNewState() memState {
	return memState{
		recordTypes:                        make(map[int64]memTableName),
		conversations:                      make(map[durable.ConversationId]durable.ConversationRecord),
		conversationIdsByOwnerConversation: make(map[durable.ConversationId][]durable.ConversationId),
		conversationIdsByOwnerTask:         make(map[durable.TaskId][]durable.ConversationId),
		entries:                            make(map[durable.EntryId]durable.EntryRecord),
		entryIds:                           make(map[durable.ConversationId][]durable.EntryId),
		headEntryIds:                       make(map[durable.ConversationId][]durable.EntryId),
		entryCommitSeqs:                    make(map[durable.EntryId]durable.Seq),
		tasks:                              make(map[durable.TaskId]durable.TaskRecord),
		taskIdsByStatus: map[durable.TaskStateStatus][]durable.TaskId{
			durable.TaskStatePending:    {},
			durable.TaskStateRunning:    {},
			durable.TaskStateWaiting:    {},
			durable.TaskStateCompleting: {},
			durable.TaskStateTerminal:   {},
		},
		submissions: make(map[durable.SubmissionId]durable.SubmissionRecord),
		submissionIdsByStatus: map[durable.SubmissionStatus][]durable.SubmissionId{
			durable.SubmissionStatusQueued:     {},
			durable.SubmissionStatusPlaced:     {},
			durable.SubmissionStatusDone:       {},
			durable.SubmissionStatusUnanswered: {},
		},
		submissionIdsByRequest: make(map[durable.ConversationId]map[string]durable.SubmissionId),
		documents:              make(map[durable.DocumentId]*memStoredDocumentState),
		documentAddresses:      make(map[string]*memDocAddressIndex),
		documentIdsByScope:     make(map[string][]durable.DocumentId),
	}
}

// PreparedMemoryCommit is a fully validated, detached state mutation whose application performs no fallible preparation.
type PreparedMemoryCommit struct {
	Seq         durable.Seq
	Writes      []durable.StorageWrite
	apply       func() durable.Seq
	applyLocked func() durable.Seq
}

// Apply commits the prepared writes to the MemoryStorage instance. Apply is idempotent.
func (p *PreparedMemoryCommit) Apply() durable.Seq {
	return p.apply()
}

// MemoryStorage is a detached in-memory reference implementation of durable.Storage.
//
// Reads and retained writes are cloned intentionally to match the ownership boundary
// of serialization-backed stores.
type MemoryStorage struct {
	mu      sync.Mutex
	state   memState
	nextId  int64
	nextSeq int64
	closed  bool
}

// NewMemoryStorage returns a ready in-memory Storage backed by deep-cloned state.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		state:   memNewState(),
		nextId:  2,
		nextSeq: 1,
	}
}

// Commit atomically validates and applies writes, returning the assigned monotonic commit sequence.
func (s *MemoryStorage) Commit(_ chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prepared, err := s.prepareCommitLocked(writes, durable.SeqFromNumber(s.nextSeq))
	if err != nil {
		return 0, err
	}
	return prepared.applyLocked(), nil
}

// PrepareCommit validates and detaches one commit without changing observable state.
// If optional seq is omitted, the next sequential commit number is used.
func (s *MemoryStorage) PrepareCommit(writes []durable.StorageWrite, seq ...durable.Seq) (*PreparedMemoryCommit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	targetSeq := durable.SeqFromNumber(s.nextSeq)
	if len(seq) > 0 {
		targetSeq = seq[0]
	}
	return s.prepareCommitLocked(writes, targetSeq)
}

func (s *MemoryStorage) prepareCommitLocked(writes []durable.StorageWrite, seq durable.Seq) (*PreparedMemoryCommit, error) {
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	if int64(seq) < s.nextSeq {
		return nil, fmt.Errorf("Commit sequence %d does not strictly increase", seq)
	}
	cloned := make([]durable.StorageWrite, len(writes))
	for i, w := range writes {
		cw, err := memCloneWrite(w)
		if err != nil {
			return nil, err
		}
		cloned[i] = cw
	}
	detachedWrites, err := s.resolveDocumentCopiesLocked(cloned)
	if err != nil {
		return nil, err
	}
	if err := s.checkGlobalIdsLocked(detachedWrites); err != nil {
		return nil, err
	}
	docActions, err := memPrepareDocumentActions(detachedWrites)
	if err != nil {
		return nil, err
	}
	if err := s.checkDocumentActionsLocked(docActions); err != nil {
		return nil, err
	}
	exposedWrites := make([]durable.StorageWrite, len(detachedWrites))
	for i, w := range detachedWrites {
		ew, err := memCloneWrite(w)
		if err != nil {
			return nil, err
		}
		exposedWrites[i] = ew
	}
	var applied bool
	applyLocked := func() durable.Seq {
		if !applied {
			applied = true
			s.applyPreparedCommitLocked(detachedWrites, docActions, seq)
		}
		return seq
	}
	return &PreparedMemoryCommit{
		Seq:         seq,
		Writes:      exposedWrites,
		applyLocked: applyLocked,
		apply: func() durable.Seq {
			s.mu.Lock()
			defer s.mu.Unlock()
			return applyLocked()
		},
	}, nil
}

// MintId allocates a fresh monotonically increasing record ID.
func (s *MemoryStorage) MintId() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return 0, err
	}
	if s.nextId > 9007199254740991 {
		return 0, fmt.Errorf("ID space is exhausted")
	}
	id := s.nextId
	s.nextId++
	return id, nil
}

// Conversation fetches one conversation record by ID.
func (s *MemoryStorage) Conversation(_ chord.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	value, ok := s.state.conversations[id]
	if !ok {
		return nil, nil
	}
	cloned := memCloneConversation(value)
	return &cloned, nil
}

// ScanConversations lists matching conversations in ascending ID order.
func (s *MemoryStorage) ScanConversations(
	_ chord.Context,
	query durable.ConversationQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	var ids []durable.ConversationId
	switch {
	case query.OwnerTaskId != nil:
		ids = s.state.conversationIdsByOwnerTask[*query.OwnerTaskId]
	case query.OwnerConversationId != nil:
		ids = s.state.conversationIdsByOwnerConversation[*query.OwnerConversationId]
	default:
		ids = s.state.conversationIds
	}
	after, hasAfter, err := memCursorId[durable.ConversationId](cursor)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	start := 0
	if hasAfter {
		start = memUpperBound(ids, after)
	}
	values := make([]durable.ConversationRecord, 0, memMin(limit+1, len(ids)-start))
	for i := start; i < len(ids) && len(values) <= limit; i++ {
		val := s.state.conversations[ids[i]]
		if query.OwnerConversationId != nil {
			if val.Owner == nil || val.Owner.ConversationId != *query.OwnerConversationId {
				continue
			}
		}
		values = append(values, val)
	}
	return memPage(values, limit, func(c durable.ConversationRecord) durable.ConversationId { return c.Id }, memCloneConversation), nil
}

// Entry fetches a transcript entry by global ID along with its commit sequence.
func (s *MemoryStorage) Entry(_ chord.Context, id durable.EntryId) (*durable.StoredEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	entry, ok := s.state.entries[id]
	if !ok {
		return nil, nil
	}
	cloned, err := memCloneEntry(entry)
	if err != nil {
		return nil, err
	}
	return &durable.StoredEntry{
		Entry:     cloned,
		CommitSeq: s.state.entryCommitSeqs[id],
	}, nil
}

// EntryInConversation fetches entry id if visible from conversationId's fork-aware history.
func (s *MemoryStorage) EntryInConversation(_ chord.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.StoredEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	visible, err := s.visibleEntriesLocked(conversationId, &id, &id)
	if err != nil {
		return nil, err
	}
	if len(visible) == 0 {
		return nil, nil
	}
	cloned, err := memCloneEntry(visible[0])
	if err != nil {
		return nil, err
	}
	return &durable.StoredEntry{
		Entry:     cloned,
		CommitSeq: s.state.entryCommitSeqs[id],
	}, nil
}

// FindLatestHeadMarker returns the newest visible entry with a Head marker at or before atOrBeforeEntryId.
func (s *MemoryStorage) FindLatestHeadMarker(
	_ chord.Context,
	conversationId durable.ConversationId,
	atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	if _, ok := s.state.conversations[conversationId]; !ok {
		return nil, fmt.Errorf("Unknown conversation: %d", conversationId)
	}
	currentId := conversationId
	upperEntryId := durable.EntryId(math.MaxInt64)
	if atOrBeforeEntryId != nil {
		upperEntryId = *atOrBeforeEntryId
	}
	for {
		ids := s.state.headEntryIds[currentId]
		idx := memUpperBound(ids, upperEntryId) - 1
		if idx >= 0 {
			entry := s.state.entries[ids[idx]]
			cloned, err := memCloneEntry(entry)
			if err != nil {
				return nil, err
			}
			return &cloned, nil
		}
		conv := s.state.conversations[currentId]
		if conv.Parent == nil {
			return nil, nil
		}
		if conv.Parent.At < upperEntryId {
			upperEntryId = conv.Parent.At
		}
		currentId = conv.Parent.ConversationId
	}
}

// ScanEntries lists fork-aware entries for a conversation in descending ID order.
func (s *MemoryStorage) ScanEntries(
	_ chord.Context,
	query durable.EntryQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	after, hasAfter, err := memCursorId[durable.EntryId](cursor)
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	maxEntryId := query.MaxEntryId
	if hasAfter {
		below := after - 1
		if maxEntryId == nil || below < *maxEntryId {
			maxEntryId = &below
		}
	}
	if _, ok := s.state.conversations[query.ConversationId]; !ok {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, fmt.Errorf("Unknown conversation: %d", query.ConversationId)
	}
	visible := make([]durable.EntryRecord, 0, memMin(limit+1, 64))
	currentId := query.ConversationId
	upperEntryId := durable.EntryId(math.MaxInt64)
	if maxEntryId != nil {
		upperEntryId = *maxEntryId
	}
	hasMin := query.MinEntryId != nil
	var minId durable.EntryId
	if hasMin {
		minId = *query.MinEntryId
	}
	for {
		ids := s.state.entryIds[currentId]
		for i := memUpperBound(ids, upperEntryId) - 1; i >= 0; i-- {
			id := ids[i]
			if hasMin && id < minId {
				break
			}
			visible = append(visible, s.state.entries[id])
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit {
			break
		}
		conv := s.state.conversations[currentId]
		if conv.Parent == nil {
			break
		}
		if conv.Parent.At < upperEntryId {
			upperEntryId = conv.Parent.At
		}
		if hasMin && upperEntryId < minId {
			break
		}
		currentId = conv.Parent.ConversationId
	}
	return memPageErr(visible, limit, func(e durable.EntryRecord) durable.EntryId { return e.Id }, memCloneEntry)
}

// Task fetches a task record by ID.
func (s *MemoryStorage) Task(_ chord.Context, id durable.TaskId) (*durable.TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	value, ok := s.state.tasks[id]
	if !ok {
		return nil, nil
	}
	cloned := memCloneTask(value)
	return &cloned, nil
}

// ScanTasks lists matching tasks in ascending ID order.
func (s *MemoryStorage) ScanTasks(
	_ chord.Context,
	query durable.TaskQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.TaskRecord, durable.Cursor], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
	}
	after, hasAfter, err := memCursorId[durable.TaskId](cursor)
	if err != nil {
		return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
	}
	ids := s.state.taskIds
	if query.Status != "" {
		ids = s.state.taskIdsByStatus[query.Status]
	}
	start := 0
	if hasAfter {
		start = memUpperBound(ids, after)
	}
	values := make([]durable.TaskRecord, 0, memMin(limit+1, len(ids)-start))
	for i := start; i < len(ids) && len(values) <= limit; i++ {
		val := s.state.tasks[ids[i]]
		if query.ConversationId != nil && val.ConversationId != *query.ConversationId {
			continue
		}
		if query.Kind != nil && val.Kind != *query.Kind {
			continue
		}
		if query.AbortRequested != nil && val.AbortRequested != *query.AbortRequested {
			continue
		}
		if query.Background != nil && val.Background != *query.Background {
			continue
		}
		values = append(values, val)
	}
	return memPage(values, limit, func(t durable.TaskRecord) durable.TaskId { return t.Id }, memCloneTask), nil
}

// Submission fetches a submission record by ID.
func (s *MemoryStorage) Submission(_ chord.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	value, ok := s.state.submissions[id]
	if !ok {
		return nil, nil
	}
	cloned := memCloneSubmission(value)
	return &cloned, nil
}

// ScanSubmissions lists matching submissions in ascending ID order.
func (s *MemoryStorage) ScanSubmissions(
	_ chord.Context,
	query durable.SubmissionQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	after, hasAfter, err := memCursorId[durable.SubmissionId](cursor)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	ids := s.state.submissionIds
	if query.Status != "" {
		ids = s.state.submissionIdsByStatus[query.Status]
	}
	start := 0
	if hasAfter {
		start = memUpperBound(ids, after)
	}
	values := make([]durable.SubmissionRecord, 0, memMin(limit+1, len(ids)-start))
	for i := start; i < len(ids) && len(values) <= limit; i++ {
		val := s.state.submissions[ids[i]]
		if query.ConversationId != nil && val.ConversationId != *query.ConversationId {
			continue
		}
		values = append(values, val)
	}
	return memPage(values, limit, func(sub durable.SubmissionRecord) durable.SubmissionId { return sub.Id }, memCloneSubmission), nil
}

// SubmissionByRequest finds a submission carrying conversationId and requestId.
func (s *MemoryStorage) SubmissionByRequest(_ chord.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	byReq := s.state.submissionIdsByRequest[conversationId]
	if byReq == nil {
		return nil, nil
	}
	id, ok := byReq[requestId]
	if !ok {
		return nil, nil
	}
	cloned := memCloneSubmission(s.state.submissions[id])
	return &cloned, nil
}

// FindDocument locates the document record at address alive at point at.
func (s *MemoryStorage) FindDocument(_ chord.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	key, err := memAddressKey(address)
	if err != nil {
		return nil, err
	}
	idx := s.state.documentAddresses[key]
	if idx == nil {
		return nil, nil
	}
	if at.IsCurrent() {
		if !idx.hasCurrentId {
			return nil, nil
		}
		cloned := memCloneDocumentRecord(s.state.documents[idx.currentId].record)
		return &cloned, nil
	}
	for _, id := range idx.ids {
		rec := s.state.documents[id].record
		if memIsAliveAt(rec, at) {
			cloned := memCloneDocumentRecord(rec)
			return &cloned, nil
		}
	}
	return nil, nil
}

// Document materializes the stored document value for id at point at.
func (s *MemoryStorage) Document(_ chord.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return nil, err
	}
	stored, err := s.materializeDocumentLocked(id, at)
	if err != nil || stored == nil {
		return nil, err
	}
	return &durable.StoredDocument{
		Record:          memCloneDocumentRecord(stored.Record),
		Version:         stored.Version,
		Value:           stored.Value.Clone(),
		DeltasSinceBase: stored.DeltasSinceBase,
	}, nil
}

// ScanDocuments lists matching documents alive at query.At in ascending ID order.
func (s *MemoryStorage) ScanDocuments(
	_ chord.Context,
	query durable.DocumentQuery,
	limit int,
	cursor durable.Cursor,
) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertOpenLocked(); err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	sKey, err := memScopeKey(query.Scope)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	ids := s.state.documentIdsByScope[sKey]
	after, hasAfter, err := memCursorId[durable.DocumentId](cursor)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	start := 0
	if hasAfter {
		start = memUpperBound(ids, after)
	}
	values := make([]durable.DocumentRecord, 0, memMin(limit+1, len(ids)-start))
	for i := start; i < len(ids) && len(values) <= limit; i++ {
		rec := s.state.documents[ids[i]].record
		if query.Kind != nil && rec.Kind != *query.Kind {
			continue
		}
		if memIsAliveAt(rec, query.At) {
			values = append(values, rec)
		}
	}
	return memPage(values, limit, func(d durable.DocumentRecord) durable.DocumentId { return d.Id }, memCloneDocumentRecord), nil
}

// Close marks the storage closed; subsequent calls fail with an error.
func (s *MemoryStorage) Close(_ chord.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *MemoryStorage) assertOpenLocked() error {
	if s.closed {
		return fmt.Errorf("MemoryStorage is closed")
	}
	return nil
}

func (s *MemoryStorage) resolveDocumentCopiesLocked(writes []durable.StorageWrite) ([]durable.StorageWrite, error) {
	hasCopy := false
	for _, w := range writes {
		if w.WriteType() == durable.StorageWriteTypeDocumentCopy {
			hasCopy = true
			break
		}
	}
	if !hasCopy {
		return writes, nil
	}
	changedDocIds := make(map[durable.DocumentId]struct{})
	for _, w := range writes {
		switch cw := w.(type) {
		case durable.StorageWriteDocumentCreate:
			changedDocIds[cw.Record.Id] = struct{}{}
		case durable.StorageWriteDocumentCopy:
			changedDocIds[cw.Record.Id] = struct{}{}
		case durable.StorageWriteDocumentChange:
			changedDocIds[cw.Id] = struct{}{}
		case durable.StorageWriteDocumentRetire:
			changedDocIds[cw.Id] = struct{}{}
		}
	}
	resolved := make([]durable.StorageWrite, len(writes))
	for i, w := range writes {
		cp, ok := w.(durable.StorageWriteDocumentCopy)
		if !ok {
			resolved[i] = w
			continue
		}
		reject := func(cause error) error {
			return durable.NewStorageRejected(fmt.Sprintf("Document copy %d was rejected", cp.Record.Id), cause)
		}
		if _, changed := changedDocIds[cp.Source.Id]; changed {
			return nil, reject(fmt.Errorf("Fork source document %d is changed in the copy batch", cp.Source.Id))
		}
		stored, err := s.materializeDocumentLocked(cp.Source.Id, cp.Source.At)
		if err != nil {
			return nil, reject(err)
		}
		if stored == nil {
			return nil, reject(fmt.Errorf("Fork source document %d cannot be read", cp.Source.Id))
		}
		if stored.Record.Scope.Kind != durable.DocumentScopeConversation ||
			cp.Record.Scope.Kind != durable.DocumentScopeConversation ||
			stored.Record.Kind != cp.Record.Kind ||
			!memEqualStringPtr(stored.Record.Key, cp.Record.Key) ||
			stored.Record.History != cp.Record.History ||
			stored.Record.Fork != cp.Record.Fork {
			return nil, reject(fmt.Errorf("Fork source document %d does not match the copied record", cp.Source.Id))
		}
		resolved[i] = durable.StorageWriteDocumentCreate{
			Type:   durable.StorageWriteTypeDocumentCreate,
			Record: cp.Record,
			Content: durable.DocumentContent{
				Kind:    durable.DocumentContentBase,
				Version: stored.Version,
				Value:   stored.Value,
			},
		}
	}
	return resolved, nil
}

func (s *MemoryStorage) checkGlobalIdsLocked(writes []durable.StorageWrite) error {
	claimed := make(map[int64]memTableName)
	for _, w := range writes {
		var table memTableName
		var id int64
		switch cw := w.(type) {
		case durable.StorageWriteDocumentChange, durable.StorageWriteDocumentRetire:
			continue
		case durable.StorageWriteDocumentCreate:
			table = memTableDocument
			id = int64(cw.Record.Id)
		case durable.StorageWriteDocumentCopy:
			table = memTableDocument
			id = int64(cw.Record.Id)
		case durable.StorageWriteConversation:
			table = memTableConversation
			id = int64(cw.Value.Id)
		case durable.StorageWriteEntry:
			table = memTableEntry
			id = int64(cw.Value.Id)
		case durable.StorageWriteTask:
			table = memTableTask
			id = int64(cw.Value.Id)
		case durable.StorageWriteSubmission:
			table = memTableSubmission
			id = int64(cw.Value.Id)
		default:
			return fmt.Errorf("Unsupported storage write type %q", w.WriteType())
		}
		existing, hasExisting := s.state.recordTypes[id]
		earlier, hasEarlier := claimed[id]
		if table == memTableConversation || table == memTableEntry || table == memTableDocument {
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

func memPrepareDocumentActions(writes []durable.StorageWrite) (map[durable.DocumentId]*memDocAction, error) {
	actions := make(map[durable.DocumentId]*memDocAction)
	for _, w := range writes {
		var id durable.DocumentId
		switch cw := w.(type) {
		case durable.StorageWriteDocumentCreate:
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
			act = &memDocAction{}
			actions[id] = act
		}
		switch cw := w.(type) {
		case durable.StorageWriteDocumentCreate:
			if act.create != nil || act.hasContent {
				return nil, fmt.Errorf("Document %d has more than one content command", id)
			}
			rec := cw.Record
			act.create = &rec
			act.hasContent = true
			act.content = cw.Content
		case durable.StorageWriteDocumentChange:
			if act.hasContent {
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

func (s *MemoryStorage) checkDocumentActionsLocked(actions map[durable.DocumentId]*memDocAction) error {
	liveCounts := make(map[string]int)
	for id, act := range actions {
		existing, hasExisting := s.state.documents[id]
		if act.create == nil && !hasExisting {
			return fmt.Errorf("Unknown document: %d", id)
		}
		if act.create != nil && hasExisting {
			return fmt.Errorf("Document %d already exists", id)
		}
		if hasExisting && existing.record.RetiredAt != nil {
			return fmt.Errorf("Document %d is retired", id)
		}
		if act.hasContent && act.content.Kind == durable.DocumentContentDelta {
			if !hasExisting || len(existing.revisions) == 0 {
				return fmt.Errorf("Document %d delta has no base", id)
			}
			prev := existing.revisions[len(existing.revisions)-1]
			if prev.content.Version != act.content.Version {
				return fmt.Errorf("Document %d version transition requires a base", id)
			}
		}

		var key string
		var err error
		if act.create == nil {
			key, err = memRecordAddressKey(existing.record)
		} else {
			key, err = memCreateAddressKey(*act.create)
		}
		if err != nil {
			return err
		}
		addrIdx := s.state.documentAddresses[key]
		hasCurrent := addrIdx != nil && addrIdx.hasCurrentId
		live, seen := liveCounts[key]
		if !seen {
			if hasCurrent {
				live = 1
			}
		}
		if act.retire && hasCurrent && addrIdx.currentId == id {
			live--
		}
		if act.create != nil && !act.retire {
			live++
		}
		liveCounts[key] = live
	}
	for _, live := range liveCounts {
		if live > 1 {
			return fmt.Errorf("Document address already has a current incarnation")
		}
	}
	return nil
}

func (s *MemoryStorage) applyPreparedCommitLocked(
	prepared []durable.StorageWrite,
	docActions map[durable.DocumentId]*memDocAction,
	seq durable.Seq,
) {
	for _, w := range prepared {
		switch cw := w.(type) {
		case durable.StorageWriteConversation:
			val := cw.Value
			s.state.recordTypes[int64(val.Id)] = memTableConversation
			s.state.conversations[val.Id] = val
			s.state.conversationIds = memInsertSorted(s.state.conversationIds, val.Id)
			if val.Owner != nil {
				s.state.conversationIdsByOwnerConversation[val.Owner.ConversationId] = memInsertSorted(
					s.state.conversationIdsByOwnerConversation[val.Owner.ConversationId],
					val.Id,
				)
				s.state.conversationIdsByOwnerTask[val.Owner.TaskId] = memInsertSorted(
					s.state.conversationIdsByOwnerTask[val.Owner.TaskId],
					val.Id,
				)
			}
			s.bumpNextIdLocked(int64(val.Id))

		case durable.StorageWriteEntry:
			val := cw.Value
			s.state.recordTypes[int64(val.Id)] = memTableEntry
			s.state.entries[val.Id] = val
			s.state.entryCommitSeqs[val.Id] = seq
			s.state.entryIds[val.ConversationId] = memInsertSorted(s.state.entryIds[val.ConversationId], val.Id)
			if val.Head != nil {
				s.state.headEntryIds[val.ConversationId] = memInsertSorted(s.state.headEntryIds[val.ConversationId], val.Id)
			}
			s.bumpNextIdLocked(int64(val.Id))

		case durable.StorageWriteTask:
			val := cw.Value
			s.state.recordTypes[int64(val.Id)] = memTableTask
			prev, existed := s.state.tasks[val.Id]
			if !existed {
				s.state.taskIds = memInsertSorted(s.state.taskIds, val.Id)
				s.state.taskIdsByStatus[val.State.Status] = memInsertSorted(s.state.taskIdsByStatus[val.State.Status], val.Id)
			} else if prev.State.Status != val.State.Status {
				s.state.taskIdsByStatus[prev.State.Status] = memRemoveSorted(s.state.taskIdsByStatus[prev.State.Status], val.Id)
				s.state.taskIdsByStatus[val.State.Status] = memInsertSorted(s.state.taskIdsByStatus[val.State.Status], val.Id)
			}
			s.state.tasks[val.Id] = val
			s.bumpNextIdLocked(int64(val.Id))

		case durable.StorageWriteSubmission:
			val := cw.Value
			s.state.recordTypes[int64(val.Id)] = memTableSubmission
			prev, existed := s.state.submissions[val.Id]
			if !existed {
				s.state.submissionIds = memInsertSorted(s.state.submissionIds, val.Id)
				s.state.submissionIdsByStatus[val.Status] = memInsertSorted(s.state.submissionIdsByStatus[val.Status], val.Id)
			} else if prev.Status != val.Status {
				s.state.submissionIdsByStatus[prev.Status] = memRemoveSorted(s.state.submissionIdsByStatus[prev.Status], val.Id)
				s.state.submissionIdsByStatus[val.Status] = memInsertSorted(s.state.submissionIdsByStatus[val.Status], val.Id)
			}
			if existed && prev.RequestId != nil {
				if prevReqs := s.state.submissionIdsByRequest[prev.ConversationId]; prevReqs != nil {
					if curId, ok := prevReqs[*prev.RequestId]; ok && curId == val.Id {
						delete(prevReqs, *prev.RequestId)
						if len(prevReqs) == 0 {
							delete(s.state.submissionIdsByRequest, prev.ConversationId)
						}
					}
				}
			}
			s.state.submissions[val.Id] = val
			if val.RequestId != nil {
				reqs := s.state.submissionIdsByRequest[val.ConversationId]
				if reqs == nil {
					reqs = make(map[string]durable.SubmissionId)
					s.state.submissionIdsByRequest[val.ConversationId] = reqs
				}
				reqs[*val.RequestId] = val.Id
			}
			s.bumpNextIdLocked(int64(val.Id))
		}
	}
	s.applyDocumentActionsLocked(docActions, seq)
	s.nextSeq = int64(seq) + 1
}

func (s *MemoryStorage) applyDocumentActionsLocked(docActions map[durable.DocumentId]*memDocAction, seq durable.Seq) {
	// Deterministic order matches commit independence across distinct document IDs.
	docIds := make([]durable.DocumentId, 0, len(docActions))
	for id := range docActions {
		docIds = append(docIds, id)
	}
	sort.Slice(docIds, func(i, j int) bool { return docIds[i] < docIds[j] })

	for _, id := range docIds {
		act := docActions[id]
		stored := s.state.documents[id]
		if act.create != nil {
			rec := act.create.AsRecord(seq)
			if act.retire {
				retired := seq
				rec.RetiredAt = &retired
			}
			stored = &memStoredDocumentState{
				record:    rec,
				revisions: []memDocRevision{{content: act.content, seq: seq}},
			}
			s.state.recordTypes[int64(id)] = memTableDocument
			s.state.documents[id] = stored

			key, _ := memRecordAddressKey(rec)
			addr := s.state.documentAddresses[key]
			if addr == nil {
				addr = &memDocAddressIndex{}
				s.state.documentAddresses[key] = addr
			}
			addr.ids = memInsertSorted(addr.ids, id)

			sKey, _ := memScopeKey(rec.Scope)
			s.state.documentIdsByScope[sKey] = memInsertSorted(s.state.documentIdsByScope[sKey], id)
			s.bumpNextIdLocked(int64(id))
		} else if act.hasContent {
			rev := memDocRevision{content: act.content, seq: seq}
			if rev.content.Kind == durable.DocumentContentBase && memIsCurrentOnly(stored.record) {
				stored.revisions = []memDocRevision{rev}
			} else {
				stored.revisions = append(stored.revisions, rev)
			}
		}

		if act.retire && act.create == nil {
			retired := seq
			stored.record.RetiredAt = &retired
		}
		if act.retire && memIsCurrentOnly(stored.record) {
			stored.revisions = nil
		}
		if act.create != nil || act.retire {
			key, _ := memRecordAddressKey(stored.record)
			addr := s.state.documentAddresses[key]
			if act.retire && addr.hasCurrentId && addr.currentId == id {
				addr.currentId = 0
				addr.hasCurrentId = false
			}
			if act.create != nil && !act.retire {
				addr.currentId = id
				addr.hasCurrentId = true
			}
		}
	}
}

func (s *MemoryStorage) materializeDocumentLocked(id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	stored := s.state.documents[id]
	if stored == nil {
		return nil, nil
	}
	isCurrent := at.IsCurrent()
	if !isCurrent && memIsCurrentOnly(stored.record) {
		return nil, fmt.Errorf("Document %d does not retain historical content", id)
	}
	if !memIsAliveAt(stored.record, at) {
		return nil, nil
	}
	revs := stored.revisions
	if seq, hasSeq := at.Seq(); !isCurrent && hasSeq {
		filtered := make([]memDocRevision, 0, len(revs))
		for _, r := range revs {
			if r.seq <= seq {
				filtered = append(filtered, r)
			}
		}
		revs = filtered
	}
	baseIdx := len(revs) - 1
	for baseIdx >= 0 && revs[baseIdx].content.Kind != durable.DocumentContentBase {
		baseIdx--
	}
	if baseIdx < 0 {
		return nil, fmt.Errorf("Document %d is missing a required base", id)
	}
	base := revs[baseIdx].content
	version := base.Version
	var batchErr error
	val, applyErr := chord.ApplyImmutableBatches(base.Value, func(yield func(chord.OpList) bool) {
		for i := baseIdx + 1; i < len(revs); i++ {
			rev := revs[i]
			if rev.content.Kind != durable.DocumentContentDelta || rev.content.Version != version {
				batchErr = fmt.Errorf("Document %d crosses a stored version boundary without a base", id)
				return
			}
			if !yield(rev.content.Ops) {
				return
			}
		}
	})
	if batchErr != nil {
		return nil, batchErr
	}
	if applyErr != nil {
		return nil, applyErr
	}
	return &durable.StoredDocument{
		Record:          stored.record,
		Version:         version,
		Value:           val,
		DeltasSinceBase: len(revs) - baseIdx - 1,
	}, nil
}

func (s *MemoryStorage) visibleEntriesLocked(
	conversationId durable.ConversationId,
	minEntryId *durable.EntryId,
	maxEntryId *durable.EntryId,
) ([]durable.EntryRecord, error) {
	if _, ok := s.state.conversations[conversationId]; !ok {
		return nil, fmt.Errorf("Unknown conversation: %d", conversationId)
	}
	var out []durable.EntryRecord
	currentId := conversationId
	upper := durable.EntryId(math.MaxInt64)
	if maxEntryId != nil {
		upper = *maxEntryId
	}
	hasMin := minEntryId != nil
	var minId durable.EntryId
	if hasMin {
		minId = *minEntryId
	}
	for {
		ids := s.state.entryIds[currentId]
		for i := memUpperBound(ids, upper) - 1; i >= 0; i-- {
			id := ids[i]
			if hasMin && id < minId {
				break
			}
			out = append(out, s.state.entries[id])
		}
		conv := s.state.conversations[currentId]
		if conv.Parent == nil {
			break
		}
		if conv.Parent.At < upper {
			upper = conv.Parent.At
		}
		if hasMin && upper < minId {
			break
		}
		currentId = conv.Parent.ConversationId
	}
	return out, nil
}

func (s *MemoryStorage) bumpNextIdLocked(id int64) {
	if id >= s.nextId {
		s.nextId = id + 1
	}
}

func memScopeKey(scope durable.DocumentScope) (string, error) {
	switch scope.Kind {
	case durable.DocumentScopeSession:
		return jsonx.Stringify([]any{"session"})
	case durable.DocumentScopeConversation:
		return jsonx.Stringify([]any{"conversation", float64(scope.ConversationId)})
	case durable.DocumentScopeTask:
		return jsonx.Stringify([]any{"task", float64(scope.TaskId)})
	default:
		return "", fmt.Errorf("Unsupported document scope %q", scope.Kind)
	}
}

func memAddressKey(addr durable.DocumentAddress) (string, error) {
	sk, err := memScopeKey(addr.Scope)
	if err != nil {
		return "", err
	}
	var tail any
	if addr.Key == nil {
		tail = []any{"singleton"}
	} else {
		tail = []any{"family", *addr.Key}
	}
	return jsonx.Stringify([]any{addr.Kind, sk, tail})
}

func memRecordAddressKey(rec durable.DocumentRecord) (string, error) {
	return memAddressKey(durable.DocumentAddress{
		Kind:  rec.Kind,
		Scope: rec.Scope,
		Key:   rec.Key,
	})
}

func memCreateAddressKey(create durable.DocumentCreate) (string, error) {
	return memAddressKey(durable.DocumentAddress{
		Kind:  create.Kind,
		Scope: create.Scope,
		Key:   create.Key,
	})
}

func memIsAliveAt(rec durable.DocumentRecord, at durable.DocumentPoint) bool {
	if at.IsCurrent() {
		return rec.RetiredAt == nil
	}
	seq, ok := at.Seq()
	if !ok {
		return false
	}
	if rec.CreatedAt > seq {
		return false
	}
	return rec.RetiredAt == nil || seq < *rec.RetiredAt
}

func memIsCurrentOnly(rec durable.DocumentRecord) bool {
	return rec.Scope.Kind != durable.DocumentScopeConversation || rec.History == durable.DocumentHistoryLatest
}

func memCursorId[I ~int64](cursor durable.Cursor) (I, bool, error) {
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

func memLowerBound[I ~int64](ids []I, target I) int {
	lo, hi := 0, len(ids)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if ids[mid] < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func memUpperBound[I ~int64](ids []I, target I) int {
	lo, hi := 0, len(ids)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if ids[mid] <= target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func memInsertSorted[I ~int64](ids []I, id I) []I {
	n := len(ids)
	if n == 0 || ids[n-1] < id {
		return append(ids, id)
	}
	idx := memLowerBound(ids, id)
	if idx < n && ids[idx] == id {
		return ids
	}
	ids = append(ids, 0)
	copy(ids[idx+1:], ids[idx:])
	ids[idx] = id
	return ids
}

func memRemoveSorted[I ~int64](ids []I, id I) []I {
	idx := memLowerBound(ids, id)
	if idx < len(ids) && ids[idx] == id {
		return append(ids[:idx], ids[idx+1:]...)
	}
	return ids
}

func memPage[T any, I ~int64](
	values []T,
	limit int,
	idOf func(T) I,
	cloneFn func(T) T,
) durable.Page[T, durable.Cursor] {
	if limit <= 0 {
		out := durable.Page[T, durable.Cursor]{Items: []T{}}
		if len(values) > 0 {
			out.Items = []T{}
		}
		return out
	}
	hasNext := len(values) > limit
	count := len(values)
	if hasNext {
		count = limit
	}
	items := make([]T, count)
	for i := 0; i < count; i++ {
		items[i] = cloneFn(values[i])
	}
	if !hasNext {
		return durable.Page[T, durable.Cursor]{Items: items}
	}
	return durable.Page[T, durable.Cursor]{
		Items: items,
		Next:  jsonx.ObjectOf("after", float64(idOf(items[len(items)-1]))),
	}
}

func memPageErr[T any, I ~int64](
	values []T,
	limit int,
	idOf func(T) I,
	cloneFn func(T) (T, error),
) (durable.Page[T, durable.Cursor], error) {
	if limit <= 0 {
		return durable.Page[T, durable.Cursor]{Items: []T{}}, nil
	}
	hasNext := len(values) > limit
	count := len(values)
	if hasNext {
		count = limit
	}
	items := make([]T, count)
	for i := 0; i < count; i++ {
		cloned, err := cloneFn(values[i])
		if err != nil {
			return durable.Page[T, durable.Cursor]{}, err
		}
		items[i] = cloned
	}
	if !hasNext {
		return durable.Page[T, durable.Cursor]{Items: items}, nil
	}
	return durable.Page[T, durable.Cursor]{
		Items: items,
		Next:  jsonx.ObjectOf("after", float64(idOf(items[len(items)-1]))),
	}, nil
}

func memMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func memEqualStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func memClonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func memCloneConversation(c durable.ConversationRecord) durable.ConversationRecord {
	out := durable.ConversationRecord{Id: c.Id}
	if c.Parent != nil {
		p := *c.Parent
		out.Parent = &p
	}
	if c.Owner != nil {
		o := *c.Owner
		out.Owner = &o
	}
	return out
}

func memCloneEntry(e durable.EntryRecord) (durable.EntryRecord, error) {
	out := durable.EntryRecord{
		Id:             e.Id,
		ConversationId: e.ConversationId,
		Kind:           e.Kind,
		Data:           jsonx.CloneValue(e.Data),
		Head:           memClonePtr(e.Head),
		ByTaskId:       memClonePtr(e.ByTaskId),
	}
	if e.Model != nil {
		raw, err := jsonx.Marshal(e.Model)
		if err != nil {
			return durable.EntryRecord{}, err
		}
		var msgs ai.MessageList
		if err := jsonx.Unmarshal(raw, &msgs); err != nil {
			return durable.EntryRecord{}, err
		}
		out.Model = msgs
	}
	if e.Edits != nil {
		edits := make([]durable.ContextEdit, len(e.Edits))
		for i, ed := range e.Edits {
			edits[i] = durable.ContextEdit{
				Target: ed.Target,
				Action: ed.Action,
			}
			if ed.Messages != nil {
				raw, err := jsonx.Marshal(ed.Messages)
				if err != nil {
					return durable.EntryRecord{}, err
				}
				var msgs ai.MessageList
				if err := jsonx.Unmarshal(raw, &msgs); err != nil {
					return durable.EntryRecord{}, err
				}
				edits[i].Messages = msgs
			}
		}
		out.Edits = edits
	}
	return out, nil
}

func memCloneTask(t durable.TaskRecord) durable.TaskRecord {
	out := durable.TaskRecord{
		Id:             t.Id,
		ConversationId: t.ConversationId,
		Kind:           t.Kind,
		Version:        t.Version,
		Input:          jsonx.CloneValue(t.Input),
		Owner:          memClonePtr(t.Owner),
		Background:     t.Background,
		AbortRequested: t.AbortRequested,
		State: durable.TaskState{
			Status:     t.State.Status,
			Checkpoint: jsonx.CloneValue(t.State.Checkpoint),
			Policy:     t.State.Policy,
		},
	}
	if t.State.On != nil {
		out.State.On = append([]durable.TaskId(nil), t.State.On...)
	}
	if t.State.Outcome != nil {
		oc := durable.TaskOutcome{
			Status: t.State.Outcome.Status,
			Reason: memClonePtr(t.State.Outcome.Reason),
		}
		if t.State.Outcome.Result.IsSome() {
			v, _ := t.State.Outcome.Result.Get()
			oc.Result = jsonx.Some(jsonx.CloneValue(v))
		} else if t.State.Outcome.Result.IsNull() {
			oc.Result = jsonx.Null[durable.JsonValue]()
		}
		if t.State.Outcome.Error != nil {
			oc.Error = &durable.TaskOutcomeError{
				Message: t.State.Outcome.Error.Message,
				Detail:  jsonx.CloneValue(t.State.Outcome.Error.Detail),
			}
		}
		out.State.Outcome = &oc
	}
	if t.Memos != nil {
		out.Memos = t.Memos.Clone()
	}
	return out
}

func memCloneSubmission(s durable.SubmissionRecord) durable.SubmissionRecord {
	return durable.SubmissionRecord{
		Id:             s.Id,
		ConversationId: s.ConversationId,
		RequestId:      memClonePtr(s.RequestId),
		Type:           s.Type,
		Status:         s.Status,
		Entry:          memClonePtr(s.Entry),
		Answer:         memClonePtr(s.Answer),
		Reason:         memClonePtr(s.Reason),
		Detail:         jsonx.CloneValue(s.Detail),
	}
}

func memCloneDocumentRecord(d durable.DocumentRecord) durable.DocumentRecord {
	return durable.DocumentRecord{
		Id:        d.Id,
		Kind:      d.Kind,
		Key:       memClonePtr(d.Key),
		CreatedAt: d.CreatedAt,
		RetiredAt: memClonePtr(d.RetiredAt),
		Scope:     d.Scope,
		History:   d.History,
		Fork:      d.Fork,
	}
}

func memCloneDocumentCreate(d durable.DocumentCreate) durable.DocumentCreate {
	return durable.DocumentCreate{
		Id:      d.Id,
		Kind:    d.Kind,
		Key:     memClonePtr(d.Key),
		Scope:   d.Scope,
		History: d.History,
		Fork:    d.Fork,
	}
}

func memCloneOps(ops chord.OpList) (chord.OpList, error) {
	if ops == nil {
		return nil, nil
	}
	raw, err := jsonx.Marshal(ops)
	if err != nil {
		return nil, err
	}
	var out chord.OpList
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func memCloneDocumentContent(c durable.DocumentContent) (durable.DocumentContent, error) {
	ops, err := memCloneOps(c.Ops)
	if err != nil {
		return durable.DocumentContent{}, err
	}
	var val durable.JsonObject
	if c.Value != nil {
		val = c.Value.Clone()
	}
	return durable.DocumentContent{
		Version: c.Version,
		Kind:    c.Kind,
		Value:   val,
		Ops:     ops,
	}, nil
}

func memCloneWrite(w durable.StorageWrite) (durable.StorageWrite, error) {
	switch cw := w.(type) {
	case durable.StorageWriteConversation:
		return durable.StorageWriteConversation{
			Type:  cw.Type,
			Value: memCloneConversation(cw.Value),
		}, nil
	case *durable.StorageWriteConversation:
		return durable.StorageWriteConversation{
			Type:  cw.Type,
			Value: memCloneConversation(cw.Value),
		}, nil
	case durable.StorageWriteEntry:
		e, err := memCloneEntry(cw.Value)
		if err != nil {
			return nil, err
		}
		return durable.StorageWriteEntry{
			Type:  cw.Type,
			Value: e,
		}, nil
	case *durable.StorageWriteEntry:
		e, err := memCloneEntry(cw.Value)
		if err != nil {
			return nil, err
		}
		return durable.StorageWriteEntry{
			Type:  cw.Type,
			Value: e,
		}, nil
	case durable.StorageWriteTask:
		return durable.StorageWriteTask{
			Type:  cw.Type,
			Value: memCloneTask(cw.Value),
		}, nil
	case *durable.StorageWriteTask:
		return durable.StorageWriteTask{
			Type:  cw.Type,
			Value: memCloneTask(cw.Value),
		}, nil
	case durable.StorageWriteSubmission:
		return durable.StorageWriteSubmission{
			Type:  cw.Type,
			Value: memCloneSubmission(cw.Value),
		}, nil
	case *durable.StorageWriteSubmission:
		return durable.StorageWriteSubmission{
			Type:  cw.Type,
			Value: memCloneSubmission(cw.Value),
		}, nil
	case durable.StorageWriteDocumentCreate:
		c, err := memCloneDocumentContent(cw.Content)
		if err != nil {
			return nil, err
		}
		return durable.StorageWriteDocumentCreate{
			Type:    cw.Type,
			Record:  memCloneDocumentCreate(cw.Record),
			Content: c,
		}, nil
	case *durable.StorageWriteDocumentCreate:
		c, err := memCloneDocumentContent(cw.Content)
		if err != nil {
			return nil, err
		}
		return durable.StorageWriteDocumentCreate{
			Type:    cw.Type,
			Record:  memCloneDocumentCreate(cw.Record),
			Content: c,
		}, nil
	case durable.StorageWriteDocumentCopy:
		return durable.StorageWriteDocumentCopy{
			Type:   cw.Type,
			Record: memCloneDocumentCreate(cw.Record),
			Source: cw.Source,
		}, nil
	case *durable.StorageWriteDocumentCopy:
		return durable.StorageWriteDocumentCopy{
			Type:   cw.Type,
			Record: memCloneDocumentCreate(cw.Record),
			Source: cw.Source,
		}, nil
	case durable.StorageWriteDocumentChange:
		c, err := memCloneDocumentContent(cw.Content)
		if err != nil {
			return nil, err
		}
		return durable.StorageWriteDocumentChange{
			Type:    cw.Type,
			Id:      cw.Id,
			Content: c,
		}, nil
	case *durable.StorageWriteDocumentChange:
		c, err := memCloneDocumentContent(cw.Content)
		if err != nil {
			return nil, err
		}
		return durable.StorageWriteDocumentChange{
			Type:    cw.Type,
			Id:      cw.Id,
			Content: c,
		}, nil
	case durable.StorageWriteDocumentRetire:
		return cw, nil
	case *durable.StorageWriteDocumentRetire:
		return *cw, nil
	default:
		return nil, fmt.Errorf("Unsupported storage write %T", w)
	}
}

var _ durable.Storage = (*MemoryStorage)(nil)
