package core

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type tntcMockHost struct {
	mu         sync.Mutex
	tools      []*agent.AgentTool
	sequential bool
	events     []NestedToolExecutionEvent
}

func (h *tntcMockHost) GetTools() []*agent.AgentTool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]*agent.AgentTool, len(h.tools))
	copy(cp, h.tools)
	return cp
}

func (h *tntcMockHost) AddTool(tool *agent.AgentTool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools = append(h.tools, tool)
}

func (h *tntcMockHost) IsSequential() bool {
	return h.sequential
}

func (h *tntcMockHost) RunToolCall(
	ctx context.Context,
	toolCall *agent.AgentToolCall,
	_ string,
	onUpdate func(ctx context.Context, partialResult agent.AgentToolResult) error,
) (agent.AgentToolCallOutcome, error) {
	var target *agent.AgentTool
	for _, t := range h.GetTools() {
		if t != nil && t.Name == toolCall.Name {
			target = t
			break
		}
	}
	if target == nil {
		return agent.AgentToolCallOutcome{
			ToolCall: toolCall,
			Result: agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "Tool " + toolCall.Name + " not found"}},
			},
			IsError: true,
		}, nil
	}
	argsObj, _ := toolCall.Arguments.(*jsonx.Object)
	res, err := target.Execute(ctx, toolCall.Id, argsObj, func(partial agent.AgentToolResult) {
		_ = onUpdate(ctx, partial)
	})
	if err != nil {
		return agent.AgentToolCallOutcome{
			ToolCall: toolCall,
			Result: agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: err.Error()}},
			},
			IsError: true,
		}, nil
	}
	return agent.AgentToolCallOutcome{
		ToolCall: toolCall,
		Result:   res,
		IsError:  false,
	}, nil
}

func (h *tntcMockHost) Emit(_ context.Context, event NestedToolExecutionEvent) error {
	h.mu.Lock()
	h.events = append(h.events, event)
	h.mu.Unlock()
	return nil
}

func tntcMakeUsage(input int, cost float64) *ai.Usage {
	return &ai.Usage{
		Input:       input,
		TotalTokens: input,
		Cost:        ai.UsageCost{Input: cost, Total: cost},
	}
}

