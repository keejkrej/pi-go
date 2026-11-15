// Ported from packages/durable/src/types.ts (pi v1.0.0).

package durable

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// JsonValue is any strict JSON value (nil, bool, float64, string, []any, or *jsonx.Object).
type JsonValue = any

// JsonObject is a JSON object used as the root of every durable document.
type JsonObject = *jsonx.Object

// ConversationId identifies a conversation.
type ConversationId int64

// EntryId identifies a transcript entry.
type EntryId int64

// TaskId identifies a durable task.
type TaskId int64

// SubmissionId identifies an admitted submission.
type SubmissionId int64

// DocumentId identifies one create-to-retire document incarnation.
type DocumentId int64

// Seq is a strictly increasing sequence assigned to one atomic storage commit; gaps are permitted.
type Seq int64

// RootConversationId is the reserved ID always used by the root conversation.
const RootConversationId ConversationId = 1

// DocumentScopeKind names who owns a document: the session, a conversation, or a task.
type DocumentScopeKind string

const (
	DocumentScopeSession      DocumentScopeKind = "session"
	DocumentScopeConversation DocumentScopeKind = "conversation"
	DocumentScopeTask         DocumentScopeKind = "task"
)

// DocumentHistory declares how much history a conversation document retains.
type DocumentHistory string

const (
	DocumentHistoryLatest     DocumentHistory = "latest"
	DocumentHistoryRewindable DocumentHistory = "rewindable"
)

// DocumentFork declares how a forked conversation initializes an inherited conversation document.
type DocumentFork string

const (
	DocumentForkAsOf    DocumentFork = "asOf"
	DocumentForkCurrent DocumentFork = "current"
	DocumentForkInitial DocumentFork = "initial"
)

// DocumentScope identifies the owner of a document incarnation or query.
type DocumentScope struct {
	Kind           DocumentScopeKind `json:"kind"`
	ConversationId ConversationId    `json:"conversationId,omitzero"`
	TaskId         TaskId            `json:"taskId,omitzero"`
}

// CheckpointInfo holds stored replay state supplied to a document's checkpoint predicate.
type CheckpointInfo struct {
	// DeltasSinceBase counts deltas already stored after the newest base, excluding the change being evaluated.
	DeltasSinceBase int `json:"deltasSinceBase"`
}

// DocDefinition declares a singleton durable document.
type DocDefinition struct {
	Kind           string
	Version        int
	Scope          DocumentScopeKind
	History        DocumentHistory
	Fork           DocumentFork
	Initial        func() JsonObject
	Migrate        func(value JsonObject, fromVersion int) JsonObject
	CheckpointWhen func(value JsonObject, ops []chord.Op, info CheckpointInfo) bool
}

// DocFamilyDefinition declares a keyed durable document family.
type DocFamilyDefinition struct {
	Kind           string
	Version        int
	Scope          DocumentScopeKind
	History        DocumentHistory
	Fork           DocumentFork
	Family         bool
	Initial        func(seed JsonValue) JsonObject
	Migrate        func(value JsonObject, fromVersion int) JsonObject
	CheckpointWhen func(value JsonObject, ops []chord.Op, info CheckpointInfo) bool
}

// AnyDocDefinition is the erased normalized document definition used by Session.
type AnyDocDefinition struct {
	Kind           string
	Version        int
	Scope          DocumentScopeKind
	History        DocumentHistory
	Fork           DocumentFork
	Family         bool
	Initial        func(seed JsonValue) JsonObject
	Migrate        func(value JsonObject, fromVersion int) JsonObject
	CheckpointWhen func(value JsonObject, ops []chord.Op, info CheckpointInfo) bool
}

// AnyDocToken is an erased singleton or family document token.
type AnyDocToken interface {
	docDefinition() *AnyDocDefinition
}

// DocToken is a typed singleton document token passed explicitly to typed access.
type DocToken struct {
	Definition *AnyDocDefinition
}

func (t *DocToken) docDefinition() *AnyDocDefinition {
	if t == nil {
		return nil
	}
	return t.Definition
}

// DocFamilyToken is a typed document family token passed explicitly to typed access.
type DocFamilyToken struct {
	Definition *AnyDocDefinition
}

func (t *DocFamilyToken) docDefinition() *AnyDocDefinition {
	if t == nil {
		return nil
	}
	return t.Definition
}

// Scope-specific documentation aliases matching TypeScript token types.
type (
	SessionDocToken                      = *DocToken
	ConversationDocToken                 = *DocToken
	RewindableConversationDocToken       = *DocToken
	TaskDocToken                         = *DocToken
	SessionDocFamilyToken                = *DocFamilyToken
	ConversationDocFamilyToken           = *DocFamilyToken
	RewindableConversationDocFamilyToken = *DocFamilyToken
	TaskDocFamilyToken                   = *DocFamilyToken
)

// TaskOwnershipKind distinguishes conversation-owned top-level tasks from task-owned children.
type TaskOwnershipKind string

const (
	TaskOwnershipConversation TaskOwnershipKind = "conversation"
	TaskOwnershipTask         TaskOwnershipKind = "task"
)

// TaskOwnership names who owns a task.
type TaskOwnership struct {
	Kind   TaskOwnershipKind `json:"kind"`
	TaskId TaskId            `json:"taskId,omitzero"`
}

// JoinPolicy determines how a waiting task treats the tasks it waits on.
type JoinPolicy string

const (
	JoinPolicyFailFast   JoinPolicy = "failFast"
	JoinPolicyAllSettled JoinPolicy = "allSettled"
)

