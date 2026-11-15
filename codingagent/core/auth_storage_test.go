package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/lockfile"
)

func TestAuthStorageCreatePermissionsAndByteParity(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "nested", "agent")
	authPath := filepath.Join(dir, "auth.json")

	// Pre-populate auth.json with custom key order and unknown extra fields on alpha.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	initial := `{
  "zeta": {
    "type": "api_key",
    "key": "zeta-old"
  },
  "alpha": {
    "type": "api_key",
    "key": "alpha-keep",
    "customMeta": {
      "z": 1,
      "a": "keep-me"
    }
  }
}`
	if err := os.WriteFile(authPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	store := AuthStorageCreate(authPath)
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v, err=%v", info.Mode().Perm(), err)
		}
		if info, err := os.Stat(authPath); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %v, err=%v", info.Mode().Perm(), err)
		}
	}

	// Modifying zeta must preserve key order ("zeta" before "alpha") and alpha's untouched object byte-for-byte.
	if _, err := store.Modify(ctx, "zeta", func(_ ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tastStrPtr("zeta-new")}, nil
	}, nil); err != nil {
		t.Fatal(err)
	}

	rawBytes, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "zeta": {
    "type": "api_key",
    "key": "zeta-new"
  },
  "alpha": {
    "type": "api_key",
    "key": "alpha-keep",
    "customMeta": {
      "z": 1,
      "a": "keep-me"
    }
  }
}`
	if string(rawBytes) != want {
		t.Fatalf("auth.json mismatch:\ngot:\n%s\nwant:\n%s", string(rawBytes), want)
	}
}

func TestAuthStorageCreateInitializesFileAndMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-new")
	authPath := filepath.Join(dir, "auth.json")
	_ = AuthStorageCreate(authPath)

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Fatalf("initial auth.json = %q, want {}", string(raw))
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v, err=%v", info.Mode().Perm(), err)
		}
		if info, err := os.Stat(authPath); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %v, err=%v", info.Mode().Perm(), err)
		}
	}
}

func TestAuthStorageConfigValueResolutionAndReadOnly(t *testing.T) {
	ctx := context.Background()
	t.Setenv("PI_AUTH_ENV_KEY", "from-env")

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	content := `{
  "cmdprov": { "type": "api_key", "key": "!printf from-cmd" },
  "envprov": { "type": "api_key", "key": "$PI_AUTH_ENV_KEY" }
}`
	if err := os.WriteFile(authPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	rw := AuthStorageCreate(authPath)
	cmdCred, err := rw.Read(ctx, "cmdprov", nil)
	if err != nil || *cmdCred.(*ai.ApiKeyCredential).Key != "from-cmd" {
		t.Fatalf("cmdCred = %#v, err=%v", cmdCred, err)
	}
	envCred, err := rw.Read(ctx, "envprov", nil)
	if err != nil || *envCred.(*ai.ApiKeyCredential).Key != "from-env" {
		t.Fatalf("envCred = %#v, err=%v", envCred, err)
	}

	// ReadOnlyAuthStorage skips !command execution and rejects mutations.
	ro := NewReadOnlyAuthStorage(authPath)
	roCmd, err := ro.Read(ctx, "cmdprov", nil)
	if err != nil || *roCmd.(*ai.ApiKeyCredential).Key != "!printf from-cmd" {
		t.Fatalf("ReadOnlyAuthStorage executed command unexpectedly: %#v, err=%v", roCmd, err)
	}
	roEnv, err := ro.Read(ctx, "envprov", nil)
	if err != nil || *roEnv.(*ai.ApiKeyCredential).Key != "from-env" {
		t.Fatalf("ReadOnlyAuthStorage env = %#v, err=%v", roEnv, err)
	}
	if _, err := ro.Modify(ctx, "envprov", nil, nil); err != ErrReadOnlyAuthStorage {
		t.Fatalf("expected ErrReadOnlyAuthStorage from Modify, got %v", err)
	}
	if err := ro.Delete(ctx, "envprov", nil); err != ErrReadOnlyAuthStorage {
		t.Fatalf("expected ErrReadOnlyAuthStorage from Delete, got %v", err)
	}

	// ReadStoredCredential leaves values unresolved without AuthStorage instantiation.
	rawCred := ReadStoredCredential("cmdprov", authPath)
	if rawCred == nil || *rawCred.(*ai.ApiKeyCredential).Key != "!printf from-cmd" {
		t.Fatalf("ReadStoredCredential = %#v", rawCred)
	}
}

func TestAuthStorageCorruptFilePreservedAndContextCancellation(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	corrupt := "{corrupt-json"
	if err := os.WriteFile(authPath, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	store := AuthStorageCreate(authPath)
	if _, err := store.Modify(context.Background(), "p", func(_ ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tastStrPtr("k")}, nil
	}, nil); err == nil {
		t.Fatal("expected Modify to fail on corrupt auth.json")
	}

	after, err := os.ReadFile(authPath)
	if err != nil || string(after) != corrupt {
		t.Fatalf("corrupt auth.json altered: %q", string(after))
	}

	// Pre-canceled context aborts immediately without blocking.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Read(canceled, "p", nil); err == nil {
		t.Fatal("expected canceled error from Read")
	}
	if _, err := store.List(canceled, nil); err == nil {
		t.Fatal("expected canceled error from List")
	}
	if err := store.Delete(canceled, "p", nil); err == nil {
		t.Fatal("expected canceled error from Delete")
	}
}

