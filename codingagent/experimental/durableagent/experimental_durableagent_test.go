// Ported/adapted unit and integration tests for codingagent/experimental/durableagent.

package durableagent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tedaSetTestAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	return dir
}

func tedaWaitUntil(t *testing.T, timeout time.Duration, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestDurableagentParseArgs(t *testing.T) {
	opts, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("ParseArgs(nil) failed: %v", err)
	}
	if opts.ContinueSession {
		t.Fatal("expected ContinueSession=false for empty args")
	}

	for _, flag := range []string{"-c", "--continue"} {
		opts, err = ParseArgs([]string{flag})
		if err != nil {
			t.Fatalf("ParseArgs(%q) failed: %v", flag, err)
		}
		if !opts.ContinueSession {
			t.Fatalf("expected ContinueSession=true for %q", flag)
		}
	}

	if _, err := ParseArgs([]string{"--bogus"}); err == nil || !strings.Contains(err.Error(), "Unknown argument: --bogus") {
		t.Fatalf("expected unknown argument error, got %v", err)
	}
}

func TestDurableagentSelectSession(t *testing.T) {
	agentDir := tedaSetTestAgentDir(t)
	cwd := t.TempDir()
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatalf("EvalSymlinks failed: %v", err)
	}

	if _, err := SelectSession(context.Background(), cwd, true); err == nil || !strings.Contains(err.Error(), "No durable session exists for "+resolvedCwd) {
		t.Fatalf("expected missing continue session error, got %v", err)
	}

	loc1, err := SelectSession(context.Background(), cwd, false)
	if err != nil {
		t.Fatalf("SelectSession(new #1) failed: %v", err)
	}
	if !loc1.Created {
		t.Fatal("expected first session Created=true")
	}
	dirPattern := regexp.MustCompile(`^\d{13}-[0-9a-f-]{36}$`)
	if !dirPattern.MatchString(loc1.Id) {
		t.Fatalf("unexpected session directory id %q", loc1.Id)
	}
	if filepath.Base(loc1.Database) != "session.sqlite" {
		t.Fatalf("expected session.sqlite database path, got %q", loc1.Database)
	}
	expectedParent := filepath.Join(agentDir, "experimental", "durable-sessions")
	if !strings.HasPrefix(loc1.Directory, expectedParent+string(filepath.Separator)) {
		t.Fatalf("expected session directory under %q, got %q", expectedParent, loc1.Directory)
	}
	if loc1.Cwd != resolvedCwd {
		t.Fatalf("expected resolved cwd %q, got %q", resolvedCwd, loc1.Cwd)
	}
	if err := os.WriteFile(loc1.Database, nil, 0o644); err != nil {
		t.Fatalf("write marker db failed: %v", err)
	}
	if err := loc1.Release(); err != nil {
		t.Fatalf("release loc1 failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)
	loc2, err := SelectSession(context.Background(), cwd, false)
	if err != nil {
		t.Fatalf("SelectSession(new #2) failed: %v", err)
	}
	if loc2.Id == loc1.Id {
		t.Fatal("expected distinct session ids")
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if _, err := SelectSession(shortCtx, cwd, true); err == nil || !strings.Contains(err.Error(), "Session is already open in another process") {
		t.Fatalf("expected lock contention error while newest session locked, got %v", err)
	}

	if err := loc2.Release(); err != nil {
		t.Fatalf("release loc2 failed: %v", err)
	}

	resumed, err := SelectSession(context.Background(), cwd, true)
	if err != nil {
		t.Fatalf("SelectSession(continue) failed: %v", err)
	}
	defer func() {
		_ = resumed.Release()
	}()
	if resumed.Created {
		t.Fatal("expected resumed session Created=false")
	}
	if resumed.Id != loc2.Id {
		t.Fatalf("expected newest session %q, got %q", loc2.Id, resumed.Id)
	}
}

func TestDurableagentCreatePiPromptAndSubagent(t *testing.T) {
	_ = tedaSetTestAgentDir(t)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("Use strict durable invariants.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md failed: %v", err)
	}

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		t.Fatalf("FauxProvider failed: %v", err)
	}
	model := faux.GetModel()
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	var (
		mu             sync.Mutex
		prompts        []string
		childHadSub    bool
		sawProjectCtx  bool
		sawNoDoubleTag bool
	)
	callNum := 0
	step := providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		callNum++
		sys := ""
		var transcriptTools []*ai.Tool
		if transcript != nil {
			sys = ai.GetCurrentSystemPrompt(transcript.Messages)
			transcriptTools = ai.GetCurrentTools(transcript.Messages)
		}
		prompts = append(prompts, sys)
		if strings.Contains(sys, "Use strict durable invariants.") {
			sawProjectCtx = true
		}
		if !strings.Contains(sys, "<preamble>") {
			sawNoDoubleTag = true
		}
		toolUse := ai.StopReasonToolUse
		switch callNum {
		case 1:
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("subagent", jsonx.ObjectOf("task", "say hi from child"), nil),
			}, &providers.FauxAssistantMessageOptions{StopReason: &toolUse}), nil
		case 2:
			for _, tl := range transcriptTools {
				if tl != nil && tl.Name == "subagent" {
					childHadSub = true
				}
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantText("child says hello"), nil), nil
		default:
			return providers.FauxAssistantMessage(providers.FauxAssistantText("parent complete"), nil), nil
		}
	})
	faux.SetResponses([]providers.FauxResponseStep{step, step, step})

	settingsManager := core.SettingsManagerInMemory(&core.Settings{})
	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		t.Fatalf("install CodingTools failed: %v", err)
	}
	if err := registry.Install(CreatePiPrompt(settingsManager, cwd)); err != nil {
		t.Fatalf("install CreatePiPrompt failed: %v", err)
	}
	if err := registry.Install(Subagent); err != nil {
		t.Fatalf("install Subagent failed: %v", err)
	}

	envs := NewExecutionEnvs(cwd)
	defer envs.Cleanup(chord.BackgroundContext)

	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env:      envs.Env,
	}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("HarnessOpen failed: %v", err)
	}
	defer func() {
		_ = harness.Close(chord.BackgroundContext)
	}()

	root, err := harness.Root(chord.BackgroundContext, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{
				Provider: string(model.Provider),
				ModelId:  model.Id,
			}),
			Cwd: jsonx.Some(cwd),
		},
	})
	if err != nil {
		t.Fatalf("harness.Root failed: %v", err)
	}
	if err := harness.Resume(); err != nil {
		t.Fatalf("harness.Resume failed: %v", err)
	}

	promptText := "delegate greeting"
	sub, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: durable.UserInput{Text: &promptText},
	}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	settled, err := sub.Wait(chord.BackgroundContext)
	if err != nil {
		t.Fatalf("sub.Wait failed: %v", err)
	}
	if settled.Status != durable.SubmissionStatusDone {
		t.Fatalf("expected submission done, got %s reason=%v detail=%v", settled.Status, settled.Reason, settled.Detail)
	}

	mu.Lock()
	defer mu.Unlock()
	if callNum != 3 {
		t.Fatalf("expected 3 faux calls (parent -> child -> parent), got %d", callNum)
	}
	if childHadSub {
		t.Fatal("expected subagent extension to be removed from child conversation")
	}
	if !sawProjectCtx {
		t.Fatalf("expected AGENTS.md content in system prompt, prompts=%v", prompts)
	}
	if !sawNoDoubleTag {
		t.Fatal("expected preamble section not to be double-wrapped with <preamble>")
	}
}

