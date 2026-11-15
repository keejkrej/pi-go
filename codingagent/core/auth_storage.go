// Ported from packages/coding-agent/src/core/auth-storage.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/lockfile"
)

const astMaxEpermRetries = 5

// ErrReadOnlyAuthStorage is returned when Modify or Delete is called on ReadOnlyAuthStorage.
var ErrReadOnlyAuthStorage = errors.New("Read-only credential storage cannot modify auth.json")

// AuthStorageLockResult is returned by lock callbacks in AuthStorageBackend.
// When Next is non-nil, the backend writes *Next atomically before releasing the lock.
type AuthStorageLockResult struct {
	Result any
	Next   *string
}

// AuthStorageBackend abstracts locked synchronous and asynchronous access to serialized credential storage.
type AuthStorageBackend interface {
	WithLock(fn func(current *string) (AuthStorageLockResult, error)) (any, error)
	WithLockAsync(
		ctx context.Context,
		fn func(current *string) (AuthStorageLockResult, error),
		options *ai.AuthOperationOptions,
	) (any, error)
}

func astRetryableLockErrorCode(err error) string {
	var lerr *lockfile.Error
	if errors.As(err, &lerr) {
		if lerr.Code == "ELOCKED" {
			return "ELOCKED"
		}
		if lerr.Code == "EPERM" && runtime.GOOS == "windows" {
			return "EPERM"
		}
	}
	return ""
}

func astCloneCredential(c ai.Credential) ai.Credential {
	if c == nil {
		return nil
	}
	switch v := c.(type) {
	case *ai.ApiKeyCredential:
		cp := &ai.ApiKeyCredential{
			Type: v.Type,
		}
		if v.Key != nil {
			k := *v.Key
			cp.Key = &k
		}
		if v.Env != nil {
			cp.Env = maps.Clone(v.Env)
		}
		return cp
	case *ai.OAuthCredential:
		cp := &ai.OAuthCredential{
			Type: v.Type,
			OAuthCredentials: ai.OAuthCredentials{
				Refresh: v.Refresh,
				Access:  v.Access,
				Expires: v.Expires,
			},
		}
		if v.Extra != nil {
			cp.Extra = v.Extra.Clone()
		}
		return cp
	default:
		return c
	}
}

func astResolveAuthPath(authPath ...string) string {
	p := filepath.Join(utils.GetAgentDir(), "auth.json")
	if len(authPath) > 0 && authPath[0] != "" {
		p = authPath[0]
	}
	if normalized, err := utils.NormalizePath(p, nil); err == nil {
		return normalized
	}
	return p
}

// FileAuthStorageBackend implements AuthStorageBackend against auth.json with proper-lockfile locking.
type FileAuthStorageBackend struct {
	authPath string
}

// NewFileAuthStorageBackend constructs a file-backed auth storage backend (default `<agentDir>/auth.json`).
func NewFileAuthStorageBackend(authPath ...string) *FileAuthStorageBackend {
	return &FileAuthStorageBackend{authPath: astResolveAuthPath(authPath...)}
}

func (b *FileAuthStorageBackend) ensureParentDir() error {
	dir := filepath.Dir(b.authPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, 0o700)
	}
	return nil
}

func (b *FileAuthStorageBackend) ensureFileExists() error {
	if _, err := os.Stat(b.authPath); os.IsNotExist(err) {
		return os.WriteFile(b.authPath, []byte("{}"), 0o600)
	}
	return nil
}

func (b *FileAuthStorageBackend) acquireLockSyncWithRetry() (func() error, error) {
	const maxAttempts = 10
	const delay = 20 * time.Millisecond
	for attempt := 1; ; attempt++ {
		release, err := lockfile.LockSync(b.authPath, nil)
		if err == nil {
			return release, nil
		}
		if astRetryableLockErrorCode(err) == "" || attempt == maxAttempts {
			return nil, err
		}
		time.Sleep(delay)
	}
}

