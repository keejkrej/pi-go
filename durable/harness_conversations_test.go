// Ported from packages/durable/test/harness-conversations.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

var thcvNoteDoc = func() *durable.DocToken {
	tok, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "test.note",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
	})
	if err != nil {
		panic(err)
	}
	return tok
}()

var thcvMessage = durable.Entry{Kind: "message"}

func thcvAppend(t *testing.T, conv durable.Conversation, text string) durable.EntryRecord {
	t.Helper()
	res, err := conv.Commit(testkit.TestContext, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(conv.Id(), durable.EntryDraft{
			Kind:  "message",
			Model: ai.MessageList{testkit.User(text)},
		})
	})
	if err != nil {
		t.Fatalf("append(%q) failed: %v", text, err)
	}
	return *res.(*durable.EntryRecord)
}

func thcvAllEntries(t *testing.T, conv durable.Conversation) []string {
	t.Helper()
	var texts []string
	var cursor durable.Cursor
	for {
		page, err := conv.Entries(durable.ConversationEntryQuery{}, 2, cursor, testkit.TestContext)
		if err != nil {
			t.Fatalf("entries() failed: %v", err)
		}
		for _, entry := range page.Items {
			u := entry.Model[0].(*ai.UserMessage)
			texts = append(texts, *u.Content.Text)
		}
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	return texts
}

func thcvToolNames(tools []*durable.ToolRegistration) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func TestHarnessConversations(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("creates the root lazily with its agent change and init in one commit", func(t *testing.T) {
		cs := testkit.NewControlledStorage()
		harness, _, err := testkit.OpenHarness(cs, []string{"read", "bash"})
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		if len(cs.Commits()) != 0 {
			t.Fatalf("expected 0 commits initially")
		}
		root, err := harness.Root(ctx, durable.RootOptions{
			Agent: &durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				agentDoc, err := tx.Doc(durable.AgentDoc, id)
				if err != nil {
					return err
				}
				if lvl, _ := agentDoc.Value.Get("thinkingLevel"); lvl != "high" {
					t.Fatalf("thinkingLevel during init = %v", lvl)
				}
				noteDoc, err := tx.Doc(thcvNoteDoc, id)
				if err != nil {
					return err
				}
				noteDoc.Value.Set("text", "root note")
				return nil
			},
		})
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		if root.Id() != durable.RootConversationId {
			t.Fatalf("root.Id = %d, want %d", root.Id(), durable.RootConversationId)
		}
		commits := cs.Commits()
		if len(commits) != 1 {
			t.Fatalf("commits len = %d, want 1", len(commits))
		}
		var writeTypes []string
		for _, w := range commits[0] {
			writeTypes = append(writeTypes, string(w.WriteType()))
		}
		// Conversation, pi.live, pi.inbox, pi.usage, pi.agent, and the init note.
		wantTypes := []string{"conversation", "document.create", "document.create", "document.create", "document.create", "document.create"}
		if !reflect.DeepEqual(writeTypes, wantTypes) {
			t.Fatalf("writeTypes = %v, want %v", writeTypes, wantTypes)
		}
		if snap, _ := harness.Snapshot(ctx, durable.LiveDoc, root.Id()); !reflect.DeepEqual(snap, jsonx.NewObject()) {
			t.Fatalf("LiveDoc = %v", snap)
		}
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, root.Id()); !reflect.DeepEqual(snap, jsonx.ObjectOf("thinkingLevel", "high")) {
			t.Fatalf("AgentDoc = %v", snap)
		}
		agent, err := root.Agent(ctx)
		if err != nil {
			t.Fatalf("root.Agent failed: %v", err)
		}
		if agent.ThinkingLevel != ai.ModelThinkingLevelHigh || !reflect.DeepEqual(thcvToolNames(agent.Tools), []string{"read", "bash"}) {
			t.Fatalf("agent = %+v", agent)
		}
		if snap, _ := harness.Snapshot(ctx, thcvNoteDoc, root.Id()); !reflect.DeepEqual(snap, jsonx.ObjectOf("text", "root note")) {
			t.Fatalf("NoteDoc = %v", snap)
		}

		again, err := harness.Root(ctx, durable.RootOptions{
			Agent: &durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelLow)},
			Init: func(_ durable.Tx, _ durable.ConversationId) error {
				t.Fatalf("init should not run on existing root")
				return nil
			},
		})
		if err != nil || again.Id() != root.Id() || len(cs.Commits()) != 1 {
			t.Fatalf("second Root = (%v, %v), commits=%d", again, err, len(cs.Commits()))
		}
	})

	t.Run("keeps root and conversation identity and state across reopen", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "session.sqlite")
		st1, err := sqlite.OpenNodeSqliteStorage(dbPath)
		if err != nil {
			t.Fatalf("OpenNodeSqliteStorage 1 failed: %v", err)
		}
		harness1, _, err := testkit.OpenHarness(st1, []string{"read"})
		if err != nil {
			t.Fatalf("OpenHarness 1 failed: %v", err)
		}
		root, err := harness1.Root(ctx)
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		if err := root.Configure(durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "anthropic", ModelId: "claude"}),
			Cwd:   jsonx.Some("/repo"),
		}, ctx); err != nil {
			t.Fatalf("Configure failed: %v", err)
		}
		entry := thcvAppend(t, root, "hello")
		child, err := harness1.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}
		fork, err := root.Fork(entry.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		if err != nil {
			t.Fatalf("Fork failed: %v", err)
		}
		if err := harness1.Close(ctx); err != nil {
			t.Fatalf("Close 1 failed: %v", err)
		}
		if _, err := root.Agent(ctx); err == nil {
			t.Fatalf("expected error after close")
		}

		// The new process installs nothing: the stored choices survive, the tools do not resolve.
		st2, err := sqlite.OpenNodeSqliteStorage(dbPath)
		if err != nil {
			t.Fatalf("OpenNodeSqliteStorage 2 failed: %v", err)
		}
		harness2, _, err := testkit.OpenHarness(st2, nil)
		if err != nil {
			t.Fatalf("OpenHarness 2 failed: %v", err)
		}
		defer func() { _ = harness2.Close(ctx) }()
		reopened, err := harness2.Root(ctx, durable.RootOptions{
			Init: func(_ durable.Tx, _ durable.ConversationId) error {
				t.Fatalf("init should not run on reopen")
				return nil
			},
		})
		if err != nil || reopened.Id() != durable.RootConversationId {
			t.Fatalf("reopened = (%v, %v)", reopened, err)
		}
		reopenedAgent, err := reopened.Agent(ctx)
		if err != nil {
			t.Fatalf("reopened.Agent failed: %v", err)
		}
		if reopenedAgent.Model == nil || *reopenedAgent.Model != (durable.ModelRef{Provider: "anthropic", ModelId: "claude"}) ||
			reopenedAgent.Cwd == nil || *reopenedAgent.Cwd != "/repo" || len(reopenedAgent.Tools) != 0 {
			t.Fatalf("reopenedAgent = %+v", reopenedAgent)
		}
		if got := thcvAllEntries(t, reopened); !reflect.DeepEqual(got, []string{"hello"}) {
			t.Fatalf("reopened entries = %v", got)
		}
		if gotChild, err := harness2.Conversation(child.Id(), ctx); err != nil || gotChild == nil || gotChild.Id() != child.Id() {
			t.Fatalf("reopened child = (%v, %v)", gotChild, err)
		}
		reopenedFork, err := harness2.Conversation(fork.Id(), ctx)
		if err != nil || reopenedFork == nil {
			t.Fatalf("reopened fork = (%v, %v)", reopenedFork, err)
		}
		if got := thcvAllEntries(t, reopenedFork); !reflect.DeepEqual(got, []string{"hello"}) {
			t.Fatalf("reopenedFork entries = %v", got)
		}
		if forkAgent, err := reopenedFork.Agent(ctx); err != nil || forkAgent.Model == nil || *forkAgent.Model != (durable.ModelRef{Provider: "anthropic", ModelId: "claude"}) {
			t.Fatalf("forkAgent = (%+v, %v)", forkAgent, err)
		}
		if missing, err := harness2.Conversation(durable.IdFromNumber[durable.ConversationId](999), ctx); err != nil || missing != nil {
			t.Fatalf("missing conversation = (%v, %v)", missing, err)
		}
	})

	t.Run("creates independent conversations atomically with init and rolls back failures", func(t *testing.T) {
		cs := testkit.NewControlledStorage()
		harness, reg, err := testkit.OpenHarness(cs, []string{"read"})
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		created, err := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				_, err := tx.AppendEntry(id, durable.EntryDraft{Kind: "message", Model: ai.MessageList{testkit.User("seed")}})
				return err
			},
		}, ctx)
		if err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}
		if len(cs.Commits()) != 1 {
			t.Fatalf("commits = %d", len(cs.Commits()))
		}
		if got := thcvAllEntries(t, created); !reflect.DeepEqual(got, []string{"seed"}) {
			t.Fatalf("entries = %v", got)
		}

		before := len(cs.Commits())
		if _, err := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			Agent:     &durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)},
			Init: func(_ durable.Tx, _ durable.ConversationId) error {
				return errors.New("init failed")
			},
		}, ctx); err == nil || !strings.Contains(err.Error(), "init failed") {
			t.Fatalf("expected init failed, got %v", err)
		}
		if len(cs.Commits()) != before {
			t.Fatalf("commits grew on rollback")
		}

		// A conversation on the default selection follows installs live.
		if _, err := testkit.AddTool(reg, testkit.Tool("bash")); err != nil {
			t.Fatalf("AddTool failed: %v", err)
		}
		agent, err := created.Agent(ctx)
		if err != nil {
			t.Fatalf("created.Agent failed: %v", err)
		}
		if got := thcvToolNames(agent.Tools); !reflect.DeepEqual(got, []string{"read", "bash"}) {
			t.Fatalf("tools = %v", got)
		}
	})

	t.Run("forks at a concrete entry with the as-of agent and applies agent and init overrides", func(t *testing.T) {
		harness, reg, err := testkit.OpenHarness(storage.NewMemoryStorage(), []string{"read"})
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		legacy := testkit.Tool("legacy")
		if _, err := testkit.AddTool(reg, legacy); err != nil {
			t.Fatalf("AddTool failed: %v", err)
		}
		root, err := harness.Root(ctx)
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		_ = root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelLow)}, ctx)
		at := thcvAppend(t, root, "one")
		_ = root.Configure(durable.AgentChange{
			ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh),
			Tools:         jsonx.Some(durable.AgentToolsSet(testkit.Tool("read"))),
		}, ctx)
		thcvAppend(t, root, "two")

		child, err := root.Fork(at.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		if err != nil {
			t.Fatalf("Fork child failed: %v", err)
		}
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, child.Id()); !reflect.DeepEqual(snap, jsonx.ObjectOf("thinkingLevel", "low")) {
			t.Fatalf("child AgentDoc = %v", snap)
		}
		if got := thcvAllEntries(t, child); !reflect.DeepEqual(got, []string{"one"}) {
			t.Fatalf("child entries = %v", got)
		}

		overridden, err := root.Fork(at.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			Agent: &durable.AgentChange{
				ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelMinimal),
				Tools:         jsonx.Some(durable.AgentToolsSet(legacy)),
			},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				d, err := tx.Doc(durable.AgentDoc, id)
				if err != nil {
					return err
				}
				if lvl, _ := d.Value.Get("thinkingLevel"); lvl != "minimal" {
					t.Fatalf("overridden thinkingLevel = %v", lvl)
				}
				return nil
			},
		}, ctx)
		if err != nil {
			t.Fatalf("Fork overridden failed: %v", err)
		}
		wantOverridden := jsonx.ObjectOf("thinkingLevel", "minimal", "tools", []any{"legacy"})
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, overridden.Id()); !reflect.DeepEqual(snap, wantOverridden) {
			t.Fatalf("overridden AgentDoc = %v", snap)
		}
		wantRoot := jsonx.ObjectOf("thinkingLevel", "high", "tools", []any{"read"})
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, root.Id()); !reflect.DeepEqual(snap, wantRoot) {
			t.Fatalf("root AgentDoc = %v", snap)
		}

		unrelated, err := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		if err != nil {
			t.Fatalf("CreateConversation unrelated failed: %v", err)
		}
		if _, err := root.Fork(at.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx); err != nil {
			t.Fatalf("second Fork from root failed: %v", err)
		}
		if _, err := unrelated.Fork(at.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx); err == nil || !strings.Contains(err.Error(), "is not visible") {
			t.Fatalf("expected is not visible error, got %v", err)
		}
	})

	t.Run("paginates fork-aware history through deep ancestor caps and same-commit prefixes", func(t *testing.T) {
		harness, _, err := testkit.OpenHarness(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		root, _ := harness.Root(ctx)
		thcvAppend(t, root, "r1")
		rawR2, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			r2, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: ai.MessageList{testkit.User("r2")}})
			if err != nil {
				return nil, err
			}
			_, err = tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: ai.MessageList{testkit.User("r3")}})
			return r2, err
		})
		if err != nil {
			t.Fatalf("append r2/r3 failed: %v", err)
		}
		r2 := *rawR2.(*durable.EntryRecord)
		child, _ := root.Fork(r2.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		c1 := thcvAppend(t, child, "c1")
		thcvAppend(t, child, "c2")
		grandchild, _ := child.Fork(c1.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		thcvAppend(t, grandchild, "g1")

		if got := thcvAllEntries(t, root); !reflect.DeepEqual(got, []string{"r3", "r2", "r1"}) {
			t.Fatalf("root entries = %v", got)
		}
		if got := thcvAllEntries(t, child); !reflect.DeepEqual(got, []string{"c2", "c1", "r2", "r1"}) {
			t.Fatalf("child entries = %v", got)
		}
		if got := thcvAllEntries(t, grandchild); !reflect.DeepEqual(got, []string{"g1", "c1", "r2", "r1"}) {
			t.Fatalf("grandchild entries = %v", got)
		}
		bounded, err := grandchild.Entries(durable.ConversationEntryQuery{
			MinEntryId: &r2.Id,
			MaxEntryId: &c1.Id,
		}, 10, nil, ctx)
		if err != nil {
			t.Fatalf("bounded Entries failed: %v", err)
		}
		var boundedIds []durable.EntryId
		for _, entry := range bounded.Items {
			boundedIds = append(boundedIds, entry.Id)
		}
		if !reflect.DeepEqual(boundedIds, []durable.EntryId{c1.Id, r2.Id}) {
			t.Fatalf("boundedIds = %v", boundedIds)
		}
		if !thcvMessage.Is(&bounded.Items[0]) || thcvMessage.Is(nil) {
			t.Fatalf("Entry.Is check failed")
		}
	})

	t.Run("binds commits and task creation to the conversation", func(t *testing.T) {
		harness, _, err := testkit.OpenHarness(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		conv, _ := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		workTask := durable.DefineTask[durable.JsonObject, durable.JsonObject, any, any](durable.TaskDefinition[durable.JsonObject, durable.JsonObject, any, any]{
			Name:    "test.work",
			Version: 1,
			Initial: func(_ durable.JsonObject) durable.JsonObject { return jsonx.ObjectOf("phase", "run") },
			Phases: map[string]durable.PhaseHandler[durable.JsonObject, durable.JsonObject, any, any]{
				"run": func(_ chord.Context, _ durable.RunningTask[durable.JsonObject, durable.JsonObject], _ *durable.TaskRuntime[durable.JsonObject, durable.JsonObject, any, any]) error {
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[durable.JsonObject, durable.JsonObject], _ *durable.TaskRuntime[durable.JsonObject, durable.JsonObject, any, any]) error {
				return nil
			},
		})
		rawTaskId, err := conv.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(workTask, jsonx.ObjectOf("n", float64(1)), durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatalf("conv.Commit CreateTask failed: %v", err)
		}
		taskId := rawTaskId.(durable.TaskId)
		var rec *durable.TaskRecord
		if err := harness.Commit(ctx, func(tx durable.Tx) error {
			var err error
			rec, err = tx.Task(taskId)
			return err
		}); err != nil {
			t.Fatalf("harness.Commit Task failed: %v", err)
		}
		if rec == nil || rec.ConversationId != conv.Id() {
			t.Fatalf("record = %+v", rec)
		}
		if err := harness.Commit(ctx, func(tx durable.Tx) error {
			_, err := tx.CreateTask(workTask, jsonx.ObjectOf("n", float64(2)), durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			return err
		}); err == nil || !strings.Contains(err.Error(), "requires options.ConversationId") {
			t.Fatalf("expected requires options.ConversationId, got %v", err)
		}
	})

	t.Run("runs conversationCreated in every creating commit, after the built-ins and before agent and init", func(t *testing.T) {
		var seen []string
		harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
			Models:   ai.CreateModels(nil),
			Registry: durable.CreateRegistry(),
			ConversationCreated: func(tx durable.Tx, conversation durable.ConversationRecord) error {
				agentDoc, err := tx.Doc(durable.AgentDoc, conversation.Id)
				if err != nil {
					return err
				}
				kind := "new"
				if conversation.Parent != nil {
					kind = "fork"
				}
				cwdStr := "undefined"
				if cwd, ok := agentDoc.Value.Get("cwd"); ok && cwd != nil {
					cwdStr, _ = cwd.(string)
				}
				seen = append(seen, string(rune('0'+conversation.Id))+":"+kind+":"+cwdStr)
				noteDoc, err := tx.Doc(thcvNoteDoc, conversation.Id)
				if err != nil {
					return err
				}
				if cur, _ := noteDoc.Value.Get("text"); cur == "" || cur == nil {
					noteDoc.Value.Set("text", "created")
				}
				if cwdStr == "/fail" {
					return errors.New("no")
				}
				return nil
			},
		}, ctx)
		if err != nil {
			t.Fatalf("HarnessOpen failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		root, err := harness.Root(ctx, durable.RootOptions{
			Agent: &durable.AgentChange{Cwd: jsonx.Some("/root")},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				d, err := tx.Doc(thcvNoteDoc, id)
				if err != nil {
					return err
				}
				txt, _ := d.Value.Get("text")
				seen = append(seen, "init:"+txt.(string))
				return nil
			},
		})
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		var rawId durable.ConversationId
		if err := harness.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			rawId = c.Id
			return nil
		}); err != nil {
			t.Fatalf("raw create failed: %v", err)
		}
		entry := thcvAppend(t, root, "hello")
		fork, err := root.Fork(entry.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		if err != nil {
			t.Fatalf("fork failed: %v", err)
		}
		wantSeen := []string{
			string(rune('0'+root.Id())) + ":new:undefined",
			"init:created",
			string(rune('0'+rawId)) + ":new:undefined",
			string(rune('0'+fork.Id())) + ":fork:/root",
		}
		if !reflect.DeepEqual(seen, wantSeen) {
			t.Fatalf("seen = %v, want %v", seen, wantSeen)
		}
		if snap, _ := harness.Snapshot(ctx, thcvNoteDoc, rawId); !reflect.DeepEqual(snap, jsonx.ObjectOf("text", "created")) {
			t.Fatalf("raw NoteDoc = %v", snap)
		}
		// A throw fails the creating commit.
		_ = root.Configure(durable.AgentChange{Cwd: jsonx.Some("/fail")}, ctx)
		if _, err := root.Fork(entry.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx); err != nil {
			t.Fatalf("fork at earlier entry before /fail failed: %v", err)
		}
		failing := thcvAppend(t, root, "after")
		if _, err := root.Fork(failing.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx); err == nil || !strings.Contains(err.Error(), "no") {
			t.Fatalf("expected error 'no', got %v", err)
		}
	})
}

