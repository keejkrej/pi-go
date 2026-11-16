// Ported from packages/durable/test/session-documents.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsdcInitialLive(count *atomic.Int32) durable.JsonObject {
	if count != nil {
		count.Add(1)
	}
	return jsonx.ObjectOf(
		"items", []any{},
		"nested", jsonx.ObjectOf("count", float64(0)),
		"other", jsonx.ObjectOf("label", "x"),
	)
}

func tsdcLiveDoc(count *atomic.Int32) *durable.DocToken {
	tok, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "test.live",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest,
		Fork:    durable.DocumentForkInitial,
		Initial: func() durable.JsonObject { return tsdcInitialLive(count) },
	})
	if err != nil {
		panic(err)
	}
	return tok
}

var (
	tsdcRewindableLiveDoc = func() *durable.DocToken {
		tok, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.live",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return tsdcInitialLive(nil) },
		})
		if err != nil {
			panic(err)
		}
		return tok
	}()

	tsdcLiveDocV2 = func() *durable.DocToken {
		tok, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.live",
			Version: 2,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkInitial,
			Initial: func() durable.JsonObject { return tsdcInitialLive(nil) },
		})
		if err != nil {
			panic(err)
		}
		return tok
	}()

	tsdcCounterDoc = func() *durable.DocToken {
		tok, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.counter",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
		})
		if err != nil {
			panic(err)
		}
		return tok
	}()
)

func tsdcSetupLive(t *testing.T, liveDoc *durable.DocToken) (*testkit.TestSession, durable.ConversationId) {
	t.Helper()
	ts := testkit.OpenTestSession()
	convId, err := testkit.CreateConversation(ts.Session)
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	if err := ts.Session.Commit(testkit.TestContext, func(tx durable.Tx) error {
		live, err := tx.Doc(liveDoc, convId)
		if err != nil {
			return err
		}
		live.Value.Set("items", []any{"a", "b"})
		return nil
	}); err != nil {
		t.Fatalf("setupLive commit failed: %v", err)
	}
	return ts, convId
}

func tsdcStrSlice(t *testing.T, obj durable.JsonObject, key string) []any {
	t.Helper()
	raw, ok := obj.Get(key)
	if !ok {
		t.Fatalf("missing key %q in %v", key, obj)
	}
	slice, ok := raw.([]any)
	if !ok {
		t.Fatalf("key %q = %T, want []any", key, raw)
	}
	return slice
}

func tsdcObjField(t *testing.T, obj durable.JsonObject, key string) *jsonx.Object {
	t.Helper()
	raw, ok := obj.Get(key)
	if !ok {
		t.Fatalf("missing key %q in %v", key, obj)
	}
	child, ok := raw.(*jsonx.Object)
	if !ok {
		t.Fatalf("key %q = %T, want *jsonx.Object", key, raw)
	}
	return child
}

