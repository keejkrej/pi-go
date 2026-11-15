// Ported from packages/coding-agent/test/model-runtime-credential-sync.test.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

func tmrcsStrPtr(s string) *string { return &s }
func tmrcsBoolPtr(b bool) *bool    { return &b }

func tmrcsNewRuntime(t *testing.T, creds ai.CredentialStore) *ModelRuntime {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	if creds == nil {
		creds = ai.NewInMemoryCredentialStore()
	}
	rt, err := CreateModelRuntime(t.Context(), &CreateModelRuntimeOptions{
		Credentials:     creds,
		ModelsPath:      tmrcsStrPtr(filepath.Join(t.TempDir(), "models.json")),
		ModelsStore:     NewInMemoryCodingAgentModelsStore(),
		RefreshOnCreate: tmrcsBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRuntime: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

type tmrcsPromptInteraction struct {
	key string
}

func (i tmrcsPromptInteraction) Prompt(_ context.Context, _ ai.AuthPrompt) (string, error) {
	return i.key, nil
}
func (tmrcsPromptInteraction) Notify(ai.AuthEvent) {}

func TestModelRuntimeCredentialSyncSerializationAndConcurrency(t *testing.T) {
	t.Run("serializes concurrent Login operations on the same provider", func(t *testing.T) {
		rt := tmrcsNewRuntime(t, nil)
		var active atomic.Int32
		var overlap atomic.Bool
		var callCount atomic.Int32

		firstEntered := make(chan struct{})
		releaseFirst := make(chan struct{})

		if err := rt.RegisterNativeProvider(&ai.Provider{
			Id:   "same-prov",
			Name: "Same Prov",
			Auth: ai.ProviderAuth{
				ApiKey: &ai.ApiKeyAuth{
					Name: "API key",
					Login: func(ctx context.Context, _ ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
						n := callCount.Add(1)
						if cur := active.Add(1); cur > 1 {
							overlap.Store(true)
						}
						defer active.Add(-1)
						if n == 1 {
							close(firstEntered)
							<-releaseFirst
						}
						key := "key-" + string(rune('0'+n))
						return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
					},
					Check: func(_ context.Context, in ai.ApiKeyCheckInput) (*ai.AuthCheck, error) {
						if in.Credential != nil && in.Credential.Key != nil {
							s := "ok"
							return &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: &s}, nil
						}
						return nil, nil
					},
					Resolve: func(_ context.Context, in ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
						if in.Credential != nil && in.Credential.Key != nil {
							s := "ok"
							return &ai.AuthResult{Auth: ai.ModelAuth{ApiKey: in.Credential.Key}, Source: &s}, nil
						}
						return nil, nil
					},
				},
			},
		}); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		errs := make([]error, 2)
		go func() {
			defer wg.Done()
			_, errs[0] = rt.Login(t.Context(), "same-prov", ai.AuthTypeApiKey, tmrcsPromptInteraction{}, nil)
		}()
		<-firstEntered
		go func() {
			defer wg.Done()
			_, errs[1] = rt.Login(t.Context(), "same-prov", ai.AuthTypeApiKey, tmrcsPromptInteraction{}, nil)
		}()

		// Give goroutine 2 a moment to queue on the provider semaphore.
		time.Sleep(15 * time.Millisecond)
		if callCount.Load() != 1 {
			close(releaseFirst)
			t.Fatalf("second Login started concurrently while first was active! callCount=%d", callCount.Load())
		}
		close(releaseFirst)
		wg.Wait()

		if errs[0] != nil || errs[1] != nil || overlap.Load() {
			t.Fatalf("unexpected serialization failure errs=%v overlap=%v", errs, overlap.Load())
		}
	})

	t.Run("runs Login concurrently across different providers and queued cancellation aborts cleanly", func(t *testing.T) {
		rt := tmrcsNewRuntime(t, nil)

		provAEntered := make(chan struct{})
		provBEntered := make(chan struct{})
		releaseBoth := make(chan struct{})
		var releaseOnce sync.Once
		closeRelease := func() { releaseOnce.Do(func() { close(releaseBoth) }) }
		defer closeRelease()

		makeBarrierProv := func(id string, entered chan struct{}) *ai.Provider {
			return &ai.Provider{
				Id:   id,
				Name: id,
				Auth: ai.ProviderAuth{
					ApiKey: &ai.ApiKeyAuth{
						Name: "API key",
						Login: func(ctx context.Context, _ ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
							select {
							case <-entered:
							default:
								close(entered)
							}
							select {
							case <-releaseBoth:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
							k := "k-" + id
							return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
						},
						Resolve: func(_ context.Context, in ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
							if in.Credential != nil && in.Credential.Key != nil {
								s := "ok"
								return &ai.AuthResult{Auth: ai.ModelAuth{ApiKey: in.Credential.Key}, Source: &s}, nil
							}
							return nil, nil
						},
					},
				},
			}
		}
		_ = rt.RegisterNativeProvider(makeBarrierProv("prov-a", provAEntered))
		_ = rt.RegisterNativeProvider(makeBarrierProv("prov-b", provBEntered))

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = rt.Login(t.Context(), "prov-a", ai.AuthTypeApiKey, tmrcsPromptInteraction{}, nil)
		}()
		go func() {
			defer wg.Done()
			_, _ = rt.Login(t.Context(), "prov-b", ai.AuthTypeApiKey, tmrcsPromptInteraction{}, nil)
		}()

		select {
		case <-provAEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("prov-a Login never started")
		}
		select {
		case <-provBEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("prov-b Login blocked on prov-a")
		}

		// Queued wait on prov-a with cancelled ctx must fail before entering Login.
		queuedCtx, cancelQueued := context.WithTimeout(t.Context(), 15*time.Millisecond)
		defer cancelQueued()
		_, err := rt.Login(queuedCtx, "prov-a", ai.AuthTypeApiKey, tmrcsPromptInteraction{}, nil)
		closeRelease()
		if err == nil {
			t.Fatal("expected queued Login cancellation error")
		}
		wg.Wait()
	})
}

func TestModelRuntimeCredentialSyncPostCommitError(t *testing.T) {
	creds := ai.NewInMemoryCredentialStore()
	rt := tmrcsNewRuntime(t, creds)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Register a native provider whose Check cancels ctx or fails after Login commits credential.
	var loginDone atomic.Bool
	if err := rt.RegisterNativeProvider(&ai.Provider{
		Id:   "post-commit-prov",
		Name: "Post Commit",
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "API key",
				Login: func(context.Context, ai.AuthInteraction) (*ai.ApiKeyCredential, error) {
					loginDone.Store(true)
					k := "committed-secret"
					return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &k}, nil
				},
				Check: func(context.Context, ai.ApiKeyCheckInput) (*ai.AuthCheck, error) {
					if loginDone.Load() {
						// Cancel right after Login committed so synchronization fails cleanly.
						cancel()
						return nil, errors.New("sync check failed")
					}
					return nil, nil
				},
				Resolve: func(_ context.Context, in ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
					if in.Credential != nil && in.Credential.Key != nil {
						s := "ok"
						return &ai.AuthResult{Auth: ai.ModelAuth{ApiKey: in.Credential.Key}, Source: &s}, nil
					}
					return nil, nil
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := rt.Login(ctx, "post-commit-prov", ai.AuthTypeApiKey, tmrcsPromptInteraction{}, nil)
	if err == nil {
		t.Fatal("expected CredentialSynchronizationError, got nil")
	}
	var syncErr *CredentialSynchronizationError
	if !errors.As(err, &syncErr) {
		t.Fatalf("expected *CredentialSynchronizationError, got %T (%v)", err, err)
	}
	if syncErr.ProviderId != "post-commit-prov" || syncErr.Operation != CredentialSyncOpLogin || syncErr.Credential == nil {
		t.Fatalf("unexpected CredentialSynchronizationError fields: %+v", syncErr)
	}

	// Verify credential really remained committed in credential store despite sync error.
	stored, readErr := creds.Read(context.Background(), "post-commit-prov", nil)
	if readErr != nil || stored == nil {
		t.Fatalf("expected credential persisted in store, got (%v, %v)", stored, readErr)
	}
}
