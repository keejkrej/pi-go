// Ported from packages/durable/test/session-checkpoints-migrations.test.ts (pi v1.0.0).

package durable_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tscmDocumentWrites(writes []durable.StorageWrite) []durable.StorageWrite {
	var out []durable.StorageWrite
	for _, w := range writes {
		switch w.(type) {
		case durable.StorageWriteDocumentCreate, durable.StorageWriteDocumentChange, durable.StorageWriteDocumentRetire:
			out = append(out, w)
		}
	}
	return out
}

func tscmMustDoc(t *testing.T, def durable.DocDefinition) *durable.DocToken {
	t.Helper()
	tok, err := durable.DefineDoc(def)
	if err != nil {
		t.Fatalf("DefineDoc(%s) failed: %v", def.Kind, err)
	}
	return tok
}

func tscmMustFamily(t *testing.T, def durable.DocFamilyDefinition) *durable.DocFamilyToken {
	t.Helper()
	tok, err := durable.DefineDocFamily(def)
	if err != nil {
		t.Fatalf("DefineDocFamily(%s) failed: %v", def.Kind, err)
	}
	return tok
}

func TestSessionDocumentCheckpoints(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("selects bases from checkpointWhen predicate using initial threshold", func(t *testing.T) {
		// Note: Go CheckpointWhen is a function field (not a JS method with `this`); closures share the initial count.
		baseInitial := func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) }
		doc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.receiver",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: baseInitial,
			CheckpointWhen: func(value durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool {
				c, _ := value.Get("count")
				ic, _ := baseInitial().Get("count")
				return c == ic.(float64)+2
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		for count := 0; count <= 2; count++ {
			c := float64(count)
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, err := tx.Doc(doc)
				if err != nil {
					return err
				}
				d.Value.Set("count", c)
				return nil
			}); err != nil {
				t.Fatalf("Commit(%d) failed: %v", count, err)
			}
		}
		var kinds []durable.DocumentContentKind
		for _, writes := range ts.Storage.Commits() {
			for _, w := range writes {
				if cw, ok := w.(durable.StorageWriteDocumentChange); ok {
					kinds = append(kinds, cw.Content.Kind)
				}
			}
		}
		if !reflect.DeepEqual(kinds, []durable.DocumentContentKind{durable.DocumentContentDelta, durable.DocumentContentBase}) {
			t.Fatalf("kinds = %v, want [delta base]", kinds)
		}
	})

	t.Run("selects bases only for nonempty ordinary batches and passes exact prepared revision and ops", func(t *testing.T) {
		type callRec struct {
			value durable.JsonObject
			ops   []chord.Op
		}
		var calls, falseCalls []callRec

		baseDoc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.base",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("items", []any{}) },
			CheckpointWhen: func(value durable.JsonObject, ops []chord.Op, _ durable.CheckpointInfo) bool {
				calls = append(calls, callRec{value: value, ops: ops})
				return true
			},
		})
		deltaDoc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.delta",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			CheckpointWhen: func(value durable.JsonObject, ops []chord.Op, _ durable.CheckpointInfo) bool {
				falseCalls = append(falseCalls, callRec{value: value, ops: ops})
				return false
			},
		})
		defaultDoc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.default",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
		})

		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			for _, tok := range []*durable.DocToken{baseDoc, deltaDoc, defaultDoc} {
				if _, err := tx.Doc(tok); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("Create commit failed: %v", err)
		}
		if len(calls) != 0 || len(falseCalls) != 0 {
			t.Fatalf("checkpointWhen should not run on create")
		}
		commits := ts.Storage.Commits()
		for _, w := range tscmDocumentWrites(commits[len(commits)-1]) {
			cw, ok := w.(durable.StorageWriteDocumentCreate)
			if !ok || cw.Content.Kind != durable.DocumentContentBase {
				t.Fatalf("expected document.create base, got %#v", w)
			}
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			b, err := tx.Doc(baseDoc)
			if err != nil {
				return err
			}
			b.Value.Set("items", []any{"x"})
			d, err := tx.Doc(deltaDoc)
			if err != nil {
				return err
			}
			c1, _ := d.Value.Get("count")
			d.Value.Set("count", c1.(float64)+1)
			def, err := tx.Doc(defaultDoc)
			if err != nil {
				return err
			}
			c2, _ := def.Value.Get("count")
			def.Value.Set("count", c2.(float64)+1)
			return nil
		}); err != nil {
			t.Fatalf("Update commit failed: %v", err)
		}
		testkit.Flush()
		admittedAll := ts.Storage.AdmittedCommits()
		var writes []durable.StorageWriteDocumentChange
		for _, w := range admittedAll[len(admittedAll)-1] {
			if cw, ok := w.(durable.StorageWriteDocumentChange); ok {
				writes = append(writes, cw)
			}
		}
		if len(writes) != 3 ||
			writes[0].Content.Kind != durable.DocumentContentBase ||
			writes[1].Content.Kind != durable.DocumentContentDelta ||
			writes[2].Content.Kind != durable.DocumentContentDelta {
			t.Fatalf("unexpected change kinds: %#v", writes)
		}
		if len(calls) != 1 || len(falseCalls) != 1 {
			t.Fatalf("calls=%d, falseCalls=%d", len(calls), len(falseCalls))
		}
		snapshot, _ := ts.Session.Snapshot(ctx, baseDoc)
		deltaSnapshot, _ := ts.Session.Snapshot(ctx, deltaDoc)
		pubs := ts.Publications()
		changes := testkit.DocumentChanges(pubs[len(pubs)-1])
		published, publishedDelta := changes[0], changes[1]
		if calls[0].value != snapshot || len(calls[0].ops) == 0 || len(published.Ops) == 0 || &calls[0].ops[0] != &published.Ops[0] {
			t.Fatalf("expected exact snapshot and ops pointer identity for baseDoc")
		}
		if falseCalls[0].value != deltaSnapshot || len(falseCalls[0].ops) == 0 || len(publishedDelta.Ops) == 0 || &falseCalls[0].ops[0] != &publishedDelta.Ops[0] {
			t.Fatalf("expected exact snapshot and ops pointer identity for deltaDoc")
		}
		if len(writes[1].Content.Ops) == 0 || &writes[1].Content.Ops[0] != &falseCalls[0].ops[0] {
			t.Fatalf("expected admitted delta ops pointer == predicate ops pointer")
		}
		if writes[0].Content.Value != snapshot {
			t.Fatalf("expected admitted base value pointer == snapshot pointer")
		}
	})

	t.Run("passes the stored delta count since the newest base, including after unload and version bases", func(t *testing.T) {
		var seen []int
		v1 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.deltas-since-base",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, info durable.CheckpointInfo) bool {
				seen = append(seen, info.DeltasSinceBase)
				return info.DeltasSinceBase >= 2
			},
		})
		v2 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.deltas-since-base",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c)
			},
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, info durable.CheckpointInfo) bool {
				seen = append(seen, info.DeltasSinceBase)
				return false
			},
		})

		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		increment := func(tok *durable.DocToken) {
			t.Helper()
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, err := tx.Doc(tok)
				if err != nil {
					return err
				}
				c, _ := d.Value.Get("count")
				d.Value.Set("count", c.(float64)+1)
				return nil
			}); err != nil {
				t.Fatalf("increment failed: %v", err)
			}
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(v1)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		increment(v1)
		increment(v1)
		increment(v1)
		_ = ts.Session.UnloadDocuments()
		increment(v1)
		if !reflect.DeepEqual(seen, []int{0, 1, 2, 0}) {
			t.Fatalf("seen = %v, want [0 1 2 0]", seen)
		}
		commits := ts.Storage.Commits()
		var kinds []durable.DocumentContentKind
		for _, writes := range commits[len(commits)-4:] {
			kinds = append(kinds, writes[0].(durable.StorageWriteDocumentChange).Content.Kind)
		}
		if !reflect.DeepEqual(kinds, []durable.DocumentContentKind{
			durable.DocumentContentDelta,
			durable.DocumentContentDelta,
			durable.DocumentContentBase,
			durable.DocumentContentDelta,
		}) {
			t.Fatalf("kinds = %v", kinds)
		}

		// Required version base resets the count without calling CheckpointWhen.
		increment(v2)
		increment(v2)
		if !reflect.DeepEqual(seen, []int{0, 1, 2, 0, 0}) {
			t.Fatalf("seen after v2 = %v, want [0 1 2 0 0]", seen)
		}
		commits = ts.Storage.Commits()
		prev := commits[len(commits)-2][0].(durable.StorageWriteDocumentChange)
		if prev.Content.Kind != durable.DocumentContentBase || prev.Content.Version != 2 {
			t.Fatalf("expected v2 base write, got %#v", prev)
		}
	})

	t.Run("skips the predicate for empty batches but calls it for nonempty structural no-ops", func(t *testing.T) {
		calls := 0
		doc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.no-op",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("items", []any{"a", "b"}) },
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool {
				calls++
				return false
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(doc)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		commits := len(ts.Storage.Commits())

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			v, err := tx.Doc(doc)
			if err != nil {
				return err
			}
			items := append([]any(nil), func() []any { s, _ := v.Value.Get("items"); return s.([]any) }()...)
			items = append(items, "x")
			items = items[:len(items)-1]
			v.Value.Set("items", items)
			return nil
		}); err != nil {
			t.Fatalf("empty batch failed: %v", err)
		}
		if calls != 0 || len(ts.Storage.Commits()) != commits {
			t.Fatalf("expected 0 calls and no storage commit, got calls=%d commits=%d", calls, len(ts.Storage.Commits()))
		}

		// Reordering array elements produces non-empty ops and consults checkpointWhen once.
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			v, err := tx.Doc(doc)
			if err != nil {
				return err
			}
			v.Value.Set("items", []any{"b", "a"})
			return nil
		}); err != nil {
			t.Fatalf("non-empty batch failed: %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		all := ts.Storage.Commits()
		last := all[len(all)-1][0].(durable.StorageWriteDocumentChange)
		if last.Content.Kind != durable.DocumentContentDelta {
			t.Fatalf("expected delta, got %#v", last)
		}
	})

	t.Run("persists repeated false decisions as deltas and replays the complete tail", func(t *testing.T) {
		calls := 0
		doc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.tail",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("values", []any{}) },
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool {
				calls++
				return false
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(doc)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		for val := 1; val <= 8; val++ {
			v := float64(val)
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				d, err := tx.Doc(doc)
				if err != nil {
					return err
				}
				raw, _ := d.Value.Get("values")
				slice := append([]any(nil), raw.([]any)...)
				d.Value.Set("values", append(slice, v))
				return nil
			}); err != nil {
				t.Fatalf("append %d failed: %v", val, err)
			}
		}
		var changes []durable.StorageWriteDocumentChange
		for _, writes := range ts.Storage.Commits() {
			for _, w := range writes {
				if cw, ok := w.(durable.StorageWriteDocumentChange); ok {
					changes = append(changes, cw)
				}
			}
		}
		if len(changes) != 8 || calls != 8 {
			t.Fatalf("changes=%d calls=%d, want 8/8", len(changes), calls)
		}
		for _, cw := range changes {
			if cw.Content.Kind != durable.DocumentContentDelta {
				t.Fatalf("expected delta, got %#v", cw)
			}
		}
		_ = ts.Session.UnloadDocuments()
		snap, _ := ts.Session.Snapshot(ctx, doc)
		want := jsonx.ObjectOf("values", []any{float64(1), float64(2), float64(3), float64(4), float64(5), float64(6), float64(7), float64(8)})
		if !reflect.DeepEqual(snap, want) {
			t.Fatalf("replayed snap = %v, want %v", snap, want)
		}
	})

	t.Run("keeps a prepared root replacement as a delta when the predicate is false", func(t *testing.T) {
		doc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.root-replacement",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject {
				obj := jsonx.NewObject()
				for i := 0; i < 4100; i++ {
					obj.Set(fmt.Sprintf("field%d", i), float64(0))
				}
				return obj
			},
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool { return false },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(doc)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(doc)
			if err != nil {
				return err
			}
			for i := 0; i < 4100; i++ {
				d.Value.Set(fmt.Sprintf("field%d", i), float64(1))
			}
			return nil
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}
		all := ts.Storage.Commits()
		write := all[len(all)-1][0].(durable.StorageWriteDocumentChange)
		if write.Content.Kind != durable.DocumentContentDelta || len(write.Content.Ops) != 1 || write.Content.Ops[0].Verb() != "r" {
			t.Fatalf("expected single OpReplace delta, got %#v", write.Content)
		}
		_ = ts.Session.UnloadDocuments()
		snap, _ := ts.Session.Snapshot(ctx, doc)
		if v, _ := snap.Get("field4099"); v != float64(1) {
			t.Fatalf("field4099 = %v, want 1", v)
		}
	})

	t.Run("uses ordinary checkpoint selection before retirement", func(t *testing.T) {
		calls := 0
		doc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "checkpoint.retire",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool {
				calls++
				return true
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(doc)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(doc)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(1))
			return tx.RetireDoc(doc)
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1", calls)
		}
		all := ts.Storage.Commits()
		writes := tscmDocumentWrites(all[len(all)-1])
		if len(writes) != 2 {
			t.Fatalf("expected 2 writes, got %d", len(writes))
		}
		if cw, ok := writes[0].(durable.StorageWriteDocumentChange); !ok || cw.Content.Kind != durable.DocumentContentBase {
			t.Fatalf("expected base write before retire, got %#v", writes[0])
		}
		if _, ok := writes[1].(durable.StorageWriteDocumentRetire); !ok {
			t.Fatalf("expected retire write, got %#v", writes[1])
		}
	})
}

