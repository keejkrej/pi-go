// Ported from packages/durable/test/harness-tools.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func thtlEchoParams() *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("text", typebox.Optional(typebox.String()))
	return typebox.Object(props)
}

func thtlTool(name string, execute durable.ToolExecuteFn, mutate ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	reg := &durable.ToolRegistration{
		Name:        name,
		Description: fmt.Sprintf("The %s tool", name),
		Parameters:  thtlEchoParams(),
		Execute:     execute,
	}
	for _, fn := range mutate {
		fn(reg)
	}
	return durable.DefineTool(reg)
}

type thtlCallSpec struct {
	name string
	args *jsonx.Object
	id   string
}

func thtlCalls(specs ...thtlCallSpec) *ai.AssistantMessage {
	blocks := make(providers.FauxAssistantBlocks, 0, len(specs))
	for _, s := range specs {
		id := s.id
		args := s.args
		if args == nil {
			args = jsonx.NewObject()
		}
		blocks = append(blocks, providers.FauxToolCall(s.name, args, &providers.FauxToolCallOptions{Id: &id}))
	}
	stop := ai.StopReasonToolUse
	return providers.FauxAssistantMessage(blocks, &providers.FauxAssistantMessageOptions{StopReason: &stop})
}

func thtlDone() *ai.AssistantMessage {
	return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)
}

func thtlNewSetup(t *testing.T, opts ...*providers.RegisterFauxProviderOptions) *testkit.ChatSetup {
	t.Helper()
	setup, err := testkit.NewChatSetup(opts...)
	if err != nil {
		t.Fatalf("NewChatSetup: %v", err)
	}
	return setup
}

func thtlRun(
	t *testing.T,
	setup *testkit.ChatSetup,
	responses []providers.FauxResponseStep,
	prepare func(h durable.Harness, root durable.Conversation),
	opts ...testkit.OpenChatOptions,
) (durable.Harness, durable.Conversation, []durable.EntryRecord, durable.SubmissionStatus) {
	t.Helper()
	setup.Faux.SetResponses(responses)
	h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup, opts...)
	if err != nil {
		t.Fatalf("OpenChat: %v", err)
	}
	if prepare != nil {
		prepare(h, root)
	}
	if err := h.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	settled, err := thtoSubmitInput(t, root, "go").Wait(nil)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	entries, err := testkit.AllEntries(root)
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	return h, root, entries, settled.Status
}

func thtlResultText(msg *ai.ToolResultMessage) string {
	if msg == nil {
		return ""
	}
	parts := make([]string, 0, len(msg.Content))
	for _, item := range msg.Content {
		switch c := item.(type) {
		case *ai.TextContent:
			parts = append(parts, c.Text)
		case *ai.ImageContent:
			parts = append(parts, "[image]")
		case *ai.ThinkingContent:
			parts = append(parts, "[thinking]")
		case *ai.ToolCall:
			parts = append(parts, "[toolCall]")
		}
	}
	return strings.Join(parts, "|")
}

func thtlScanTasks(t *testing.T, h durable.Harness, cid durable.ConversationId) []durable.TaskRecord {
	t.Helper()
	var page durable.Page[durable.TaskRecord, durable.Cursor]
	if err := h.Commit(nil, func(tx durable.Tx) error {
		var err error
		page, err = tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 50, nil)
		return err
	}); err != nil {
		t.Fatalf("ScanTasks: %v", err)
	}
	return page.Items
}

func thtlDecodeToolResultData(t *testing.T, entry durable.EntryRecord) []durable.ToolDiagnostic {
	t.Helper()
	if entry.Data == nil {
		return nil
	}
	var payload struct {
		Diagnostics []durable.ToolDiagnostic `json:"diagnostics"`
	}
	if err := jsonx.Decode(entry.Data, &payload); err != nil {
		t.Fatalf("decode tool-result data: %v", err)
	}
	return payload.Diagnostics
}

func thtlDecodeAgentState(t *testing.T, h durable.Harness, cid durable.ConversationId) durable.AgentState {
	t.Helper()
	raw, err := h.Snapshot(nil, durable.AgentDoc, cid)
	if err != nil {
		t.Fatalf("Snapshot(AgentDoc): %v", err)
	}
	var st durable.AgentState
	if raw != nil {
		var toolsVal any
		var hasTools bool
		var filtered *jsonx.Object
		if v, ok := raw.Get("tools"); ok {
			if arr, isArr := v.([]any); isArr {
				hasTools = true
				toolsVal = arr
				filtered = jsonx.NewObject()
				for _, k := range raw.Keys() {
					if k != "tools" {
						if val, exists := raw.Get(k); exists {
							filtered.Set(k, val)
						}
					}
				}
			}
		}
		target := raw
		if filtered != nil {
			target = filtered
		}
		if err := jsonx.Decode(target, &st); err != nil {
			t.Fatalf("decode AgentState: %v", err)
		}
		if hasTools {
			arr := toolsVal.([]any)
			names := make([]string, 0, len(arr))
			for _, item := range arr {
				names = append(names, fmt.Sprint(item))
			}
			st.Tools = &durable.AgentToolsState{List: names}
		}
	}
	return st
}

func thtlPtr[T any](v T) *T { return &v }

func thtlNoopAbort(_ durable.TaskRuntime[any, any, any, any], _ chord.Context) {}