// TaskOptions configures task creation.
type TaskOptions struct {
	Ownership      TaskOwnership   `json:"ownership"`
	ConversationId *ConversationId `json:"conversationId,omitzero"`
	Background     *bool           `json:"background,omitzero"`
}

// ConversationOwnershipKind names who owns a conversation.
type ConversationOwnershipKind string

const (
	ConversationOwnershipOwnerless ConversationOwnershipKind = "ownerless"
	ConversationOwnershipTask      ConversationOwnershipKind = "task"
)

// ConversationOwnership is selected explicitly whenever a conversation is created.
type ConversationOwnership struct {
	Kind   ConversationOwnershipKind `json:"kind"`
	TaskId TaskId                    `json:"taskId,omitzero"`
}

// ConversationParent is the fork source and inclusive parent entry through which history is inherited.
type ConversationParent struct {
	ConversationId ConversationId `json:"conversationId"`
	At             EntryId        `json:"at"`
}

// ConversationOwner is the creator edge used for attribution, subtree abort, and subtree idle waits.
type ConversationOwner struct {
	ConversationId ConversationId `json:"conversationId"`
	TaskId         TaskId         `json:"taskId"`
}

// ConversationRecord holds immutable identity, history ancestry, and task ownership of a transcript scope.
type ConversationRecord struct {
	Id     ConversationId      `json:"id"`
	Parent *ConversationParent `json:"parent,omitzero"`
	Owner  *ConversationOwner  `json:"owner,omitzero"`
}

// ContextEditAction is "omit" or "replace".
type ContextEditAction string

const (
	ContextEditActionOmit    ContextEditAction = "omit"
	ContextEditActionReplace ContextEditAction = "replace"
)

// ContextEdit overrides one visible entry's contribution to model context.
type ContextEdit struct {
	Target   EntryId           `json:"target"`
	Action   ContextEditAction `json:"action"`
	Messages ai.MessageList    `json:"messages,omitzero"`
}

// EntryHead selects the first entry in active context for an EntryDraft: absent, an EntryId, or "self".
type EntryHead struct {
	id   EntryId
	self bool
}

// EntryHeadSelf returns an EntryHead pointing at the newly assigned entry ID.
func EntryHeadSelf() EntryHead { return EntryHead{self: true} }

// EntryHeadAt returns an EntryHead pointing at an explicit existing entry ID.
func EntryHeadAt(id EntryId) EntryHead { return EntryHead{id: id} }

// IsZero reports whether the head is unset (absent).
func (h EntryHead) IsZero() bool { return !h.self && h.id == 0 }

// IsSelf reports whether the head is "self".
func (h EntryHead) IsSelf() bool { return h.self }

// Id returns the explicit EntryId when not "self" and non-zero.
func (h EntryHead) Id() (EntryId, bool) {
	if h.self || h.id == 0 {
		return 0, false
	}
	return h.id, true
}

func (h EntryHead) MarshalJSON() ([]byte, error) {
	if h.self {
		return []byte(`"self"`), nil
	}
	if h.id != 0 {
		return jsonx.Marshal(h.id)
	}
	return []byte("null"), nil
}

func (h *EntryHead) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch v := parsed.(type) {
	case nil:
		*h = EntryHead{}
		return nil
	case string:
		if v == "self" {
			*h = EntryHeadSelf()
			return nil
		}
		return fmt.Errorf("invalid entry head string %q", v)
	case float64:
		if float64(int64(v)) != v {
			return fmt.Errorf("invalid entry head number %v", v)
		}
		*h = EntryHeadAt(EntryId(int64(v)))
		return nil
	default:
		return fmt.Errorf("invalid entry head %T", parsed)
	}
}

// EntryRecord is an immutable transcript event with separate model-facing and application-facing payloads.
type EntryRecord struct {
	Id             EntryId        `json:"id"`
	ConversationId ConversationId `json:"conversationId"`
	Kind           string         `json:"kind"`
	Model          ai.MessageList `json:"model,omitzero"`
	Data           JsonValue      `json:"data,omitzero"`
	Head           *EntryId       `json:"head,omitzero"`
	Edits          []ContextEdit  `json:"edits,omitzero"`
	ByTaskId       *TaskId        `json:"byTaskId,omitzero"`
}

// EntryDraft is entry content supplied before the Session assigns identity and task attribution.
type EntryDraft struct {
	Kind  string         `json:"kind"`
	Model ai.MessageList `json:"model,omitzero"`
	Data  JsonValue      `json:"data,omitzero"`
	Head  EntryHead      `json:"head,omitzero"`
	Edits []ContextEdit  `json:"edits,omitzero"`
}

// Entry identifies a typed entry kind with a narrowing guard.
type Entry struct {
	Kind string
}

// Is reports whether record is non-nil and matches this entry's Kind.
func (e *Entry) Is(record *EntryRecord) bool {
	return e != nil && record != nil && record.Kind == e.Kind
}

// SubmissionType distinguishes user inputs from passive writes.
type SubmissionType string

const (
	SubmissionTypeInput SubmissionType = "input"
	SubmissionTypeWrite SubmissionType = "write"
)

// SubmissionStatus tracks the durable lifecycle of a submission.
type SubmissionStatus string

const (
	SubmissionStatusQueued     SubmissionStatus = "queued"
	SubmissionStatusPlaced     SubmissionStatus = "placed"
	SubmissionStatusDone       SubmissionStatus = "done"
	SubmissionStatusUnanswered SubmissionStatus = "unanswered"
)

