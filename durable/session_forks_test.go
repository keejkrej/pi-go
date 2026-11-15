// Ported from packages/durable/test/session-forks.test.ts (pi v1.0.0).

package durable_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsfkMustDoc(t *testing.T, def durable.DocDefinition) *durable.DocToken {
	t.Helper()
	tok, err := durable.DefineDoc(def)
	if err != nil {
		t.Fatalf("DefineDoc(%s) failed: %v", def.Kind, err)
	}
	return tok
}

func tsfkMustFamily(t *testing.T, def durable.DocFamilyDefinition) *durable.DocFamilyToken {
	t.Helper()
	tok, err := durable.DefineDocFamily(def)
	if err != nil {
		t.Fatalf("DefineDocFamily(%s) failed: %v", def.Kind, err)
	}
	return tok
}

func tsfkDocCreates(writes []durable.StorageWrite) []durable.StorageWriteDocumentCreate {
	var out []durable.StorageWriteDocumentCreate
	for _, w := range writes {
		if c, ok := w.(durable.StorageWriteDocumentCreate); ok {
			out = append(out, c)
		}
	}
	return out
}

func tsfkDocCopies(writes []durable.StorageWrite) []durable.StorageWriteDocumentCopy {
	var out []durable.StorageWriteDocumentCopy
	for _, w := range writes {
		if c, ok := w.(durable.StorageWriteDocumentCopy); ok {
			out = append(out, c)
		}
	}
	return out
}

