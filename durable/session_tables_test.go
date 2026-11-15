// Ported from packages/durable/test/session-tables.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tstbMustDoc(t *testing.T, def durable.DocDefinition) *durable.DocToken {
	t.Helper()
	tok, err := durable.DefineDoc(def)
	if err != nil {
		t.Fatalf("DefineDoc(%s) failed: %v", def.Kind, err)
	}
	return tok
}

func tstbMustFamily(t *testing.T, def durable.DocFamilyDefinition) *durable.DocFamilyToken {
	t.Helper()
	tok, err := durable.DefineDocFamily(def)
	if err != nil {
		t.Fatalf("DefineDocFamily(%s) failed: %v", def.Kind, err)
	}
	return tok
}

var tstbWorkTask = durable.DefineTask[durable.JsonObject, durable.JsonObject, durable.JsonObject, any](durable.TaskDefinition[durable.JsonObject, durable.JsonObject, durable.JsonObject, any]{
	Name:    "test.work",
	Version: 1,
	Initial: func(_ durable.JsonObject) durable.JsonObject { return jsonx.ObjectOf("phase", "start") },
	Phases: map[string]durable.PhaseHandler[durable.JsonObject, durable.JsonObject, durable.JsonObject, any]{
		"start": func(_ chord.Context, _ durable.RunningTask[durable.JsonObject, durable.JsonObject], _ *durable.TaskRuntime[durable.JsonObject, durable.JsonObject, durable.JsonObject, any]) error {
			return nil
		},
		"next": func(_ chord.Context, _ durable.RunningTask[durable.JsonObject, durable.JsonObject], _ *durable.TaskRuntime[durable.JsonObject, durable.JsonObject, durable.JsonObject, any]) error {
			return nil
		},
	},
	Abort: func(_ chord.Context, _ durable.RunningTask[durable.JsonObject, durable.JsonObject], _ *durable.TaskRuntime[durable.JsonObject, durable.JsonObject, durable.JsonObject, any]) error {
		return nil
	},
})

func tstbTerminal(task durable.TaskRecord) durable.TaskRecord {
	task.State = durable.TaskState{
		Status: durable.TaskStateTerminal,
		Outcome: &durable.TaskOutcome{
			Status: durable.TaskOutcomeCompleted,
			Result: jsonx.Some[durable.JsonValue](jsonx.ObjectOf("ok", true)),
		},
	}
	return task
}

func tstbCreateTask(t *testing.T, session *durable.SessionImpl, convId durable.ConversationId, progressDoc *durable.DocToken, withDocument bool) durable.TaskId {
	t.Helper()
	var taskId durable.TaskId
	if err := session.Commit(testkit.TestContext, func(tx durable.Tx) error {
		tid, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "a"), durable.TaskOptions{
			Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			ConversationId: &convId,
		})
		if err != nil {
			return err
		}
		taskId = tid
		if withDocument && progressDoc != nil {
			d, err := tx.Doc(progressDoc, tid)
			if err != nil {
				return err
			}
			d.Value.Set("lines", []any{"started"})
		}
		return nil
	}); err != nil {
		t.Fatalf("createTask failed: %v", err)
	}
	return taskId
}