func TestSessionDocumentMigrations(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("migrates read-only once per cold load, copies the callback result, and writes nothing", func(t *testing.T) {
		old := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.read-only",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(2)) },
		})
		calls := 0
		var retained durable.JsonObject
		current := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.read-only",
			Version: 3,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0), "labels", []any{}) },
			Migrate: func(value durable.JsonObject, fromVersion int) durable.JsonObject {
				if fromVersion != 1 {
					t.Fatalf("fromVersion = %d, want 1", fromVersion)
				}
				calls++
				c, _ := value.Get("count")
				retained = jsonx.ObjectOf("count", c, "labels", []any{"migrated"})
				return retained
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(old)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		commits := len(ts.Storage.Commits())

		first, err := ts.Session.Snapshot(ctx, current)
		if err != nil {
			t.Fatalf("Snapshot 1 failed: %v", err)
		}
		if again, _ := ts.Session.Snapshot(ctx, current); again != first {
			t.Fatalf("expected cached pointer on second Snapshot")
		}
		if calls != 1 || len(ts.Storage.Commits()) != commits {
			t.Fatalf("calls=%d commits=%d/%d", calls, len(ts.Storage.Commits()), commits)
		}
		retained.Set("count", float64(99))
		retained.Set("labels", []any{"migrated", "mutated"})
		want := jsonx.ObjectOf("count", float64(2), "labels", []any{"migrated"})
		if !reflect.DeepEqual(first, want) {
			t.Fatalf("first = %v, want %v", first, want)
		}

		_ = ts.Session.UnloadDocuments()
		second, err := ts.Session.Snapshot(ctx, current)
		if err != nil {
			t.Fatalf("Snapshot 2 failed: %v", err)
		}
		if second == first || !reflect.DeepEqual(second, first) {
			t.Fatalf("expected fresh equal object on second cold load")
		}
		if calls != 2 || len(ts.Storage.Commits()) != commits {
			t.Fatalf("calls=%d commits=%d/%d", calls, len(ts.Storage.Commits()), commits)
		}
	})

	t.Run("writes the required base on the first successful transaction, then writes deltas", func(t *testing.T) {
		old := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.transition",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(4)) },
		})
		checkpoints := 0
		current := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.transition",
			Version: 3,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			Migrate: func(value durable.JsonObject, fromVersion int) durable.JsonObject {
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c.(float64)+float64(fromVersion-1))
			},
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool {
				checkpoints++
				return false
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(old)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()

		watch, err := ts.Session.WatchDoc(ctx, old)
		if err != nil {
			t.Fatalf("WatchDoc(old) failed: %v", err)
		}
		type frame struct {
			value durable.JsonObject
			ops   []chord.Op
		}
		var framesMu sync.Mutex
		var frames []frame
		_ = watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			framesMu.Lock()
			frames = append(frames, frame{value: value, ops: ops})
			framesMu.Unlock()
			return nil
		})

		snapshot, err := ts.Session.Snapshot(ctx, current)
		if err != nil {
			t.Fatalf("Snapshot(current) failed: %v", err)
		}
		if !reflect.DeepEqual(snapshot, jsonx.ObjectOf("count", float64(4))) {
			t.Fatalf("snapshot = %v", snapshot)
		}
		currentWatch, err := ts.Session.WatchDoc(ctx, current)
		if err != nil {
			t.Fatalf("WatchDoc(current) failed: %v", err)
		}
		var currentFrames []durable.JsonObject
		var cfMu sync.Mutex
		_ = currentWatch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			cfMu.Lock()
			currentFrames = append(currentFrames, value)
			cfMu.Unlock()
			return nil
		})

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(current)
			return err
		}); err != nil {
			t.Fatalf("Commit(current) failed: %v", err)
		}
		testkit.Flush()
		all := ts.Storage.Commits()
		cw := all[len(all)-1][0].(durable.StorageWriteDocumentChange)
		if cw.Content.Kind != durable.DocumentContentBase || cw.Content.Version != 3 || !reflect.DeepEqual(cw.Content.Value, jsonx.ObjectOf("count", float64(4))) {
			t.Fatalf("expected v3 base {count:4}, got %#v", cw)
		}
		if checkpoints != 0 {
			t.Fatalf("checkpoints = %d, want 0", checkpoints)
		}
		pubs := ts.Publications()
		pubChange := testkit.DocumentChanges(pubs[len(pubs)-1])[0]
		if pubChange.Version == nil || *pubChange.Version != 3 || len(pubChange.Ops) != 0 {
			t.Fatalf("unexpected pubChange: %#v", pubChange)
		}

		if err := testkit.Eventually(func() (bool, error) {
			framesMu.Lock()
			defer framesMu.Unlock()
			return len(frames) == 1, nil
		}); err != nil {
			t.Fatalf("Eventually old watch frame failed: %v", err)
		}
		framesMu.Lock()
		if len(frames[0].ops) != 1 || frames[0].ops[0].Verb() != "r" {
			t.Fatalf("expected root replacement frame on old watch, got %#v", frames)
		}
		framesMu.Unlock()

		watch.Stop()
		currentWatch.Stop()
		cfMu.Lock()
		if len(currentFrames) != 0 {
			t.Fatalf("expected 0 frames on currentWatch, got %v", currentFrames)
		}
		cfMu.Unlock()

		if s, _ := ts.Session.Snapshot(ctx, current); s != snapshot {
			t.Fatalf("expected cached pointer identity")
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(current)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(7))
			return nil
		}); err != nil {
			t.Fatalf("update failed: %v", err)
		}
		all = ts.Storage.Commits()
		cw2 := all[len(all)-1][0].(durable.StorageWriteDocumentChange)
		if cw2.Content.Kind != durable.DocumentContentDelta || cw2.Content.Version != 3 || checkpoints != 1 {
			t.Fatalf("expected v3 delta with 1 checkpoint check, got %#v (checkpoints=%d)", cw2, checkpoints)
		}
		_ = ts.Session.UnloadDocuments()
		snapAfter, _ := ts.Session.Snapshot(ctx, current)
		if c, _ := snapAfter.Get("count"); c != float64(7) {
			t.Fatalf("count = %v, want 7", c)
		}
	})

	t.Run("rolls migration and edits back with the callback, then coalesces later edits into one base", func(t *testing.T) {
		old := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.rollback",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(1)) },
		})
		migrations := 0
		current := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.rollback",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0), "migrated", false) },
			Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
				migrations++
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c, "migrated", true)
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(old)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		commits := len(ts.Storage.Commits())

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(current)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(8))
			return fmt.Errorf("rollback")
		}); err == nil || !strings.Contains(err.Error(), "rollback") {
			t.Fatalf("expected rollback error, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits || migrations != 1 {
			t.Fatalf("commits=%d/%d migrations=%d", len(ts.Storage.Commits()), commits, migrations)
		}
		if snap, _ := ts.Session.Snapshot(ctx, current); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(1), "migrated", true)) {
			t.Fatalf("snap = %v", snap)
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(current)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(9))
			d.Value.Set("migrated", false)
			return nil
		}); err != nil {
			t.Fatalf("coalesce Commit failed: %v", err)
		}
		all := ts.Storage.Commits()
		writes := tscmDocumentWrites(all[len(all)-1])
		if len(writes) != 1 {
			t.Fatalf("expected 1 write, got %d", len(writes))
		}
		cw := writes[0].(durable.StorageWriteDocumentChange)
		if cw.Content.Kind != durable.DocumentContentBase || cw.Content.Version != 2 ||
			!reflect.DeepEqual(cw.Content.Value, jsonx.ObjectOf("count", float64(9), "migrated", false)) {
			t.Fatalf("unexpected write: %#v", cw)
		}
		testkit.Flush()
		pubs := ts.Publications()
		published := testkit.DocumentChanges(pubs[len(pubs)-1])[0]
		admittedAll := ts.Storage.AdmittedCommits()
		admitted := admittedAll[len(admittedAll)-1][0].(durable.StorageWriteDocumentChange)
		snap, _ := ts.Session.Snapshot(ctx, current)
		if published.Value != snap || published.Value != admitted.Content.Value || len(published.Ops) == 0 || migrations != 1 {
			t.Fatalf("unexpected publication/admitted state")
		}
	})

	t.Run("strict-checks migration results before tracker ownership and remains usable after rejection", func(t *testing.T) {
		old := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.invalid",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(1)) },
		})
		invalid := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.invalid",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.NewObject() },
			Migrate: func(_ durable.JsonObject, _ int) durable.JsonObject {
				return jsonx.ObjectOf("invalid", []byte{1, 2})
			},
		})
		other := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.invalid.other",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("ok", true) },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(old)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		commits := len(ts.Storage.Commits())

		if _, err := ts.Session.Snapshot(ctx, invalid); err == nil || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("expected strict JSON error on Snapshot, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(invalid)
			return err
		}); err == nil || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("expected strict JSON error on Commit, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits changed on rejected migration")
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(other)
			return err
		}); err != nil {
			t.Fatalf("other Commit failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, other); !reflect.DeepEqual(snap, jsonx.ObjectOf("ok", true)) {
			t.Fatalf("other = %v", snap)
		}
	})

	t.Run("rejects newer stored versions and older versions without migration", func(t *testing.T) {
		v2 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.compatibility",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(2)) },
		})
		v1 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.compatibility",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(1)) },
		})
		v3NoMigrate := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.compatibility",
			Version: 3,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(3)) },
		})

		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(v2)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		commits := len(ts.Storage.Commits())

		if _, err := ts.Session.Snapshot(ctx, v1); err == nil || !strings.Contains(err.Error(), "newer version 2 than 1") {
			t.Fatalf("expected newer version error on Snapshot, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(v1)
			return err
		}); err == nil || !strings.Contains(err.Error(), "newer version 2 than 1") {
			t.Fatalf("expected newer version error on Commit, got %v", err)
		}
		if _, err := ts.Session.Snapshot(ctx, v3NoMigrate); err == nil || !strings.Contains(err.Error(), "requires migration from version 2") {
			t.Fatalf("expected migration error on Snapshot, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(v3NoMigrate)
			return err
		}); err == nil || !strings.Contains(err.Error(), "requires migration from version 2") {
			t.Fatalf("expected migration error on Commit, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits grew on incompatible access")
		}
	})

	t.Run("persists a required migration base before retirement without consulting checkpointWhen", func(t *testing.T) {
		old := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.retire",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(1)) },
		})
		current := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.retire",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c)
			},
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool {
				t.Fatalf("checkpointWhen must not run during migration-before-retire")
				return false
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(old)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if _, err := tx.Doc(current); err != nil {
				return err
			}
			return tx.RetireDoc(current)
		}); err != nil {
			t.Fatalf("migrate+retire failed: %v", err)
		}
		all := ts.Storage.Commits()
		writes := tscmDocumentWrites(all[len(all)-1])
		if len(writes) != 2 {
			t.Fatalf("expected 2 writes, got %d", len(writes))
		}
		cw := writes[0].(durable.StorageWriteDocumentChange)
		if cw.Content.Kind != durable.DocumentContentBase || cw.Content.Version != 2 || !reflect.DeepEqual(cw.Content.Value, jsonx.ObjectOf("count", float64(1))) {
			t.Fatalf("unexpected change write: %#v", cw)
		}
		if _, ok := writes[1].(durable.StorageWriteDocumentRetire); !ok {
			t.Fatalf("expected retire write, got %#v", writes[1])
		}
	})

	t.Run("leaves unaccessed older documents and unavailable definitions untouched", func(t *testing.T) {
		firstV1 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.lazy.first",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(1)) },
		})
		secondV1 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.lazy.second",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(2)) },
		})
		secondMigrations := 0
		firstV2 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.lazy.first",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c)
			},
		})
		_ = tscmMustDoc(t, durable.DocDefinition{
			Kind:    "migration.lazy.second",
			Version: 2,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
			Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
				secondMigrations++
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c)
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if _, err := tx.Doc(firstV1); err != nil {
				return err
			}
			_, err := tx.Doc(secondV1)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		commits := len(ts.Storage.Commits())
		if snap, err := ts.Session.Snapshot(ctx, firstV2); err != nil || !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(1))) {
			t.Fatalf("firstV2 = (%v, %v)", snap, err)
		}
		if len(ts.Storage.Commits()) != commits || secondMigrations != 0 {
			t.Fatalf("expected lazy behavior, commits=%d/%d secondMigrations=%d", len(ts.Storage.Commits()), commits, secondMigrations)
		}
		secondRec, err := ts.Storage.FindDocument(
			ctx,
			durable.DocumentAddress{Kind: "migration.lazy.second", Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}},
			durable.DocumentPointCurrent(),
		)
		if err != nil || secondRec == nil {
			t.Fatalf("FindDocument failed: %v %v", secondRec, err)
		}
		stored, err := ts.Storage.Document(ctx, secondRec.Id, durable.DocumentPointCurrent())
		if err != nil || stored == nil || stored.Version != 1 {
			t.Fatalf("stored second = (%#v, %v), want version 1", stored, err)
		}
	})
}