func TestAuthStorageSymlinkLockSharingAndConcurrentModifications(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real-auth.json")
	linkPath := filepath.Join(dir, "link-auth.json")
	if err := os.WriteFile(realPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// Holding a realpath lock on realPath causes a canceled Read/Modify on linkPath to fail cleanly.
	release, err := lockfile.Lock(ctx, realPath, &lockfile.Options{Realpath: tastBoolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	storeLink := AuthStorageCreate(linkPath)
	ctxAbort, cancel := context.WithCancel(ctx)
	cancel()
	_, modifyErr := storeLink.Modify(ctxAbort, "prov", func(_ ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tastStrPtr("v")}, nil
	}, nil)
	_ = release()
	if modifyErr == nil {
		t.Fatal("expected Modify through symlink to respect context cancellation while target locked")
	}

	// Concurrent Modify calls across real and symlinked stores serialize without lost writes.
	storeReal := AuthStorageCreate(realPath)
	var wg sync.WaitGroup
	providers := []string{"p1", "p2", "p3", "p4"}
	for i, p := range providers {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			s := storeReal
			if idx%2 == 1 {
				s = storeLink
			}
			_, err := s.Modify(ctx, name, func(_ ai.Credential) (ai.Credential, error) {
				return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: tastStrPtr("key-" + name)}, nil
			}, nil)
			if err != nil {
				t.Errorf("concurrent Modify(%s): %v", name, err)
			}
		}(i, p)
	}
	wg.Wait()

	list, err := storeReal.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(providers) {
		t.Fatalf("expected %d providers persisted across concurrent writes, got %+v", len(providers), list)
	}
}

func TestAuthStorageCoalescedConcurrentReloads(t *testing.T) {
	ctx := context.Background()
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"p":{"type":"api_key","key":"v1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	inner := NewInMemoryAuthStorageBackend()
	_, _ = inner.WithLock(func(_ *string) (AuthStorageLockResult, error) {
		raw := `{"p":{"type":"api_key","key":"v2"}}`
		return AuthStorageLockResult{Next: &raw}, nil
	})
	counting := &tastCountingBackend{
		inner: inner,
		gate:  make(chan struct{}),
		hit:   make(chan struct{}),
	}
	close(counting.gate)

	store := astNewAuthStorage(counting, authPath)
	initialCalls := counting.calls.Load()

	// Update file on disk so GetFileRevision changes, forcing concurrent Reads through readLatestDoc reload.
	if err := os.WriteFile(authPath, []byte(`{"p":{"type":"api_key","key":"v2-longer"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	counting.mu.Lock()
	counting.gate = make(chan struct{})
	counting.mu.Unlock()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cred, err := store.Read(ctx, "p", nil)
			if err != nil {
				t.Errorf("concurrent Read error: %v", err)
				return
			}
			if ak, ok := cred.(*ai.ApiKeyCredential); !ok || ak.Key == nil || *ak.Key != "v2" {
				t.Errorf("unexpected cred from coalesced reload: %#v", cred)
			}
		}()
	}
	close(start)
	<-counting.hit
	close(counting.gate)
	wg.Wait()

	if diff := counting.calls.Load() - initialCalls; diff != 1 {
		t.Fatalf("expected 5 concurrent reads after revision change to coalesce into 1 backend load, got %d", diff)
	}
}

type tastCountingBackend struct {
	inner AuthStorageBackend
	calls tastAtomicInt64
	gate  chan struct{}
	hit   chan struct{}
	once  sync.Once
	mu    sync.Mutex
}

type tastAtomicInt64 struct {
	mu sync.Mutex
	v  int64
}

func (a *tastAtomicInt64) Add(d int64) {
	a.mu.Lock()
	a.v += d
	a.mu.Unlock()
}

func (a *tastAtomicInt64) Load() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

func (b *tastCountingBackend) WithLock(fn func(current *string) (AuthStorageLockResult, error)) (any, error) {
	return b.inner.WithLock(fn)
}

func (b *tastCountingBackend) WithLockAsync(
	ctx context.Context,
	fn func(current *string) (AuthStorageLockResult, error),
	options *ai.AuthOperationOptions,
) (any, error) {
	b.calls.Add(1)
	b.mu.Lock()
	gate := b.gate
	hit := b.hit
	b.mu.Unlock()
	if gate != nil {
		b.once.Do(func() {
			if hit != nil {
				close(hit)
			}
		})
		<-gate
	}
	return b.inner.WithLockAsync(ctx, fn, options)
}

func tastStrPtr(s string) *string { return &s }
func tastBoolPtr(b bool) *bool    { return &b }
