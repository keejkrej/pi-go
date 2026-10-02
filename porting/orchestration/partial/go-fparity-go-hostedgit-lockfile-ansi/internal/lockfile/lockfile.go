// Ported from proper-lockfile@4.1.2 index.js, lib/lockfile.js, lib/adapter.js and lib/mtime-precision.js.

package lockfile

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Options are the proper-lockfile options. Lock and LockSync read every field; Unlock reads Realpath and
// LockfilePath; Check reads Stale, Realpath and LockfilePath.
type Options struct {
	// Stale is the age in milliseconds after which a lock is considered stale. Default 10000, minimum 2000.
	Stale *int64
	// Update is the interval in milliseconds at which the lock mtime is refreshed. Default Stale/2,
	// clamped to [1000, Stale/2].
	Update *int64
	// Realpath resolves symlinks of file (which must exist). Default true; false only makes file absolute.
	Realpath *bool
	// Retries configures lock acquisition retries (TS `retries: number | RetryOptions`). Nil means 0.
	Retries *RetryOptions
	// LockfilePath overrides the lock directory path. Default "<file>.lock".
	LockfilePath string
	// OnCompromised is called when the lock is lost (mtime refresh failed or the lock was taken over).
	// The default panics with the error, matching the TS default that throws from a timer.
	OnCompromised func(err error)
}

// Error is a proper-lockfile error (an Error with a `code`), or a Node-style fs error.
type Error struct {
	// Code is "ELOCKED", "ECOMPROMISED", "ERELEASED", "ENOTACQUIRED", "ESYNC", a Node errno code such as
	// "ENOENT", or "" (for example "RetryOperation timeout occurred").
	Code    string
	Message string
	// File is set on ELOCKED errors: the canonical path of the locked file.
	File string
	// Syscall and Path are set on fs errors, as on Node's fs errors.
	Syscall string
	Path    string
	// Err is the underlying Go error for fs errors.
	Err error
}

func (e *Error) Error() string { return e.Message }

// Name is the JS error name.
func (e *Error) Name() string { return "Error" }

func (e *Error) Unwrap() error { return e.Err }

type resolvedOptions struct {
	stale         float64
	update        float64
	realpath      bool
	retries       *RetryOptions
	lockfilePath  string
	onCompromised func(err error)
	// syncFS marks options of the sync API, whose fs object is created per call (no mtime precision cache).
	syncFS bool
}

type heldLock struct {
	// mu serializes the mtime refresh with release, which TS gets from run-to-completion.
	mu             sync.Mutex
	lockfilePath   string
	mtime          int64
	mtimePrecision string
	options        *resolvedOptions
	lastUpdate     int64
	updateTimer    *time.Timer
	updateDelay    float64
	released       bool
	// exited is set by CleanupOnExit: the process is exiting, so the mtime refresh stops (TS timers die
	// with the process) while release still succeeds.
	exited bool
}

var (
	locksMu sync.Mutex
	locks   = map[string]*heldLock{}

	// precisionCache is the mtime precision cached on the shared async fs object.
	precisionMu    sync.Mutex
	precisionCache string

	// Test hook: Date.now().
	lfNow = func() int64 { return time.Now().UnixMilli() }
)

func getLockFile(file string, options *resolvedOptions) string {
	if options.lockfilePath != "" {
		return options.lockfilePath
	}
	return file + ".lock"
}

func resolveCanonicalPath(file string, realpath bool) (string, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", toNodeError(err, "realpath", file)
	}
	if !realpath {
		return abs, nil
	}
	// Use realpath to resolve symlinks
	// It also resolves relative paths
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", toNodeError(err, "lstat", abs)
	}
	return resolved, nil
}

func newLockedError(file string) *Error {
	return &Error{Code: "ELOCKED", Message: "Lock file is already being held", File: file}
}

