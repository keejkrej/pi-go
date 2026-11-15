// Ported from packages/durable/test/memory-storage.test.ts (pi v1.0.0).

package storage_test

import (
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/durabletest"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestMemoryStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "MemoryStorage", func(use func(durable.Storage) error) error {
		return use(storage.NewMemoryStorage())
	})
}

func TestMemoryStoragePreparedCommitDetachment(t *testing.T) {
	ctx := chord.BackgroundContext
	store := storage.NewMemoryStorage()
	defer func() { _ = store.Close(ctx) }()

	if _, err := store.Commit(ctx, []durable.StorageWrite{
		durable.StorageWriteConversation{
			Type:  durable.StorageWriteTypeConversation,
			Value: durable.ConversationRecord{Id: durable.RootConversationId},
		},
	}); err != nil {
		t.Fatalf("root commit failed: %v", err)
	}

	entryId := durable.IdFromNumber[durable.EntryId](2)
	prepared, err := store.PrepareCommit([]durable.StorageWrite{
		durable.StorageWriteEntry{
			Type: durable.StorageWriteTypeEntry,
			Value: durable.EntryRecord{
				Id:             entryId,
				ConversationId: durable.RootConversationId,
				Kind:           "test",
				Data:           jsonx.ObjectOf("nested", []any{float64(1)}),
			},
		},
	})
	if err != nil {
		t.Fatalf("PrepareCommit failed: %v", err)
	}

	exposed, ok := prepared.Writes[0].(durable.StorageWriteEntry)
	if !ok {
		t.Fatalf("Expected StorageWriteEntry, got %T", prepared.Writes[0])
	}
	obj, ok := exposed.Value.Data.(*jsonx.Object)
	if !ok {
		t.Fatalf("Expected *jsonx.Object, got %T", exposed.Value.Data)
	}
	nestedAny, _ := obj.Get("nested")
	nestedSlice, ok := nestedAny.([]any)
	if !ok {
		t.Fatalf("Expected []any nested slice, got %T", nestedAny)
	}
	// Mutate caller-visible slice; retained storage state must remain detached.
	nestedSlice[0] = float64(99)

	if seq := prepared.Apply(); seq != durable.SeqFromNumber(2) {
		t.Fatalf("expected seq 2, got %d", seq)
	}
	if seq := prepared.Apply(); seq != durable.SeqFromNumber(2) {
		t.Fatalf("expected idempotent seq 2, got %d", seq)
	}

	stored, err := store.Entry(ctx, entryId)
	if err != nil || stored == nil {
		t.Fatalf("Entry read failed: stored=%v err=%v", stored, err)
	}
	want := jsonx.ObjectOf("nested", []any{float64(1)})
	if !jsonx.DeepEqual(stored.Entry.Data, want) {
		t.Fatalf("expected %#v, got %#v", want, stored.Entry.Data)
	}
}