// SubmissionRecord is the durable lifecycle of one admitted user input or passive entry write.
type SubmissionRecord struct {
	Id             SubmissionId     `json:"id"`
	ConversationId ConversationId   `json:"conversationId"`
	RequestId      *string          `json:"requestId,omitzero"`
	Type           SubmissionType   `json:"type"`
	Status         SubmissionStatus `json:"status"`
	Entry          *EntryId         `json:"entry,omitzero"`
	Answer         *EntryId         `json:"answer,omitzero"`
	Reason         *string          `json:"reason,omitzero"`
	Detail         JsonValue        `json:"detail,omitzero"`
}

// SubmissionSettlement is a terminal status staged for a submission.
type SubmissionSettlement struct {
	Status SubmissionStatus `json:"status"`
	Answer *EntryId         `json:"answer,omitzero"`
	Reason *string          `json:"reason,omitzero"`
	Detail JsonValue        `json:"detail,omitzero"`
}

// SubmissionCreate holds submission fields supplied before Session assigns an ID.
type SubmissionCreate struct {
	ConversationId ConversationId   `json:"conversationId"`
	RequestId      *string          `json:"requestId,omitzero"`
	Type           SubmissionType   `json:"type"`
	Status         SubmissionStatus `json:"status"`
	Entry          *EntryId         `json:"entry,omitzero"`
	Answer         *EntryId         `json:"answer,omitzero"`
	Reason         *string          `json:"reason,omitzero"`
	Detail         JsonValue        `json:"detail,omitzero"`
}

// TaskOutcomeStatus classifies how a task terminated.
type TaskOutcomeStatus string

const (
	TaskOutcomeCompleted TaskOutcomeStatus = "completed"
	TaskOutcomeFailed    TaskOutcomeStatus = "failed"
	TaskOutcomeAborted   TaskOutcomeStatus = "aborted"
	TaskOutcomeOrphaned  TaskOutcomeStatus = "orphaned"
	TaskOutcomeFaulted   TaskOutcomeStatus = "faulted"
)

// TaskOutcomeError is a JSON-safe error snapshot persisted instead of a runtime error.
type TaskOutcomeError struct {
	Message string    `json:"message"`
	Detail  JsonValue `json:"detail,omitzero"`
}

// TaskOutcome is the durable reason and optional result recorded when a task becomes terminal.
type TaskOutcome struct {
	Status TaskOutcomeStatus    `json:"status"`
	Result jsonx.Opt[JsonValue] `json:"result,omitzero"`
	Error  *TaskOutcomeError    `json:"error,omitzero"`
	Reason *string              `json:"reason,omitzero"`
}

// TaskStateStatus classifies a task's execution state.
type TaskStateStatus string

const (
	TaskStatePending    TaskStateStatus = "pending"
	TaskStateRunning    TaskStateStatus = "running"
	TaskStateWaiting    TaskStateStatus = "waiting"
	TaskStateCompleting TaskStateStatus = "completing"
	TaskStateTerminal   TaskStateStatus = "terminal"
)

// TaskState is the complete durable execution state of a task.
type TaskState struct {
	Status     TaskStateStatus `json:"status"`
	Checkpoint JsonValue       `json:"checkpoint,omitzero"`
	On         []TaskId        `json:"on,omitzero"`
	Policy     JoinPolicy      `json:"policy,omitzero"`
	Outcome    *TaskOutcome    `json:"outcome,omitzero"`
}

// TaskRecord is the complete replacement record for one durable task state machine.
type TaskRecord struct {
	Id             TaskId         `json:"id"`
	ConversationId ConversationId `json:"conversationId"`
	Kind           string         `json:"kind"`
	Version        int            `json:"version"`
	Input          JsonValue      `json:"input"`
	Owner          *TaskId        `json:"owner,omitzero"`
	Background     bool           `json:"background"`
	AbortRequested bool           `json:"abortRequested"`
	State          TaskState      `json:"state"`
	Memos          *jsonx.Object  `json:"memos,omitzero"`
}

// RunningTaskRecord is a TaskRecord narrowed conceptually to status == "running".
type RunningTaskRecord = TaskRecord

// RunningTask is a typed live task record reserved by one invocation.
type RunningTask[I, S any] struct {
	Id             TaskId
	ConversationId ConversationId
	Kind           string
	Version        int
	Input          I
	Owner          *TaskId
	Background     bool
	AbortRequested bool
	Checkpoint     S
	Memos          *jsonx.Object
}

// NextTaskState is the typed next state a task commits for itself:
// Status "running" | "waiting" | "terminal".
type NextTaskState[S, R any] struct {
	Status     TaskStateStatus
	Checkpoint S
	On         []TaskId
	Policy     JoinPolicy
	Outcome    *TypedTaskOutcome[R]
}

// TypedTaskOutcome is a typed TaskOutcome for task implementations.
type TypedTaskOutcome[R any] struct {
	Status TaskOutcomeStatus
	Result R
	HasRes bool
	Error  *TaskOutcomeError
	Reason *string
}

// DocumentRecord is the persisted lifecycle record for one create-to-retire document incarnation.
type DocumentRecord struct {
	Id        DocumentId      `json:"id"`
	Kind      string          `json:"kind"`
	Key       *string         `json:"key,omitzero"`
	CreatedAt Seq             `json:"createdAt"`
	RetiredAt *Seq            `json:"retiredAt,omitzero"`
	Scope     DocumentScope   `json:"scope"`
	History   DocumentHistory `json:"history,omitzero"`
	Fork      DocumentFork    `json:"fork,omitzero"`
}

