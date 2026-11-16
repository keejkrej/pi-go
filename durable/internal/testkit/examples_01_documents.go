// Ported from packages/durable/test/examples/01-documents.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"

	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example01Documents runs the 01-documents example.
func Example01Documents(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	session := durable.NewSession(storage.NewMemoryStorage())
	defer func() { _ = session.Close(ctx) }()

	notes, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "example.notes",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
	})
	if err != nil {
		return err
	}

	var chat *durable.ConversationRecord
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if err != nil {
			return err
		}
		chat = c
		return nil
	}); err != nil {
		return err
	}

	var firstEntry, secondEntry *durable.EntryRecord
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		entry, err := tx.AppendEntry(chat.Id, durable.EntryDraft{Kind: "note", Data: "hello"})
		if err != nil {
			return err
		}
		d, err := tx.Doc(notes, chat.Id)
		if err != nil {
			return err
		}
		d.Value.Set("text", "after hello")
		firstEntry = entry
		return nil
	}); err != nil {
		return err
	}

	if err := session.Commit(ctx, func(tx durable.Tx) error {
		entry, err := tx.AppendEntry(chat.Id, durable.EntryDraft{Kind: "note", Data: "goodbye"})
		if err != nil {
			return err
		}
		d, err := tx.Doc(notes, chat.Id)
		if err != nil {
			return err
		}
		d.Value.Set("text", "after goodbye")
		secondEntry = entry
		return nil
	}); err != nil {
		return err
	}

	latest, err := session.Snapshot(ctx, notes, chat.Id)
	if err != nil {
		return err
	}
	atFirst, err := session.SnapshotAsOf(ctx, notes, chat.Id, firstEntry.Id)
	if err != nil {
		return err
	}
	atSecond, err := session.SnapshotAsOf(ctx, notes, chat.Id, secondEntry.Id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "latest notes: %v\nnotes at first entry: %v\nnotes at second entry: %v\n", latest, atFirst, atSecond)
	return session.Close(ctx)
}
