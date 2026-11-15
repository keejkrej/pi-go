// Ported from packages/coding-agent/test/footer-data-provider.test.ts (pi v1.5.2).

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

type tfdpFakeWatcher struct {
	path     string
	listener func(eventType, filename string)
	onError  func()
	closed   atomic.Bool
}

func (w *tfdpFakeWatcher) Close() error {
	w.closed.Store(true)
	return nil
}

type tfdpFakeWatchRegistry struct {
	mu       sync.Mutex
	watchers map[string][]*tfdpFakeWatcher
}

func tfdpNewFakeWatchRegistry() *tfdpFakeWatchRegistry {
	return &tfdpFakeWatchRegistry{
		watchers: make(map[string][]*tfdpFakeWatcher),
	}
}

func (r *tfdpFakeWatchRegistry) watch(path string, listener func(eventType, filename string), onError func()) fdpWatcher {
	w := &tfdpFakeWatcher{
		path:     path,
		listener: listener,
		onError:  onError,
	}
	r.mu.Lock()
	r.watchers[path] = append(r.watchers[path], w)
	r.mu.Unlock()
	return w
}

func (r *tfdpFakeWatchRegistry) latest(path string) *tfdpFakeWatcher {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.watchers[path]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func (r *tfdpFakeWatchRegistry) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.watchers[path])
}

func tfdpCreatePlainRepo(t *testing.T, tempDir string) string {
	t.Helper()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir
}

func tfdpCreatePlainReftableRepo(t *testing.T, tempDir string) string {
	t.Helper()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "reftable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git", "HEAD"), []byte("ref: refs/heads/.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir
}

type tfdpWorktreeFixture struct {
	worktreeDir string
	reftableDir string
}

