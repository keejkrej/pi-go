// Ported from packages/durable/test/harness-context.test.ts (pi v1.0.0).

package durable_test

import (
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func thcxSetup(t *testing.T) (durable.Harness, durable.Conversation, func(durable.EntryDraft) durable.EntryRecord, func(ai.Message, ...string) durable.EntryRecord) {
	t.Helper()
	ctx := testkit.TestContext
	harness, _, err := testkit.OpenHarness(storage.NewMemoryStorage(), nil)
	if err != nil {
		t.Fatalf("OpenHarness failed: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close(ctx) })
	root, err := harness.Root(ctx)
	if err != nil {
		t.Fatalf("Root failed: %v", err)
	}
	appendDraft := func(draft durable.EntryDraft) durable.EntryRecord {
		res, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), draft)
		})
		if err != nil {
			t.Fatalf("AppendEntry(%s) failed: %v", draft.Kind, err)
		}
		return *res.(*durable.EntryRecord)
	}
	appendMsg := func(model ai.Message, kind ...string) durable.EntryRecord {
		k := "message"
		if len(kind) > 0 && kind[0] != "" {
			k = kind[0]
		}
		return appendDraft(durable.EntryDraft{Kind: k, Model: ai.MessageList{model}})
	}
	return harness, root, appendDraft, appendMsg
}

func thcxEntryIds(entries []durable.EntryRecord) []durable.EntryId {
	ids := make([]durable.EntryId, len(entries))
	for i, e := range entries {
		ids[i] = e.Id
	}
	return ids
}

func thcxDescribe(msgs []ai.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = testkit.DescribeMessage(m)
	}
	return out
}

func thcxPtrStop(r ai.StopReason) *ai.StopReason { return &r }

func thcxSystemMap(pairs ...string) *omap.Map[string, *string] {
	m := omap.NewMap[string, *string]()
	for i := 0; i < len(pairs); i += 2 {
		val := pairs[i+1]
		m.Set(pairs[i], &val)
	}
	return m
}