func TestHarnessAgentConfiguration(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("replaces whole fields, clears them with null, and leaves undefined fields alone", func(t *testing.T) {
		harness, reg, err := testkit.OpenHarness(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		read := testkit.Tool("read")
		bash := testkit.Tool("bash")
		edit := testkit.Tool("edit")
		_ = reg.Install(durable.DefineExtension(&durable.Extension{
			Name:  "coding",
			Tools: []*durable.ToolRegistration{read, bash, edit},
		}))
		root, _ := harness.Root(ctx)
		stored := func() durable.JsonObject {
			s, err := harness.Snapshot(ctx, durable.AgentDoc, root.Id())
			if err != nil {
				t.Fatalf("Snapshot failed: %v", err)
			}
			return s
		}
		offered := func() []string {
			a, err := root.Agent(ctx)
			if err != nil {
				t.Fatalf("Agent failed: %v", err)
			}
			return thcvToolNames(a.Tools)
		}
		initialAgent, _ := root.Agent(ctx)
		if initialAgent.ThinkingLevel != ai.ModelThinkingLevelOff || !reflect.DeepEqual(initialAgent.Tools, []*durable.ToolRegistration{read, bash, edit}) || initialAgent.Model != nil {
			t.Fatalf("initialAgent = %+v", initialAgent)
		}

		_ = root.Configure(durable.AgentChange{
			Model:         jsonx.Some(durable.ModelRef{Provider: "openai", ModelId: "gpt"}),
			ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelMedium),
		}, ctx)
		_ = root.Configure(durable.AgentChange{
			Instructions: jsonx.Some("Be terse."),
		}, ctx)
		want1 := jsonx.ObjectOf(
			"model", jsonx.ObjectOf("provider", "openai", "modelId", "gpt"),
			"thinkingLevel", "medium",
			"instructions", "Be terse.",
		)
		if got := stored(); !reflect.DeepEqual(got, want1) {
			t.Fatalf("stored = %v, want %v", got, want1)
		}
		_ = root.Configure(durable.AgentChange{
			Model:        jsonx.Null[durable.ModelRef](),
			Instructions: jsonx.Null[string](),
		}, ctx)
		if got := stored(); !reflect.DeepEqual(got, jsonx.ObjectOf("thinkingLevel", "medium")) {
			t.Fatalf("stored after clear = %v", got)
		}

		_ = root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsRemove(edit))}, ctx)
		if got := offered(); !reflect.DeepEqual(got, []string{"read", "bash"}) {
			t.Fatalf("after remove edit = %v", got)
		}
		// A new filter replaces the old one: edit is offered again.
		_ = root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsRemove(bash))}, ctx)
		if got := offered(); !reflect.DeepEqual(got, []string{"read", "edit"}) {
			t.Fatalf("after remove bash = %v", got)
		}
		_ = root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsSet(edit, read))}, ctx)
		if got := offered(); !reflect.DeepEqual(got, []string{"edit", "read"}) {
			t.Fatalf("after explicit list = %v", got)
		}
		_ = root.Configure(durable.AgentChange{Tools: jsonx.Null[durable.AgentToolsChange]()}, ctx)
		if got := offered(); !reflect.DeepEqual(got, []string{"read", "bash", "edit"}) {
			t.Fatalf("after clear tools = %v", got)
		}
		// Names are stored without checking the registry.
		_ = root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsSet(testkit.Tool("missing"), read))}, ctx)
		if got := stored(); !reflect.DeepEqual(got, jsonx.ObjectOf("thinkingLevel", "medium", "tools", []any{"missing", "read"})) {
			t.Fatalf("stored with missing = %v", got)
		}
		if got := offered(); !reflect.DeepEqual(got, []string{"read"}) {
			t.Fatalf("offered with missing = %v", got)
		}
	})

	t.Run("gives conversations created through Tx their documents: empty, an owner copy, or the fork's as-of copy", func(t *testing.T) {
		harness, _, err := testkit.OpenHarness(storage.NewMemoryStorage(), []string{"read"})
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		root, _ := harness.Root(ctx, durable.RootOptions{
			Agent: &durable.AgentChange{
				Model:        jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "m"}),
				Instructions: jsonx.Some("Main role."),
				Cwd:          jsonx.Some("/repo"),
			},
		})
		ownerTask := durable.DefineTask[any, durable.JsonObject, any, any](durable.TaskDefinition[any, durable.JsonObject, any, any]{
			Name:    "test.owner",
			Version: 1,
			Initial: func(_ any) durable.JsonObject { return jsonx.ObjectOf("phase", "never") },
			Phases: map[string]durable.PhaseHandler[any, durable.JsonObject, any, any]{
				"never": func(_ chord.Context, _ durable.RunningTask[any, durable.JsonObject], _ *durable.TaskRuntime[any, durable.JsonObject, any, any]) error {
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[any, durable.JsonObject], _ *durable.TaskRuntime[any, durable.JsonObject, any, any]) error {
				return nil
			},
		})
		type createdIds struct {
			taskId durable.TaskId
			plain  durable.ConversationId
			owned  durable.ConversationId
		}
		rawIds, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			taskId, err := tx.CreateTask(ownerTask, jsonx.NewObject(), durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			if err != nil {
				return nil, err
			}
			plain, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return nil, err
			}
			owned, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: taskId},
			})
			if err != nil {
				return nil, err
			}
			// The copy exists when createConversation() returns, so a configure() in the same callback overrides it.
			d, err := tx.Doc(durable.AgentDoc, owned.Id)
			if err != nil {
				return nil, err
			}
			if cwd, _ := d.Value.Get("cwd"); cwd != "/repo" {
				t.Fatalf("owned cwd before configure = %v", cwd)
			}
			if err := durable.Configure(tx, owned.Id, durable.AgentChange{Cwd: jsonx.Some("/worktree")}); err != nil {
				return nil, err
			}
			return createdIds{taskId: taskId, plain: plain.Id, owned: owned.Id}, nil
		})
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		ids := rawIds.(createdIds)
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, ids.plain); !jsonx.DeepEqual(snap, jsonx.NewObject()) {
			t.Fatalf("plain AgentDoc = %v", snap)
		}
		if snap, _ := harness.Snapshot(ctx, durable.LiveDoc, ids.plain); !jsonx.DeepEqual(snap, jsonx.NewObject()) {
			t.Fatalf("plain LiveDoc = %v", snap)
		}
		wantOwned := jsonx.ObjectOf(
			"model", jsonx.ObjectOf("provider", "faux", "modelId", "m"),
			"instructions", "Main role.",
			"cwd", "/worktree",
		)
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, ids.owned); !reflect.DeepEqual(snap, wantOwned) {
			t.Fatalf("owned AgentDoc = %v", snap)
		}

		// A later owner change does not reach the child.
		_ = root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)}, ctx)
		ownedSnap, _ := harness.Snapshot(ctx, durable.AgentDoc, ids.owned)
		if _, has := ownedSnap.Get("thinkingLevel"); has {
			t.Fatalf("owned should not inherit later owner change: %v", ownedSnap)
		}

		// A task-owned fork keeps its as-of copy of its fork parent, not its owner's agent.
		rawAt, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			e, err := tx.AppendEntry(ids.plain, durable.EntryDraft{Kind: "note"})
			if err != nil {
				return nil, err
			}
			return e.Id, nil
		})
		if err != nil {
			t.Fatalf("append plain failed: %v", err)
		}
		at := rawAt.(durable.EntryId)
		rawFork, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			c, err := tx.ForkConversation(ids.plain, at, durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: ids.taskId},
			})
			if err != nil {
				return nil, err
			}
			return c.Id, nil
		})
		if err != nil {
			t.Fatalf("fork failed: %v", err)
		}
		forkId := rawFork.(durable.ConversationId)
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, forkId); !jsonx.DeepEqual(snap, jsonx.NewObject()) {
			t.Fatalf("fork AgentDoc = %v", snap)
		}
		if snap, _ := harness.Snapshot(ctx, durable.LiveDoc, forkId); !jsonx.DeepEqual(snap, jsonx.NewObject()) {
			t.Fatalf("fork LiveDoc = %v", snap)
		}
	})

	t.Run("reads an absent agent for conversations a plain Session created without writing", func(t *testing.T) {
		cs := testkit.NewControlledStorage()
		sess := durable.NewSession(cs)
		var id durable.ConversationId
		if err := sess.Commit(ctx, func(tx durable.Tx) error {
			c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			})
			if err != nil {
				return err
			}
			id = c.Id
			return nil
		}); err != nil {
			t.Fatalf("CreateConversation failed: %v", err)
		}

		harness, _, err := testkit.OpenHarness(cs, []string{"read"})
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		raw, err := harness.Conversation(id, ctx)
		if err != nil || raw == nil {
			t.Fatalf("Conversation = (%v, %v)", raw, err)
		}
		if snap, _ := harness.Snapshot(ctx, durable.LiveDoc, id); snap != nil {
			t.Fatalf("expected nil LiveDoc snapshot, got %v", snap)
		}
		commits := len(cs.Commits())
		agent, err := raw.Agent(ctx)
		if err != nil || agent.ThinkingLevel != ai.ModelThinkingLevelOff || !reflect.DeepEqual(thcvToolNames(agent.Tools), []string{"read"}) {
			t.Fatalf("raw.Agent = (%+v, %v)", agent, err)
		}
		if len(cs.Commits()) != commits {
			t.Fatalf("reading absent agent committed")
		}
		_ = raw.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelLow)}, ctx)
		if snap, _ := harness.Snapshot(ctx, durable.AgentDoc, id); !reflect.DeepEqual(snap, jsonx.ObjectOf("thinkingLevel", "low")) {
			t.Fatalf("configured AgentDoc = %v", snap)
		}
	})
}