func TestDurableagentOpenDurableAndController(t *testing.T) {
	_ = tedaSetTestAgentDir(t)
	cwd := t.TempDir()
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatalf("EvalSymlinks failed: %v", err)
	}

	res, err := OpenDurable(context.Background(), &OpenDurableOptions{Cwd: cwd})
	if err != nil {
		t.Fatalf("OpenDurable(new) failed: %v", err)
	}

	snap := res.View.Current()
	if snap.Session.Cwd != resolvedCwd {
		t.Fatalf("expected session cwd %q, got %q", resolvedCwd, snap.Session.Cwd)
	}
	if len(snap.Conversations) != 1 || snap.Conversations[0].Label != "main" {
		t.Fatalf("expected single main conversation, got %+v", snap.Conversations)
	}
	if snap.Tasks == nil {
		t.Fatal("expected task panel open initially")
	}

	if err := res.Controller.ToggleTasks(context.Background()); err != nil {
		t.Fatalf("ToggleTasks(off) failed: %v", err)
	}
	tedaWaitUntil(t, 2*time.Second, func() bool {
		return res.View.Current().Tasks == nil
	}, "tasks toggled off")

	if err := res.Controller.ToggleTasks(context.Background()); err != nil {
		t.Fatalf("ToggleTasks(on) failed: %v", err)
	}
	tedaWaitUntil(t, 2*time.Second, func() bool {
		return res.View.Current().Tasks != nil
	}, "tasks toggled back on")

	if AgentOf(snap.Conversation).Model == nil {
		sawNoModelNotice := false
		for _, n := range snap.Notices {
			if strings.Contains(n.Message, "No model configured") {
				sawNoModelNotice = true
				break
			}
		}
		if !sawNoModelNotice {
			t.Fatalf("expected no-model warning notice, got %+v", snap.Notices)
		}
		if err := res.Controller.CycleThinking(context.Background()); err == nil || !strings.Contains(err.Error(), "No model selected") {
			t.Fatalf("expected No model selected error from CycleThinking, got %v", err)
		}
	} else {
		if err := res.Controller.SetModel(context.Background(), durable.ModelRef{Provider: "missing", ModelId: "missing"}); err == nil || !strings.Contains(err.Error(), "Unknown model") {
			t.Fatalf("expected Unknown model error from SetModel, got %v", err)
		}
	}

	firstSessionId := snap.Session.Id
	if err := res.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	resumed, err := OpenDurable(context.Background(), &OpenDurableOptions{
		Cwd:             cwd,
		ContinueSession: true,
	})
	if err != nil {
		t.Fatalf("OpenDurable(continue) failed: %v", err)
	}
	defer func() {
		_ = resumed.Close()
	}()
	if resumed.View.Current().Session.Id != firstSessionId {
		t.Fatalf("expected resumed session id %q, got %q", firstSessionId, resumed.View.Current().Session.Id)
	}
}
