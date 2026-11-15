// Unit tests for codingagent/experimental/durableagent.

package durableagent_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental/durableagent"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tedaOpenHarness(
	t *testing.T,
	responses []providers.FauxResponseStep,
	cwd string,
) (*providers.FauxProviderHandle, durable.Harness, durable.Conversation) {
	t.Helper()
	faux, err := providers.FauxProvider(nil)
	if err != nil {
		t.Fatalf("FauxProvider: %v", err)
	}
	faux.SetResponses(responses)

	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		t.Fatalf("install CodingTools: %v", err)
	}
	if err := registry.Install(durableagent.CreateDurablePromptExtension(durableagent.DurablePromptExtensionOptions{
		Cwd: cwd,
	})); err != nil {
		t.Fatalf("install PromptExtension: %v", err)
	}
	if err := registry.Install(durableagent.CreateSubagentExtension()); err != nil {
		t.Fatalf("install SubagentExtension: %v", err)
	}

	ctx := chord.WithAbortSignal(context.Background(), chord.BackgroundContext)
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env:      durableagent.CreateHarnessEnv(cwd, nil),
	}, ctx)
	if err != nil {
		t.Fatalf("HarnessOpen: %v", err)
	}
	t.Cleanup(func() {
		_ = harness.Close(chord.BackgroundContext)
	})

	model := faux.GetModel()
	instr := "Follow durable instructions."
	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{
				Provider: string(model.Provider),
				ModelId:  model.Id,
			}),
			Cwd:          jsonx.Some(cwd),
			Instructions: jsonx.Some(instr),
		},
	})
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	return faux, harness, root
}

func TestParseDurableAgentArgs(t *testing.T) {
	opts, err := durableagent.ParseDurableAgentArgs([]string{
		"-p",
		"--continue",
		"--provider=anthropic",
		"--model", "claude-sonnet",
		"--thinking", "high",
		"--cwd", "/tmp/work",
		"--instructions", "be concise",
		"hello", "world",
	})
	if err != nil {
		t.Fatalf("ParseDurableAgentArgs: %v", err)
	}
	if !opts.PrintMode || !opts.Continue {
		t.Fatalf("expected print & continue flags set: %+v", opts)
	}
	if opts.Provider == nil || *opts.Provider != "anthropic" {
		t.Fatalf("unexpected provider: %v", opts.Provider)
	}
	if opts.Model == nil || *opts.Model != "claude-sonnet" {
		t.Fatalf("unexpected model: %v", opts.Model)
	}
	if opts.Thinking == nil || *opts.Thinking != ai.ModelThinkingLevelHigh {
		t.Fatalf("unexpected thinking: %v", opts.Thinking)
	}
	if opts.Cwd != "/tmp/work" || opts.Prompt != "hello world" {
		t.Fatalf("unexpected cwd/prompt: %+v", opts)
	}

	if _, err := durableagent.ParseDurableAgentArgs([]string{"--unknown-flag"}); err == nil {
		t.Fatalf("expected error for unknown flag")
	}
}

