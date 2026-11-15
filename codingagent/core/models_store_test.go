package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

func tmstSampleModel(provider, id string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      "openai-responses",
			Provider: ai.ProviderId(provider),
			BaseUrl:  "https://example.invalid/v1",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		ContextWindow: 128_000,
		MaxTokens:     4096,
	}
}

type tmstMockStorageBackend struct {
	withLock      func(fn func(current *string) (AuthStorageLockResult, error)) (any, error)
	withLockAsync func(ctx context.Context, fn func(current *string) (AuthStorageLockResult, error), options *ai.AuthOperationOptions) (any, error)
}

func (m *tmstMockStorageBackend) WithLock(fn func(current *string) (AuthStorageLockResult, error)) (any, error) {
	if m.withLock != nil {
		return m.withLock(fn)
	}
	return m.withLockAsync(context.Background(), fn, nil)
}

func (m *tmstMockStorageBackend) WithLockAsync(ctx context.Context, fn func(current *string) (AuthStorageLockResult, error), opts *ai.AuthOperationOptions) (any, error) {
	return m.withLockAsync(ctx, fn, opts)
}

func TestInMemoryModelsStoreReadWriteDeleteAndIsolation(t *testing.T) {
	store := NewInMemoryCodingAgentModelsStore()
	entry := &ai.ModelsStoreEntry{
		Models: ai.AnyModelList{tmstSampleModel("openai", "gpt-one")},
	}
	if err := store.Write(context.Background(), "openai", entry, nil); err != nil {
		t.Fatal(err)
	}

	got, err := store.Read(context.Background(), "openai", nil)
	if err != nil || got == nil || len(got.Models) != 1 {
		t.Fatalf("unexpected read result: got=%+v err=%v", got, err)
	}
	// Mutating the returned slice must not mutate stored state.
	got.Models = nil
	gotAgain, err := store.Read(context.Background(), "openai", nil)
	if err != nil || gotAgain == nil || len(gotAgain.Models) != 1 {
		t.Fatalf("expected isolated clone on read, got=%+v err=%v", gotAgain, err)
	}

	if err := store.Delete(context.Background(), "openai", nil); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := store.Read(context.Background(), "openai", nil)
	if err != nil || afterDelete != nil {
		t.Fatalf("expected nil after delete, got=%+v err=%v", afterDelete, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Read(canceled, "openai", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on cancelled read, got %v", err)
	}
}

func TestFileModelsStorePersistenceBomAndCoalescedReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	storeA := NewFileModelsStore(path)
	storeB := NewFileModelsStore(path)

	if err := storeA.Write(context.Background(), "one", &ai.ModelsStoreEntry{
		Models: ai.AnyModelList{tmstSampleModel("one", "m1")},
	}, nil); err != nil {
		t.Fatal(err)
	}
	gotB, err := storeB.Read(context.Background(), "one", nil)
	if err != nil || gotB == nil || len(gotB.Models) != 1 {
		t.Fatalf("expected storeB to observe storeA write, got=%+v err=%v", gotB, err)
	}

	// External BOM-prefixed update invalidates file revision cache.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updatedJson := "\ufeff" + strings.ReplaceAll(string(raw), `"m1"`, `"m2"`)
	if err := os.WriteFile(path, []byte(updatedJson), 0o600); err != nil {
		t.Fatal(err)
	}
	// Ensure mtime/revision updates cleanly if filesystem clock precision requires.
	_ = os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second))

	afterExternal, err := storeA.Read(context.Background(), "one", nil)
	if err != nil || afterExternal == nil || len(afterExternal.Models) != 1 {
		t.Fatalf("expected reload after external BOM edit, got=%+v err=%v", afterExternal, err)
	}
	if m, ok := afterExternal.Models[0].(*ai.Model); !ok || m.Id != "m2" {
		t.Fatalf("expected model id m2 after external edit, got %+v", afterExternal.Models[0])
	}

	// Coalesced concurrent reload: first caller cancels while second caller completes with a single lock.
	freshPath := filepath.Join(t.TempDir(), "coalesce.json")
	if err := os.WriteFile(freshPath, []byte(`{"one":{"models":[{"id":"stored","name":"stored","api":"openai-responses","provider":"one","baseUrl":"https://example.invalid","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	coalesceStore := NewFileModelsStore(freshPath)
	grantLock := make(chan struct{})
	var lockCalls atomic.Int32
	coalesceStore.storage = &tmstMockStorageBackend{
		withLockAsync: func(_ context.Context, fn func(current *string) (AuthStorageLockResult, error), _ *ai.AuthOperationOptions) (any, error) {
			lockCalls.Add(1)
			<-grantLock
			data, err := os.ReadFile(freshPath)
			if err != nil {
				return nil, err
			}
			s := string(data)
			res, err := fn(&s)
			if err != nil {
				return nil, err
			}
			return res.Result, nil
		},
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2 := context.Background()

	type readRes struct {
		entry *ai.ModelsStoreEntry
		err   error
	}
	ch1 := make(chan readRes, 1)
	ch2 := make(chan readRes, 1)

	go func() {
		e, err := coalesceStore.Read(ctx1, "one", nil)
		ch1 <- readRes{e, err}
	}()
	go func() {
		e, err := coalesceStore.Read(ctx2, "one", nil)
		ch2 <- readRes{e, err}
	}()

	time.Sleep(15 * time.Millisecond)
	cancel1()
	r1 := <-ch1
	if !errors.Is(r1.err, context.Canceled) {
		t.Fatalf("expected first cancelled caller to return context.Canceled, got %v", r1.err)
	}

	close(grantLock)
	r2 := <-ch2
	if r2.err != nil || r2.entry == nil || len(r2.entry.Models) != 1 {
		t.Fatalf("expected second caller to resolve stored model, got=%+v err=%v", r2.entry, r2.err)
	}
	if lockCalls.Load() != 1 {
		t.Fatalf("expected coalesced lock call count=1, got %d", lockCalls.Load())
	}
}

func TestFileModelsStoreCancelsWriteWaitingForLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.json")
	initial := `{"one":{"models":[{"id":"existing","name":"existing","api":"openai-responses","provider":"one","baseUrl":"https://example.invalid","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileModelsStore(path)
	store.storage = &tmstMockStorageBackend{
		withLockAsync: func(ctx context.Context, _ func(current *string) (AuthStorageLockResult, error), _ *ai.AuthOperationOptions) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- store.Write(ctx, "two", &ai.ModelsStoreEntry{
			Models: ai.AnyModelList{tmstSampleModel("two", "cancelled")},
		}, nil)
	}()

	time.Sleep(15 * time.Millisecond)
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from waiting Write, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), `"existing"`) || strings.Contains(string(after), `"cancelled"`) {
		t.Fatalf("expected locked file unchanged after cancelled write, got: %s", string(after))
	}
}
