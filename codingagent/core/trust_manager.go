// Ported from packages/coding-agent/src/core/trust-manager.ts (pi v1.5.2).

package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
	"unicode/utf16"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/lockfile"
)

// ProjectTrustDecision models true (trusted), false (untrusted), or nil (clear/undecided).
type ProjectTrustDecision = *bool

// ProjectTrustStoreEntry is the nearest matching trust entry in trust.json.
type ProjectTrustStoreEntry struct {
	Path     string
	Decision bool
}

// ProjectTrustUpdate represents one path decision to apply atomically to trust.json.
type ProjectTrustUpdate struct {
	Path     string
	Decision ProjectTrustDecision
}

// ProjectTrustOption describes an interactive trust choice and its persisted updates.
type ProjectTrustOption struct {
	Label     string
	Trusted   bool
	Updates   []ProjectTrustUpdate
	SavedPath *string
}

// ProjectTrustOptionsConfig configures GetProjectTrustOptions.
type ProjectTrustOptionsConfig struct {
	IncludeSessionOnly bool
}

var tmTrustRequiringConfigResources = []string{
	"settings.json",
	"mcp.json",
	"extensions",
	"skills",
	"prompts",
	"themes",
	"SYSTEM.md",
	"APPEND_SYSTEM.md",
}

func tmBoolPtr(v bool) ProjectTrustDecision {
	return &v
}

func tmNormalizeCwd(cwd string) string {
	resolved, err := utils.ResolvePath(cwd, "", nil)
	if err != nil {
		resolved = cwd
	}
	return utils.CanonicalizePath(resolved)
}

func tmFindTrustFileKeys(data *jsonx.Object, target string) []string {
	var matched []string
	for _, key := range data.Keys() {
		if utils.PathsEqual(key, target) {
			matched = append(matched, key)
		}
	}
	return matched
}

func tmFindNearestTrustEntry(data *jsonx.Object, cwd string) *ProjectTrustStoreEntry {
	currentDir := tmNormalizeCwd(cwd)
	for {
		for _, key := range tmFindTrustFileKeys(data, currentDir) {
			if b, ok := data.GetBool(key); ok {
				return &ProjectTrustStoreEntry{Path: key, Decision: b}
			}
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return nil
		}
		currentDir = parentDir
	}
}

// GetProjectTrustParentPath returns the canonicalized parent directory of cwd, or nil if at filesystem root.
func GetProjectTrustParentPath(cwd string) *string {
	trustPath := tmNormalizeCwd(cwd)
	parentDir := filepath.Dir(trustPath)
	if parentDir == trustPath {
		return nil
	}
	return &parentDir
}

// GetProjectTrustOptions returns the selectable trust choices for cwd.
func GetProjectTrustOptions(cwd string, options ...ProjectTrustOptionsConfig) []ProjectTrustOption {
	includeSessionOnly := len(options) > 0 && options[0].IncludeSessionOnly
	trustPath := tmNormalizeCwd(cwd)
	savedTrust := trustPath

	opts := []ProjectTrustOption{
		{
			Label:     "Trust",
			Trusted:   true,
			Updates:   []ProjectTrustUpdate{{Path: trustPath, Decision: tmBoolPtr(true)}},
			SavedPath: &savedTrust,
		},
	}
	if parentPath := GetProjectTrustParentPath(cwd); parentPath != nil {
		savedParent := *parentPath
		opts = append(opts, ProjectTrustOption{
			Label:   fmt.Sprintf("Trust parent folder (%s)", *parentPath),
			Trusted: true,
			Updates: []ProjectTrustUpdate{
				{Path: *parentPath, Decision: tmBoolPtr(true)},
				{Path: trustPath, Decision: nil},
			},
			SavedPath: &savedParent,
		})
	}
	if includeSessionOnly {
		opts = append(opts, ProjectTrustOption{
			Label:   "Trust (this session only)",
			Trusted: true,
			Updates: []ProjectTrustUpdate{},
		})
	}
	savedDeny := trustPath
	opts = append(opts, ProjectTrustOption{
		Label:     "Do not trust",
		Trusted:   false,
		Updates:   []ProjectTrustUpdate{{Path: trustPath, Decision: tmBoolPtr(false)}},
		SavedPath: &savedDeny,
	})
	if includeSessionOnly {
		opts = append(opts, ProjectTrustOption{
			Label:   "Do not trust (this session only)",
			Trusted: false,
			Updates: []ProjectTrustUpdate{},
		})
	}
	return opts
}

func tmReadTrustFile(path string) (*jsonx.Object, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return jsonx.NewObject(), nil
		}
		return nil, fmt.Errorf("Failed to read trust store %s: %w", path, err)
	}

	parsed, err := jsonx.ParseString(utils.StripBom(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("Failed to read trust store %s: %w", path, err)
	}

	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("Invalid trust store %s: expected an object", path)
	}

	data := jsonx.NewObject()
	for _, key := range obj.Keys() {
		v, _ := obj.Get(key)
		switch val := v.(type) {
		case bool:
			data.Set(key, val)
		case nil:
			data.Set(key, nil)
		default:
			quoted, _ := jsonx.Stringify(key)
			return nil, fmt.Errorf("Invalid trust store %s: value for %s must be true, false, or null", path, quoted)
		}
	}
	return data, nil
}

