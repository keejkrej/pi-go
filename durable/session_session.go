// Ported from packages/durable/src/session/session.ts (pi v1.0.0).

package durable

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
)

// SessionHooks holds internal lifecycle hooks used by HarnessImpl.
type SessionHooks struct {
	ConversationCreated func(tx *Transaction, record ConversationRecord) error
	BeforeClose         func()
}

// LoadedDocumentView is the materialized document view returned by ConversationDocumentOnLine.
type LoadedDocumentView struct {
	Record  DocumentRecord
	Version int
	Value   JsonObject
}

// NewSession opens a Session kernel over one storage backend.
func NewSession(storage Storage) Session {
	return NewSessionWithHooks(storage, SessionHooks{})
}

// NewSessionWithHooks opens a SessionImpl with internal Harness hooks.
func NewSessionWithHooks(storage Storage, hooks SessionHooks) *SessionImpl {
	s := &SessionImpl{
		storage:         storage,
		hooks:           hooks,
		documents:       make(map[string]*stLoadedDocument),
		commitListeners: omap.NewSet[*ssCommitSubscription](),
		closeListeners:  omap.NewSet[*ssCloseSubscription](),
	}
	s.host = &ssTransactionHost{session: s}
	return s
}

type ssCommitSubscription struct {
	fn func(publication CommitPublication, ctx chord.Context)
}

type ssCloseSubscription struct {
	fn func()
}

// SessionImpl owns one mutation line, the loaded document tracker cache, and committed publication.
type SessionImpl struct {
	storage Storage
	hooks   SessionHooks
	host    *ssTransactionHost

	// Mutation line FIFO queue.
	lineMu    sync.Mutex
	lineBusy  bool
	lineQueue []chan struct{}

	// Loaded document tracker cache. Writes happen on the mutation line; Snapshot fast-path reads under docsMu.
	docsMu    sync.Mutex
	documents map[string]*stLoadedDocument

	// State/listeners mutex; always released before calling external listeners.
	stateMu         sync.Mutex
	closingStarted  bool
	closeNotifyDone chan struct{}
	closeDone       chan struct{}
	closeErr        error
	poison          error
	commitListeners *omap.Set[*ssCommitSubscription]
	closeListeners  *omap.Set[*ssCloseSubscription]
}

var _ Session = (*SessionImpl)(nil)

type ssTransactionHost struct {
	session *SessionImpl
}

func (h *ssTransactionHost) Storage() Storage { return h.session.storage }

func (h *ssTransactionHost) Cached(addressId string) *stLoadedDocument {
	h.session.docsMu.Lock()
	defer h.session.docsMu.Unlock()
	return h.session.documents[addressId]
}

func (h *ssTransactionHost) Load(
	ctx chord.Context,
	definition *AnyDocDefinition,
	addressId string,
	address DocumentAddress,
) (*stLoadedDocument, error) {
	return h.session.loadDocument(ctx, definition, addressId, address)
}

func (h *ssTransactionHost) Install(document *stLoadedDocument) {
	h.session.docsMu.Lock()
	h.session.documents[document.addressId] = document
	h.session.docsMu.Unlock()
}

func (h *ssTransactionHost) Evict(addressId string, recordId DocumentId) {
	h.session.docsMu.Lock()
	if cur := h.session.documents[addressId]; cur != nil && cur.record.Id == recordId {
		delete(h.session.documents, addressId)
	}
	h.session.docsMu.Unlock()
}

func (h *ssTransactionHost) ConversationCreated(tx *Transaction, record ConversationRecord) error {
	if h.session.hooks.ConversationCreated != nil {
		return h.session.hooks.ConversationCreated(tx, record)
	}
	return nil
}

func (s *SessionImpl) Commit(ctx chord.Context, change func(tx Tx) error) error {
	if change == nil {
		return js.NewTypeError("Session.Commit requires a change callback")
	}
	return s.CommitWith(ctx, func(tx *Transaction) error {
		return change(tx)
	}, nil)
}