// DocumentCreate is supplied when storage creates and stamps a new DocumentRecord.
type DocumentCreate struct {
	Id      DocumentId      `json:"id"`
	Kind    string          `json:"kind"`
	Key     *string         `json:"key,omitzero"`
	Scope   DocumentScope   `json:"scope"`
	History DocumentHistory `json:"history,omitzero"`
	Fork    DocumentFork    `json:"fork,omitzero"`
}

// AsRecord converts a DocumentCreate into a DocumentRecord using createdAt seq.
func (c DocumentCreate) AsRecord(createdAt Seq) DocumentRecord {
	return DocumentRecord{
		Id:        c.Id,
		Kind:      c.Kind,
		Key:       docuCopyStringPtr(c.Key),
		CreatedAt: createdAt,
		Scope:     c.Scope,
		History:   c.History,
		Fork:      c.Fork,
	}
}

// Page holds one ordered scan result and its optional continuation state.
type Page[T, C any] struct {
	Items []T `json:"items"`
	Next  C   `json:"next,omitzero"`
}

// Cursor is backend-owned JSON continuation state that callers only round-trip to the same scan.
type Cursor = *jsonx.Object

// ConversationQuery specifies optional filters for an ordered conversation scan.
type ConversationQuery struct {
	OwnerConversationId *ConversationId `json:"ownerConversationId,omitzero"`
	OwnerTaskId         *TaskId         `json:"ownerTaskId,omitzero"`
}

// EntryQuery gives inclusive ID bounds for a newest-first scan of one conversation's fork-aware history.
type EntryQuery struct {
	ConversationId ConversationId `json:"conversationId"`
	MinEntryId     *EntryId       `json:"minEntryId,omitzero"`
	MaxEntryId     *EntryId       `json:"maxEntryId,omitzero"`
}

// TaskQuery specifies optional filters for an ordered scan of durable task records.
type TaskQuery struct {
	ConversationId *ConversationId `json:"conversationId,omitzero"`
	Kind           *string         `json:"kind,omitzero"`
	Status         TaskStateStatus `json:"status,omitzero"`
	AbortRequested *bool           `json:"abortRequested,omitzero"`
	Background     *bool           `json:"background,omitzero"`
}

// SubmissionQuery specifies optional filters for an ordered scan of submission records.
type SubmissionQuery struct {
	ConversationId *ConversationId  `json:"conversationId,omitzero"`
	Status         SubmissionStatus `json:"status,omitzero"`
}

// DocumentPoint selects either current state or a concrete commit sequence for document reads.
type DocumentPoint struct {
	seq     Seq
	current bool
}

// DocumentPointCurrent selects the latest current state.
func DocumentPointCurrent() DocumentPoint { return DocumentPoint{current: true} }

// DocumentPointSeq selects history at commit sequence seq.
func DocumentPointSeq(seq Seq) DocumentPoint { return DocumentPoint{seq: seq} }

// IsCurrent reports whether this point is "current".
func (p DocumentPoint) IsCurrent() bool { return p.current }

// Seq returns the commit sequence and true when not "current".
func (p DocumentPoint) Seq() (Seq, bool) {
	if p.current {
		return 0, false
	}
	return p.seq, true
}

// DocumentAddress is the exact logical identity of a singleton or one keyed family member.
type DocumentAddress struct {
	Kind  string        `json:"kind"`
	Scope DocumentScope `json:"scope"`
	Key   *string       `json:"key,omitzero"`
}

// DocumentQuery specifies an ordered scan of document incarnations alive in one exact scope at one point.
type DocumentQuery struct {
	Scope DocumentScope
	At    DocumentPoint
	Kind  *string
}

// DocumentContentKind is "base" or "delta".
type DocumentContentKind string

const (
	DocumentContentBase  DocumentContentKind = "base"
	DocumentContentDelta DocumentContentKind = "delta"
)

// DocumentContent is a complete checkpoint base or Chord operation delta selected by Session.
type DocumentContent struct {
	Version int                 `json:"version"`
	Kind    DocumentContentKind `json:"kind"`
	Value   JsonObject          `json:"value,omitzero"`
	Ops     chord.OpList        `json:"ops,omitzero"`
}

// DocumentCopySource names an exact persisted source for definition-free document copy.
type DocumentCopySource struct {
	Id DocumentId
	At DocumentPoint
}

// StoredDocument is a detached materialized value and stored definition version at a selected point.
type StoredDocument struct {
	Record          DocumentRecord `json:"record"`
	Version         int            `json:"version"`
	Value           JsonObject     `json:"value"`
	DeltasSinceBase int            `json:"deltasSinceBase"`
}

// StoredEntry pairs an entry record with the sequence of the commit that persisted it.
type StoredEntry struct {
	Entry     EntryRecord `json:"entry"`
	CommitSeq Seq         `json:"commitSeq"`
}

// StorageWriteType discriminates storage writes.
type StorageWriteType string

const (
	StorageWriteTypeConversation   StorageWriteType = "conversation"
	StorageWriteTypeEntry          StorageWriteType = "entry"
	StorageWriteTypeTask           StorageWriteType = "task"
	StorageWriteTypeSubmission     StorageWriteType = "submission"
	StorageWriteTypeDocumentCreate StorageWriteType = "document.create"
	StorageWriteTypeDocumentCopy   StorageWriteType = "document.copy"
	StorageWriteTypeDocumentChange StorageWriteType = "document.change"
	StorageWriteTypeDocumentRetire StorageWriteType = "document.retire"
)