func acquireLock(file string, options *resolvedOptions) (int64, string, error) {
	lockfilePath := getLockFile(file, options)

	// Use mkdir to create the lockfile (atomic operation)
	err := os.Mkdir(lockfilePath, 0o777)
	if err == nil {
		// At this point, we acquired the lock!
		// Probe the mtime precision
		mtime, precision, err := probeMtimePrecision(lockfilePath, options)
		if err != nil {
			// If it failed, try to remove the lock..
			_ = lfRmdir(lockfilePath)
			return 0, "", err
		}
		return mtime, precision, nil
	}

	// If error is not EEXIST then some other error occurred while locking
	if !errors.Is(err, fs.ErrExist) {
		return 0, "", toNodeError(err, "mkdir", lockfilePath)
	}

	// Otherwise, check if lock is stale by analyzing the file mtime
	if options.stale <= 0 {
		return 0, "", newLockedError(file)
	}

	stat, err := os.Stat(lockfilePath)
	if err != nil {
		// Retry if the lockfile has been removed (meanwhile)
		// Skip stale check to avoid recursiveness
		if errors.Is(err, fs.ErrNotExist) {
			return acquireLock(file, withStale(options, 0))
		}
		return 0, "", toNodeError(err, "stat", lockfilePath)
	}

	if !isLockStale(stat, options) {
		return 0, "", newLockedError(file)
	}

	// If it's stale, remove it and try again!
	// Skip stale check to avoid recursiveness
	if err := removeLock(file, options); err != nil {
		return 0, "", err
	}

	return acquireLock(file, withStale(options, 0))
}

func withStale(options *resolvedOptions, stale float64) *resolvedOptions {
	o := *options
	o.stale = stale
	return &o
}

func isLockStale(stat fs.FileInfo, options *resolvedOptions) bool {
	return float64(lfStatMtime(stat)) < float64(lfNow())-options.stale
}

// lfStatMtime is `stat.mtime.getTime()`. Node computes mtimeMs as the double sec*1e3 + nsec/1e6 and builds
// the Date from Math.round(mtimeMs), so it rounds to the nearest millisecond where UnixMilli truncates.
// For example a TS utimes(Date) write can store ...122999906 ns, which Node reads back as ...123 ms.
func lfStatMtime(stat fs.FileInfo) int64 {
	return lfMtimeFromTime(stat.ModTime())
}

func lfMtimeFromTime(t time.Time) int64 {
	ms := float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6
	// Math.round: ties toward +Infinity.
	f := math.Floor(ms)
	if ms-f >= 0.5 {
		f++
	}
	return int64(f)
}

func removeLock(file string, options *resolvedOptions) error {
	// Remove lockfile, ignoring ENOENT errors
	if err := lfRmdir(getLockFile(file, options)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return toNodeError(err, "rmdir", getLockFile(file, options))
	}
	return nil
}

// probeMtimePrecision is mtime-precision.probe: it reports the lock mtime (ms) and the filesystem mtime
// precision ("s" or "ms").
func probeMtimePrecision(file string, options *resolvedOptions) (int64, string, error) {
	cachedPrecision := ""
	if !options.syncFS {
		precisionMu.Lock()
		cachedPrecision = precisionCache
		precisionMu.Unlock()
	}

	if cachedPrecision != "" {
		stat, err := os.Stat(file)
		if err != nil {
			return 0, "", toNodeError(err, "stat", file)
		}
		return lfStatMtime(stat), cachedPrecision, nil
	}

	// Set mtime by ceiling Date.now() to seconds + 5ms so that it's "not on the second"
	mtime := int64(math.Ceil(float64(lfNow())/1000))*1000 + 5

	t := time.UnixMilli(mtime)
	if err := os.Chtimes(file, t, t); err != nil {
		return 0, "", toNodeError(err, "utime", file)
	}

	stat, err := os.Stat(file)
	if err != nil {
		return 0, "", toNodeError(err, "stat", file)
	}

	statMtime := lfStatMtime(stat)
	precision := "ms"
	if statMtime%1000 == 0 {
		precision = "s"
	}

	if !options.syncFS {
		precisionMu.Lock()
		precisionCache = precision
		precisionMu.Unlock()
	}

	return statMtime, precision, nil
}