func TestSessionHistoricalDocumentSnapshots(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("migrates current and historical rewindable values independently and follows fork ancestry", func(t *testing.T) {
		v1 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "history.migration",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
		})
		family := tscmMustFamily(t, durable.DocFamilyDefinition{
			Kind:    "history.family",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func(seed durable.JsonValue) durable.JsonObject {
				return jsonx.ObjectOf("seed", seed, "count", float64(0))
			},
		})
		var migrations []int
		v3 := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "history.migration",
			Version: 3,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0), "version", float64(3)) },
			Migrate: func(value durable.JsonObject, fromVersion int) durable.JsonObject {
				migrations = append(migrations, fromVersion)
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c, "version", float64(3))
			},
		})

		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, err := testkit.CreateConversation(ts.Session)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}

		var firstEntry, secondEntry, thirdEntry durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "first"})
			if err != nil {
				return err
			}
			firstEntry = e.Id
			d, err := tx.Doc(v1, convId)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(1))
			fd, err := tx.Doc(family, convId, "member", "seed")
			if err != nil {
				return err
			}
			fd.Value.Set("count", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Commit 1 failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "second"})
			if err != nil {
				return err
			}
			secondEntry = e.Id
			d, err := tx.Doc(v1, convId)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(2))
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		commits := len(ts.Storage.Commits())

		if snap, err := ts.Session.Snapshot(ctx, v3, convId); err != nil ||
			!reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(2), "version", float64(3))) {
			t.Fatalf("Snapshot(v3) = (%v, %v)", snap, err)
		}
		if !reflect.DeepEqual(migrations, []int{1}) || len(ts.Storage.Commits()) != commits {
			t.Fatalf("migrations=%v commits=%d/%d", migrations, len(ts.Storage.Commits()), commits)
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "third"})
			if err != nil {
				return err
			}
			thirdEntry = e.Id
			_, err = tx.Doc(v3, convId)
			return err
		}); err != nil {
			t.Fatalf("Commit 3 failed: %v", err)
		}
		all := ts.Storage.Commits()
		hasV3Base := false
		for _, w := range all[len(all)-1] {
			if cw, ok := w.(durable.StorageWriteDocumentChange); ok && cw.Content.Kind == durable.DocumentContentBase && cw.Content.Version == 3 {
				hasV3Base = true
			}
		}
		if !hasV3Base {
			t.Fatalf("expected v3 base write in %#v", all[len(all)-1])
		}

		if snap, _ := ts.Session.SnapshotAsOf(ctx, v3, convId, firstEntry); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(1), "version", float64(3))) {
			t.Fatalf("SnapshotAsOf(1) = %v", snap)
		}
		if snap, _ := ts.Session.SnapshotAsOf(ctx, v3, convId, secondEntry); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(2), "version", float64(3))) {
			t.Fatalf("SnapshotAsOf(2) = %v", snap)
		}
		if snap, _ := ts.Session.SnapshotAsOf(ctx, v3, convId, thirdEntry); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(2), "version", float64(3))) {
			t.Fatalf("SnapshotAsOf(3) = %v", snap)
		}
		if !reflect.DeepEqual(migrations, []int{1, 1, 1}) {
			t.Fatalf("migrations = %v, want [1 1 1]", migrations)
		}
		if snap, _ := ts.Session.SnapshotAsOf(ctx, family, convId, "member", firstEntry); !reflect.DeepEqual(snap, jsonx.ObjectOf("seed", "seed", "count", float64(1))) {
			t.Fatalf("SnapshotAsOf(family) = %v", snap)
		}

		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			child, err := tx.ForkConversation(convId, secondEntry, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = child.Id
			return nil
		}); err != nil {
			t.Fatalf("ForkConversation failed: %v", err)
		}
		if snap, _ := ts.Session.SnapshotAsOf(ctx, v3, childId, firstEntry); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(1), "version", float64(3))) {
			t.Fatalf("child SnapshotAsOf(1) = %v", snap)
		}
		if snap, _ := ts.Session.SnapshotAsOf(ctx, v3, childId, secondEntry); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(2), "version", float64(3))) {
			t.Fatalf("child SnapshotAsOf(2) = %v", snap)
		}
		if snap, _ := ts.Session.SnapshotAsOf(ctx, family, childId, "member", firstEntry); !reflect.DeepEqual(snap, jsonx.ObjectOf("seed", "seed", "count", float64(1))) {
			t.Fatalf("child SnapshotAsOf(family) = %v", snap)
		}
		wantErr := fmt.Sprintf("Entry %d is not visible", thirdEntry)
		if _, err := ts.Session.SnapshotAsOf(ctx, v3, childId, thirdEntry); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("expected %q error, got %v", wantErr, err)
		}
	})

	t.Run("selects the incarnation alive at the entry commit across retirement and recreation", func(t *testing.T) {
		doc := tscmMustDoc(t, durable.DocDefinition{
			Kind:    "history.incarnation",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "initial") },
		})
		ts := testkit.OpenTestSession()
		convId, err := testkit.CreateConversation(ts.Session)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}
		var beforeCreation, createdAt, retiredAt, recreatedAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "before"})
			if err != nil {
				return err
			}
			beforeCreation = e.Id
			return nil
		}); err != nil {
			t.Fatalf("before commit failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "create"})
			if err != nil {
				return err
			}
			createdAt = e.Id
			d, err := tx.Doc(doc, convId)
			if err != nil {
				return err
			}
			d.Value.Set("value", "old")
			return nil
		}); err != nil {
			t.Fatalf("create commit failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "retire"})
			if err != nil {
				return err
			}
			retiredAt = e.Id
			return tx.RetireDoc(doc, convId)
		}); err != nil {
			t.Fatalf("retire commit failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "recreate"})
			if err != nil {
				return err
			}
			recreatedAt = e.Id
			d, err := tx.Doc(doc, convId)
			if err != nil {
				return err
			}
			d.Value.Set("value", "new")
			return nil
		}); err != nil {
			t.Fatalf("recreate commit failed: %v", err)
		}

		if snap, err := ts.Session.SnapshotAsOf(ctx, doc, convId, beforeCreation); err != nil || snap != nil {
			t.Fatalf("beforeCreation = (%v, %v), want nil", snap, err)
		}
		if snap, err := ts.Session.SnapshotAsOf(ctx, doc, convId, createdAt); err != nil || !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "old")) {
			t.Fatalf("createdAt = (%v, %v)", snap, err)
		}
		if snap, err := ts.Session.SnapshotAsOf(ctx, doc, convId, retiredAt); err != nil || snap != nil {
			t.Fatalf("retiredAt = (%v, %v), want nil", snap, err)
		}
		if snap, err := ts.Session.SnapshotAsOf(ctx, doc, convId, recreatedAt); err != nil || !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "new")) {
			t.Fatalf("recreatedAt = (%v, %v)", snap, err)
		}
		_ = ts.Session.Close(ctx)
		if _, err := ts.Session.SnapshotAsOf(ctx, doc, convId, recreatedAt); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("expected closed error after Close, got %v", err)
		}
	})
}

