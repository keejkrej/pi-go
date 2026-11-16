// Ported from packages/durable/test/session-support.ts (pi v1.0.0).

package testkit

import (
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// TestContext is the shared background chord.Context used across durable tests.
var TestContext chord.Context = chord.BackgroundContext

// Gate holds calls in ControlledStorage until Release is called and signals Entered on first arrival.
type Gate struct {
	Entered <-chan struct{}
	release func()
}

// Release stops holding future calls on this gate and unblocks any caller waiting on it.
func (g *Gate) Release() {
	if g != nil && g.release != nil {
		g.release()
	}
}

type tkssHeldGate struct {
	enteredCh   chan struct{}
	enteredOnce sync.Once
	gateCh      chan struct{}
	gateOnce    sync.Once
}

func tkssNewHeldGate() *tkssHeldGate {
	return &tkssHeldGate{
		enteredCh: make(chan struct{}),
		gateCh:    make(chan struct{}),
	}
}

func (g *tkssHeldGate) markEntered() {
	g.enteredOnce.Do(func() {
		close(g.enteredCh)
	})
}

func (g *tkssHeldGate) release() {
	g.gateOnce.Do(func() {
		close(g.gateCh)
	})
}

// ControlledStorage wraps *storage.MemoryStorage with observable commits, held calls, and injected failures.
type ControlledStorage struct {
	*storage.MemoryStorage

	mu                sync.Mutex
	admittedCommits   [][]durable.StorageWrite
	commits           [][]durable.StorageWrite
	mintCount         int
	documentReadCount int
	commitGate        *tkssHeldGate
	findGate          *tkssHeldGate
	commitFailure     error
	closedCh          chan struct{}
	closeOnce         sync.Once
}

// NewControlledStorage constructs a ready ControlledStorage backed by an empty MemoryStorage.
func NewControlledStorage() *ControlledStorage {
	return &ControlledStorage{
		MemoryStorage: storage.NewMemoryStorage(),
		closedCh:      make(chan struct{}),
	}
}

// AdmittedCommits returns the exact write batches received by Commit.
func (s *ControlledStorage) AdmittedCommits() [][]durable.StorageWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]durable.StorageWrite, len(s.admittedCommits))
	copy(out, s.admittedCommits)
	return out
}

// Commits returns detached deep-cloned write batches received by Commit.
func (s *ControlledStorage) Commits() [][]durable.StorageWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]durable.StorageWrite, len(s.commits))
	copy(out, s.commits)
	return out
}

// MintCount returns how many times MintId was called.
func (s *ControlledStorage) MintCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mintCount
}

// DocumentReadCount returns how many times Document was called.
func (s *ControlledStorage) DocumentReadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.documentReadCount
}

// HoldCommits blocks future Commit calls until the returned Gate is released or Crash/Close happens.
func (s *ControlledStorage) HoldCommits() *Gate {
	held := tkssNewHeldGate()
	s.mu.Lock()
	s.commitGate = held
	s.mu.Unlock()
	return &Gate{
		Entered: held.enteredCh,
		release: func() { s.releaseGate("commit", held) },
	}
}

// HoldFindDocument blocks future FindDocument calls until the returned Gate is released.
func (s *ControlledStorage) HoldFindDocument() *Gate {
	held := tkssNewHeldGate()
	s.mu.Lock()
	s.findGate = held
	s.mu.Unlock()
	return &Gate{
		Entered: held.enteredCh,
		release: func() { s.releaseGate("find", held) },
	}
}

// Crash simulates an in-flight crash during a held commit: the current commit never completes,
// while future commits bypass the cleared gate.
func (s *ControlledStorage) Crash() {
	s.mu.Lock()
	s.commitGate = nil
	s.mu.Unlock()
}

// FailNextCommit causes the next Commit call to fail with err after gate release.
func (s *ControlledStorage) FailNextCommit(err error) {
	s.mu.Lock()
	s.commitFailure = err
	s.mu.Unlock()
}

func (s *ControlledStorage) releaseGate(kind string, held *tkssHeldGate) {
	s.mu.Lock()
	if kind == "commit" && s.commitGate == held {
		s.commitGate = nil
	}
	if kind == "find" && s.findGate == held {
		s.findGate = nil
	}
	s.mu.Unlock()
	held.release()
}

