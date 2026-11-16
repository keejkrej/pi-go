// Ported from packages/coding-agent/src/core/footer-data-provider.ts (pi v1.5.2).

package core

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

const (
	fdpWatchDebounceMs    = 500 * time.Millisecond
	fdpHeadPollInterval   = 1000 * time.Millisecond
	fdpTablesPollInterval = 250 * time.Millisecond
)

var fdpWindowsMountRe = regexp.MustCompile(`(?i)^/mnt/[a-z](?:/|$)`)

// GitPaths holds resolved filesystem paths for a git repository or worktree.
type GitPaths struct {
	RepoDir      string
	CommonGitDir string
	HeadPath     string
}

func fdpResolvePath(baseDir, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(baseDir, target))
}

func fdpStrPtr(s string) *string {
	return &s
}

func fdpEqualStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// FindGitPaths walks up from cwd to locate git metadata paths.
// Handles both regular git repositories (.git directory) and worktrees (.git file).
func FindGitPaths(cwd string) *GitPaths {
	dir := cwd
	for {
		gitPath := filepath.Join(dir, ".git")
		stat, err := os.Stat(gitPath)
		if err == nil {
			if stat.Mode().IsRegular() {
				raw, readErr := os.ReadFile(gitPath)
				if readErr != nil {
					return nil
				}
				content := strings.TrimSpace(string(raw))
				if strings.HasPrefix(content, "gitdir: ") {
					gitDir := fdpResolvePath(dir, strings.TrimSpace(content[8:]))
					headPath := filepath.Join(gitDir, "HEAD")
					if _, headErr := os.Stat(headPath); headErr != nil {
						return nil
					}
					commonGitDir := gitDir
					commonDirPath := filepath.Join(gitDir, "commondir")
					if commonRaw, commonErr := os.ReadFile(commonDirPath); commonErr == nil {
						commonGitDir = fdpResolvePath(gitDir, strings.TrimSpace(string(commonRaw)))
					}
					return &GitPaths{
						RepoDir:      dir,
						CommonGitDir: commonGitDir,
						HeadPath:     headPath,
					}
				}
			} else if stat.IsDir() {
				headPath := filepath.Join(gitPath, "HEAD")
				if _, headErr := os.Stat(headPath); headErr != nil {
					return nil
				}
				return &GitPaths{
					RepoDir:      dir,
					CommonGitDir: gitPath,
					HeadPath:     headPath,
				}
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func fdpResolveBranchWithGitSync(repoDir string) *string {
	cmd := exec.Command("git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return nil
	}
	return &branch
}

func fdpResolveBranchWithGitAsync(ctx context.Context, repoDir string) *string {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return nil
	}
	return &branch
}

func fdpIsWslEnvironment() bool {
	return runtime.GOOS == "linux" && (os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "")
}

func fdpIsWindowsMountedRepoPath(repoDir string) bool {
	return fdpWindowsMountRe.MatchString(repoDir)
}

func fdpShouldPollGitHead(repoDir string) bool {
	return fdpIsWslEnvironment() && fdpIsWindowsMountedRepoPath(repoDir)
}

type fdpWatcher interface {
	Close() error
}

type fdpWatchFunc func(path string, listener func(eventType, filename string), onError func()) fdpWatcher

func fdpDefaultWatch(path string, listener func(eventType, filename string), onError func()) fdpWatcher {
	w := utils.WatchWithErrorHandler(path, listener, onError)
	if w == nil {
		return nil
	}
	return w
}

type fdpPollStop struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *fdpPollStop) stop() {
	if p == nil {
		return
	}
	p.cancel()
	<-p.done
}

func fdpStartFilePoll(filePath string, interval time.Duration, onChange func()) *fdpPollStop {
	if interval <= 0 {
		interval = fdpTablesPollInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	var prevValid bool
	var prevModUnixNano int64
	var prevSize int64
	if info, err := os.Stat(filePath); err == nil {
		prevValid = true
		prevModUnixNano = info.ModTime().UnixNano()
		prevSize = info.Size()
	}

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(filePath)
				var curValid bool
				var curModUnixNano int64
				var curSize int64
				if err == nil {
					curValid = true
					curModUnixNano = info.ModTime().UnixNano()
					curSize = info.Size()
				}
				if curValid != prevValid || (curValid && (curModUnixNano != prevModUnixNano || curSize != prevSize)) {
					prevValid = curValid
					prevModUnixNano = curModUnixNano
					prevSize = curSize
					onChange()
				}
			}
		}
	}()

	return &fdpPollStop{cancel: cancel, done: done}
}

