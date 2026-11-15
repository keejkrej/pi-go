// Ported from packages/coding-agent/src/core/runtime-credentials.ts (pi v1.5.2).

package core

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/ai"
)

// RuntimeCredentials wraps an ai.CredentialStore with non-persistent runtime API key overrides.
type RuntimeCredentials struct {
	store ai.CredentialStore

	mu                   sync.RWMutex
	overrides            map[string]string
	overrideOrder        []string
	pendingModifications map[uint64]chan struct{}
	nextModID            uint64
}

// NewRuntimeCredentials constructs a RuntimeCredentials overlay backed by store.
func NewRuntimeCredentials(store ai.CredentialStore) *RuntimeCredentials {
	return &RuntimeCredentials{
		store:                store,
		overrides:            make(map[string]string),
		pendingModifications: make(map[uint64]chan struct{}),
	}
}

// SetRuntimeApiKey sets a transient runtime API key override for providerId.
func (r *RuntimeCredentials) SetRuntimeApiKey(providerId string, apiKey string) {
	r.mu.Lock()
	if _, exists := r.overrides[providerId]; !exists {
		r.overrideOrder = append(r.overrideOrder, providerId)
	}
	r.overrides[providerId] = apiKey
	r.mu.Unlock()
}

// RemoveRuntimeApiKey removes any transient runtime API key override for providerId.
func (r *RuntimeCredentials) RemoveRuntimeApiKey(providerId string) {
	r.mu.Lock()
	if _, exists := r.overrides[providerId]; exists {
		delete(r.overrides, providerId)
		for i, id := range r.overrideOrder {
			if id == providerId {
				r.overrideOrder = append(r.overrideOrder[:i], r.overrideOrder[i+1:]...)
				break
			}
		}
	}
	r.mu.Unlock()
}

// HasRuntimeApiKey reports whether a runtime API key override is registered for providerId.
func (r *RuntimeCredentials) HasRuntimeApiKey(providerId string) bool {
	r.mu.RLock()
	_, ok := r.overrides[providerId]
	r.mu.RUnlock()
	return ok
}

// Read returns the runtime API key override if non-empty, or delegates to the underlying store.
func (r *RuntimeCredentials) Read(ctx context.Context, providerId string, options *ai.AuthOperationOptions) (ai.Credential, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	r.mu.RLock()
	override := r.overrides[providerId]
	r.mu.RUnlock()
	if override != "" {
		key := override
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}
	return r.store.Read(ctx, providerId, options)
}

// List enumerates stored credentials overlaid with runtime API key overrides.
func (r *RuntimeCredentials) List(ctx context.Context, options *ai.AuthOperationOptions) ([]ai.CredentialInfo, error) {
	baseEntries, err := r.store.List(ctx, options)
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	r.mu.RLock()
	overridesCopy := append([]string(nil), r.overrideOrder...)
	r.mu.RUnlock()

	entries := make([]ai.CredentialInfo, 0, len(baseEntries)+len(overridesCopy))
	idxByProvider := make(map[string]int, len(baseEntries)+len(overridesCopy))
	for _, entry := range baseEntries {
		idxByProvider[entry.ProviderId] = len(entries)
		entries = append(entries, entry)
	}
	for _, providerId := range overridesCopy {
		info := ai.CredentialInfo{ProviderId: providerId, Type: ai.AuthTypeApiKey}
		if idx, exists := idxByProvider[providerId]; exists {
			entries[idx] = info
		} else {
			idxByProvider[providerId] = len(entries)
			entries = append(entries, info)
		}
	}
	return entries, nil
}

// Modify delegates credential mutation to the underlying store while tracking completion for Settled.
func (r *RuntimeCredentials) Modify(
	ctx context.Context,
	providerId string,
	fn func(current ai.Credential) (ai.Credential, error),
	options *ai.AuthOperationOptions,
) (ai.Credential, error) {
	done := make(chan struct{})
	r.mu.Lock()
	r.nextModID++
	id := r.nextModID
	r.pendingModifications[id] = done
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.pendingModifications, id)
		r.mu.Unlock()
		close(done)
	}()

	return r.store.Modify(ctx, providerId, fn, options)
}

// Settled waits until all Modify calls started so far have completed or failed.
func (r *RuntimeCredentials) Settled(ctx ...context.Context) error {
	var c context.Context
	if len(ctx) > 0 {
		c = ctx[0]
	}
	r.mu.RLock()
	waitChans := make([]chan struct{}, 0, len(r.pendingModifications))
	for _, ch := range r.pendingModifications {
		waitChans = append(waitChans, ch)
	}
	r.mu.RUnlock()

	for _, ch := range waitChans {
		if c != nil {
			select {
			case <-ch:
			case <-c.Done():
				return c.Err()
			}
		} else {
			<-ch
		}
	}
	return nil
}

// Delete removes stored credentials and any runtime override for providerId.
func (r *RuntimeCredentials) Delete(ctx context.Context, providerId string, options *ai.AuthOperationOptions) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := r.store.Delete(ctx, providerId, options); err != nil {
		return err
	}
	r.RemoveRuntimeApiKey(providerId)
	return nil
}