// Commit records writes, waits on any active commit gate, applies injected failures, and delegates to MemoryStorage.
func (s *ControlledStorage) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	cloned := tkssCloneWrites(writes)
	s.mu.Lock()
	s.admittedCommits = append(s.admittedCommits, writes)
	s.commits = append(s.commits, cloned)
	held := s.commitGate
	s.mu.Unlock()

	if held != nil {
		held.markEntered()
		select {
		case <-held.gateCh:
		case <-s.closedCh:
		}
	}

	s.mu.Lock()
	failure := s.commitFailure
	if failure != nil {
		s.commitFailure = nil
	}
	s.mu.Unlock()
	if failure != nil {
		return 0, failure
	}
	return s.MemoryStorage.Commit(ctx, writes)
}

// MintId counts mint calls and delegates to MemoryStorage.
func (s *ControlledStorage) MintId() (int64, error) {
	s.mu.Lock()
	s.mintCount++
	s.mu.Unlock()
	return s.MemoryStorage.MintId()
}

// Document counts document reads and delegates to MemoryStorage.
func (s *ControlledStorage) Document(ctx chord.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	s.mu.Lock()
	s.documentReadCount++
	s.mu.Unlock()
	return s.MemoryStorage.Document(ctx, id, at)
}

// FindDocument waits on any active find gate and delegates to MemoryStorage.
func (s *ControlledStorage) FindDocument(ctx chord.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	s.mu.Lock()
	held := s.findGate
	s.mu.Unlock()
	if held != nil {
		held.markEntered()
		select {
		case <-held.gateCh:
		case <-s.closedCh:
		}
	}
	return s.MemoryStorage.FindDocument(ctx, address, at)
}

// Close releases any parked crashed calls and closes the underlying MemoryStorage.
func (s *ControlledStorage) Close(ctx chord.Context) error {
	s.closeOnce.Do(func() {
		close(s.closedCh)
	})
	return s.MemoryStorage.Close(ctx)
}

var _ durable.Storage = (*ControlledStorage)(nil)

// TestSession pairs a SessionImpl with its ControlledStorage and collected publications.
type TestSession struct {
	Storage      *ControlledStorage
	Session      *durable.SessionImpl
	mu           sync.Mutex
	publications []durable.CommitPublication
	unsub        func()
}

// OpenTestSession creates a ControlledStorage, a SessionImpl, and subscribes to all commit publications.
func OpenTestSession() *TestSession {
	st := NewControlledStorage()
	sess := durable.NewSession(st).(*durable.SessionImpl)
	ts := &TestSession{
		Storage: st,
		Session: sess,
	}
	ts.unsub = sess.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
		ts.mu.Lock()
		ts.publications = append(ts.publications, pub)
		ts.mu.Unlock()
	})
	return ts
}

// Publications returns a snapshot copy of every commit publication observed so far.
func (t *TestSession) Publications() []durable.CommitPublication {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]durable.CommitPublication, len(t.publications))
	copy(out, t.publications)
	return out
}

// DocumentChanges returns all "document" changes from one commit publication.
func DocumentChanges(publication durable.CommitPublication) []durable.DocumentCommitChangeUpdate {
	var out []durable.DocumentCommitChangeUpdate
	for _, c := range publication.Changes {
		switch dc := c.(type) {
		case durable.DocumentCommitChangeUpdate:
			out = append(out, dc)
		case *durable.DocumentCommitChangeUpdate:
			if dc != nil {
				out = append(out, *dc)
			}
		}
	}
	return out
}

// DocumentCopyChanges returns all "document.copy" changes from one commit publication.
func DocumentCopyChanges(publication durable.CommitPublication) []durable.DocumentCommitChangeCopy {
	var out []durable.DocumentCommitChangeCopy
	for _, c := range publication.Changes {
		switch dc := c.(type) {
		case durable.DocumentCommitChangeCopy:
			out = append(out, dc)
		case *durable.DocumentCommitChangeCopy:
			if dc != nil {
				out = append(out, *dc)
			}
		}
	}
	return out
}

