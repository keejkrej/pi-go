// Ported from packages/durable/src/session/transaction.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"math"
	"reflect"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const stInternalScanPageSize = 256

var stEmptyOperations = []chord.Op{}

type stSubmissionChange struct {
	id         SubmissionId
	settlement *SubmissionSettlement
	placedAt   *EntryId
}

func stApplySubmissionChange(current SubmissionRecord, change stSubmissionChange) (SubmissionRecord, error) {
	if current.Status == SubmissionStatusDone || current.Status == SubmissionStatusUnanswered {
		return current, nil
	}
	if change.placedAt != nil {
		if current.Status != SubmissionStatusQueued {
			return SubmissionRecord{}, fmt.Errorf("Submission %d is not queued", current.Id)
		}
		nextStatus := SubmissionStatusDone
		if current.Type == SubmissionTypeInput {
			nextStatus = SubmissionStatusPlaced
		}
		entry := *change.placedAt
		next := current
		next.Status = nextStatus
		next.Entry = &entry
		return next, nil
	}
	settlement := change.settlement
	if settlement.Status == SubmissionStatusDone && current.Status != SubmissionStatusPlaced {
		return SubmissionRecord{}, fmt.Errorf("Submission %d is not a placed input", current.Id)
	}
	next := current
	next.Status = settlement.Status
	next.Answer = stCopyPtr(settlement.Answer)
	next.Reason = stCopyPtr(settlement.Reason)
	if settlement.Detail != nil {
		copied, err := stCopyStrictJsonValue(settlement.Detail)
		if err != nil {
			return SubmissionRecord{}, err
		}
		next.Detail = copied
	} else {
		next.Detail = nil
	}
	return next, nil
}

// stLoadedDocument is one committed document incarnation owned by the Session tracker cache.
type stLoadedDocument struct {
	addressId       string
	record          DocumentRecord
	storedVersion   int
	valueVersion    int
	deltasSinceBase int
	tracker         chord.Tracker[JsonObject]
}

// stTransactionHost is Session services used by a transaction while it holds the mutation line.
type stTransactionHost interface {
	Storage() Storage
	Cached(addressId string) *stLoadedDocument
	Load(ctx chord.Context, definition *AnyDocDefinition, addressId string, address DocumentAddress) (*stLoadedDocument, error)
	Install(document *stLoadedDocument)
	Evict(addressId string, recordId DocumentId)
	ConversationCreated(tx *Transaction, record ConversationRecord) error
}

type stTransactionTask struct {
	committedLoaded           bool
	committedRecord           *TaskRecord
	writeKind                 string // "create" | "replace"
	writeRecord               *TaskRecord
	publicationConversationId *ConversationId
}

// TransactionScope sets default tx.CreateTask() conversation and appended entry task attribution.
type TransactionScope struct {
	ConversationId *ConversationId
	TaskId         *TaskId
}

type stDocumentTargetKind string

const (
	stTargetLoaded     stDocumentTargetKind = "loaded"
	stTargetCreated    stDocumentTargetKind = "created"
	stTargetForkCopy   stDocumentTargetKind = "fork-copy"
	stTargetRetireOnly stDocumentTargetKind = "retire-only"
)

type stDocumentTarget struct {
	kind         stDocumentTargetKind
	loaded       *stLoadedDocument
	createRecord DocumentCreate
	record       DocumentRecord
	version      int
	tracker      chord.Tracker[JsonObject]
	source       DocumentCopySource
}

type stDocumentEntry struct {
	addressId      string
	address        DocumentAddress
	definition     *AnyDocDefinition
	acquired       bool
	draft          chord.Draft[JsonObject]
	target         *stDocumentTarget
	change         chord.Change[JsonObject]
	prepared       chord.Prepared[JsonObject]
	retireOnCommit bool
}

type stDocumentPlanChange struct {
	tracker    chord.Tracker[JsonObject]
	prepared   chord.Prepared[JsonObject]
	version    int
	loaded     *stLoadedDocument
	definition *AnyDocDefinition
}

type stDocumentPlan struct {
	addressId      string
	committed      bool
	createRecord   DocumentCreate
	record         DocumentRecord
	retire         bool
	contentWrite   StorageWrite // nil or StorageWriteDocumentCreate / Copy / Change
	change         *stDocumentPlanChange
	conversationId *ConversationId
}

// Transaction executes one Session commit callback on the mutation line.
type Transaction struct {
	host    stTransactionHost
	ctx     chord.Context
	scope   TransactionScope
	sealed  bool
	hasTabW bool

	writes                    []StorageWrite
	createdConversationIds    map[ConversationId]struct{}
	forkSourceConversationIds map[ConversationId]struct{}
	forkSourceDocumentIds     map[DocumentId]struct{}

	tasksById               map[TaskId]*stTransactionTask
	taskOrder               []TaskId
	submissions             map[SubmissionId]SubmissionRecord
	submissionOrder         []SubmissionId
	submissionChanges       []stSubmissionChange
	plans                   []*stDocumentPlan
	documents               []*stDocumentEntry
	latestDocumentByAddress map[string]*stDocumentEntry
}

var _ Tx = (*Transaction)(nil)

func stNewTransaction(host stTransactionHost, ctx chord.Context, scope TransactionScope) *Transaction {
	return &Transaction{
		host:                      host,
		ctx:                       ctx,
		scope:                     scope,
		createdConversationIds:    make(map[ConversationId]struct{}),
		forkSourceConversationIds: make(map[ConversationId]struct{}),
		forkSourceDocumentIds:     make(map[DocumentId]struct{}),
		tasksById:                 make(map[TaskId]*stTransactionTask),
		submissions:               make(map[SubmissionId]SubmissionRecord),
		latestDocumentByAddress:   make(map[string]*stDocumentEntry),
	}
}

// ─── Table reads ─────────────────────────────────────────────────────────────

func (t *Transaction) Conversation(id ConversationId) (*ConversationRecord, error) {
	if err := t.beginRead("conversation"); err != nil {
		return nil, err
	}
	return t.host.Storage().Conversation(t.ctx, id)
}