// WithLock acquires a synchronous file lock around auth.json, invokes fn, and writes Next if non-nil.
func (b *FileAuthStorageBackend) WithLock(fn func(current *string) (AuthStorageLockResult, error)) (any, error) {
	if err := b.ensureParentDir(); err != nil {
		return nil, err
	}
	if err := b.ensureFileExists(); err != nil {
		return nil, err
	}

	release, err := b.acquireLockSyncWithRetry()
	if err != nil {
		return nil, err
	}
	defer func() {
		if release != nil {
			_ = release()
		}
	}()

	var current *string
	if raw, err := os.ReadFile(b.authPath); err == nil {
		s := string(raw)
		current = &s
	}

	lockRes, err := fn(current)
	if err != nil {
		return nil, err
	}
	if lockRes.Next != nil {
		if err := os.WriteFile(b.authPath, []byte(*lockRes.Next), 0o600); err != nil {
			return nil, err
		}
	}
	return lockRes.Result, nil
}

func (b *FileAuthStorageBackend) acquireLockAsync(
	ctx context.Context,
	onCompromised func(err error),
) (func() error, error) {
	const staleMs int64 = 30_000
	const maxDelayMs = 2_000.0
	deadline := time.Now().Add(30 * time.Second)
	retry := 0
	epermRetries := 0
	stale := staleMs

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		release, err := lockfile.Lock(ctx, b.authPath, &lockfile.Options{
			Stale:         &stale,
			OnCompromised: onCompromised,
		})
		if err == nil {
			if ctx.Err() != nil {
				_ = release()
				return nil, ctx.Err()
			}
			return release, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		code := astRetryableLockErrorCode(err)
		remaining := time.Until(deadline)
		if code == "" || remaining <= 0 {
			return nil, err
		}
		if code == "EPERM" {
			epermRetries++
			if epermRetries > astMaxEpermRetries {
				return nil, err
			}
		}

		baseDelayMs := math.Min(10*math.Pow(2, float64(retry)), maxDelayMs/2)
		retry++
		delayMs := math.Min(math.Round(baseDelayMs*(1+rand.Float64())), float64(remaining.Milliseconds()))
		delay := time.Duration(delayMs) * time.Millisecond
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// WithLockAsync acquires a file lock with exponential backoff and compromise detection.
func (b *FileAuthStorageBackend) WithLockAsync(
	ctx context.Context,
	fn func(current *string) (AuthStorageLockResult, error),
	_ *ai.AuthOperationOptions,
) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.ensureParentDir(); err != nil {
		return nil, err
	}
	if err := b.ensureFileExists(); err != nil {
		return nil, err
	}

	var compMu sync.Mutex
	var compromised bool
	var compromisedErr error
	checkCompromised := func() error {
		compMu.Lock()
		defer compMu.Unlock()
		if !compromised {
			return nil
		}
		if compromisedErr != nil {
			return compromisedErr
		}
		return errors.New("Auth storage lock was compromised")
	}

	release, err := b.acquireLockAsync(ctx, func(err error) {
		compMu.Lock()
		compromised = true
		compromisedErr = err
		compMu.Unlock()
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if release != nil {
			_ = release()
		}
	}()

	if err := checkCompromised(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var current *string
	if raw, err := os.ReadFile(b.authPath); err == nil {
		s := string(raw)
		current = &s
	}

	lockRes, err := fn(current)
	if err != nil {
		return nil, err
	}
	if err := checkCompromised(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lockRes.Next != nil {
		if err := os.WriteFile(b.authPath, []byte(*lockRes.Next), 0o600); err != nil {
			return nil, err
		}
	}
	if err := checkCompromised(); err != nil {
		return nil, err
	}
	return lockRes.Result, nil
}

// ReadOnlyAuthStorage implements ai.CredentialStore in read-only mode without running !command resolvers.
type ReadOnlyAuthStorage struct {
	mu       sync.Mutex
	authPath string
	doc      *jsonx.Object
	creds    map[string]ai.Credential
}

// NewReadOnlyAuthStorage constructs a ReadOnlyAuthStorage backed by `<agentDir>/auth.json` or authPath.
func NewReadOnlyAuthStorage(authPath ...string) *ReadOnlyAuthStorage {
	return &ReadOnlyAuthStorage{
		authPath: astResolveAuthPath(authPath...),
	}
}

func astValidateCredentialEntry(providerId string, val any) (ai.Credential, error) {
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
	}
	typVal, _ := obj.Get("type")
	typ, _ := typVal.(string)
	switch typ {
	case string(ai.AuthTypeApiKey):
		if obj.Has("key") {
			k, _ := obj.Get("key")
			if _, ok := k.(string); !ok {
				return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
			}
		}
		if obj.Has("env") {
			envVal, _ := obj.Get("env")
			envObj, ok := envVal.(*jsonx.Object)
			if !ok || envObj == nil {
				return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
			}
			for _, k := range envObj.Keys() {
				ev, _ := envObj.Get(k)
				if _, ok := ev.(string); !ok {
					return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
				}
			}
		}
		cred, err := ai.DecodeCredential(obj)
		if err != nil {
			return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
		}
		return cred, nil
	case string(ai.AuthTypeOAuth):
		accVal, hasAcc := obj.Get("access")
		refVal, hasRef := obj.Get("refresh")
		expVal, hasExp := obj.Get("expires")
		_, okAcc := accVal.(string)
		_, okRef := refVal.(string)
		expNum, okExp := expVal.(float64)
		if !hasAcc || !hasRef || !hasExp || !okAcc || !okRef || !okExp || math.IsNaN(expNum) || math.IsInf(expNum, 0) {
			return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
		}
		cred, err := ai.DecodeCredential(obj)
		if err != nil {
			return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
		}
		return cred, nil
	default:
		return nil, fmt.Errorf(`Invalid auth.json credential for provider "%s"`, providerId)
	}
}

func (r *ReadOnlyAuthStorage) load() (*jsonx.Object, map[string]ai.Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.doc != nil {
		return r.doc, r.creds, nil
	}

	raw, err := os.ReadFile(r.authPath)
	if err != nil {
		if os.IsNotExist(err) {
			r.doc = jsonx.NewObject()
			r.creds = make(map[string]ai.Credential)
			return r.doc, r.creds, nil
		}
		return nil, nil, fmt.Errorf("Failed to read auth.json: %w", err)
	}

	parsed, err := jsonx.ParseString(utils.StripBom(string(raw)))
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to read auth.json: %w", err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, nil, errors.New("Invalid auth.json: expected an object")
	}

	creds := make(map[string]ai.Credential, obj.Len())
	for _, providerId := range obj.Keys() {
		v, _ := obj.Get(providerId)
		cred, err := astValidateCredentialEntry(providerId, v)
		if err != nil {
			return nil, nil, err
		}
		creds[providerId] = cred
	}
	r.doc = obj
	r.creds = creds
	return r.doc, r.creds, nil
}

// Read returns a resolved copy of the provider's credential, leaving !command values unresolved.
func (r *ReadOnlyAuthStorage) Read(ctx context.Context, providerId string, _ *ai.AuthOperationOptions) (ai.Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, creds, err := r.load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cred := creds[providerId]
	if cred == nil {
		return nil, nil
	}
	cloned := astCloneCredential(cred)
	apiKey, ok := cloned.(*ai.ApiKeyCredential)
	if !ok || apiKey.Key == nil || *apiKey.Key == "" || IsCommandConfigValue(*apiKey.Key) {
		return cloned, nil
	}
	apiKey.Key = ResolveConfigValue(*apiKey.Key, apiKey.Env)
	return apiKey, nil
}

// List returns non-secret metadata for all credentials in auth.json.
func (r *ReadOnlyAuthStorage) List(ctx context.Context, _ *ai.AuthOperationOptions) ([]ai.CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	doc, creds, err := r.load()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]ai.CredentialInfo, 0, doc.Len())
	for _, providerId := range doc.Keys() {
		if cred := creds[providerId]; cred != nil {
			out = append(out, ai.CredentialInfo{
				ProviderId: providerId,
				Type:       cred.CredentialType(),
			})
		}
	}
	return out, nil
}

