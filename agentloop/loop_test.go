package agentloop

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// identityConvert passes the three LLM message types through.
func identityConvert(msgs []AgentMessage) ([]Message, error) {
	var out []Message
	for _, m := range msgs {
		if lm, ok := m.(Message); ok {
			out = append(out, lm)
		}
	}
	return out, nil
}

func baseConfig() *AgentLoopConfig {
	return &AgentLoopConfig{
		Model:        &Model{ID: "faux", Api: "faux", Provider: "faux"},
		MaxTokens:    100,
		ConvertToLlm: identityConvert,
	}
}

// collect drains a loop stream into a slice of event type names.
func collect(stream *EventStream[AgentEvent, []AgentMessage]) []string {
	var names []string
	for ev := range stream.Events() {
		names = append(names, eventName(ev))
	}
	return names
}

func eventName(ev AgentEvent) string {
	switch e := ev.(type) {
	case *AgentStartEvent:
		return "agent_start"
	case *AgentEndEvent:
		return "agent_end"
	case *TurnStartEvent:
		return "turn_start"
	case *TurnEndEvent:
		return "turn_end"
	case *MessageStartEvent:
		return "message_start(" + e.Message.Role() + ")"
	case *MessageUpdateEvent:
		return "message_update"
	case *MessageEndEvent:
		return "message_end(" + e.Message.Role() + ")"
	case *ToolExecutionStartEvent:
		return "tool_execution_start(" + e.ToolName + ")"
	case *ToolExecutionUpdateEvent:
		return "tool_execution_update(" + e.ToolName + ")"
	case *ToolExecutionEndEvent:
		return "tool_execution_end(" + e.ToolName + ")"
	default:
		return "unknown"
	}
}

func TestSingleTextTurn(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	agentCtx := &AgentContext{}
	prompts := []AgentMessage{NewUserText("hi")}

	stream := AgentLoop(ctx, prompts, agentCtx, cfg, FauxStreamFn(FauxText("hello")))
	got := collect(stream)

	want := []string{
		"agent_start",
		"turn_start",
		"message_start(user)",
		"message_end(user)",
		"message_start(assistant)",
		"message_update", // text_start
		"message_update", // text_delta
		"message_update", // text_end
		"message_end(assistant)",
		"turn_end",
		"agent_end",
	}
	if !equalStrs(got, want) {
		t.Fatalf("event sequence mismatch:\n got=%v\nwant=%v", got, want)
	}

	res := stream.Result()
	if len(res) != 2 {
		t.Fatalf("expected 2 result messages, got %d", len(res))
	}
}

func TestOneToolCallThenFinalText(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	echo := &FuncTool{
		NameVal: "echo",
		ExecuteFn: func(ctx context.Context, id string, p map[string]any, u UpdateFunc) (ToolResult, error) {
			return TextResult("echoed"), nil
		},
	}
	agentCtx := &AgentContext{Tools: []AgentTool{echo}}
	prompts := []AgentMessage{NewUserText("go")}

	streamFn := FauxStreamFn(
		FauxToolCall("c1", "echo", map[string]any{"x": 1}),
		FauxText("done"),
	)
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, streamFn)
	got := collect(stream)

	// Verify key ordering: tool_execution_start before _end, tool result
	// message_start/end after _end, and a second turn runs.
	mustContainInOrder(t, got,
		"tool_execution_start(echo)",
		"tool_execution_end(echo)",
		"message_start(toolResult)",
		"message_end(toolResult)",
	)
	if countStr(got, "turn_start") < 1 {
		t.Fatalf("expected at least one explicit turn_start for second turn: %v", got)
	}
	// Two assistant messages total (tool turn + final text turn).
	if c := countStr(got, "message_start(assistant)"); c != 2 {
		t.Fatalf("expected 2 assistant message_start, got %d: %v", c, got)
	}
}

// orderedTool completes only after its release channel is closed, and records
// completion order into a shared slice.
type orderedTool struct {
	name      string
	release   chan struct{}
	completed *[]string
	mu        *sync.Mutex
}