func (t *Transaction) Entry(id EntryId) (*EntryRecord, error) {
	if err := t.beginRead("entry"); err != nil {
		return nil, err
	}
	stored, err := t.host.Storage().Entry(t.ctx, id)
	if err != nil || stored == nil {
		return nil, err
	}
	e := stored.Entry
	return &e, nil
}

func (t *Transaction) TypedEntry(token *Entry, id EntryId) (*EntryRecord, error) {
	if err := t.beginRead("entry"); err != nil {
		return nil, err
	}
	stored, err := t.host.Storage().Entry(t.ctx, id)
	if err != nil || stored == nil {
		return nil, err
	}
	e := stored.Entry
	if token != nil && !token.Is(&e) {
		return nil, nil
	}
	return &e, nil
}

func (t *Transaction) Task(id TaskId) (*TaskRecord, error) {
	if err := t.beginRead("task"); err != nil {
		return nil, err
	}
	return t.committedTask(id)
}

func (t *Transaction) ScanConversations(query ConversationQuery, limit int, cursor Cursor) (Page[ConversationRecord, Cursor], error) {
	if err := t.beginRead("scanConversations"); err != nil {
		return Page[ConversationRecord, Cursor]{}, err
	}
	return t.host.Storage().ScanConversations(t.ctx, query, limit, cursor)
}

func (t *Transaction) ScanEntries(query EntryQuery, limit int, cursor Cursor) (Page[EntryRecord, Cursor], error) {
	if err := t.beginRead("scanEntries"); err != nil {
		return Page[EntryRecord, Cursor]{}, err
	}
	return t.host.Storage().ScanEntries(t.ctx, query, limit, cursor)
}

func (t *Transaction) LatestHeadMarker(conversationId ConversationId) (*EntryRecord, error) {
	if err := t.beginRead("latestHeadMarker"); err != nil {
		return nil, err
	}
	return t.host.Storage().FindLatestHeadMarker(t.ctx, conversationId, nil)
}

func (t *Transaction) ScanTasks(query TaskQuery, limit int, cursor Cursor) (Page[TaskRecord, Cursor], error) {
	if err := t.beginRead("scanTasks"); err != nil {
		return Page[TaskRecord, Cursor]{}, err
	}
	return t.host.Storage().ScanTasks(t.ctx, query, limit, cursor)
}

// Submission reads a committed submission record for internal callers on the mutation line.
func (t *Transaction) Submission(id SubmissionId) (*SubmissionRecord, error) {
	if err := t.beginRead("submission"); err != nil {
		return nil, err
	}
	return t.host.Storage().Submission(t.ctx, id)
}

func (t *Transaction) SubmissionByRequest(conversationId ConversationId, requestId string) (*SubmissionRecord, error) {
	if err := t.beginRead("submissionByRequest"); err != nil {
		return nil, err
	}
	return t.host.Storage().SubmissionByRequest(t.ctx, conversationId, requestId)
}

// ─── Table writes ────────────────────────────────────────────────────────────

func (t *Transaction) CreateConversation(options ConversationCreateTxOptions) (*ConversationRecord, error) {
	if err := t.beginWrite(); err != nil {
		return nil, err
	}
	return t.stageConversation(nil, options.Ownership, nil)
}

// CreateRootConversation bootstraps the reserved root conversation identity.
func (t *Transaction) CreateRootConversation() (*ConversationRecord, error) {
	if err := t.beginWrite(); err != nil {
		return nil, err
	}
	root := RootConversationId
	return t.stageConversation(nil, ConversationOwnership{Kind: ConversationOwnershipOwnerless}, &root)
}

func (t *Transaction) ForkConversation(
	parentConversationId ConversationId,
	at EntryId,
	options ConversationCreateTxOptions,
) (*ConversationRecord, error) {
	if err := t.beginWrite(); err != nil {
		return nil, err
	}
	parent := &ConversationParent{ConversationId: parentConversationId, At: at}
	return t.stageConversation(parent, options.Ownership, nil)
}

func (t *Transaction) stageConversation(
	parent *ConversationParent,
	ownership ConversationOwnership,
	reservedId *ConversationId,
) (*ConversationRecord, error) {
	var id ConversationId
	if reservedId != nil {
		id = *reservedId
	} else {
		minted, err := t.host.Storage().MintId()
		if err != nil {
			return nil, err
		}
		id = IdFromNumber[ConversationId](minted)
	}
	if err := t.assertOpen(); err != nil {
		return nil, err
	}

	var owner *ConversationOwner
	if ownership.Kind == ConversationOwnershipTask {
		task, err := t.currentTask(ownership.TaskId)
		if err != nil {
			return nil, err
		}
		if err := t.assertOpen(); err != nil {
			return nil, err
		}
		if task == nil {
			return nil, fmt.Errorf("Conversation owner task %d does not exist", ownership.TaskId)
		}
		owner = &ConversationOwner{
			ConversationId: task.ConversationId,
			TaskId:         ownership.TaskId,
		}
	}

	record := ConversationRecord{
		Id:     id,
		Parent: parent,
		Owner:  owner,
	}

	if parent != nil {
		copies, err := sfPrepareForkDocumentCopies(t.ctx, t.host.Storage(), parent.ConversationId, parent.At, id)
		if err != nil {
			return nil, err
		}
		if err := t.assertOpen(); err != nil {
			return nil, err
		}
		for _, copyItem := range copies {
			t.forkSourceDocumentIds[copyItem.Source.Id] = struct{}{}
			addr := DocumentAddress{Kind: copyItem.Record.Kind, Scope: copyItem.Record.Scope, Key: docuCopyStringPtr(copyItem.Record.Key)}
			entry := &stDocumentEntry{
				addressId: AddressId(addr),
				address:   addr,
				target: &stDocumentTarget{
					kind:         stTargetForkCopy,
					createRecord: copyItem.Record,
					source:       copyItem.Source,
				},
			}
			t.documents = append(t.documents, entry)
			t.latestDocumentByAddress[entry.addressId] = entry
		}
		t.forkSourceConversationIds[parent.ConversationId] = struct{}{}
	}

	t.createdConversationIds[id] = struct{}{}
	t.writes = append(t.writes, StorageWriteConversation{
		Type:  StorageWriteTypeConversation,
		Value: record,
	})
	if err := t.host.ConversationCreated(t, record); err != nil {
		return nil, err
	}
	if err := t.assertOpen(); err != nil {
		return nil, err
	}
	out := record
	return &out, nil
}