func TestDurablePromptAndSubagentExecution(t *testing.T) {
	cwd := t.TempDir()
	stopToolUse := ai.StopReasonToolUse
	_, harness, root := tedaOpenHarness(t, []providers.FauxResponseStep{
		// Parent assistant issues subagent tool call
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("subagent", jsonx.ObjectOf("prompt", "child task prompt"), nil),
			}, &providers.FauxAssistantMessageOptions{StopReason: &stopToolUse}),
		},
		// Child conversation runs and replies
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("child finished work"), nil),
		},
		// Parent assistant summarizes child output
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("parent done: child finished work"), nil),
		},
	}, cwd)

	ctx := chord.WithAbortSignal(context.Background(), chord.BackgroundContext)
	inputText := "delegate work"
	sub, err := root.Submit(durable.SubmissionDraft{
		RequestId: "sub-1",
		Type:      durable.SubmissionTypeInput,
		Content:   durable.UserInput{Text: &inputText},
	}, ctx)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	settled, err := sub.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if settled.Status != durable.SubmissionStatusDone {
		t.Fatalf("expected submission done, got %s", settled.Status)
	}
	if err := harness.WaitForIdle(ctx); err != nil {
		t.Fatalf("WaitForIdle: %v", err)
	}

	view, err := root.Context(ctx)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	var foundToolResult, foundFinal bool
	for _, msg := range view.Messages {
		if tr, ok := msg.(*ai.ToolResultMessage); ok && tr.ToolName == "subagent" {
			foundToolResult = true
			t.Logf("subagent tool result (isError=%v): %+v", tr.IsError, tedaPromptText(tr.Content))
		}
		if am, ok := msg.(*ai.AssistantMessage); ok {
			t.Logf("assistant stop=%s err=%v content=%q", am.StopReason, am.ErrorMessage, tedaPromptText(am.Content))
			for _, block := range am.Content {
				if tc, ok := block.(*ai.TextContent); ok && strings.Contains(tc.Text, "parent done: child finished work") {
					foundFinal = true
				}
			}
		}
	}
	if !foundToolResult {
		t.Fatalf("expected subagent tool result in parent context")
	}
	if !foundFinal {
		t.Fatalf("expected final parent reply containing child result")
	}

	// Verify system prompt entry rendered sections without double XML wrapping
	var foundSystem bool
	for _, entry := range view.Entries {
		if entry.Kind == durable.SystemEntry.Kind {
			foundSystem = true
			if entry.Data != nil {
				raw, _ := entry.Data.(string)
				if strings.Contains(raw, "<tools>\n<tools>") {
					t.Fatalf("detected double XML tag wrapping in system prompt: %s", raw)
				}
			}
		}
	}
	if !foundSystem {
		t.Fatalf("expected system prompt entry in durable transcript")
	}
}

func TestLockedSessionStorageAndListing(t *testing.T) {
	dir := t.TempDir()
	dbPath := durableagent.NewDurableSessionPath(dir)
	if filepath.Dir(dbPath) != dir {
		t.Fatalf("unexpected session path dir: %s", dbPath)
	}

	ctx := context.Background()
	st, release, err := durableagent.OpenLockedSessionStorage(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenLockedSessionStorage: %v", err)
	}

	// Concurrent lock on same DB must fail
	if _, _, lockErr := durableagent.OpenLockedSessionStorage(ctx, dbPath); lockErr == nil {
		t.Fatalf("expected concurrent session lock acquisition to fail")
	}

	sess := durable.NewSession(st)
	chordCtx := chord.WithAbortSignal(ctx, chord.BackgroundContext)
	if commitErr := sess.Commit(chordCtx, func(tx durable.Tx) error {
		rec, createErr := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if createErr != nil {
			return createErr
		}
		msg := &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: tedaPtrString("first durable question")}, Timestamp: time.Now().UnixMilli()}
		_, err := tx.AppendTypedEntry(durable.UserEntry, rec.Id, durable.EntryDraft{
			Model: ai.MessageList{msg},
		})
		return err
	}); commitErr != nil {
		t.Fatalf("sess.Commit: %v", commitErr)
	}
	if err := sess.Close(chordCtx); err != nil {
		t.Fatalf("sess.Close: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release lock: %v", err)
	}

	listed, err := durableagent.ListDurableSessions(ctx, dir)
	if err != nil {
		t.Fatalf("ListDurableSessions: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 session, got %d", len(listed))
	}
	if listed[0].Locked {
		t.Fatalf("expected session unlocked after release")
	}
	if listed[0].EntryCount != 1 || listed[0].FirstText != "first durable question" {
		t.Fatalf("unexpected summary: %+v", listed[0])
	}
}

func TestDurableAgentRunHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := durableagent.Run(context.Background(), []string{"--help"}, &out, &errOut); err != nil {
		t.Fatalf("Run --help: %v", err)
	}
	if !strings.Contains(out.String(), "Usage: durable-pi") {
		t.Fatalf("expected usage text, got: %s", out.String())
	}
}

func tedaPtrString(s string) *string { return &s }

func tedaPromptText(blocks ai.ContentList) string {
	var parts []string
	for _, b := range blocks {
		if tc, ok := b.(*ai.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "|")
}