func getMtime(precision string) int64 {
	now := lfNow()
	if precision == "s" {
		now = int64(math.Ceil(float64(now)/1000)) * 1000
	}
	return now
}

// lfTimeoutMax is Node's TIMEOUT_MAX (2^31 - 1 ms).
const lfTimeoutMax = 2147483647

// lfTimerDelay is the delay Node's setTimeout(fn, ms) waits: anything outside [1, TIMEOUT_MAX] (0, NaN,
// Infinity, an overflowing retry backoff) becomes 1 ms, and fractions are truncated.
func lfTimerDelay(ms float64) time.Duration {
	if !(ms >= 1 && ms <= lfTimeoutMax) {
		ms = 1
	}
	return time.Duration(math.Trunc(ms)) * time.Millisecond
}

// updateLock schedules the next mtime refresh. Caller holds lock.mu.
func updateLock(file string, lock *heldLock) {
	// Just for safety, should never happen
	if lock.updateTimer != nil {
		return
	}

	if lock.updateDelay == 0 {
		lock.updateDelay = lock.options.update
	}
	lock.updateTimer = time.AfterFunc(lfTimerDelay(lock.updateDelay), func() { runUpdate(file, lock) })
}

func runUpdate(file string, lock *heldLock) {
	lock.mu.Lock()
	if lock.released || lock.exited {
		// The timer fired while the lock was being released (clearTimeout in TS), or the process is exiting.
		lock.mu.Unlock()
		return
	}
	lock.updateTimer = nil
	options := lock.options

	// Stat the file to check if mtime is still ours
	// If it is, we can still recover from a system sleep or a busy event loop
	stat, err := os.Stat(lock.lockfilePath)
	isOverThreshold := float64(lock.lastUpdate)+options.stale < float64(lfNow())

	// If it failed to update the lockfile, keep trying unless
	// the lockfile was deleted or we are over the threshold
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isOverThreshold {
			compromisedErr := toNodeError(err, "stat", lock.lockfilePath)
			compromisedErr.Code = "ECOMPROMISED"
			setLockAsCompromised(file, lock, compromisedErr)
			return
		}
		lock.updateDelay = 1000
		updateLock(file, lock)
		lock.mu.Unlock()
		return
	}

	isMtimeOurs := lock.mtime == lfStatMtime(stat)
	if !isMtimeOurs {
		setLockAsCompromised(file, lock, &Error{Code: "ECOMPROMISED", Message: "Unable to update lock within the stale threshold"})
		return
	}

	mtime := getMtime(lock.mtimePrecision)
	t := time.UnixMilli(mtime)
	err = os.Chtimes(lock.lockfilePath, t, t)
	isOverThreshold = float64(lock.lastUpdate)+options.stale < float64(lfNow())

	// Ignore if the lock was released
	if lock.released {
		lock.mu.Unlock()
		return
	}

	// If it failed to update the lockfile, keep trying unless
	// the lockfile was deleted or we are over the threshold
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isOverThreshold {
			compromisedErr := toNodeError(err, "utime", lock.lockfilePath)
			compromisedErr.Code = "ECOMPROMISED"
			setLockAsCompromised(file, lock, compromisedErr)
			return
		}
		lock.updateDelay = 1000
		updateLock(file, lock)
		lock.mu.Unlock()
		return
	}

	// All ok, keep updating..
	lock.mtime = mtime
	lock.lastUpdate = lfNow()
	lock.updateDelay = 0
	updateLock(file, lock)
	lock.mu.Unlock()
}

// setLockAsCompromised marks the lock released and reports err. Caller holds lock.mu; it is unlocked
// before onCompromised runs.
func setLockAsCompromised(file string, lock *heldLock, err error) {
	// Signal the lock has been released
	lock.released = true

	// Cancel lock mtime update
	// Just for safety, at this point updateTimeout should be null
	if lock.updateTimer != nil {
		lock.updateTimer.Stop()
		lock.updateTimer = nil
	}

	locksMu.Lock()
	if locks[file] == lock {
		delete(locks, file)
	}
	locksMu.Unlock()

	onCompromised := lock.options.onCompromised
	lock.mu.Unlock()
	onCompromised(err)
}