func TestHarnessToolRound(t *testing.T) {
	t.Run("runs input, tool call, tool result, and answer, and settles the input", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(args any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			var txt string
			if obj, ok := args.(*jsonx.Object); ok {
				if v, ok := obj.Get("text"); ok {
					txt = fmt.Sprint(v)
				}
			}
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "echo " + txt}},
			}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, root, entries, status := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"echo", jsonx.ObjectOf("text", "hi"), "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s, want done", status)
		}
		kinds := make([]string, len(entries))
		for i, e := range entries {
			kinds[i] = e.Kind
		}
		if !slices.Equal(kinds, []string{"pi.user", "pi.system", "pi.assistant", "pi.tool-result", "pi.assistant"}) {
			t.Fatalf("kinds=%v", kinds)
		}
		sys := entries[1].Model[0].(*ai.SystemMessage)
		if len(sys.ToolsAdded) != 1 || sys.ToolsAdded[0].Name != "echo" || sys.ToolsAdded[0].Description != "The echo tool" || sys.ToolsAdded[0].Parameters == nil {
			t.Fatalf("toolsAdded=%+v", sys.ToolsAdded)
		}
		results := thtoResults(entries)
		if len(results) != 1 || results[0].ToolCallId != "c1" || results[0].ToolName != "echo" || results[0].IsError {
			t.Fatalf("results=%+v", results)
		}
		if got := thtlResultText(results[0]); got != "echo hi" {
			t.Fatalf("result text=%q, want %q", got, "echo hi")
		}
		if diags := thtlDecodeToolResultData(t, entries[3]); len(diags) != 0 {
			t.Fatalf("expected empty diagnostics, got %+v", diags)
		}
		if entries[3].ByTaskId == nil {
			t.Fatalf("expected byTaskId on tool-result entry")
		}
		if setup.Faux.State.CallCount != 2 {
			t.Fatalf("callCount=%d, want 2", setup.Faux.State.CallCount)
		}
		live := thtoLiveSnapshot(t, h, root.Id())
		if live.Run != nil || live.Generation != nil || len(live.Tools) != 0 || len(live.Compactions) != 0 {
			t.Fatalf("expected empty LiveDoc, got %+v", live)
		}
	})

	t.Run("answers calls to tools the request did not offer without a task", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, root, entries, status := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(
				thtlCallSpec{"ghost", nil, "c1"},
				thtlCallSpec{"echo", nil, "c2"},
			)},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s", status)
		}
		results := thtoResults(entries)
		if len(results) != 2 {
			t.Fatalf("len(results)=%d, want 2", len(results))
		}
		if results[0].ToolCallId != "c1" || !results[0].IsError {
			t.Fatalf("ghost=%+v", results[0])
		}
		if got := thtlResultText(results[0]); got != "<harness>\n[error] Tool ghost is not available\n</harness>" {
			t.Fatalf("ghost text=%q", got)
		}
		if results[1].ToolCallId != "c2" || results[1].IsError {
			t.Fatalf("echo=%+v", results[1])
		}
		var ghostEntry *durable.EntryRecord
		for i := range entries {
			if durable.ToolResultEntry.Is(&entries[i]) {
				ghostEntry = &entries[i]
				break
			}
		}
		if ghostEntry == nil {
			t.Fatalf("missing ghost entry")
		}
		diags := thtlDecodeToolResultData(t, *ghostEntry)
		wantCode := "tool_unavailable"
		if len(diags) != 1 || diags[0].Severity != durable.ToolDiagnosticError || diags[0].Code == nil || *diags[0].Code != wantCode || diags[0].Message != "Tool ghost is not available" {
			t.Fatalf("ghost diagnostics=%+v", diags)
		}
		items := thtlScanTasks(t, h, root.Id())
		toolCount := 0
		for _, task := range items {
			if task.Kind == durable.ToolTask.TaskKind() {
				toolCount++
			}
		}
		if toolCount != 1 {
			t.Fatalf("tool task count=%d, want 1", toolCount)
		}
	})

	t.Run("answers a call to a tool deactivated after preparation with tool_unavailable", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var seen []string
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			mu.Lock()
			seen = append(seen, "ran")
			mu.Unlock()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		var rootRef durable.Conversation
		deactivate := providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			if err := rootRef.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsSet())}, nil); err != nil {
				return nil, err
			}
			return thtlCalls(thtlCallSpec{"echo", nil, "c1"}), nil
		})
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			deactivate,
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, func(_ durable.Harness, root durable.Conversation) {
			rootRef = root
		})
		defer func() { _ = h.Close(nil) }()
		mu.Lock()
		gotSeen := slices.Clone(seen)
		mu.Unlock()
		if len(gotSeen) != 0 {
			t.Fatalf("expected seen empty, got %v", gotSeen)
		}
		results := thtoResults(entries)
		if len(results) != 1 || thtlResultText(results[0]) != "<harness>\n[error] Tool echo is not available\n</harness>" {
			t.Fatalf("results=%+v", results)
		}
		var lastSys *ai.SystemMessage
		for _, e := range entries {
			if e.Kind == "pi.system" && len(e.Model) > 0 {
				if sm, ok := e.Model[0].(*ai.SystemMessage); ok {
					lastSys = sm
				}
			}
		}
		if lastSys == nil || len(lastSys.ToolsRemoved) != 1 || lastSys.ToolsRemoved[0].Name != "echo" {
			t.Fatalf("lastSys=%+v", lastSys)
		}
	})

	t.Run("removes unregistered active tools from the offer and adds them back after re-registration", func(t *testing.T) {
		setup := thtlNewSetup(t)
		echo := thtlTool("echo", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})
		reg, err := testkit.AddTool(setup.Registry, echo)
		if err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, root, _, _ := thtlRun(t, setup, []providers.FauxResponseStep{&providers.FauxResponseMessage{Message: thtlDone()}}, nil)
		defer func() { _ = h.Close(nil) }()

		reg.Dispose()
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"echo", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		settled, err := thtoSubmitInput(t, root, "again").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("second Wait: err=%v status=%v", err, settled)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		var lastSys *ai.SystemMessage
		for _, e := range entries {
			if e.Kind == "pi.system" && len(e.Model) > 0 {
				if sm, ok := e.Model[0].(*ai.SystemMessage); ok {
					lastSys = sm
				}
			}
		}
		if lastSys == nil || len(lastSys.ToolsRemoved) != 1 || lastSys.ToolsRemoved[0].Name != "echo" {
			t.Fatalf("removal=%+v", lastSys)
		}
		if res := thtoResults(entries); len(res) != 1 || !res[0].IsError {
			t.Fatalf("expected error tool result, got %+v", res)
		}
		agent, err := root.Agent(nil)
		if err != nil {
			t.Fatalf("Agent: %v", err)
		}
		if len(agent.Tools) != 0 {
			t.Fatalf("expected 0 resolved tools, got %d", len(agent.Tools))
		}

		if _, err := testkit.AddTool(setup.Registry, echo); err != nil {
			t.Fatalf("re-register AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{&providers.FauxResponseMessage{Message: thtlDone()}})
		if _, err := thtoSubmitInput(t, root, "back").Wait(nil); err != nil {
			t.Fatalf("back Wait: %v", err)
		}
		entries, err = testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries 2: %v", err)
		}
		lastSys = nil
		for _, e := range entries {
			if e.Kind == "pi.system" && len(e.Model) > 0 {
				if sm, ok := e.Model[0].(*ai.SystemMessage); ok {
					lastSys = sm
				}
			}
		}
		if lastSys == nil || len(lastSys.ToolsAdded) != 1 || lastSys.ToolsAdded[0].Name != "echo" {
			t.Fatalf("addition=%+v", lastSys)
		}
	})

	t.Run("produces tool_unavailable when the implementation is unregistered before its task runs", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var second testkit.Installed
		if _, err := testkit.AddTool(setup.Registry, thtlTool("first", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			second.Dispose()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		}, func(r *durable.ToolRegistration) {
			r.ExecutionMode = durable.ToolExecutionModeSequential
		})); err != nil {
			t.Fatalf("AddTool first: %v", err)
		}
		var err error
		second, err = testkit.AddTool(setup.Registry, thtlTool("second", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ran"}},
			}, nil
		}))
		if err != nil {
			t.Fatalf("AddTool second: %v", err)
		}
		h, _, entries, status := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(
				thtlCallSpec{"first", nil, "c1"},
				thtlCallSpec{"second", nil, "c2"},
			)},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s", status)
		}
		results := thtoResults(entries)
		if len(results) != 2 {
			t.Fatalf("len(results)=%d", len(results))
		}
		if got := thtlResultText(results[1]); got != "<harness>\n[error] Tool second is not available\n</harness>" {
			t.Fatalf("late text=%q", got)
		}
	})

	t.Run("reads the execution mode when a round starts and keeps it for the round", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var events []string
		slow := func(name string) durable.ToolExecuteFn {
			return func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				mu.Lock()
				events = append(events, "start "+name)
				mu.Unlock()
				setup.UpdateSettings(func(s *durable.HarnessSettings) {
					s.ToolExecution = durable.ToolExecutionModeParallel
				})
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				events = append(events, "end "+name)
				mu.Unlock()
				return &durable.ToolExecutionResult{HasContent: true}, nil
			}
		}
		if _, err := testkit.AddTool(setup.Registry, thtlTool("a", slow("a"))); err != nil {
			t.Fatalf("AddTool a: %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, thtlTool("b", slow("b"))); err != nil {
			t.Fatalf("AddTool b: %v", err)
		}
		req := providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			setup.UpdateSettings(func(s *durable.HarnessSettings) {
				s.ToolExecution = durable.ToolExecutionModeSequential
			})
			return thtlCalls(thtlCallSpec{"a", nil, "c1"}, thtlCallSpec{"b", nil, "c2"}), nil
		})
		h, _, _, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			req,
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		mu.Lock()
		gotEvents := slices.Clone(events)
		mu.Unlock()
		want := []string{"start a", "end a", "start b", "end b"}
		if !slices.Equal(gotEvents, want) {
			t.Fatalf("events=%v, want %v", gotEvents, want)
		}
	})

	t.Run("runs a round in parallel by default and sequentially when configured or required by a tool", func(t *testing.T) {
		trace := func(setup *testkit.ChatSetup) []string {
			var mu sync.Mutex
			var events []string
			slow := func(name string) durable.ToolExecuteFn {
				return func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
					mu.Lock()
					events = append(events, "start "+name)
					mu.Unlock()
					time.Sleep(20 * time.Millisecond)
					mu.Lock()
					events = append(events, "end "+name)
					mu.Unlock()
					return &durable.ToolExecutionResult{HasContent: true}, nil
				}
			}
			if _, err := testkit.AddTool(setup.Registry, thtlTool("a", slow("a"))); err != nil {
				t.Fatalf("AddTool a: %v", err)
			}
			if _, err := testkit.AddTool(setup.Registry, thtlTool("b", slow("b"))); err != nil {
				t.Fatalf("AddTool b: %v", err)
			}
			h, _, _, _ := thtlRun(t, setup, []providers.FauxResponseStep{
				&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"a", nil, "c1"}, thtlCallSpec{"b", nil, "c2"})},
				&providers.FauxResponseMessage{Message: thtlDone()},
			}, nil)
			_ = h.Close(nil)
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(events)
		}

		par := trace(thtlNewSetup(t))
		if len(par) != 4 || !((par[0] == "start a" && par[1] == "start b") || (par[0] == "start b" && par[1] == "start a")) {
			t.Fatalf("expected parallel starts before ends, got %v", par)
		}

		seqSetup := thtlNewSetup(t)
		seqSetup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.ToolExecution = durable.ToolExecutionModeSequential
		})
		seq := trace(seqSetup)
		if !slices.Equal(seq, []string{"start a", "end a", "start b", "end b"}) {
			t.Fatalf("expected sequential events, got %v", seq)
		}

		perTool := thtlNewSetup(t)
		var mu sync.Mutex
		var events []string
		if _, err := testkit.AddTool(perTool.Registry, thtlTool("a", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			events = append(events, "a")
			mu.Unlock()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		}, func(r *durable.ToolRegistration) {
			r.ExecutionMode = durable.ToolExecutionModeSequential
		})); err != nil {
			t.Fatalf("AddTool perTool a: %v", err)
		}
		if _, err := testkit.AddTool(perTool.Registry, thtlTool("b", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			mu.Lock()
			events = append(events, "b")
			mu.Unlock()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool perTool b: %v", err)
		}
		h, root, _, _ := thtlRun(t, perTool, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"a", nil, "c1"}, thtlCallSpec{"b", nil, "c2"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		items := thtlScanTasks(t, h, root.Id())
		slices.SortFunc(items, func(a, b durable.TaskRecord) int {
			return int(a.Id - b.Id)
		})
		var genTask *durable.TaskRecord
		var toolTasks []durable.TaskRecord
		for _, task := range items {
			if task.Kind == durable.GenerationTask.TaskKind() {
				if genTask == nil {
					copyTask := task
					genTask = &copyTask
				}
			} else if task.Kind == durable.ToolTask.TaskKind() {
				toolTasks = append(toolTasks, task)
			}
		}
		if genTask == nil || len(toolTasks) != 2 {
			t.Fatalf("gen=%v len(tools)=%d", genTask, len(toolTasks))
		}
		for _, tt := range toolTasks {
			var ownerStr string
			if tt.Owner == nil {
				ownerStr = "<nil>"
			} else {
				ownerStr = fmt.Sprintf("%d", *tt.Owner)
			}
			if tt.Owner == nil || *tt.Owner != genTask.Id {
				t.Fatalf("tool task owner=%s, want %d", ownerStr, genTask.Id)
			}
		}
		mu.Lock()
		gotEvents := slices.Clone(events)
		mu.Unlock()
		if !slices.Equal(gotEvents, []string{"a", "b"}) {
			t.Fatalf("events=%v, want [a b]", gotEvents)
		}
	})
}

