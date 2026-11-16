// Ported from packages/coding-agent/test/tool-search.test.ts (catalog suite) and
// packages/coding-agent/test/codemode-worker-admission.test.ts (pi v1.5.2).

package codemodeext_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/codemodeext"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tcaStrPtr(s string) *string       { return &s }
func tcaFloat64Ptr(v float64) *float64 { return &v }

func tcaSimpleTool(name, description string) *agent.AgentTool {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("query", typebox.String())
	return &agent.AgentTool{
		Name:        name,
		Label:       name,
		Description: description,
		Parameters:  typebox.Object(props),
		Execute: func(context.Context, string, any, agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{}, nil
		},
	}
}

func TestCodemodeDescriptionCatalog(t *testing.T) {
	plain := tcaSimpleTool("read_notes", "Read notes.")
	github := []*agent.AgentTool{
		tcaSimpleTool("mcp__github__a", "GitHub a."),
		tcaSimpleTool("mcp__github__b", "GitHub b."),
		tcaSimpleTool("mcp__github__c", "GitHub c."),
	}
	docs := []*agent.AgentTool{
		tcaSimpleTool("mcp__docs__search", "Search docs."),
		tcaSimpleTool("mcp__docs__long", strings.Repeat("Long ", 200)),
	}
	all := make([]*agent.AgentTool, 0, 1+len(github)+len(docs))
	all = append(all, plain)
	all = append(all, github...)
	all = append(all, docs...)

	namespaces := make(map[string]*core.ToolNamespace)
	for _, entry := range github {
		namespaces[entry.Name] = &core.ToolNamespace{
			Name:        "mcp__github",
			Description: tcaStrPtr("GitHub server"),
		}
	}
	for _, entry := range docs {
		namespaces[entry.Name] = &core.ToolNamespace{
			Name: "mcp__docs",
		}
	}

	t.Run("lists everything without a budget", func(t *testing.T) {
		desc := codemodeext.CreateCodemodeDescription(all, &codemodeext.CodemodeDescriptionOptions{
			Namespaces: namespaces,
		})
		checks := []string{
			"Nested tools:",
			"## mcp__github\nGitHub server",
			"## mcp__docs\n\n### `mcp__docs",
			"find unlisted tools, such as MCP tools",
			"`await searchTools(query, { limit?, namespace? })`",
			"`await describeTool(name)`",
			"`await describeNamespace(name)`",
		}
		for _, want := range checks {
			if !strings.Contains(desc, want) {
				t.Fatalf("expected description to contain %q, got:\n%s", want, desc)
			}
		}
	})

	t.Run("fills the budget round-robin, cheapest first, and says what is missing", func(t *testing.T) {
		desc := codemodeext.CreateCodemodeDescription(all, &codemodeext.CodemodeDescriptionOptions{
			Namespaces:   namespaces,
			InlineBudget: tcaFloat64Ptr(170),
		})
		for _, want := range []string{
			"### `read_notes`",
			"## mcp__docs (some tools not listed)",
			"### `mcp__docs__search`",
			"## mcp__github (some tools not listed)",
			"find unlisted tools, such as MCP tools",
		} {
			if !strings.Contains(desc, want) {
				t.Fatalf("expected description to contain %q, got:\n%s", want, desc)
			}
		}
		if strings.Contains(desc, "### `mcp__docs__long`") {
			t.Fatalf("did not expect long docs tool in budgeted description:\n%s", desc)
		}
		second := codemodeext.CreateCodemodeDescription(all, &codemodeext.CodemodeDescriptionOptions{
			Namespaces:   namespaces,
			InlineBudget: tcaFloat64Ptr(170),
		})
		if desc != second {
			t.Fatalf("description was not deterministic")
		}
	})

	t.Run("leaves deferred tools and their namespaces out entirely", func(t *testing.T) {
		deferred := make(map[string]struct{}, len(github))
		for _, entry := range github {
			deferred[entry.Name] = struct{}{}
		}
		desc := codemodeext.CreateCodemodeDescription(all, &codemodeext.CodemodeDescriptionOptions{
			Namespaces: namespaces,
			Deferred:   deferred,
		})
		if strings.Contains(desc, "mcp__github") {
			t.Fatalf("did not expect deferred github namespace:\n%s", desc)
		}
		withoutGithub := make([]*agent.AgentTool, 0, 1+len(docs))
		withoutGithub = append(withoutGithub, plain)
		withoutGithub = append(withoutGithub, docs...)
		want := codemodeext.CreateCodemodeDescription(withoutGithub, &codemodeext.CodemodeDescriptionOptions{
			Namespaces: namespaces,
		})
		if desc != want {
			t.Fatalf("deferred tools affected description:\ngot:\n%s\nwant:\n%s", desc, want)
		}
	})

	t.Run("leaves namespace instructions out", func(t *testing.T) {
		withInstr := make(map[string]*core.ToolNamespace, len(github))
		for _, entry := range github {
			withInstr[entry.Name] = &core.ToolNamespace{
				Name:         "mcp__github",
				Instructions: tcaStrPtr("Long usage guide."),
			}
		}
		desc := codemodeext.CreateCodemodeDescription(github, &codemodeext.CodemodeDescriptionOptions{
			Namespaces: withInstr,
		})
		if !strings.Contains(desc, "## mcp__github\n\n### `mcp__github__a`") {
			t.Fatalf("unexpected description:\n%s", desc)
		}
		if strings.Contains(desc, "Long usage guide.") {
			t.Fatalf("instructions should not be included in description:\n%s", desc)
		}
	})

	t.Run("lists only namespaces with a zero budget", func(t *testing.T) {
		desc := codemodeext.CreateCodemodeDescription(all, &codemodeext.CodemodeDescriptionOptions{
			Namespaces:   namespaces,
			InlineBudget: tcaFloat64Ptr(0),
		})
		if !strings.Contains(desc, "## mcp__docs (tools not listed)") {
			t.Fatalf("expected unlisted namespace marker:\n%s", desc)
		}
		if strings.Contains(desc, "codemode tool declaration:") {
			t.Fatalf("did not expect tool declarations with zero budget:\n%s", desc)
		}
	})
}

