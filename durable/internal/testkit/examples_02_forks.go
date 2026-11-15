// Ported from packages/durable/test/examples/02-forks.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"

	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example02Forks runs the 02-forks example.
func Example02Forks(out io.Writer) error {
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

	var firstEntry *durable.EntryRecord
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
		if _, err := tx.AppendEntry(chat.Id, durable.EntryDraft{Kind: "note", Data: "goodbye"}); err != nil {
			return err
		}
		d, err := tx.Doc(notes, chat.Id)
		if err != nil {
			return err
		}
		d.Value.Set("text", "after goodbye")
		return nil
	}); err != nil {
		return err
	}

	var branch *durable.ConversationRecord
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		b, err := tx.ForkConversation(chat.Id, firstEntry.Id, durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if err != nil {
			return err
		}
		branch = b
		return nil
	}); err != nil {
		return err
	}

	var branchEntries durable.Page[durable.EntryRecord, durable.Cursor]
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		p, err := tx.ScanEntries(durable.EntryQuery{ConversationId: branch.Id}, 10, nil)
		if err != nil {
			return err
		}
		branchEntries = p
		return nil
	}); err != nil {
		return err
	}
	var data []any
	for _, entry := range branchEntries.Items {
		data = append(data, entry.Data)
	}
	forkSnap, err := session.Snapshot(ctx, notes, branch.Id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "fork transcript: %v\nfork notes: %v\n", data, forkSnap)

	if err := session.Commit(ctx, func(tx durable.Tx) error {
		d, err := tx.Doc(notes, branch.Id)
		if err != nil {
			return err
		}
		d.Value.Set("text", "changed only in the fork")
		return nil
	}); err != nil {
		return err
	}
	forkAfter, err := session.Snapshot(ctx, notes, branch.Id)
	if err != nil {
		return err
	}
	parentAfter, err := session.Snapshot(ctx, notes, chat.Id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "fork notes after edit: %v\nparent notes after edit: %v\n", forkAfter, parentAfter)
	return session.Close(ctx)
}