func TestHarnessToolResults(t *testing.T) {
	t.Run("uses retained output and the last details when the result omits them, with diagnostics in order", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("log", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			api.Output("line 1\n")
			api.OutputBytes([]byte("line 2\nline 3\n"))
			api.Diagnostic(durable.ToolDiagnostic{Severity: durable.ToolDiagnosticInfo, Message: "from api"})
			if err := api.Details(jsonx.ObjectOf("step", 1), callCtx); err != nil {
				return nil, err
			}
			if err := api.Details(jsonx.ObjectOf("step", 2), callCtx); err != nil {
				return nil, err
			}
			return &durable.ToolExecutionResult{
				Diagnostics: []durable.ToolDiagnostic{{Severity: durable.ToolDiagnosticWarn, Message: "from result"}},
			}, nil
		}, func(r *durable.ToolRegistration) {
			r.OutputLimits = &durable.ToolOutputLimits{MaxLines: thtlPtr(2)}
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"log", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		results := thtoResults(entries)
		if len(results) != 1 {
			t.Fatalf("len(results)=%d", len(results))
		}
		detVal, ok := results[0].Details.Get()
		if !ok {
			t.Fatalf("expected details")
		}
		detObj, ok := detVal.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected object details, got %T", detVal)
		}
		if step, _ := detObj.Get("step"); fmt.Sprint(step) != "2" {
			t.Fatalf("step=%v, want 2", step)
		}
		want := "line 1\nline 2\n|<harness>\n[info] from api\n[warn] from result\n[warn] Output truncated to its beginning: 1 lines, 7 bytes dropped\n</harness>"
		if got := thtlResultText(results[0]); got != want {
			t.Fatalf("text=%q, want %q", got, want)
		}
		var resultEntry *durable.EntryRecord
		for i := range entries {
			if durable.ToolResultEntry.Is(&entries[i]) {
				resultEntry = &entries[i]
				break
			}
		}
		if resultEntry == nil {
			t.Fatalf("missing ToolResultEntry")
		}
		diags := thtlDecodeToolResultData(t, *resultEntry)
		var codes []string
		for _, d := range diags {
			if d.Code == nil {
				codes = append(codes, "")
			} else {
				codes = append(codes, *d.Code)
			}
		}
		if !slices.Equal(codes, []string{"", "", "truncated"}) {
			t.Fatalf("codes=%v", codes)
		}
	})

	t.Run("bounds explicit text content and keeps other content", func(t *testing.T) {
		setup := thtlNewSetup(t)
		image := &ai.ImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"}
		if _, err := testkit.AddTool(setup.Registry, thtlTool("big", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content: ai.ContentList{
					&ai.TextContent{Type: "text", Text: "a\nb\n"},
					image,
					&ai.TextContent{Type: "text", Text: "c\nd\n"},
				},
			}, nil
		}, func(r *durable.ToolRegistration) {
			r.OutputLimits = &durable.ToolOutputLimits{
				MaxLines: thtlPtr(2),
				Retain:   durable.ToolOutputRetainTail,
			}
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"big", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		want := "[image]|c\nd\n|<harness>\n[warn] Output truncated to its end: 2 lines, 4 bytes dropped\n</harness>"
		if got := thtlResultText(thtoResults(entries)[0]); got != want {
			t.Fatalf("text=%q, want %q", got, want)
		}
	})

	t.Run("turns a throw into a tool_error result with the partial output", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("fail", func(_ any, api durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			api.Output("partial\n")
			return nil, errors.New("boom")
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, status := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"fail", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s", status)
		}
		res := thtoResults(entries)[0]
		if !res.IsError || thtlResultText(res) != "partial\n|<harness>\n[error] boom\n</harness>" {
			t.Fatalf("unexpected res=%+v text=%q", res, thtlResultText(res))
		}
	})

	t.Run("validates arguments before and after beforeTool and applies blocks and replacements", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var seen []string
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(args any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			var txt string
			if obj, ok := args.(*jsonx.Object); ok {
				if v, ok := obj.Get("text"); ok {
					txt = fmt.Sprint(v)
				}
			}
			mu.Lock()
			seen = append(seen, txt)
			mu.Unlock()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			BeforeTool: func(call *ai.ToolCall, _ durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
				if call.Id == "block" {
					return &durable.BeforeToolDecision{Block: thtlPtr("not today")}, nil
				}
				if call.Id == "throw" {
					return nil, errors.New("hook failed")
				}
				if call.Id == "bad" {
					return &durable.BeforeToolDecision{
						Arguments: jsonx.ObjectOf("text", jsonx.ObjectOf("not", "a string")),
					}, nil
				}
				txt, _ := call.Arguments.Get("text")
				return &durable.BeforeToolDecision{
					Arguments: jsonx.ObjectOf("text", fmt.Sprintf("%v!", txt)),
				}, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(
				thtlCallSpec{"echo", jsonx.ObjectOf("text", 1), "coerced"},
				thtlCallSpec{"echo", jsonx.ObjectOf("text", jsonx.ObjectOf("not", "a string")), "invalid"},
				thtlCallSpec{"echo", nil, "block"},
				thtlCallSpec{"echo", nil, "throw"},
				thtlCallSpec{"echo", jsonx.ObjectOf("text", "x"), "bad"},
				thtlCallSpec{"echo", jsonx.ObjectOf("text", "x"), "ok"},
			)},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()

		byId := map[string]*ai.ToolResultMessage{}
		for _, res := range thtoResults(entries) {
			byId[res.ToolCallId] = res
		}
		if !byId["block"].IsError || thtlResultText(byId["block"]) != "<harness>\n[error] Tool call blocked: not today\n</harness>" {
			t.Fatalf("block=%+v (%q)", byId["block"], thtlResultText(byId["block"]))
		}
		if !byId["throw"].IsError || thtlResultText(byId["throw"]) != "<harness>\n[error] Tool call blocked: hook failed\n</harness>" {
			t.Fatalf("throw=%+v (%q)", byId["throw"], thtlResultText(byId["throw"]))
		}
		if !byId["bad"].IsError || !strings.Contains(thtlResultText(byId["bad"]), "Validation failed") {
			t.Fatalf("bad=%+v (%q)", byId["bad"], thtlResultText(byId["bad"]))
		}
		if byId["ok"].IsError || thtlResultText(byId["ok"]) != "" {
			t.Fatalf("ok=%+v (%q)", byId["ok"], thtlResultText(byId["ok"]))
		}
		if !byId["invalid"].IsError || !strings.Contains(thtlResultText(byId["invalid"]), "Validation failed") {
			t.Fatalf("invalid=%+v (%q)", byId["invalid"], thtlResultText(byId["invalid"]))
		}
		if byId["coerced"].IsError || thtlResultText(byId["coerced"]) != "" {
			t.Fatalf("coerced=%+v (%q)", byId["coerced"], thtlResultText(byId["coerced"]))
		}
		mu.Lock()
		gotSeen := slices.Clone(seen)
		mu.Unlock()
		slices.Sort(gotSeen)
		if !slices.Equal(gotSeen, []string{"1!", "x!"}) {
			t.Fatalf("seen=%v, want [1! x!]", gotSeen)
		}
	})

	t.Run("repairs arguments with prepareArguments before validation, and a throwing repair is invalid", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var seen []string
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(args any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			var txt string
			if obj, ok := args.(*jsonx.Object); ok {
				if v, ok := obj.Get("text"); ok {
					txt = fmt.Sprint(v)
				}
			}
			mu.Lock()
			seen = append(seen, txt)
			mu.Unlock()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		}, func(r *durable.ToolRegistration) {
			r.PrepareArguments = func(args any) any {
				obj, ok := args.(*jsonx.Object)
				if !ok || obj == nil {
					return args
				}
				v, _ := obj.Get("text")
				if s, ok := v.(string); ok && s == "throw" {
					panic("cannot repair")
				}
				switch v.(type) {
				case float64, int, int64:
					return jsonx.ObjectOf("text", fmt.Sprintf("#%v", v))
				default:
					return args
				}
			}
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(
				thtlCallSpec{"echo", jsonx.ObjectOf("text", 7), "fixed"},
				thtlCallSpec{"echo", jsonx.ObjectOf("text", "throw"), "broken"},
			)},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()

		byId := map[string]string{}
		for _, r := range thtoResults(entries) {
			byId[r.ToolCallId] = thtlResultText(r)
		}
		if byId["fixed"] != "" {
			t.Fatalf("fixed=%q, want empty", byId["fixed"])
		}
		if byId["broken"] != "<harness>\n[error] cannot repair\n</harness>" {
			t.Fatalf("broken=%q", byId["broken"])
		}
		mu.Lock()
		gotSeen := slices.Clone(seen)
		mu.Unlock()
		if !slices.Equal(gotSeen, []string{"#7"}) {
			t.Fatalf("seen=%v, want [#7]", gotSeen)
		}
		asst := entries[2].Model[0].(*ai.AssistantMessage)
		var firstCall *ai.ToolCall
		for _, c := range asst.Content {
			if tc, ok := c.(*ai.ToolCall); ok {
				firstCall = tc
				break
			}
		}
		if firstCall == nil {
			t.Fatalf("expected toolCall")
		}
		if rawText, _ := firstCall.Arguments.Get("text"); fmt.Sprint(rawText) != "7" {
			t.Fatalf("stored call text=%v, want 7", rawText)
		}
	})

	t.Run("lets the first beforeTool block win and skips later handlers", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		var mu sync.Mutex
		var asked []string
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			BeforeTool: func(_ *ai.ToolCall, _ durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
				mu.Lock()
				asked = append(asked, "first")
				mu.Unlock()
				return &durable.BeforeToolDecision{Block: thtlPtr("first says no")}, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 1: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			BeforeTool: func(_ *ai.ToolCall, _ durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
				mu.Lock()
				asked = append(asked, "second")
				mu.Unlock()
				return &durable.BeforeToolDecision{Block: thtlPtr("second says no")}, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 2: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"echo", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		mu.Lock()
		gotAsked := slices.Clone(asked)
		mu.Unlock()
		if !slices.Equal(gotAsked, []string{"first"}) {
			t.Fatalf("asked=%v, want [first]", gotAsked)
		}
		if got := thtlResultText(thtoResults(entries)[0]); got != "<harness>\n[error] Tool call blocked: first says no\n</harness>" {
			t.Fatalf("text=%q", got)
		}
	})

	t.Run("chains afterTool replacements and observes the round with afterTools", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "raw"}},
			}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			AfterTool: func(_ *ai.ToolCall, result *durable.ToolExecutionResult, _ durable.HookApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				copyRes := *result
				copyRes.HasContent = true
				copyRes.Content = ai.ContentList{&ai.TextContent{Type: "text", Text: "first"}}
				return &copyRes, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 1: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			AfterTool: func(_ *ai.ToolCall, result *durable.ToolExecutionResult, _ durable.HookApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				copyRes := *result
				var txt string
				for _, c := range result.Content {
					if tc, ok := c.(*ai.TextContent); ok {
						txt = tc.Text
						break
					}
				}
				copyRes.HasDetails = true
				copyRes.Details = jsonx.ObjectOf("replaced", txt)
				return &copyRes, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 2: %v", err)
		}
		var mu sync.Mutex
		var obsAsst durable.EntryId
		var obsResults []durable.EntryId
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterTools: func(assistant durable.EntryId, results []durable.EntryId, _ durable.HookApi, _ chord.Context) error {
				mu.Lock()
				obsAsst = assistant
				obsResults = slices.Clone(results)
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("AddHooks gen: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"echo", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		res := thtoResults(entries)[0]
		if thtlResultText(res) != "first" {
			t.Fatalf("text=%q, want first", thtlResultText(res))
		}
		det, ok := res.Details.Get()
		if !ok {
			t.Fatalf("expected details")
		}
		if obj, ok := det.(*jsonx.Object); !ok {
			t.Fatalf("expected *jsonx.Object details, got %T", det)
		} else if rep, _ := obj.Get("replaced"); rep != "first" {
			t.Fatalf("replaced=%v, want first", rep)
		}
		var resultEntry durable.EntryRecord
		for _, e := range entries {
			if durable.ToolResultEntry.Is(&e) {
				resultEntry = e
				break
			}
		}
		mu.Lock()
		gotAsst, gotRes := obsAsst, slices.Clone(obsResults)
		mu.Unlock()
		if gotAsst != entries[2].Id || !slices.Equal(gotRes, []durable.EntryId{resultEntry.Id}) {
			t.Fatalf("observed=(%d, %v), want=(%d, [%d])", gotAsst, gotRes, entries[2].Id, resultEntry.Id)
		}
	})

	t.Run("runs the hooks of the selected extensions; a task-owned child copies its owner's selection", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var calledIn []durable.ConversationId
		echoExt := durable.DefineExtension(&durable.Extension{
			Name: "echo",
			Tools: []*durable.ToolRegistration{thtlTool("echo", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				return &durable.ToolExecutionResult{HasContent: true}, nil
			})},
		})
		auditExt := durable.DefineExtension(&durable.Extension{
			Name: "audit",
			Hooks: []durable.HookRegistration{durable.Hook(durable.ToolTask, durable.ToolHooks{
				BeforeTool: func(_ *ai.ToolCall, api durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
					mu.Lock()
					calledIn = append(calledIn, api.ConversationId())
					mu.Unlock()
					return nil, nil
				},
			})},
		})
		if err := setup.Registry.Install(echoExt); err != nil {
			t.Fatalf("Install echo: %v", err)
		}
		if err := setup.Registry.Install(auditExt); err != nil {
			t.Fatalf("Install audit: %v", err)
		}
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Extensions = []*durable.Extension{echoExt}
			s.HasExtensions = true
		})

		h, root, _, _ := thtlRun(t, setup, []providers.FauxResponseStep{&providers.FauxResponseMessage{Message: thtlDone()}}, nil)
		defer func() { _ = h.Close(nil) }()
		if err := root.Configure(durable.AgentChange{
			Extensions: jsonx.Some(durable.AgentExtensionsEdit([]*durable.Extension{auditExt}, nil)),
		}, nil); err != nil {
			t.Fatalf("Configure audit: %v", err)
		}
		ownerTask := durable.DefineTask(durable.TaskDefinition[struct{}, struct {
			Phase string `json:"phase"`
		}, any, struct{}]{
			Name:    "test.owner",
			Version: 1,
			Initial: func(_ struct{}) struct {
				Phase string `json:"phase"`
			} {
				return struct {
					Phase string `json:"phase"`
				}{Phase: "never"}
			},
			Phases: map[string]durable.PhaseHandler[struct{}, struct {
				Phase string `json:"phase"`
			}, any, struct{}]{
				"never": func(_ chord.Context, _ durable.RunningTask[struct{}, struct {
					Phase string `json:"phase"`
				}], _ *durable.TaskRuntime[struct{}, struct {
					Phase string `json:"phase"`
				}, any, struct{}]) error {
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[struct{}, struct {
				Phase string `json:"phase"`
			}], _ *durable.TaskRuntime[struct{}, struct {
				Phase string `json:"phase"`
			}, any, struct{}]) error {
				return nil
			},
		})
		var childId durable.ConversationId
		if err := h.Commit(nil, func(tx durable.Tx) error {
			rootCid := root.Id()
			tid, err := tx.CreateTask(ownerTask, struct{}{}, durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &rootCid,
			})
			if err != nil {
				return err
			}
			rec, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
				Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: tid},
			})
			if err != nil {
				return err
			}
			childId = rec.Id
			return nil
		}); err != nil {
			t.Fatalf("Commit child: %v", err)
		}
		child, err := h.Conversation(childId, nil)
		if err != nil || child == nil {
			t.Fatalf("Conversation(child): %v", err)
		}
		other, err := h.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			Agent:     &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
		}, nil)
		if err != nil {
			t.Fatalf("CreateConversation other: %v", err)
		}
		for _, conv := range []durable.Conversation{root, child, other} {
			setup.Faux.SetResponses([]providers.FauxResponseStep{
				&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"echo", nil, "c1"})},
				&providers.FauxResponseMessage{Message: thtlDone()},
			})
			if _, err := thtoSubmitInput(t, conv, "go").Wait(nil); err != nil {
				t.Fatalf("conv %d Wait: %v", conv.Id(), err)
			}
		}
		mu.Lock()
		gotCalledIn := slices.Clone(calledIn)
		mu.Unlock()
		if !slices.Equal(gotCalledIn, []durable.ConversationId{root.Id(), child.Id()}) {
			t.Fatalf("calledIn=%v, want [%d %d]", gotCalledIn, root.Id(), child.Id())
		}
	})

	t.Run("applies addTools and terminates only when every result of the round asks to", func(t *testing.T) {
		setup := thtlNewSetup(t)
		stop := thtlTool("stop", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true, Control: &durable.ToolControl{Terminate: true}}, nil
		})
		grow := thtlTool("grow", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true, Control: &durable.ToolControl{AddTools: []string{"extra", "stop"}}}, nil
		})
		extra := thtlTool("extra", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})
		for _, tl := range []*durable.ToolRegistration{stop, grow, extra} {
			if _, err := testkit.AddTool(setup.Registry, tl); err != nil {
				t.Fatalf("AddTool %s: %v", tl.Name, err)
			}
		}
		h, root, entries, status := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"stop", nil, "c1"})},
		}, func(_ durable.Harness, root durable.Conversation) {
			if err := root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsSet(stop, grow))}, nil); err != nil {
				t.Fatalf("Configure: %v", err)
			}
		})
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s", status)
		}
		if entries[len(entries)-1].Kind != "pi.tool-result" {
			t.Fatalf("last entry=%s, want pi.tool-result", entries[len(entries)-1].Kind)
		}
		items := thtlScanTasks(t, h, root.Id())
		for _, task := range items {
			if task.State.Status != durable.TaskStateTerminal {
				t.Fatalf("expected all tasks terminal, got %+v", task)
			}
		}

		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"stop", nil, "c1"}, thtlCallSpec{"grow", nil, "c2"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		second, err := thtoSubmitInput(t, root, "again").Wait(nil)
		if err != nil || second.Status != durable.SubmissionStatusDone {
			t.Fatalf("second Wait: err=%v status=%v", err, second)
		}
		entries, err = testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries 2: %v", err)
		}
		if entries[len(entries)-1].Kind != "pi.assistant" {
			t.Fatalf("last entry=%s, want pi.assistant", entries[len(entries)-1].Kind)
		}
		st := thtlDecodeAgentState(t, h, root.Id())
		if st.Tools == nil || !slices.Equal(st.Tools.List, []string{"stop", "grow", "extra"}) {
			t.Fatalf("agent tools=%+v", st.Tools)
		}
	})
}