func fdpCloseWatchersAndPolls(watchers []fdpWatcher, polls []*fdpPollStop) {
	for _, w := range watchers {
		if w != nil {
			_ = w.Close()
		}
	}
	for _, p := range polls {
		if p != nil {
			p.stop()
		}
	}
}

type fdpBranchCallbackEntry struct {
	id uint64
	fn func()
}

// ReadonlyFooterDataProvider exposes read-only git branch and extension status state
// for footer rendering without allowing status mutation or disposal.
type ReadonlyFooterDataProvider interface {
	GetGitBranch() *string
	GetExtensionStatuses() map[string]string
	GetAvailableProviderCount() int
	OnBranchChange(callback func()) func()
}

// FooterDataProvider provides git branch tracking and extension statuses for footer display.
type FooterDataProvider struct {
	mu sync.Mutex

	cwd                     string
	extensionStatuses       map[string]string
	cachedBranch            *string
	cachedBranchSet         bool
	gitPaths                *GitPaths
	headWatcher             fdpWatcher
	headPoll                *fdpPollStop
	reftableWatcher         fdpWatcher
	reftableTablesListWatch fdpWatcher
	reftableTablesPoll      *fdpPollStop

	watcherGen             uint64
	branchCallbacks        []fdpBranchCallbackEntry
	nextCallbackId         uint64
	availableProviderCount int

	refreshTimer       *time.Timer
	gitWatcherRetryTim *time.Timer
	refreshInFlight    bool
	refreshPending     bool
	disposed           bool
	asyncCancel        context.CancelFunc

	// Overridable test hooks (nil uses real OS/git/timing implementations).
	watchDirFn        fdpWatchFunc
	resolveGitSyncFn  func(repoDir string) *string
	resolveGitAsyncFn func(ctx context.Context, repoDir string) *string
	shouldPollHeadFn  func(repoDir string) bool
	debounceDelay     time.Duration
	retryDelay        time.Duration
	headPollInterval  time.Duration
	tablePollInterval time.Duration
}

type fdpOptions struct {
	watchDirFn        fdpWatchFunc
	resolveGitSyncFn  func(repoDir string) *string
	resolveGitAsyncFn func(ctx context.Context, repoDir string) *string
	shouldPollHeadFn  func(repoDir string) bool
	debounceDelay     time.Duration
	retryDelay        time.Duration
	headPollInterval  time.Duration
	tablePollInterval time.Duration
}

// NewFooterDataProvider creates a FooterDataProvider for cwd and begins watching git metadata.
func NewFooterDataProvider(cwd string) *FooterDataProvider {
	return fdpNewWithOptions(cwd, fdpOptions{})
}

func fdpNewWithOptions(cwd string, opts fdpOptions) *FooterDataProvider {
	debounce := opts.debounceDelay
	if debounce <= 0 {
		debounce = fdpWatchDebounceMs
	}
	retry := opts.retryDelay
	if retry <= 0 {
		retry = utils.FsWatchRetryDelayMs * time.Millisecond
	}
	headInterval := opts.headPollInterval
	if headInterval <= 0 {
		headInterval = fdpHeadPollInterval
	}
	tableInterval := opts.tablePollInterval
	if tableInterval <= 0 {
		tableInterval = fdpTablesPollInterval
	}
	p := &FooterDataProvider{
		cwd:               cwd,
		extensionStatuses: make(map[string]string),
		gitPaths:          FindGitPaths(cwd),
		watchDirFn:        opts.watchDirFn,
		resolveGitSyncFn:  opts.resolveGitSyncFn,
		resolveGitAsyncFn: opts.resolveGitAsyncFn,
		shouldPollHeadFn:  opts.shouldPollHeadFn,
		debounceDelay:     debounce,
		retryDelay:        retry,
		headPollInterval:  headInterval,
		tablePollInterval: tableInterval,
	}
	p.setupGitWatcher()
	return p
}

