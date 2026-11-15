// Ported from packages/coding-agent/src/core/models-store.ts (pi v1.5.2).

package core

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type mstStoredModels = map[string]*ai.ModelsStoreEntry

type mstModelsFileReload struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	result  mstStoredModels
	err     error
	readers int
}

type mstModelsFileReadState struct {
	mu       sync.Mutex
	data     mstStoredModels
	revision *string
	reload   *mstModelsFileReload
}

var (
	mstSharedStateMu   sync.Mutex
	mstSharedStatePath string
	mstSharedState     *mstModelsFileReadState
)

func mstGetSharedReadState(path string) *mstModelsFileReadState {
	mstSharedStateMu.Lock()
	defer mstSharedStateMu.Unlock()
	if mstSharedState != nil && mstSharedStatePath == path {
		return mstSharedState
	}
	state := &mstModelsFileReadState{data: make(mstStoredModels)}
	if mstSharedState == nil {
		mstSharedStatePath = path
		mstSharedState = state
	}
	return state
}

// InMemoryCodingAgentModelsStore keeps dynamically refreshed provider catalogs in memory.
type InMemoryCodingAgentModelsStore struct {
	mu      sync.RWMutex
	entries map[string]*ai.ModelsStoreEntry
}

// NewInMemoryCodingAgentModelsStore returns an empty in-memory ModelsStore.
func NewInMemoryCodingAgentModelsStore() *InMemoryCodingAgentModelsStore {
	return &InMemoryCodingAgentModelsStore{
		entries: make(map[string]*ai.ModelsStoreEntry),
	}
}

func (s *InMemoryCodingAgentModelsStore) Read(ctx context.Context, providerId string, _ *ai.ModelsStoreOperationOptions) (*ai.ModelsStoreEntry, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s.mu.RLock()
	entry := s.entries[providerId]
	s.mu.RUnlock()
	return mstCloneEntry(entry), nil
}

func (s *InMemoryCodingAgentModelsStore) Write(ctx context.Context, providerId string, entry *ai.ModelsStoreEntry, _ *ai.ModelsStoreOperationOptions) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	cloned := mstCloneEntry(entry)
	s.mu.Lock()
	if cloned == nil {
		delete(s.entries, providerId)
	} else {
		s.entries[providerId] = cloned
	}
	s.mu.Unlock()
	return nil
}

func (s *InMemoryCodingAgentModelsStore) Delete(ctx context.Context, providerId string, _ *ai.ModelsStoreOperationOptions) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	delete(s.entries, providerId)
	s.mu.Unlock()
	return nil
}

// FileModelsStore provides locked JSON-backed storage for dynamically refreshed provider catalogs.
type FileModelsStore struct {
	storage   AuthStorageBackend
	path      string
	readState *mstModelsFileReadState
}

// NewFileModelsStore creates a FileModelsStore at the given path (or <agentDir>/models-store.json by default).
func NewFileModelsStore(pathArg ...string) *FileModelsStore {
	rawPath := filepath.Join(utils.GetAgentDir(), "models-store.json")
	if len(pathArg) > 0 && pathArg[0] != "" {
		rawPath = pathArg[0]
	}
	p, err := utils.NormalizePath(rawPath, nil)
	if err != nil {
		p = rawPath
	}
	return &FileModelsStore{
		storage:   NewFileAuthStorageBackend(p),
		path:      p,
		readState: mstGetSharedReadState(p),
	}
}

func (s *FileModelsStore) parse(content *string) (mstStoredModels, error) {
	if content == nil || *content == "" {
		return make(mstStoredModels), nil
	}
	var data mstStoredModels
	if err := jsonx.Unmarshal([]byte(utils.StripBom(*content)), &data); err != nil {
		return nil, err
	}
	if data == nil {
		data = make(mstStoredModels)
	}
	return data, nil
}

func (s *FileModelsStore) updateReadState(data mstStoredModels, revision *string) {
	s.readState.mu.Lock()
	s.readState.data = data
	s.readState.revision = revision
	s.readState.mu.Unlock()
}

