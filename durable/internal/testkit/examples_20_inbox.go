// Ported from packages/durable/test/examples/20-inbox.ts (pi v1.0.0).

package testkit

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example20Inbox runs the 20-inbox example.
func Example20Inbox(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext

	held := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(held) }) }
	defer release()

	firstStarted := make(chan struct{})
	var startedOnce sync.Once
	slow := providers.FauxResponseFactory(func(callCtx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		startedOnce.Do(func() { close(firstStarted) })
		select {
		case <-held:
		case <-callCtx.Done():
			return nil, callCtx.Err()
		}
		return providers.FauxAssistantMessage(providers.FauxAssistantText("Answer to the first question."), nil), nil
	})
	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	faux.SetResponses([]providers.FauxResponseStep{
		slow,
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Answer to the follow-up and the steer."), nil)},
	})
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:         models,
		Registry:       durable.CreateRegistry(),
		StaticSettings: &durable.HarnessSettings{FollowUpMode: durable.QueueModeAll},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
	})
	if err != nil {
		return err
	}

	q1 := "First question"
	first, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q1},
	}, ctx)
	if err != nil {
		return err
	}
	<-firstStarted

	q2 := "A follow-up"
	followUp, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q2},
	}, ctx)
	if err != nil {
		return err
	}
	q3 := "A steer"
	steer, err := root.Submit(durable.SubmissionDraft{
		Type:     durable.SubmissionTypeInput,
		Content:  ai.UserMessageContent{Text: &q3},
		WhenBusy: durable.SubmissionWhenBusySteer,
	}, ctx)
	if err != nil {
		return err
	}
	noteSub, err := root.Submit(durable.SubmissionDraft{
		Type: durable.SubmissionTypeWrite,
		Entry: &durable.EntryDraft{
			Kind: "app.note",
			Data: "noted while busy",
		},
	}, ctx)
	if err != nil {
		return err
	}
	q4 := "Never mind"
	withdrawn, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q4},
	}, ctx)
	if err != nil {
		return err
	}
	q5 := "Now or never"
	if _, err := root.Submit(durable.SubmissionDraft{
		Type:     durable.SubmissionTypeInput,
		Content:  ai.UserMessageContent{Text: &q5},
		WhenBusy: durable.SubmissionWhenBusyReject,
	}, ctx); err != nil {
		_, _ = fmt.Fprintf(out, "rejected: %v\n", err)
	}
	withdrawRes, err := withdrawn.Abort(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "withdraw: %s\n", withdrawRes)

	inboxSnap, err := harness.Snapshot(ctx, durable.InboxDoc, root.Id())
	if err != nil {
		return err
	}
	var inbox durable.InboxState
	if inboxSnap != nil {
		if err := jsonx.Decode(inboxSnap, &inbox); err != nil {
			return err
		}
	}
	inboxSummary := make([]string, len(inbox.Items))
	for i, item := range inbox.Items {
		inboxSummary[i] = fmt.Sprintf("%d %s", item.Id, item.Mode)
	}
	_, _ = fmt.Fprintf(out, "inbox: %v\n", inboxSummary)

	release()
	for _, pair := range []struct {
		name string
		sub  durable.Submission
	}{
		{"first", first},
		{"follow-up", followUp},
		{"steer", steer},
		{"note", noteSub},
		{"withdrawn", withdrawn},
	} {
		rec, err := pair.sub.Wait(ctx)
		if err != nil {
			return err
		}
		reason := ""
		if rec.Reason != nil {
			reason = *rec.Reason
		}
		_, _ = fmt.Fprintf(out, "%s: %s %s\n", pair.name, rec.Status, reason)
	}

	page, err := root.Entries(durable.ConversationEntryQuery{}, 20, nil, ctx)
	if err != nil {
		return err
	}
	items := slices.Clone(page.Items)
	slices.Reverse(items)
	kinds := make([]string, len(items))
	for i, e := range items {
		kinds[i] = e.Kind
	}
	_, _ = fmt.Fprintf(out, "transcript: %v\n", kinds)
	return harness.Close(ctx)
}