func resolveLockOptions(options *Options, syncFS bool) *resolvedOptions {
	o := &resolvedOptions{
		stale:    10000,
		realpath: true,
		onCompromised: func(err error) {
			panic(err)
		},
		syncFS: syncFS,
	}
	var update *int64
	if options != nil {
		if options.Stale != nil {
			o.stale = float64(*options.Stale)
		}
		update = options.Update
		if options.Realpath != nil {
			o.realpath = *options.Realpath
		}
		o.retries = options.Retries
		o.lockfilePath = options.LockfilePath
		if options.OnCompromised != nil {
			o.onCompromised = options.OnCompromised
		}
	}
	if o.retries == nil {
		o.retries = Retries(0)
	}
	o.stale = math.Max(o.stale, 2000)
	if update == nil {
		o.update = o.stale / 2
	} else {
		o.update = float64(*update)
	}
	o.update = math.Max(math.Min(o.update, o.stale/2), 1000)
	return o
}

func lock(ctx context.Context, file string, options *resolvedOptions) (func() error, error) {
	// Resolve to a canonical file path
	file, err := resolveCanonicalPath(file, options.realpath)
	if err != nil {
		return nil, err
	}

	// Attempt to acquire the lock
	operation, err := newRetryOperation(options.retries)
	if err != nil {
		return nil, err
	}

	operation.attempt()
	for {
		mtime, precision, err := acquireLock(file, options)
		if err != nil {
			retry, delay := operation.retry(err)
			if !retry {
				return nil, operation.mainError()
			}
			timer := time.NewTimer(lfTimerDelay(delay))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, context.Cause(ctx)
			case <-timer.C:
			}
			continue
		}

		// We now own the lock
		held := &heldLock{
			lockfilePath:   getLockFile(file, options),
			mtime:          mtime,
			mtimePrecision: precision,
			options:        options,
			lastUpdate:     lfNow(),
		}
		locksMu.Lock()
		locks[file] = held
		locksMu.Unlock()

		// We must keep the lock fresh to avoid staleness
		held.mu.Lock()
		updateLock(file, held)
		held.mu.Unlock()

		return func() error {
			held.mu.Lock()
			released := held.released
			held.mu.Unlock()
			if released {
				return &Error{Code: "ERELEASED", Message: "Lock is already released"}
			}

			// Not necessary to use realpath twice when unlocking
			unlockOptions := *options
			unlockOptions.realpath = false
			return unlock(file, &unlockOptions)
		}, nil
	}
}

func unlock(file string, options *resolvedOptions) error {
	// Resolve to a canonical file path
	file, err := resolveCanonicalPath(file, options.realpath)
	if err != nil {
		return err
	}

	// Skip if the lock is not acquired
	locksMu.Lock()
	held := locks[file]
	locksMu.Unlock()

	notAcquired := &Error{Code: "ENOTACQUIRED", Message: "Lock is not acquired/owned by you"}
	if held == nil {
		return notAcquired
	}

	held.mu.Lock()
	if held.released {
		// Released or compromised concurrently: TS would no longer find it in `locks`.
		held.mu.Unlock()
		return notAcquired
	}
	if held.updateTimer != nil {
		held.updateTimer.Stop() // Cancel lock mtime update
		held.updateTimer = nil
	}
	held.released = true // Signal the lock has been released
	locksMu.Lock()
	if locks[file] == held {
		delete(locks, file) // Delete from locks
	}
	locksMu.Unlock()
	held.mu.Unlock()

	return removeLock(file, options)
}

