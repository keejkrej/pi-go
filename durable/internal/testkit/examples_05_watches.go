// Ported from packages/durable/test/examples/05-watches.ts (pi v1.0.0).

package testkit

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example05Watches runs the 05-watches example.
func Example05Watches(out io.Writer) error {
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

	notesWatch, err := session.WatchDoc(ctx, notes, chat.Id)
	if err != nil {
		return err
	}
	if notesWatch == nil {
		return errors.New("notes are absent")
	}
	_, _ = fmt.Fprintf(out, "watch baseline: %v\n", notesWatch.Value())

	var once sync.Once
	delivered := make(chan struct{})
	if err := notesWatch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
		_, _ = fmt.Fprintf(out, "watch update: %v\n", value)
		once.Do(func() { close(delivered) })
		return nil
	}); err != nil {
		return err
	}
	defer func() { _ = notesWatch.Stop() }()

	if err := session.Commit(ctx, func(tx durable.Tx) error {
		d, err := tx.Doc(notes, chat.Id)
		if err != nil {
			return err
		}
		d.Value.Set("text", "observed asynchronously")
		return nil
	}); err != nil {
		return err
	}
	<-delivered
	_ = notesWatch.Stop()
	return session.Close(ctx)
}