// GetGitBranch returns the current git branch, nil if not in a repo, or "detached" on detached HEAD.
func (p *FooterDataProvider) GetGitBranch() *string {
	p.mu.Lock()
	if p.cachedBranchSet {
		res := p.cachedBranch
		p.mu.Unlock()
		return res
	}
	paths := p.gitPaths
	syncFn := p.resolveGitSyncFn
	p.mu.Unlock()

	if syncFn == nil {
		syncFn = fdpResolveBranchWithGitSync
	}
	branch := fdpResolveGitBranchSync(paths, syncFn)

	p.mu.Lock()
	if !p.cachedBranchSet {
		p.cachedBranch = branch
		p.cachedBranchSet = true
	}
	res := p.cachedBranch
	p.mu.Unlock()
	return res
}

// GetExtensionStatuses returns a snapshot of extension status texts set via SetExtensionStatus.
func (p *FooterDataProvider) GetExtensionStatuses() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.extensionStatuses)
}

// OnBranchChange subscribes to git branch changes and returns an unsubscribe function.
func (p *FooterDataProvider) OnBranchChange(callback func()) func() {
	if callback == nil {
		return func() {}
	}
	p.mu.Lock()
	id := p.nextCallbackId
	p.nextCallbackId++
	p.branchCallbacks = append(p.branchCallbacks, fdpBranchCallbackEntry{id: id, fn: callback})
	p.mu.Unlock()

	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, entry := range p.branchCallbacks {
			if entry.id == id {
				p.branchCallbacks = append(p.branchCallbacks[:i], p.branchCallbacks[i+1:]...)
				break
			}
		}
	}
}

// SetExtensionStatus sets or deletes an extension footer status text.
func (p *FooterDataProvider) SetExtensionStatus(key string, text *string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if text == nil {
		delete(p.extensionStatuses, key)
	} else {
		p.extensionStatuses[key] = *text
	}
}

// ClearExtensionStatuses removes all extension footer statuses.
func (p *FooterDataProvider) ClearExtensionStatuses() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.extensionStatuses)
}

// GetAvailableProviderCount returns the count of unique providers with available models.
func (p *FooterDataProvider) GetAvailableProviderCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.availableProviderCount
}

// SetAvailableProviderCount updates the available provider count shown in the footer.
func (p *FooterDataProvider) SetAvailableProviderCount(count int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.availableProviderCount = count
}

// SetCwd updates the tracked working directory and reinitializes git branch watching.
func (p *FooterDataProvider) SetCwd(cwd string) {
	p.mu.Lock()
	if p.cwd == cwd {
		p.mu.Unlock()
		return
	}
	p.cwd = cwd
	if p.refreshTimer != nil {
		p.refreshTimer.Stop()
		p.refreshTimer = nil
	}
	p.refreshPending = false
	if p.asyncCancel != nil {
		p.asyncCancel()
		p.asyncCancel = nil
	}
	watchers, polls := p.detachWatchersLocked()
	p.cachedBranch = nil
	p.cachedBranchSet = false
	p.gitPaths = FindGitPaths(cwd)
	p.mu.Unlock()

	fdpCloseWatchersAndPolls(watchers, polls)
	p.setupGitWatcher()
	p.notifyBranchChange()
}

// RefreshGitBranch clears any cached branch name and notifies listeners so callers re-read HEAD immediately.
func (p *FooterDataProvider) RefreshGitBranch() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.cachedBranch = nil
	p.cachedBranchSet = false
	p.mu.Unlock()
	p.notifyBranchChange()
}

// Dispose stops all file watchers, pollers, and timers and clears subscribers.
func (p *FooterDataProvider) Dispose() {
	p.mu.Lock()
	p.disposed = true
	if p.refreshTimer != nil {
		p.refreshTimer.Stop()
		p.refreshTimer = nil
	}
	p.refreshPending = false
	if p.asyncCancel != nil {
		p.asyncCancel()
		p.asyncCancel = nil
	}
	watchers, polls := p.detachWatchersLocked()
	p.branchCallbacks = nil
	p.mu.Unlock()

	fdpCloseWatchersAndPolls(watchers, polls)
}

func (p *FooterDataProvider) notifyBranchChange() {
	p.mu.Lock()
	if p.disposed || len(p.branchCallbacks) == 0 {
		p.mu.Unlock()
		return
	}
	callbacks := make([]func(), len(p.branchCallbacks))
	for i, entry := range p.branchCallbacks {
		callbacks[i] = entry.fn
	}
	p.mu.Unlock()

	for _, cb := range callbacks {
		cb()
	}
}