func TestSessionDocumentTransactions(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("creates an initial base on first access and adopts it after Storage success", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, err := testkit.CreateConversation(ts.Session)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			live.Value.Set("message", "hello")
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		commits := ts.Storage.Commits()
		writes := commits[len(commits)-1]
		if len(writes) != 1 {
			t.Fatalf("expected 1 write, got %d (%#v)", len(writes), writes)
		}
		cw, ok := writes[0].(durable.StorageWriteDocumentCreate)
		if !ok {
			t.Fatalf("expected StorageWriteDocumentCreate, got %T", writes[0])
		}
		if cw.Record.Kind != "test.live" ||
			cw.Record.Scope != (durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: convId}) ||
			cw.Record.History != durable.DocumentHistoryLatest ||
			cw.Record.Fork != durable.DocumentForkInitial ||
			cw.Content.Kind != durable.DocumentContentBase ||
			cw.Content.Version != 1 {
			t.Fatalf("unexpected create write: %#v", cw)
		}

		snapshot, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil || snapshot == nil {
			t.Fatalf("Snapshot failed: %v %v", snapshot, err)
		}
		wantSnap := jsonx.ObjectOf(
			"items", []any{},
			"nested", jsonx.ObjectOf("count", float64(0)),
			"other", jsonx.ObjectOf("label", "x"),
			"message", "hello",
		)
		if !reflect.DeepEqual(snapshot, wantSnap) {
			t.Fatalf("snapshot = %v, want %v", snapshot, wantSnap)
		}

		testkit.Flush()
		pubs := ts.Publications()
		pub := pubs[len(pubs)-1]
		changes := testkit.DocumentChanges(pub)
		if len(changes) != 1 {
			t.Fatalf("expected 1 change, got %d", len(changes))
		}
		published := changes[0]
		if published.Record.CreatedAt != pub.Seq {
			t.Fatalf("createdAt = %d, want %d", published.Record.CreatedAt, pub.Seq)
		}
		if published.Value != snapshot {
			t.Fatalf("expected published.Value pointer == snapshot pointer")
		}
		if published.ConversationId == nil || *published.ConversationId != convId {
			t.Fatalf("published.ConversationId = %v, want %d", published.ConversationId, convId)
		}

		admittedAll := ts.Storage.AdmittedCommits()
		admitted := admittedAll[len(admittedAll)-1]
		var admittedCreate durable.StorageWriteDocumentCreate
		for _, w := range admitted {
			if c, ok := w.(durable.StorageWriteDocumentCreate); ok {
				admittedCreate = c
				break
			}
		}
		if admittedCreate.Content.Value != published.Value {
			t.Fatalf("expected admittedCreate.Content.Value pointer == published.Value pointer")
		}
	})

	t.Run("never creates on snapshot and returns nil when absent", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		memberDoc, err := durable.DefineDocFamily(durable.DocFamilyDefinition{
			Kind:    "test.member.absent",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func(seed durable.JsonValue) durable.JsonObject {
				return jsonx.ObjectOf("seed", seed, "hits", float64(0))
			},
		})
		if err != nil {
			t.Fatalf("DefineDocFamily failed: %v", err)
		}
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, err := testkit.CreateConversation(ts.Session)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}
		before := len(ts.Storage.Commits())
		if v, err := ts.Session.Snapshot(ctx, liveDoc, convId); err != nil || v != nil {
			t.Fatalf("expected nil snapshot, got (%v, %v)", v, err)
		}
		if v, err := ts.Session.Snapshot(ctx, tsdcCounterDoc); err != nil || v != nil {
			t.Fatalf("expected nil counter snapshot, got (%v, %v)", v, err)
		}
		if v, err := ts.Session.Snapshot(ctx, memberDoc, "k"); err != nil || v != nil {
			t.Fatalf("expected nil member snapshot, got (%v, %v)", v, err)
		}
		if len(ts.Storage.Commits()) != before {
			t.Fatalf("commits grew from %d to %d", before, len(ts.Storage.Commits()))
		}
		if ts.Storage.MintCount() != 1 {
			t.Fatalf("mintCount = %d, want 1", ts.Storage.MintCount())
		}
	})

	t.Run("returns shared immutable snapshots and keeps prior revisions stable", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		first, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil || first == nil {
			t.Fatalf("Snapshot failed: %v", err)
		}
		firstAgain, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil || firstAgain != first {
			t.Fatalf("expected cached pointer identity")
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			tsdcObjField(t, live.Value, "nested").Set("count", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		second, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil || second == nil {
			t.Fatalf("Snapshot second failed: %v", err)
		}
		if second == first {
			t.Fatalf("expected new root pointer after commit")
		}
		firstCount, _ := tsdcObjField(t, first, "nested").Get("count")
		secondCount, _ := tsdcObjField(t, second, "nested").Get("count")
		if firstCount != float64(0) || secondCount != float64(1) {
			t.Fatalf("counts = (%v, %v), want (0, 1)", firstCount, secondCount)
		}
		// Unchanged *jsonx.Object branches preserve pointer sharing.
		if tsdcObjField(t, second, "other") != tsdcObjField(t, first, "other") {
			t.Fatalf("expected unchanged branch pointer sharing for other")
		}
		if !reflect.DeepEqual(tsdcStrSlice(t, second, "items"), tsdcStrSlice(t, first, "items")) {
			t.Fatalf("expected unchanged items slice content")
		}
	})

	t.Run("adopts by pointer swap and shares operation payloads with the published revision", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			live.Value.Set("added", jsonx.ObjectOf("label", "y"))
			items := slices.Clone(tsdcStrSlice(t, live.Value, "items"))
			live.Value.Set("items", append(items, "c"))
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		testkit.Flush()
		snapshot, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil {
			t.Fatalf("Snapshot failed: %v", err)
		}
		pubs := ts.Publications()
		published := testkit.DocumentChanges(pubs[len(pubs)-1])[0]
		if published.Value != snapshot {
			t.Fatalf("expected published.Value == snapshot")
		}

		admittedAll := ts.Storage.AdmittedCommits()
		admittedWrites := admittedAll[len(admittedAll)-1]
		var admittedChange durable.StorageWriteDocumentChange
		found := false
		for _, w := range admittedWrites {
			if c, ok := w.(durable.StorageWriteDocumentChange); ok {
				admittedChange = c
				found = true
			}
		}
		if !found || admittedChange.Content.Kind != durable.DocumentContentDelta {
			t.Fatalf("expected admitted delta, got %#v", admittedWrites)
		}
		if len(published.Ops) == 0 || len(admittedChange.Content.Ops) != len(published.Ops) ||
			&published.Ops[0] != &admittedChange.Content.Ops[0] {
			t.Fatalf("expected shared ops slice backing array between publication and admitted delta")
		}

		var setOp *chord.OpSet
		for _, op := range published.Ops {
			if s, ok := op.(chord.OpSet); ok {
				cp := s
				setOp = &cp
				break
			}
		}
		if setOp == nil || len(setOp.Path) != 1 || setOp.Path[0] != chord.SegString("added") {
			t.Fatalf("expected OpSet at ['added'], got %#v", published.Ops)
		}
		if setVal, ok := setOp.Value.(*jsonx.Object); !ok || setVal != tsdcObjField(t, snapshot, "added") {
			t.Fatalf("expected OpSet value pointer == snapshot.added pointer")
		}
	})

	t.Run("deduplicates shared placed values across fields", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		value := jsonx.ObjectOf("label", "shared")
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			live.Value.Set("other", value)
			live.Value.Set("copy", value)
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		snapshot, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil {
			t.Fatalf("Snapshot failed: %v", err)
		}
		otherObj := tsdcObjField(t, snapshot, "other")
		copyObj := tsdcObjField(t, snapshot, "copy")
		if l, _ := otherObj.Get("label"); l != "shared" {
			t.Fatalf("other.label = %v, want shared", l)
		}
		if l, _ := copyObj.Get("label"); l != "shared" {
			t.Fatalf("copy.label = %v, want shared", l)
		}
		if copyObj == otherObj {
			t.Fatalf("expected distinct *jsonx.Object pointers for duplicate placement across fields")
		}
	})

	t.Run("suppresses writes and publications for empty batches", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		testkit.Flush()
		commits := len(ts.Storage.Commits())
		published := len(ts.Publications())

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			tsdcObjField(t, live.Value, "nested").Set("count", float64(0))
			items := slices.Clone(tsdcStrSlice(t, live.Value, "items"))
			items = append(items, "z")
			items = items[:len(items)-1]
			live.Value.Set("items", items)
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		testkit.Flush()
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("expected no storage commit for empty batch")
		}
		if len(ts.Publications()) != published {
			t.Fatalf("expected no publication for empty batch")
		}
	})

	t.Run("emits minimal delta ops on structural edits and preserves prior revision", func(t *testing.T) {
		// Note: Go chord.Draft uses value diffing (DiffRevisions) rather than live JS Proxy operation logging,
		// so equal before/after values are suppressed as empty batches while structural array edits emit splice ops.
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		before, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil {
			t.Fatalf("Snapshot failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			live.Value.Set("items", []any{"b", "c"})
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		testkit.Flush()
		commits := ts.Storage.Commits()
		last := commits[len(commits)-1]
		cw, ok := last[0].(durable.StorageWriteDocumentChange)
		if !ok || cw.Content.Kind != durable.DocumentContentDelta {
			t.Fatalf("expected document delta write, got %#v", last)
		}
		after, err := ts.Session.Snapshot(ctx, liveDoc, convId)
		if err != nil {
			t.Fatalf("Snapshot after failed: %v", err)
		}
		if !reflect.DeepEqual(tsdcStrSlice(t, after, "items"), []any{"b", "c"}) {
			t.Fatalf("items = %v, want [b c]", tsdcStrSlice(t, after, "items"))
		}
		if after == before {
			t.Fatalf("expected distinct root pointer after non-empty batch")
		}
		pubs := ts.Publications()
		if len(testkit.DocumentChanges(pubs[len(pubs)-1])[0].Ops) == 0 {
			t.Fatalf("expected non-empty published ops")
		}
	})

	t.Run("aborts every change when the callback fails", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		before, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		commits := len(ts.Storage.Commits())
		boom := errors.New("callback failed")
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			counter, err := tx.Doc(tsdcCounterDoc)
			if err != nil {
				return err
			}
			live.Value.Set("message", "lost")
			counter.Value.Set("count", float64(5))
			return boom
		}); !errors.Is(err, boom) {
			t.Fatalf("expected boom error, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits changed after rollback")
		}
		if got, _ := ts.Session.Snapshot(ctx, liveDoc, convId); got != before {
			t.Fatalf("expected prior live snapshot preserved")
		}
		if got, _ := ts.Session.Snapshot(ctx, tsdcCounterDoc); got != nil {
			t.Fatalf("expected counter doc to remain absent, got %v", got)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			live.Value.Set("message", "kept")
			return nil
		}); err != nil {
			t.Fatalf("subsequent Commit failed: %v", err)
		}
		after, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		if msg, _ := after.Get("message"); msg != "kept" {
			t.Fatalf("message = %v, want kept", msg)
		}
	})

	t.Run("memoizes duplicate acquisition within a commit and initializes once", func(t *testing.T) {
		var initCount atomic.Int32
		liveDoc := tsdcLiveDoc(&initCount)
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, err := testkit.CreateConversation(ts.Session)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}
		beforeInit := initCount.Load()
		beforeMints := ts.Storage.MintCount()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			first, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			second, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			if first.Value != second.Value {
				t.Fatalf("expected duplicate tx.Doc to return identical draft")
			}
			third, err := tx.Doc(liveDoc, convId)
			if err != nil || third.Value != first.Value {
				t.Fatalf("expected third tx.Doc to match first (%v)", err)
			}
			first.Value.Set("message", "once")
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		if initCount.Load() != beforeInit+1 {
			t.Fatalf("initCount = %d, want %d", initCount.Load(), beforeInit+1)
		}
		if ts.Storage.MintCount() != beforeMints+1 {
			t.Fatalf("mintCount = %d, want %d", ts.Storage.MintCount(), beforeMints+1)
		}
		last := ts.Storage.Commits()[len(ts.Storage.Commits())-1]
		creates := 0
		for _, w := range last {
			if _, ok := w.(durable.StorageWriteDocumentCreate); ok {
				creates++
			}
		}
		if creates != 1 {
			t.Fatalf("expected 1 document.create write, got %d", creates)
		}
	})

	t.Run("uses the first family seed and ignores seeds for existing members", func(t *testing.T) {
		var mu sync.Mutex
		var seeds []string
		memberDoc, err := durable.DefineDocFamily(durable.DocFamilyDefinition{
			Kind:    "test.member.seed",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func(seed durable.JsonValue) durable.JsonObject {
				mu.Lock()
				seeds = append(seeds, seed.(string))
				mu.Unlock()
				return jsonx.ObjectOf("seed", seed, "hits", float64(0))
			},
		})
		if err != nil {
			t.Fatalf("DefineDocFamily failed: %v", err)
		}
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			first, err := tx.Doc(memberDoc, "k", "first")
			if err != nil {
				return err
			}
			second, err := tx.Doc(memberDoc, "k", "second")
			if err != nil {
				return err
			}
			if first.Value != second.Value {
				t.Fatalf("expected duplicate member draft pointer equality")
			}
			hits, _ := first.Value.Get("hits")
			first.Value.Set("hits", hits.(float64)+1)
			return nil
		}); err != nil {
			t.Fatalf("Commit 1 failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			kDraft, err := tx.Doc(memberDoc, "k", "third")
			if err != nil {
				return err
			}
			kHits, _ := kDraft.Value.Get("hits")
			kDraft.Value.Set("hits", kHits.(float64)+1)

			oDraft, err := tx.Doc(memberDoc, "other", "fourth")
			if err != nil {
				return err
			}
			oHits, _ := oDraft.Value.Get("hits")
			oDraft.Value.Set("hits", oHits.(float64)+1)
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}

		if !reflect.DeepEqual(seeds, []string{"first", "fourth"}) {
			t.Fatalf("seeds = %v, want [first fourth]", seeds)
		}
		if snap, _ := ts.Session.Snapshot(ctx, memberDoc, "k"); !reflect.DeepEqual(snap, jsonx.ObjectOf("seed", "first", "hits", float64(2))) {
			t.Fatalf("member k = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, memberDoc, "other"); !reflect.DeepEqual(snap, jsonx.ObjectOf("seed", "fourth", "hits", float64(1))) {
			t.Fatalf("member other = %v", snap)
		}
	})

	t.Run("rejects Tx use after the callback settles", func(t *testing.T) {
		// Note: Go Transaction.Doc is synchronous, so JS unawaited-promise cases do not arise;
		// we still verify that any escaped Tx handle rejects post-settlement use.
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		var captured *durable.Transaction
		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			captured = tx
			return nil
		}, nil); err != nil {
			t.Fatalf("CommitWith failed: %v", err)
		}
		if _, err := captured.Doc(liveDoc, convId); err == nil || !strings.Contains(err.Error(), "Transaction has settled") {
			t.Fatalf("expected settled error from Doc, got %v", err)
		}
		if _, err := captured.Conversation(convId); err == nil || !strings.Contains(err.Error(), "Transaction has settled") {
			t.Fatalf("expected settled error from Conversation, got %v", err)
		}
		if err := captured.SetTask(durable.TaskRecord{}); err == nil || !strings.Contains(err.Error(), "Transaction has settled") {
			t.Fatalf("expected settled error from SetTask, got %v", err)
		}
	})

	t.Run("rejects tokens whose semantics or version disagree with the stored incarnation", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		if _, err := ts.Session.Snapshot(ctx, tsdcRewindableLiveDoc, convId); err == nil || !strings.Contains(err.Error(), "does not match the supplied definition semantics") {
			t.Fatalf("expected scope/semantics error on Snapshot, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(tsdcRewindableLiveDoc, convId)
			return err
		}); err == nil || !strings.Contains(err.Error(), "does not match the supplied definition semantics") {
			t.Fatalf("expected scope/semantics error on Doc, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(tsdcLiveDocV2, convId)
			return err
		}); err == nil || !strings.Contains(err.Error(), "requires migration from version 1") {
			t.Fatalf("expected version migration error, got %v", err)
		}
	})

	t.Run("rejects non-JSON initializer values and draft placements before Storage admission", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, err := testkit.CreateConversation(ts.Session)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}

		badInitDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.nonfinite",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject {
				return jsonx.ObjectOf("at", math.NaN())
			},
		})
		if err != nil {
			t.Fatalf("DefineDoc failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(badInitDoc)
			return err
		}); err == nil || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("expected strict JSON error on initial, got %v", err)
		}

		commits := len(ts.Storage.Commits())
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			live.Value.Set("items", []any{[]byte{1, 2, 3}})
			return nil
		}); err == nil || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("expected strict JSON error on placement, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("expected no storage commit for invalid draft placement")
		}
		if snap, _ := ts.Session.Snapshot(ctx, liveDoc, convId); snap != nil {
			t.Fatalf("expected absent liveDoc after failed creation commit, got %v", snap)
		}
	})

	t.Run("rolls back prepared documents when batch assembly fails", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.Doc(tsdcCounterDoc)
			if err != nil {
				return err
			}
			c.Value.Set("count", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		live, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		counter, _ := ts.Session.Snapshot(ctx, tsdcCounterDoc)
		commits := len(ts.Storage.Commits())

		err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			l, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			l.Value.Set("message", "lost")
			c, err := tx.Doc(tsdcCounterDoc)
			if err != nil {
				return err
			}
			c.Value.Set("count", float64(2))
			// Replacing a missing task fails during assembly, after every change was prepared.
			return tx.SetTask(durable.TaskRecord{
				Id:             durable.IdFromNumber[durable.TaskId](999),
				ConversationId: convId,
				Kind:           "missing",
				Version:        1,
				Input:          nil,
				State: durable.TaskState{
					Status:     durable.TaskStatePending,
					Checkpoint: jsonx.ObjectOf("phase", "start"),
				},
			})
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "Task 999 does not exist") {
			t.Fatalf("expected Task 999 error, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits grew on assembly failure")
		}
		if got, _ := ts.Session.Snapshot(ctx, liveDoc, convId); got != live {
			t.Fatalf("live snapshot pointer changed after assembly failure")
		}
		if got, _ := ts.Session.Snapshot(ctx, tsdcCounterDoc); got != counter {
			t.Fatalf("counter snapshot pointer changed after assembly failure")
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			l, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			l.Value.Set("message", "next")
			c, err := tx.Doc(tsdcCounterDoc)
			if err != nil {
				return err
			}
			c.Value.Set("count", float64(3))
			return nil
		}); err != nil {
			t.Fatalf("recovery Commit failed: %v", err)
		}
		gotCounter, _ := ts.Session.Snapshot(ctx, tsdcCounterDoc)
		if cVal, _ := gotCounter.Get("count"); cVal != float64(3) {
			t.Fatalf("count = %v, want 3", cVal)
		}
	})

	t.Run("poisons the Session after an uncertain Storage failure and publishes nothing", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		testkit.Flush()
		before, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		published := len(ts.Publications())
		diskErr := errors.New("disk vanished")
		ts.Storage.FailNextCommit(diskErr)

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			l, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			l.Value.Set("message", "uncertain")
			return nil
		}); !errors.Is(err, diskErr) {
			t.Fatalf("expected diskErr, got %v", err)
		}
		testkit.Flush()
		if len(ts.Publications()) != published {
			t.Fatalf("expected no publication on failed storage commit")
		}
		if _, ok := before.Get("message"); ok {
			t.Fatalf("expected before snapshot untouched")
		}
		if _, err := ts.Session.Snapshot(ctx, liveDoc, convId); err == nil || !strings.Contains(err.Error(), "poisoned") {
			t.Fatalf("expected poisoned error from Snapshot, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(_ durable.Tx) error { return nil }); err == nil || !strings.Contains(err.Error(), "poisoned") {
			t.Fatalf("expected poisoned error from Commit, got %v", err)
		}
	})

	t.Run("keeps the previous revision unchanged through Storage settlement", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		before, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		copyBefore, err := chord.CopyJson(before, nil)
		if err != nil {
			t.Fatalf("CopyJson failed: %v", err)
		}
		gate := ts.Storage.HoldCommits()
		commitErrCh := make(chan error, 1)
		go func() {
			commitErrCh <- ts.Session.Commit(ctx, func(tx durable.Tx) error {
				live, err := tx.Doc(liveDoc, convId)
				if err != nil {
					return err
				}
				items := slices.Clone(tsdcStrSlice(t, live.Value, "items"))
				live.Value.Set("items", append(items, "c"))
				tsdcObjField(t, live.Value, "nested").Set("count", float64(9))
				return nil
			})
		}()
		<-gate.Entered
		if got, _ := ts.Session.Snapshot(ctx, liveDoc, convId); got != before {
			t.Fatalf("expected Snapshot to return unchanged before pointer while commit held")
		}
		if !reflect.DeepEqual(before, copyBefore) {
			t.Fatalf("before mutated during held commit: %v != %v", before, copyBefore)
		}
		gate.Release()
		if err := <-commitErrCh; err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		after, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		if !reflect.DeepEqual(tsdcStrSlice(t, after, "items"), []any{"a", "b", "c"}) {
			t.Fatalf("items = %v, want [a b c]", after)
		}
		if !reflect.DeepEqual(before, copyBefore) {
			t.Fatalf("before mutated after commit")
		}
	})

	t.Run("retires documents and creates a new incarnation at the same address", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		testkit.Flush()
		pubs := ts.Publications()
		oldId := testkit.DocumentChanges(pubs[len(pubs)-1])[0].Record.Id

		var oldDraft, replacement durable.JsonObject
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			oldDraft = live.Value
			live.Value.Set("message", "final")
			if err := tx.RetireDoc(liveDoc, convId); err != nil {
				return err
			}
			rep, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			replacement = rep.Value
			if replacement == oldDraft {
				t.Fatalf("expected fresh replacement draft distinct from retired draft")
			}
			rep.Value.Set("message", "new")
			return nil
		}); err != nil {
			t.Fatalf("retire+recreate commit failed: %v", err)
		}

		commits := ts.Storage.Commits()
		writes := commits[len(commits)-1]
		if len(writes) != 3 {
			t.Fatalf("expected 3 writes (change, retire, create), got %d (%#v)", len(writes), writes)
		}
		if cw, ok := writes[0].(durable.StorageWriteDocumentChange); !ok || cw.Id != oldId {
			t.Fatalf("writes[0] = %#v, want document.change for %d", writes[0], oldId)
		}
		if rw, ok := writes[1].(durable.StorageWriteDocumentRetire); !ok || rw.Id != oldId {
			t.Fatalf("writes[1] = %#v, want document.retire for %d", writes[1], oldId)
		}
		if cw, ok := writes[2].(durable.StorageWriteDocumentCreate); !ok || cw.Record.Kind != "test.live" {
			t.Fatalf("writes[2] = %#v, want document.create", writes[2])
		}

		testkit.Flush()
		pubs = ts.Publications()
		pub := pubs[len(pubs)-1]
		changes := testkit.DocumentChanges(pub)
		if len(changes) != 2 {
			t.Fatalf("expected 2 changes, got %d", len(changes))
		}
		retired, created := changes[0], changes[1]
		if retired.Record.Id != oldId || retired.Value != nil || len(retired.Ops) != 0 ||
			retired.Record.RetiredAt == nil || *retired.Record.RetiredAt != pub.Seq {
			t.Fatalf("unexpected retired change: %#v", retired)
		}
		if created.Record.Id == oldId || created.Record.CreatedAt != pub.Seq || created.Value == nil {
			t.Fatalf("unexpected created change: %#v", created)
		}
		if msg, _ := created.Value.Get("message"); msg != "new" {
			t.Fatalf("created message = %v, want new", msg)
		}
		if snap, _ := ts.Session.Snapshot(ctx, liveDoc, convId); snap != created.Value {
			t.Fatalf("expected snapshot to match created value pointer")
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			return tx.RetireDoc(liveDoc, convId)
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, liveDoc, convId); snap != nil {
			t.Fatalf("expected nil snapshot after retire, got %v", snap)
		}
		_ = ts.Session.UnloadDocuments()
		if snap, _ := ts.Session.Snapshot(ctx, liveDoc, convId); snap != nil {
			t.Fatalf("expected nil snapshot after unload, got %v", snap)
		}

		beforeNoOp := len(ts.Storage.Commits())
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			return tx.RetireDoc(liveDoc, convId)
		}); err != nil {
			t.Fatalf("absent retire failed: %v", err)
		}
		if len(ts.Storage.Commits()) != beforeNoOp {
			t.Fatalf("retiring an absent address should be a no-op")
		}
	})

	t.Run("retires without acquisition and recreates both existing and absent addresses", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		memberDoc, err := durable.DefineDocFamily(durable.DocFamilyDefinition{
			Kind:    "test.member.recreate",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func(seed durable.JsonValue) durable.JsonObject {
				return jsonx.ObjectOf("seed", seed, "hits", float64(0))
			},
		})
		if err != nil {
			t.Fatalf("DefineDocFamily failed: %v", err)
		}
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		testkit.Flush()
		pubs := ts.Publications()
		oldId := testkit.DocumentChanges(pubs[len(pubs)-1])[0].Record.Id
		_ = ts.Session.UnloadDocuments()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if err := tx.RetireDoc(liveDoc, convId); err != nil {
				return err
			}
			rep, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			rep.Value.Set("message", "replacement")
			return nil
		}); err != nil {
			t.Fatalf("Commit 1 failed: %v", err)
		}
		commits := ts.Storage.Commits()
		existingWrites := commits[len(commits)-1]
		if len(existingWrites) != 2 {
			t.Fatalf("expected 2 writes, got %d (%#v)", len(existingWrites), existingWrites)
		}
		hasRetire, hasCreate := false, false
		for _, w := range existingWrites {
			if r, ok := w.(durable.StorageWriteDocumentRetire); ok && r.Id == oldId {
				hasRetire = true
			}
			if c, ok := w.(durable.StorageWriteDocumentCreate); ok && c.Record.Kind == "test.live" {
				hasCreate = true
			}
		}
		if !hasRetire || !hasCreate {
			t.Fatalf("expected retire(%d) and create, got %#v", oldId, existingWrites)
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if err := tx.RetireDoc(memberDoc, "absent"); err != nil {
				return err
			}
			rep, err := tx.Doc(memberDoc, "absent", "seed")
			if err != nil {
				return err
			}
			rep.Value.Set("hits", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}
		commits = ts.Storage.Commits()
		absentWrites := commits[len(commits)-1]
		retireCount, createCount := 0, 0
		for _, w := range absentWrites {
			switch w.(type) {
			case durable.StorageWriteDocumentRetire:
				retireCount++
			case durable.StorageWriteDocumentCreate:
				createCount++
			}
		}
		if retireCount != 0 || createCount != 1 {
			t.Fatalf("expected 0 retires and 1 create for absent address, got %d/%d", retireCount, createCount)
		}
		if snap, _ := ts.Session.Snapshot(ctx, memberDoc, "absent"); !reflect.DeepEqual(snap, jsonx.ObjectOf("seed", "seed", "hits", float64(1))) {
			t.Fatalf("absent member = %v", snap)
		}
	})

	t.Run("retires an acquired document and recreates in the following commit", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		testkit.Flush()
		pubs := ts.Publications()
		oldId := testkit.DocumentChanges(pubs[len(pubs)-1])[0].Record.Id

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			acquired, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			acquired.Value.Set("message", "final")
			return tx.RetireDoc(liveDoc, convId)
		}); err != nil {
			t.Fatalf("Commit 1 failed: %v", err)
		}
		commits := ts.Storage.Commits()
		firstWrites := commits[len(commits)-1]
		if len(firstWrites) != 2 {
			t.Fatalf("expected 2 writes, got %d", len(firstWrites))
		}
		if cw, ok := firstWrites[0].(durable.StorageWriteDocumentChange); !ok || cw.Id != oldId || cw.Content.Kind != durable.DocumentContentDelta {
			t.Fatalf("expected delta write for oldId=%d, got %#v", oldId, firstWrites[0])
		}
		if rw, ok := firstWrites[1].(durable.StorageWriteDocumentRetire); !ok || rw.Id != oldId {
			t.Fatalf("expected retire write for oldId=%d, got %#v", oldId, firstWrites[1])
		}
		if snap, _ := ts.Session.Snapshot(ctx, liveDoc, convId); snap != nil {
			t.Fatalf("expected nil snapshot after retire")
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			d.Value.Set("message", "second")
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}
		testkit.Flush()
		pubs = ts.Publications()
		secondId := testkit.DocumentChanges(pubs[len(pubs)-1])[0].Record.Id

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			oldDraft, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			if err := tx.RetireDoc(liveDoc, convId); err != nil {
				return err
			}
			fresh, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			if fresh.Value == oldDraft.Value {
				t.Fatalf("expected fresh incarnation distinct from oldDraft")
			}
			fresh.Value.Set("message", "third")
			return nil
		}); err != nil {
			t.Fatalf("Commit 3 failed: %v", err)
		}
		commits = ts.Storage.Commits()
		writes := commits[len(commits)-1]
		if len(writes) != 2 {
			t.Fatalf("expected 2 writes (retire + create), got %d (%#v)", len(writes), writes)
		}
		if rw, ok := writes[0].(durable.StorageWriteDocumentRetire); !ok || rw.Id != secondId {
			t.Fatalf("expected retire for secondId=%d, got %#v", secondId, writes[0])
		}
		if cw, ok := writes[1].(durable.StorageWriteDocumentCreate); !ok || cw.Record.Kind != "test.live" {
			t.Fatalf("expected create for test.live, got %#v", writes[1])
		}
		after, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		if msg, _ := after.Get("message"); msg != "third" {
			t.Fatalf("message = %v, want third", msg)
		}
	})

	t.Run("reloads an unloaded document from Storage", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		loaded, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		_ = ts.Session.UnloadDocuments()
		reloaded, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		if reloaded == loaded {
			t.Fatalf("expected new object pointer after unload+reload")
		}
		if !reflect.DeepEqual(reloaded, loaded) {
			t.Fatalf("reloaded = %v, want %v", reloaded, loaded)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			items := slices.Clone(tsdcStrSlice(t, live.Value, "items"))
			live.Value.Set("items", append(items, "c"))
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		after, _ := ts.Session.Snapshot(ctx, liveDoc, convId)
		if !reflect.DeepEqual(tsdcStrSlice(t, after, "items"), []any{"a", "b", "c"}) {
			t.Fatalf("items = %v, want [a b c]", after)
		}
	})

	t.Run("delivers complete publications synchronously after adoption", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		ts, convId := tsdcSetupLive(t, liveDoc)
		defer func() { _ = ts.Session.Close(ctx) }()

		published := len(ts.Publications())
		key := chord.CreateContextKey[string]("listener-tag")
		callCtx := chord.WithContextValue(key, "tag-42", ctx)
		var listenerCtx chord.Context
		unsub := ts.Session.SubscribeCommits(func(_ durable.CommitPublication, delivered chord.Context) {
			listenerCtx = delivered
		})
		defer unsub()

		if err := ts.Session.Commit(callCtx, func(tx durable.Tx) error {
			live, err := tx.Doc(liveDoc, convId)
			if err != nil {
				return err
			}
			live.Value.Set("message", "m")
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		if got, ok := listenerCtx.Value(key); !ok || got != "tag-42" {
			t.Fatalf("expected listenerCtx to carry commit call context, got (%v, %v)", got, ok)
		}
		if len(ts.Publications()) != published+1 {
			t.Fatalf("publications = %d, want %d", len(ts.Publications()), published+1)
		}
	})

	t.Run("publishes close synchronously and supports unsubscription", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		var calls []string
		ts.Session.SubscribeClose(func() { calls = append(calls, "active") })
		unsub := ts.Session.SubscribeClose(func() { calls = append(calls, "removed") })
		unsub()
		if err := ts.Session.Close(ctx); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		if !reflect.DeepEqual(calls, []string{"active"}) {
			t.Fatalf("calls = %v, want [active]", calls)
		}
	})

	t.Run("settles admitted commits before close and rejects later admission", func(t *testing.T) {
		liveDoc := tsdcLiveDoc(nil)
		memberDoc, err := durable.DefineDocFamily(durable.DocFamilyDefinition{
			Kind:    "test.member.close",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func(seed durable.JsonValue) durable.JsonObject {
				return jsonx.ObjectOf("seed", seed)
			},
		})
		if err != nil {
			t.Fatalf("DefineDocFamily failed: %v", err)
		}
		ts, convId := tsdcSetupLive(t, liveDoc)
		gate := ts.Storage.HoldCommits()

		commitCh := make(chan error, 1)
		go func() {
			commitCh <- ts.Session.Commit(ctx, func(tx durable.Tx) error {
				l, err := tx.Doc(liveDoc, convId)
				if err != nil {
					return err
				}
				l.Value.Set("message", "admitted")
				return nil
			})
		}()
		<-gate.Entered

		queuedCh := make(chan error, 1)
		go func() {
			queuedCh <- ts.Session.Commit(ctx, func(tx durable.Tx) error {
				l, err := tx.Doc(liveDoc, convId)
				if err != nil {
					return err
				}
				l.Value.Set("message", "queued")
				return nil
			})
		}()

		snapCh := make(chan struct {
			v   durable.JsonObject
			err error
		}, 1)
		go func() {
			v, err := ts.Session.Snapshot(ctx, memberDoc, "absent")
			snapCh <- struct {
				v   durable.JsonObject
				err error
			}{v, err}
		}()

		// Wait until the queued commit and snapshot are admitted behind the commit line
		// before sealing admission via Close.
		testkit.Flush()

		closeStarted := make(chan struct{})
		ts.Session.SubscribeClose(func() { close(closeStarted) })
		closeErrCh := make(chan error, 1)
		go func() {
			closeErrCh <- ts.Session.Close(ctx)
		}()
		<-closeStarted

		if err := ts.Session.Commit(ctx, func(_ durable.Tx) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("expected closed error on late Commit, got %v", err)
		}
		if _, err := ts.Session.Snapshot(ctx, liveDoc, convId); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("expected closed error on late Snapshot, got %v", err)
		}

		gate.Release()
		if err := <-commitCh; err != nil {
			t.Fatalf("admitted commit failed: %v", err)
		}
		if err := <-queuedCh; err != nil {
			t.Fatalf("queued commit failed: %v", err)
		}
		if res := <-snapCh; res.err != nil || res.v != nil {
			t.Fatalf("admittedSnapshot = (%v, %v), want (nil, nil)", res.v, res.err)
		}
		if err := <-closeErrCh; err != nil {
			t.Fatalf("Close failed: %v", err)
		}
		if _, err := ts.Storage.FindDocument(
			ctx,
			durable.DocumentAddress{
				Kind:  "test.live",
				Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: convId},
			},
			durable.DocumentPointCurrent(),
		); err == nil {
			t.Fatalf("expected closed error querying Storage after Session.Close")
		}
	})
}
