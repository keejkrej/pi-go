// End-to-end headless virtual-terminal integration tests for InteractiveMode
// (vt terminal + tui.TUI + core.AgentSession + FauxProvider).

package interactive

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

func timiWaitViewportOrTranscript(t *testing.T, mf *tithModeFixture, substr string) {
	t.Helper()
	tithWaitUntil(t, 5*time.Second, "terminal scrollback or transcript contains "+substr, func() bool {
		if strings.Contains(mf.Term.tithScrollBufferText(), substr) {
			return true
		}
		mf.Mode.mu.Lock()
		inited := mf.Mode.isInitialized
		mf.Mode.mu.Unlock()
		if inited && mf.Mode.Ui() != nil {
			return strings.Contains(tithRenderOnUI(t, mf.Mode.Ui(), mf.Mode.chatContainer, 100), substr)
		}
		return false
	})
}

func TestInteractiveRunPromptToolCallSlashBashModeSwitchAndShutdown(t *testing.T) {
	// Register a tiny custom tool executed by the faux model turn.
	echoParams := typebox.Object(nil)
	var toolCalls atomic.Int32
	echoTool := &agent.AgentTool{
		Name:        "timi_echo",
		Label:       "timi_echo",
		Description: "Echo test helper",
		Parameters:  echoParams,
		Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			toolCalls.Add(1)
			return agent.AgentToolResult{
				Content: ai.ContentList{
					&ai.TextContent{Type: "text", Text: "tool-echoed-ok"},
				},
			}, nil
		},
	}

	fx := tithNewRuntimeFixture(t, &tithRuntimeOptions{
		Tools: []*agent.AgentTool{echoTool},
	})
	term := tithNewVirtualTerminal(110, 32)
	t.Cleanup(term.tithClose)

	mode := NewInteractiveMode(fx.Runtime, &InteractiveModeOptions{
		Terminal:              term,
		InitialMessage:        tithStrPtr("startup ping"),
		InitialMessages:       []string{"queued startup follow"},
		DisableVersionCheck:   true,
		DisableTmuxCheck:      true,
		IsInteractiveTerminal: func() bool { return true },
	})
	mf := &tithModeFixture{tithRuntimeFixture: fx, Term: term, Mode: mode}

	stopTool := ai.StopReasonToolUse
	// Queue responses: startup ping, queued startup follow, then interactive tool call + final text.
	fx.Faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("startup pong"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("follow pong"), nil)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlock{Block: providers.FauxToolCall("timi_echo", jsonx.ObjectOf("msg", "hi"), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
		)},
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("tool run finished cleanly"), nil)},
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() {
		runDone <- mode.Run(runCtx)
	}()

	// Startup messages execute automatically during Run().
	timiWaitViewportOrTranscript(t, mf, "startup pong")
	timiWaitViewportOrTranscript(t, mf, "follow pong")
	tithWaitUntil(t, 3*time.Second, "startup turns finished streaming", func() bool {
		return !mode.Session().IsStreaming()
	})

	// Send interactive prompt + Enter via the virtual terminal input callback.
	term.tithTypeAndSubmit("please run tool")
	timiWaitViewportOrTranscript(t, mf, "tool run finished cleanly")
	tithWaitUntil(t, 3*time.Second, "tool prompt turn settled", func() bool {
		return !mode.Session().IsStreaming()
	})
	if got := toolCalls.Load(); got != 1 {
		t.Fatalf("expected 1 custom tool call execution, got %d", got)
	}

	// Execute live interactive `!` bash command via editor input.
	term.tithTypeAndSubmit("!printf 'BASH_INTEGRATION_OK'")
	timiWaitViewportOrTranscript(t, mf, "BASH_INTEGRATION_OK")
	tithWaitUntil(t, 3*time.Second, "bash command finished", func() bool {
		mode.mu.Lock()
		active := mode.bashComponent != nil
		mode.mu.Unlock()
		return !active && !mode.Session().IsBashRunning()
	})

	// Execute slash command `/name integration-e2e` live via terminal input.
	term.tithTypeAndSubmit("/name integration-e2e")
	tithWaitUntil(t, 3*time.Second, "session renamed via /name", func() bool {
		n := mode.imcCachedSessionName()
		return n != nil && *n == "integration-e2e" && mode.imcIsFooterSafeToReadSession()
	})
	tithDrainUI(t, mode)
	if n := mode.Session().SessionManager.GetSessionName(); n == nil || *n != "integration-e2e" {
		t.Fatalf("expected persisted session name integration-e2e, got %v", n)
	}

	// Switch TUI mode regular -> fullscreen -> regular seamlessly without losing transcript.
	_ = mode.imCallUI(context.Background(), func() {
		mode.SwitchTuiMode(tui.TuiModeFullscreen)
	})
	if mode.Ui().Mode() != tui.TuiModeFullscreen {
		t.Fatalf("expected fullscreen TUI mode after SwitchTuiMode")
	}
	tithWaitUntil(t, 3*time.Second, "fullscreen viewport renders transcript", func() bool {
		return strings.Contains(term.tithViewportText(), "tool run finished cleanly") ||
			strings.Contains(tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100), "tool run finished cleanly")
	})

	_ = mode.imCallUI(context.Background(), func() {
		mode.SwitchTuiMode(tui.TuiModeRegular)
	})
	if mode.Ui().Mode() != tui.TuiModeRegular {
		t.Fatalf("expected regular TUI mode restored")
	}

	// Verify ProgramStatusReporter emitted done/working transitions.
	statuses := term.tithProgramStatuses()
	var sawWorking, sawDone bool
	for _, st := range statuses {
		if st.State == tui.ProgramStatusStateWorking {
			sawWorking = true
		}
		if st.State == tui.ProgramStatusStateDone {
			sawDone = true
		}
	}
	if !sawWorking || !sawDone {
		t.Fatalf("expected working and done OSC 7501 status updates, got %+v", statuses)
	}

	// Cancel Run context and verify clean Stop() exit.
	cancelRun()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("InteractiveMode.Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("InteractiveMode.Run did not exit on cancel")
	}
}