func TestNestedToolCallRunner_And_Recorder(t *testing.T) {
	t.Run("assigns ids, emits parent events, and consumes record once", func(t *testing.T) {
		host := &tntcMockHost{
			tools: []*agent.AgentTool{{
				Name:       "echo",
				Parameters: typebox.Object(nil),
				Execute: func(_ context.Context, _ string, _ any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
					onUpdate(agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "partial"}}})
					return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}}, nil
				},
			}},
		}
		runner := NewNestedToolCallRunner(host)
		var updates []agent.AgentToolResult

		first, err := runner.Execute(context.Background(), "call", "echo", jsonx.ObjectOf("a", 1), &NestedToolCallOptions{
			OnUpdate: func(p agent.AgentToolResult) { updates = append(updates, p) },
		})
		if err != nil || first.ToolCall.Id != "call/1" {
			t.Fatalf("unexpected first: (%+v, %v)", first, err)
		}
		missing, err := runner.Execute(context.Background(), "call", "missing", nil)
		if err != nil || missing.ToolCall.Id != "call/2" || !missing.IsError {
			t.Fatalf("unexpected missing: (%+v, %v)", missing, err)
		}
		if len(updates) != 1 {
			t.Fatalf("updates=%d, want 1", len(updates))
		}
		if len(host.events) != 5 {
			t.Fatalf("events=%d, want 5", len(host.events))
		}
		rec := runner.TakeRecord("call")
		if rec == nil || rec.Calls == nil || !rec.Calls.Complete || len(rec.Calls.Calls) != 2 {
			t.Fatalf("unexpected record: %+v", rec)
		}
		if rec.Calls.Calls[0].Id != "call/1" || rec.Calls.Calls[0].Status != "ok" {
			t.Fatalf("unexpected first call record: %+v", rec.Calls.Calls[0])
		}
		if rec.Calls.Calls[1].Id != "call/2" || rec.Calls.Calls[1].Status != "error" || rec.Calls.Calls[1].Error == nil || *rec.Calls.Calls[1].Error != "Tool missing not found" {
			t.Fatalf("unexpected second call record: %+v", rec.Calls.Calls[1])
		}
		if runner.TakeRecord("call") != nil || runner.TakeRecord("other") != nil {
			t.Fatalf("expected TakeRecord to consume record once")
		}
	})

	t.Run("records nested calls and sums recursive usage", func(t *testing.T) {
		host := &tntcMockHost{}
		runner := NewNestedToolCallRunner(host)

		host.AddTool(&agent.AgentTool{
			Name:       "leaf",
			Parameters: typebox.Object(nil),
			Execute: func(context.Context, string, any, agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Usage: tntcMakeUsage(10, 0.01)}, nil
			},
		})
		host.AddTool(&agent.AgentTool{
			Name:       "plain",
			Parameters: typebox.Object(nil),
			Execute: func(context.Context, string, any, agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{}, nil
			},
		})
		host.AddTool(&agent.AgentTool{
			Name:       "middle",
			Parameters: typebox.Object(nil),
			Execute: func(ctx context.Context, toolCallId string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				if _, err := runner.Execute(ctx, toolCallId, "leaf", jsonx.NewObject()); err != nil {
					return agent.AgentToolResult{}, err
				}
				return agent.AgentToolResult{Usage: tntcMakeUsage(5, 0.005)}, nil
			},
		})

		_, _ = runner.Execute(context.Background(), "call", "middle", jsonx.NewObject())
		_, _ = runner.Execute(context.Background(), "call", "leaf", jsonx.NewObject())
		_, _ = runner.Execute(context.Background(), "call", "plain", jsonx.NewObject())
		_, _ = runner.Execute(context.Background(), "free", "plain", jsonx.NewObject())

		summary := runner.TakeRecord("call")
		if summary == nil || summary.Usage == nil || summary.Usage.Input != 25 {
			t.Fatalf("unexpected summary usage: %+v", summary)
		}
		var ids []string
		for _, c := range summary.Calls.Calls {
			ids = append(ids, c.Id)
		}
		if !slices.Equal(ids, []string{"call/1", "call/1/1", "call/2", "call/3"}) {
			t.Fatalf("ids=%v", ids)
		}
		freeSummary := runner.TakeRecord("free")
		if freeSummary == nil || freeSummary.Usage != nil || !freeSummary.Calls.Complete {
			t.Fatalf("unexpected freeSummary: %+v", freeSummary)
		}
	})

	t.Run("serializes concurrent calls to sequential tools", func(t *testing.T) {
		var seqActive, parActive, seqMax, parMax atomic.Int32
		host := &tntcMockHost{
			tools: []*agent.AgentTool{
				{
					Name:          "sequential",
					Parameters:    typebox.Object(nil),
					ExecutionMode: agent.ToolExecutionModeSequential,
					Execute: func(context.Context, string, any, agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
						cur := seqActive.Add(1)
						for {
							prev := seqMax.Load()
							if cur <= prev || seqMax.CompareAndSwap(prev, cur) {
								break
							}
						}
						time.Sleep(10 * time.Millisecond)
						seqActive.Add(-1)
						return agent.AgentToolResult{}, nil
					},
				},
				{
					Name:       "parallel",
					Parameters: typebox.Object(nil),
					Execute: func(context.Context, string, any, agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
						cur := parActive.Add(1)
						for {
							prev := parMax.Load()
							if cur <= prev || parMax.CompareAndSwap(prev, cur) {
								break
							}
						}
						time.Sleep(30 * time.Millisecond)
						parActive.Add(-1)
						return agent.AgentToolResult{}, nil
					},
				},
			},
		}
		runner := NewNestedToolCallRunner(host)

		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = runner.Execute(context.Background(), "call", "sequential", jsonx.NewObject())
			}()
		}
		wg.Wait()

		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = runner.Execute(context.Background(), "call", "parallel", jsonx.NewObject())
			}()
		}
		wg.Wait()

		if seqMax.Load() != 1 {
			t.Fatalf("seqMax=%d, want 1", seqMax.Load())
		}
		if parMax.Load() < 2 {
			t.Fatalf("parMax=%d, want >=2", parMax.Load())
		}
	})

	t.Run("NestedCallRecorder enforces per-call, total, error, and count limits", func(t *testing.T) {
		rec := NewNestedCallRecorder()
		if rec.Snapshot() != nil {
			t.Fatalf("expected nil snapshot on empty recorder")
		}

		makeCall := func(id string, args *jsonx.Object) *agent.AgentToolCall {
			return &agent.AgentToolCall{Type: "toolCall", Id: id, Name: "t", Arguments: args}
		}

		small := rec.Start(makeCall("a", jsonx.ObjectOf("x", 1)))
		rec.Finish(small, false, "")
		snap := rec.Snapshot()
		if snap == nil || !snap.Complete || len(snap.Calls) != 1 || snap.Calls[0].Status != "ok" {
			t.Fatalf("unexpected small snapshot: %+v", snap)
		}

		big := rec.Start(makeCall("b", jsonx.ObjectOf("text", strings.Repeat("x", NestedCallLimits.MaxArgumentBytesPerCall))))
		rec.Finish(big, true, strings.Repeat("e", 1000))
		snap = rec.Snapshot()
		if snap.Complete || len(snap.Calls) != 2 || snap.Calls[1].Arguments != nil || snap.Calls[1].ArgumentsBytes == nil ||
			*snap.Calls[1].ArgumentsBytes <= NestedCallLimits.MaxArgumentBytesPerCall ||
			snap.Calls[1].Error == nil || len(*snap.Calls[1].Error) != NestedCallLimits.MaxErrorChars {
			t.Fatalf("unexpected big call snapshot: %+v", snap.Calls[1])
		}

		for i := 0; i < NestedCallLimits.MaxCalls; i++ {
			r := rec.Start(makeCall("c", jsonx.NewObject()))
			rec.Finish(r, false, "")
		}
		if got := len(rec.Snapshot().Calls); got != NestedCallLimits.MaxCalls {
			t.Fatalf("calls=%d, want %d", got, NestedCallLimits.MaxCalls)
		}

		totalRec := NewNestedCallRecorder()
		chunk := jsonx.ObjectOf("text", strings.Repeat("x", 7000))
		for i := 0; i < 6; i++ {
			totalRec.Start(makeCall("u", chunk))
		}
		totalSnap := totalRec.Snapshot()
		withArgs := 0
		for _, entry := range totalSnap.Calls {
			if entry.Arguments != nil {
				withArgs++
			}
			if entry.Status != "unfinished" {
				t.Fatalf("expected unfinished, got %q", entry.Status)
			}
		}
		if withArgs != 4 || totalSnap.Complete {
			t.Fatalf("withArgs=%d complete=%v", withArgs, totalSnap.Complete)
		}
	})
}