func (o *orderedTool) Name() string                 { return o.name }
func (o *orderedTool) Label() string                { return o.name }
func (o *orderedTool) Description() string          { return "" }
func (o *orderedTool) Parameters() map[string]any   { return nil }
func (o *orderedTool) ExecutionMode() ExecutionMode { return ExecutionParallel }
func (o *orderedTool) PrepareArguments(raw map[string]any) (map[string]any, bool) {
	return raw, false
}
func (o *orderedTool) Execute(ctx context.Context, id string, p map[string]any, u UpdateFunc) (ToolResult, error) {
	<-o.release
	o.mu.Lock()
	*o.completed = append(*o.completed, o.name)
	o.mu.Unlock()
	return TextResult("ok:" + o.name), nil
}

func threeToolMessage() *AssistantMessage {
	return FauxAssistant(StopReasonToolUse,
		&ToolCall{ID: "1", Name: "a", Arguments: map[string]any{}},
		&ToolCall{ID: "2", Name: "b", Arguments: map[string]any{}},
		&ToolCall{ID: "3", Name: "c", Arguments: map[string]any{}},
	)
}

func TestParallelOrdering(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	cfg.ToolExecution = ExecutionParallel

	var mu sync.Mutex
	var completed []string
	relA := make(chan struct{})
	relB := make(chan struct{})
	relC := make(chan struct{})

	toolA := &orderedTool{name: "a", release: relA, completed: &completed, mu: &mu}
	toolB := &orderedTool{name: "b", release: relB, completed: &completed, mu: &mu}
	toolC := &orderedTool{name: "c", release: relC, completed: &completed, mu: &mu}

	agentCtx := &AgentContext{Tools: []AgentTool{toolA, toolB, toolC}}
	prompts := []AgentMessage{NewUserText("go")}

	streamFn := FauxStreamFn(threeToolMessage(), FauxText("done"))
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, streamFn)

	// Consume events concurrently; control completion order: c, then a, then b.
	var names []string
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		for ev := range stream.Events() {
			names = append(names, eventName(ev))
		}
	}()

	close(relC)
	waitFor(&mu, &completed, 1)
	close(relA)
	waitFor(&mu, &completed, 2)
	close(relB)
	waitFor(&mu, &completed, 3)
	done.Wait()

	// tool_execution_end events come in completion order: c, a, b.
	ends := filterEnds(names)
	wantEnds := []string{
		"tool_execution_end(c)",
		"tool_execution_end(a)",
		"tool_execution_end(b)",
	}
	if !equalStrs(ends, wantEnds) {
		t.Fatalf("tool_execution_end order = %v want %v", ends, wantEnds)
	}

	// tool-result message_start events come in SOURCE order: a, b, c.
	starts := filterToolResultStarts(stream, names)
	_ = starts // names only has role; verify via direct loop instead.

	// Re-run to capture tool-result ordering by ToolCallID via a fresh run.
	verifyToolResultSourceOrder(t)

	// tool_execution_start events are in source order: a, b, c.
	wantStartOrder := []string{
		"tool_execution_start(a)",
		"tool_execution_start(b)",
		"tool_execution_start(c)",
	}
	if !equalStrs(filterStarts(names), wantStartOrder) {
		t.Fatalf("tool_execution_start order = %v want %v", filterStarts(names), wantStartOrder)
	}
}

// verifyToolResultSourceOrder runs a parallel batch and asserts the toolResult
// messages are emitted in source order (by ToolCallID) regardless of completion
// order.
func verifyToolResultSourceOrder(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	cfg.ToolExecution = ExecutionParallel

	var mu sync.Mutex
	var completed []string
	relA := make(chan struct{})
	relB := make(chan struct{})
	relC := make(chan struct{})
	toolA := &orderedTool{name: "a", release: relA, completed: &completed, mu: &mu}
	toolB := &orderedTool{name: "b", release: relB, completed: &completed, mu: &mu}
	toolC := &orderedTool{name: "c", release: relC, completed: &completed, mu: &mu}

	agentCtx := &AgentContext{Tools: []AgentTool{toolA, toolB, toolC}}
	prompts := []AgentMessage{NewUserText("go")}
	streamFn := FauxStreamFn(threeToolMessage(), FauxText("done"))
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, streamFn)

	var toolResultIDs []string
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		for ev := range stream.Events() {
			if ms, ok := ev.(*MessageStartEvent); ok {
				if trm, ok := ms.Message.(*ToolResultMessage); ok {
					toolResultIDs = append(toolResultIDs, trm.ToolCallID)
				}
			}
		}
	}()

	close(relC)
	waitFor(&mu, &completed, 1)
	close(relA)
	waitFor(&mu, &completed, 2)
	close(relB)
	waitFor(&mu, &completed, 3)
	done.Wait()

	want := []string{"1", "2", "3"} // source order a,b,c
	if !equalStrs(toolResultIDs, want) {
		t.Fatalf("toolResult message order = %v want %v (source order)", toolResultIDs, want)
	}
}