func TestSessionTrackerCacheAcrossDefinitionVersions(t *testing.T) {
	ctx := testkit.TestContext
	v1Doc := tscmMustDoc(t, durable.DocDefinition{
		Kind:    "cache.versioned",
		Version: 1,
		Scope:   durable.DocumentScopeSession,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("name", "first") },
	})
	v2Doc := tscmMustDoc(t, durable.DocDefinition{
		Kind:    "cache.versioned",
		Version: 2,
		Scope:   durable.DocumentScopeSession,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("names", []any{}) },
		Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
			n, _ := value.Get("name")
			return jsonx.ObjectOf("names", []any{n})
		},
	})

	t.Run("migrates a document cached by an older token without unloading", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(v1Doc)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, v1Doc); !reflect.DeepEqual(snap, jsonx.ObjectOf("name", "first")) {
			t.Fatalf("v1 = %v", snap)
		}

		// Reloaded extension code accesses the still-cached document with a newer token.
		if snap, _ := ts.Session.Snapshot(ctx, v2Doc); !reflect.DeepEqual(snap, jsonx.ObjectOf("names", []any{"first"})) {
			t.Fatalf("v2 = %v", snap)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(v2Doc)
			if err != nil {
				return err
			}
			raw, _ := d.Value.Get("names")
			slice := append([]any(nil), raw.([]any)...)
			d.Value.Set("names", append(slice, "second"))
			return nil
		}); err != nil {
			t.Fatalf("v2 commit failed: %v", err)
		}
		all := ts.Storage.Commits()
		w := tscmDocumentWrites(all[len(all)-1])[0].(durable.StorageWriteDocumentChange)
		if w.Content.Version != 2 || w.Content.Kind != durable.DocumentContentBase ||
			!reflect.DeepEqual(w.Content.Value, jsonx.ObjectOf("names", []any{"first", "second"})) {
			t.Fatalf("unexpected write: %#v", w)
		}
		if _, err := ts.Session.Snapshot(ctx, v1Doc); err == nil || !strings.Contains(err.Error(), "newer version 2") {
			t.Fatalf("expected newer version 2 error, got %v", err)
		}
	})

	t.Run("sends observers of an older shape a root replacement after a newer token writes", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(v1Doc)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		state, err := ts.Session.DocumentState(ctx, v1Doc)
		if err != nil {
			t.Fatalf("DocumentState failed: %v", err)
		}
		defer state.Dispose()
		watch, err := ts.Session.WatchDoc(ctx, v1Doc)
		if err != nil {
			t.Fatalf("WatchDoc failed: %v", err)
		}
		defer func() { watch.Stop() }()

		var framesMu sync.Mutex
		var frames [][]chord.Op
		delivered := make(chan struct{})
		var once sync.Once
		_ = watch.Start(func(_ durable.JsonObject, ops []chord.Op, _ chord.Context) error {
			framesMu.Lock()
			frames = append(frames, ops)
			framesMu.Unlock()
			once.Do(func() { close(delivered) })
			return nil
		})
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(v2Doc)
			if err != nil {
				return err
			}
			raw, _ := d.Value.Get("names")
			slice := append([]any(nil), raw.([]any)...)
			d.Value.Set("names", append(slice, "second"))
			return nil
		}); err != nil {
			t.Fatalf("v2 commit failed: %v", err)
		}
		<-delivered
		wantVal := jsonx.ObjectOf("names", []any{"first", "second"})
		if err := testkit.Eventually(func() (bool, error) {
			v, ok := state.Value()
			return ok && reflect.DeepEqual(v, wantVal), nil
		}); err != nil {
			v, ok := state.Value()
			t.Fatalf("state.Value() = (%v, %v), want %v (%v)", v, ok, wantVal, err)
		}
		if !reflect.DeepEqual(watch.Value(), wantVal) {
			t.Fatalf("watch.Value() = %v, want %v", watch.Value(), wantVal)
		}
		framesMu.Lock()
		if len(frames) != 1 || len(frames[0]) != 1 || frames[0][0].Verb() != "r" {
			t.Fatalf("expected single OpReplace frame, got %#v", frames)
		}
		if rep, ok := frames[0][0].(chord.OpReplace); !ok || !reflect.DeepEqual(rep.Value, wantVal) {
			t.Fatalf("unexpected OpReplace payload: %#v", frames[0][0])
		}
		framesMu.Unlock()
	})

	t.Run("serves an older token from Storage after a newer token migrated only in memory", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(v1Doc)
			return err
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		if snap, _ := ts.Session.Snapshot(ctx, v2Doc); !reflect.DeepEqual(snap, jsonx.ObjectOf("names", []any{"first"})) {
			t.Fatalf("v2 = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, v1Doc); !reflect.DeepEqual(snap, jsonx.ObjectOf("name", "first")) {
			t.Fatalf("v1 = %v", snap)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(v1Doc)
			if err != nil {
				return err
			}
			d.Value.Set("name", "renamed")
			return nil
		}); err != nil {
			t.Fatalf("v1 commit failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, v2Doc); !reflect.DeepEqual(snap, jsonx.ObjectOf("names", []any{"renamed"})) {
			t.Fatalf("v2 after rename = %v", snap)
		}
	})
}