func (s *FileModelsStore) reloadFromStorage(ctx context.Context) (mstStoredModels, error) {
	res, err := s.storage.WithLockAsync(ctx, func(content *string) (AuthStorageLockResult, error) {
		data, err := s.parse(content)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		s.updateReadState(data, utils.GetFileRevision(s.path))
		return AuthStorageLockResult{Result: data}, nil
	}, nil)
	if err != nil {
		return nil, err
	}
	data, _ := res.(mstStoredModels)
	if data == nil {
		return make(mstStoredModels), nil
	}
	return data, nil
}

func (s *FileModelsStore) readLatest(ctx context.Context) (mstStoredModels, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	rev := utils.GetFileRevision(s.path)

	s.readState.mu.Lock()
	if rev != nil && s.readState.revision != nil && *rev == *s.readState.revision {
		data := s.readState.data
		s.readState.mu.Unlock()
		return data, nil
	}
	reload := s.readState.reload
	if reload == nil {
		reloadCtx, cancel := context.WithCancel(context.Background())
		reload = &mstModelsFileReload{
			cancel: cancel,
			done:   make(chan struct{}),
		}
		s.readState.reload = reload
		go func(r *mstModelsFileReload) {
			data, err := s.reloadFromStorage(reloadCtx)
			r.result = data
			r.err = err
			close(r.done)

			s.readState.mu.Lock()
			if s.readState.reload == r {
				s.readState.reload = nil
			}
			s.readState.mu.Unlock()
		}(reload)
	}
	reload.mu.Lock()
	reload.readers++
	reload.mu.Unlock()
	s.readState.mu.Unlock()

	defer func() {
		reload.mu.Lock()
		reload.readers--
		noReaders := reload.readers == 0
		reload.mu.Unlock()
		if noReaders {
			s.readState.mu.Lock()
			if s.readState.reload == reload {
				s.readState.reload = nil
				reload.cancel()
			}
			s.readState.mu.Unlock()
		}
	}()

	if ctx != nil {
		select {
		case <-reload.done:
			return reload.result, reload.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	<-reload.done
	return reload.result, reload.err
}

func (s *FileModelsStore) Read(ctx context.Context, providerId string, _ *ai.ModelsStoreOperationOptions) (*ai.ModelsStoreEntry, error) {
	data, err := s.readLatest(ctx)
	if err != nil {
		return nil, err
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return mstCloneEntry(data[providerId]), nil
}

func (s *FileModelsStore) Write(ctx context.Context, providerId string, entry *ai.ModelsStoreEntry, _ *ai.ModelsStoreOperationOptions) error {
	var latest mstStoredModels
	_, err := s.storage.WithLockAsync(ctx, func(content *string) (AuthStorageLockResult, error) {
		current, err := s.parse(content)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		current[providerId] = mstCloneEntry(entry)
		latest = current
		formatted, err := jsonx.MarshalIndent(current, "  ")
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		next := string(formatted)
		return AuthStorageLockResult{Next: &next}, nil
	}, nil)
	if err != nil {
		return err
	}
	if latest != nil {
		s.updateReadState(latest, nil)
	}
	return nil
}

func (s *FileModelsStore) Delete(ctx context.Context, providerId string, _ *ai.ModelsStoreOperationOptions) error {
	var latest mstStoredModels
	_, err := s.storage.WithLockAsync(ctx, func(content *string) (AuthStorageLockResult, error) {
		current, err := s.parse(content)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		delete(current, providerId)
		latest = current
		formatted, err := jsonx.MarshalIndent(current, "  ")
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		next := string(formatted)
		return AuthStorageLockResult{Next: &next}, nil
	}, nil)
	if err != nil {
		return err
	}
	if latest != nil {
		s.updateReadState(latest, nil)
	}
	return nil
}

func mstCloneEntry(entry *ai.ModelsStoreEntry) *ai.ModelsStoreEntry {
	if entry == nil {
		return nil
	}
	raw, err := jsonx.Marshal(entry)
	if err != nil {
		return nil
	}
	var cloned ai.ModelsStoreEntry
	if err := jsonx.Unmarshal(raw, &cloned); err != nil {
		return nil
	}
	return &cloned
}