func (p *FooterDataProvider) scheduleRefresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disposed || p.refreshTimer != nil {
		return
	}
	if p.refreshInFlight {
		p.refreshPending = true
		return
	}
	delay := p.debounceDelay
	if delay <= 0 {
		delay = fdpWatchDebounceMs
	}
	p.refreshTimer = time.AfterFunc(delay, func() {
		p.mu.Lock()
		p.refreshTimer = nil
		p.mu.Unlock()
		p.refreshGitBranchAsync()
	})
}

func (p *FooterDataProvider) refreshGitBranchAsync() {
	p.mu.Lock()
	if p.disposed {
		p.mu.Unlock()
		return
	}
	if p.refreshInFlight {
		p.refreshPending = true
		p.mu.Unlock()
		return
	}
	p.refreshInFlight = true
	paths := p.gitPaths
	asyncFn := p.resolveGitAsyncFn
	ctx, cancel := context.WithCancel(context.Background())
	p.asyncCancel = cancel
	p.mu.Unlock()

	if asyncFn == nil {
		asyncFn = fdpResolveBranchWithGitAsync
	}
	nextBranch := fdpResolveGitBranchAsync(ctx, paths, asyncFn)
	cancel()

	shouldNotify := false
	shouldReschedule := false

	p.mu.Lock()
	p.asyncCancel = nil
	p.refreshInFlight = false
	if !p.disposed {
		if p.cachedBranchSet && !fdpEqualStringPtr(p.cachedBranch, nextBranch) {
			p.cachedBranch = nextBranch
			shouldNotify = true
		} else {
			p.cachedBranch = nextBranch
			p.cachedBranchSet = true
		}
		if p.refreshPending {
			p.refreshPending = false
			shouldReschedule = true
		}
	}
	p.mu.Unlock()

	if shouldNotify {
		p.notifyBranchChange()
	}
	if shouldReschedule {
		p.scheduleRefresh()
	}
}

func fdpResolveGitBranchSync(paths *GitPaths, gitSync func(repoDir string) *string) *string {
	if paths == nil {
		return nil
	}
	raw, err := os.ReadFile(paths.HeadPath)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(raw))
	if strings.HasPrefix(content, "ref: refs/heads/") {
		branch := content[len("ref: refs/heads/"):]
		if branch == ".invalid" {
			if resolved := gitSync(paths.RepoDir); resolved != nil {
				return resolved
			}
			return fdpStrPtr("detached")
		}
		return &branch
	}
	return fdpStrPtr("detached")
}

func fdpResolveGitBranchAsync(ctx context.Context, paths *GitPaths, gitAsync func(ctx context.Context, repoDir string) *string) *string {
	if paths == nil {
		return nil
	}
	raw, err := os.ReadFile(paths.HeadPath)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(raw))
	if strings.HasPrefix(content, "ref: refs/heads/") {
		branch := content[len("ref: refs/heads/"):]
		if branch == ".invalid" {
			if resolved := gitAsync(ctx, paths.RepoDir); resolved != nil {
				return resolved
			}
			return fdpStrPtr("detached")
		}
		return &branch
	}
	return fdpStrPtr("detached")
}

// detachWatchersLocked clears watcher/poll/retry state and advances watcherGen. Caller must hold p.mu.
func (p *FooterDataProvider) detachWatchersLocked() ([]fdpWatcher, []*fdpPollStop) {
	p.watcherGen++
	var watchers []fdpWatcher
	if p.headWatcher != nil {
		watchers = append(watchers, p.headWatcher)
		p.headWatcher = nil
	}
	if p.reftableWatcher != nil {
		watchers = append(watchers, p.reftableWatcher)
		p.reftableWatcher = nil
	}
	if p.reftableTablesListWatch != nil {
		watchers = append(watchers, p.reftableTablesListWatch)
		p.reftableTablesListWatch = nil
	}

	var polls []*fdpPollStop
	if p.headPoll != nil {
		polls = append(polls, p.headPoll)
		p.headPoll = nil
	}
	if p.reftableTablesPoll != nil {
		polls = append(polls, p.reftableTablesPoll)
		p.reftableTablesPoll = nil
	}

	if p.gitWatcherRetryTim != nil {
		p.gitWatcherRetryTim.Stop()
		p.gitWatcherRetryTim = nil
	}
	return watchers, polls
}