// StorageWrite is one record or document mutation in an atomic storage commit.
type StorageWrite interface {
	isStorageWrite()
	WriteType() StorageWriteType
}

// CommitChange is one immutable change from a successful Session commit.
type CommitChange interface {
	isCommitChange()
	ChangeType() string
}

// StorageWriteConversation creates a conversation record.
type StorageWriteConversation struct {
	Type  StorageWriteType   `json:"type"`
	Value ConversationRecord `json:"value"`
}

func (StorageWriteConversation) isStorageWrite()             {}
func (StorageWriteConversation) isCommitChange()             {}
func (StorageWriteConversation) WriteType() StorageWriteType { return StorageWriteTypeConversation }
func (StorageWriteConversation) ChangeType() string          { return string(StorageWriteTypeConversation) }

// StorageWriteEntry appends a transcript entry record.
type StorageWriteEntry struct {
	Type  StorageWriteType `json:"type"`
	Value EntryRecord      `json:"value"`
}

func (StorageWriteEntry) isStorageWrite()             {}
func (StorageWriteEntry) isCommitChange()             {}
func (StorageWriteEntry) WriteType() StorageWriteType { return StorageWriteTypeEntry }
func (StorageWriteEntry) ChangeType() string          { return string(StorageWriteTypeEntry) }

// StorageWriteTask creates or replaces a task record.
type StorageWriteTask struct {
	Type  StorageWriteType `json:"type"`
	Value TaskRecord       `json:"value"`
}

func (StorageWriteTask) isStorageWrite()             {}
func (StorageWriteTask) isCommitChange()             {}
func (StorageWriteTask) WriteType() StorageWriteType { return StorageWriteTypeTask }
func (StorageWriteTask) ChangeType() string          { return string(StorageWriteTypeTask) }

// StorageWriteSubmission creates or replaces a submission record.
type StorageWriteSubmission struct {
	Type  StorageWriteType `json:"type"`
	Value SubmissionRecord `json:"value"`
}

func (StorageWriteSubmission) isStorageWrite()             {}
func (StorageWriteSubmission) isCommitChange()             {}
func (StorageWriteSubmission) WriteType() StorageWriteType { return StorageWriteTypeSubmission }
func (StorageWriteSubmission) ChangeType() string          { return string(StorageWriteTypeSubmission) }

// StorageWriteDocumentCreate creates a new document incarnation with its initial base value.
type StorageWriteDocumentCreate struct {
	Type    StorageWriteType `json:"type"`
	Record  DocumentCreate   `json:"record"`
	Content DocumentContent  `json:"content"`
}

func (StorageWriteDocumentCreate) isStorageWrite()             {}
func (StorageWriteDocumentCreate) WriteType() StorageWriteType { return StorageWriteTypeDocumentCreate }

// StorageWriteDocumentCopy creates a new conversation document copied from an existing source.
type StorageWriteDocumentCopy struct {
	Type   StorageWriteType   `json:"type"`
	Record DocumentCreate     `json:"record"`
	Source DocumentCopySource `json:"source"`
}

func (StorageWriteDocumentCopy) isStorageWrite()             {}
func (StorageWriteDocumentCopy) WriteType() StorageWriteType { return StorageWriteTypeDocumentCopy }

// StorageWriteDocumentChange records a base checkpoint or delta for an existing document.
type StorageWriteDocumentChange struct {
	Type    StorageWriteType `json:"type"`
	Id      DocumentId       `json:"id"`
	Content DocumentContent  `json:"content"`
}

func (StorageWriteDocumentChange) isStorageWrite()             {}
func (StorageWriteDocumentChange) WriteType() StorageWriteType { return StorageWriteTypeDocumentChange }

// StorageWriteDocumentRetire marks an existing document incarnation retired.
type StorageWriteDocumentRetire struct {
	Type StorageWriteType `json:"type"`
	Id   DocumentId       `json:"id"`
}

func (StorageWriteDocumentRetire) isStorageWrite()             {}
func (StorageWriteDocumentRetire) WriteType() StorageWriteType { return StorageWriteTypeDocumentRetire }

// DocumentCommitChange is implemented by document commit publication variants.
type DocumentCommitChange interface {
	CommitChange
	isDocumentCommitChange()
}

// DocumentCommitChangeUpdate publishes a committed change or retirement of one document incarnation.
type DocumentCommitChangeUpdate struct {
	Type           string          `json:"type"` // "document"
	Record         DocumentRecord  `json:"record"`
	ConversationId *ConversationId `json:"conversationId,omitzero"`
	Version        *int            `json:"version,omitzero"`
	Value          JsonObject      `json:"value"` // nil for retirement
	Ops            chord.OpList    `json:"ops"`
}

func (DocumentCommitChangeUpdate) isCommitChange()         {}
func (DocumentCommitChangeUpdate) isDocumentCommitChange() {}
func (DocumentCommitChangeUpdate) ChangeType() string      { return "document" }

// DocumentCommitChangeCopy publishes a definition-free child initialization copy.
type DocumentCommitChangeCopy struct {
	Type           string             `json:"type"` // "document.copy"
	Record         DocumentRecord     `json:"record"`
	ConversationId ConversationId     `json:"conversationId"`
	Source         DocumentCopySource `json:"source"`
}