func (t *Transaction) AppendEntry(conversationId ConversationId, value EntryDraft) (*EntryRecord, error) {
	if err := t.beginWrite(); err != nil {
		return nil, err
	}
	return t.appendEntry(conversationId, value)
}

func (t *Transaction) AppendTypedEntry(token *Entry, conversationId ConversationId, value EntryDraft) (*EntryRecord, error) {
	if err := t.beginWrite(); err != nil {
		return nil, err
	}
	if token == nil {
		return nil, js.NewTypeError("Entry token is required")
	}
	value.Kind = token.Kind
	return t.appendEntry(conversationId, value)
}

func (t *Transaction) appendEntry(conversationId ConversationId, value EntryDraft) (*EntryRecord, error) {
	if err := t.requireConversation(conversationId); err != nil {
		return nil, err
	}
	if err := t.assertOpen(); err != nil {
		return nil, err
	}
	minted, err := t.host.Storage().MintId()
	if err != nil {
		return nil, err
	}
	if err := t.assertOpen(); err != nil {
		return nil, err
	}
	id := IdFromNumber[EntryId](minted)

	var head *EntryId
	if value.Head.IsSelf() {
		selfId := id
		head = &selfId
	} else if hid, ok := value.Head.Id(); ok {
		head = &hid
	}

	raw := EntryRecord{
		Id:             id,
		ConversationId: conversationId,
		Kind:           value.Kind,
		Model:          value.Model,
		Data:           value.Data,
		Head:           head,
		Edits:          value.Edits,
		ByTaskId:       stCopyPtr(t.scope.TaskId),
	}
	record, err := stCopyEntryRecord(raw)
	if err != nil {
		return nil, err
	}
	t.writes = append(t.writes, StorageWriteEntry{
		Type:  StorageWriteTypeEntry,
		Value: record,
	})
	out := record
	return &out, nil
}

func (t *Transaction) CreateTask(task AnyTask, input any, options TaskOptions) (TaskId, error) {
	if err := t.beginWrite(); err != nil {
		return 0, err
	}
	if task == nil {
		return 0, js.NewTypeError("Task definition is required")
	}
	ownership := options.Ownership
	if ownership.Kind != TaskOwnershipConversation && ownership.Kind != TaskOwnershipTask {
		return 0, js.NewTypeError("Tx.CreateTask() requires options.Ownership")
	}
	var owner *TaskRecord
	if ownership.Kind == TaskOwnershipTask {
		var err error
		owner, err = t.currentTask(ownership.TaskId)
		if err != nil {
			return 0, err
		}
		if err := t.assertOpen(); err != nil {
			return 0, err
		}
		if owner == nil {
			return 0, fmt.Errorf("Task owner %d does not exist", ownership.TaskId)
		}
		if options.Background != nil && *options.Background {
			return 0, js.NewTypeError("A child task cannot be background")
		}
		if options.ConversationId != nil && *options.ConversationId != owner.ConversationId {
			return 0, fmt.Errorf("A child task lives in its owner's conversation %d", owner.ConversationId)
		}
	}

	var conversationId *ConversationId
	if owner != nil {
		cid := owner.ConversationId
		conversationId = &cid
	} else if options.ConversationId != nil {
		cid := *options.ConversationId
		conversationId = &cid
	} else if t.scope.ConversationId != nil {
		cid := *t.scope.ConversationId
		conversationId = &cid
	}
	if conversationId == nil {
		return 0, js.NewTypeError("Tx.CreateTask() requires options.ConversationId")
	}
	if err := t.requireConversation(*conversationId); err != nil {
		return 0, err
	}
	if err := t.assertOpen(); err != nil {
		return 0, err
	}

	def := task.erasedDefinition()
	inJson, cpJson, err := def.Initial(input)
	if err != nil {
		return 0, err
	}
	minted, err := t.host.Storage().MintId()
	if err != nil {
		return 0, err
	}
	if err := t.assertOpen(); err != nil {
		return 0, err
	}
	id := IdFromNumber[TaskId](minted)

	var ownerId *TaskId
	if owner != nil {
		oid := owner.Id
		ownerId = &oid
	}
	bg := false
	if options.Background != nil {
		bg = *options.Background
	}

	inCopied, err := stCopyStrictJsonValue(inJson)
	if err != nil {
		return 0, err
	}
	cpCopied, err := stCopyStrictJsonValue(cpJson)
	if err != nil {
		return 0, err
	}

	record := TaskRecord{
		Id:             id,
		ConversationId: *conversationId,
		Kind:           def.Name,
		Version:        def.Version,
		Input:          inCopied,
		Owner:          ownerId,
		Background:     bg,
		AbortRequested: false,
		State: TaskState{
			Status:     TaskStatePending,
			Checkpoint: cpCopied,
		},
	}
	entry := t.taskEntry(id)
	recCopy := record
	entry.writeKind = "create"
	entry.writeRecord = &recCopy
	return id, nil
}

func (t *Transaction) CreateSubmission(create SubmissionCreate) (*SubmissionRecord, error) {
	if err := t.beginWrite(); err != nil {
		return nil, err
	}
	if err := t.requireConversation(create.ConversationId); err != nil {
		return nil, err
	}
	if err := t.assertOpen(); err != nil {
		return nil, err
	}
	minted, err := t.host.Storage().MintId()
	if err != nil {
		return nil, err
	}
	if err := t.assertOpen(); err != nil {
		return nil, err
	}
	id := IdFromNumber[SubmissionId](minted)

	var detail JsonValue
	if create.Detail != nil {
		var err error
		detail, err = stCopyStrictJsonValue(create.Detail)
		if err != nil {
			return nil, err
		}
	}
	record := SubmissionRecord{
		Id:             id,
		ConversationId: create.ConversationId,
		RequestId:      stCopyPtr(create.RequestId),
		Type:           create.Type,
		Status:         create.Status,
		Entry:          stCopyPtr(create.Entry),
		Answer:         stCopyPtr(create.Answer),
		Reason:         stCopyPtr(create.Reason),
		Detail:         detail,
	}
	if _, seen := t.submissions[id]; !seen {
		t.submissionOrder = append(t.submissionOrder, id)
	}
	t.submissions[id] = record
	out := record
	return &out, nil
}

