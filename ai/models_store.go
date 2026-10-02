// Ported from packages/ai/src/models-store.ts (pi v1.0.0).

package ai

import (
	"context"
	"sync"
)

// ModelsStoreEntry is one provider's persisted catalog.
// Models holds every model type. LastModified is the remote Last-Modified
// header as epoch milliseconds (Date.parse). CheckedAt is the last completed
// remote check, epoch milliseconds. Etag is the remote ETag stored verbatim,
// quotes included, and echoed as If-None-Match.
type ModelsStoreEntry struct {
	Models       AnyModelList `json:"models"`
	LastModified *int64       `json:"lastModified,omitzero"`
	CheckedAt    *int64       `json:"checkedAt,omitzero"`
	Etag         *string      `json:"etag,omitzero"`
}

// ModelsStoreOperationOptions is the TS options bag.
// Cancellation is the ctx argument; the bag has no other fields.
type ModelsStoreOperationOptions struct{}

// ModelsStore is a persistent model catalog keyed by provider id.
// A missing entry is a nil *ModelsStoreEntry and a nil error.
type ModelsStore interface {
	Read(ctx context.Context, providerId string, options *ModelsStoreOperationOptions) (*ModelsStoreEntry, error)
	Write(ctx context.Context, providerId string, entry *ModelsStoreEntry, options *ModelsStoreOperationOptions) error
	Delete(ctx context.Context, providerId string, options *ModelsStoreOperationOptions) error
}

// InMemoryModelsStore is a process-local ModelsStore.
// mu guards entries. Read, write, and delete copy entries the way structuredClone does.
type InMemoryModelsStore struct {
	mu      sync.Mutex
	entries map[string]*ModelsStoreEntry
}

// NewInMemoryModelsStore returns an empty store.
func NewInMemoryModelsStore() *InMemoryModelsStore {
	return &InMemoryModelsStore{entries: map[string]*ModelsStoreEntry{}}
}

func (s *InMemoryModelsStore) Read(ctx context.Context, providerId string, options *ModelsStoreOperationOptions) (*ModelsStoreEntry, error) {
	panic("unported: InMemoryModelsStore.Read")
}

func (s *InMemoryModelsStore) Write(ctx context.Context, providerId string, entry *ModelsStoreEntry, options *ModelsStoreOperationOptions) error {
	panic("unported: InMemoryModelsStore.Write")
}

func (s *InMemoryModelsStore) Delete(ctx context.Context, providerId string, options *ModelsStoreOperationOptions) error {
	panic("unported: InMemoryModelsStore.Delete")
}

var _ ModelsStore = (*InMemoryModelsStore)(nil)