func (DocumentCommitChangeCopy) isCommitChange()         {}
func (DocumentCommitChangeCopy) isDocumentCommitChange() {}
func (DocumentCommitChangeCopy) ChangeType() string      { return "document.copy" }

// CommitPublication holds every immutable change from one successful Session commit.
type CommitPublication struct {
	Seq     Seq            `json:"seq"`
	Changes []CommitChange `json:"changes"`
}

// ConversationCreateTxOptions configures Tx.CreateConversation.
type ConversationCreateTxOptions struct {
	Ownership ConversationOwnership
}

// Tx is the transaction surface of one Session commit callback.
type Tx interface {
	Conversation(id ConversationId) (*ConversationRecord, error)
	Entry(id EntryId) (*EntryRecord, error)
	TypedEntry(token *Entry, id EntryId) (*EntryRecord, error)
	Task(id TaskId) (*TaskRecord, error)
	ScanConversations(query ConversationQuery, limit int, cursor Cursor) (Page[ConversationRecord, Cursor], error)
	ScanEntries(query EntryQuery, limit int, cursor Cursor) (Page[EntryRecord, Cursor], error)
	LatestHeadMarker(conversationId ConversationId) (*EntryRecord, error)
	ScanTasks(query TaskQuery, limit int, cursor Cursor) (Page[TaskRecord, Cursor], error)
	SubmissionByRequest(conversationId ConversationId, requestId string) (*SubmissionRecord, error)

	CreateConversation(options ConversationCreateTxOptions) (*ConversationRecord, error)
	ForkConversation(parentConversationId ConversationId, at EntryId, options ConversationCreateTxOptions) (*ConversationRecord, error)
	AppendEntry(conversationId ConversationId, value EntryDraft) (*EntryRecord, error)
	AppendTypedEntry(token *Entry, conversationId ConversationId, value EntryDraft) (*EntryRecord, error)
	CreateTask(task AnyTask, input any, options TaskOptions) (TaskId, error)
	CreateSubmission(create SubmissionCreate) (*SubmissionRecord, error)
	SettleSubmission(id SubmissionId, settlement SubmissionSettlement) error
	PlaceSubmission(id SubmissionId, entry EntryId) error

	Doc(token AnyDocToken, args ...any) (chord.Draft[JsonObject], error)
	RetireDoc(token AnyDocToken, args ...any) error
}

// DocumentState is a disposable, read-only Chord state bound to one committed document incarnation.
type DocumentState = chord.AttachedReplicatedState[JsonObject]

// WatchEndReason explains why a WatchHandle terminated.
type WatchEndReason string

const (
	WatchEndStopped       WatchEndReason = "stopped"
	WatchEndCancelled     WatchEndReason = "cancelled"
	WatchEndSessionClosed WatchEndReason = "session_closed"
	WatchEndRetired       WatchEndReason = "retired"
	WatchEndListenerError WatchEndReason = "listener_error"
)

// WatchEnd is the terminal result of one WatchHandle.
type WatchEnd struct {
	Reason WatchEndReason
	Error  error
}

// WatchHandle provides serialized exact-frame observation of an immutable value with bounded pending delivery.
type WatchHandle[T any] interface {
	// Value returns the acquisition revision before Start, or the latest delivered immutable revision afterward.
	Value() T
	// Start installs the sole asynchronous listener. Never invokes it inline.
	Start(listener func(value T, ops []chord.Op, ctx chord.Context) error) error
	// Stop idempotently stops future callbacks and waits for termination, returning WatchEnd.
	Stop() WatchEnd
	// Closed blocks until the watch terminates and returns WatchEnd. An already-running callback remains caller-owned.
	Closed() WatchEnd
	// Done is closed when the watch terminates.
	Done() <-chan struct{}
}

// DocumentWatch is a serialized watch on one document incarnation (nil value = retired).
type DocumentWatch = WatchHandle[JsonObject]

// DocumentReader reads committed documents.
type DocumentReader interface {
	Snapshot(ctx chord.Context, token AnyDocToken, args ...any) (JsonObject, error)
	SnapshotAsOf(ctx chord.Context, token AnyDocToken, conversationId ConversationId, args ...any) (JsonObject, error)
}

// DocumentObserver acquires non-creating document watches.
type DocumentObserver interface {
	WatchDoc(ctx chord.Context, token AnyDocToken, args ...any) (DocumentWatch, error)
}

// Session owns one mutation line, its records, and its tracked documents.
type Session interface {
	DocumentObserver
	DocumentReader

	Commit(ctx chord.Context, change func(tx Tx) error) error
	Close(ctx chord.Context) error
	SubscribeCommits(listener func(publication CommitPublication, ctx chord.Context)) func()
	SubscribeClose(listener func()) func()

	DocumentState(ctx chord.Context, token AnyDocToken, args ...any) (DocumentState, error)
}