type tcaHoldToolContext struct {
	core.ExtensionContext
	tools       []*agent.AgentTool
	enteredCh   chan chan struct{}
	activeCount *atomic.Int32
	maxActive   *atomic.Int32
	mu          sync.Mutex
	releaseAll  []chan struct{}
}

func tcaNewHoldToolContext() *tcaHoldToolContext {
	var active atomic.Int32
	var maxAct atomic.Int32
	h := &tcaHoldToolContext{
		enteredCh:   make(chan chan struct{}, 16),
		activeCount: &active,
		maxActive:   &maxAct,
	}
	h.tools = []*agent.AgentTool{
		{
			Name:        "hold",
			Label:       "Hold",
			Description: "Block until released by test.",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(context.Context, string, any, agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{}, nil
			},
		},
	}
	return h
}

func (h *tcaHoldToolContext) SessionManager() core.ReadonlySessionManager { return nil }
func (h *tcaHoldToolContext) ModelRegistry() *core.ModelRegistry          { return nil }
func (h *tcaHoldToolContext) Tools() []*agent.AgentTool                   { return h.tools }
func (h *tcaHoldToolContext) ExecuteTool(
	ctx context.Context,
	name string,
	args any,
	_ ...*core.ExecuteToolOptions,
) (agent.AgentToolCallOutcome, error) {
	if name != "hold" {
		return agent.AgentToolCallOutcome{IsError: true}, fmt.Errorf("unknown tool %s", name)
	}
	cur := h.activeCount.Add(1)
	for {
		prev := h.maxActive.Load()
		if cur <= prev || h.maxActive.CompareAndSwap(prev, cur) {
			break
		}
	}
	defer h.activeCount.Add(-1)

	rel := make(chan struct{})
	h.mu.Lock()
	h.releaseAll = append(h.releaseAll, rel)
	h.mu.Unlock()
	h.enteredCh <- rel

	select {
	case <-rel:
		return agent.AgentToolCallOutcome{
			ToolCall: &agent.AgentToolCall{Id: "hold-call", Name: name},
			Result:   agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "released"}}},
		}, nil
	case <-ctx.Done():
		return agent.AgentToolCallOutcome{
			ToolCall: &agent.AgentToolCall{Id: "hold-call", Name: name},
			IsError:  true,
		}, ctx.Err()
	}
}

func (h *tcaHoldToolContext) waitEntered(t *testing.T) chan struct{} {
	t.Helper()
	select {
	case rel := <-h.enteredCh:
		return rel
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for held codemode worker to start")
		return nil
	}
}

