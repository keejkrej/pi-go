// Unit and integration tests for codingagent/experimental/vacation.

package vacation

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
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tevaSetTestAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	return dir
}

func tevaWaitUntil(t *testing.T, timeout time.Duration, cond func() bool, desc string) {
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

func TestVacationParseArgs(t *testing.T) {
	opts, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("ParseArgs(nil) failed: %v", err)
	}
	if opts.ContinueSession {
		t.Fatal("expected ContinueSession=false")
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

	if _, err := ParseArgs([]string{"--wat"}); err == nil || !strings.Contains(err.Error(), "Unknown argument: --wat") {
		t.Fatalf("expected Unknown argument error, got %v", err)
	}
}

func TestVacationSelectSession(t *testing.T) {
	agentDir := tevaSetTestAgentDir(t)
	cwd := t.TempDir()
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatalf("EvalSymlinks failed: %v", err)
	}

	if _, err := SelectSession(context.Background(), cwd, true); err == nil || !strings.Contains(err.Error(), "No vacation session exists for "+resolvedCwd) {
		t.Fatalf("expected missing continue session error, got %v", err)
	}

	loc1, err := SelectSession(context.Background(), cwd, false)
	if err != nil {
		t.Fatalf("SelectSession(new) failed: %v", err)
	}
	dirPattern := regexp.MustCompile(`^\d{13}-[0-9a-f-]{36}$`)
	if !dirPattern.MatchString(loc1.Id) {
		t.Fatalf("unexpected session directory id %q", loc1.Id)
	}
	expectedRoot := filepath.Join(agentDir, "experimental", "vacation-sessions")
	if !strings.HasPrefix(loc1.Directory, expectedRoot+string(filepath.Separator)) {
		t.Fatalf("expected directory under %q, got %q", expectedRoot, loc1.Directory)
	}
	if err := os.WriteFile(loc1.Database, nil, 0o644); err != nil {
		t.Fatalf("write marker failed: %v", err)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if _, err := SelectSession(shortCtx, cwd, true); err == nil || !strings.Contains(err.Error(), "Session is already open in another process") {
		t.Fatalf("expected lock contention error, got %v", err)
	}
	if err := loc1.Release(); err != nil {
		t.Fatalf("release loc1 failed: %v", err)
	}

	resumed, err := SelectSession(context.Background(), cwd, true)
	if err != nil {
		t.Fatalf("SelectSession(continue) failed: %v", err)
	}
	defer func() {
		_ = resumed.Release()
	}()
	if resumed.Created || resumed.Id != loc1.Id {
		t.Fatalf("expected resumed newest session %q (created=false), got %q (created=%v)", loc1.Id, resumed.Id, resumed.Created)
	}
}

func tevaFindTool(snap durable.RegistrySnapshot, name string) *durable.ToolRegistration {
	for _, rt := range snap.Tools() {
		if rt.Tool != nil && rt.Tool.Name == name {
			return rt.Tool
		}
	}
	return nil
}

func TestVacationRegistryAndSearchAbort(t *testing.T) {
	registry, err := CreateVacationRegistry()
	if err != nil {
		t.Fatalf("CreateVacationRegistry failed: %v", err)
	}
	snap := registry.Snapshot()
	searchTool := tevaFindTool(snap, "search")
	if searchTool == nil {
		t.Fatal("expected search tool in vacation registry")
	}
	if tevaFindTool(snap, "research") == nil {
		t.Fatal("expected research tool in vacation registry")
	}
	for _, banned := range []string{"read", "bash", "edit", "write", "subagent"} {
		if tevaFindTool(snap, banned) != nil {
			t.Fatalf("did not expect coding/subagent tool %q in vacation registry", banned)
		}
	}

	abortCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, execErr := searchTool.Execute(
		jsonx.ObjectOf("topic", "trains", "city", "Vienna"),
		nil,
		chord.WithAbortSignal(abortCtx, chord.BackgroundContext),
	)
	if execErr == nil {
		t.Fatal("expected search tool to return error on aborted signal")
	}
}

func TestVacationBackgroundResearchEndToEnd(t *testing.T) {
	_ = tevaSetTestAgentDir(t)
	prevDelay := vacaSearchStepDelay
	vacaSearchStepDelay = 2 * time.Millisecond
	defer func() {
		vacaSearchStepDelay = prevDelay
	}()

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		t.Fatalf("FauxProvider failed: %v", err)
	}
	model := faux.GetModel()
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	var (
		mu             sync.Mutex
		subTools       []string
		sawMainFollow  bool
		subagentAsked  bool
		mainStarted    bool
		subSearchAsked bool
	)
	step := providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		sys := ""
		var transcriptTools []*ai.Tool
		if transcript != nil {
			sys = ai.GetCurrentSystemPrompt(transcript.Messages)
			transcriptTools = ai.GetCurrentTools(transcript.Messages)
		}
		isSubagent := strings.Contains(sys, "research subagent for a vacation planner")
		toolUse := ai.StopReasonToolUse

		if isSubagent {
			if !subagentAsked {
				subagentAsked = true
				for _, tl := range transcriptTools {
					if tl != nil {
						subTools = append(subTools, tl.Name)
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
					providers.FauxToolCall("search", jsonx.ObjectOf("topic", "weather", "city", "Vienna"), nil),
				}, &providers.FauxAssistantMessageOptions{StopReason: &toolUse}), nil
			}
			subSearchAsked = true
			return providers.FauxAssistantMessage(providers.FauxAssistantText("Sunny 24C in Vienna."), nil), nil
		}

		for _, msg := range transcript.Messages {
			if um, ok := msg.(*ai.UserMessage); ok && um != nil && um.Content.Text != nil {
				if strings.Contains(*um.Content.Text, "[research report] Sunny 24C in Vienna.") {
					sawMainFollow = true
				}
			}
		}
		if sawMainFollow {
			return providers.FauxAssistantMessage(providers.FauxAssistantText("Vienna plan: pack sunglasses."), nil), nil
		}
		if !mainStarted {
			mainStarted = true
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("research", jsonx.ObjectOf("task", "Vienna weekend"), nil),
			}, &providers.FauxAssistantMessageOptions{StopReason: &toolUse}), nil
		}
		return providers.FauxAssistantMessage(providers.FauxAssistantText("Research running, ask me anything meanwhile."), nil), nil
	})
	faux.SetResponses([]providers.FauxResponseStep{step, step, step, step, step})

	registry, err := CreateVacationRegistry()
	if err != nil {
		t.Fatalf("CreateVacationRegistry failed: %v", err)
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
	}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("HarnessOpen failed: %v", err)
	}
	defer func() {
		_ = harness.Close(chord.BackgroundContext)
	}()

	root, err := harness.Root(chord.BackgroundContext, durable.RootOptions{
		Agent: &durable.AgentChange{
			Extensions: jsonx.Some(durable.AgentExtensionsSet(Vacation)),
			Model: jsonx.Some(durable.ModelRef{
				Provider: string(model.Provider),
				ModelId:  model.Id,
			}),
		},
	})
	if err != nil {
		t.Fatalf("harness.Root failed: %v", err)
	}
	if err := harness.Resume(); err != nil {
		t.Fatalf("harness.Resume failed: %v", err)
	}

	userMsg := "Plan Vienna"
	sub, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: durable.UserInput{Text: &userMsg},
	}, chord.BackgroundContext)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	settled, err := sub.Wait(chord.BackgroundContext)
	if err != nil {
		t.Fatalf("sub.Wait failed: %v", err)
	}
	if settled.Status != durable.SubmissionStatusDone {
		t.Fatalf("expected initial turn done, got %s", settled.Status)
	}

	// Wait for background research task to deliver [research report] and trigger follow-up answer.
	tevaWaitUntil(t, 5*time.Second, func() bool {
		page, pageErr := root.Entries(durable.ConversationEntryQuery{}, 50, nil, chord.BackgroundContext)
		if pageErr != nil {
			return false
		}
		for _, item := range page.Items {
			if item.Kind == durable.AssistantEntry.Kind && len(item.Model) > 0 {
				if am, ok := item.Model[0].(*ai.AssistantMessage); ok && am != nil {
					for _, part := range am.Content {
						if tc, ok := part.(*ai.TextContent); ok && tc != nil && strings.Contains(tc.Text, "Vienna plan: pack sunglasses.") {
							return true
						}
					}
				}
			}
		}
		return false
	}, "background research report delivered and answered on main conversation")

	mu.Lock()
	defer mu.Unlock()
	if len(subTools) != 1 || subTools[0] != "search" {
		t.Fatalf("expected subagent to have only [search], got %v", subTools)
	}
	if !subSearchAsked || !sawMainFollow {
		t.Fatalf("expected full search -> report cycle (subSearchAsked=%v sawMainFollow=%v)", subSearchAsked, sawMainFollow)
	}
}

func TestVacationOpenDurable(t *testing.T) {
	_ = tevaSetTestAgentDir(t)
	cwd := t.TempDir()

	res, err := OpenDurable(context.Background(), &OpenDurableOptions{Cwd: cwd})
	if err != nil {
		t.Fatalf("OpenDurable(vacation) failed: %v", err)
	}
	defer func() {
		_ = res.Close()
	}()

	snap := res.View.Current()
	agentDoc := AgentOf(snap.Conversation)
	if agentDoc.Extensions == nil || len(agentDoc.Extensions.List) != 1 || agentDoc.Extensions.List[0] != Vacation.Name {
		t.Fatalf("expected root agent extensions=[vacation], got %+v", agentDoc.Extensions)
	}
	if snap.Tasks == nil {
		t.Fatal("expected tasks panel open initially")
	}
}