func TestSessionTransactionTables(t *testing.T) {
	ctx := testkit.TestContext
	progressDoc := tstbMustDoc(t, durable.DocDefinition{
		Kind:    "test.progress",
		Version: 1,
		Scope:   durable.DocumentScopeTask,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("lines", []any{}) },
	})
	stepDoc := tstbMustFamily(t, durable.DocFamilyDefinition{
		Kind:    "test.step",
		Version: 1,
		Scope:   durable.DocumentScopeTask,
		Initial: func(_ durable.JsonValue) durable.JsonObject { return jsonx.ObjectOf("lines", []any{}) },
	})
	notesDoc := tstbMustDoc(t, durable.DocDefinition{
		Kind:    "test.notes",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
	})

	t.Run("allows table reads only before the first table write", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, _ := testkit.CreateConversation(ts.Session)
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.Conversation(convId)
			if err != nil || c == nil || c.Id != convId {
				t.Fatalf("tx.Conversation = (%v, %v)", c, err)
			}
			convs, err := tx.ScanConversations(durable.ConversationQuery{}, 1, nil)
			if err != nil || len(convs.Items) != 1 || convs.Items[0].Id != convId {
				t.Fatalf("ScanConversations = (%v, %v)", convs, err)
			}
			tasks, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &convId}, 10, nil)
			if err != nil || len(tasks.Items) != 0 {
				t.Fatalf("ScanTasks = (%v, %v)", tasks, err)
			}
			entries, err := tx.ScanEntries(durable.EntryQuery{ConversationId: convId}, 10, nil)
			if err != nil || len(entries.Items) != 0 {
				t.Fatalf("ScanEntries = (%v, %v)", entries, err)
			}
			if _, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "note"}); err != nil {
				return err
			}
			var rawErr *durable.ReadAfterWrite
			if _, err := tx.Conversation(convId); !errors.As(err, &rawErr) {
				t.Fatalf("expected ReadAfterWrite from Conversation, got %v", err)
			}
			if _, err := tx.Task(durable.IdFromNumber[durable.TaskId](1)); err == nil || !strings.Contains(err.Error(), "Tx.task() cannot read tables after the first table write") {
				t.Fatalf("expected Tx.task() ReadAfterWrite message, got %v", err)
			}
			if _, err := tx.Entry(durable.IdFromNumber[durable.EntryId](1)); !errors.As(err, &rawErr) {
				t.Fatalf("expected ReadAfterWrite from Entry, got %v", err)
			}
			if _, err := tx.ScanConversations(durable.ConversationQuery{}, 10, nil); !errors.As(err, &rawErr) {
				t.Fatalf("expected ReadAfterWrite from ScanConversations, got %v", err)
			}
			if _, err := tx.ScanEntries(durable.EntryQuery{ConversationId: convId}, 10, nil); !errors.As(err, &rawErr) {
				t.Fatalf("expected ReadAfterWrite from ScanEntries, got %v", err)
			}
			// Document access remains available after table writes.
			d, err := tx.Doc(notesDoc, convId)
			if err != nil {
				return err
			}
			d.Value.Set("text", "after write")
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, notesDoc, convId); !reflect.DeepEqual(snap, jsonx.ObjectOf("text", "after write")) {
			t.Fatalf("notes = %v", snap)
		}
	})

	t.Run("passes caller-selected limits and cursors through table scans", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		c1, _ := testkit.CreateConversation(ts.Session)
		c2, _ := testkit.CreateConversation(ts.Session)
		c3, _ := testkit.CreateConversation(ts.Session)
		ids := []durable.ConversationId{c1, c2, c3}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			first, err := tx.ScanConversations(durable.ConversationQuery{}, 2, nil)
			if err != nil {
				return err
			}
			if len(first.Items) != 2 || first.Items[0].Id != ids[0] || first.Items[1].Id != ids[1] || first.Next == nil {
				t.Fatalf("first page = %#v", first)
			}
			second, err := tx.ScanConversations(durable.ConversationQuery{}, 2, first.Next)
			if err != nil {
				return err
			}
			if len(second.Items) != 1 || second.Items[0].Id != ids[2] || second.Next != nil {
				t.Fatalf("second page = %#v", second)
			}
			return nil
		}); err != nil {
			t.Fatalf("scan commit failed: %v", err)
		}
	})

	t.Run("treats synchronous setTask as the first table write", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, _ := testkit.CreateConversation(ts.Session)
		taskId := tstbCreateTask(t, ts.Session, convId, progressDoc, false)

		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			task, err := tx.Task(taskId)
			if err != nil {
				return err
			}
			if err := tx.SetTask(*task); err != nil {
				return err
			}
			var rawErr *durable.ReadAfterWrite
			if _, err := tx.Task(taskId); !errors.As(err, &rawErr) {
				t.Fatalf("expected ReadAfterWrite after SetTask, got %v", err)
			}
			return nil
		}, nil); err != nil {
			t.Fatalf("CommitWith failed: %v", err)
		}
	})

	t.Run("creates conversations, entries, and tasks with minted IDs", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()

		var conv *durable.ConversationRecord
		var first, headed *durable.EntryRecord
		var taskId durable.TaskId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			conv = c
			e1, err := tx.AppendEntry(c.Id, durable.EntryDraft{Kind: "note", Data: "one"})
			if err != nil {
				return err
			}
			first = e1
			e2, err := tx.AppendEntry(c.Id, durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadSelf()})
			if err != nil {
				return err
			}
			headed = e2
			bg := true
			tid, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "x"), durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &c.Id,
				Background:     &bg,
			})
			if err != nil {
				return err
			}
			taskId = tid
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}

		uniq := map[int64]bool{int64(conv.Id): true, int64(first.Id): true, int64(headed.Id): true, int64(taskId): true}
		if len(uniq) != 4 {
			t.Fatalf("expected 4 distinct minted IDs, got %d", len(uniq))
		}
		if headed.Head == nil || *headed.Head != headed.Id {
			t.Fatalf("headed.Head = %v, want %d", headed.Head, headed.Id)
		}
		if first.ConversationId != conv.Id || first.Kind != "note" || first.Data != "one" {
			t.Fatalf("first entry = %#v", first)
		}
		if se, _ := ts.Storage.Entry(ctx, headed.Id); se == nil || !reflect.DeepEqual(&se.Entry, headed) {
			t.Fatalf("stored headed entry = %#v, want %#v", se, headed)
		}
		storedTask, _ := ts.Storage.Task(ctx, taskId)
		if storedTask == nil || storedTask.Id != taskId || storedTask.ConversationId != conv.Id ||
			storedTask.Kind != "test.work" || storedTask.Version != 1 ||
			!reflect.DeepEqual(storedTask.Input, jsonx.ObjectOf("path", "x")) ||
			!storedTask.Background || storedTask.AbortRequested ||
			storedTask.State.Status != durable.TaskStatePending ||
			!reflect.DeepEqual(storedTask.State.Checkpoint, jsonx.ObjectOf("phase", "start")) {
			t.Fatalf("storedTask = %#v", storedTask)
		}

		testkit.Flush()
		pubs := ts.Publications()
		changes := pubs[len(pubs)-1].Changes
		if len(changes) != 4 {
			t.Fatalf("expected 4 changes, got %d", len(changes))
		}
		types := map[string]int{}
		for _, ch := range changes {
			types[ch.ChangeType()]++
		}
		if types["conversation"] != 1 || types["entry"] != 2 || types["task"] != 1 {
			t.Fatalf("change types = %v", types)
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "x"), durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), "requires options.ConversationId") {
			t.Fatalf("expected requires options.ConversationId error, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.AppendEntry(durable.IdFromNumber[durable.ConversationId](12345), durable.EntryDraft{Kind: "note"})
			return err
		}); err == nil || !strings.Contains(err.Error(), "Conversation 12345 does not exist") {
			t.Fatalf("expected Conversation 12345 error, got %v", err)
		}
	})

	t.Run("creates a conversation with an explicitly staged task owner", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)
		bg := true
		var supervisorId durable.TaskId
		var child durable.ConversationRecord
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			tid, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "background"), durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &parentId,
				Background:     &bg,
			})
			if err != nil {
				return err
			}
			supervisorId = tid
			c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: tid},
			})
			if err != nil {
				return err
			}
			child = *c
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		if child.Owner == nil || child.Owner.ConversationId != parentId || child.Owner.TaskId != supervisorId {
			t.Fatalf("child.Owner = %#v, want (%d, %d)", child.Owner, parentId, supervisorId)
		}
		if stored, _ := ts.Storage.Conversation(ctx, child.Id); !reflect.DeepEqual(stored, &child) {
			t.Fatalf("stored child = %#v, want %#v", stored, child)
		}

		wantErr := fmt.Sprintf("Task %d cannot change conversations", supervisorId)
		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			sup, err := tx.Task(supervisorId)
			if err != nil {
				return err
			}
			moved := *sup
			moved.ConversationId = child.Id
			return tx.SetTask(moved)
		}, nil); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("expected %q, got %v", wantErr, err)
		}
		if stored, _ := ts.Storage.Task(ctx, supervisorId); stored == nil || stored.ConversationId != parentId {
			t.Fatalf("supervisor conversationId = %v, want %d", stored, parentId)
		}
	})

	t.Run("rejects missing, terminal, and abort-marked conversation owners atomically", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		parentId, _ := testkit.CreateConversation(ts.Session)

		var movedStagedTaskId durable.TaskId
		err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			tid, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "move"), durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &parentId,
			})
			if err != nil {
				return err
			}
			movedStagedTaskId = tid
			return tx.SetTask(durable.TaskRecord{
				Id:             tid,
				ConversationId: durable.IdFromNumber[durable.ConversationId](998),
				Kind:           tstbWorkTask.TaskKind(),
				Version:        tstbWorkTask.TaskVersion(),
				Input:          jsonx.ObjectOf("path", "move"),
				State: durable.TaskState{
					Status:     durable.TaskStatePending,
					Checkpoint: jsonx.ObjectOf("phase", "start"),
				},
			})
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "cannot change conversations") {
			t.Fatalf("expected cannot change conversations error, got %v", err)
		}
		if movedStagedTaskId == 0 {
			t.Fatalf("expected staged task ID")
		}
		if got, _ := ts.Storage.Task(ctx, movedStagedTaskId); got != nil {
			t.Fatalf("expected staged moved task rolled back, got %v", got)
		}

		missingTaskId := durable.IdFromNumber[durable.TaskId](999)
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: missingTaskId},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), "Conversation owner task 999 does not exist") {
			t.Fatalf("expected missing owner error, got %v", err)
		}

		var rejectedChildId durable.ConversationId
		err = ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			supId, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "aborting"), durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &parentId,
			})
			if err != nil {
				return err
			}
			child, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: supId},
			})
			if err != nil {
				return err
			}
			rejectedChildId = child.Id
			return tx.SetTask(durable.TaskRecord{
				Id:             supId,
				ConversationId: parentId,
				Kind:           tstbWorkTask.TaskKind(),
				Version:        tstbWorkTask.TaskVersion(),
				Input:          jsonx.ObjectOf("path", "aborting"),
				AbortRequested: true,
				State: durable.TaskState{
					Status:     durable.TaskStatePending,
					Checkpoint: jsonx.ObjectOf("phase", "start"),
				},
			})
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "is abort-marked") {
			t.Fatalf("expected abort-marked error, got %v", err)
		}
		if rejectedChildId == 0 {
			t.Fatalf("expected rejectedChildId")
		}
		if got, _ := ts.Storage.Conversation(ctx, rejectedChildId); got != nil {
			t.Fatalf("expected child rolled back, got %v", got)
		}

		err = ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			supId, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "terminal"), durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &parentId,
			})
			if err != nil {
				return err
			}
			if _, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: supId},
			}); err != nil {
				return err
			}
			return tx.SetTask(durable.TaskRecord{
				Id:             supId,
				ConversationId: parentId,
				Kind:           tstbWorkTask.TaskKind(),
				Version:        tstbWorkTask.TaskVersion(),
				Input:          jsonx.ObjectOf("path", "terminal"),
				State: durable.TaskState{
					Status: durable.TaskStateTerminal,
					Outcome: &durable.TaskOutcome{
						Status: durable.TaskOutcomeCompleted,
						Result: jsonx.Some[durable.JsonValue](jsonx.ObjectOf("ok", true)),
					},
				},
			})
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "is terminal") {
			t.Fatalf("expected terminal error, got %v", err)
		}

		terminalOwnerId := tstbCreateTask(t, ts.Session, parentId, progressDoc, false)
		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			task, _ := tx.Task(terminalOwnerId)
			return tx.SetTask(tstbTerminal(*task))
		}, nil); err != nil {
			t.Fatalf("terminal settle failed: %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: terminalOwnerId},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), "is terminal") {
			t.Fatalf("expected terminal owner error, got %v", err)
		}
	})

	t.Run("takes ownership of table JSON and rejects non-strict values", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, _ := testkit.CreateConversation(ts.Session)
		payload := jsonx.ObjectOf("nested", jsonx.ObjectOf("value", float64(1)))
		var entry durable.EntryRecord
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			created, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "data", Data: payload})
			if err != nil {
				return err
			}
			nested, _ := payload.Get("nested")
			nested.(*jsonx.Object).Set("value", float64(2))
			entry = *created
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		se, _ := ts.Storage.Entry(ctx, entry.Id)
		if se == nil || !reflect.DeepEqual(se.Entry.Data, jsonx.ObjectOf("nested", jsonx.ObjectOf("value", float64(1)))) {
			t.Fatalf("stored entry data = %#v", se)
		}
		var omitted durable.EntryRecord
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			created, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "omitted"})
			if err != nil {
				return err
			}
			omitted = *created
			return nil
		}); err != nil {
			t.Fatalf("omitted Commit failed: %v", err)
		}
		if omitted.Data != nil {
			t.Fatalf("expected nil omitted.Data, got %#v", omitted.Data)
		}
		se2, _ := ts.Storage.Entry(ctx, omitted.Id)
		if se2 == nil || se2.Entry.Data != nil {
			t.Fatalf("expected stored omitted Data nil, got %#v", se2)
		}

		commits := len(ts.Storage.Commits())
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.AppendEntry(convId, durable.EntryDraft{Kind: "invalid", Data: math.NaN()})
			return err
		}); err == nil || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("expected strict JSON error on NaN, got %v", err)
		}
		if len(ts.Storage.Commits()) != commits {
			t.Fatalf("commits changed on invalid table JSON")
		}
	})

	t.Run("replaces task records completely", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, _ := testkit.CreateConversation(ts.Session)
		taskId := tstbCreateTask(t, ts.Session, convId, progressDoc, false)

		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			task, err := tx.Task(taskId)
			if err != nil {
				return err
			}
			return tx.SetTask(durable.TaskRecord{
				Id:             task.Id,
				ConversationId: task.ConversationId,
				Kind:           task.Kind,
				Version:        task.Version,
				Input:          task.Input,
				Background:     task.Background,
				AbortRequested: task.AbortRequested,
				State: durable.TaskState{
					Status:     durable.TaskStateRunning,
					Checkpoint: jsonx.ObjectOf("phase", "next", "step", float64(2)),
				},
				Memos: jsonx.ObjectOf("choice", "b"),
			})
		}, nil); err != nil {
			t.Fatalf("CommitWith failed: %v", err)
		}
		stored, _ := ts.Storage.Task(ctx, taskId)
		if stored == nil || stored.State.Status != durable.TaskStateRunning ||
			!reflect.DeepEqual(stored.State.Checkpoint, jsonx.ObjectOf("phase", "next", "step", float64(2))) ||
			!reflect.DeepEqual(stored.Memos, jsonx.ObjectOf("choice", "b")) {
			t.Fatalf("stored task = %#v", stored)
		}
	})

	t.Run("creates a task and then its document in one transaction without ReadAfterWrite", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, _ := testkit.CreateConversation(ts.Session)
		var taskId durable.TaskId
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			createdId, err := tx.CreateTask(tstbWorkTask, jsonx.ObjectOf("path", "a"), durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &convId,
			})
			if err != nil {
				return err
			}
			taskId = createdId
			d1, err := tx.Doc(progressDoc, createdId)
			if err != nil {
				return err
			}
			d1.Value.Set("lines", []any{"created"})
			d2, err := tx.Doc(stepDoc, createdId, "one", nil)
			if err != nil {
				return err
			}
			d2.Value.Set("lines", []any{"step"})
			return nil
		}); err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		if snap, _ := ts.Session.Snapshot(ctx, progressDoc, taskId); !reflect.DeepEqual(snap, jsonx.ObjectOf("lines", []any{"created"})) {
			t.Fatalf("progress = %v", snap)
		}
		testkit.Flush()
		pubs := ts.Publications()
		pub := pubs[len(pubs)-1]
		docs := testkit.DocumentChanges(pub)
		hasTaskChange := false
		for _, ch := range pub.Changes {
			if st, ok := ch.(durable.StorageWriteTask); ok && st.Value.Id == taskId {
				hasTaskChange = true
			}
		}
		if !hasTaskChange || len(docs) != 2 {
			t.Fatalf("hasTaskChange=%v len(docs)=%d", hasTaskChange, len(docs))
		}
		for _, doc := range docs {
			if doc.ConversationId == nil || *doc.ConversationId != convId {
				t.Fatalf("doc.ConversationId = %v, want %d", doc.ConversationId, convId)
			}
		}

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			if _, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			}); err != nil {
				return err
			}
			d, err := tx.Doc(progressDoc, taskId)
			if err != nil {
				return err
			}
			raw, _ := d.Value.Get("lines")
			slice := append([]any(nil), raw.([]any)...)
			d.Value.Set("lines", append(slice, "committed task"))
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}
		testkit.Flush()
		pubs = ts.Publications()
		docs2 := testkit.DocumentChanges(pubs[len(pubs)-1])
		if len(docs2) != 1 || docs2[0].ConversationId == nil || *docs2[0].ConversationId != convId {
			t.Fatalf("docs2 = %#v, want conversationId %d", docs2, convId)
		}
	})

	t.Run("rejects task documents after a terminal candidate", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, _ := testkit.CreateConversation(ts.Session)
		taskId := tstbCreateTask(t, ts.Session, convId, progressDoc, true)

		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			task, err := tx.Task(taskId)
			if err != nil {
				return err
			}
			progress, err := tx.Doc(progressDoc, taskId)
			if err != nil {
				return err
			}
			if err := tx.SetTask(tstbTerminal(*task)); err != nil {
				return err
			}
			wantTerm := fmt.Sprintf("Task %d is terminal", taskId)
			if _, err := tx.Doc(progressDoc, taskId); err == nil || !strings.Contains(err.Error(), wantTerm) {
				t.Fatalf("expected %q on progressDoc, got %v", wantTerm, err)
			}
			if _, err := tx.Doc(stepDoc, taskId, "late", nil); err == nil || !strings.Contains(err.Error(), wantTerm) {
				t.Fatalf("expected %q on stepDoc, got %v", wantTerm, err)
			}
			if err := tx.SetTask(*task); err == nil || !strings.Contains(err.Error(), "terminal candidate") {
				t.Fatalf("expected terminal candidate error, got %v", err)
			}
			raw, _ := progress.Value.Get("lines")
			slice := append([]any(nil), raw.([]any)...)
			progress.Value.Set("lines", append(slice, "final"))
			return nil
		}, nil); err != nil {
			t.Fatalf("CommitWith failed: %v", err)
		}
		wantTerm := fmt.Sprintf("Task %d is terminal", taskId)
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(progressDoc, taskId)
			return err
		}); err == nil || !strings.Contains(err.Error(), wantTerm) {
			t.Fatalf("expected %q after settlement, got %v", wantTerm, err)
		}
	})

	t.Run("retires task documents at terminal settlement, including documents created in the same transaction", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		convId, _ := testkit.CreateConversation(ts.Session)
		taskId := tstbCreateTask(t, ts.Session, convId, progressDoc, true)

		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(stepDoc, taskId, "committed", nil)
			if err != nil {
				return err
			}
			d.Value.Set("lines", []any{"x"})
			return nil
		}); err != nil {
			t.Fatalf("setup step failed: %v", err)
		}
		testkit.Flush()
		published := len(ts.Publications())

		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			task, err := tx.Task(taskId)
			if err != nil {
				return err
			}
			d, err := tx.Doc(stepDoc, taskId, "new", nil)
			if err != nil {
				return err
			}
			d.Value.Set("lines", []any{"created then retired"})
			return tx.SetTask(tstbTerminal(*task))
		}, nil); err != nil {
			t.Fatalf("terminal CommitWith failed: %v", err)
		}

		all := ts.Storage.Commits()
		writes := all[len(all)-1]
		if len(writes) != 5 {
			t.Fatalf("expected 5 writes, got %d (%#v)", len(writes), writes)
		}
		taskWrites := 0
		var creates []durable.StorageWriteDocumentCreate
		var retires []durable.DocumentId
		for _, w := range writes {
			switch v := w.(type) {
			case durable.StorageWriteTask:
				taskWrites++
			case durable.StorageWriteDocumentCreate:
				creates = append(creates, v)
			case durable.StorageWriteDocumentRetire:
				retires = append(retires, v.Id)
			}
		}
		if taskWrites != 1 || len(creates) != 1 || len(retires) != 3 {
			t.Fatalf("counts = task:%d create:%d retire:%d", taskWrites, len(creates), len(retires))
		}
		createdRetired := false
		for _, id := range retires {
			if id == creates[0].Record.Id {
				createdRetired = true
				break
			}
		}
		if !createdRetired {
			t.Fatalf("expected created doc %d in retirements %v", creates[0].Record.Id, retires)
		}

		testkit.Flush()
		pubs := ts.Publications()
		if len(pubs) != published+1 {
			t.Fatalf("publications = %d, want %d", len(pubs), published+1)
		}
		pub := pubs[len(pubs)-1]
		docs := testkit.DocumentChanges(pub)
		if len(docs) != 3 {
			t.Fatalf("expected 3 doc retire changes, got %d", len(docs))
		}
		for _, d := range docs {
			if d.Value != nil || len(d.Ops) != 0 || d.ConversationId == nil || *d.ConversationId != convId {
				t.Fatalf("unexpected retire doc change: %#v", d)
			}
		}
		if s, _ := ts.Session.Snapshot(ctx, progressDoc, taskId); s != nil {
			t.Fatalf("expected nil progressDoc snapshot, got %v", s)
		}
		if s, _ := ts.Session.Snapshot(ctx, stepDoc, taskId, "committed"); s != nil {
			t.Fatalf("expected nil stepDoc snapshot, got %v", s)
		}
		alive, err := ts.Storage.ScanDocuments(ctx, durable.DocumentQuery{
			Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId},
			At:    durable.DocumentPointCurrent(),
		}, 10, nil)
		if err != nil || len(alive.Items) != 0 {
			t.Fatalf("expected 0 alive task docs, got %#v (%v)", alive, err)
		}
		wantAlready := fmt.Sprintf("Task %d is already terminal", taskId)
		if err := ts.Session.CommitWith(ctx, func(tx *durable.Transaction) error {
			task, _ := tx.Task(taskId)
			return tx.SetTask(tstbTerminal(*task))
		}, nil); err == nil || !strings.Contains(err.Error(), wantAlready) {
			t.Fatalf("expected %q, got %v", wantAlready, err)
		}
	})

	t.Run("validates document owners", func(t *testing.T) {
		ts := testkit.OpenTestSession()
		defer func() { _ = ts.Session.Close(ctx) }()
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(progressDoc, durable.IdFromNumber[durable.TaskId](4242))
			return err
		}); err == nil || !strings.Contains(err.Error(), "Task 4242 does not exist") {
			t.Fatalf("expected missing task 4242 error, got %v", err)
		}
		if err := ts.Session.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.Doc(notesDoc, durable.IdFromNumber[durable.ConversationId](4242))
			return err
		}); err == nil || !strings.Contains(err.Error(), "Conversation 4242 does not exist") {
			t.Fatalf("expected missing conv 4242 error, got %v", err)
		}
	})
}