func (h *tcaHoldToolContext) cleanup() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.releaseAll {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func tcaResultTexts(res agent.AgentToolResult) string {
	var parts []string
	for _, block := range res.Content {
		if txt, ok := block.(*ai.TextContent); ok && txt != nil {
			parts = append(parts, txt.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func tcaRunAsync(ctx context.Context, code string, ectx core.ExtensionToolContext) <-chan agent.AgentToolResult {
	ch := make(chan agent.AgentToolResult, 1)
	go func() {
		res, err := codemodeext.ExecuteCodemode(ctx, "worker-admission", codemodeext.CodemodeToolInput{Code: code}, nil, ectx)
		if err != nil {
			isErr := true
			ch <- agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: err.Error()}},
				IsError: &isErr,
			}
			return
		}
		ch <- res
	}()
	return ch
}

func TestCodemodeWorkerAdmission(t *testing.T) {
	t.Run("bounds workers, queues waiters, aborts and times out unstarted scripts", func(t *testing.T) {
		ctx := tcaNewHoldToolContext()
		var wg sync.WaitGroup
		defer func() {
			ctx.cleanup()
			wg.Wait()
		}()

		// Hold both default worker slots cleanly across two in-flight scripts.
		held := make([]chan struct{}, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = codemodeext.ExecuteCodemode(context.Background(), fmt.Sprintf("hold-%d", i), codemodeext.CodemodeToolInput{
					Code: `await tools.hold({}); return "ok";`,
				}, nil, ctx)
			}()
			held[i] = ctx.waitEntered(t)
		}

		// A queued script waits while both workers are busy.
		var waitingSettled atomic.Bool
		waitingDone := make(chan agent.AgentToolResult, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := codemodeext.ExecuteCodemode(context.Background(), "waiting", codemodeext.CodemodeToolInput{Code: "return 3"}, nil, nil)
			waitingSettled.Store(true)
			waitingDone <- res
		}()

		// Aborting a queued script fails before starting a sandbox worker.
		abortCtx, abortCancel := context.WithCancel(context.Background())
		abortedCh := tcaRunAsync(abortCtx, "while (true) {}", nil)
		time.Sleep(50 * time.Millisecond)
		abortCancel()
		abortedRes := <-abortedCh
		if abortedRes.IsError == nil || !*abortedRes.IsError {
			t.Fatalf("expected aborted queued script error, got %+v", abortedRes)
		}
		abortedText := tcaResultTexts(abortedRes)
		if !strings.Contains(abortedText, "Script aborted:") || !strings.Contains(abortedText, "The script did not start.") {
			t.Fatalf("unexpected aborted message:\n%s", abortedText)
		}

		// Timing out while waiting fails with worker timeout before starting a sandbox worker.
		timedOutRes, err := codemodeext.ExecuteCodemode(context.Background(), "timeout-wait", codemodeext.CodemodeToolInput{
			Code: `// @options: {"timeout_ms": 50}` + "\nreturn 1",
		}, nil, nil)
		if err != nil {
			t.Fatalf("ExecuteCodemode error: %v", err)
		}
		if timedOutRes.IsError == nil || !*timedOutRes.IsError {
			t.Fatalf("expected timeout error result, got %+v", timedOutRes)
		}
		timedOutText := tcaResultTexts(timedOutRes)
		if !strings.Contains(timedOutText, "Execution timed out after 50 ms while waiting for a free codemode worker") {
			t.Fatalf("unexpected timeout text:\n%s", timedOutText)
		}

		if waitingSettled.Load() {
			t.Fatal("waiting script settled before a worker slot was freed")
		}

		// Release one held worker and confirm the waiting script runs and returns 3.
		close(held[0])
		select {
		case admitted := <-waitingDone:
			if admitted.IsError != nil && *admitted.IsError {
				t.Fatalf("expected admitted script to succeed, got error: %s", tcaResultTexts(admitted))
			}
			if !strings.Contains(tcaResultTexts(admitted), "3") {
				t.Fatalf("expected admitted script output to contain 3, got %s", tcaResultTexts(admitted))
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for admitted script after releasing worker slot")
		}
		close(held[1])
		wg.Wait()
	})

	t.Run("releases worker slots after timeout, abort, and success", func(t *testing.T) {
		for _, how := range []string{"timeout", "abort", "success"} {
			for i := 0; i < 2; i++ {
				ctx := context.Background()
				var cancel context.CancelFunc
				code := "return 1"
				if how == "timeout" {
					code = `// @options: {"timeout_ms": 100}` + "\nwhile (true) {}"
				} else if how == "abort" {
					ctx, cancel = context.WithCancel(ctx)
					defer cancel()
					code = "while (true) {}"
				}
				doneCh := tcaRunAsync(ctx, code, nil)
				if how == "abort" {
					time.Sleep(60 * time.Millisecond)
					cancel()
				}
				res := <-doneCh
				if how == "success" {
					if res.IsError != nil && *res.IsError {
						t.Fatalf("%s[%d] failed: %s", how, i, tcaResultTexts(res))
					}
				} else {
					if res.IsError == nil || !*res.IsError {
						t.Fatalf("%s[%d] expected error, got %+v", how, i, res)
					}
					wantPrefix := "Script timed out:"
					if how == "abort" {
						wantPrefix = "Script aborted:"
					}
					if !strings.Contains(tcaResultTexts(res), wantPrefix) {
						t.Fatalf("%s[%d] text = %s, want %s", how, i, tcaResultTexts(res), wantPrefix)
					}
				}
			}
		}
		// Verify both slots are still free afterward.
		res, err := codemodeext.ExecuteCodemode(context.Background(), "after", codemodeext.CodemodeToolInput{Code: "return 42"}, nil, nil)
		if err != nil || (res.IsError != nil && *res.IsError) {
			t.Fatalf("worker slots leaked: err=%v res=%+v", err, res)
		}
	})

	t.Run("queues concurrent burst behind two workers and respects PI_CODEMODE_MAX_WORKERS", func(t *testing.T) {
		ctx := tcaNewHoldToolContext()
		var wg sync.WaitGroup
		defer func() {
			ctx.cleanup()
			wg.Wait()
		}()

		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = codemodeext.ExecuteCodemode(context.Background(), fmt.Sprintf("burst-%d", i), codemodeext.CodemodeToolInput{
					Code: `await tools.hold({}); return 1;`,
				}, nil, ctx)
			}()
		}
		bHeld0 := ctx.waitEntered(t)
		bHeld1 := ctx.waitEntered(t)
		time.Sleep(50 * time.Millisecond)
		if act := ctx.activeCount.Load(); act != 2 {
			t.Fatalf("expected 2 concurrent workers under default limit, got %d", act)
		}
		close(bHeld0)
		bHeld2 := ctx.waitEntered(t)
		close(bHeld1)
		bHeld3 := ctx.waitEntered(t)
		close(bHeld2)
		close(bHeld3)
		wg.Wait()
		if maxAct := ctx.maxActive.Load(); maxAct != 2 {
			t.Fatalf("expected max concurrency 2, got %d", maxAct)
		}

		// Raise limit via PI_CODEMODE_MAX_WORKERS=3.
		t.Setenv("PI_CODEMODE_MAX_WORKERS", "3")
		ctx3 := tcaNewHoldToolContext()
		defer ctx3.cleanup()
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = codemodeext.ExecuteCodemode(context.Background(), fmt.Sprintf("max3-%d", i), codemodeext.CodemodeToolInput{
					Code: `await tools.hold({}); return 1;`,
				}, nil, ctx3)
			}()
		}
		held3 := make([]chan struct{}, 3)
		for i := 0; i < 3; i++ {
			held3[i] = ctx3.waitEntered(t)
		}
		time.Sleep(50 * time.Millisecond)
		if ctx3.activeCount.Load() != 3 {
			t.Fatalf("expected 3 concurrent workers under PI_CODEMODE_MAX_WORKERS=3, got %d", ctx3.activeCount.Load())
		}
		close(held3[0])
		held4 := ctx3.waitEntered(t)
		close(held3[1])
		close(held3[2])
		close(held4)
		wg.Wait()

		// Invalid PI_CODEMODE_MAX_WORKERS falls back to default 2.
		t.Setenv("PI_CODEMODE_MAX_WORKERS", "0")
		ctxInv := tcaNewHoldToolContext()
		defer ctxInv.cleanup()
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = codemodeext.ExecuteCodemode(context.Background(), fmt.Sprintf("inv-%d", i), codemodeext.CodemodeToolInput{
					Code: `await tools.hold({}); return 1;`,
				}, nil, ctxInv)
			}()
		}
		invHeld := make([]chan struct{}, 2)
		for i := 0; i < 2; i++ {
			invHeld[i] = ctxInv.waitEntered(t)
		}
		time.Sleep(50 * time.Millisecond)
		if ctxInv.activeCount.Load() != 2 {
			t.Fatalf("expected fallback to 2 workers when PI_CODEMODE_MAX_WORKERS=0, got %d", ctxInv.activeCount.Load())
		}
		close(invHeld[0])
		inv3 := ctxInv.waitEntered(t)
		close(invHeld[1])
		close(inv3)
		wg.Wait()
	})
}
