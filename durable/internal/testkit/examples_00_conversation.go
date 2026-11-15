// Ported from packages/durable/test/examples/00-conversation.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"

	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
)

// Example00Conversation runs the 00-conversation example.
func Example00Conversation(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	session := durable.NewSession(storage.NewMemoryStorage())
	defer func() { _ = session.Close(ctx) }()

	var standalone *durable.ConversationRecord
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if err != nil {
			return err
		}
		standalone = created
		return nil
	}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "standalone conversation: %+v\n", standalone)
	return session.Close(ctx)
}