func TestHarnessGenerationHooks(t *testing.T) {
	t.Run("replaces request messages, observes responses, and continues on yield", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var requests [][]string
		record := providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			var msgs []string
			for _, m := range transcript.Messages {
				txt, _ := testkit.TextOf(m)
				msgs = append(msgs, m.GetRole()+":"+txt)
			}
			mu.Lock()
			requests = append(requests, msgs)
			n := len(requests)
			mu.Unlock()
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(fmt.Sprintf("answer %d", n))}, nil), nil
		})
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			BeforeRequest: func(req durable.GenerationRequest, _ durable.HookApi, _ chord.Context) (*durable.GenerationRequest, error) {
				inj := "injected"
				next := append(slices.Clone(req.Messages), &ai.UserMessage{
					Role:    "user",
					Content: ai.UserMessageContent{Text: &inj},
				})
				return &durable.GenerationRequest{Messages: next}, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks BeforeRequest: %v", err)
		}
		var responses []string
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterResponse: func(msg *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) error {
				txt, _ := testkit.TextOf(msg)
				mu.Lock()
				responses = append(responses, txt)
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("AddHooks AfterResponse: %v", err)
		}
		yields := 0
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			OnYield: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
				mu.Lock()
				n := yields
				yields++
				mu.Unlock()
				if n == 0 {
					return &durable.GenerationYieldDecision{
						Continue: durable.UserInput{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "keep going"}}},
					}, nil
				}
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks OnYield: %v", err)
		}

		h, _, entries, status := thtlRun(t, setup, []providers.FauxResponseStep{record, record}, nil)
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s", status)
		}
		mu.Lock()
		req0 := slices.Clone(requests[0])
		gotResponses := slices.Clone(responses)
		mu.Unlock()
		if len(req0) < 2 || !slices.Equal(req0[len(req0)-2:], []string{"user:go", "user:injected"}) {
			t.Fatalf("req0 tail=%v", req0)
		}
		if !slices.Equal(gotResponses, []string{"answer 1", "answer 2"}) {
			t.Fatalf("responses=%v", gotResponses)
		}
		kinds := make([]string, len(entries))
		for i, e := range entries {
			kinds[i] = e.Kind
		}
		if !slices.Equal(kinds, []string{"pi.user", "pi.assistant", "pi.user", "pi.assistant"}) {
			t.Fatalf("kinds=%v", kinds)
		}
		for _, e := range entries {
			if len(e.Model) > 0 {
				if txt, _ := testkit.TextOf(e.Model[0]); txt == "injected" {
					t.Fatalf("injected message leaked into entries")
				}
			}
		}
	})

	t.Run("keeps the run's input open across an onYield continuation and answers it with the final answer", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		yields := 0
		var hRef durable.Harness
		var inputId durable.SubmissionId
		var statusAtSecond durable.SubmissionStatus
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			OnYield: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
				mu.Lock()
				n := yields
				yields++
				mu.Unlock()
				if n == 0 {
					return &durable.GenerationYieldDecision{Continue: durable.UserInput{Text: thtlPtr("again")}}, nil
				}
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		second := providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			live := thtoLiveSnapshot(t, hRef, 1)
			id := live.Run.Inputs[0]
			sub, err := hRef.Submission(nil, id)
			if err != nil {
				return nil, err
			}
			st, err := sub.Status(nil)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			inputId = id
			statusAtSecond = st.Status
			mu.Unlock()
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("second")}, nil), nil
		})
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("first")}, nil)},
			second,
		}, func(opened durable.Harness, _ durable.Conversation) {
			hRef = opened
		})
		defer func() { _ = h.Close(nil) }()
		mu.Lock()
		gotId, gotStatus := inputId, statusAtSecond
		mu.Unlock()
		if gotStatus != durable.SubmissionStatusPlaced {
			t.Fatalf("statusAtSecond=%s, want placed", gotStatus)
		}
		var answers []durable.EntryRecord
		for _, e := range entries {
			if e.Kind == "pi.assistant" {
				answers = append(answers, e)
			}
		}
		sub, err := h.Submission(nil, gotId)
		if err != nil {
			t.Fatalf("Submission: %v", err)
		}
		finalSt, err := sub.Status(nil)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if finalSt.Status != durable.SubmissionStatusDone || finalSt.Answer == nil || *finalSt.Answer != answers[1].Id {
			t.Fatalf("finalSt=%+v, want answer %d", finalSt, answers[1].Id)
		}
	})

	t.Run("observes responses that arrive by polling a deferred request", func(t *testing.T) {
		setup := thtlNewSetup(t, &providers.RegisterFauxProviderOptions{
			Deferred: &providers.FauxDeferredOptions{
				PendingFetches: thtlPtr(1),
				PollAfterMs:    thtlPtr(int64(1)),
			},
		})
		var mu sync.Mutex
		var observed []string
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterResponse: func(msg *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) error {
				txt, _ := testkit.TextOf(msg)
				mu.Lock()
				observed = append(observed, string(msg.StopReason)+":"+txt)
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{
				Deferred: &durable.ConversationDeferredOptions{Enabled: true},
			}
		})
		h, _, _, status := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("late")}, nil)},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s", status)
		}
		mu.Lock()
		gotObs := slices.Clone(observed)
		mu.Unlock()
		if !slices.Equal(gotObs, []string{"stop:late"}) {
			t.Fatalf("observed=%v", gotObs)
		}
	})

	t.Run("lets the first onYield continuation win and reports throws without stopping later handlers", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var called []string
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterResponse: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) error {
				mu.Lock()
				called = append(called, "throwing observer")
				mu.Unlock()
				return errors.New("observer failed")
			},
		}); err != nil {
			t.Fatalf("AddHooks 1: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterResponse: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) error {
				mu.Lock()
				called = append(called, "next observer")
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 2: %v", err)
		}
		yields := 0
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			OnYield: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
				mu.Lock()
				n := yields
				yields++
				mu.Unlock()
				if n == 0 {
					return &durable.GenerationYieldDecision{Continue: durable.UserInput{Text: thtlPtr("first")}}, nil
				}
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 3: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			OnYield: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
				mu.Lock()
				called = append(called, "second onYield")
				cnt := 0
				for _, c := range called {
					if c == "second onYield" {
						cnt++
					}
				}
				mu.Unlock()
				if cnt == 1 {
					return &durable.GenerationYieldDecision{Continue: durable.UserInput{Text: thtlPtr("second")}}, nil
				}
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 4: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("a")}, nil)},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("b")}, nil)},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("c")}, nil)},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		var users []string
		for _, e := range entries {
			if e.Kind == "pi.user" && len(e.Model) > 0 {
				txt, _ := testkit.TextOf(e.Model[0])
				users = append(users, txt)
			}
		}
		if !slices.Equal(users, []string{"go", "first", "second"}) {
			t.Fatalf("users=%v", users)
		}
		mu.Lock()
		gotCalled := slices.Clone(called)
		mu.Unlock()
		secondCount, nextCount := 0, 0
		for _, c := range gotCalled {
			if c == "second onYield" {
				secondCount++
			} else if c == "next observer" {
				nextCount++
			}
		}
		if secondCount != 2 || nextCount != 3 {
			t.Fatalf("counts second=%d next=%d (called=%v)", secondCount, nextCount, gotCalled)
		}
		var repMsgs []string
		for _, err := range setup.Reports() {
			repMsgs = append(repMsgs, err.Error())
		}
		if !slices.Contains(repMsgs, "observer failed") {
			t.Fatalf("reports=%v", repMsgs)
		}
	})

	t.Run("keeps durable hook decisions in task memos", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		asked := 0
		if _, err := testkit.AddTool(setup.Registry, thtlTool("echo", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			BeforeTool: func(_ *ai.ToolCall, api durable.HookApi, callCtx chord.Context) (*durable.BeforeToolDecision, error) {
				mu.Lock()
				asked++
				mu.Unlock()
				dec, err := api.MemoPut(callCtx, "approval:decision", "approved")
				if err != nil {
					return nil, err
				}
				dec2, err := api.MemoPut(callCtx, "approval:decision", "denied")
				if err != nil {
					return nil, err
				}
				if dec != "approved" || dec2 != dec {
					return nil, fmt.Errorf("unexpected memos %v vs %v", dec, dec2)
				}
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		h, _, _, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"echo", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		mu.Lock()
		gotAsked := asked
		mu.Unlock()
		if gotAsked != 1 {
			t.Fatalf("asked=%d, want 1", gotAsked)
		}
	})
}

func TestHarnessToolExecutionApi(t *testing.T) {
	t.Run("builds the environment per call from the conversation's cwd and runs commits, memos, and child tasks", func(t *testing.T) {
		setup := thtlNewSetup(t)
		type childInput struct {
			N int `json:"n"`
		}
		type childCP struct {
			Phase string `json:"phase"`
		}
		child := durable.DefineTask(durable.TaskDefinition[childInput, childCP, int, struct{}]{
			Name:    "test.child",
			Version: 1,
			Initial: func(_ childInput) childCP { return childCP{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[childInput, childCP, int, struct{}]{
				"run": func(callCtx chord.Context, task durable.RunningTask[childInput, childCP], runtime *durable.TaskRuntime[childInput, childCP, int, struct{}]) error {
					return runtime.Commit(callCtx, func(_ durable.Tx, _ durable.RunningTask[childInput, childCP]) (*durable.NextTaskState[childCP, int], error) {
						s := testkit.Completed(childCP{Phase: "run"}, task.Input.N*2)
						return &s, nil
					})
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[childInput, childCP], _ *durable.TaskRuntime[childInput, childCP, int, struct{}]) error {
				return nil
			},
		})
		if _, err := testkit.AddTask(setup.Registry, child); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		var mu sync.Mutex
		type probeSeen struct {
			cwd        string
			tools      []string
			hasSelfExt bool
			byTask     bool
			m1, m2     any
			outcome    durable.TaskOutcomeStatus
			res        string
		}
		var seen []probeSeen
		probe := thtlTool("probe", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			execEnv, err := api.Env(callCtx)
			if err != nil {
				return nil, err
			}
			ag, err := api.Agent(callCtx)
			if err != nil {
				return nil, err
			}
			var tNames []string
			for _, tl := range ag.Tools {
				tNames = append(tNames, tl.Name)
			}
			hasSelf := api.Registry().Extension("tool:probe") != nil
			entryAny, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
				if err := durable.Configure(tx, api.ConversationId(), durable.AgentChange{Cwd: jsonx.Some("/")}); err != nil {
					return nil, err
				}
				return tx.AppendEntry(api.ConversationId(), durable.EntryDraft{Kind: "test.note", Data: api.CallId()})
			})
			if err != nil {
				return nil, err
			}
			entry := entryAny.(*durable.EntryRecord)
			m1, err := api.MemoPut(callCtx, "m", 1)
			if err != nil {
				return nil, err
			}
			m2, err := api.MemoPut(callCtx, "m", 2)
			if err != nil {
				return nil, err
			}
			id, err := api.CreateTask(callCtx, child, childInput{N: 21}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			if err != nil {
				return nil, err
			}
			done, err := api.WaitForTask(callCtx, id)
			if err != nil {
				return nil, err
			}
			resVal, _ := done.State.Outcome.Result.Get()
			mu.Lock()
			seen = append(seen, probeSeen{
				cwd:        execEnv.Cwd(),
				tools:      tNames,
				hasSelfExt: hasSelf,
				byTask:     entry.ByTaskId != nil && *entry.ByTaskId == api.TaskId(),
				m1:         m1,
				m2:         m2,
				outcome:    done.State.Outcome.Status,
				res:        fmt.Sprint(resVal),
			})
			mu.Unlock()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})
		if _, err := testkit.AddTool(setup.Registry, probe); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"probe", nil, "c1"}, thtlCallSpec{"probe", nil, "c2"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.ToolExecution = durable.ToolExecutionModeSequential
		})
		type envCall struct {
			cid durable.ConversationId
			cwd string
		}
		var targets []envCall
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup, testkit.OpenChatOptions{
			EnvFn: func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
				cwd := "/tmp"
				if target.Cwd != nil {
					cwd = *target.Cwd
				}
				mu.Lock()
				targets = append(targets, envCall{cid: target.ConversationId, cwd: cwd})
				mu.Unlock()
				return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd}), nil
			},
		})
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if err := root.Configure(durable.AgentChange{Cwd: jsonx.Some("/tmp")}, nil); err != nil {
			t.Fatalf("Configure: %v", err)
		}
		if _, err := thtoSubmitInput(t, root, "go").Wait(nil); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		mu.Lock()
		gotSeen := slices.Clone(seen)
		gotTargets := slices.Clone(targets)
		mu.Unlock()
		if len(gotSeen) != 2 {
			t.Fatalf("expected 2 seen, got %d", len(gotSeen))
		}
		for i, wantCwd := range []string{"/tmp", "/"} {
			s := gotSeen[i]
			if s.cwd != wantCwd || !slices.Equal(s.tools, []string{"probe"}) || !s.hasSelfExt || !s.byTask || fmt.Sprint(s.m1) != "1" || fmt.Sprint(s.m2) != "1" || s.outcome != durable.TaskOutcomeCompleted || s.res != "42" {
				t.Fatalf("seen[%d]=%+v, want cwd=%s", i, s, wantCwd)
			}
		}
		if !slices.Contains(gotTargets, envCall{root.Id(), "/tmp"}) || !slices.Contains(gotTargets, envCall{root.Id(), "/"}) {
			t.Fatalf("targets=%+v", gotTargets)
		}
	})

	t.Run("answers a throwing environment with a tool_error result and reports it once while preparing", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("probe", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ran"}},
			}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"probe", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil, testkit.OpenChatOptions{
			EnvFn: func(_ durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
				return nil, errors.New("no sandbox")
			},
		})
		defer func() { _ = h.Close(nil) }()
		res := thtoResults(entries)[0]
		if !res.IsError || thtlResultText(res) != "<harness>\n[error] no sandbox\n</harness>" {
			t.Fatalf("unexpected res=%+v (%q)", res, thtlResultText(res))
		}
		count := 0
		for _, r := range setup.Reports() {
			if r.Error() == "no sandbox" {
				count++
			}
		}
		if count != 2 {
			t.Fatalf("no sandbox report count=%d, want 2 (%v)", count, setup.Reports())
		}
	})
}