func TestRunInteractiveModeAndCtrlDShutdownExitHook(t *testing.T) {
	origExitHook := utils.ConfigExitHook
	var exitCode atomic.Int32
	exitCalled := make(chan struct{}, 1)
	utils.ConfigExitHook = func(code int) {
		exitCode.Store(int32(code))
		select {
		case exitCalled <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() {
		utils.ConfigExitHook = origExitHook
	})

	fx := tithNewRuntimeFixture(t, nil)
	term := tithNewVirtualTerminal(90, 25)
	t.Cleanup(term.tithClose)

	var modeRef atomic.Pointer[InteractiveMode]
	runDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Exercise exported RunInteractiveMode entrypoint cleanly.
	mode := NewInteractiveMode(fx.Runtime, &InteractiveModeOptions{
		Terminal:              term,
		DisableVersionCheck:   true,
		DisableTmuxCheck:      true,
		IsInteractiveTerminal: func() bool { return true },
	})
	modeRef.Store(mode)
	go func() {
		runDone <- mode.Run(ctx)
	}()

	tithWaitUntil(t, 3*time.Second, "mode initialized and started", func() bool {
		mode.mu.Lock()
		inited := mode.isInitialized
		mode.mu.Unlock()
		return mode.Ui() != nil && inited
	})
	// Wait until Run() passes startup notices and enters wait loop cleanly.
	tithDrainUI(t, mode)

	// Ctrl+D on empty editor requests clean Run() exit via OnCtrlD -> imcRequestShutdown.
	term.tithSendInput("\x04")

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Run did not exit after Ctrl+D")
	}

	// Explicitly exercise imlcShutdown(0) to verify ConfigExitHook invocation & cleanup.
	mode.imlcShutdown(0)
	select {
	case <-exitCalled:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected imlcShutdown(0) to invoke ConfigExitHook")
	}
	if got := exitCode.Load(); got != 0 {
		t.Fatalf("expected exit code 0, got %d", got)
	}

	// Verify RunInteractiveMode convenience helper initializes & exits cleanly on canceled context.
	fx2 := tithNewRuntimeFixture(t, nil)
	term2 := tithNewVirtualTerminal(80, 24)
	t.Cleanup(term2.tithClose)
	canceledCtx, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if err := RunInteractiveMode(canceledCtx, fx2.Runtime, &InteractiveModeOptions{
		Terminal:              term2,
		DisableVersionCheck:   true,
		DisableTmuxCheck:      true,
		IsInteractiveTerminal: func() bool { return true },
	}); err != nil {
		t.Fatalf("RunInteractiveMode(canceledCtx): %v", err)
	}
}