func (p *FooterDataProvider) scheduleGitWatcherRetryLocked() {
	if p.disposed || p.gitWatcherRetryTim != nil {
		return
	}
	delay := p.retryDelay
	if delay <= 0 {
		delay = utils.FsWatchRetryDelayMs * time.Millisecond
	}
	p.gitWatcherRetryTim = time.AfterFunc(delay, func() {
		p.mu.Lock()
		p.gitWatcherRetryTim = nil
		disposed := p.disposed
		p.mu.Unlock()
		if !disposed {
			p.setupGitWatcher()
		}
	})
}

func (p *FooterDataProvider) handleGitWatcherError(gen uint64) {
	p.mu.Lock()
	if p.disposed || p.watcherGen != gen {
		p.mu.Unlock()
		return
	}
	watchers, polls := p.detachWatchersLocked()
	p.scheduleGitWatcherRetryLocked()
	p.mu.Unlock()

	// Close asynchronously so returning from fsnotify's error callback does not deadlock on w.done.
	go fdpCloseWatchersAndPolls(watchers, polls)
}

func (p *FooterDataProvider) setupGitWatcher() {
	p.mu.Lock()
	oldWatchers, oldPolls := p.detachWatchersLocked()
	gen := p.watcherGen
	paths := p.gitPaths
	disposed := p.disposed
	watchFn := p.watchDirFn
	pollHeadFn := p.shouldPollHeadFn
	headPollInterval := p.headPollInterval
	tablePollInterval := p.tablePollInterval
	p.mu.Unlock()

	fdpCloseWatchersAndPolls(oldWatchers, oldPolls)

	if disposed || paths == nil {
		return
	}
	if watchFn == nil {
		watchFn = fdpDefaultWatch
	}
	if pollHeadFn == nil {
		pollHeadFn = fdpShouldPollGitHead
	}

	pollGitHead := pollHeadFn(paths.RepoDir)
	onError := func() {
		p.handleGitWatcherError(gen)
	}

	// Watch the directory containing HEAD, not HEAD itself.
	// Git uses atomic writes (write temp, rename over HEAD), which changes the inode.
	headWatcher := watchFn(
		filepath.Dir(paths.HeadPath),
		func(_ string, filename string) {
			if filename == "" || filename == "HEAD" {
				p.scheduleRefresh()
			}
		},
		onError,
	)

	var headPoll *fdpPollStop
	if pollGitHead {
		headPoll = fdpStartFilePoll(paths.HeadPath, headPollInterval, p.scheduleRefresh)
	}

	var reftableWatcher fdpWatcher
	var tablesListWatcher fdpWatcher
	var tablesListPoll *fdpPollStop

	if headWatcher != nil || pollGitHead {
		reftableDir := filepath.Join(paths.CommonGitDir, "reftable")
		if _, err := os.Stat(reftableDir); err == nil {
			reftableWatcher = watchFn(
				reftableDir,
				func(_, _ string) {
					p.scheduleRefresh()
				},
				onError,
			)
			if reftableWatcher != nil {
				tablesListPath := filepath.Join(reftableDir, "tables.list")
				if _, tblErr := os.Stat(tablesListPath); tblErr == nil {
					tablesListWatcher = watchFn(
						tablesListPath,
						func(_, _ string) {
							p.scheduleRefresh()
						},
						onError,
					)
					if tablesListWatcher != nil {
						tablesListPoll = fdpStartFilePoll(tablesListPath, tablePollInterval, p.scheduleRefresh)
					}
				}
			}
		}
	}

	p.mu.Lock()
	if p.disposed || p.watcherGen != gen {
		p.mu.Unlock()
		fdpCloseWatchersAndPolls(
			[]fdpWatcher{headWatcher, reftableWatcher, tablesListWatcher},
			[]*fdpPollStop{headPoll, tablesListPoll},
		)
		return
	}
	p.headWatcher = headWatcher
	p.headPoll = headPoll
	p.reftableWatcher = reftableWatcher
	p.reftableTablesListWatch = tablesListWatcher
	p.reftableTablesPoll = tablesListPoll
	p.mu.Unlock()
}
