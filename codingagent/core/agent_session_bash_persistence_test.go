package core_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tasbpIntPtr(v int) *int { return &v }

func TestAgentSessionBashAndPersistence(t *testing.T) {
	ctx := context.Background()

	t.Run("records bash results immediately while idle", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		h.Session.RecordBashResult("echo idle", core.BashResult{
			Output:   "idle\n",
			ExitCode: tasbpIntPtr(0),
		})

		msgs := h.Session.Messages()
		var found *core.BashExecutionMessage
		for _, m := range msgs {
			if bm, ok := m.(*core.BashExecutionMessage); ok {
				found = bm
			}
		}
		if found == nil || found.Command != "echo idle" || found.Output != "idle\n" {
			t.Fatalf("expected recorded bash message, got %+v", found)
		}
		if h.Session.HasPendingBashMessages() {
			t.Fatalf("expected no pending bash messages after idle RecordBashResult")
		}
	})

	t.Run("defers bash results while streaming and flushes them before the next prompt", func(t *testing.T) {
		toolStarted := make(chan struct{})
		releaseTool := make(chan struct{})
		waitTool := &agent.AgentTool{
			Name:        "wait",
			Label:       "Wait",
			Description: "Wait for release",
			Parameters:  typebox.Object(nil),
			Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				close(toolStarted)
				<-releaseTool
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "released"}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{waitTool},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		var nextPromptSawBash bool
		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlock{Block: providers.FauxToolCall("wait", jsonx.NewObject(), nil)}, &providers.FauxAssistantMessageOptions{
					StopReason: &stopTool,
				}),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("first done"), nil),
			},
			providers.FauxResponseFactory(func(reqCtx context.Context, tc *ai.TranscriptContext, opts *ai.SimpleStreamOptions, state *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "deferred output") {
						nextPromptSawBash = true
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("second done"), nil), nil
			}),
		})

		promptDone := make(chan error, 1)
		go func() {
			promptDone <- h.Session.Prompt(ctx, "run wait")
		}()

		<-toolStarted
		h.Session.RecordBashResult("echo deferred", core.BashResult{
			Output:   "deferred output\n",
			ExitCode: tasbpIntPtr(0),
		})
		if !h.Session.HasPendingBashMessages() {
			t.Fatalf("expected pending bash message while streaming")
		}

		close(releaseTool)
		if err := <-promptDone; err != nil {
			t.Fatalf("first Prompt failed: %v", err)
		}

		// Trigger next prompt; deferred bash message should flush into session history before LLM sees it.
		if err := h.Session.Prompt(ctx, "second prompt"); err != nil {
			t.Fatalf("second Prompt failed: %v", err)
		}
		if h.Session.HasPendingBashMessages() {
			t.Fatalf("expected pending bash messages cleared on next prompt")
		}
		if !nextPromptSawBash {
			t.Fatalf("expected flushed bash result in next prompt transcript")
		}
	})

	t.Run("executes bash commands and records the result", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		res, err := h.Session.ExecuteBash(ctx, "printf 'hello world\\n'", nil)
		if err != nil {
			t.Fatalf("ExecuteBash failed: %v", err)
		}
		if res.ExitCode == nil || *res.ExitCode != 0 || res.Output != "hello world\n" {
			t.Fatalf("unexpected BashResult: %+v", res)
		}
		if h.Session.IsBashRunning() {
			t.Fatalf("expected IsBashRunning=false after ExecuteBash completed")
		}
	})

	t.Run("cancels running bash commands with abortBash", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		started := make(chan struct{})
		ops := core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
			close(started)
			<-execCtx.Done()
			return core.BashOperationsResult{}, execCtx.Err()
		})

		done := make(chan struct {
			res core.BashResult
			err error
		}, 1)
		go func() {
			res, err := h.Session.ExecuteBash(ctx, "sleep 10", nil, &core.ExecuteBashOptions{Operations: ops})
			done <- struct {
				res core.BashResult
				err error
			}{res, err}
		}()

		<-started
		if !h.Session.IsBashRunning() {
			t.Fatalf("expected IsBashRunning=true while command running")
		}
		h.Session.AbortBash()

		got := <-done
		if got.err == nil && !got.res.Cancelled {
			t.Fatalf("expected aborted bash execution error or Cancelled=true, got res=%+v err=%v", got.res, got.err)
		}
		if h.Session.IsBashRunning() {
			t.Fatalf("expected IsBashRunning=false after abortBash")
		}
	})

	t.Run("keeps newer bash execution tracked when an older execution finishes", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		secondStarted := make(chan struct{})
		releaseSecond := make(chan struct{})

		ops1 := core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
			close(firstStarted)
			<-releaseFirst
			return core.BashOperationsResult{ExitCode: tasbpIntPtr(0)}, nil
		})
		ops2 := core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
			close(secondStarted)
			select {
			case <-releaseSecond:
				return core.BashOperationsResult{ExitCode: tasbpIntPtr(0)}, nil
			case <-execCtx.Done():
				return core.BashOperationsResult{}, execCtx.Err()
			}
		})

		done1 := make(chan error, 1)
		done2 := make(chan error, 1)
		go func() {
			_, err := h.Session.ExecuteBash(ctx, "first", nil, &core.ExecuteBashOptions{Operations: ops1})
			done1 <- err
		}()
		<-firstStarted
		go func() {
			_, err := h.Session.ExecuteBash(ctx, "second", nil, &core.ExecuteBashOptions{Operations: ops2})
			done2 <- err
		}()
		<-secondStarted

		close(releaseFirst)
		if err := <-done1; err != nil {
			t.Fatalf("first bash failed: %v", err)
		}
		if !h.Session.IsBashRunning() {
			t.Fatalf("expected IsBashRunning to stay true while second bash runs")
		}

		close(releaseSecond)
		if err := <-done2; err != nil {
			t.Fatalf("second bash failed: %v", err)
		}
		if h.Session.IsBashRunning() {
			t.Fatalf("expected IsBashRunning=false after both executions finish")
		}
	})

	t.Run("aborts all active bash executions", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		started1 := make(chan struct{})
		started2 := make(chan struct{})
		makeBlockingOps := func(sig chan struct{}) core.BashOperations {
			return core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
				close(sig)
				<-execCtx.Done()
				return core.BashOperationsResult{}, execCtx.Err()
			})
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = h.Session.ExecuteBash(ctx, "cmd1", nil, &core.ExecuteBashOptions{Operations: makeBlockingOps(started1)})
		}()
		go func() {
			defer wg.Done()
			_, _ = h.Session.ExecuteBash(ctx, "cmd2", nil, &core.ExecuteBashOptions{Operations: makeBlockingOps(started2)})
		}()

		<-started1
		<-started2
		h.Session.AbortBash()
		wg.Wait()

		if h.Session.IsBashRunning() {
			t.Fatalf("expected all bash executions aborted and IsBashRunning=false")
		}
	})

	t.Run("persists user, assistant, toolResult, and custom messages in order", func(t *testing.T) {
		echoTool := &agent.AgentTool{
			Name:        "echo",
			Label:       "Echo",
			Description: "Echo text",
			Parameters:  typebox.Object(nil),
			Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "echoed"}},
				}, nil
			},
		}
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{echoTool},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantBlock{Block: providers.FauxToolCall("echo", jsonx.NewObject(), nil)}, &providers.FauxAssistantMessageOptions{
					StopReason: &stopTool,
				}),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("tool finished"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "run echo"); err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}
		if err := h.Session.SendCustomMessage(ctx, "note", "custom note", true, nil); err != nil {
			t.Fatalf("SendCustomMessage failed: %v", err)
		}

		var entryKinds []string
		for _, e := range h.SessionManager.GetEntries() {
			switch entry := e.(type) {
			case *core.SessionMessageEntry:
				entryKinds = append(entryKinds, entry.Message.GetRole())
			case *core.CustomMessageEntry:
				entryKinds = append(entryKinds, "custom:"+entry.CustomType)
			}
		}
		want := []string{"system", "user", "assistant", "toolResult", "assistant", "custom:note"}
		if strings.Join(entryKinds, ",") != strings.Join(want, ",") {
			t.Fatalf("expected persisted sequence %v, got %v", want, entryKinds)
		}
	})

	t.Run("does not emit message_end for bash execution messages", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		h.Session.RecordBashResult("echo test", core.BashResult{Output: "test\n", ExitCode: tasbpIntPtr(0)})
		for _, ev := range h.EventsOfType("message_end") {
			me := ev.(*core.AgentSessionEventMessageEnd)
			if me.Message.GetRole() == "bashExecution" {
				t.Fatalf("unexpected message_end emitted for bashExecution")
			}
		}
	})

	t.Run("persists aborted assistant messages", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		sawUpdate := make(chan struct{})
		var once sync.Once
		h.Session.Subscribe(func(ev core.AgentSessionEvent) {
			if _, ok := ev.(*core.AgentSessionEventMessageUpdate); ok {
				once.Do(func() { close(sawUpdate) })
			}
		})

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText(strings.Repeat("x", 20_000)), nil),
			},
		})

		done := make(chan error, 1)
		go func() {
			done <- h.Session.Prompt(ctx, "long stream")
		}()

		select {
		case <-sawUpdate:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for message_update")
		}
		if err := h.Session.Abort(ctx); err != nil {
			t.Fatalf("Abort failed: %v", err)
		}
		<-done

		var lastAssistant *ai.AssistantMessage
		for _, e := range h.SessionManager.GetEntries() {
			if sm, ok := e.(*core.SessionMessageEntry); ok {
				if am, ok := sm.Message.(*ai.AssistantMessage); ok {
					lastAssistant = am
				}
			}
		}
		if lastAssistant == nil || lastAssistant.StopReason != ai.StopReasonAborted {
			t.Fatalf("expected persisted aborted assistant message, got %+v", lastAssistant)
		}
	})

	t.Run("records bash output through custom operations", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		ops := core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
			if options.OnData != nil {
				options.OnData([]byte("custom op output\n"))
			}
			return core.BashOperationsResult{ExitCode: tasbpIntPtr(42)}, nil
		})

		res, err := h.Session.ExecuteBash(ctx, "custom-cmd", nil, &core.ExecuteBashOptions{Operations: ops})
		if err != nil {
			t.Fatalf("ExecuteBash failed: %v", err)
		}
		if res.Output != "custom op output\n" || res.ExitCode == nil || *res.ExitCode != 42 {
			t.Fatalf("unexpected BashResult: %+v", res)
		}
	})

	t.Run("streams bash output to the callback and session events", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness failed: %v", err)
		}
		defer h.Cleanup()

		ops := core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
			if options.OnData != nil {
				options.OnData([]byte("chunk1-"))
				options.OnData([]byte("chunk2\n"))
			}
			return core.BashOperationsResult{ExitCode: tasbpIntPtr(0)}, nil
		})

		var callbackChunks []string
		bashId := "bash-1"
		res, err := h.Session.ExecuteBash(ctx, "stream-cmd", func(chunk string) {
			callbackChunks = append(callbackChunks, chunk)
		}, &core.ExecuteBashOptions{Id: &bashId, Operations: ops})
		if err != nil {
			t.Fatalf("ExecuteBash failed: %v", err)
		}
		if res.Output != "chunk1-chunk2\n" {
			t.Fatalf("unexpected output: %q", res.Output)
		}
		if strings.Join(callbackChunks, "") != "chunk1-chunk2\n" {
			t.Fatalf("unexpected callback chunks: %v", callbackChunks)
		}

		var eventChunks []string
		for _, ev := range h.EventsOfType("bash_execution_update") {
			bu := ev.(*core.AgentSessionEventBashExecutionUpdate)
			if bu.Id != nil && *bu.Id == "bash-1" {
				eventChunks = append(eventChunks, bu.Delta)
			}
		}
		if strings.Join(eventChunks, "") != "chunk1-chunk2\n" {
			t.Fatalf("unexpected bash_execution_update chunks: %v", eventChunks)
		}
	})

	t.Run("escape sequences split across output chunks", func(t *testing.T) {
		runChunked := func(t *testing.T, chunks [][]byte) (string, string) {
			t.Helper()
			h, err := testkit.CreateSuiteHarness(ctx)
			if err != nil {
				t.Fatalf("CreateSuiteHarness failed: %v", err)
			}
			defer h.Cleanup()

			ops := core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
				for _, ch := range chunks {
					if options.OnData != nil {
						options.OnData(ch)
					}
				}
				return core.BashOperationsResult{ExitCode: tasbpIntPtr(0)}, nil
			})
			var streamed strings.Builder
			res, err := h.Session.ExecuteBash(ctx, "split-esc", func(chunk string) {
				streamed.WriteString(chunk)
			}, &core.ExecuteBashOptions{Operations: ops})
			if err != nil {
				t.Fatalf("ExecuteBash failed: %v", err)
			}
			return streamed.String(), res.Output
		}

		t.Run("strips a color reset split inside its parameters", func(t *testing.T) {
			streamed, output := runChunked(t, [][]byte{
				[]byte("hello \x1b[38;5;"),
				[]byte("196mworld\x1b[0m"),
			})
			if streamed != "hello world" || output != "hello world" {
				t.Fatalf("expected stripped text 'hello world', got streamed=%q output=%q", streamed, output)
			}
		})

		t.Run("strips a color code split right after ESC", func(t *testing.T) {
			streamed, output := runChunked(t, [][]byte{
				[]byte("a\x1b"),
				[]byte("[31mb"),
			})
			if streamed != "ab" || output != "ab" {
				t.Fatalf("expected 'ab', got streamed=%q output=%q", streamed, output)
			}
		})

		t.Run("strips an OSC sequence split before its terminator", func(t *testing.T) {
			streamed, output := runChunked(t, [][]byte{
				[]byte("before\x1b]0;title"),
				[]byte(" rest\x07after"),
			})
			if streamed != "beforeafter" || output != "beforeafter" {
				t.Fatalf("expected 'beforeafter', got streamed=%q output=%q", streamed, output)
			}
		})

		t.Run("flushes an incomplete multi-byte character at the end of output", func(t *testing.T) {
			streamed, output := runChunked(t, [][]byte{
				[]byte("ok "),
				{0xf0, 0x9f},
			})
			if !strings.HasPrefix(streamed, "ok ") || !strings.HasPrefix(output, "ok ") {
				t.Fatalf("expected flushed incomplete UTF-8 with 'ok ', got streamed=%q output=%q", streamed, output)
			}
		})

		t.Run("does not hold back output behind a long unterminated sequence", func(t *testing.T) {
			h, err := testkit.CreateSuiteHarness(ctx)
			if err != nil {
				t.Fatalf("CreateSuiteHarness failed: %v", err)
			}
			defer h.Cleanup()

			var chunksSeen []string
			firstSeen := make(chan struct{}, 1)
			releaseEnd := make(chan struct{})
			longBody := strings.Repeat("x", 300)
			ops := core.BashOperationsFunc(func(execCtx context.Context, command, cwd string, options core.BashOperationsOptions) (core.BashOperationsResult, error) {
				// Long unterminated OSC (>256 bytes) should flush immediately before command finishes.
				longSeq := "\x1b]" + longBody
				if options.OnData != nil {
					options.OnData([]byte(longSeq))
				}
				<-releaseEnd
				return core.BashOperationsResult{ExitCode: tasbpIntPtr(0)}, nil
			})

			done := make(chan core.BashResult, 1)
			go func() {
				res, err := h.Session.ExecuteBash(ctx, "long-unterminated", func(c string) {
					chunksSeen = append(chunksSeen, c)
					select {
					case firstSeen <- struct{}{}:
					default:
					}
				}, &core.ExecuteBashOptions{Operations: ops})
				if err != nil {
					t.Errorf("ExecuteBash failed: %v", err)
				}
				done <- res
			}()

			select {
			case <-firstSeen:
			case <-time.After(2 * time.Second):
				t.Fatalf("expected long unterminated sequence not to block streaming callback")
			}
			close(releaseEnd)
			<-done
			if got := strings.Join(chunksSeen, ""); got != "]"+longBody {
				t.Fatalf("expected ]%s in streamed chunks, got %q", longBody, got)
			}
		})
	})
}