// CommitWith runs a commit with access to concrete *Transaction and optional TransactionScope.
func (s *SessionImpl) CommitWith(ctx chord.Context, change func(tx *Transaction) error, scope *TransactionScope) error {
	if err := s.assertUsable(); err != nil {
		return err
	}
	s.acquireLine()
	defer s.releaseLine()
	return s.runCommit(ctx, change, scope)
}

// ReadOnLine runs a read-only job on the mutation line so multi-read derivations observe one committed state.
func (s *SessionImpl) ReadOnLine(job func() error) error {
	if err := s.assertUsable(); err != nil {
		return err
	}
	s.acquireLine()
	defer s.releaseLine()
	if err := s.assertHealthy(); err != nil {
		return err
	}
	return job()
}

// ConversationDocumentOnLine reads a conversation document on the mutation line.
func (s *SessionImpl) ConversationDocumentOnLine(
	ctx chord.Context,
	token ConversationDocToken,
	conversationId ConversationId,
) (*LoadedDocumentView, error) {
	if token == nil {
		return nil, js.NewTypeError("Document token is required")
	}
	def := token.docDefinition()
	resolved, err := ResolveAddress(def, []any{conversationId})
	if err != nil {
		return nil, err
	}
	loaded, err := s.loadDocument(ctx, def, resolved.Id, resolved.Address)
	if err != nil || loaded == nil {
		return nil, err
	}
	if err := CheckRecordScope(def, loaded.record); err != nil {
		return nil, err
	}
	if err := CheckRecordVersion(def, loaded.record, loaded.storedVersion); err != nil {
		return nil, err
	}
	return &LoadedDocumentView{
		Record:  loaded.record,
		Version: loaded.valueVersion,
		Value:   loaded.tracker.Value(),
	}, nil
}

func (s *SessionImpl) Snapshot(ctx chord.Context, token AnyDocToken, args ...any) (JsonObject, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	if token == nil {
		return nil, js.NewTypeError("Document token is required")
	}
	def := token.docDefinition()
	resolved, err := ResolveAddress(def, args)
	if err != nil {
		return nil, err
	}
	if err := docuCheckExactArgs(def, resolved, args, 0, "Session.Snapshot()"); err != nil {
		return nil, err
	}

	s.docsMu.Lock()
	cached := s.documents[resolved.Id]
	s.docsMu.Unlock()

	var loaded *stLoadedDocument
	if cached != nil && cached.valueVersion == def.Version {
		loaded = cached
	} else {
		s.acquireLine()
		err := s.assertHealthy()
		if err == nil {
			loaded, err = s.loadDocument(ctx, def, resolved.Id, resolved.Address)
		}
		s.releaseLine()
		if err != nil {
			return nil, err
		}
	}
	if loaded == nil {
		return nil, nil
	}
	if err := CheckRecordScope(def, loaded.record); err != nil {
		return nil, err
	}
	if err := CheckRecordVersion(def, loaded.record, loaded.storedVersion); err != nil {
		return nil, err
	}
	return loaded.tracker.Value(), nil
}

func (s *SessionImpl) DocumentState(ctx chord.Context, token AnyDocToken, args ...any) (DocumentState, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	if token == nil {
		return nil, js.NewTypeError("Document token is required")
	}
	def := token.docDefinition()
	resolved, err := ResolveAddress(def, args)
	if err != nil {
		return nil, err
	}
	if err := docuCheckExactArgs(def, resolved, args, 0, "Session.DocumentState()"); err != nil {
		return nil, err
	}

	s.acquireLine()
	defer s.releaseLine()
	if err := s.assertHealthy(); err != nil {
		return nil, err
	}
	loaded, err := s.loadDocument(ctx, def, resolved.Id, resolved.Address)
	if err != nil || loaded == nil {
		return nil, err
	}
	if err := CheckRecordScope(def, loaded.record); err != nil {
		return nil, err
	}
	if err := CheckRecordVersion(def, loaded.record, loaded.storedVersion); err != nil {
		return nil, err
	}

	var detach func()
	source := soNewCommittedStateSource(loaded.tracker.Value(), func() {
		if detach != nil {
			detach()
		}
	})
	if err := s.attachDocumentObserver(
		loaded,
		func(d func()) { detach = d },
		func(change DocumentCommitChangeUpdate, observed *int, pubCtx chord.Context) {
			ops := ssObservedOperations(observed, change)
			if len(ops) == 0 {
				return
			}
			source.Advance(change.Value, ops, chord.WithoutAbortSignal(pubCtx))
		},
		func() {
			source.CloseSession()
		},
	); err != nil {
		return nil, err
	}
	state, err := chord.NewReplicatedStateSource[JsonObject](source, nil)
	if err != nil {
		detach()
		return nil, err
	}
	return state, nil
}