func (t *Transaction) SettleSubmission(id SubmissionId, settlement SubmissionSettlement) error {
	if err := t.assertOpen(); err != nil {
		return err
	}
	t.hasTabW = true
	copied := SubmissionSettlement{
		Status: settlement.Status,
		Answer: stCopyPtr(settlement.Answer),
		Reason: stCopyPtr(settlement.Reason),
	}
	if settlement.Detail != nil {
		var err error
		copied.Detail, err = stCopyStrictJsonValue(settlement.Detail)
		if err != nil {
			return err
		}
	}
	t.submissionChanges = append(t.submissionChanges, stSubmissionChange{
		id:         id,
		settlement: &copied,
	})
	return nil
}

func (t *Transaction) PlaceSubmission(id SubmissionId, entry EntryId) error {
	if err := t.assertOpen(); err != nil {
		return err
	}
	t.hasTabW = true
	e := entry
	t.submissionChanges = append(t.submissionChanges, stSubmissionChange{
		id:       id,
		placedAt: &e,
	})
	return nil
}

// SetTask replaces one task record completely. Internal to the Harness/scheduler.
func (t *Transaction) SetTask(value TaskRecord) error {
	if err := t.assertOpen(); err != nil {
		return err
	}
	t.hasTabW = true
	task := t.taskEntry(value.Id)
	if task.writeRecord != nil {
		if task.writeRecord.State.Status == TaskStateTerminal {
			return fmt.Errorf("Task %d already has a terminal candidate", value.Id)
		}
		if task.writeRecord.ConversationId != value.ConversationId {
			return fmt.Errorf("Task %d cannot change conversations", value.Id)
		}
	}
	copied, err := stCopyTaskRecord(value)
	if err != nil {
		return err
	}
	if task.writeKind != "create" {
		task.writeKind = "replace"
	}
	task.writeRecord = &copied
	return nil
}

// StagedTasks returns candidate records of the tasks this transaction created or replaced so far.
func (t *Transaction) StagedTasks() []TaskRecord {
	var records []TaskRecord
	for _, id := range t.taskOrder {
		if entry := t.tasksById[id]; entry.writeRecord != nil {
			records = append(records, *entry.writeRecord)
		}
	}
	return records
}

// StagedConversations returns conversations this transaction created or forked so far.
func (t *Transaction) StagedConversations() []ConversationRecord {
	var records []ConversationRecord
	for _, write := range t.writes {
		if cw, ok := write.(StorageWriteConversation); ok {
			records = append(records, cw.Value)
		}
	}
	return records
}

// ─── Documents ───────────────────────────────────────────────────────────────

func (t *Transaction) Doc(token AnyDocToken, args ...any) (chord.Draft[JsonObject], error) {
	var zero chord.Draft[JsonObject]
	if err := t.assertOpen(); err != nil {
		return zero, err
	}
	if token == nil {
		return zero, js.NewTypeError("Document token is required")
	}
	def := token.docDefinition()
	resolved, err := ResolveAddress(def, args)
	if err != nil {
		return zero, err
	}
	trailing := 0
	if def.Family {
		trailing = 1
	}
	if err := docuCheckExactArgs(def, resolved, args, trailing, "Tx.Doc()"); err != nil {
		return zero, err
	}
	if err := t.assertTaskDocumentsOpen(resolved); err != nil {
		return zero, err
	}

	latest := t.latestDocumentByAddress[resolved.Id]
	if latest != nil && !latest.retireOnCommit {
		if latest.acquired {
			return latest.draft, nil
		}
		if latest.target != nil && latest.target.kind == stTargetForkCopy {
			return t.acquireForkCopy(latest, def)
		}
	}

	var seed JsonValue
	if def.Family {
		if resolved.NextArgument >= len(args) {
			return zero, js.NewTypeError(fmt.Sprintf("Document family %s requires a creation seed", def.Kind))
		}
		var copyErr error
		seed, copyErr = stCopyStrictJsonValue(args[resolved.NextArgument])
		if copyErr != nil {
			return zero, copyErr
		}
	}

	skipLoad := latest != nil && latest.retireOnCommit
	docEntry := &stDocumentEntry{
		addressId:  resolved.Id,
		address:    resolved.Address,
		definition: def,
	}
	t.documents = append(t.documents, docEntry)
	t.latestDocumentByAddress[docEntry.addressId] = docEntry

	return t.acquire(docEntry, seed, skipLoad)
}

func (t *Transaction) RetireDoc(token AnyDocToken, args ...any) error {
	if err := t.assertOpen(); err != nil {
		return err
	}
	if token == nil {
		return js.NewTypeError("Document token is required")
	}
	def := token.docDefinition()
	resolved, err := ResolveAddress(def, args)
	if err != nil {
		return err
	}
	if err := docuCheckExactArgs(def, resolved, args, 0, "Tx.RetireDoc()"); err != nil {
		return err
	}
	latest := t.latestDocumentByAddress[resolved.Id]
	if latest != nil && latest.retireOnCommit {
		return nil
	}
	if latest != nil && latest.target != nil && latest.target.kind == stTargetForkCopy {
		if err := CheckRecordScope(def, latest.target.createRecord.AsRecord(0)); err != nil {
			return err
		}
		latest.retireOnCommit = true
		return nil
	}
	if latest != nil && latest.acquired {
		latest.retireOnCommit = true
		return nil
	}
	entry := &stDocumentEntry{
		addressId:      resolved.Id,
		address:        resolved.Address,
		definition:     def,
		retireOnCommit: true,
	}
	t.documents = append(t.documents, entry)
	t.latestDocumentByAddress[entry.addressId] = entry
	return t.findRetirement(entry)
}