// CreateConversation creates one ownerless conversation and returns its ID.
func CreateConversation(session durable.Session) (durable.ConversationId, error) {
	var id durable.ConversationId
	err := session.Commit(TestContext, func(tx durable.Tx) error {
		rec, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if err != nil {
			return err
		}
		id = rec.Id
		return nil
	})
	return id, err
}

// Flush yields execution so background goroutines can progress.
func Flush() {
	runtime.Gosched()
	time.Sleep(time.Millisecond)
}

func tkssCopyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func tkssCloneMessageList(msgs ai.MessageList) ai.MessageList {
	if msgs == nil {
		return nil
	}
	raw, err := jsonx.Marshal(msgs)
	if err != nil {
		return slices.Clone(msgs)
	}
	var out ai.MessageList
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		return slices.Clone(msgs)
	}
	return out
}

func tkssCloneOps(ops chord.OpList) chord.OpList {
	if ops == nil {
		return nil
	}
	raw, err := jsonx.Marshal(ops)
	if err != nil {
		return slices.Clone(ops)
	}
	var out chord.OpList
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		return slices.Clone(ops)
	}
	return out
}

func tkssCloneContent(c durable.DocumentContent) durable.DocumentContent {
	var val durable.JsonObject
	if c.Value != nil {
		val = c.Value.Clone()
	}
	return durable.DocumentContent{
		Version: c.Version,
		Kind:    c.Kind,
		Value:   val,
		Ops:     tkssCloneOps(c.Ops),
	}
}

func tkssCloneDocumentCreate(d durable.DocumentCreate) durable.DocumentCreate {
	return durable.DocumentCreate{
		Id:      d.Id,
		Kind:    d.Kind,
		Key:     tkssCopyPtr(d.Key),
		Scope:   d.Scope,
		History: d.History,
		Fork:    d.Fork,
	}
}