func (s *SessionImpl) WatchDoc(ctx chord.Context, token AnyDocToken, args ...any) (DocumentWatch, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	if token == nil {
		return nil, js.NewTypeError("Document token is required")
	}
	def := token.docDefinition()
	resolved, err := ResolveAddress(def, args)
	if err != nil {
		return nil, err
	}
	if err := docuCheckExactArgs(def, resolved, args, 0, "Session.WatchDoc()"); err != nil {
		return nil, err
	}

	var sig context.Context
	if ctx != nil {
		sig = ctx.AbortSignal()
	}
	if sig != nil && sig.Err() != nil {
		return nil, ssCancellationError(sig)
	}

	s.acquireLine()
	if err := s.assertHealthy(); err != nil {
		s.releaseLine()
		return nil, err
	}
	if sig != nil && sig.Err() != nil {
		s.releaseLine()
		return nil, ssCancellationError(sig)
	}
	loaded, err := s.loadDocument(ctx, def, resolved.Id, resolved.Address)
	if err != nil {
		s.releaseLine()
		return nil, err
	}
	if sig != nil && sig.Err() != nil {
		s.releaseLine()
		return nil, ssCancellationError(sig)
	}
	if loaded == nil {
		s.releaseLine()
		return nil, nil
	}
	if err := CheckRecordScope(def, loaded.record); err != nil {
		s.releaseLine()
		return nil, err
	}
	if err := CheckRecordVersion(def, loaded.record, loaded.storedVersion); err != nil {
		s.releaseLine()
		return nil, err
	}

	var detach func()
	watch := soNewCommittedWatch[JsonObject](loaded.tracker.Value(), func() {
		if detach != nil {
			detach()
		}
	}, nil)
	if err := s.attachDocumentObserver(
		loaded,
		func(d func()) { detach = d },
		func(change DocumentCommitChangeUpdate, observed *int, pubCtx chord.Context) {
			ops := ssObservedOperations(observed, change)
			if len(ops) == 0 {
				return
			}
			watch.Advance(change.Value, ops, pubCtx)
		},
		func() {
			watch.CloseSession()
		},
	); err != nil {
		s.releaseLine()
		return nil, err
	}
	s.releaseLine()

	if sig != nil && sig.Err() != nil {
		watch.Cancel()
		return nil, ssCancellationError(sig)
	}
	if sig != nil {
		if err := watch.ObserveCancellation(sig); err != nil {
			watch.Cancel()
			return nil, err
		}
	}
	return watch, nil
}