func (t *Transaction) acquire(entry *stDocumentEntry, seed JsonValue, skipLoad bool) (chord.Draft[JsonObject], error) {
	var zero chord.Draft[JsonObject]
	def := entry.definition
	var loaded *stLoadedDocument
	if !skipLoad {
		var err error
		loaded, err = t.host.Load(t.ctx, def, entry.addressId, entry.address)
		if err != nil {
			return zero, err
		}
	}
	if err := t.assertOpen(); err != nil {
		return zero, err
	}
	if loaded != nil {
		if err := CheckRecordScope(def, loaded.record); err != nil {
			return zero, err
		}
		if err := CheckRecordVersion(def, loaded.record, loaded.storedVersion); err != nil {
			return zero, err
		}
		entry.target = &stDocumentTarget{
			kind:   stTargetLoaded,
			loaded: loaded,
		}
		entry.change = loaded.tracker.BeginChange()
		draft, err := entry.change.State()
		if err != nil {
			return zero, err
		}
		entry.acquired = true
		entry.draft = draft
		return draft, nil
	}

	switch entry.address.Scope.Kind {
	case DocumentScopeConversation:
		if err := t.requireConversation(entry.address.Scope.ConversationId); err != nil {
			return zero, err
		}
	case DocumentScopeTask:
		task, err := t.currentTask(entry.address.Scope.TaskId)
		if err != nil {
			return zero, err
		}
		if task == nil {
			return zero, fmt.Errorf("Task %d does not exist", entry.address.Scope.TaskId)
		}
		if task.State.Status == TaskStateTerminal {
			return zero, fmt.Errorf("Task %d is terminal", entry.address.Scope.TaskId)
		}
	}
	if err := t.assertOpen(); err != nil {
		return zero, err
	}

	rawInit := def.Initial(seed)
	copied, err := chord.CopyJson(rawInit, nil)
	if err != nil {
		return zero, err
	}
	obj, ok := copied.(JsonObject)
	if !ok || obj == nil {
		return zero, js.NewTypeError(fmt.Sprintf("Document %s initial() must return a JSON object", def.Kind))
	}

	minted, err := t.host.Storage().MintId()
	if err != nil {
		return zero, err
	}
	if err := t.assertOpen(); err != nil {
		return zero, err
	}
	id := IdFromNumber[DocumentId](minted)
	tracker := chord.Track(obj)
	entry.target = &stDocumentTarget{
		kind:         stTargetCreated,
		createRecord: NewDocumentCreate(def, entry.address, id),
		version:      def.Version,
		tracker:      tracker,
	}
	entry.change = tracker.BeginChange()
	draft, err := entry.change.State()
	if err != nil {
		return zero, err
	}
	entry.acquired = true
	entry.draft = draft
	return draft, nil
}

func (t *Transaction) acquireForkCopy(entry *stDocumentEntry, def *AnyDocDefinition) (chord.Draft[JsonObject], error) {
	var zero chord.Draft[JsonObject]
	target := entry.target
	stored, err := t.host.Storage().Document(t.ctx, target.source.Id, target.source.At)
	if err != nil {
		return zero, err
	}
	if err := t.assertOpen(); err != nil {
		return zero, err
	}
	if stored == nil {
		return zero, fmt.Errorf("Fork source document %d cannot be read", target.source.Id)
	}
	if stored.Record.Scope.Kind != DocumentScopeConversation ||
		stored.Record.Kind != target.createRecord.Kind ||
		!stEqualStringPtr(stored.Record.Key, target.createRecord.Key) ||
		stored.Record.History != target.createRecord.History ||
		stored.Record.Fork != target.createRecord.Fork {
		return zero, fmt.Errorf("Fork source document %d does not match the copied record", target.source.Id)
	}
	value, err := MaterializeDocumentValue(def, target.createRecord.AsRecord(0), stored.Version, stored.Value)
	if err != nil {
		return zero, err
	}
	tracker := chord.Track(value)
	entry.definition = def
	entry.target = &stDocumentTarget{
		kind:         stTargetCreated,
		createRecord: target.createRecord,
		version:      def.Version,
		tracker:      tracker,
	}
	entry.change = tracker.BeginChange()
	draft, err := entry.change.State()
	if err != nil {
		return zero, err
	}
	entry.acquired = true
	entry.draft = draft
	return draft, nil
}

func (t *Transaction) findRetirement(entry *stDocumentEntry) error {
	var record *DocumentRecord
	if cached := t.host.Cached(entry.addressId); cached != nil {
		r := cached.record
		record = &r
	} else {
		var err error
		record, err = t.host.Storage().FindDocument(t.ctx, entry.address, DocumentPointCurrent())
		if err != nil {
			return err
		}
	}
	if err := t.assertOpen(); err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	if err := CheckRecordScope(entry.definition, *record); err != nil {
		return err
	}
	entry.target = &stDocumentTarget{
		kind:   stTargetRetireOnly,
		record: *record,
	}
	return nil
}

// ─── Settlement ──────────────────────────────────────────────────────────────

func (t *Transaction) SettleFailure() {
	t.sealed = true
	t.abortChanges()
}

func (t *Transaction) SettleSuccess() ([]StorageWrite, error) {
	t.sealed = true
	for _, doc := range t.documents {
		if doc.change != nil {
			prepared, err := doc.change.Prepare()
			if err != nil {
				t.abortChanges()
				return nil, err
			}
			doc.prepared = prepared
		}
	}
	writes, err := t.assemble()
	if err != nil {
		t.abortChanges()
		return nil, err
	}
	return writes, nil
}

func (t *Transaction) Discard() {
	t.abortChanges()
}