func tfdpCreateReftableWorktree(t *testing.T, tempDir string) tfdpWorktreeFixture {
	t.Helper()
	repoDir := filepath.Join(tempDir, "repo")
	commonGitDir := filepath.Join(repoDir, ".git")
	gitDir := filepath.Join(commonGitDir, "worktrees", "src")
	worktreeDir := filepath.Join(tempDir, "worktree")
	reftableDir := filepath.Join(commonGitDir, "reftable")

	for _, dir := range []string{gitDir, reftableDir, worktreeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(worktreeDir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reftableDir, "tables.list"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return tfdpWorktreeFixture{worktreeDir: worktreeDir, reftableDir: reftableDir}
}

func tfdpWaitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFooterDataProvider_PlainRepoNestedDir(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := tfdpCreatePlainRepo(t, tempDir)
	nestedDir := filepath.Join(repoDir, "src", "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var syncCalls atomic.Int32
	provider := fdpNewWithOptions(nestedDir, fdpOptions{
		resolveGitSyncFn: func(string) *string {
			syncCalls.Add(1)
			return nil
		},
	})
	defer provider.Dispose()

	branch := provider.GetGitBranch()
	if branch == nil || *branch != "main" {
		t.Fatalf("expected branch 'main', got %v", branch)
	}
	if syncCalls.Load() != 0 {
		t.Fatalf("expected 0 git sync calls, got %d", syncCalls.Load())
	}
}

func TestFooterDataProvider_PlainReftableRepoResolvesViaGit(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := tfdpCreatePlainReftableRepo(t, tempDir)

	var gotRepoDir string
	var syncCalls atomic.Int32
	provider := fdpNewWithOptions(repoDir, fdpOptions{
		resolveGitSyncFn: func(dir string) *string {
			syncCalls.Add(1)
			gotRepoDir = dir
			return fdpStrPtr("main")
		},
	})
	defer provider.Dispose()

	branch := provider.GetGitBranch()
	if branch == nil || *branch != "main" {
		t.Fatalf("expected branch 'main', got %v", branch)
	}
	if syncCalls.Load() != 1 {
		t.Fatalf("expected 1 git sync call, got %d", syncCalls.Load())
	}
	if !strings.HasSuffix(gotRepoDir, "repo") {
		t.Fatalf("expected repoDir ending in 'repo', got %q", gotRepoDir)
	}
}

func TestFooterDataProvider_ReftableWorktreeResolvesViaGit(t *testing.T) {
	tempDir := t.TempDir()
	fix := tfdpCreateReftableWorktree(t, tempDir)

	provider := fdpNewWithOptions(fix.worktreeDir, fdpOptions{
		resolveGitSyncFn: func(string) *string {
			return fdpStrPtr("main")
		},
	})
	defer provider.Dispose()

	branch := provider.GetGitBranch()
	if branch == nil || *branch != "main" {
		t.Fatalf("expected branch 'main', got %v", branch)
	}
}

func TestFooterDataProvider_UnresolvedInvalidHeadIsDetached(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := tfdpCreatePlainReftableRepo(t, tempDir)

	provider := fdpNewWithOptions(repoDir, fdpOptions{
		resolveGitSyncFn: func(string) *string {
			return nil
		},
	})
	defer provider.Dispose()

	branch := provider.GetGitBranch()
	if branch == nil || *branch != "detached" {
		t.Fatalf("expected 'detached', got %v", branch)
	}
}

func TestFooterDataProvider_ReftableSameBranchDoesNotNotify(t *testing.T) {
	tempDir := t.TempDir()
	fix := tfdpCreateReftableWorktree(t, tempDir)
	reg := tfdpNewFakeWatchRegistry()

	var syncCalls, asyncCalls, notifyCalls atomic.Int32
	provider := fdpNewWithOptions(fix.worktreeDir, fdpOptions{
		watchDirFn: reg.watch,
		resolveGitSyncFn: func(string) *string {
			syncCalls.Add(1)
			return fdpStrPtr("main")
		},
		resolveGitAsyncFn: func(context.Context, string) *string {
			asyncCalls.Add(1)
			return fdpStrPtr("main")
		},
		debounceDelay:     30 * time.Millisecond,
		tablePollInterval: time.Hour,
	})
	defer provider.Dispose()

	if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
		t.Fatalf("expected 'main', got %v", branch)
	}
	syncCalls.Store(0)

	provider.OnBranchChange(func() {
		notifyCalls.Add(1)
	})

	rw := reg.latest(fix.reftableDir)
	if rw == nil {
		t.Fatal("expected reftableWatcher to be non-nil")
	}
	rw.listener("change", "tables.list")

	tfdpWaitFor(t, 2*time.Second, func() bool {
		return asyncCalls.Load() == 1
	})
	time.Sleep(40 * time.Millisecond)

	if syncCalls.Load() != 0 {
		t.Fatalf("expected 0 sync calls, got %d", syncCalls.Load())
	}
	if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
		t.Fatalf("expected branch 'main', got %v", branch)
	}
	if notifyCalls.Load() != 0 {
		t.Fatalf("expected 0 branch change notifications, got %d", notifyCalls.Load())
	}
}

func TestFooterDataProvider_DebouncesRapidReftableUpdates(t *testing.T) {
	tempDir := t.TempDir()
	fix := tfdpCreateReftableWorktree(t, tempDir)
	reg := tfdpNewFakeWatchRegistry()

	var asyncCalls atomic.Int32
	provider := fdpNewWithOptions(fix.worktreeDir, fdpOptions{
		watchDirFn: reg.watch,
		resolveGitSyncFn: func(string) *string {
			return fdpStrPtr("main")
		},
		resolveGitAsyncFn: func(context.Context, string) *string {
			asyncCalls.Add(1)
			return fdpStrPtr("main")
		},
		debounceDelay:     40 * time.Millisecond,
		tablePollInterval: time.Hour,
	})
	defer provider.Dispose()

	if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
		t.Fatalf("expected 'main', got %v", branch)
	}
	asyncCalls.Store(0)

	rw := reg.latest(fix.reftableDir)
	if rw == nil {
		t.Fatal("expected reftableWatcher")
	}
	rw.listener("change", "tables.list")
	rw.listener("change", "tables.list")
	rw.listener("change", "tables.list")

	time.Sleep(10 * time.Millisecond)
	if asyncCalls.Load() != 0 {
		t.Fatalf("expected debounce before delay, got %d calls", asyncCalls.Load())
	}

	tfdpWaitFor(t, 2*time.Second, func() bool {
		return asyncCalls.Load() == 1
	})
	time.Sleep(60 * time.Millisecond)
	if asyncCalls.Load() != 1 {
		t.Fatalf("expected single coalesced async call, got %d", asyncCalls.Load())
	}
}