func TestHarnessLifecycleSurface(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("returns stateless handles and rejects operations after close", func(t *testing.T) {
		harness, _, err := testkit.OpenHarness(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		root, err := harness.Root(ctx)
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		again, err := harness.Root(ctx)
		if err != nil {
			t.Fatalf("Root 2 failed: %v", err)
		}
		if again.Id() != root.Id() {
			t.Fatalf("Root ids differ: %d vs %d", again.Id(), root.Id())
		}
		_ = harness.Close(ctx)
		if _, err := harness.Root(ctx); err == nil || !strings.Contains(err.Error(), "Harness is closed") {
			t.Fatalf("expected Harness is closed on Root, got %v", err)
		}
		if _, err := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx); err == nil || !strings.Contains(err.Error(), "Harness is closed") {
			t.Fatalf("expected Harness is closed on CreateConversation, got %v", err)
		}
		if _, err := harness.Conversation(root.Id(), ctx); err == nil || !strings.Contains(err.Error(), "Harness is closed") {
			t.Fatalf("expected Harness is closed on Conversation, got %v", err)
		}
	})

	t.Run("forwards generic Session document APIs", func(t *testing.T) {
		harness, _, err := testkit.OpenHarness(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenHarness failed: %v", err)
		}
		defer func() { _ = harness.Close(ctx) }()
		root, _ := harness.Root(ctx)
		rawEntry, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			d, err := tx.Doc(thcvNoteDoc, root.Id())
			if err != nil {
				return nil, err
			}
			d.Value.Set("text", "first")
			return tx.AppendEntry(root.Id(), durable.EntryDraft{
				Kind:  "message",
				Model: ai.MessageList{testkit.User("m")},
			})
		})
		if err != nil {
			t.Fatalf("Commit 1 failed: %v", err)
		}
		entry := rawEntry.(*durable.EntryRecord)
		if err := harness.Commit(ctx, func(tx durable.Tx) error {
			d, err := tx.Doc(thcvNoteDoc, root.Id())
			if err != nil {
				return err
			}
			d.Value.Set("text", "second")
			return nil
		}); err != nil {
			t.Fatalf("Commit 2 failed: %v", err)
		}
		if snap, _ := harness.Snapshot(ctx, thcvNoteDoc, root.Id()); !reflect.DeepEqual(snap, jsonx.ObjectOf("text", "second")) {
			t.Fatalf("Snapshot = %v", snap)
		}
		if snap, _ := harness.SnapshotAsOf(ctx, thcvNoteDoc, root.Id(), entry.Id); !reflect.DeepEqual(snap, jsonx.ObjectOf("text", "first")) {
			t.Fatalf("SnapshotAsOf = %v", snap)
		}
		state, err := harness.DocumentState(ctx, thcvNoteDoc, root.Id())
		if err != nil || state == nil {
			t.Fatalf("DocumentState = (%v, %v)", state, err)
		}
		val, ok := state.Value()
		if !ok || !reflect.DeepEqual(val, jsonx.ObjectOf("text", "second")) {
			t.Fatalf("state.Value = (%v, %v)", val, ok)
		}
		state.Dispose()
	})
}