func (t *Transaction) Adopt(seq Seq) []DocumentCommitChange {
	var publications []DocumentCommitChange
	for _, plan := range t.plans {
		var record DocumentRecord
		if plan.committed {
			record = plan.record
		} else {
			record = plan.createRecord.AsRecord(seq)
		}
		if plan.retire {
			s := seq
			record.RetiredAt = &s
		}
		if plan.change != nil {
			c := plan.change
			adopt := len(c.prepared.Ops()) > 0
			if c.loaded == nil {
				adopt = !plan.retire
			}
			if adopt {
				if err := c.tracker.Adopt(c.prepared); err != nil {
					panic(err)
				}
			} else {
				c.prepared.Abort()
			}
			if c.loaded != nil {
				if c.loaded.storedVersion < c.version {
					c.loaded.storedVersion = c.version
				}
				if wc, ok := plan.contentWrite.(StorageWriteDocumentChange); ok {
					if wc.Content.Kind == DocumentContentBase {
						c.loaded.deltasSinceBase = 0
					} else {
						c.loaded.deltasSinceBase++
					}
				}
			} else if !plan.retire {
				t.host.Install(&stLoadedDocument{
					addressId:       plan.addressId,
					record:          record,
					storedVersion:   c.version,
					valueVersion:    c.version,
					deltasSinceBase: 0,
					tracker:         c.tracker,
				})
			}
		}

		convId := stCopyPtr(plan.conversationId)
		if plan.retire {
			if plan.committed {
				t.host.Evict(plan.addressId, record.Id)
			}
			publications = append(publications, DocumentCommitChangeUpdate{
				Type:           "document",
				Record:         record,
				ConversationId: convId,
				Version:        nil,
				Value:          nil,
				Ops:            stEmptyOperations,
			})
		} else if wc, ok := plan.contentWrite.(StorageWriteDocumentCopy); ok {
			var cid ConversationId
			if convId != nil {
				cid = *convId
			}
			publications = append(publications, DocumentCommitChangeCopy{
				Type:           "document.copy",
				Record:         record,
				ConversationId: cid,
				Source:         wc.Source,
			})
		} else if plan.change != nil && stPublishesPlan(plan) {
			ops := stEmptyOperations
			if plan.change.loaded != nil {
				ops = plan.change.prepared.Ops()
			}
			ver := plan.change.version
			publications = append(publications, DocumentCommitChangeUpdate{
				Type:           "document",
				Record:         record,
				ConversationId: convId,
				Version:        &ver,
				Value:          plan.change.prepared.Value(),
				Ops:            ops,
			})
		}
	}
	return publications
}

func (t *Transaction) assemble() ([]StorageWrite, error) {
	storage := t.host.Storage()
	for _, doc := range t.documents {
		if plan := stPlanDocument(doc); plan != nil {
			t.plans = append(t.plans, plan)
		}
	}
	if err := t.rejectForkSourceWrites(); err != nil {
		return nil, err
	}
	if err := t.validateOwners(); err != nil {
		return nil, err
	}
	for _, id := range t.taskOrder {
		task := t.tasksById[id]
		if task.writeKind != "replace" {
			continue
		}
		committed, err := t.committedTask(id)
		if err != nil {
			return nil, err
		}
		if committed == nil {
			return nil, fmt.Errorf("Task %d does not exist", id)
		}
		if committed.State.Status == TaskStateTerminal {
			return nil, fmt.Errorf("Task %d is already terminal", id)
		}
		if committed.ConversationId != task.writeRecord.ConversationId {
			return nil, fmt.Errorf("Task %d cannot change conversations", id)
		}
	}

	terminalTaskIds := make(map[TaskId]struct{})
	var terminalTaskOrder []TaskId
	for _, id := range t.taskOrder {
		task := t.tasksById[id]
		if task.writeRecord != nil && task.writeRecord.State.Status == TaskStateTerminal {
			terminalTaskIds[id] = struct{}{}
			terminalTaskOrder = append(terminalTaskOrder, id)
		}
	}
	if len(terminalTaskIds) > 0 {
		retiring := make(map[DocumentId]struct{})
		for _, plan := range t.plans {
			scope := stPlanScope(plan)
			if scope.Kind != DocumentScopeTask {
				continue
			}
			if _, ok := terminalTaskIds[scope.TaskId]; !ok {
				continue
			}
			plan.retire = true
			retiring[stPlanId(plan)] = struct{}{}
		}
		for _, taskId := range terminalTaskOrder {
			if t.tasksById[taskId].writeKind == "create" {
				continue
			}
			var cursor Cursor
			for {
				page, err := storage.ScanDocuments(
					t.ctx,
					DocumentQuery{
						Scope: DocumentScope{Kind: DocumentScopeTask, TaskId: taskId},
						At:    DocumentPointCurrent(),
					},
					stInternalScanPageSize,
					cursor,
				)
				if err != nil {
					return nil, err
				}
				for _, record := range page.Items {
					if _, exists := retiring[record.Id]; exists {
						continue
					}
					addr := DocumentAddress{Kind: record.Kind, Scope: record.Scope, Key: docuCopyStringPtr(record.Key)}
					t.plans = append(t.plans, &stDocumentPlan{
						addressId: AddressId(addr),
						committed: true,
						record:    record,
						retire:    true,
					})
					retiring[record.Id] = struct{}{}
				}
				if page.Next == nil {
					break
				}
				cursor = page.Next
			}
		}
	}

	for _, plan := range t.plans {
		if !stPublishesPlan(plan) {
			continue
		}
		scope := stPlanScope(plan)
		if scope.Kind == DocumentScopeConversation {
			cid := scope.ConversationId
			plan.conversationId = &cid
		}
		if scope.Kind != DocumentScopeTask {
			continue
		}
		task := t.taskEntry(scope.TaskId)
		if task.publicationConversationId == nil {
			current, err := t.currentTask(scope.TaskId)
			if err != nil {
				return nil, err
			}
			if current != nil {
				cid := current.ConversationId
				task.publicationConversationId = &cid
			}
		}
		plan.conversationId = stCopyPtr(task.publicationConversationId)
	}

	for _, sc := range t.submissionChanges {
		var current *SubmissionRecord
		if local, ok := t.submissions[sc.id]; ok {
			c := local
			current = &c
		} else {
			var err error
			current, err = storage.Submission(t.ctx, sc.id)
			if err != nil {
				return nil, err
			}
		}
		if current == nil {
			return nil, fmt.Errorf("Submission %d does not exist", sc.id)
		}
		next, err := stApplySubmissionChange(*current, sc)
		if err != nil {
			return nil, err
		}
		if _, seen := t.submissions[sc.id]; !seen {
			t.submissionOrder = append(t.submissionOrder, sc.id)
		}
		t.submissions[sc.id] = next
	}

	writes := append([]StorageWrite(nil), t.writes...)
	for _, id := range t.submissionOrder {
		writes = append(writes, StorageWriteSubmission{
			Type:  StorageWriteTypeSubmission,
			Value: t.submissions[id],
		})
	}
	for _, id := range t.taskOrder {
		if task := t.tasksById[id]; task.writeRecord != nil {
			writes = append(writes, StorageWriteTask{
				Type:  StorageWriteTypeTask,
				Value: *task.writeRecord,
			})
		}
	}
	for _, plan := range t.plans {
		if wc, ok := plan.contentWrite.(StorageWriteDocumentChange); ok &&
			wc.Content.Kind == DocumentContentDelta &&
			plan.change != nil &&
			plan.change.loaded != nil {
			c := plan.change
			info := CheckpointInfo{DeltasSinceBase: c.loaded.deltasSinceBase}
			if c.definition != nil && c.definition.CheckpointWhen != nil &&
				c.definition.CheckpointWhen(c.prepared.Value(), c.prepared.Ops(), info) {
				plan.contentWrite = StorageWriteDocumentChange{
					Type: StorageWriteTypeDocumentChange,
					Id:   wc.Id,
					Content: DocumentContent{
						Version: c.version,
						Kind:    DocumentContentBase,
						Value:   c.prepared.Value(),
					},
				}
			}
		}
		if plan.contentWrite != nil {
			writes = append(writes, plan.contentWrite)
		}
		if plan.retire {
			writes = append(writes, StorageWriteDocumentRetire{
				Type: StorageWriteTypeDocumentRetire,
				Id:   stPlanId(plan),
			})
		}
	}
	return writes, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (t *Transaction) validateOwners() error {
	type ownerCheck struct {
		what   string
		taskId TaskId
	}
	var owners []ownerCheck
	for _, write := range t.writes {
		cw, ok := write.(StorageWriteConversation)
		if !ok || cw.Value.Owner == nil {
			continue
		}
		owners = append(owners, ownerCheck{what: "Conversation owner task", taskId: cw.Value.Owner.TaskId})
	}
	for _, id := range t.taskOrder {
		task := t.tasksById[id]
		if task.writeKind == "create" && task.writeRecord != nil && task.writeRecord.Owner != nil {
			owners = append(owners, ownerCheck{what: "Task owner", taskId: *task.writeRecord.Owner})
		}
	}
	for _, check := range owners {
		task, err := t.currentTask(check.taskId)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("%s %d does not exist", check.what, check.taskId)
		}
		if task.State.Status == TaskStateTerminal || task.State.Status == TaskStateCompleting {
			return fmt.Errorf("%s %d is %s", check.what, check.taskId, task.State.Status)
		}
		if task.AbortRequested {
			return fmt.Errorf("%s %d is abort-marked", check.what, check.taskId)
		}
	}
	return nil
}