func tkssCloneWrites(writes []durable.StorageWrite) []durable.StorageWrite {
	if writes == nil {
		return nil
	}
	out := make([]durable.StorageWrite, len(writes))
	for i, w := range writes {
		switch cw := w.(type) {
		case durable.StorageWriteConversation:
			v := cw.Value
			v.Parent = tkssCopyPtr(cw.Value.Parent)
			v.Owner = tkssCopyPtr(cw.Value.Owner)
			out[i] = durable.StorageWriteConversation{Type: cw.Type, Value: v}
		case *durable.StorageWriteConversation:
			v := cw.Value
			v.Parent = tkssCopyPtr(cw.Value.Parent)
			v.Owner = tkssCopyPtr(cw.Value.Owner)
			out[i] = durable.StorageWriteConversation{Type: cw.Type, Value: v}
		case durable.StorageWriteEntry:
			v := cw.Value
			v.Model = tkssCloneMessageList(cw.Value.Model)
			v.Data = jsonx.CloneValue(cw.Value.Data)
			v.Head = tkssCopyPtr(cw.Value.Head)
			v.ByTaskId = tkssCopyPtr(cw.Value.ByTaskId)
			if cw.Value.Edits != nil {
				v.Edits = make([]durable.ContextEdit, len(cw.Value.Edits))
				for j, ed := range cw.Value.Edits {
					v.Edits[j] = durable.ContextEdit{
						Target:   ed.Target,
						Action:   ed.Action,
						Messages: tkssCloneMessageList(ed.Messages),
					}
				}
			}
			out[i] = durable.StorageWriteEntry{Type: cw.Type, Value: v}
		case *durable.StorageWriteEntry:
			v := cw.Value
			v.Model = tkssCloneMessageList(cw.Value.Model)
			v.Data = jsonx.CloneValue(cw.Value.Data)
			v.Head = tkssCopyPtr(cw.Value.Head)
			v.ByTaskId = tkssCopyPtr(cw.Value.ByTaskId)
			if cw.Value.Edits != nil {
				v.Edits = make([]durable.ContextEdit, len(cw.Value.Edits))
				for j, ed := range cw.Value.Edits {
					v.Edits[j] = durable.ContextEdit{
						Target:   ed.Target,
						Action:   ed.Action,
						Messages: tkssCloneMessageList(ed.Messages),
					}
				}
			}
			out[i] = durable.StorageWriteEntry{Type: cw.Type, Value: v}
		case durable.StorageWriteTask:
			t := cw.Value
			t.Input = jsonx.CloneValue(cw.Value.Input)
			t.Owner = tkssCopyPtr(cw.Value.Owner)
			t.State = durable.TaskState{
				Status:     cw.Value.State.Status,
				Checkpoint: jsonx.CloneValue(cw.Value.State.Checkpoint),
				On:         slices.Clone(cw.Value.State.On),
				Policy:     cw.Value.State.Policy,
			}
			if cw.Value.State.Outcome != nil {
				oc := *cw.Value.State.Outcome
				if val, ok := oc.Result.Get(); ok {
					oc.Result = jsonx.Some(jsonx.CloneValue(val))
				}
				oc.Reason = tkssCopyPtr(oc.Reason)
				if oc.Error != nil {
					oc.Error = &durable.TaskOutcomeError{
						Message: oc.Error.Message,
						Detail:  jsonx.CloneValue(oc.Error.Detail),
					}
				}
				t.State.Outcome = &oc
			}
			if cw.Value.Memos != nil {
				t.Memos = cw.Value.Memos.Clone()
			}
			out[i] = durable.StorageWriteTask{Type: cw.Type, Value: t}
		case *durable.StorageWriteTask:
			v := *cw
			out[i] = tkssCloneWrites([]durable.StorageWrite{v})[0]
		case durable.StorageWriteSubmission:
			s := cw.Value
			s.RequestId = tkssCopyPtr(cw.Value.RequestId)
			s.Entry = tkssCopyPtr(cw.Value.Entry)
			s.Answer = tkssCopyPtr(cw.Value.Answer)
			s.Reason = tkssCopyPtr(cw.Value.Reason)
			s.Detail = jsonx.CloneValue(cw.Value.Detail)
			out[i] = durable.StorageWriteSubmission{Type: cw.Type, Value: s}
		case *durable.StorageWriteSubmission:
			s := cw.Value
			s.RequestId = tkssCopyPtr(cw.Value.RequestId)
			s.Entry = tkssCopyPtr(cw.Value.Entry)
			s.Answer = tkssCopyPtr(cw.Value.Answer)
			s.Reason = tkssCopyPtr(cw.Value.Reason)
			s.Detail = jsonx.CloneValue(cw.Value.Detail)
			out[i] = durable.StorageWriteSubmission{Type: cw.Type, Value: s}
		case durable.StorageWriteDocumentCreate:
			out[i] = durable.StorageWriteDocumentCreate{
				Type:    cw.Type,
				Record:  tkssCloneDocumentCreate(cw.Record),
				Content: tkssCloneContent(cw.Content),
			}
		case *durable.StorageWriteDocumentCreate:
			out[i] = durable.StorageWriteDocumentCreate{
				Type:    cw.Type,
				Record:  tkssCloneDocumentCreate(cw.Record),
				Content: tkssCloneContent(cw.Content),
			}
		case durable.StorageWriteDocumentCopy:
			out[i] = durable.StorageWriteDocumentCopy{
				Type:   cw.Type,
				Record: tkssCloneDocumentCreate(cw.Record),
				Source: cw.Source,
			}
		case *durable.StorageWriteDocumentCopy:
			out[i] = durable.StorageWriteDocumentCopy{
				Type:   cw.Type,
				Record: tkssCloneDocumentCreate(cw.Record),
				Source: cw.Source,
			}
		case durable.StorageWriteDocumentChange:
			out[i] = durable.StorageWriteDocumentChange{
				Type:    cw.Type,
				Id:      cw.Id,
				Content: tkssCloneContent(cw.Content),
			}
		case *durable.StorageWriteDocumentChange:
			out[i] = durable.StorageWriteDocumentChange{
				Type:    cw.Type,
				Id:      cw.Id,
				Content: tkssCloneContent(cw.Content),
			}
		default:
			out[i] = w
		}
	}
	return out
}
