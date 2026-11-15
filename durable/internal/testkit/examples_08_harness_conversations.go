// Ported from packages/durable/test/examples/08-harness-conversations.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example08HarnessConversations runs the 08-harness-conversations example.
func Example08HarnessConversations(out io.Writer) error {
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

	root, err := harness.Root(ctx)
	if err != nil {
		return err
	}
	if err := root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)}, ctx); err != nil {
		return err
	}

	message, err := durable.DefineEntry("message")
	if err != nil {
		return err
	}
	text := "hello"
	rawHello, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.AppendTypedEntry(message, root.Id(), durable.EntryDraft{
			Kind:  "message",
			Data:  jsonx.ObjectOf("from", "example"),
			Model: ai.MessageList{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &text}, Timestamp: 1}},
		})
	})
	if err != nil {
		return err
	}
	hello := rawHello.(*durable.EntryRecord)
	from := ""
	if obj, ok := hello.Data.(*jsonx.Object); ok {
		if raw, exists := obj.Get("from"); exists {
			from, _ = raw.(string)
		}
	}
	_, _ = fmt.Fprintf(out, "typed entry: %v %s\n", message.Is(hello), from)

	helper, err := harness.CreateConversation(durable.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		Agent:     &durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelMinimal)},
	}, ctx)
	if err != nil {
		return err
	}
	retry, err := root.Fork(hello.Id, durable.ConversationCreateOptions{
		Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
	}, ctx)
	if err != nil {
		return err
	}
	helperAgent, err := helper.Agent(ctx)
	if err != nil {
		return err
	}
	retryAgent, err := retry.Agent(ctx)
	if err != nil {
		return err
	}
	lookedUp, err := harness.Conversation(retry.Id(), ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "helper thinking: %s\nfork thinking: %s\nlookup: %v\n",
		helperAgent.ThinkingLevel, retryAgent.ThinkingLevel, lookedUp != nil && lookedUp.Id() == retry.Id())
	return harness.Close(ctx)
}
