// Ported from packages/durable/test/examples/04-chord-state.ts (pi v1.0.0).

package testkit

import (
	"errors"
	"fmt"
	"io"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example04ChordState runs the 04-chord-state example.
func Example04ChordState(out io.Writer) error {
	sw := NewSyncWriter(out)
	defer sw.Close()
	out = sw
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
		conv, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if err != nil {
			return err
		}
		d, err := tx.Doc(notes, conv.Id)
		if err != nil {
			return err
		}
		d.Value.Set("text", "first")
		chat = conv
		return nil
	}); err != nil {
		return err
	}

	notesState, err := session.DocumentState(ctx, notes, chat.Id)
	if err != nil {
		return err
	}
	if notesState == nil {
		return errors.New("notes are absent")
	}
	defer notesState.Dispose()

	stopNotes := notesState.Subscribe(func(value durable.JsonObject, _ chord.Context, delivery chord.ReplicatedStateDelivery) error {
		_, _ = fmt.Fprintf(out, "Chord notes: %s %d %v\n", delivery.Kind, delivery.Sequence, value)
		return nil
	})
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		d, err := tx.Doc(notes, chat.Id)
		if err != nil {
			return err
		}
		d.Value.Set("text", "published through Chord")
		return nil
	}); err != nil {
		return err
	}
	stopNotes()
	notesState.Dispose()
	return session.Close(ctx)
}