func TestSequentialMode(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	cfg.ToolExecution = ExecutionSequential

	// In sequential mode each Execute runs to completion before the next
	// starts; we verify no overlap and source order via a running counter.
	var mu sync.Mutex
	active := 0
	maxActive := 0
	var order []string

	mk := func(name string) *FuncTool {
		return &FuncTool{
			NameVal: name,
			ExecuteFn: func(ctx context.Context, id string, p map[string]any, u UpdateFunc) (ToolResult, error) {
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				order = append(order, name)
				mu.Unlock()
				mu.Lock()
				active--
				mu.Unlock()
				return TextResult(name), nil
			},
		}
	}

	agentCtx := &AgentContext{Tools: []AgentTool{mk("a"), mk("b"), mk("c")}}
	prompts := []AgentMessage{NewUserText("go")}
	streamFn := FauxStreamFn(threeToolMessage(), FauxText("done"))
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, streamFn)
	got := collect(stream)

	if maxActive > 1 {
		t.Fatalf("sequential mode had overlapping execution: maxActive=%d", maxActive)
	}
	if !equalStrs(order, []string{"a", "b", "c"}) {
		t.Fatalf("sequential exec order = %v want [a b c]", order)
	}
	// tool_execution_end interleaves per-tool in source order.
	wantEnds := []string{
		"tool_execution_end(a)",
		"tool_execution_end(b)",
		"tool_execution_end(c)",
	}
	if !equalStrs(filterEnds(got), wantEnds) {
		t.Fatalf("sequential end order = %v want %v", filterEnds(got), wantEnds)
	}
}

func TestTerminateHint(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	stopTool := &FuncTool{
		NameVal: "stop",
		ExecuteFn: func(ctx context.Context, id string, p map[string]any, u UpdateFunc) (ToolResult, error) {
			return ToolResult{Content: []Content{&TextContent{Text: "bye"}}, Terminate: true}, nil
		},
	}
	agentCtx := &AgentContext{Tools: []AgentTool{stopTool}}
	prompts := []AgentMessage{NewUserText("go")}

	// Second faux message should NOT be reached because terminate ends the loop.
	streamFn := FauxStreamFn(
		FauxToolCall("c1", "stop", map[string]any{}),
		FauxText("should-not-appear"),
	)
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, streamFn)
	got := collect(stream)

	// Only ONE assistant message (the tool turn). No second turn.
	if c := countStr(got, "message_start(assistant)"); c != 1 {
		t.Fatalf("terminate should end loop after one assistant turn, got %d: %v", c, got)
	}
	if got[len(got)-1] != "agent_end" {
		t.Fatalf("last event should be agent_end: %v", got)
	}
}

func TestErrorStop(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	agentCtx := &AgentContext{}
	prompts := []AgentMessage{NewUserText("go")}

	errMsg := FauxAssistant(StopReasonError, &TextContent{Text: "boom"})
	errMsg.ErrorMessage = "boom"
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, FauxStreamFn(errMsg, FauxText("nope")))
	got := collect(stream)

	// turn_end then agent_end; only one assistant message.
	if c := countStr(got, "message_start(assistant)"); c != 1 {
		t.Fatalf("error stop should end after one assistant turn, got %d: %v", c, got)
	}
	mustContainInOrder(t, got, "turn_end", "agent_end")
	if got[len(got)-1] != "agent_end" {
		t.Fatalf("last event should be agent_end: %v", got)
	}
	// turn_end carries empty toolResults — checked structurally below.
}