// Modify always fails on ReadOnlyAuthStorage.
func (r *ReadOnlyAuthStorage) Modify(
	_ context.Context,
	_ string,
	_ func(current ai.Credential) (ai.Credential, error),
	_ *ai.AuthOperationOptions,
) (ai.Credential, error) {
	return nil, ErrReadOnlyAuthStorage
}

// Delete always fails on ReadOnlyAuthStorage.
func (r *ReadOnlyAuthStorage) Delete(_ context.Context, _ string, _ *ai.AuthOperationOptions) error {
	return ErrReadOnlyAuthStorage
}

// InMemoryAuthStorageBackend is an in-memory AuthStorageBackend for tests and ephemeral stores.
type InMemoryAuthStorageBackend struct {
	syncMu  sync.Mutex
	asyncMu sync.Mutex
	hasVal  bool
	value   string
}

// NewInMemoryAuthStorageBackend creates an empty InMemoryAuthStorageBackend.
func NewInMemoryAuthStorageBackend() *InMemoryAuthStorageBackend {
	return &InMemoryAuthStorageBackend{}
}

func (m *InMemoryAuthStorageBackend) currentLocked() *string {
	if !m.hasVal {
		return nil
	}
	v := m.value
	return &v
}

// WithLock runs fn synchronously with the current serialized JSON string.
func (m *InMemoryAuthStorageBackend) WithLock(fn func(current *string) (AuthStorageLockResult, error)) (any, error) {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()

	res, err := fn(m.currentLocked())
	if err != nil {
		return nil, err
	}
	if res.Next != nil {
		m.value = *res.Next
		m.hasVal = true
	}
	return res.Result, nil
}