func TestFooterDataProvider_UpdatesBranchOnReftableDirectoryChange(t *testing.T) {
	tempDir := t.TempDir()
	fix := tfdpCreateReftableWorktree(t, tempDir)

	var branchMu sync.Mutex
	resolvedBranch := "main"
	getBranch := func() *string {
		branchMu.Lock()
		defer branchMu.Unlock()
		b := resolvedBranch
		return &b
	}

	var asyncCalls, notifyCalls atomic.Int32
	provider := fdpNewWithOptions(fix.worktreeDir, fdpOptions{
		resolveGitSyncFn: func(string) *string {
			return getBranch()
		},
		resolveGitAsyncFn: func(context.Context, string) *string {
			asyncCalls.Add(1)
			return getBranch()
		},
		debounceDelay:     40 * time.Millisecond,
		tablePollInterval: 30 * time.Millisecond,
	})
	defer provider.Dispose()

	if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
		t.Fatalf("expected 'main', got %v", branch)
	}

	branchMu.Lock()
	resolvedBranch = "foo"
	branchMu.Unlock()

	provider.OnBranchChange(func() {
		notifyCalls.Add(1)
	})

	if err := os.WriteFile(filepath.Join(fix.reftableDir, "tables.list"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tfdpWaitFor(t, 5*time.Second, func() bool {
		b := provider.GetGitBranch()
		return b != nil && *b == "foo" && notifyCalls.Load() >= 1
	})

	if asyncCalls.Load() < 1 {
		t.Fatalf("expected at least 1 async resolution, got %d", asyncCalls.Load())
	}
	if branch := provider.GetGitBranch(); branch == nil || *branch != "foo" {
		t.Fatalf("expected 'foo', got %v", branch)
	}
}

func TestFooterDataProvider_RetriesWatchersAfterAsyncError(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := tfdpCreatePlainRepo(t, tempDir)
	gitDir := filepath.Join(repoDir, ".git")
	reg := tfdpNewFakeWatchRegistry()

	provider := fdpNewWithOptions(repoDir, fdpOptions{
		watchDirFn: reg.watch,
		retryDelay: 50 * time.Millisecond,
	})
	defer provider.Dispose()

	if provider.retryDelay != 50*time.Millisecond {
		t.Fatalf("unexpected retryDelay")
	}
	defaultProvider := NewFooterDataProvider(repoDir)
	if defaultProvider.retryDelay != utils.FsWatchRetryDelayMs*time.Millisecond {
		defaultProvider.Dispose()
		t.Fatalf("expected default retryDelay %v, got %v", utils.FsWatchRetryDelayMs*time.Millisecond, defaultProvider.retryDelay)
	}
	defaultProvider.Dispose()

	orig := reg.latest(gitDir)
	if orig == nil {
		t.Fatal("expected initial headWatcher")
	}

	orig.onError()

	tfdpWaitFor(t, 2*time.Second, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.headWatcher == nil && orig.closed.Load()
	})

	time.Sleep(15 * time.Millisecond)
	provider.mu.Lock()
	stillNil := provider.headWatcher == nil
	provider.mu.Unlock()
	if !stillNil {
		t.Fatal("expected headWatcher to remain nil before retry delay elapses")
	}

	tfdpWaitFor(t, 2*time.Second, func() bool {
		return reg.count(gitDir) == 2
	})

	next := reg.latest(gitDir)
	if next == nil || next == orig {
		t.Fatal("expected a new headWatcher after retry delay")
	}
}

func TestFooterDataProvider_StatusesProviderCountAndSetCwd(t *testing.T) {
	tempDir := t.TempDir()
	repo1 := tfdpCreatePlainRepo(t, filepath.Join(tempDir, "one"))
	repo2 := filepath.Join(tempDir, "two", "repo")
	if err := os.MkdirAll(filepath.Join(repo2, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo2, ".git", "HEAD"), []byte("ref: refs/heads/feat/test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := NewFooterDataProvider(repo1)
	defer provider.Dispose()

	var ro ReadonlyFooterDataProvider = provider
	if ro.GetGitBranch() == nil || *ro.GetGitBranch() != "main" {
		t.Fatalf("expected 'main', got %v", ro.GetGitBranch())
	}

	s1 := "ready"
	provider.SetExtensionStatus("ext-a", &s1)
	statuses := ro.GetExtensionStatuses()
	if statuses["ext-a"] != "ready" {
		t.Fatalf("expected ext-a status 'ready', got %v", statuses)
	}
	// Returned map should be a defensive copy.
	statuses["ext-a"] = "mutated"
	if ro.GetExtensionStatuses()["ext-a"] != "ready" {
		t.Fatal("expected defensive copy from GetExtensionStatuses")
	}
	provider.SetExtensionStatus("ext-a", nil)
	if _, ok := ro.GetExtensionStatuses()["ext-a"]; ok {
		t.Fatal("expected ext-a to be deleted when nil status is passed")
	}
	provider.SetExtensionStatus("ext-b", &s1)
	provider.ClearExtensionStatuses()
	if len(ro.GetExtensionStatuses()) != 0 {
		t.Fatal("expected ClearExtensionStatuses to empty map")
	}

	provider.SetAvailableProviderCount(3)
	if ro.GetAvailableProviderCount() != 3 {
		t.Fatalf("expected provider count 3, got %d", ro.GetAvailableProviderCount())
	}

	var branchChanges atomic.Int32
	unsub := ro.OnBranchChange(func() {
		branchChanges.Add(1)
	})
	provider.SetCwd(repo2)
	if branchChanges.Load() != 1 {
		t.Fatalf("expected SetCwd to notify branch listeners once, got %d", branchChanges.Load())
	}
	if ro.GetGitBranch() == nil || *ro.GetGitBranch() != "feat/test" {
		t.Fatalf("expected 'feat/test' after SetCwd, got %v", ro.GetGitBranch())
	}

	// Same cwd is a no-op.
	provider.SetCwd(repo2)
	if branchChanges.Load() != 1 {
		t.Fatalf("expected same cwd not to notify, got %d", branchChanges.Load())
	}

	unsub()
	notRepo := filepath.Join(tempDir, "not-git")
	if err := os.MkdirAll(notRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	provider.SetCwd(notRepo)
	if branchChanges.Load() != 1 {
		t.Fatalf("expected unsubscribed callback not to run, got %d", branchChanges.Load())
	}
	if ro.GetGitBranch() != nil {
		t.Fatalf("expected nil branch outside git repo, got %v", *ro.GetGitBranch())
	}
}
