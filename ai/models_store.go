// Ported from packages/ai/src/models_store.ts (pi v1.5.2).

package ai

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
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

func msCloneEntry(entry *ModelsStoreEntry) (*ModelsStoreEntry, error) {
	if entry == nil {
		return nil, nil
	}
	raw, err := jsonx.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var cloned ModelsStoreEntry
	if err := jsonx.Unmarshal(raw, &cloned); err != nil {
		return nil, err
	}
	if cloned.Models == nil {
		cloned.Models = AnyModelList{}
	}
	return &cloned, nil
}

func (s *InMemoryModelsStore) Read(ctx context.Context, providerId string, _ *ModelsStoreOperationOptions) (*ModelsStoreEntry, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, uaAbortReason(ctx)
	}
	s.mu.Lock()
	entry := s.entries[providerId]
	s.mu.Unlock()
	return msCloneEntry(entry)
}

func (s *InMemoryModelsStore) Write(ctx context.Context, providerId string, entry *ModelsStoreEntry, _ *ModelsStoreOperationOptions) error {
	if ctx != nil && ctx.Err() != nil {
		return uaAbortReason(ctx)
	}
	cloned, err := msCloneEntry(entry)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string]*ModelsStoreEntry{}
	}
	s.entries[providerId] = cloned
	s.mu.Unlock()
	return nil
}

func (s *InMemoryModelsStore) Delete(ctx context.Context, providerId string, _ *ModelsStoreOperationOptions) error {
	if ctx != nil && ctx.Err() != nil {
		return uaAbortReason(ctx)
	}
	s.mu.Lock()
	delete(s.entries, providerId)
	s.mu.Unlock()
	return nil
}

var _ ModelsStore = (*InMemoryModelsStore)(nil)