func TestHarnessConversationContext(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("returns the whole transcript without a head and excludes model-less entries from messages", func(t *testing.T) {
		_, root, appendDraft, appendMsg := thcxSetup(t)
		first := appendMsg(testkit.User("hi"))
		note := appendDraft(durable.EntryDraft{Kind: "note", Data: jsonx.ObjectOf("text", "display only")})
		answer := appendMsg(testkit.Assistant("hello"))
		view, err := root.Context(ctx)
		if err != nil {
			t.Fatalf("Context failed: %v", err)
		}
		if view.Head != nil {
			t.Fatalf("expected nil head, got %v", view.Head)
		}
		if got := thcxEntryIds(view.Entries); !reflect.DeepEqual(got, []durable.EntryId{first.Id, note.Id, answer.Id}) {
			t.Fatalf("entry ids = %v", got)
		}
		if got := thcxDescribe(view.Messages); !reflect.DeepEqual(got, []string{"user:hi", "assistant:hello"}) {
			t.Fatalf("messages = %v", got)
		}
	})

	t.Run("excludes aborted, error, and deferred assistant messages but keeps their raw entries", func(t *testing.T) {
		_, root, _, appendMsg := thcxSetup(t)
		appendMsg(testkit.User("q"))
		aborted := appendMsg(testkit.Assistant("partial", testkit.AssistantOptions{StopReason: thcxPtrStop(ai.StopReasonAborted)}))
		appendMsg(testkit.Assistant("failed", testkit.AssistantOptions{StopReason: thcxPtrStop(ai.StopReasonError)}))
		appendMsg(testkit.Assistant("later", testkit.AssistantOptions{StopReason: thcxPtrStop(ai.StopReasonDeferred)}))
		appendMsg(testkit.Assistant("done", testkit.AssistantOptions{StopReason: thcxPtrStop(ai.StopReasonLength)}))
		view, err := root.Context(ctx)
		if err != nil {
			t.Fatalf("Context failed: %v", err)
		}
		if len(view.Entries) != 5 || view.Entries[1].Id != aborted.Id {
			t.Fatalf("entries = %v", view.Entries)
		}
		if got := thcxDescribe(view.Messages); !reflect.DeepEqual(got, []string{"user:q", "assistant:done"}) {
			t.Fatalf("messages = %v", got)
		}
	})

	t.Run("resolves self heads and uses the newest head marker", func(t *testing.T) {
		_, root, appendDraft, appendMsg := thcxSetup(t)
		appendMsg(testkit.User("old"))
		reset := appendDraft(durable.EntryDraft{
			Kind:  "reset",
			Head:  durable.EntryHeadSelf(),
			Model: ai.MessageList{testkit.User("fresh start")},
		})
		if reset.Head == nil || *reset.Head != reset.Id {
			t.Fatalf("reset.Head = %v, want %d", reset.Head, reset.Id)
		}
		after := appendMsg(testkit.Assistant("after reset"))
		view, _ := root.Context(ctx)
		if view.Head == nil || view.Head.Id != reset.Id {
			t.Fatalf("view.Head = %v", view.Head)
		}
		if got := thcxEntryIds(view.Entries); !reflect.DeepEqual(got, []durable.EntryId{reset.Id, after.Id}) {
			t.Fatalf("entry ids = %v", got)
		}
		if got := thcxDescribe(view.Messages); !reflect.DeepEqual(got, []string{"user:fresh start", "assistant:after reset"}) {
			t.Fatalf("messages = %v", got)
		}

		// A compaction summary heads an earlier kept entry; older head markers in range drop out.
		summary := appendDraft(durable.EntryDraft{
			Kind:  "summary",
			Head:  durable.EntryHeadAt(after.Id),
			Model: ai.MessageList{testkit.User("summary")},
		})
		tail := appendMsg(testkit.User("next"))
		view, _ = root.Context(ctx)
		if view.Head == nil || view.Head.Id != summary.Id {
			t.Fatalf("view.Head after summary = %v", view.Head)
		}
		if got := thcxEntryIds(view.Entries); !reflect.DeepEqual(got, []durable.EntryId{summary.Id, after.Id, tail.Id}) {
			t.Fatalf("entry ids after summary = %v", got)
		}
		if got := thcxDescribe(view.Messages); !reflect.DeepEqual(got, []string{"user:summary", "assistant:after reset", "user:next"}) {
			t.Fatalf("messages after summary = %v", got)
		}
	})

	t.Run("applies the newest edit per target within the active range", func(t *testing.T) {
		_, root, appendDraft, appendMsg := thcxSetup(t)
		first := appendMsg(testkit.User("first"))
		second := appendMsg(testkit.User("second"))
		appendDraft(durable.EntryDraft{
			Kind:  "edit",
			Edits: []durable.ContextEdit{{Target: first.Id, Action: durable.ContextEditActionReplace, Messages: ai.MessageList{testkit.User("first v2")}}},
		})
		appendDraft(durable.EntryDraft{
			Kind:  "edit",
			Edits: []durable.ContextEdit{{Target: first.Id, Action: durable.ContextEditActionReplace, Messages: ai.MessageList{testkit.User("first v3")}}},
		})
		appendDraft(durable.EntryDraft{
			Kind:  "edit",
			Edits: []durable.ContextEdit{{Target: second.Id, Action: durable.ContextEditActionOmit}},
		})
		view, _ := root.Context(ctx)
		if len(view.Entries) != 5 {
			t.Fatalf("len(entries) = %d", len(view.Entries))
		}
		if got := thcxDescribe(view.Messages); !reflect.DeepEqual(got, []string{"user:first v3"}) {
			t.Fatalf("messages = %v", got)
		}

		// Edits before the active range no longer apply.
		reset := appendDraft(durable.EntryDraft{Kind: "reset", Head: durable.EntryHeadAt(second.Id)})
		view, _ = root.Context(ctx)
		if view.Head == nil || view.Head.Id != reset.Id || len(view.Messages) != 0 {
			t.Fatalf("view after reset = %+v", view)
		}
		appendDraft(durable.EntryDraft{
			Kind:  "edit",
			Edits: []durable.ContextEdit{{Target: second.Id, Action: durable.ContextEditActionReplace, Messages: ai.MessageList{testkit.User("second v2")}}},
		})
		view, _ = root.Context(ctx)
		if got := thcxDescribe(view.Messages); !reflect.DeepEqual(got, []string{"user:second v2"}) {
			t.Fatalf("messages after new edit = %v", got)
		}
	})

	t.Run("keeps positional system messages and orders tool results by call order", func(t *testing.T) {
		_, root, appendDraft, appendMsg := thcxSetup(t)
		appendMsg(testkit.System(thcxSystemMap("preamble", "You help.")), "pi.system")
		appendMsg(testkit.User("run tools"))
		appendMsg(testkit.Assistant("calling", testkit.AssistantOptions{Calls: []string{"b", "a"}}))
		appendMsg(testkit.ToolResult("a"))
		appendDraft(durable.EntryDraft{
			Kind:  "pi.system",
			Model: ai.MessageList{testkit.System(thcxSystemMap("cwd", "/repo"))},
		})
		appendMsg(testkit.ToolResult("b"))
		appendMsg(testkit.ToolResult("zz"))
		appendMsg(testkit.Assistant("done"))
		view, _ := root.Context(ctx)
		want := []string{
			"system:preamble",
			"user:run tools",
			"assistant:calling",
			"result:b:result b",
			"result:a:result a",
			"system:cwd",
			"assistant:done",
		}
		if got := thcxDescribe(view.Messages); !reflect.DeepEqual(got, want) {
			t.Fatalf("messages = %v, want %v", got, want)
		}
	})

	t.Run("synthesizes missing tool results after a fork and drops results cut from their call", func(t *testing.T) {
		_, root, _, appendMsg := thcxSetup(t)
		appendMsg(testkit.User("go"))
		call := appendMsg(testkit.Assistant("calling", testkit.AssistantOptions{Calls: []string{"x", "y"}}))
		appendMsg(testkit.ToolResult("x"))
		second := appendMsg(testkit.ToolResult("y"))
		child, err := root.Fork(call.Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, ctx)
		if err != nil {
			t.Fatalf("Fork failed: %v", err)
		}
		childView, err := child.Context(ctx)
		if err != nil {
			t.Fatalf("child.Context failed: %v", err)
		}
		wantChild := []string{"user:go", "assistant:calling", "result:x:error", "result:y:error"}
		if got := thcxDescribe(childView.Messages); !reflect.DeepEqual(got, wantChild) {
			t.Fatalf("child messages = %v", got)
		}
		missing, ok := childView.Messages[2].(*ai.ToolResultMessage)
		if !ok || missing.ToolName != "tool-x" {
			t.Fatalf("missing tool result = %#v", childView.Messages[2])
		}
		if details, ok := missing.Details.Get(); !ok || !reflect.DeepEqual(details, jsonx.ObjectOf("reason", "missing_result")) {
			t.Fatalf("missing details = (%v, %v)", details, ok)
		}

		// A head between a call and its results leaves stray results that are not sent.
		_, err = root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "reset", Head: durable.EntryHeadAt(second.Id)})
		})
		if err != nil {
			t.Fatalf("reset commit failed: %v", err)
		}
		parentView, _ := root.Context(ctx)
		if len(parentView.Messages) != 0 {
			t.Fatalf("parent messages = %v", parentView.Messages)
		}
		var kinds []string
		for _, e := range parentView.Entries {
			kinds = append(kinds, e.Kind)
		}
		if !reflect.DeepEqual(kinds, []string{"reset", "message"}) {
			t.Fatalf("parent entry kinds = %v", kinds)
		}
	})
}