func (s *SessionImpl) SnapshotAsOf(ctx chord.Context, token AnyDocToken, conversationId ConversationId, args ...any) (JsonObject, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	if token == nil {
		return nil, js.NewTypeError("Document token is required")
	}
	def := token.docDefinition()
	fullArgs := append([]any{conversationId}, args...)
	resolved, err := ResolveAddress(def, fullArgs)
	if err != nil {
		return nil, err
	}
	if resolved.Address.Scope.Kind != DocumentScopeConversation {
		return nil, js.NewTypeError("Session.SnapshotAsOf() requires a conversation document")
	}
	if def.History != DocumentHistoryRewindable {
		return nil, js.NewTypeError("Session.SnapshotAsOf() requires a rewindable conversation document")
	}
	if err := docuCheckExactArgs(def, resolved, fullArgs, 1, "Session.SnapshotAsOf()"); err != nil {
		return nil, err
	}
	if resolved.NextArgument >= len(fullArgs) {
		return nil, js.NewTypeError("Session.SnapshotAsOf() requires an entry ID")
	}
	at, err := docuOwnerId[EntryId](fullArgs[resolved.NextArgument], &AnyDocDefinition{
		Kind:  def.Kind,
		Scope: DocumentScopeTask,
	})
	// Check exact EntryId integer type cleanly.
	switch rawAt := fullArgs[resolved.NextArgument].(type) {
	case EntryId:
		at = rawAt
		err = nil
	case int64:
		at = EntryId(rawAt)
		err = nil
	case int:
		at = EntryId(rawAt)
		err = nil
	default:
		err = js.NewTypeError("Session.SnapshotAsOf() requires an entry ID")
	}
	if err != nil {
		return nil, err
	}

	s.acquireLine()
	defer s.releaseLine()
	if err := s.assertHealthy(); err != nil {
		return nil, err
	}
	storedEntry, err := s.storage.EntryInConversation(ctx, conversationId, at)
	if err != nil {
		return nil, err
	}
	if storedEntry == nil {
		return nil, fmt.Errorf("Entry %d is not visible from conversation %d", at, conversationId)
	}
	address := resolved.Address
	address.Scope = DocumentScope{Kind: DocumentScopeConversation, ConversationId: storedEntry.Entry.ConversationId}
	record, err := s.storage.FindDocument(ctx, address, DocumentPointSeq(storedEntry.CommitSeq))
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, nil
	}
	stored, err := s.storage.Document(ctx, record.Id, DocumentPointSeq(storedEntry.CommitSeq))
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("Historical document %d (%s) cannot be read", record.Id, record.Kind)
	}
	return MaterializeDocument(def, *stored)
}

func (s *SessionImpl) Close(ctx chord.Context) error {
	var startClose bool
	var listeners []*ssCloseSubscription
	var notifyDone chan struct{}
	s.stateMu.Lock()
	if !s.closingStarted {
		s.closingStarted = true
		s.closeDone = make(chan struct{})
		notifyDone = make(chan struct{})
		s.closeNotifyDone = notifyDone
		startClose = true
		listeners = s.closeListeners.Values()
		s.closeListeners.Clear()
	}
	done := s.closeDone
	s.stateMu.Unlock()

	if startClose {
		for _, sub := range listeners {
			sub.fn()
		}
		close(notifyDone)

		cleanup := chord.WithoutAbortSignal(ctx)
		go func() {
			if s.hooks.BeforeClose != nil {
				s.hooks.BeforeClose()
			}
			s.acquireLine()
			s.stateMu.Lock()
			s.commitListeners.Clear()
			s.stateMu.Unlock()

			s.docsMu.Lock()
			s.documents = make(map[string]*stLoadedDocument)
			s.docsMu.Unlock()

			err := s.storage.Close(cleanup)
			s.releaseLine()

			s.stateMu.Lock()
			s.closeErr = err
			s.stateMu.Unlock()
			close(done)
		}()
	}

	var sig context.Context
	if ctx != nil {
		sig = ctx.AbortSignal()
	}
	if sig == nil {
		<-done
		return s.closeErr
	}
	select {
	case <-done:
		return s.closeErr
	case <-sig.Done():
		return ssCancellationError(sig)
	}
}

func (s *SessionImpl) SubscribeCommits(listener func(publication CommitPublication, ctx chord.Context)) func() {
	if listener == nil {
		return func() {}
	}
	s.stateMu.Lock()
	if s.closingStarted || s.poison != nil {
		s.stateMu.Unlock()
		return func() {}
	}
	sub := &ssCommitSubscription{fn: listener}
	s.commitListeners.Add(sub)
	s.stateMu.Unlock()
	return func() {
		s.stateMu.Lock()
		s.commitListeners.Delete(sub)
		s.stateMu.Unlock()
	}
}

