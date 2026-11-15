// Ported from packages/durable/test/examples/09-context.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func e0conAssistantMessage(text string, calls []string, stopReason ...ai.StopReason) *ai.AssistantMessage {
	content := ai.ContentList{&ai.TextContent{Type: "text", Text: text}}
	for _, id := range calls {
		content = append(content, &ai.ToolCall{Type: "toolCall", Id: id, Name: "read", Arguments: jsonx.NewObject()})
	}
	sr := ai.StopReasonStop
	if len(calls) > 0 {
		sr = ai.StopReasonToolUse
	}
	if len(stopReason) > 0 {
		sr = stopReason[0]
	}
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    content,
		Api:        "example",
		Provider:   "example",
		Model:      "example",
		StopReason: sr,
		Timestamp:  2,
	}
}

func e0conToolResultMessage(id string) *ai.ToolResultMessage {
	return &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: id,
		ToolName:   "read",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "file " + id}},
		Timestamp:  3,
	}
}

func e0conShow(msg ai.Message) string {
	switch m := msg.(type) {
	case *ai.UserMessage:
		if m.Content.Text != nil {
			return "user: " + *m.Content.Text
		}
		return "user: "
	case *ai.SystemMessage:
		raw, _ := jsonx.Stringify(m.Sections)
		return "system: " + raw
	case *ai.AssistantMessage:
		parts := make([]string, len(m.Content))
		for i, c := range m.Content {
			switch b := c.(type) {
			case *ai.TextContent:
				parts[i] = b.Text
			case *ai.ToolCall:
				parts[i] = "call(" + b.Id + ")"
			}
		}
		return "assistant: " + strings.Join(parts, " ")
	case *ai.ToolResultMessage:
		suffix := ""
		if m.IsError {
			suffix = " error"
		}
		return fmt.Sprintf("result(%s)%s", m.ToolCallId, suffix)
	default:
		return string(msg.GetRole())
	}
}

func e0conUser(text string, ts int64) *ai.UserMessage {
	return &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &text}, Timestamp: ts}
}

func e0conShowAll(msgs []ai.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = e0conShow(m)
	}
	return out
}

// Example09Context runs the 09-context example.
func Example09Context(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   ai.CreateModels(nil),
		Registry: durable.CreateRegistry(),
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	transcript, err := harness.CreateConversation(durable.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
	}, ctx)
	if err != nil {
		return err
	}
	say := func(kind string, model ...ai.Message) (*durable.EntryRecord, error) {
		raw, err := transcript.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(transcript.Id(), durable.EntryDraft{Kind: kind, Model: ai.MessageList(model)})
		})
		if err != nil {
			return nil, err
		}
		return raw.(*durable.EntryRecord), nil
	}

	question, err := say("message", e0conUser("read a and b", 1))
	if err != nil {
		return err
	}
	if _, err := say("message", e0conAssistantMessage("I crashed", nil, ai.StopReasonAborted)); err != nil {
		return err
	}
	calls, err := say("message", e0conAssistantMessage("reading", []string{"a", "b"}))
	if err != nil {
		return err
	}
	if _, err := say("message", e0conToolResultMessage("b")); err != nil {
		return err
	}
	sec := omap.NewMap[string, *string]()
	cwdVal := "<cwd>/repo"
	sec.Set("cwd", &cwdVal)
	if _, err := say("pi.system", &ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{}, Sections: sec, Timestamp: 4}); err != nil {
		return err
	}
	if _, err := say("message", e0conToolResultMessage("a")); err != nil {
		return err
	}
	if _, err := say("message", e0conAssistantMessage("a and b look fine", nil)); err != nil {
		return err
	}
	if _, err := transcript.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(transcript.Id(), durable.EntryDraft{
			Kind: "edit",
			Data: "user fixed a typo",
			Edits: []durable.ContextEdit{{
				Target:   question.Id,
				Action:   durable.ContextEditActionReplace,
				Messages: ai.MessageList{e0conUser("read files a and b", 1)},
			}},
		})
	}); err != nil {
		return err
	}
	if _, err := transcript.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(transcript.Id(), durable.EntryDraft{Kind: "note", Data: "display only"})
	}); err != nil {
		return err
	}

	view, err := transcript.Context(ctx)
	if err != nil {
		return err
	}
	entryKinds := make([]string, len(view.Entries))
	for i, e := range view.Entries {
		entryKinds[i] = e.Kind
	}
	_, _ = fmt.Fprintf(out, "raw active entries: %v\nrequest messages: %v\n", entryKinds, e0conShowAll(view.Messages))

	cut, err := transcript.Fork(calls.Id, durable.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
	}, ctx)
	if err != nil {
		return err
	}
	cutView, err := cut.Context(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "fork messages: %v\n", e0conShowAll(cutView.Messages))

	if _, err := transcript.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(transcript.Id(), durable.EntryDraft{
			Kind:  "summary",
			Head:  durable.EntryHeadSelf(),
			Model: ai.MessageList{e0conUser("Summary: a and b are fine.", 5)},
		})
	}); err != nil {
		return err
	}
	view, err = transcript.Context(ctx)
	if err != nil {
		return err
	}
	headKind := ""
	if view.Head != nil {
		headKind = view.Head.Kind
	}
	_, _ = fmt.Fprintf(out, "after summary: %s %v\n", headKind, e0conShowAll(view.Messages))

	history, err := transcript.Entries(durable.ConversationEntryQuery{}, 3, nil, ctx)
	if err != nil {
		return err
	}
	newestKinds := make([]string, len(history.Items))
	for i, e := range history.Items {
		newestKinds[i] = e.Kind
	}
	_, _ = fmt.Fprintf(out, "newest stored entries: %v more: %v\n", newestKinds, history.Next != nil)
	return harness.Close(ctx)
}
