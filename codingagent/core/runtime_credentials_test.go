package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

func trcStrPtr(s string) *string { return &s }

func TestRuntimeCredentialsOverridesAndPersistence(t *testing.T) {
	ctx := context.Background()
	store := AuthStorageInMemory(map[string]ai.Credential{
		"anthropic": &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: trcStrPtr("stored-anth")},
	})
	rc := NewRuntimeCredentials(store)

	// Without override, delegates to store
	got, err := rc.Read(ctx, "anthropic", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ak, ok := got.(*ai.ApiKeyCredential); !ok || ak.Key == nil || *ak.Key != "stored-anth" {
		t.Fatalf("unexpected credential: %#v", got)
	}

	// Override shadows stored value without persisting
	rc.SetRuntimeApiKey("anthropic", "runtime-anth")
	rc.SetRuntimeApiKey("openai", "runtime-oai")
	if !rc.HasRuntimeApiKey("anthropic") || rc.HasRuntimeApiKey("google") {
		t.Fatal("HasRuntimeApiKey mismatch")
	}

	gotOverride, err := rc.Read(ctx, "anthropic", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ak := gotOverride.(*ai.ApiKeyCredential); *ak.Key != "runtime-anth" {
		t.Fatalf("expected runtime override, got %q", *ak.Key)
	}

	// Underlying store untouched
	storedAfter, err := store.Read(ctx, "anthropic", nil)
	if err != nil || *storedAfter.(*ai.ApiKeyCredential).Key != "stored-anth" {
		t.Fatalf("store modified unexpectedly: %#v, %v", storedAfter, err)
	}

	// List merges stored and runtime-only providers without duplicates
	list, err := rc.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ProviderId != "anthropic" || list[1].ProviderId != "openai" {
		t.Fatalf("list = %+v", list)
	}

	// Remove override reverts to stored value
	rc.RemoveRuntimeApiKey("anthropic")
	if rc.HasRuntimeApiKey("anthropic") {
		t.Fatal("expected override removed")
	}
	reverted, err := rc.Read(ctx, "anthropic", nil)
	if err != nil || *reverted.(*ai.ApiKeyCredential).Key != "stored-anth" {
		t.Fatalf("reverted = %#v, %v", reverted, err)
	}

	// Empty override falls back to store (mirrors JS `if (override)`)
	rc.SetRuntimeApiKey("anthropic", "")
	fallback, err := rc.Read(ctx, "anthropic", nil)
	if err != nil || *fallback.(*ai.ApiKeyCredential).Key != "stored-anth" {
		t.Fatalf("empty override fallback = %#v, %v", fallback, err)
	}

	// Delete deletes from store and clears runtime override
	rc.SetRuntimeApiKey("anthropic", "runtime-anth")
	if err := rc.Delete(ctx, "anthropic", nil); err != nil {
		t.Fatal(err)
	}
	if rc.HasRuntimeApiKey("anthropic") {
		t.Fatal("Delete should remove runtime override")
	}
	afterDel, err := rc.Read(ctx, "anthropic", nil)
	if err != nil || afterDel != nil {
		t.Fatalf("expected nil after Delete, got %#v, %v", afterDel, err)
	}
}

func TestRuntimeCredentialsSettledTracksConcurrentModify(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	store := AuthStorageFromStorage(&trcBlockingBackend{started: started, release: release})
	// Consume Reload's initial call or make trcBlockingBackend only block on Modify:
	rc := NewRuntimeCredentials(store)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = rc.Modify(context.Background(), "demo", func(cur ai.Credential) (ai.Credential, error) {
			return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: trcStrPtr("k")}, nil
		}, nil)
	}()

	<-started
	settledDone := make(chan error, 1)
	go func() {
		settledDone <- rc.Settled()
	}()

	select {
	case err := <-settledDone:
		t.Fatalf("Settled returned early while Modify in flight: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rc.Settled(ctxCanceled); err == nil {
		t.Fatal("expected canceled context error from Settled")
	}

	close(release)
	if err := <-settledDone; err != nil {
		t.Fatalf("Settled error: %v", err)
	}
	wg.Wait()
}

type trcBlockingBackend struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *trcBlockingBackend) WithLock(fn func(current *string) (AuthStorageLockResult, error)) (any, error) {
	return fn(nil)
}

func (b *trcBlockingBackend) WithLockAsync(
	_ context.Context,
	fn func(current *string) (AuthStorageLockResult, error),
	_ *ai.AuthOperationOptions,
) (any, error) {
	res, err := fn(nil)
	if res.Next != nil {
		b.once.Do(func() { close(b.started) })
		<-b.release
	}
	return res.Result, err
}