func TestHarnessCodingTools(t *testing.T) {
	t.Run("answers a failing command with its retained tail and diagnostics in order", func(t *testing.T) {
		dir := t.TempDir()
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, tools.CreateBashTool()); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		cmd := "i=1; while [ $i -le 3000 ]; do echo line-$i; i=$((i + 1)); done; exit 7"
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"bash", jsonx.ObjectOf("command", cmd), "b"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		execEnv := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup, testkit.OpenChatOptions{Env: execEnv})
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := thtoSubmitInput(t, root, "go").Wait(nil); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		var resultEntry durable.EntryRecord
		for _, e := range entries {
			if durable.ToolResultEntry.Is(&e) {
				resultEntry = e
				break
			}
		}
		res := resultEntry.Model[0].(*ai.ToolResultMessage)
		if !res.IsError {
			t.Fatalf("expected IsError")
		}
		txt := thtlResultText(res)
		if !strings.HasPrefix(txt, "line-1001\n") {
			t.Fatalf("expected prefix line-1001\\n, got %.40q", txt)
		}
		var codes []string
		for _, d := range thtlDecodeToolResultData(t, resultEntry) {
			if d.Code != nil {
				codes = append(codes, *d.Code)
			}
		}
		if !slices.Equal(codes, []string{"full_output", "tool_error", "truncated"}) {
			t.Fatalf("codes=%v", codes)
		}
		if !strings.Contains(txt, "line-3000\n|<harness>\n[info] Full output: ") ||
			!strings.Contains(txt, "\n[error] Command exited with code 7\n[warn] Output truncated to its end: 1000 lines, ") {
			t.Fatalf("unexpected text tail: %q", txt)
		}
	})

	t.Run("reads, edits, and runs a command in one run, then answers", func(t *testing.T) {
		dir := t.TempDir()
		notePath := filepath.Join(dir, "notes.txt")
		if err := os.WriteFile(notePath, []byte("hello world\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		setup := thtlNewSetup(t)
		if err := setup.Registry.Install(durable.DefineExtension(&durable.Extension{
			Name:  "coding",
			Tools: []*durable.ToolRegistration{tools.CreateReadTool(), tools.CreateEditTool(), tools.CreateBashTool()},
		})); err != nil {
			t.Fatalf("Install: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"read", jsonx.ObjectOf("path", "notes.txt"), "r"})},
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"edit", jsonx.ObjectOf("path", "notes.txt", "edits", []any{jsonx.ObjectOf("oldText", "world", "newText", "durable")}), "e"})},
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"bash", jsonx.ObjectOf("command", "cat notes.txt"), "b"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		execEnv := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup, testkit.OpenChatOptions{Env: execEnv})
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thtoSubmitInput(t, root, "go").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("Wait: err=%v status=%v", err, settled)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		results := thtoResults(entries)
		if len(results) != 3 {
			t.Fatalf("len(results)=%d", len(results))
		}
		wants := []struct {
			name string
			txt  string
		}{
			{"read", "hello world\n"},
			{"edit", "Successfully replaced 1 block(s) in notes.txt."},
			{"bash", "hello durable\n"},
		}
		for i, w := range wants {
			if results[i].ToolName != w.name || results[i].IsError || thtlResultText(results[i]) != w.txt {
				t.Fatalf("result[%d]=(%s, %v, %q), want (%s, false, %q)", i, results[i].ToolName, results[i].IsError, thtlResultText(results[i]), w.name, w.txt)
			}
		}
		if entries[len(entries)-1].Kind != "pi.assistant" {
			t.Fatalf("last entry=%s", entries[len(entries)-1].Kind)
		}
		gotFile, err := os.ReadFile(notePath)
		if err != nil || string(gotFile) != "hello durable\n" {
			t.Fatalf("file=%q err=%v", string(gotFile), err)
		}
	})
}