func TestSessionConversationDocumentForks(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("copies as-of and current singleton and family bases while leaving initial documents absent", func(t *testing.T) {
		asOf := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.policies.as-of",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "as-initial") },
		})
		current := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.policies.current",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "current-initial") },
		})
		initialDoc := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.policies.initial",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkInitial,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "fresh") },
		})
		asOfFamily := tsfkMustFamily(t, durable.DocFamilyDefinition{
			Kind:    "fork.policies.as-of-family",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func(seed durable.JsonValue) durable.JsonObject { return jsonx.ObjectOf("value", seed) },
		})
		currentFamily := tsfkMustFamily(t, durable.DocFamilyDefinition{
			Kind:    "fork.policies.current-family",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func(seed durable.JsonValue) durable.JsonObject { return jsonx.ObjectOf("value", seed) },
		})

		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, err := testkit.CreateConversation(ts.Session)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "fork-point"})
			if err != nil {
				return err
			}
			forkAt = e.Id
			for _, pair := range []struct {
				fn  func() (chord.Draft[durable.JsonObject], error)
				val string
			}{
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(asOf, parentId) }, "as-at-fork"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(current, parentId) }, "current-at-fork"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(initialDoc, parentId) }, "parent-only"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(asOfFamily, parentId, "a", "unused") }, "family-as-a"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(asOfFamily, parentId, "b", "unused") }, "family-as-b"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(currentFamily, parentId, "a", "unused") }, "family-current-a"},
			} {
				d, err := pair.fn()
				if err != nil {
					return err
				}
				d.Value.Set("value", pair.val)
			}
			return nil
		}); err != nil {
			t.Fatalf("Commit 1 failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			for _, pair := range []struct {
				fn  func() (chord.Draft[durable.JsonObject], error)
				val string
			}{
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(asOf, parentId) }, "as-after-fork"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(current, parentId) }, "current-when-copied"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(asOfFamily, parentId, "a", "unused") }, "family-as-after"},
				{func() (chord.Draft[durable.JsonObject], error) { return tx.Doc(currentFamily, parentId, "a", "unused") }, "family-current-when-copied"},
			} {
				d, err := pair.fn()
				if err != nil {
					return err
				}
				d.Value.Set("value", pair.val)
			}
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}
		testkit.Flush()
		docReads := ts.Storage.DocumentReadCount()

		var child *durable.ConversationRecord
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			child = c
			return nil
		}); err != nil {
			t.Fatalf("ForkConversation failed: %v", err)
		}
		testkit.Flush()
		if ts.Storage.DocumentReadCount() != docReads {
			t.Fatalf("expected fork copy without reading document values: before=%d after=%d", docReads, ts.Storage.DocumentReadCount())
		}

		expectSnap := func(want any, tok durable.AnyDocToken, args ...any) {
			t.Helper()
			got, err := ts.Session.Snapshot(ctx, tok, args...)
			if err != nil {
				t.Fatalf("Snapshot failed: %v", err)
			}
			if want == nil {
				if got != nil {
					t.Fatalf("expected nil snapshot, got %v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot = %v, want %v", got, want)
			}
		}
		expectSnap(jsonx.ObjectOf("value", "as-at-fork"), asOf, child.Id)
		expectSnap(jsonx.ObjectOf("value", "current-when-copied"), current, child.Id)
		expectSnap(nil, initialDoc, child.Id)
		expectSnap(jsonx.ObjectOf("value", "family-as-a"), asOfFamily, child.Id, "a")
		expectSnap(jsonx.ObjectOf("value", "family-as-b"), asOfFamily, child.Id, "b")
		expectSnap(jsonx.ObjectOf("value", "family-current-when-copied"), currentFamily, child.Id, "a")

		admitted := ts.Storage.AdmittedCommits()
		copies := tsfkDocCopies(admitted[len(admitted)-1])
		if len(copies) != 5 {
			t.Fatalf("expected 5 document.copy writes, got %d", len(copies))
		}
		for _, cp := range copies {
			if cp.Record.Scope.Kind != durable.DocumentScopeConversation || cp.Record.Scope.ConversationId != child.Id {
				t.Fatalf("unexpected copy scope: %#v", cp.Record.Scope)
			}
		}
		pubs := ts.Publications()
		pub := pubs[len(pubs)-1]
		copiedChanges := testkit.DocumentCopyChanges(pub)
		if len(copiedChanges) != 5 {
			t.Fatalf("expected 5 document.copy changes, got %d", len(copiedChanges))
		}
		for _, ch := range copiedChanges {
			if ch.Record.CreatedAt != pub.Seq || ch.ConversationId != child.Id {
				t.Fatalf("unexpected copy change metadata: %#v", ch)
			}
			var matched *durable.StorageWriteDocumentCopy
			for i := range copies {
				if copies[i].Record.Id == ch.Record.Id {
					matched = &copies[i]
					break
				}
			}
			if matched == nil || matched.Source != ch.Source {
				t.Fatalf("copy change %#v did not match write %#v", ch, matched)
			}
		}

		parentRec, _ := ts.Storage.FindDocument(ctx, durable.DocumentAddress{
			Kind:  asOf.Definition.Kind,
			Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: parentId},
		}, durable.DocumentPointCurrent())
		childRec, _ := ts.Storage.FindDocument(ctx, durable.DocumentAddress{
			Kind:  asOf.Definition.Kind,
			Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: child.Id},
		}, durable.DocumentPointCurrent())
		if childRec.Id == parentRec.Id {
			t.Fatalf("expected distinct document ID for forked child")
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(asOf, child.Id)
			if err != nil {
				return err
			}
			d.Value.Set("value", "child-independent")
			return nil
		}); err != nil {
			t.Fatalf("child edit failed: %v", err)
		}
		expectSnap(jsonx.ObjectOf("value", "as-after-fork"), asOf, parentId)
		expectSnap(jsonx.ObjectOf("value", "child-independent"), asOf, child.Id)
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(initialDoc, child.Id)
			if err != nil {
				return err
			}
			d.Value.Set("value", "child-created")
			return nil
		}); err != nil {
			t.Fatalf("child initialDoc create failed: %v", err)
		}
		expectSnap(jsonx.ObjectOf("value", "child-created"), initialDoc, child.Id)
	})

	t.Run("uses final document state from the fork entry commit while excluding later same-commit entries", func(t *testing.T) {
		doc := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.same-commit",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "initial") },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt, excluded durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e1, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "included"})
			if err != nil {
				return err
			}
			forkAt = e1.Id
			d, err := tx.Doc(doc, parentId)
			if err != nil {
				return err
			}
			d.Value.Set("value", "final-state-of-commit")
			e2, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "excluded"})
			if err != nil {
				return err
			}
			excluded = e2.Id
			return nil
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("fork failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, doc, childId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "final-state-of-commit")) {
			t.Fatalf("snap = %v", snap)
		}
		var visibleIds []durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: childId}, 10, nil)
			if err != nil {
				return err
			}
			for _, it := range page.Items {
				visibleIds = append(visibleIds, it.Id)
			}
			return nil
		}); err != nil {
			t.Fatalf("scan entries failed: %v", err)
		}
		hasForkAt, hasExcluded := false, false
		for _, id := range visibleIds {
			if id == forkAt {
				hasForkAt = true
			}
			if id == excluded {
				hasExcluded = true
			}
		}
		if !hasForkAt || hasExcluded {
			t.Fatalf("visibleIds = %v, want contains %d and not %d", visibleIds, forkAt, excluded)
		}
	})

	t.Run("selects the entry-owning ancestor for as-of copies and the immediate parent for current copies", func(t *testing.T) {
		asOf := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.ancestry.as-of",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "initial") },
		})
		current := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.ancestry.current",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "initial") },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		rootId, _ := testkit.CreateConversation(ts.Session)
		var inherited, parentEntry durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "root"})
			if err != nil {
				return err
			}
			inherited = e.Id
			d1, _ := tx.Doc(asOf, rootId)
			d1.Value.Set("value", "root-at-entry")
			d2, _ := tx.Doc(current, rootId)
			d2.Value.Set("value", "root-current")
			return nil
		}); err != nil {
			t.Fatalf("root commit failed: %v", err)
		}
		var parentId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(rootId, inherited, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			parentId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("parent fork failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "parent"})
			if err != nil {
				return err
			}
			parentEntry = e.Id
			d1, _ := tx.Doc(asOf, parentId)
			d1.Value.Set("value", "parent-at-own-entry")
			d2, _ := tx.Doc(current, parentId)
			d2.Value.Set("value", "parent-current")
			return nil
		}); err != nil {
			t.Fatalf("parent commit failed: %v", err)
		}
		var inheritedForkId, ownEntryForkId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, inherited, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			inheritedForkId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("inherited fork failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, asOf, inheritedForkId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "root-at-entry")) {
			t.Fatalf("inheritedFork asOf = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, current, inheritedForkId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "parent-current")) {
			t.Fatalf("inheritedFork current = %v", snap)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, parentEntry, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			ownEntryForkId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("own entry fork failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, asOf, ownEntryForkId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "parent-at-own-entry")) {
			t.Fatalf("ownEntryFork asOf = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, current, ownEntryForkId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "parent-current")) {
			t.Fatalf("ownEntryFork current = %v", snap)
		}
	})

	t.Run("copies the stored value and version without consulting migration definitions or migrated caches", func(t *testing.T) {
		v1 := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.stored-version",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(1)) },
		})
		migrations := 0
		v3 := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.stored-version",
			Version: 3,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0), "migrated", false) },
			Migrate: func(value durable.JsonObject, fromVersion int) durable.JsonObject {
				if fromVersion != 1 {
					t.Fatalf("fromVersion = %d, want 1", fromVersion)
				}
				migrations++
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c, "migrated", true)
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			if err != nil {
				return err
			}
			forkAt = e.Id
			_, err = tx.Doc(v1, parentId)
			return err
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		_ = ts.Session.UnloadDocuments()
		if snap, _ := ts.Session.Snapshot(ctx, v3, parentId); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(1), "migrated", true)) {
			t.Fatalf("parent v3 = %v", snap)
		}
		if migrations != 1 {
			t.Fatalf("migrations = %d, want 1", migrations)
		}
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("fork failed: %v", err)
		}
		if migrations != 1 {
			t.Fatalf("migrations after fork = %d, want 1", migrations)
		}
		childRec, _ := ts.Storage.FindDocument(ctx, durable.DocumentAddress{
			Kind:  v1.Definition.Kind,
			Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: childId},
		}, durable.DocumentPointCurrent())
		childStored, _ := ts.Storage.Document(ctx, childRec.Id, durable.DocumentPointCurrent())
		if childStored == nil || childStored.Version != 1 || !reflect.DeepEqual(childStored.Value, jsonx.ObjectOf("count", float64(1))) {
			t.Fatalf("childStored = %#v", childStored)
		}
		if snap, _ := ts.Session.Snapshot(ctx, v3, childId); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(1), "migrated", true)) {
			t.Fatalf("child v3 = %v", snap)
		}
		if migrations != 2 {
			t.Fatalf("migrations = %d, want 2", migrations)
		}
	})

	t.Run("coalesces a typed migration and override into the copied creation base", func(t *testing.T) {
		v1 := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.override",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(2)) },
		})
		checkpoints := 0
		v2 := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.override",
			Version: 2,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0), "migrated", false) },
			Migrate: func(value durable.JsonObject, _ int) durable.JsonObject {
				c, _ := value.Get("count")
				return jsonx.ObjectOf("count", c, "migrated", true)
			},
			CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, _ durable.CheckpointInfo) bool {
				checkpoints++
				return false
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			if err != nil {
				return err
			}
			forkAt = e.Id
			_, err = tx.Doc(v1, parentId)
			return err
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}

		docReads := ts.Storage.DocumentReadCount()
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			d, err := tx.Doc(v2, c.Id)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(9))
			childId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("fork+override failed: %v", err)
		}
		testkit.Flush()
		if ts.Storage.DocumentReadCount() != docReads+1 {
			t.Fatalf("docReads = %d, want %d", ts.Storage.DocumentReadCount(), docReads+1)
		}
		admitted := ts.Storage.AdmittedCommits()
		creates := tsfkDocCreates(admitted[len(admitted)-1])
		if len(creates) != 1 || creates[0].Content.Kind != durable.DocumentContentBase || creates[0].Content.Version != 2 ||
			!reflect.DeepEqual(creates[0].Content.Value, jsonx.ObjectOf("count", float64(9), "migrated", true)) {
			t.Fatalf("creates = %#v", creates)
		}
		if checkpoints != 0 {
			t.Fatalf("checkpoints = %d, want 0", checkpoints)
		}
		pubs := ts.Publications()
		changes := testkit.DocumentChanges(pubs[len(pubs)-1])
		if len(changes) != 1 || changes[0].Version == nil || *changes[0].Version != 2 {
			t.Fatalf("pub changes = %#v", changes)
		}
		if snap, _ := ts.Session.Snapshot(ctx, v2, childId); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(9), "migrated", true)) {
			t.Fatalf("child snap = %v", snap)
		}
		parentRec, _ := ts.Storage.FindDocument(ctx, durable.DocumentAddress{
			Kind:  v1.Definition.Kind,
			Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: parentId},
		}, durable.DocumentPointCurrent())
		parentStored, _ := ts.Storage.Document(ctx, parentRec.Id, durable.DocumentPointCurrent())
		if parentStored.Version != 1 {
			t.Fatalf("parent stored version = %d, want 1", parentStored.Version)
		}
	})

	t.Run("copies the incarnation alive at the fork point across retirement and recreation", func(t *testing.T) {
		doc := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.incarnations",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "initial") },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var oldAt, retiredAt, newAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "old"})
			oldAt = e.Id
			d, _ := tx.Doc(doc, parentId)
			d.Value.Set("value", "old")
			return nil
		}); err != nil {
			t.Fatalf("old commit failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "retired"})
			retiredAt = e.Id
			return tx.RetireDoc(doc, parentId)
		}); err != nil {
			t.Fatalf("retire commit failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "new"})
			newAt = e.Id
			d, _ := tx.Doc(doc, parentId)
			d.Value.Set("value", "new")
			return nil
		}); err != nil {
			t.Fatalf("new commit failed: %v", err)
		}

		forkAtEntry := func(at durable.EntryId) durable.ConversationId {
			var cid durable.ConversationId
			if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
				c, err := tx.ForkConversation(parentId, at, durable.ConversationCreateTxOptions{
					Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
				})
				if err != nil {
					return err
				}
				cid = c.Id
				return nil
			}); err != nil {
				t.Fatalf("fork(%d) failed: %v", at, err)
			}
			return cid
		}
		oldChild := forkAtEntry(oldAt)
		emptyChild := forkAtEntry(retiredAt)
		newChild := forkAtEntry(newAt)

		if snap, _ := ts.Session.Snapshot(ctx, doc, oldChild); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "old")) {
			t.Fatalf("oldChild = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, doc, emptyChild); snap != nil {
			t.Fatalf("emptyChild = %v, want nil", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, doc, newChild); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "new")) {
			t.Fatalf("newChild = %v", snap)
		}
	})

	t.Run("rejects invisible fork points before admission and remains usable", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		rootId, _ := testkit.CreateConversation(ts.Session)
		var visible, hidden durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e1, _ := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "visible"})
			e2, _ := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "hidden"})
			visible, hidden = e1.Id, e2.Id
			return nil
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		var parentId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(rootId, visible, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			parentId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("parent fork failed: %v", err)
		}
		testkit.Flush()
		commits := len(ts.Storage.Commits())
		published := len(ts.Publications())

		wantErr := fmt.Sprintf("Entry %d is not visible", hidden)
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.ForkConversation(parentId, hidden, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("expected %q error, got %v", wantErr, err)
		}
		testkit.Flush()
		if len(ts.Storage.Commits()) != commits || len(ts.Publications()) != published {
			t.Fatalf("unexpected commit or publication on invalid fork point")
		}
		var ind durable.ConversationRecord
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			ind = *c
			return nil
		}); err != nil {
			t.Fatalf("create independent failed: %v", err)
		}
		if got, _ := ts.Storage.Conversation(ctx, ind.Id); !reflect.DeepEqual(got, &ind) {
			t.Fatalf("independent = %v, want %v", got, ind)
		}
	})

	t.Run("rejects duplicate as-of and current selections for one child address before admission", func(t *testing.T) {
		asOf := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.duplicate-policy",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "as-of") },
		})
		current := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.duplicate-policy",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "current") },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var oldAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "old"})
			oldAt = e.Id
			_, err := tx.Doc(asOf, parentId)
			return err
		}); err != nil {
			t.Fatalf("commit 1 failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			return tx.RetireDoc(asOf, parentId)
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(current, parentId)
			return err
		}); err != nil {
			t.Fatalf("commit 3 failed: %v", err)
		}
		commits := len(ts.Storage.Commits())
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.ForkConversation(parentId, oldAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), "Fork selects multiple source documents") {
			t.Fatalf("expected duplicate policy error, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits grew on duplicate selection")
		}
		var ind durable.ConversationRecord
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			ind = *c
			return nil
		}); err != nil {
			t.Fatalf("create independent failed: %v", err)
		}
		if got, _ := ts.Storage.Conversation(ctx, ind.Id); !reflect.DeepEqual(got, &ind) {
			t.Fatalf("independent mismatch")
		}
	})

	t.Run("rejects current and as-of source writes in the fork transaction", func(t *testing.T) {
		current := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.same-transaction-current",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "committed") },
		})
		asOf := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.same-transaction-as-of",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "committed") },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			forkAt = e.Id
			if _, err := tx.Doc(current, parentId); err != nil {
				return err
			}
			_, err := tx.Doc(asOf, parentId)
			return err
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		commits := len(ts.Storage.Commits())

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(current, parentId)
			d.Value.Set("value", "before-fork")
			_, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), "Cannot change fork source document") {
			t.Fatalf("expected error changing source before fork, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if _, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			}); err != nil {
				return err
			}
			d, _ := tx.Doc(current, parentId)
			d.Value.Set("value", "after-fork")
			return nil
		}); err == nil || !strings.Contains(err.Error(), "Cannot change fork source document") {
			t.Fatalf("expected error changing source after fork, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(asOf, parentId)
			d.Value.Set("value", "as-of-write")
			_, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), "Cannot change fork source document") {
			t.Fatalf("expected error changing asOf source in fork tx, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits grew on rejected fork writes")
		}
		if snap, _ := ts.Session.Snapshot(ctx, current, parentId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "committed")) {
			t.Fatalf("parent current = %v", snap)
		}
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("clean fork failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, current, childId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "committed")) {
			t.Fatalf("child current = %v", snap)
		}
	})

	t.Run("rolls every copied base back when later pre-admission assembly fails", func(t *testing.T) {
		copiedDoc := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.rollback.copied",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "copied") },
		})
		otherDoc := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.rollback.other",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("count", float64(0)) },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			forkAt = e.Id
			if _, err := tx.Doc(copiedDoc, parentId); err != nil {
				return err
			}
			_, err := tx.Doc(otherDoc)
			return err
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		testkit.Flush()
		commits := len(ts.Storage.Commits())
		published := len(ts.Publications())
		var childId durable.ConversationId

		err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			d, err := tx.Doc(otherDoc)
			if err != nil {
				return err
			}
			d.Value.Set("count", float64(1))
			// Missing task replacement fails during batch assembly after copy preparation.
			return tx.SetTask(durable.TaskRecord{
				Id:             durable.IdFromNumber[durable.TaskId](999),
				ConversationId: parentId,
				Kind:           "missing",
				Version:        1,
				State:          durable.TaskState{Status: durable.TaskStatePending},
			})
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "Task 999 does not exist") {
			t.Fatalf("expected assembly failure, got %v", err)
		}
		testkit.Flush()
		if len(ts.Storage.Commits()) != commits || len(ts.Publications()) != published {
			t.Fatalf("expected rollback without commits or publications")
		}
		if conv, _ := ts.Storage.Conversation(ctx, childId); conv != nil {
			t.Fatalf("expected child %d rolled back, got %v", childId, conv)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, _ := tx.Doc(otherDoc)
			d.Value.Set("count", float64(2))
			return nil
		}); err != nil {
			t.Fatalf("recovery commit failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, otherDoc); !reflect.DeepEqual(snap, jsonx.ObjectOf("count", float64(2))) {
			t.Fatalf("otherDoc = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, copiedDoc, parentId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "copied")) {
			t.Fatalf("copiedDoc = %v", snap)
		}
	})

	t.Run("rolls back a guaranteed Storage rejection without poisoning the Session", func(t *testing.T) {
		doc := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.storage-rejected",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "source") },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			forkAt = e.Id
			_, err := tx.Doc(doc, parentId)
			return err
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		var rejectedChildId durable.ConversationId
		ts.Storage.FailNextCommit(durable.NewStorageRejected("copy rejected", nil))
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			rejectedChildId = c.Id
			return nil
		}); err == nil || !strings.Contains(err.Error(), "copy rejected") {
			t.Fatalf("expected StorageRejected error, got %v", err)
		}
		if got, _ := ts.Storage.Conversation(ctx, rejectedChildId); got != nil {
			t.Fatalf("expected rejected child rolled back, got %v", got)
		}
		var next durable.ConversationRecord
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			next = *c
			return nil
		}); err != nil {
			t.Fatalf("post-rejection commit failed: %v", err)
		}
		if got, _ := ts.Storage.Conversation(ctx, next.Id); !reflect.DeepEqual(got, &next) {
			t.Fatalf("next = %v, want %v", got, next)
		}
	})

	t.Run("retires a copied document and can recreate the address in the fork transaction", func(t *testing.T) {
		doc := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.retire-recreate",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "fresh") },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			forkAt = e.Id
			d, _ := tx.Doc(doc, parentId)
			d.Value.Set("value", "copied")
			return nil
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			if err := tx.RetireDoc(doc, c.Id); err != nil {
				return err
			}
			d, err := tx.Doc(doc, c.Id)
			if err != nil {
				return err
			}
			d.Value.Set("value", "replacement")
			return nil
		}); err != nil {
			t.Fatalf("fork+retire+recreate failed: %v", err)
		}
		all := ts.Storage.Commits()
		writes := all[len(all)-1]
		retires := 0
		for _, w := range writes {
			if _, ok := w.(durable.StorageWriteDocumentRetire); ok {
				retires++
			}
		}
		if len(tsfkDocCopies(writes)) != 1 || len(tsfkDocCreates(writes)) != 1 || retires != 1 {
			t.Fatalf("writes = %#v, want 1 copy, 1 create, 1 retire", writes)
		}
		if snap, _ := ts.Session.Snapshot(ctx, doc, childId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "replacement")) {
			t.Fatalf("child snap = %v", snap)
		}
	})

	t.Run("copies only conversation documents, leaving Session and task documents in their original scopes", func(t *testing.T) {
		copied := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.scope.conversation",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "conversation") },
		})
		sessionOnly := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.scope.session",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "session") },
		})
		taskOnly := tsfkMustDoc(t, durable.DocDefinition{
			Kind:    "fork.scope.task",
			Version: 1,
			Scope:   durable.DocumentScopeTask,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("value", "task") },
		})
		workTask := durable.DefineTask[any, durable.JsonObject, any, any](durable.TaskDefinition[any, durable.JsonObject, any, any]{
			Name:    "fork.scope.work",
			Version: 1,
			Initial: func(_ any) durable.JsonObject { return jsonx.ObjectOf("phase", "start") },
			Phases: map[string]durable.PhaseHandler[any, durable.JsonObject, any, any]{
				"start": func(_ chord.Context, _ durable.RunningTask[any, durable.JsonObject], _ *durable.TaskRuntime[any, durable.JsonObject, any, any]) error {
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, durable.JsonObject], _ *durable.TaskRuntime[any, durable.JsonObject, any, any]) error {
				return nil
			},
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		var taskId durable.TaskId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			forkAt = e.Id
			tid, err := tx.CreateTask(workTask, nil, durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &parentId,
			})
			if err != nil {
				return err
			}
			taskId = tid
			if _, err := tx.Doc(copied, parentId); err != nil {
				return err
			}
			if _, err := tx.Doc(sessionOnly); err != nil {
				return err
			}
			_, err = tx.Doc(taskOnly, tid)
			return err
		}); err != nil {
			t.Fatalf("setup commit failed: %v", err)
		}
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("fork failed: %v", err)
		}
		testkit.Flush()
		all := ts.Storage.Commits()
		copies := tsfkDocCopies(all[len(all)-1])
		if len(copies) != 1 || copies[0].Record.Kind != copied.Definition.Kind {
			t.Fatalf("copies = %#v", copies)
		}
		pubs := ts.Publications()
		copyChanges := testkit.DocumentCopyChanges(pubs[len(pubs)-1])
		if len(copyChanges) != 1 || copyChanges[0].Record.Kind != copied.Definition.Kind {
			t.Fatalf("copyChanges = %#v", copyChanges)
		}
		if snap, _ := ts.Session.Snapshot(ctx, copied, childId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "conversation")) {
			t.Fatalf("child copied = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, sessionOnly); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "session")) {
			t.Fatalf("sessionOnly = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, taskOnly, taskId); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", "task")) {
			t.Fatalf("taskOnly = %v", snap)
		}
	})

	t.Run("copies every family member across storage scan pages", func(t *testing.T) {
		family := tsfkMustFamily(t, durable.DocFamilyDefinition{
			Kind:    "fork.pagination",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func(seed durable.JsonValue) durable.JsonObject { return jsonx.ObjectOf("value", seed) },
		})
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		var forkAt durable.EntryId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			e, _ := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			forkAt = e.Id
			return nil
		}); err != nil {
			t.Fatalf("point commit failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			for index := 0; index < 260; index++ {
				if _, err := tx.Doc(family, parentId, fmt.Sprintf("member-%d", index), float64(index)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("260 members commit failed: %v", err)
		}
		var childId durable.ConversationId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.ForkConversation(parentId, forkAt, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			childId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("fork failed: %v", err)
		}
		all := ts.Storage.Commits()
		if len(tsfkDocCopies(all[len(all)-1])) != 260 {
			t.Fatalf("expected 260 document copies, got %d", len(tsfkDocCopies(all[len(all)-1])))
		}
		if snap, _ := ts.Session.Snapshot(ctx, family, childId, "member-0"); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", float64(0))) {
			t.Fatalf("member-0 = %v", snap)
		}
		if snap, _ := ts.Session.Snapshot(ctx, family, childId, "member-259"); !reflect.DeepEqual(snap, jsonx.ObjectOf("value", float64(259))) {
			t.Fatalf("member-259 = %v", snap)
		}
	})
}