func TestErrorStopEmptyToolResults(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	agentCtx := &AgentContext{}
	prompts := []AgentMessage{NewUserText("go")}

	errMsg := FauxAssistant(StopReasonError, &TextContent{Text: "boom"})
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, FauxStreamFn(errMsg))

	for ev := range stream.Events() {
		if te, ok := ev.(*TurnEndEvent); ok {
			if len(te.ToolResults) != 0 {
				t.Fatalf("error stop turn_end should have empty toolResults, got %d", len(te.ToolResults))
			}
		}
	}
}

func TestGetFollowUpMessages(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()

	calls := 0
	cfg.GetFollowUpMessages = func() ([]AgentMessage, error) {
		calls++
		if calls == 1 {
			return []AgentMessage{NewUserText("follow up")}, nil
		}
		return nil, nil
	}

	agentCtx := &AgentContext{}
	prompts := []AgentMessage{NewUserText("first")}
	streamFn := FauxStreamFn(FauxText("a1"), FauxText("a2"))
	stream := AgentLoop(ctx, prompts, agentCtx, cfg, streamFn)
	got := collect(stream)

	// Two assistant turns because of one follow-up.
	if c := countStr(got, "message_start(assistant)"); c != 2 {
		t.Fatalf("expected 2 assistant turns with follow-up, got %d: %v", c, got)
	}
	// The follow-up user message should appear as a user message_start beyond
	// the initial prompt.
	if c := countStr(got, "message_start(user)"); c != 2 {
		t.Fatalf("expected 2 user message_start (prompt + follow-up), got %d: %v", c, got)
	}
}

func TestAgentLoopContinueValidation(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()

	// Empty context => immediate end with nil result, no agent_start.
	empty := AgentLoopContinue(ctx, &AgentContext{}, cfg, FauxStreamFn(FauxText("x")))
	if got := collect(empty); len(got) != 0 {
		t.Fatalf("expected no events for empty continue, got %v", got)
	}

	// Last message assistant => immediate end.
	withAssistant := &AgentContext{Messages: []AgentMessage{
		NewUserText("hi"),
		FauxText("done"),
	}}
	bad := AgentLoopContinue(ctx, withAssistant, cfg, FauxStreamFn(FauxText("x")))
	if got := collect(bad); len(got) != 0 {
		t.Fatalf("expected no events for assistant-last continue, got %v", got)
	}

	// Valid continue (last is user).
	good := &AgentContext{Messages: []AgentMessage{NewUserText("hi")}}
	ok := AgentLoopContinue(ctx, good, cfg, FauxStreamFn(FauxText("answer")))
	got := collect(ok)
	if len(got) == 0 || got[0] != "agent_start" {
		t.Fatalf("valid continue should start with agent_start: %v", got)
	}
}

// --- helpers ---

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countStr(list []string, s string) int {
	n := 0
	for _, e := range list {
		if e == s {
			n++
		}
	}
	return n
}

func filterEnds(names []string) []string {
	var out []string
	for _, n := range names {
		if len(n) >= len("tool_execution_end") && n[:len("tool_execution_end")] == "tool_execution_end" {
			out = append(out, n)
		}
	}
	return out
}

func filterStarts(names []string) []string {
	var out []string
	for _, n := range names {
		if len(n) >= len("tool_execution_start") && n[:len("tool_execution_start")] == "tool_execution_start" {
			out = append(out, n)
		}
	}
	return out
}

func filterToolResultStarts(_ *EventStream[AgentEvent, []AgentMessage], names []string) []string {
	var out []string
	for _, n := range names {
		if n == "message_start(toolResult)" {
			out = append(out, n)
		}
	}
	return out
}

func mustContainInOrder(t *testing.T, names []string, seq ...string) {
	t.Helper()
	i := 0
	for _, n := range names {
		if i < len(seq) && n == seq[i] {
			i++
		}
	}
	if i != len(seq) {
		t.Fatalf("expected subsequence %v in %v (matched %d)", seq, names, i)
	}
}

func waitFor(mu *sync.Mutex, completed *[]string, n int) {
	for {
		mu.Lock()
		c := len(*completed)
		mu.Unlock()
		if c >= n {
			return
		}
	}
}

var _ = fmt.Sprintf
