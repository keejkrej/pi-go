// Unit tests for codingagent/experimental/vacation.

package vacation_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental/vacation"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tevaOpenHarness(
	t *testing.T,
	responses []providers.FauxResponseStep,
) (durable.Harness, durable.Conversation) {
	t.Helper()
	faux, err := providers.FauxProvider(nil)
	if err != nil {
		t.Fatalf("FauxProvider: %v", err)
	}
	faux.SetResponses(responses)

	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	registry := durable.CreateRegistry()
	if err := registry.Install(vacation.CreateVacationExtension()); err != nil {
		t.Fatalf("install VacationExtension: %v", err)
	}

	ctx := chord.WithAbortSignal(context.Background(), chord.BackgroundContext)
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
	}, ctx)
	if err != nil {
		t.Fatalf("HarnessOpen: %v", err)
	}
	t.Cleanup(func() {
		_ = harness.Close(chord.BackgroundContext)
	})

	model := faux.GetModel()
	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{
				Provider: string(model.Provider),
				ModelId:  model.Id,
			}),
		},
	})
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	return harness, root
}

func TestParseVacationArgs(t *testing.T) {
	opts, err := vacation.ParseVacationArgs([]string{
		"--print",
		"--agent-dir", "/tmp/agent-vaca",
		"--model", "claude-sonnet",
		"take a break",
	})
	if err != nil {
		t.Fatalf("ParseVacationArgs: %v", err)
	}
	if !opts.PrintMode || opts.Prompt != "take a break" {
		t.Fatalf("unexpected opts: %+v", opts)
	}
	if !strings.HasSuffix(opts.SessionDir, "vacation-sessions") {
		t.Fatalf("expected vacation-sessions dir, got %s", opts.SessionDir)
	}
}

func TestVacationSleepToolAndResume(t *testing.T) {
	stopToolUse := ai.StopReasonToolUse
	harness, root := tevaOpenHarness(t, []providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("sleep", jsonx.ObjectOf(
					"durationMs", 25,
					"reason", "coffee break",
				), nil),
			}, &providers.FauxAssistantMessageOptions{StopReason: &stopToolUse}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("back from vacation"), nil),
		},
	})

	ctx := chord.WithAbortSignal(context.Background(), chord.BackgroundContext)
	prompt := "sleep briefly"
	sub, err := root.Submit(durable.SubmissionDraft{
		RequestId: "vaca-1",
		Type:      durable.SubmissionTypeInput,
		Content:   durable.UserInput{Text: &prompt},
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
	var sawSleepResult bool
	var sawWakeReply bool
	for _, m := range view.Messages {
		if tr, ok := m.(*ai.ToolResultMessage); ok && tr.ToolName == "sleep" {
			sawSleepResult = true
			if len(tr.Content) == 0 {
				t.Fatalf("expected sleep tool content")
			}
			if tc, ok := tr.Content[0].(*ai.TextContent); !ok || !strings.Contains(tc.Text, "coffee break") {
				t.Fatalf("expected coffee break in sleep output (isError=%v), got %+v", tr.IsError, tr.Content[0])
			}
		}
		if am, ok := m.(*ai.AssistantMessage); ok {
			for _, block := range am.Content {
				if tc, ok := block.(*ai.TextContent); ok && strings.Contains(tc.Text, "back from vacation") {
					sawWakeReply = true
				}
			}
		}
	}
	if !sawSleepResult || !sawWakeReply {
		t.Fatalf("expected sleep tool result (%v) and final reply (%v)", sawSleepResult, sawWakeReply)
	}

	doc, err := harness.Snapshot(ctx, vacation.VacationDoc, root.Id())
	if err != nil {
		t.Fatalf("Snapshot VacationDoc: %v", err)
	}
	if active, ok := doc.Get("active"); !ok || active != false {
		t.Fatalf("expected VacationDoc.active=false after wake, got %v", active)
	}
}

func TestVacationTaskAbort(t *testing.T) {
	registry := durable.CreateRegistry()
	if err := registry.Install(vacation.CreateVacationExtension()); err != nil {
		t.Fatalf("install: %v", err)
	}
	models := ai.CreateModels(nil)
	ctx := chord.WithAbortSignal(context.Background(), chord.BackgroundContext)
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
	}, ctx)
	if err != nil {
		t.Fatalf("HarnessOpen: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close(chord.BackgroundContext) })

	root, err := harness.Root(ctx)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	var tid durable.TaskId
	_, err = root.Commit(ctx, func(tx durable.Tx) (any, error) {
		convId := root.Id()
		var cErr error
		tid, cErr = tx.CreateTask(vacation.VacationTask, vacation.VacationTaskInput{
			UntilMs: time.Now().Add(10 * time.Second).UnixMilli(),
			Reason:  "long nap",
		}, durable.TaskOptions{
			Ownership: durable.TaskOwnership{
				Kind: durable.TaskOwnershipConversation,
			},
			ConversationId: &convId,
		})
		return nil, cErr
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if _, err := harness.AbortTask(ctx, tid); err != nil {
		t.Fatalf("AbortTask: %v", err)
	}
	settled, err := harness.WaitForTask(ctx, tid)
	if err != nil {
		t.Fatalf("WaitForTask: %v", err)
	}
	if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.TaskOutcomeAborted {
		t.Fatalf("expected TaskOutcomeAborted, got %+v", settled.State.Outcome)
	}
}

func TestVacationRunHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := vacation.Run(context.Background(), []string{"--help"}, &out, &errOut); err != nil {
		t.Fatalf("Run --help: %v", err)
	}
	if !strings.Contains(out.String(), "Usage: vacation-pi") {
		t.Fatalf("expected usage text, got: %s", out.String())
	}
}
