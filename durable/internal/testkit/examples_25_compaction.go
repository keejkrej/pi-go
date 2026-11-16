// Ported from packages/durable/test/examples/25-compaction.ts (pi v1.0.0).

package testkit

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func e2cMsgText(msg ai.Message) string {
	if msg == nil || msg.GetRole() == "system" {
		return ""
	}
	switch m := msg.(type) {
	case *ai.UserMessage:
		if m.Content.Text != nil {
			return strings.ReplaceAll(*m.Content.Text, "\n", " ")
		}
		for _, c := range m.Content.Blocks {
			if tc, ok := c.(*ai.TextContent); ok {
				return strings.ReplaceAll(tc.Text, "\n", " ")
			}
		}
	case *ai.AssistantMessage:
		for _, c := range m.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				return strings.ReplaceAll(tc.Text, "\n", " ")
			}
		}
	case *ai.ToolResultMessage:
		for _, c := range m.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				return strings.ReplaceAll(tc.Text, "\n", " ")
			}
		}
	}
	return ""
}

// Example25Compaction runs the 25-compaction example.
func Example25Compaction(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	var mu sync.Mutex
	overflowOnce := false
	summaries := 0
	var hold chan struct{}

	respond := providers.FauxResponseFactory(func(callCtx context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		if len(transcript.Messages) > 0 {
			if sys, ok := transcript.Messages[0].(*ai.SystemMessage); ok && sys.Content.Text != nil && strings.Contains(*sys.Content.Text, "summarization") {
				mu.Lock()
				summaries++
				n := summaries
				mu.Unlock()
				return providers.FauxAssistantMessage(
					providers.FauxAssistantText(fmt.Sprintf("## Goal\nPlan a week in Lisbon (summary #%d).", n)), nil,
				), nil
			}
		}
		mu.Lock()
		if overflowOnce {
			overflowOnce = false
			mu.Unlock()
			sr := ai.StopReasonError
			errMsg := "prompt is too long"
			return providers.FauxAssistantMessage(providers.FauxAssistantText(""), &providers.FauxAssistantMessageOptions{
				StopReason:   &sr,
				ErrorMessage: &errMsg,
			}), nil
		}
		held := hold
		hold = nil
		mu.Unlock()
		if held != nil {
			select {
			case <-held:
			case <-callCtx.Done():
				return nil, callCtx.Err()
			}
		}
		var question ai.Message
		for i := len(transcript.Messages) - 1; i >= 0; i-- {
			if transcript.Messages[i].GetRole() == "user" {
				question = transcript.Messages[i]
				break
			}
		}
		return providers.FauxAssistantMessage(
			providers.FauxAssistantText(fmt.Sprintf("A detailed answer to %q: %s", e2cMsgText(question), strings.Repeat("details ", 200))),
			nil,
		), nil
	})

	cw := 3000
	mt := 1000
	faux, err := providers.FauxProvider(&providers.RegisterFauxProviderOptions{
		Models: []providers.FauxModelDefinition{{Id: "tiny", ContextWindow: &cw, MaxTokens: &mt}},
	})
	if err != nil {
		return err
	}
	steps := make([]providers.FauxResponseStep, 100)
	for i := range steps {
		steps[i] = respond
	}
	faux.SetResponses(steps)
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	var backgroundTokens atomic.Int64
	backgroundTokens.Store(800)
	reserveTokens := 1000
	keepRecentTokens := 400

	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: durable.CreateRegistry(),
		Settings: func() durable.HarnessSettings {
			bg := int(backgroundTokens.Load())
			return durable.HarnessSettings{
				Compaction: &durable.CompactionPolicyPatch{
					ReserveTokens:    &reserveTokens,
					KeepRecentTokens: &keepRecentTokens,
					BackgroundTokens: &bg,
				},
			}
		},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "tiny"})},
	})
	if err != nil {
		return err
	}

	show := func(conv durable.Conversation, label string) error {
		view, err := conv.Context(ctx)
		if err != nil {
			return err
		}
		all, err := conv.Entries(durable.ConversationEntryQuery{}, 1000, nil, ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "\n%s: %d messages in context, %d entries stored\n", label, len(view.Messages), len(all.Items))
		if durable.CompactionEntry.Is(view.Head) {
			if obj, ok := view.Head.Data.(*jsonx.Object); ok {
				if reason, exists := obj.Get("reason"); exists {
					_, _ = fmt.Fprintf(out, "  (%v compaction summary first)\n", reason)
				}
			}
		}
		for _, m := range view.Messages {
			preview := "(system prompt)"
			if m.GetRole() != "system" {
				t := e2cMsgText(m)
				if len(t) > 70 {
					t = t[:70]
				}
				preview = t
			}
			_, _ = fmt.Fprintf(out, "  %-9s %s\n", m.GetRole(), preview)
		}
		return nil
	}

	ask := func(question string) error {
		q := question
		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: ai.UserMessageContent{Text: &q},
		}, ctx)
		if err != nil {
			return err
		}
		rec, err := sub.Wait(ctx)
		if err != nil {
			return err
		}
		liveSnap, err := harness.Snapshot(ctx, durable.LiveDoc, root.Id())
		if err != nil {
			return err
		}
		var live durable.LiveState
		if liveSnap != nil {
			_ = jsonx.Decode(liveSnap, &live)
		}
		for _, c := range live.Compactions {
			if _, err := harness.WaitForTask(ctx, c.TaskId); err != nil {
				return err
			}
		}
		statusLabel := "answered"
		if rec.Status != durable.SubmissionStatusDone {
			if rec.Reason != nil {
				statusLabel = *rec.Reason
			} else {
				statusLabel = string(rec.Status)
			}
		}
		return show(root, fmt.Sprintf("after %q (%s)", question, statusLabel))
	}

	for _, question := range []string{
		"Where should we stay?",
		"What should we eat?",
		"Which day trips?",
		"Any museums?",
		"Nightlife?",
	} {
		if err := ask(question); err != nil {
			return err
		}
	}

	finishCh := make(chan struct{})
	var finishOnce sync.Once
	finishAnswer := func() { finishOnce.Do(func() { close(finishCh) }) }
	defer finishAnswer()

	mu.Lock()
	hold = finishCh
	mu.Unlock()

	busyQ := "How do we get around?"
	busy, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &busyQ},
	}, ctx)
	if err != nil {
		return err
	}
	inst := "Keep the hotel shortlist"
	manual, err := root.Compact(&inst, ctx)
	if err != nil {
		return err
	}
	settledTask, err := harness.WaitForTask(ctx, manual)
	if err != nil {
		return err
	}
	rawRes, hasRes := settledTask.State.Outcome.Result.Get()
	if !hasRes {
		return fmt.Errorf("missing compaction result")
	}
	var compRes durable.CompactionResult
	if err := jsonx.Decode(rawRes, &compRes); err != nil {
		return err
	}
	summarySub, err := harness.Submission(ctx, *compRes.SubmissionId)
	if err != nil {
		return err
	}
	sumStatus, err := summarySub.Status(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\nmanual compaction finished; its summary is %s\n", sumStatus.Status)
	finishAnswer()
	if _, err := busy.Wait(ctx); err != nil {
		return err
	}
	sumSettled, err := summarySub.Wait(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "after the answer, the summary is %s\n", sumSettled.Status)
	if err := show(root, "after compact()"); err != nil {
		return err
	}

	backgroundTokens.Store(0)
	if err := ask("What should we pack?"); err != nil {
		return err
	}
	mu.Lock()
	overflowOnce = true
	mu.Unlock()
	if err := ask("Summarize the plan for my partner"); err != nil {
		return err
	}
	return harness.Close(ctx)
}