func (t *Transaction) rejectForkSourceWrites() error {
	for _, plan := range t.plans {
		if plan.contentWrite == nil && !plan.retire {
			continue
		}
		id := stPlanId(plan)
		if _, isSource := t.forkSourceDocumentIds[id]; isSource {
			return fmt.Errorf("Cannot change fork source document %d in the fork transaction", id)
		}
		scope := stPlanScope(plan)
		forkPolicy := stPlanFork(plan)
		if scope.Kind == DocumentScopeConversation {
			if _, isForkSrc := t.forkSourceConversationIds[scope.ConversationId]; isForkSrc && forkPolicy == DocumentForkCurrent {
				return fmt.Errorf("Cannot fork conversation %d while changing its current-policy documents", scope.ConversationId)
			}
		}
	}
	return nil
}

func (t *Transaction) abortChanges() {
	for _, doc := range t.documents {
		if doc.change != nil {
			doc.change.Abort()
		}
	}
}

func (t *Transaction) assertOpen() error {
	if t.sealed {
		return fmt.Errorf("Transaction has settled")
	}
	return nil
}

func (t *Transaction) assertTaskDocumentsOpen(resolved *ResolvedAddress) error {
	if resolved.Address.Scope.Kind == DocumentScopeTask {
		if entry := t.tasksById[resolved.Address.Scope.TaskId]; entry != nil &&
			entry.writeRecord != nil &&
			entry.writeRecord.State.Status == TaskStateTerminal {
			return fmt.Errorf("Task %d is terminal", resolved.Address.Scope.TaskId)
		}
	}
	return nil
}

func (t *Transaction) beginRead(method string) error {
	if err := t.assertOpen(); err != nil {
		return err
	}
	if t.hasTabW {
		return NewReadAfterWrite(method)
	}
	return nil
}

func (t *Transaction) beginWrite() error {
	if err := t.assertOpen(); err != nil {
		return err
	}
	t.hasTabW = true
	return nil
}

func (t *Transaction) requireConversation(id ConversationId) error {
	if _, created := t.createdConversationIds[id]; created {
		return nil
	}
	conv, err := t.host.Storage().Conversation(t.ctx, id)
	if err != nil {
		return err
	}
	if conv == nil {
		return fmt.Errorf("Conversation %d does not exist", id)
	}
	return nil
}

func (t *Transaction) taskEntry(id TaskId) *stTransactionTask {
	entry, ok := t.tasksById[id]
	if !ok {
		entry = &stTransactionTask{}
		t.tasksById[id] = entry
		t.taskOrder = append(t.taskOrder, id)
	}
	return entry
}

func (t *Transaction) currentTask(id TaskId) (*TaskRecord, error) {
	if entry := t.tasksById[id]; entry != nil && entry.writeRecord != nil {
		return entry.writeRecord, nil
	}
	return t.committedTask(id)
}

func (t *Transaction) committedTask(id TaskId) (*TaskRecord, error) {
	entry := t.taskEntry(id)
	if !entry.committedLoaded {
		rec, err := t.host.Storage().Task(t.ctx, id)
		if err != nil {
			return nil, err
		}
		entry.committedLoaded = true
		entry.committedRecord = rec
	}
	return entry.committedRecord, nil
}

