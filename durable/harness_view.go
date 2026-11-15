// Ported from packages/durable/src/harness/view.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ConversationView is the structural mount of one conversation's active transcript and built-in documents.
type ConversationView struct {
	Conversation ConversationRecord    `json:"conversation"`
	Entries      []EntryRecord         `json:"entries"`
	Docs         map[string]JsonObject `json:"docs"`
}

// hvViewObserver receives each next revision of a conversation mount and Session closure.
type hvViewObserver interface {
	Advance(value ConversationView, ops []chord.Op, ctx chord.Context)
	Publication(before ConversationView, after ConversationView, ops []chord.Op, publication CommitPublication, ctx chord.Context)
	CloseSession()
}

// hvStateSourceViewObserver adapts *soCommittedStateSource[ConversationView] to hvViewObserver.
type hvStateSourceViewObserver struct {
	*soCommittedStateSource[ConversationView]
}

func (o *hvStateSourceViewObserver) Publication(_ ConversationView, _ ConversationView, _ []chord.Op, _ CommitPublication, _ chord.Context) {
}

// hvWatchViewObserver adapts *soCommittedWatch[ConversationView] to hvViewObserver.
type hvWatchViewObserver struct {
	*soCommittedWatch[ConversationView]
}

func (o *hvWatchViewObserver) Publication(_ ConversationView, _ ConversationView, _ []chord.Op, _ CommitPublication, _ chord.Context) {
}

var hvMountedTokens = []*DocToken{AgentDoc, LiveDoc, InboxDoc, UsageDoc}

var hvMountedKinds = func() map[string]struct{} {
	m := make(map[string]struct{}, len(hvMountedTokens))
	for _, token := range hvMountedTokens {
		m[token.docDefinition().Kind] = struct{}{}
	}
	return m
}()

type hvDocIncarnation struct {
	id      DocumentId
	version int
}

type hvMount struct {
	value     ConversationView
	docs      map[string]hvDocIncarnation
	observers map[hvViewObserver]struct{}
}

var (
	hvViewsMu sync.Mutex
	hvViews   = make(map[any]*ConversationViews)
)

// GetConversationViews returns the ConversationViews mount manager attached to harness or session.
func GetConversationViews(harness any) (*ConversationViews, error) {
	if p, ok := harness.(interface{ hvViews() *ConversationViews }); ok {
		if views := p.hvViews(); views != nil {
			return views, nil
		}
	}
	hvViewsMu.Lock()
	views := hvViews[harness]
	hvViewsMu.Unlock()
	if views == nil {
		return nil, fmt.Errorf("Not a Harness")
	}
	return views, nil
}

// ConversationViews manages at most one shared view mount per conversation, built on the Session line
// by its first observer and dropped with its last.
type ConversationViews struct {
	session *SessionImpl
	storage Storage

	mu     sync.Mutex
	mounts map[ConversationId]*hvMount
	closed bool
}

// NewConversationViews constructs a ConversationViews manager subscribed to session's commits and closure.
func NewConversationViews(session *SessionImpl, storage Storage) *ConversationViews {
	v := &ConversationViews{
		session: session,
		storage: storage,
		mounts:  make(map[ConversationId]*hvMount),
	}
	hvViewsMu.Lock()
	hvViews[session] = v
	hvViewsMu.Unlock()

	session.SubscribeCommits(func(publication CommitPublication, ctx chord.Context) {
		v.onPublication(publication, ctx)
	})
	session.SubscribeClose(func() {
		v.onClose()
	})
	return v
}

// State returns a disposable read-only Chord ReplicatedState of conversation id's view.
func (v *ConversationViews) State(id ConversationId, ctx chord.Context) (chord.AttachedReplicatedState[ConversationView], error) {
	obs, detach, err := hvAttach(v, id, func(val ConversationView, release func(), _ Storage) (*hvStateSourceViewObserver, error) {
		return &hvStateSourceViewObserver{soCommittedStateSource: soNewCommittedStateSource(val, release)}, nil
	}, ctx)
	if err != nil {
		return nil, err
	}
	state, err := chord.AttachReplicatedStateSource[ConversationView](obs.soCommittedStateSource, nil)
	if err != nil {
		detach()
		return nil, err
	}
	return state, nil
}

// Watch returns a serialized exact-frame WatchHandle of conversation id's view; cancelling ctx stops it.
func (v *ConversationViews) Watch(id ConversationId, ctx chord.Context) (WatchHandle[ConversationView], error) {
	obs, _, err := hvAttach(v, id, func(val ConversationView, release func(), _ Storage) (*hvWatchViewObserver, error) {
		watch := soNewCommittedWatch(val, release, nil)
		return &hvWatchViewObserver{soCommittedWatch: watch}, nil
	}, ctx)
	if err != nil {
		return nil, err
	}
	sig := hutAbortSignal(ctx)
	if sig != nil && sig.Err() != nil {
		obs.Cancel()
		return nil, hutSignalErr(sig)
	}
	if sig != nil {
		_ = obs.ObserveCancellation(sig)
	}
	return obs.soCommittedWatch, nil
}