func (s *SessionImpl) SubscribeClose(listener func()) func() {
	if listener == nil {
		return func() {}
	}
	s.stateMu.Lock()
	if s.closingStarted || s.poison != nil {
		s.stateMu.Unlock()
		return func() {}
	}
	sub := &ssCloseSubscription{fn: listener}
	s.closeListeners.Add(sub)
	s.stateMu.Unlock()
	return func() {
		s.stateMu.Lock()
		s.closeListeners.Delete(sub)
		s.stateMu.Unlock()
	}
}

// UnloadDocuments drops every loaded tracker on the mutation line; later access cold-loads from Storage.
func (s *SessionImpl) UnloadDocuments() error {
	s.acquireLine()
	defer s.releaseLine()
	s.docsMu.Lock()
	s.documents = make(map[string]*stLoadedDocument)
	s.docsMu.Unlock()
	return nil
}

func (s *SessionImpl) runCommit(ctx chord.Context, change func(tx *Transaction) error, scope *TransactionScope) error {
	if err := s.assertHealthy(); err != nil {
		return err
	}
	if ctx != nil {
		if sig := ctx.AbortSignal(); sig != nil && sig.Err() != nil {
			return ssCancellationError(sig)
		}
	}
	var sc TransactionScope
	if scope != nil {
		sc = *scope
	}
	tx := stNewTransaction(s.host, ctx, sc)
	if err := change(tx); err != nil {
		tx.SettleFailure()
		return err
	}
	writes, err := tx.SettleSuccess()
	if err != nil {
		return err
	}
	if len(writes) == 0 {
		tx.Discard()
		return nil
	}

	seq, err := s.storage.Commit(chord.WithoutAbortSignal(ctx), writes)
	if err != nil {
		tx.Discard()
		var rejected *StorageRejected
		if !errors.As(err, &rejected) {
			s.stateMu.Lock()
			s.poison = err
			s.stateMu.Unlock()
		}
		return err
	}

	documents := tx.Adopt(seq)
	s.publish(seq, writes, documents, ctx)
	return nil
}

func (s *SessionImpl) publish(
	seq Seq,
	writes []StorageWrite,
	documents []DocumentCommitChange,
	ctx chord.Context,
) {
	s.stateMu.Lock()
	notifyDone := s.closeNotifyDone
	s.stateMu.Unlock()
	if notifyDone != nil {
		<-notifyDone
	}

	s.stateMu.Lock()
	if s.commitListeners.Len() == 0 {
		s.stateMu.Unlock()
		return
	}
	listeners := s.commitListeners.Values()
	s.stateMu.Unlock()

	var changes []CommitChange
	for _, write := range writes {
		if cc, ok := write.(CommitChange); ok {
			changes = append(changes, cc)
		}
	}
	for _, doc := range documents {
		changes = append(changes, doc)
	}
	publication := CommitPublication{Seq: seq, Changes: changes}
	for _, sub := range listeners {
		sub.fn(publication, ctx)
	}
}

func (s *SessionImpl) attachDocumentObserver(
	loaded *stLoadedDocument,
	bindDetach func(func()),
	onUpdate func(change DocumentCommitChangeUpdate, observedVersion *int, ctx chord.Context),
	onClose func(),
) error {
	var once sync.Once
	observedVersion := loaded.valueVersion
	targetDocId := loaded.record.Id

	commitSub := &ssCommitSubscription{fn: func(publication CommitPublication, ctx chord.Context) {
		for _, change := range publication.Changes {
			docChange, ok := change.(DocumentCommitChangeUpdate)
			if !ok || docChange.Record.Id != targetDocId {
				continue
			}
			onUpdate(docChange, &observedVersion, ctx)
		}
	}}
	closeSub := &ssCloseSubscription{fn: onClose}
	detach := func() {
		once.Do(func() {
			s.stateMu.Lock()
			s.commitListeners.Delete(commitSub)
			s.closeListeners.Delete(closeSub)
			s.stateMu.Unlock()
		})
	}

	s.stateMu.Lock()
	if s.closingStarted {
		s.stateMu.Unlock()
		return fmt.Errorf("Session is closed")
	}
	if s.poison != nil {
		poison := s.poison
		s.stateMu.Unlock()
		return fmt.Errorf("Session is poisoned by a failed commit after storage admission; reopen it: %w", poison)
	}
	if bindDetach != nil {
		bindDetach(detach)
	}
	s.commitListeners.Add(commitSub)
	s.closeListeners.Add(closeSub)
	s.stateMu.Unlock()
	return nil
}