func syncRetriesError(options *Options) error {
	// Retries are not allowed because it requires the flow to be sync
	if options != nil && options.Retries != nil {
		esync := &Error{Code: "ESYNC", Message: "Cannot use retries with the sync api"}
		if options.Retries.Retries != nil && *options.Retries.Retries > 0 {
			return esync
		}
		timeouts, err := retryTimeouts(options.Retries)
		if err != nil {
			return err
		}
		if len(timeouts) > 0 {
			return esync
		}
	}
	return nil
}

// Lock acquires a lock on file by creating the "<file>.lock" directory and keeps it fresh by touching its
// mtime every Update ms until release is called. Waits between retries end early when ctx is done; the
// first attempt always runs. Errors are *Error values (code ELOCKED when the lock is held elsewhere).
func Lock(ctx context.Context, file string, options *Options) (release func() error, err error) {
	return lock(ctx, file, resolveLockOptions(options, false))
}

// LockSync is the synchronous lock (`lockSync`). Retries are rejected with ESYNC ("Cannot use retries with
// the sync api").
//
// TS parity deviation: TS only rejects an explicit positive `retries` count; a retries object that
// still schedules retries (default count, `forever`) makes TS return a broken release function. Here
// any retry schedule is rejected with ESYNC.
func LockSync(file string, options *Options) (release func() error, err error) {
	if err := syncRetriesError(options); err != nil {
		return nil, err
	}
	return lock(context.Background(), file, resolveLockOptions(options, true))
}

func resolveUnlockOptions(options *Options) *resolvedOptions {
	o := &resolvedOptions{realpath: true}
	if options != nil {
		if options.Realpath != nil {
			o.realpath = *options.Realpath
		}
		o.lockfilePath = options.LockfilePath
	}
	return o
}

// Unlock releases a lock held by this process (ENOTACQUIRED when it holds none).
func Unlock(file string, options *Options) error {
	return unlock(file, resolveUnlockOptions(options))
}

// UnlockSync is the synchronous Unlock (`unlockSync`).
func UnlockSync(file string, options *Options) error {
	return unlock(file, resolveUnlockOptions(options))
}

func check(file string, options *Options) (bool, error) {
	o := &resolvedOptions{stale: 10000, realpath: true}
	if options != nil {
		if options.Stale != nil {
			o.stale = float64(*options.Stale)
		}
		if options.Realpath != nil {
			o.realpath = *options.Realpath
		}
		o.lockfilePath = options.LockfilePath
	}
	o.stale = math.Max(o.stale, 2000)

	// Resolve to a canonical file path
	file, err := resolveCanonicalPath(file, o.realpath)
	if err != nil {
		return false, err
	}

	// Check if lockfile exists
	stat, err := os.Stat(getLockFile(file, o))
	if err != nil {
		// If does not exist, file is not locked. Otherwise, callback with error
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, toNodeError(err, "stat", getLockFile(file, o))
	}

	// Otherwise, check if lock is stale by analyzing the file mtime
	return !isLockStale(stat, o), nil
}

// Check reports whether file is locked (by any process) and the lock is not stale.
func Check(file string, options *Options) (bool, error) {
	return check(file, options)
}

// CheckSync is the synchronous Check (`checkSync`).
func CheckSync(file string, options *Options) (bool, error) {
	return check(file, options)
}

// CleanupOnExit removes every lock directory this process holds, ignoring errors. TS does this from a
// signal-exit handler (process exit and fatal signals); Go has no exit hooks, so the application's exit
// and signal paths must call it. It also stops the mtime refresh of those locks, as the TS timers end with
// the process; a later release still succeeds because rmdir ENOENT is ignored.
func CleanupOnExit() {
	locksMu.Lock()
	held := make([]*heldLock, 0, len(locks))
	for _, l := range locks {
		held = append(held, l)
	}
	locksMu.Unlock()
	for _, l := range held {
		l.mu.Lock()
		l.exited = true
		if l.updateTimer != nil {
			l.updateTimer.Stop()
			l.updateTimer = nil
		}
		path := l.lockfilePath
		l.mu.Unlock()
		_ = lfRmdir(path)
	}
}