func TestHarnessToolProgressAndLifetime(t *testing.T) {
	t.Run("applies the default output limits", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("lines", func(_ any, api durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			for i := 1; i <= 2500; i++ {
				api.Output(fmt.Sprintf("%d\n", i))
			}
			return &durable.ToolExecutionResult{}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"lines", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		txt := thtlResultText(thtoResults(entries)[0])
		if !strings.HasPrefix(txt, "1\n2\n") || !strings.HasSuffix(txt, "\n2000\n|<harness>\n[warn] Output truncated to its beginning: 500 lines, 2500 bytes dropped\n</harness>") {
			t.Fatalf("unexpected truncated text: %q", txt)
		}
	})

	t.Run("sanitizes running output but keeps explicit result content as the tool returned it", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var slotOutput string
		if _, err := testkit.AddTool(setup.Registry, thtlTool("noisy", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			api.Output("a\ab\r\n")
			if err := api.Details(jsonx.ObjectOf("ready", true), callCtx); err != nil {
				return nil, err
			}
			raw, err := api.Snapshot(callCtx, durable.LiveDoc, api.ConversationId())
			if err != nil {
				return nil, err
			}
			var live durable.LiveState
			if err := jsonx.Decode(raw, &live); err != nil {
				return nil, err
			}
			mu.Lock()
			if len(live.Tools) > 0 && live.Tools[0].Output != nil {
				slotOutput = *live.Tools[0].Output
			}
			mu.Unlock()
			return &durable.ToolExecutionResult{}, nil
		})); err != nil {
			t.Fatalf("AddTool noisy: %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, thtlTool("explicit", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "c\x1bd"}},
			}, nil
		})); err != nil {
			t.Fatalf("AddTool explicit: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"noisy", nil, "c1"}, thtlCallSpec{"explicit", nil, "c2"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		mu.Lock()
		gotSlot := slotOutput
		mu.Unlock()
		if gotSlot != "ab\n" {
			t.Fatalf("slotOutput=%q, want ab\\n", gotSlot)
		}
		byId := map[string]string{}
		for _, r := range thtoResults(entries) {
			byId[r.ToolCallId] = thtlResultText(r)
		}
		if byId["c1"] != "ab\n" || byId["c2"] != "c\x1bd" {
			t.Fatalf("byId=%+v", byId)
		}
	})

	t.Run("drops control keys set to undefined instead of faulting", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("grow", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				Control:    &durable.ToolControl{AddTools: []string{"extra"}},
			}, nil
		})); err != nil {
			t.Fatalf("AddTool grow: %v", err)
		}
		extra := thtlTool("extra", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})
		if _, err := testkit.AddTool(setup.Registry, extra); err != nil {
			t.Fatalf("AddTool extra: %v", err)
		}
		h, root, _, status := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"grow", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, func(_ durable.Harness, root durable.Conversation) {
			if err := root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsRemove(extra))}, nil); err != nil {
				t.Fatalf("Configure: %v", err)
			}
		})
		defer func() { _ = h.Close(nil) }()
		if status != durable.SubmissionStatusDone {
			t.Fatalf("status=%s", status)
		}
		st := thtlDecodeAgentState(t, h, root.Id())
		if st.Tools == nil || len(st.Tools.Remove) != 0 {
			t.Fatalf("agent tools=%+v", st.Tools)
		}
	})

	t.Run("uses explicit null details instead of the last reported value", func(t *testing.T) {
		setup := thtlNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("null", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			if err := api.Details(jsonx.ObjectOf("old", 1), callCtx); err != nil {
				return nil, err
			}
			return &durable.ToolExecutionResult{HasContent: true, HasDetails: true, Details: nil}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"null", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		res := thtoResults(entries)[0]
		if !res.Details.IsNull() {
			t.Fatalf("expected null details, got %+v", res.Details)
		}
	})

	t.Run("settles details() promises with coalesced progress commits and the terminal commit", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var settled []string
		var wg sync.WaitGroup
		if _, err := testkit.AddTool(setup.Registry, thtlTool("details", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			if err := api.Details(jsonx.ObjectOf("n", 1), callCtx); err != nil {
				return nil, err
			}
			mu.Lock()
			settled = append(settled, "first")
			mu.Unlock()

			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := api.Details(jsonx.ObjectOf("n", 2), callCtx); err == nil {
					mu.Lock()
					settled = append(settled, "second")
					mu.Unlock()
				}
			}()
			time.Sleep(5 * time.Millisecond)

			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := api.Details(jsonx.ObjectOf("n", 3), callCtx); err == nil {
					mu.Lock()
					settled = append(settled, "third")
					mu.Unlock()
				}
			}()
			time.Sleep(5 * time.Millisecond)
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, entries, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"details", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		wg.Wait()
		mu.Lock()
		gotSettled := slices.Clone(settled)
		mu.Unlock()
		if len(gotSettled) != 3 || gotSettled[0] != "first" || !slices.Contains(gotSettled, "second") || !slices.Contains(gotSettled, "third") {
			t.Fatalf("settled=%v", gotSettled)
		}
		det, ok := thtoResults(entries)[0].Details.Get()
		if !ok {
			t.Fatalf("expected details")
		}
		if obj, ok := det.(*jsonx.Object); !ok {
			t.Fatalf("expected *jsonx.Object, got %T", det)
		} else if n, _ := obj.Get("n"); fmt.Sprint(n) != "3" {
			t.Fatalf("details.n=%v, want 3", n)
		}
	})

	t.Run("finishes a call under the implementation it resolved when the tool is replaced mid-call", func(t *testing.T) {
		setup := thtlNewSetup(t)
		started := testkit.NewDeferred[struct{}]()
		finished := testkit.NewDeferred[struct{}]()
		v1 := thtlTool("work", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			started.Resolve(struct{}{})
			_, _ = finished.Wait()
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "v1"}},
			}, nil
		})
		if _, err := testkit.AddTool(setup.Registry, v1); err != nil {
			t.Fatalf("AddTool v1: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"work", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thtoSubmitInput(t, root, "go")
		if _, err := started.Wait(); err != nil {
			t.Fatalf("started.Wait: %v", err)
		}
		if _, err := testkit.AddTool(setup.Registry, thtlTool("work", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "v2"}},
			}, nil
		})); err != nil {
			t.Fatalf("AddTool v2: %v", err)
		}
		finished.Resolve(struct{}{})
		if _, err := sub.Wait(nil); err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thtlResultText(thtoResults(entries)[0]); got != "v1" {
			t.Fatalf("text=%q, want v1", got)
		}
	})

	t.Run("uses a section and hook extension reloaded mid-run from the run's next request", func(t *testing.T) {
		setup := thtlNewSetup(t)
		var mu sync.Mutex
		var requests []string
		prompt := func(version string) *durable.Extension {
			return durable.DefineExtension(&durable.Extension{
				Name: "prompt",
				Sections: []*durable.PromptSection{
					durable.SimplePromptSection("mode", version),
				},
				Hooks: []durable.HookRegistration{
					durable.Hook(durable.GenerationTask, durable.GenerationHooks{
						BeforeRequest: func(_ durable.GenerationRequest, _ durable.HookApi, _ chord.Context) (*durable.GenerationRequest, error) {
							mu.Lock()
							requests = append(requests, version)
							mu.Unlock()
							return nil, nil
						},
					}),
				},
			})
		}
		if err := setup.Registry.Install(prompt("v1")); err != nil {
			t.Fatalf("Install v1: %v", err)
		}
		running := testkit.NewDeferred[struct{}]()
		reloaded := testkit.NewDeferred[struct{}]()
		if _, err := testkit.AddTool(setup.Registry, thtlTool("work", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			running.Resolve(struct{}{})
			_, _ = reloaded.Wait()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"work", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thtoSubmitInput(t, root, "go")
		if _, err := running.Wait(); err != nil {
			t.Fatalf("running.Wait: %v", err)
		}
		if err := setup.Registry.Install(prompt("v2")); err != nil {
			t.Fatalf("Install v2: %v", err)
		}
		reloaded.Resolve(struct{}{})
		if _, err := sub.Wait(nil); err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		var modes []string
		for _, e := range entries {
			if len(e.Model) > 0 {
				if sm, ok := e.Model[0].(*ai.SystemMessage); ok && sm.Sections != nil {
					if v, ok := sm.Sections.Get("mode"); ok && v != nil {
						modes = append(modes, *v)
					}
				}
			}
		}
		if !slices.Equal(modes, []string{"<mode>\nv1\n</mode>", "<mode>\nv2\n</mode>"}) {
			t.Fatalf("modes=%v", modes)
		}
		mu.Lock()
		gotReqs := slices.Clone(requests)
		mu.Unlock()
		if !slices.Equal(gotReqs, []string{"v1", "v2"}) {
			t.Fatalf("requests=%v", gotReqs)
		}
	})

	t.Run("rejects invocation-bound waits and stops watches when the tool's invocation ends", func(t *testing.T) {
		setup := thtlNewSetup(t)
		type cp struct {
			Phase string `json:"phase"`
		}
		never := durable.DefineTask(durable.TaskDefinition[struct{}, cp, any, struct{}]{
			Name:    "test.never",
			Version: 1,
			Initial: func(_ struct{}) cp { return cp{Phase: "never"} },
			Phases: map[string]durable.PhaseHandler[struct{}, cp, any, struct{}]{
				"never": func(_ chord.Context, _ durable.RunningTask[struct{}, cp], _ *durable.TaskRuntime[struct{}, cp, any, struct{}]) error {
					return nil
				},
			},
			Abort: func(_ chord.Context, _ durable.RunningTask[struct{}, cp], _ *durable.TaskRuntime[struct{}, cp, any, struct{}]) error {
				return nil
			},
		})
		waitErrCh := make(chan error, 1)
		watchEndCh := make(chan durable.WatchEnd, 1)
		if _, err := testkit.AddTool(setup.Registry, thtlTool("detach", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			child, err := api.CreateTask(callCtx, never, struct{}{}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
			if err != nil {
				return nil, err
			}
			go func() {
				_, wErr := api.WaitForTask(callCtx, child)
				waitErrCh <- wErr
			}()
			watch, err := api.WatchDoc(callCtx, durable.LiveDoc, api.ConversationId())
			if err != nil {
				return nil, err
			}
			go func() {
				watchEndCh <- watch.Closed()
			}()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		h, _, _, _ := thtlRun(t, setup, []providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"detach", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		}, nil)
		defer func() { _ = h.Close(nil) }()
		waitErr := <-waitErrCh
		if waitErr == nil || !strings.Contains(waitErr.Error(), "invocation has ended") {
			t.Fatalf("waitErr=%v, want invocation has ended", waitErr)
		}
		_ = <-watchEndCh
	})

	t.Run("rejects details() still waiting when the call is aborted during afterTool", func(t *testing.T) {
		setup := thtlNewSetup(t)
		pendingDetailsErr := make(chan error, 1)
		inAfterTool := testkit.NewDeferred[struct{}]()
		if _, err := testkit.AddTool(setup.Registry, thtlTool("slow", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			api.Output("first\n")
			startedDetails := make(chan struct{})
			go func() {
				close(startedDetails)
				pendingDetailsErr <- api.Details(jsonx.ObjectOf("step", 1), callCtx)
			}()
			<-startedDetails
			time.Sleep(5 * time.Millisecond)
			return &durable.ToolExecutionResult{}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			AfterTool: func(_ *ai.ToolCall, _ *durable.ToolExecutionResult, _ durable.HookApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				inAfterTool.Resolve(struct{}{})
				_ = testkit.Aborted(callCtx.AbortSignal())
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"slow", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thtoSubmitInput(t, root, "go")
		if _, err := inAfterTool.Wait(); err != nil {
			t.Fatalf("inAfterTool.Wait: %v", err)
		}
		taskId := thtoToolTaskId(t, h)
		if _, err := h.AbortTask(nil, taskId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if err := <-pendingDetailsErr; err == nil {
			t.Fatalf("expected pending details to fail on abort")
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
	})

	t.Run("answers an aborted tool with only its durable output, discarding buffered output", func(t *testing.T) {
		setup := thtlNewSetup(t)
		reached := testkit.NewDeferred[struct{}]()
		if _, err := testkit.AddTool(setup.Registry, thtlTool("slow", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			api.Output("durable\n")
			if err := testkit.WaitFor(func() (bool, error) {
				raw, err := api.Snapshot(callCtx, durable.LiveDoc, api.ConversationId())
				if err != nil || raw == nil {
					return false, err
				}
				var live durable.LiveState
				if err := jsonx.Decode(raw, &live); err != nil {
					return false, err
				}
				return len(live.Tools) > 0 && live.Tools[0].Output != nil && *live.Tools[0].Output == "durable\n", nil
			}); err != nil {
				return nil, err
			}
			api.Output("buffered\n")
			reached.Resolve(struct{}{})
			_ = testkit.Aborted(callCtx.AbortSignal())
			return &durable.ToolExecutionResult{}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtlCalls(thtlCallSpec{"slow", nil, "c1"})},
			&providers.FauxResponseMessage{Message: thtlDone()},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thtoSubmitInput(t, root, "go")
		if _, err := reached.Wait(); err != nil {
			t.Fatalf("reached.Wait: %v", err)
		}
		taskId := thtoToolTaskId(t, h)
		if _, err := h.AbortTask(nil, taskId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		want := "durable\n|<harness>\n[error] Tool slow was aborted\n</harness>"
		if got := thtlResultText(thtoResults(entries)[0]); got != want {
			t.Fatalf("text=%q, want %q", got, want)
		}
	})
}
