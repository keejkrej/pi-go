// Ported from packages/durable/test/session-definitions.test.ts (pi v1.0.0).

package durable_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tsdfInitial() durable.JsonObject {
	return jsonx.ObjectOf("value", 0)
}

func tsdfFamilyInitial(seed durable.JsonValue) durable.JsonObject {
	return jsonx.ObjectOf("value", seed)
}

func tsdfMustDoc(t *testing.T, def durable.DocDefinition) *durable.DocToken {
	t.Helper()
	tok, err := durable.DefineDoc(def)
	if err != nil {
		t.Fatalf("DefineDoc(%q) failed: %v", def.Kind, err)
	}
	return tok
}

func tsdfMustFamily(t *testing.T, def durable.DocFamilyDefinition) *durable.DocFamilyToken {
	t.Helper()
	tok, err := durable.DefineDocFamily(def)
	if err != nil {
		t.Fatalf("DefineDocFamily(%q) failed: %v", def.Kind, err)
	}
	return tok
}

func TestSessionDefinitions(t *testing.T) {
	ctx := testkit.TestContext
	sessionDoc := tsdfMustDoc(t, durable.DocDefinition{
		Kind: "t.session", Version: 1, Scope: durable.DocumentScopeSession, Initial: tsdfInitial,
	})
	latestDoc := tsdfMustDoc(t, durable.DocDefinition{
		Kind: "t.latest", Version: 1, Scope: durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest, Fork: durable.DocumentForkCurrent, Initial: tsdfInitial,
	})
	rewindableDoc := tsdfMustDoc(t, durable.DocDefinition{
		Kind: "t.rewindable", Version: 1, Scope: durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable, Fork: durable.DocumentForkAsOf, Initial: tsdfInitial,
	})
	taskDoc := tsdfMustDoc(t, durable.DocDefinition{
		Kind: "t.task", Version: 1, Scope: durable.DocumentScopeTask, Initial: tsdfInitial,
	})
	sessionFamily := tsdfMustFamily(t, durable.DocFamilyDefinition{
		Kind: "t.session-family", Version: 1, Family: true, Scope: durable.DocumentScopeSession, Initial: tsdfFamilyInitial,
	})
	conversationFamily := tsdfMustFamily(t, durable.DocFamilyDefinition{
		Kind: "t.conversation-family", Version: 1, Family: true, Scope: durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable, Fork: durable.DocumentForkInitial, Initial: tsdfFamilyInitial,
	})
	taskFamily := tsdfMustFamily(t, durable.DocFamilyDefinition{
		Kind: "t.task-family", Version: 1, Family: true, Scope: durable.DocumentScopeTask, Initial: tsdfFamilyInitial,
	})

	t.Run("validates persisted version semantics", func(t *testing.T) {
		if _, err := durable.DefineDoc(durable.DocDefinition{
			Kind: "k", Version: 0, Scope: durable.DocumentScopeSession, Initial: tsdfInitial,
		}); err == nil || !strings.Contains(err.Error(), "positive integer") {
			t.Fatalf("expected positive integer error for version 0, got %v", err)
		}
		if _, err := durable.DefineDoc(durable.DocDefinition{
			Kind: "k", Version: -1, Scope: durable.DocumentScopeSession, Initial: tsdfInitial,
		}); err == nil || !strings.Contains(err.Error(), "positive integer") {
			t.Fatalf("expected positive integer error for version -1, got %v", err)
		}
		emptyKind := tsdfMustDoc(t, durable.DocDefinition{
			Kind: "", Version: 1, Scope: durable.DocumentScopeSession, Initial: tsdfInitial,
		})
		if emptyKind.Definition.Kind != "" {
			t.Fatalf("expected empty kind preserved, got %q", emptyKind.Definition.Kind)
		}
		if sessionDoc.Definition.Kind != "t.session" {
			t.Fatalf("expected t.session, got %q", sessionDoc.Definition.Kind)
		}
	})

	t.Run("handles every owner key and seed overload", func(t *testing.T) {
		st := storage.NewMemoryStorage()
		session := durable.NewSession(st)
		defer func() { _ = session.Close(ctx) }()

		taskId := durable.IdFromNumber[durable.TaskId](1)
		if v, err := session.Snapshot(ctx, sessionDoc); err != nil || v != nil {
			t.Fatalf("expected nil snapshot for sessionDoc, got (%v, %v)", v, err)
		}
		if v, err := session.Snapshot(ctx, latestDoc, durable.RootConversationId); err != nil || v != nil {
			t.Fatalf("expected nil snapshot for latestDoc, got (%v, %v)", v, err)
		}
		if v, err := session.Snapshot(ctx, taskDoc, taskId); err != nil || v != nil {
			t.Fatalf("expected nil snapshot for taskDoc, got (%v, %v)", v, err)
		}
		if v, err := session.Snapshot(ctx, sessionFamily, "k"); err != nil || v != nil {
			t.Fatalf("expected nil snapshot for sessionFamily, got (%v, %v)", v, err)
		}
		if v, err := session.Snapshot(ctx, conversationFamily, durable.RootConversationId, "k"); err != nil || v != nil {
			t.Fatalf("expected nil snapshot for conversationFamily, got (%v, %v)", v, err)
		}
		if v, err := session.Snapshot(ctx, taskFamily, taskId, "k"); err != nil || v != nil {
			t.Fatalf("expected nil snapshot for taskFamily, got (%v, %v)", v, err)
		}

		// Snapshot never accepts a creation seed (arity error when extra arg supplied).
		if _, err := session.Snapshot(ctx, sessionFamily, "k", 1); err == nil {
			t.Fatalf("expected error when passing seed to Snapshot")
		}

		dummyTask := durable.DefineTask[jsonx.Object, jsonx.Object, jsonx.Object, jsonx.Object](durable.TaskDefinition[jsonx.Object, jsonx.Object, jsonx.Object, jsonx.Object]{
			Name:    "dummy.task",
			Version: 1,
			Initial: func(_ jsonx.Object) jsonx.Object { return jsonx.Object{} },
			Phases: map[string]durable.PhaseHandler[jsonx.Object, jsonx.Object, jsonx.Object, jsonx.Object]{
				"run": func(_ chord.Context, _ durable.RunningTask[jsonx.Object, jsonx.Object], _ *durable.TaskRuntime[jsonx.Object, jsonx.Object, jsonx.Object, jsonx.Object]) error {
					return nil
				},
			},
		})
		if err := session.Commit(ctx, func(tx durable.Tx) error {
			conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			createdTaskId, err := tx.CreateTask(dummyTask, jsonx.Object{}, durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &conv.Id,
			})
			if err != nil {
				return err
			}
			for _, call := range []func() error{
				func() error { _, e := tx.Doc(sessionDoc); return e },
				func() error { _, e := tx.Doc(latestDoc, conv.Id); return e },
				func() error { _, e := tx.Doc(rewindableDoc, conv.Id); return e },
				func() error { _, e := tx.Doc(taskDoc, createdTaskId); return e },
				func() error { _, e := tx.Doc(sessionFamily, "k", 1); return e },
				func() error { _, e := tx.Doc(conversationFamily, conv.Id, "k", 1); return e },
				func() error { _, e := tx.Doc(taskFamily, createdTaskId, "k", 1); return e },
				func() error { return tx.RetireDoc(sessionDoc) },
				func() error { return tx.RetireDoc(latestDoc, conv.Id) },
				func() error { return tx.RetireDoc(taskDoc, createdTaskId) },
				func() error { return tx.RetireDoc(sessionFamily, "k") },
				func() error { return tx.RetireDoc(conversationFamily, conv.Id, "k") },
				func() error { return tx.RetireDoc(taskFamily, createdTaskId, "k") },
			} {
				if err := call(); err != nil {
					return err
				}
			}
			// Verify arity/type rejections matching TS type constraints.
			if _, err := tx.Doc(sessionDoc, conv.Id); err == nil {
				t.Fatalf("expected error passing owner to session singleton tx.Doc")
			}
			if _, err := tx.Doc(latestDoc); err == nil {
				t.Fatalf("expected error omitting conversationId for tx.Doc")
			}
			if _, err := tx.Doc(sessionFamily, "k"); err == nil {
				t.Fatalf("expected error omitting seed for family tx.Doc")
			}
			if err := tx.RetireDoc(taskFamily, createdTaskId, "k", 1); err == nil {
				t.Fatalf("expected error passing seed to tx.RetireDoc")
			}
			return nil
		}); err != nil {
			t.Fatalf("overload commit failed: %v", err)
		}
	})

	t.Run("handles historical reads states watches and typed entries", func(t *testing.T) {
		noteEntry, err := durable.DefineEntry("t.note")
		if err != nil {
			t.Fatalf("DefineEntry note failed: %v", err)
		}
		markerEntry, err := durable.DefineEntry("t.marker")
		if err != nil {
			t.Fatalf("DefineEntry marker failed: %v", err)
		}

		st := storage.NewMemoryStorage()
		session := durable.NewSession(st)
		defer func() { _ = session.Close(ctx) }()

		var convId durable.ConversationId
		var at durable.EntryId
		if err := session.Commit(ctx, func(tx durable.Tx) error {
			conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			convId = conv.Id
			if _, err := tx.Doc(sessionDoc); err != nil {
				return err
			}
			if _, err := tx.Doc(rewindableDoc, convId); err != nil {
				return err
			}
			if _, err := tx.Doc(conversationFamily, convId, "k", 1); err != nil {
				return err
			}
			if _, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "t.raw"}); err != nil {
				return err
			}
			note, err := tx.AppendTypedEntry(noteEntry, convId, durable.EntryDraft{
				Data: jsonx.ObjectOf("text", "x"),
			})
			if err != nil {
				return err
			}
			if !noteEntry.Is(note) {
				t.Fatalf("expected noteEntry.Is(note)")
			}
			at = note.Id
			marker, err := tx.AppendTypedEntry(markerEntry, convId, durable.EntryDraft{})
			if err != nil {
				return err
			}
			if !markerEntry.Is(marker) {
				t.Fatalf("expected markerEntry.Is(marker)")
			}
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}

		if err := session.Commit(ctx, func(tx durable.Tx) error {
			gotRaw, err := tx.Entry(at)
			if err != nil || gotRaw == nil || gotRaw.Id != at {
				t.Fatalf("tx.Entry failed: %v %v", gotRaw, err)
			}
			gotTyped, err := tx.TypedEntry(noteEntry, at)
			if err != nil || gotTyped == nil || !noteEntry.Is(gotTyped) {
				t.Fatalf("tx.TypedEntry failed: %v %v", gotTyped, err)
			}
			return nil
		}); err != nil {
			t.Fatalf("read commit failed: %v", err)
		}

		if snap, err := session.SnapshotAsOf(ctx, rewindableDoc, convId, at); err != nil || snap == nil {
			t.Fatalf("SnapshotAsOf rewindableDoc failed: %v %v", snap, err)
		}
		if snap, err := session.SnapshotAsOf(ctx, conversationFamily, convId, "k", at); err != nil || snap == nil {
			t.Fatalf("SnapshotAsOf conversationFamily failed: %v %v", snap, err)
		}
		if _, err := session.SnapshotAsOf(ctx, latestDoc, convId, at); err == nil {
			t.Fatalf("expected SnapshotAsOf error for latest doc")
		}
		if _, err := session.SnapshotAsOf(ctx, sessionDoc, convId, at); err == nil {
			t.Fatalf("expected SnapshotAsOf error for session doc")
		}

		ds, err := session.DocumentState(ctx, sessionDoc)
		if err != nil || ds == nil {
			t.Fatalf("DocumentState failed: %v %v", ds, err)
		}
		ds.Dispose()

		watch, err := session.WatchDoc(ctx, rewindableDoc, convId)
		if err != nil || watch == nil {
			t.Fatalf("WatchDoc failed: %v %v", watch, err)
		}
		watch.Stop()
	})
}