func tmCompareUTF16(a, b string) int {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	n := min(len(ua), len(ub))
	for i := range n {
		if ua[i] < ub[i] {
			return -1
		}
		if ua[i] > ub[i] {
			return 1
		}
	}
	if len(ua) < len(ub) {
		return -1
	}
	if len(ua) > len(ub) {
		return 1
	}
	return 0
}

func tmWriteTrustFile(path string, data *jsonx.Object) error {
	keys := data.Keys()
	slices.SortFunc(keys, tmCompareUTF16)

	sorted := jsonx.NewObject()
	for _, key := range keys {
		v, _ := data.Get(key)
		switch val := v.(type) {
		case bool:
			sorted.Set(key, val)
		case nil:
			sorted.Set(key, nil)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := jsonx.StringifyIndent(sorted, "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(encoded+"\n"), 0o644)
}

func tmAcquireTrustLockSync(path string) (func() error, error) {
	trustDir := filepath.Dir(path)
	if err := os.MkdirAll(trustDir, 0o755); err != nil {
		return nil, err
	}

	const maxAttempts = 10
	const delay = 20 * time.Millisecond
	realpath := false
	opts := &lockfile.Options{
		Realpath:     &realpath,
		LockfilePath: path + ".lock",
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		release, err := lockfile.LockSync(trustDir, opts)
		if err == nil {
			return release, nil
		}
		var lerr *lockfile.Error
		if !errors.As(err, &lerr) || lerr.Code != "ELOCKED" || attempt == maxAttempts {
			return nil, err
		}
		lastErr = err
		time.Sleep(delay)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("Failed to acquire trust store lock")
}

func tmWithTrustFileLock[T any](path string, fn func() (T, error)) (T, error) {
	var zero T
	release, err := tmAcquireTrustLockSync(path)
	if err != nil {
		return zero, err
	}
	defer func() {
		if release != nil {
			_ = release()
		}
	}()
	return fn()
}

// HasTrustRequiringProjectResources reports whether cwd contains project-local config
// resources or .agents/skills (excluding the user's global ~/.agents/skills) in cwd or ancestors.
func HasTrustRequiringProjectResources(cwd string) bool {
	homeEnv := os.Getenv("HOME")
	if homeEnv == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeEnv = h
		}
	}
	resolvedHome, err := utils.ResolvePath(homeEnv, "", nil)
	if err != nil {
		resolvedHome = homeEnv
	}
	homeDir := utils.CanonicalizePath(resolvedHome)
	userAgentsSkillsDir := filepath.Join(homeDir, ".agents", "skills")

	currentDir := tmNormalizeCwd(cwd)
	configDir := filepath.Join(currentDir, utils.ConfigDirName)
	for _, entry := range tmTrustRequiringConfigResources {
		if _, err := os.Stat(filepath.Join(configDir, entry)); err == nil {
			return true
		}
	}

	for {
		agentsSkillsDir := filepath.Join(currentDir, ".agents", "skills")
		if !utils.PathsEqual(agentsSkillsDir, userAgentsSkillsDir) {
			if _, err := os.Stat(agentsSkillsDir); err == nil {
				return true
			}
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return false
		}
		currentDir = parentDir
	}
}

// ProjectTrustStore persists per-directory project trust decisions in `<agentDir>/trust.json`.
type ProjectTrustStore struct {
	trustPath string
}

// NewProjectTrustStore opens or prepares `<agentDir>/trust.json`.
func NewProjectTrustStore(agentDir string) *ProjectTrustStore {
	resolved, err := utils.ResolvePath(agentDir, "", nil)
	if err != nil {
		resolved = agentDir
	}
	return &ProjectTrustStore{
		trustPath: filepath.Join(resolved, "trust.json"),
	}
}

// Get returns the nearest applicable trust decision for cwd (nil when undecided).
func (s *ProjectTrustStore) Get(cwd string) (ProjectTrustDecision, error) {
	entry, err := s.GetEntry(cwd)
	if err != nil || entry == nil {
		return nil, err
	}
	decision := entry.Decision
	return &decision, nil
}

// GetEntry returns the nearest applicable entry in trust.json for cwd.
func (s *ProjectTrustStore) GetEntry(cwd string) (*ProjectTrustStoreEntry, error) {
	return tmWithTrustFileLock(s.trustPath, func() (*ProjectTrustStoreEntry, error) {
		data, err := tmReadTrustFile(s.trustPath)
		if err != nil {
			return nil, err
		}
		return tmFindNearestTrustEntry(data, cwd), nil
	})
}

// Set persists or clears the trust decision for cwd.
func (s *ProjectTrustStore) Set(cwd string, decision ProjectTrustDecision) error {
	return s.SetMany([]ProjectTrustUpdate{{Path: cwd, Decision: decision}})
}

// SetMany atomically applies trust updates to trust.json under file lock.
func (s *ProjectTrustStore) SetMany(decisions []ProjectTrustUpdate) error {
	_, err := tmWithTrustFileLock(s.trustPath, func() (struct{}, error) {
		data, err := tmReadTrustFile(s.trustPath)
		if err != nil {
			return struct{}{}, err
		}
		for _, upd := range decisions {
			key := tmNormalizeCwd(upd.Path)
			for _, existingKey := range tmFindTrustFileKeys(data, key) {
				data.Delete(existingKey)
			}
			if upd.Decision != nil {
				data.Set(key, *upd.Decision)
			}
		}
		return struct{}{}, tmWriteTrustFile(s.trustPath, data)
	})
	return err
}