// Storage is the atomic persistence boundary for Session records.
type Storage interface {
	Commit(ctx chord.Context, writes []StorageWrite) (Seq, error)
	MintId() (int64, error)

	Conversation(ctx chord.Context, id ConversationId) (*ConversationRecord, error)
	ScanConversations(ctx chord.Context, query ConversationQuery, limit int, cursor Cursor) (Page[ConversationRecord, Cursor], error)

	Entry(ctx chord.Context, id EntryId) (*StoredEntry, error)
	EntryInConversation(ctx chord.Context, conversationId ConversationId, id EntryId) (*StoredEntry, error)
	FindLatestHeadMarker(ctx chord.Context, conversationId ConversationId, atOrBeforeEntryId *EntryId) (*EntryRecord, error)
	ScanEntries(ctx chord.Context, query EntryQuery, limit int, cursor Cursor) (Page[EntryRecord, Cursor], error)

	Task(ctx chord.Context, id TaskId) (*TaskRecord, error)
	ScanTasks(ctx chord.Context, query TaskQuery, limit int, cursor Cursor) (Page[TaskRecord, Cursor], error)

	Submission(ctx chord.Context, id SubmissionId) (*SubmissionRecord, error)
	ScanSubmissions(ctx chord.Context, query SubmissionQuery, limit int, cursor Cursor) (Page[SubmissionRecord, Cursor], error)
	SubmissionByRequest(ctx chord.Context, conversationId ConversationId, requestId string) (*SubmissionRecord, error)

	FindDocument(ctx chord.Context, address DocumentAddress, at DocumentPoint) (*DocumentRecord, error)
	Document(ctx chord.Context, id DocumentId, at DocumentPoint) (*StoredDocument, error)
	ScanDocuments(ctx chord.Context, query DocumentQuery, limit int, cursor Cursor) (Page[DocumentRecord, Cursor], error)

	Close(ctx chord.Context) error
}

// AnyTask is an erased executable task definition stored in the registry.
type AnyTask interface {
	erasedDefinition() *AnyTaskDefinition
	TaskKind() string
	TaskVersion() int
}

// AnyPhaseHandler executes one checkpoint phase or abort handler in erased form.
type AnyPhaseHandler func(ctx chord.Context, task *RunningTaskRecord, runtime AnyTaskRuntime) error

// AnyTaskDefinition holds the erased task definition hooks and phases used by the scheduler.
type AnyTaskDefinition struct {
	Name    string
	Version int
	Initial func(input any) (JsonValue, JsonValue, error)
	Phases  map[string]AnyPhaseHandler
	Abort   AnyPhaseHandler
	Migrate func(input JsonValue, checkpoint JsonValue, fromVersion int) (JsonValue, JsonValue, error)
	Hooks   any
}

// AnyHookRunner dispatches hooks dynamically for a task.
type AnyHookRunner interface {
	EachRaw(invoke func(handler any) error) error
}

// AnyTaskRuntime is the erased task runtime passed to AnyPhaseHandler.
type AnyTaskRuntime interface {
	DocumentObserver
	DocumentReader

	TaskId() TaskId
	ConversationId() ConversationId
	Signal() context.Context
	Registry() RegistrySnapshot
	Agent(ctx chord.Context) (*Agent, error)
	Settings() Settings
	Models() ai.Models
	Env(ctx chord.Context) (env.ExecutionEnv, error)
	HooksAny() AnyHookRunner

	Commit(ctx chord.Context, change func(tx Tx, current *RunningTaskRecord) (*TaskState, bool, error)) error
	Memo(ctx chord.Context, name string) (JsonValue, bool, error)
	MemoPut(ctx chord.Context, name string, candidate JsonValue) (JsonValue, error)
	GetTask(ctx chord.Context, id TaskId) (*TaskRecord, error)
	WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error)
	Outcomes(ctx chord.Context, ids []TaskId) ([]TaskOutcome, error)
	Conversation(ctx chord.Context, id ConversationId) (ConversationHandle, error)
	Entry(ctx chord.Context, id EntryId) (*EntryRecord, error)
	TypedEntry(ctx chord.Context, token *Entry, id EntryId) (*EntryRecord, error)
	Context(ctx chord.Context, conversationId ConversationId, at ...EntryId) (*ContextView, error)
	Now() int64
	Report(err error)
	Sleep(ctx chord.Context, until int64) error
}

// HookRunner dispatches registered hooks of type H in registry order of the phase snapshot.
type HookRunner[H any] struct {
	raw AnyHookRunner
}

// Each invokes invoke with each matching registered handler of type H.
func (r HookRunner[H]) Each(invoke func(handler H) error) error {
	if r.raw == nil {
		return nil
	}
	return r.raw.EachRaw(func(rawHandler any) error {
		typed, ok := tskConvertHooks[H](rawHandler)
		if !ok {
			return nil
		}
		return invoke(typed)
	})
}

// TaskRuntime provides typed task invocation operations backed by AnyTaskRuntime.
type TaskRuntime[I, S, R, H any] struct {
	base  AnyTaskRuntime
	hooks HookRunner[H]
}

// Base returns the underlying erased AnyTaskRuntime.
func (r *TaskRuntime[I, S, R, H]) Base() AnyTaskRuntime { return r.base }

func (r *TaskRuntime[I, S, R, H]) TaskId() TaskId                 { return r.base.TaskId() }
func (r *TaskRuntime[I, S, R, H]) ConversationId() ConversationId { return r.base.ConversationId() }
func (r *TaskRuntime[I, S, R, H]) Signal() context.Context        { return r.base.Signal() }
func (r *TaskRuntime[I, S, R, H]) Registry() RegistrySnapshot     { return r.base.Registry() }
func (r *TaskRuntime[I, S, R, H]) Agent(ctx chord.Context) (*Agent, error) {
	return r.base.Agent(ctx)
}
func (r *TaskRuntime[I, S, R, H]) Settings() Settings { return r.base.Settings() }
func (r *TaskRuntime[I, S, R, H]) Models() ai.Models  { return r.base.Models() }
func (r *TaskRuntime[I, S, R, H]) Env(ctx chord.Context) (env.ExecutionEnv, error) {
	return r.base.Env(ctx)
}
func (r *TaskRuntime[I, S, R, H]) Hooks() HookRunner[H] { return r.hooks }