func TestInteractiveCtrlCClearsEditorAndDoubleCtrlCQuits(t *testing.T) {
	fx := tithNewRuntimeFixture(t, nil)
	fx.Faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("hi back"), nil)},
	})

	term := tithNewVirtualTerminal(100, 28)
	t.Cleanup(term.tithClose)
	var stdoutBuf bytes.Buffer

	mode := NewInteractiveMode(fx.Runtime, &InteractiveModeOptions{
		Terminal:              term,
		Stdout:                &stdoutBuf,
		DisableVersionCheck:   true,
		DisableTmuxCheck:      true,
		IsInteractiveTerminal: func() bool { return true },
	})
	mf := &tithModeFixture{tithRuntimeFixture: fx, Term: term, Mode: mode}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		runDone <- mode.Run(ctx)
	}()

	tithWaitUntil(t, 3*time.Second, "mode initialized", func() bool {
		mode.mu.Lock()
		inited := mode.isInitialized
		mode.mu.Unlock()
		return mode.Ui() != nil && inited
	})
	tithDrainUI(t, mode)

	// Welcome header should show resolved app.* key hints (tui.SetKeybindings installed).
	headerRendered := utils.StripAnsi(tithRenderOnUI(t, mode.Ui(), mode.headerContainer, 100))
	if !strings.Contains(strings.ToLower(headerRendered), "ctrl+c") {
		t.Fatalf("expected welcome header to contain ctrl+c key hint, got:\n%s", headerRendered)
	}

	// Execute one prompt turn so persisted session gets a message (exercising resume command on exit).
	term.tithTypeAndSubmit("hello")
	timiWaitViewportOrTranscript(t, mf, "hi back")
	tithWaitUntil(t, 3*time.Second, "turn finished", func() bool {
		return !mode.Session().IsStreaming()
	})
	tithDrainUI(t, mode)

	// First Ctrl+C clears non-empty bash-mode draft without exiting.
	term.tithSendInput("!echo draft")
	tithWaitUntil(t, 2*time.Second, "draft text present in editor", func() bool {
		var text string
		var bash bool
		_ = mode.imCallUI(context.Background(), func() {
			text = mode.editor.GetText()
			mode.mu.Lock()
			bash = mode.isBashMode
			mode.mu.Unlock()
		})
		return text == "!echo draft" && bash
	})

	term.tithSendInput("\x03")
	tithWaitUntil(t, 2*time.Second, "first Ctrl+C clears editor and bash mode", func() bool {
		var text string
		var bash bool
		_ = mode.imCallUI(context.Background(), func() {
			text = mode.editor.GetText()
			mode.mu.Lock()
			bash = mode.isBashMode
			mode.mu.Unlock()
		})
		return text == "" && !bash
	})
	select {
	case err := <-runDone:
		t.Fatalf("single Ctrl+C exited unexpectedly (err=%v)", err)
	default:
	}

	// After the 500ms window expires, another single Ctrl+C + Kitty key-release must NOT exit.
	time.Sleep(550 * time.Millisecond)
	term.tithSendInput("\x03")
	term.tithSendInput("\x1b[99;5:3u") // Kitty key release ignored by TuiBase
	tithDrainUI(t, mode)
	select {
	case err := <-runDone:
		t.Fatalf("single Ctrl+C after 500ms window + key release exited unexpectedly (err=%v)", err)
	default:
	}

	// Expire window, open slash autocomplete, and press Kitty CSI-u Ctrl+C twice within 500ms to quit.
	time.Sleep(550 * time.Millisecond)
	term.tithSendInput("/")
	tithWaitUntil(t, 2*time.Second, "slash autocomplete open", func() bool {
		var open bool
		_ = mode.imCallUI(context.Background(), func() {
			open = mode.defaultEditor != nil && mode.defaultEditor.IsShowingAutocomplete()
		})
		return open
	})

	term.tithSendInput("\x1b[99;5:1u")
	term.tithSendInput("\x1b[99;5:3u")
	term.tithSendInput("\x1b[99;5:1u")

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("InteractiveMode.Run returned error on double Ctrl+C: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("InteractiveMode.Run did not exit on double Ctrl+C while autocomplete was open")
	}

	if got := utils.StripAnsi(stdoutBuf.String()); !strings.Contains(got, "To resume this session:") {
		t.Fatalf("expected resume hint printed on interactive quit, got %q", got)
	}
}