func stPlanDocument(doc *stDocumentEntry) *stDocumentPlan {
	target := doc.target
	if target == nil {
		return nil
	}
	switch target.kind {
	case stTargetCreated:
		prepared := doc.prepared
		return &stDocumentPlan{
			addressId:    doc.addressId,
			committed:    false,
			createRecord: target.createRecord,
			retire:       doc.retireOnCommit,
			contentWrite: StorageWriteDocumentCreate{
				Type:   StorageWriteTypeDocumentCreate,
				Record: target.createRecord,
				Content: DocumentContent{
					Version: target.version,
					Kind:    DocumentContentBase,
					Value:   prepared.Value(),
				},
			},
			change: &stDocumentPlanChange{
				tracker:  target.tracker,
				prepared: prepared,
				version:  target.version,
			},
		}
	case stTargetForkCopy:
		return &stDocumentPlan{
			addressId:    doc.addressId,
			committed:    false,
			createRecord: target.createRecord,
			retire:       doc.retireOnCommit,
			contentWrite: StorageWriteDocumentCopy{
				Type:   StorageWriteTypeDocumentCopy,
				Record: target.createRecord,
				Source: target.source,
			},
		}
	case stTargetRetireOnly:
		return &stDocumentPlan{
			addressId: doc.addressId,
			committed: true,
			record:    target.record,
			retire:    doc.retireOnCommit,
		}
	case stTargetLoaded:
		loaded := target.loaded
		def := doc.definition
		prepared := doc.prepared
		version := def.Version
		id := loaded.record.Id
		var contentWrite StorageWrite
		if loaded.storedVersion < version {
			contentWrite = StorageWriteDocumentChange{
				Type: StorageWriteTypeDocumentChange,
				Id:   id,
				Content: DocumentContent{
					Version: version,
					Kind:    DocumentContentBase,
					Value:   prepared.Value(),
				},
			}
		} else if len(prepared.Ops()) > 0 {
			contentWrite = StorageWriteDocumentChange{
				Type: StorageWriteTypeDocumentChange,
				Id:   id,
				Content: DocumentContent{
					Version: version,
					Kind:    DocumentContentDelta,
					Ops:     prepared.Ops(),
				},
			}
		}
		return &stDocumentPlan{
			addressId:    doc.addressId,
			committed:    true,
			record:       loaded.record,
			retire:       doc.retireOnCommit,
			contentWrite: contentWrite,
			change: &stDocumentPlanChange{
				tracker:    loaded.tracker,
				prepared:   prepared,
				version:    version,
				loaded:     loaded,
				definition: def,
			},
		}
	}
	return nil
}

func stPlanScope(plan *stDocumentPlan) DocumentScope {
	if plan.committed {
		return plan.record.Scope
	}
	return plan.createRecord.Scope
}

func stPlanFork(plan *stDocumentPlan) DocumentFork {
	if plan.committed {
		return plan.record.Fork
	}
	return plan.createRecord.Fork
}

func stPlanId(plan *stDocumentPlan) DocumentId {
	if plan.committed {
		return plan.record.Id
	}
	return plan.createRecord.Id
}

func stPublishesPlan(plan *stDocumentPlan) bool {
	return plan.retire || plan.change == nil || plan.change.loaded == nil || plan.contentWrite != nil
}

func stEqualStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func stCopyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func stCheckFinite(rv reflect.Value, seen map[uintptr]struct{}) error {
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		if rv.Kind() == reflect.Pointer {
			ptr := rv.Pointer()
			if ptr != 0 {
				if _, ok := seen[ptr]; ok {
					return js.NewTypeError("Value contains cycles and is not strict JSON")
				}
				seen[ptr] = struct{}{}
				defer delete(seen, ptr)
			}
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Func, reflect.Chan, reflect.Complex64, reflect.Complex128:
		return js.NewTypeError("Value is not strict JSON")
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return js.NewTypeError("Value contains a non-finite number and is not strict JSON")
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if err := stCheckFinite(rv.Index(i), seen); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, k := range rv.MapKeys() {
			if err := stCheckFinite(rv.MapIndex(k), seen); err != nil {
				return err
			}
		}
	case reflect.Struct:
		if rv.CanAddr() {
			if obj, ok := rv.Addr().Interface().(*jsonx.Object); ok && obj != nil {
				for _, v := range obj.All() {
					if v != nil {
						if err := stCheckFinite(reflect.ValueOf(v), seen); err != nil {
							return err
						}
					}
				}
				return nil
			}
		}
		if rv.CanInterface() {
			if opt, ok := rv.Interface().(jsonx.Opt[any]); ok {
				if v, has := opt.Get(); has && v != nil {
					return stCheckFinite(reflect.ValueOf(v), seen)
				}
				return nil
			}
		}
		for i := 0; i < rv.NumField(); i++ {
			if rv.Type().Field(i).PkgPath != "" {
				continue
			}
			if err := stCheckFinite(rv.Field(i), seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func stCopyStrictJsonValue(v any) (JsonValue, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, js.NewTypeError("Value contains a non-finite number and is not strict JSON")
		}
		if x == 0 {
			return float64(0), nil
		}
		return x, nil
	case []any, *jsonx.Object:
		return chord.CopyJson(x, nil)
	default:
		if err := stCheckFinite(reflect.ValueOf(v), make(map[uintptr]struct{})); err != nil {
			return nil, err
		}
		encoded, err := jsonx.Encode(v)
		if err != nil {
			return nil, err
		}
		if encoded == nil {
			return nil, nil
		}
		return chord.CopyJson(encoded, nil)
	}
}

func stCopyEntryRecord(r EntryRecord) (EntryRecord, error) {
	if r.Model != nil || r.Data != nil || r.Edits != nil {
		if err := stCheckFinite(reflect.ValueOf(r), make(map[uintptr]struct{})); err != nil {
			return EntryRecord{}, err
		}
	}
	b, err := jsonx.Marshal(r)
	if err != nil {
		return EntryRecord{}, err
	}
	var out EntryRecord
	if err := jsonx.Unmarshal(b, &out); err != nil {
		return EntryRecord{}, err
	}
	return out, nil
}

func stCopyTaskRecord(r TaskRecord) (TaskRecord, error) {
	if err := stCheckFinite(reflect.ValueOf(r), make(map[uintptr]struct{})); err != nil {
		return TaskRecord{}, err
	}
	b, err := jsonx.Marshal(r)
	if err != nil {
		return TaskRecord{}, err
	}
	var out TaskRecord
	if err := jsonx.Unmarshal(b, &out); err != nil {
		return TaskRecord{}, err
	}
	return out, nil
}