func (r *TaskRuntime[I, S, R, H]) Snapshot(ctx chord.Context, token AnyDocToken, args ...any) (JsonObject, error) {
	return r.base.Snapshot(ctx, token, args...)
}

func (r *TaskRuntime[I, S, R, H]) SnapshotAsOf(ctx chord.Context, token AnyDocToken, conversationId ConversationId, args ...any) (JsonObject, error) {
	return r.base.SnapshotAsOf(ctx, token, conversationId, args...)
}

func (r *TaskRuntime[I, S, R, H]) WatchDoc(ctx chord.Context, token AnyDocToken, args ...any) (DocumentWatch, error) {
	return r.base.WatchDoc(ctx, token, args...)
}

// Commit commits on the Session line after rereading the task. Returning nil leaves the task state unchanged.
func (r *TaskRuntime[I, S, R, H]) Commit(
	ctx chord.Context,
	change func(tx Tx, current RunningTask[I, S]) (*NextTaskState[S, R], error),
) error {
	return r.base.Commit(ctx, func(tx Tx, raw *RunningTaskRecord) (*TaskState, bool, error) {
		running, err := tskDecodeRunning[I, S](raw)
		if err != nil {
			return nil, false, err
		}
		next, err := change(tx, running)
		if err != nil {
			return nil, false, err
		}
		if next == nil {
			return nil, false, nil
		}
		state := TaskState{Status: next.Status}
		switch next.Status {
		case TaskStateRunning:
			cpJson, err := tskEncodeJsonValue(next.Checkpoint)
			if err != nil {
				return nil, false, err
			}
			state.Checkpoint = cpJson
		case TaskStateWaiting:
			cpJson, err := tskEncodeJsonValue(next.Checkpoint)
			if err != nil {
				return nil, false, err
			}
			state.Checkpoint = cpJson
			state.On = next.On
			state.Policy = next.Policy
		case TaskStateTerminal:
			if next.Outcome == nil {
				return nil, false, fmt.Errorf("terminal task state requires an outcome")
			}
			out := &TaskOutcome{
				Status: next.Outcome.Status,
				Error:  next.Outcome.Error,
				Reason: next.Outcome.Reason,
			}
			if next.Outcome.Status == TaskOutcomeCompleted || next.Outcome.HasRes {
				resJson, err := tskEncodeJsonValue(next.Outcome.Result)
				if err != nil {
					return nil, false, err
				}
				if resJson == nil {
					out.Result = jsonx.Null[JsonValue]()
				} else {
					out.Result = jsonx.Some(resJson)
				}
			}
			state.Outcome = out
		default:
			return nil, false, fmt.Errorf("invalid NextTaskState status %q", next.Status)
		}
		return &state, true, nil
	})
}

func (r *TaskRuntime[I, S, R, H]) Memo(ctx chord.Context, name string) (JsonValue, bool, error) {
	return r.base.Memo(ctx, name)
}

func (r *TaskRuntime[I, S, R, H]) MemoPut(ctx chord.Context, name string, candidate JsonValue) (JsonValue, error) {
	return r.base.MemoPut(ctx, name, candidate)
}

func (r *TaskRuntime[I, S, R, H]) GetTask(ctx chord.Context, id TaskId) (*TaskRecord, error) {
	return r.base.GetTask(ctx, id)
}

func (r *TaskRuntime[I, S, R, H]) WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error) {
	return r.base.WaitForTask(ctx, id)
}

func (r *TaskRuntime[I, S, R, H]) Outcomes(ctx chord.Context, ids []TaskId) ([]TaskOutcome, error) {
	return r.base.Outcomes(ctx, ids)
}

func (r *TaskRuntime[I, S, R, H]) Conversation(ctx chord.Context, id ConversationId) (ConversationHandle, error) {
	return r.base.Conversation(ctx, id)
}

func (r *TaskRuntime[I, S, R, H]) Entry(ctx chord.Context, id EntryId) (*EntryRecord, error) {
	return r.base.Entry(ctx, id)
}

func (r *TaskRuntime[I, S, R, H]) TypedEntry(ctx chord.Context, token *Entry, id EntryId) (*EntryRecord, error) {
	return r.base.TypedEntry(ctx, token, id)
}

func (r *TaskRuntime[I, S, R, H]) Context(ctx chord.Context, conversationId ConversationId, at ...EntryId) (*ContextView, error) {
	return r.base.Context(ctx, conversationId, at...)
}

func (r *TaskRuntime[I, S, R, H]) Now() int64       { return r.base.Now() }
func (r *TaskRuntime[I, S, R, H]) Report(err error) { r.base.Report(err) }
func (r *TaskRuntime[I, S, R, H]) Sleep(ctx chord.Context, until int64) error {
	return r.base.Sleep(ctx, until)
}

// PhaseHandler runs one checkpoint phase of a durable task.
type PhaseHandler[I, S, R, H any] func(ctx chord.Context, task RunningTask[I, S], runtime *TaskRuntime[I, S, R, H]) error

// TaskDefinition declares an executable durable state machine registered by Name.
type TaskDefinition[I, S, R, H any] struct {
	Name    string
	Version int
	Initial func(input I) S
	Phases  map[string]PhaseHandler[I, S, R, H]
	Abort   PhaseHandler[I, S, R, H]
	Migrate func(input JsonValue, checkpoint JsonValue, fromVersion int) (I, S, error)
	Hooks   H
}