// WithLockAsync serializes async lock callbacks and honors ctx cancellation before/after callback execution.
func (m *InMemoryAuthStorageBackend) WithLockAsync(
	ctx context.Context,
	fn func(current *string) (AuthStorageLockResult, error),
	_ *ai.AuthOperationOptions,
) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.asyncMu.Lock()
	defer m.asyncMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type done struct {
		res AuthStorageLockResult
		err error
	}
	ch := make(chan done, 1)
	go func() {
		m.syncMu.Lock()
		res, err := fn(m.currentLocked())
		if err == nil && ctx.Err() == nil && res.Next != nil {
			m.value = *res.Next
			m.hasVal = true
		}
		m.syncMu.Unlock()
		ch <- done{res: res, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case out := <-ch:
		if out.err != nil {
			return nil, out.err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return out.res.Result, nil
	}
}

type astFileReload struct {
	cancel  context.CancelFunc
	done    chan struct{}
	doc     *jsonx.Object
	err     error
	readers int
}

type astFileReadState struct {
	mu       sync.Mutex
	doc      *jsonx.Object
	revision *string
	reload   *astFileReload
}

func (s *astFileReadState) update(doc *jsonx.Object, revision *string) {
	s.mu.Lock()
	s.doc = doc
	s.revision = revision
	s.mu.Unlock()
}

func (s *astFileReadState) snapshotDoc() *jsonx.Object {
	s.mu.Lock()
	doc := s.doc
	s.mu.Unlock()
	if doc == nil {
		return jsonx.NewObject()
	}
	return doc.Clone()
}

var (
	astSharedMu    sync.Mutex
	astSharedState *struct {
		authPath  string
		readState *astFileReadState
	}
)

// AuthStorage implements ai.CredentialStore backed by JSON storage and proper-lockfile locking.
type AuthStorage struct {
	storage   AuthStorageBackend
	authPath  string
	readState *astFileReadState
}

func astNewAuthStorage(storage AuthStorageBackend, authPath string) *AuthStorage {
	var rs *astFileReadState
	if authPath != "" {
		astSharedMu.Lock()
		if astSharedState != nil && astSharedState.authPath == authPath {
			rs = astSharedState.readState
		} else {
			rs = &astFileReadState{doc: jsonx.NewObject()}
			if astSharedState == nil {
				astSharedState = &struct {
					authPath  string
					readState *astFileReadState
				}{authPath: authPath, readState: rs}
			}
		}
		astSharedMu.Unlock()
	} else {
		rs = &astFileReadState{doc: jsonx.NewObject()}
	}

	a := &AuthStorage{
		storage:   storage,
		authPath:  authPath,
		readState: rs,
	}

	if authPath != "" {
		if rev := utils.GetFileRevision(authPath); rev != nil {
			rs.mu.Lock()
			match := rs.revision != nil && *rev == *rs.revision
			rs.mu.Unlock()
			if match {
				return a
			}
		}
	}
	_ = a.Reload()
	return a
}

// AuthStorageCreate constructs a file-backed AuthStorage (default `<agentDir>/auth.json`).
func AuthStorageCreate(authPath ...string) *AuthStorage {
	resolved := astResolveAuthPath(authPath...)
	return astNewAuthStorage(NewFileAuthStorageBackend(resolved), resolved)
}

// AuthStorageFromStorage constructs an AuthStorage from an arbitrary AuthStorageBackend.
func AuthStorageFromStorage(storage AuthStorageBackend) *AuthStorage {
	return astNewAuthStorage(storage, "")
}

// AuthStorageInMemory constructs an in-memory AuthStorage seeded with optional credentials.
func AuthStorageInMemory(data ...map[string]ai.Credential) *AuthStorage {
	storage := NewInMemoryAuthStorageBackend()
	doc := jsonx.NewObject()
	if len(data) > 0 && data[0] != nil {
		for k, v := range data[0] {
			doc.Set(k, v)
		}
	}
	encoded, err := jsonx.StringifyIndent(doc, "  ")
	if err == nil {
		_, _ = storage.WithLock(func(_ *string) (AuthStorageLockResult, error) {
			return AuthStorageLockResult{Next: &encoded}, nil
		})
	}
	return AuthStorageFromStorage(storage)
}

func astParseStorageDoc(content *string) (*jsonx.Object, error) {
	if content == nil || *content == "" {
		return jsonx.NewObject(), nil
	}
	parsed, err := jsonx.ParseString(utils.StripBom(*content))
	if err != nil {
		return nil, err
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, errors.New("Invalid auth.json: expected an object")
	}
	return obj, nil
}

// Reload synchronously reloads credentials from the storage backend, keeping the previous snapshot on error.
func (a *AuthStorage) Reload() error {
	var doc *jsonx.Object
	var rev *string
	_, err := a.storage.WithLock(func(current *string) (AuthStorageLockResult, error) {
		parsed, err := astParseStorageDoc(current)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		doc = parsed
		if a.authPath != "" {
			rev = utils.GetFileRevision(a.authPath)
		}
		return AuthStorageLockResult{}, nil
	})
	if err != nil {
		return err
	}
	a.readState.update(doc, rev)
	return nil
}

func (a *AuthStorage) reloadFromStorageAsync(ctx context.Context, options *ai.AuthOperationOptions) (*jsonx.Object, error) {
	res, err := a.storage.WithLockAsync(ctx, func(current *string) (AuthStorageLockResult, error) {
		doc, err := astParseStorageDoc(current)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		var rev *string
		if a.authPath != "" {
			rev = utils.GetFileRevision(a.authPath)
		}
		a.readState.update(doc, rev)
		return AuthStorageLockResult{Result: doc}, nil
	}, options)
	if err != nil {
		return nil, err
	}
	if doc, ok := res.(*jsonx.Object); ok {
		return doc, nil
	}
	return jsonx.NewObject(), nil
}

func (a *AuthStorage) readLatestDoc(ctx context.Context, options *ai.AuthOperationOptions) (*jsonx.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.authPath == "" {
		doc, err := a.reloadFromStorageAsync(ctx, options)
		if err != nil {
			if ctx.Done() != nil {
				return nil, err
			}
			return a.readState.snapshotDoc(), nil
		}
		return doc, nil
	}

	rev := utils.GetFileRevision(a.authPath)
	a.readState.mu.Lock()
	if rev != nil && a.readState.revision != nil && *rev == *a.readState.revision {
		snap := a.readState.doc.Clone()
		a.readState.mu.Unlock()
		return snap, nil
	}

	reload := a.readState.reload
	if reload == nil {
		reloadCtx, cancel := context.WithCancel(context.Background())
		reload = &astFileReload{
			cancel: cancel,
			done:   make(chan struct{}),
		}
		a.readState.reload = reload
		go func(r *astFileReload) {
			r.doc, r.err = a.reloadFromStorageAsync(reloadCtx, options)
			a.readState.mu.Lock()
			if a.readState.reload == r {
				a.readState.reload = nil
			}
			a.readState.mu.Unlock()
			close(r.done)
		}(reload)
	}
	reload.readers++
	a.readState.mu.Unlock()

	defer func() {
		a.readState.mu.Lock()
		reload.readers--
		if reload.readers == 0 && a.readState.reload == reload {
			a.readState.reload = nil
			reload.cancel()
		}
		a.readState.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-reload.done:
		if reload.err != nil {
			if ctx.Done() != nil {
				return nil, reload.err
			}
			return a.readState.snapshotDoc(), nil
		}
		if reload.doc == nil {
			return jsonx.NewObject(), nil
		}
		return reload.doc.Clone(), nil
	}
}

// Read returns the provider's credential, resolving any configured shell command or env references.
func (a *AuthStorage) Read(
	ctx context.Context,
	providerId string,
	options *ai.AuthOperationOptions,
) (ai.Credential, error) {
	doc, err := a.readLatestDoc(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, ok := doc.Get(providerId)
	if !ok || raw == nil {
		return nil, nil
	}
	cred, err := ai.DecodeCredential(raw)
	if err != nil {
		return nil, err
	}
	cloned := astCloneCredential(cred)
	apiKey, ok := cloned.(*ai.ApiKeyCredential)
	if !ok || apiKey.Key == nil {
		return cloned, nil
	}
	apiKey.Key = ResolveConfigValue(*apiKey.Key, apiKey.Env)
	return apiKey, nil
}

// Modify runs an atomic read-modify-write update on providerId under file lock,
// preserving existing file key order and untouched provider objects byte-for-byte.
func (a *AuthStorage) Modify(
	ctx context.Context,
	providerId string,
	fn func(current ai.Credential) (ai.Credential, error),
	options *ai.AuthOperationOptions,
) (ai.Credential, error) {
	var latestDoc *jsonx.Object
	var rev *string
	res, err := a.storage.WithLockAsync(ctx, func(current *string) (AuthStorageLockResult, error) {
		doc, err := astParseStorageDoc(current)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		var currentCred ai.Credential
		if raw, ok := doc.Get(providerId); ok && raw != nil {
			if decoded, err := ai.DecodeCredential(raw); err == nil {
				currentCred = decoded
			}
		}
		next, err := fn(currentCred)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		if next == nil {
			latestDoc = doc
			if a.authPath != "" {
				rev = utils.GetFileRevision(a.authPath)
			}
			return AuthStorageLockResult{Result: currentCred}, nil
		}
		merged := doc.Clone()
		merged.Set(providerId, next)
		encoded, err := jsonx.StringifyIndent(merged, "  ")
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		latestDoc = merged
		return AuthStorageLockResult{Result: next, Next: &encoded}, nil
	}, options)
	if err != nil {
		return nil, err
	}
	if latestDoc != nil {
		a.readState.update(latestDoc, rev)
	}
	if cred, ok := res.(ai.Credential); ok {
		return cred, nil
	}
	return nil, nil
}

// Delete removes providerId under lock while preserving existing key order and untouched entries.
func (a *AuthStorage) Delete(ctx context.Context, providerId string, options *ai.AuthOperationOptions) error {
	var latestDoc *jsonx.Object
	_, err := a.storage.WithLockAsync(ctx, func(current *string) (AuthStorageLockResult, error) {
		doc, err := astParseStorageDoc(current)
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		merged := doc.Clone()
		merged.Delete(providerId)
		encoded, err := jsonx.StringifyIndent(merged, "  ")
		if err != nil {
			return AuthStorageLockResult{}, err
		}
		latestDoc = merged
		return AuthStorageLockResult{Next: &encoded}, nil
	}, options)
	if err != nil {
		return err
	}
	if latestDoc != nil {
		a.readState.update(latestDoc, nil)
	}
	return nil
}

// List returns non-secret metadata for every provider stored in auth.json.
func (a *AuthStorage) List(ctx context.Context, options *ai.AuthOperationOptions) ([]ai.CredentialInfo, error) {
	doc, err := a.readLatestDoc(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]ai.CredentialInfo, 0, doc.Len())
	for _, providerId := range doc.Keys() {
		raw, _ := doc.Get(providerId)
		if obj, ok := raw.(*jsonx.Object); ok && obj != nil {
			typ, _ := obj.GetString("type")
			out = append(out, ai.CredentialInfo{
				ProviderId: providerId,
				Type:       ai.AuthType(typ),
			})
		}
	}
	return out, nil
}

// GetAuthPath returns the backing auth.json path when configured, or empty string for custom backends.
func (a *AuthStorage) GetAuthPath() string {
	return a.authPath
}

// ReadStoredCredential reads a single stored credential synchronously from auth.json
// without instantiating AuthStorage or resolving !command values.
func ReadStoredCredential(providerId string, authPath ...string) ai.Credential {
	resolved := astResolveAuthPath(authPath...)
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil
	}
	parsed, err := jsonx.ParseString(utils.StripBom(string(raw)))
	if err != nil {
		return nil
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	entry, ok := obj.Get(providerId)
	if !ok || entry == nil {
		return nil
	}
	cred, err := ai.DecodeCredential(entry)
	if err != nil {
		return nil
	}
	return cred
}