// hvAttach registers an observer created from the current revision atomically on the Session line.
func hvAttach[O hvViewObserver](
	v *ConversationViews,
	id ConversationId,
	create func(value ConversationView, release func(), storage Storage) (O, error),
	ctx chord.Context,
) (O, func(), error) {
	var observer O
	var detach func()
	err := v.session.ReadOnLine(func() error {
		v.mu.Lock()
		if v.closed {
			v.mu.Unlock()
			return hutClosedError()
		}
		mount := v.mounts[id]
		v.mu.Unlock()

		if mount == nil {
			built, err := v.buildMount(ctx, id)
			if err != nil {
				return err
			}
			mount = built
		}

		detach = func() {
			v.mu.Lock()
			delete(mount.observers, observer)
			if len(mount.observers) == 0 && v.mounts[id] == mount {
				delete(v.mounts, id)
			}
			v.mu.Unlock()
		}

		created, err := create(mount.value, detach, v.storage)
		if err != nil {
			return err
		}
		observer = created

		v.mu.Lock()
		if v.closed {
			v.mu.Unlock()
			return hutClosedError()
		}
		if err := hutCheckAborted(ctx); err != nil {
			v.mu.Unlock()
			return err
		}
		v.mounts[id] = mount
		mount.observers[observer] = struct{}{}
		v.mu.Unlock()
		return nil
	})
	if err != nil {
		var zero O
		return zero, nil, err
	}
	return observer, detach, nil
}

func (v *ConversationViews) buildMount(ctx chord.Context, id ConversationId) (*hvMount, error) {
	conversation, err := v.storage.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, fmt.Errorf("Conversation %d does not exist", id)
	}
	bounds, err := hxCaptureContextBounds(ctx, v.storage, id)
	if err != nil {
		return nil, err
	}
	entries, err := hxActiveEntries(ctx, v.storage, id, bounds)
	if err != nil {
		return nil, err
	}
	docs := make(map[string]JsonObject)
	incarnations := make(map[string]hvDocIncarnation)
	for _, token := range hvMountedTokens {
		loaded, err := v.session.ConversationDocumentOnLine(ctx, token, id)
		if err != nil {
			return nil, err
		}
		if loaded == nil {
			continue
		}
		kind := token.docDefinition().Kind
		docs[kind] = loaded.Value
		incarnations[kind] = hvDocIncarnation{id: loaded.Record.Id, version: loaded.Version}
	}
	return &hvMount{
		value: ConversationView{
			Conversation: *conversation,
			Entries:      entries,
			Docs:         docs,
		},
		docs:      incarnations,
		observers: make(map[hvViewObserver]struct{}),
	}, nil
}

func (v *ConversationViews) onClose() {
	v.mu.Lock()
	v.closed = true
	var observers []hvViewObserver
	for _, mount := range v.mounts {
		for obs := range mount.observers {
			observers = append(observers, obs)
		}
	}
	clear(v.mounts)
	v.mu.Unlock()

	hvViewsMu.Lock()
	delete(hvViews, v.session)
	hvViewsMu.Unlock()

	for _, obs := range observers {
		obs.CloseSession()
	}
}

func (v *ConversationViews) onPublication(publication CommitPublication, ctx chord.Context) {
	v.mu.Lock()
	type delivery struct {
		before    ConversationView
		after     ConversationView
		ops       []chord.Op
		observers []hvViewObserver
	}
	var deliveries []delivery
	for id, mount := range v.mounts {
		before, after, ops := hvAdvanceMount(id, mount, publication)
		observers := make([]hvViewObserver, 0, len(mount.observers))
		for obs := range mount.observers {
			observers = append(observers, obs)
		}
		deliveries = append(deliveries, delivery{
			before:    before,
			after:     after,
			ops:       ops,
			observers: observers,
		})
	}
	v.mu.Unlock()

	frameCtx := chord.WithoutAbortSignal(ctx)
	for _, d := range deliveries {
		if len(d.ops) > 0 {
			for _, obs := range d.observers {
				obs.Advance(d.after, d.ops, frameCtx)
			}
		}
		for _, obs := range d.observers {
			obs.Publication(d.before, d.after, d.ops, publication, frameCtx)
		}
	}
}