func (s *SessionImpl) loadDocument(
	ctx chord.Context,
	definition *AnyDocDefinition,
	addressId string,
	address DocumentAddress,
) (*stLoadedDocument, error) {
	s.docsMu.Lock()
	cached := s.documents[addressId]
	s.docsMu.Unlock()

	if cached != nil && cached.valueVersion == definition.Version {
		return cached, nil
	}
	if cached != nil {
		s.docsMu.Lock()
		if s.documents[addressId] == cached {
			delete(s.documents, addressId)
		}
		s.docsMu.Unlock()
	}

	record, err := s.storage.FindDocument(ctx, address, DocumentPointCurrent())
	if err != nil || record == nil {
		return nil, err
	}
	stored, err := s.storage.Document(ctx, record.Id, DocumentPointCurrent())
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("Current document %d (%s) cannot be read", record.Id, record.Kind)
	}
	value, err := MaterializeDocument(definition, *stored)
	if err != nil {
		return nil, err
	}
	loaded := &stLoadedDocument{
		addressId:       addressId,
		record:          stored.Record,
		storedVersion:   stored.Version,
		valueVersion:    definition.Version,
		deltasSinceBase: stored.DeltasSinceBase,
		tracker:         chord.Track(value),
	}
	s.docsMu.Lock()
	s.documents[addressId] = loaded
	s.docsMu.Unlock()
	return loaded, nil
}

func (s *SessionImpl) acquireLine() {
	s.lineMu.Lock()
	if !s.lineBusy {
		s.lineBusy = true
		s.lineMu.Unlock()
		return
	}
	wait := make(chan struct{})
	s.lineQueue = append(s.lineQueue, wait)
	s.lineMu.Unlock()
	<-wait
}

func (s *SessionImpl) releaseLine() {
	s.lineMu.Lock()
	if len(s.lineQueue) == 0 {
		s.lineBusy = false
		s.lineMu.Unlock()
		return
	}
	next := s.lineQueue[0]
	s.lineQueue[0] = nil
	s.lineQueue = s.lineQueue[1:]
	s.lineMu.Unlock()
	close(next)
}

func (s *SessionImpl) assertUsable() error {
	s.stateMu.Lock()
	closing := s.closingStarted
	poison := s.poison
	s.stateMu.Unlock()
	if closing {
		return fmt.Errorf("Session is closed")
	}
	if poison != nil {
		return fmt.Errorf("Session is poisoned by a failed commit after storage admission; reopen it: %w", poison)
	}
	return nil
}

func (s *SessionImpl) assertHealthy() error {
	s.stateMu.Lock()
	poison := s.poison
	s.stateMu.Unlock()
	if poison != nil {
		return fmt.Errorf("Session is poisoned by a failed commit after storage admission; reopen it: %w", poison)
	}
	return nil
}

func ssObservedOperations(observedVersion *int, change DocumentCommitChangeUpdate) []chord.Op {
	if change.Value == nil {
		return soRetirementOperations
	}
	if change.Version != nil && *change.Version == *observedVersion {
		return change.Ops
	}
	if change.Version != nil {
		*observedVersion = *change.Version
	}
	return []chord.Op{chord.OpReplace{Value: change.Value}}
}

func ssCancellationError(sig context.Context) error {
	if sig == nil {
		return nil
	}
	cause := context.Cause(sig)
	if cause == nil {
		return &js.JSError{ErrName: "AbortError", Message: "The operation was aborted"}
	}
	var reasonCarrier interface{ Reason() any }
	if errors.As(cause, &reasonCarrier) {
		return &js.JSError{ErrName: "AbortError", Message: "The operation was aborted"}
	}
	return cause
}