func hvAdvanceMount(id ConversationId, mount *hvMount, publication CommitPublication) (ConversationView, ConversationView, []chord.Op) {
	var docOps []chord.Op
	var entryOps []chord.Op
	entries := mount.value.Entries
	entriesChanged := false

	var nextDocs map[string]JsonObject
	cloneDocs := func() map[string]JsonObject {
		if nextDocs != nil {
			return nextDocs
		}
		nextDocs = make(map[string]JsonObject, len(mount.value.Docs))
		for k, v := range mount.value.Docs {
			nextDocs[k] = v
		}
		return nextDocs
	}

	for _, change := range publication.Changes {
		switch c := change.(type) {
		case StorageWriteEntry:
			if c.Value.ConversationId != id {
				continue
			}
			entry := c.Value
			encodedEntry, err := jsonx.Encode(entry)
			if err != nil {
				encodedEntry = entry
			}
			if entry.Head == nil {
				entryOps = append(entryOps, chord.OpSplice{
					Path:   chord.Path{chord.SegString("entries")},
					Index:  len(entries),
					Remove: 0,
					Items:  []chord.JsonValue{encodedEntry},
				})
				next := make([]EntryRecord, len(entries)+1)
				copy(next, entries)
				next[len(entries)] = entry
				entries = next
				entriesChanged = true
				continue
			}
			target := *entry.Head
			kept := len(entries)
			for i, candidate := range entries {
				if candidate.Head == nil && candidate.Id >= target {
					kept = i
					break
				}
			}
			entryOps = append(entryOps, chord.OpSplice{
				Path:   chord.Path{chord.SegString("entries")},
				Index:  0,
				Remove: kept,
				Items:  []chord.JsonValue{encodedEntry},
			})
			tailLen := len(entries) - kept
			next := make([]EntryRecord, 1+tailLen)
			next[0] = entry
			copy(next[1:], entries[kept:])
			entries = next
			entriesChanged = true

		case DocumentCommitChangeUpdate:
			if c.ConversationId == nil || *c.ConversationId != id {
				continue
			}
			kind := c.Record.Kind
			if _, ok := hvMountedKinds[kind]; !ok || c.Record.Key != nil {
				continue
			}
			prefix := chord.Path{chord.SegString("docs"), chord.SegString(kind)}
			mounted, hasMounted := mount.docs[kind]
			if c.Value == nil {
				if !hasMounted || mounted.id != c.Record.Id {
					continue
				}
				delete(mount.docs, kind)
				delete(cloneDocs(), kind)
				docOps = append(docOps, chord.OpDelete{Path: prefix})
			} else {
				docObj := c.Value
				ver := 0
				if c.Version != nil {
					ver = *c.Version
				}
				if hasMounted && mounted.id == c.Record.Id && mounted.version == ver {
					for _, op := range c.Ops {
						docOps = append(docOps, hvPrefixedOp(op, prefix))
					}
				} else {
					mount.docs[kind] = hvDocIncarnation{id: c.Record.Id, version: ver}
					docOps = append(docOps, chord.OpSet{Path: prefix, Value: docObj})
				}
				cloneDocs()[kind] = docObj
			}
		}
	}

	before := mount.value
	ops := make([]chord.Op, 0, len(docOps)+len(entryOps))
	ops = append(ops, docOps...)
	ops = append(ops, entryOps...)

	if len(ops) > 0 {
		after := before
		if nextDocs != nil {
			after.Docs = nextDocs
		}
		if entriesChanged {
			after.Entries = entries
		}
		mount.value = after
	}
	return before, mount.value, ops
}

func hvPrefixedOp(op chord.Op, prefix chord.NonEmptyPath) chord.Op {
	at := func(path chord.Path) chord.NonEmptyPath {
		joined := make(chord.Path, 0, len(prefix)+len(path))
		joined = append(joined, prefix...)
		joined = append(joined, path...)
		return joined
	}
	switch x := op.(type) {
	case chord.OpReplace:
		return chord.OpSet{Path: append(chord.Path(nil), prefix...), Value: x.Value}
	case chord.OpSplice:
		return chord.OpSplice{Path: at(x.Path), Index: x.Index, Remove: x.Remove, Items: x.Items}
	case chord.OpMove:
		return chord.OpMove{Path: at(x.Path), Permutation: x.Permutation}
	case chord.OpSet:
		return chord.OpSet{Path: at(x.Path), Value: x.Value}
	case chord.OpDelete:
		return chord.OpDelete{Path: at(x.Path)}
	case chord.OpAppend:
		return chord.OpAppend{Path: at(x.Path), Text: x.Text}
	case chord.OpTruncate:
		return chord.OpTruncate{Path: at(x.Path), Count: x.Count}
	default:
		return op
	}
}
