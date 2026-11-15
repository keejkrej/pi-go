// Ported from packages/coding-agent/src/core/settings-manager.ts (pi v1.5.2).

package core

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/lockfile"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

// CacheWarmingModes lists all valid CacheWarmingMode values.
var CacheWarmingModes = []CacheWarmingMode{
	CacheWarmingOff,
	CacheWarmingStreaming,
	CacheWarmingIdle,
}

// DefaultToolNames are the tools enabled at startup when defaultTools does not change them.
var DefaultToolNames = []string{"read", "bash", "edit", "write"}

// ErrProjectNotTrusted is returned when attempting to persist project settings for an untrusted project.
var ErrProjectNotTrusted = errors.New("Project is not trusted; refusing to write project settings")

var (
	smaRuntimePlatform = func() string { return runtime.GOOS }
)

// IsToolModifier reports whether entry is a "+name" or "-name" tool selection modifier.
func IsToolModifier(entry any) bool {
	s, ok := entry.(string)
	if !ok {
		return false
	}
	return strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-")
}

// GetToolListError validates a tool list from --tools or SDK options.
// Returns nil when valid, or a non-nil error describing the mixed/pattern misuse.
func GetToolListError(entries []string) error {
	var modifiers []string
	for _, e := range entries {
		if IsToolModifier(e) {
			modifiers = append(modifiers, e)
		}
	}
	if len(modifiers) == 0 {
		return nil
	}
	if len(modifiers) < len(entries) {
		return errors.New("tool names cannot be mixed with +name or -name entries")
	}
	for _, entry := range modifiers {
		if strings.Contains(entry, "*") {
			return fmt.Errorf("+name and -name entries take exact tool names, not patterns: %s", entry)
		}
	}
	return nil
}

// ApplyToolModifiers applies +name and -name modifiers in entries to base in list order.
func ApplyToolModifiers(base []string, entries []string) []string {
	tools := slices.Clone(base)
	if tools == nil {
		tools = []string{}
	}
	for _, entry := range entries {
		if !IsToolModifier(entry) {
			continue
		}
		name := entry[1:]
		idx := slices.Index(tools, name)
		if strings.HasPrefix(entry, "+") && idx == -1 && name != "" {
			tools = append(tools, name)
		} else if strings.HasPrefix(entry, "-") && idx != -1 {
			tools = append(tools[:idx], tools[idx+1:]...)
		}
	}
	return tools
}

func smaMergeDefaultToolsDoc(base *jsonx.Object, overrides *jsonx.Object) (any, bool) {
	hasOverride := overrides != nil && overrides.Has("defaultTools")
	hasBase := base != nil && base.Has("defaultTools")
	if !hasOverride {
		if !hasBase {
			return nil, false
		}
		v, _ := base.Get("defaultTools")
		return smaCloneValue(v), true
	}
	overVal, _ := overrides.Get("defaultTools")
	if !hasBase {
		return smaCloneValue(overVal), true
	}
	baseVal, _ := base.Get("defaultTools")
	baseArr, okBase := baseVal.([]any)
	overArr, okOver := overVal.([]any)
	if !okBase || !okOver {
		return smaCloneValue(overVal), true
	}
	for _, item := range overArr {
		if !IsToolModifier(item) {
			return smaCloneValue(overVal), true
		}
	}
	combined := make([]any, 0, len(baseArr)+len(overArr))
	combined = append(combined, baseArr...)
	combined = append(combined, overArr...)
	return combined, true
}

func smaResolveDefaultTools(entries []string) []string {
	plain := make([]string, 0, len(entries))
	for _, e := range entries {
		if !IsToolModifier(e) {
			plain = append(plain, e)
		}
	}
	base := plain
	if len(plain) == 0 && len(entries) > 0 {
		base = DefaultToolNames
	}
	return ApplyToolModifiers(base, entries)
}

func smaCloneValue(v any) any {
	switch val := v.(type) {
	case *jsonx.Object:
		if val == nil {
			return nil
		}
		return val.Clone()
	case []any:
		cp := make([]any, len(val))
		for i, item := range val {
			cp[i] = smaCloneValue(item)
		}
		return cp
	default:
		return v
	}
}

func smaDeepMergeObjects(base *jsonx.Object, overrides *jsonx.Object) *jsonx.Object {
	if base == nil && overrides == nil {
		return jsonx.NewObject()
	}
	if base == nil {
		return overrides.Clone()
	}
	result := base.Clone()
	if overrides == nil {
		return result
	}
	for _, key := range overrides.Keys() {
		overVal, _ := overrides.Get(key)
		baseVal, hasBase := base.Get(key)
		if hasBase {
			baseObj, okB := baseVal.(*jsonx.Object)
			overObj, okO := overVal.(*jsonx.Object)
			if okB && baseObj != nil && okO && overObj != nil {
				result.Set(key, smaDeepMergeObjects(baseObj, overObj))
				continue
			}
		}
		result.Set(key, smaCloneValue(overVal))
	}
	return result
}

func smaDeepMergeSettingsDoc(base *jsonx.Object, overrides *jsonx.Object) *jsonx.Object {
	merged := smaDeepMergeObjects(base, overrides)
	if dt, ok := smaMergeDefaultToolsDoc(base, overrides); ok {
		merged.Set("defaultTools", dt)
	}
	return merged
}

func smaFormatInvalidValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(val)
	case string:
		return val
	case float64:
		if math.IsNaN(val) {
			return "NaN"
		}
		if math.IsInf(val, 1) {
			return "Infinity"
		}
		if math.IsInf(val, -1) {
			return "-Infinity"
		}
		return jsonx.FormatNumber(val)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case *jsonx.Object:
		return "[object Object]"
	case []any:
		parts := make([]string, len(val))
		for i, elem := range val {
			parts[i] = smaFormatInvalidValue(elem)
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprintf("%v", v)
	}
}

func smaIsNonNegativeSafeInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > sschMaxSafeInteger || math.Trunc(n) != n {
			return 0, false
		}
		return int(n), true
	case int:
		if n < 0 || float64(n) > sschMaxSafeInteger {
			return 0, false
		}
		return n, true
	case int64:
		if n < 0 || float64(n) > sschMaxSafeInteger {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func smaParseTimeoutSettingDoc(doc *jsonx.Object, settingName string) (*int64, error) {
	if doc == nil || !doc.Has(settingName) {
		return nil, nil
	}
	val, _ := doc.Get(settingName)
	timeoutMs := ParseHttpIdleTimeoutMs(val)
	if timeoutMs != nil {
		return timeoutMs, nil
	}
	return nil, fmt.Errorf("Invalid %s setting: %s", settingName, smaFormatInvalidValue(val))
}

// SettingsScope selects global or project settings storage.
type SettingsScope string

const (
	SettingsScopeGlobal  SettingsScope = "global"
	SettingsScopeProject SettingsScope = "project"
)

// SettingsManagerCreateOptions configures SettingsManager trust behavior.
type SettingsManagerCreateOptions struct {
	ProjectTrusted *bool
}

// SettingsError describes a non-fatal load or persistence error recorded by SettingsManager.
type SettingsError struct {
	Scope SettingsScope
	Path  *string
	Error error
}

// SettingsStorage abstracts locked read-modify-write access to global and project settings JSON.
type SettingsStorage interface {
	WithLock(scope SettingsScope, fn func(current *string) (next *string, err error)) error
}

func smaToSettingsError(scope SettingsScope, err error, path *string) SettingsError {
	se := SettingsError{
		Scope: scope,
		Error: err,
	}
	if path != nil && *path != "" {
		p := *path
		se.Path = &p
	}
	return se
}

// FileSettingsStorage implements SettingsStorage against `<agentDir>/settings.json` and `<cwd>/.pi/settings.json`.
type FileSettingsStorage struct {
	globalSettingsPath  string
	projectSettingsPath string
}

// NewFileSettingsStorage constructs file-backed SettingsStorage for cwd and agentDir.
func NewFileSettingsStorage(cwd, agentDir string) *FileSettingsStorage {
	resolvedCwd, err := utils.ResolvePath(cwd, "", nil)
	if err != nil {
		resolvedCwd = cwd
	}
	resolvedAgentDir, err := utils.ResolvePath(agentDir, "", nil)
	if err != nil {
		resolvedAgentDir = agentDir
	}
	return &FileSettingsStorage{
		globalSettingsPath:  filepath.Join(resolvedAgentDir, "settings.json"),
		projectSettingsPath: filepath.Join(resolvedCwd, utils.ConfigDirName, "settings.json"),
	}
}

func (f *FileSettingsStorage) acquireLockSyncWithRetry(path string) (func() error, error) {
	const maxAttempts = 10
	const delay = 20 * time.Millisecond
	realpath := false
	opts := &lockfile.Options{Realpath: &realpath}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		release, err := lockfile.LockSync(path, opts)
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
	return nil, errors.New("Failed to acquire settings lock")
}

// WithLock locks the scope settings file if it exists or when writing, reads its content, and writes next if non-nil.
func (f *FileSettingsStorage) WithLock(scope SettingsScope, fn func(current *string) (*string, error)) error {
	path := f.projectSettingsPath
	if scope == SettingsScopeGlobal {
		path = f.globalSettingsPath
	}
	dir := filepath.Dir(path)

	var release func() error
	defer func() {
		if release != nil {
			_ = release()
		}
	}()

	_, statErr := os.Stat(path)
	fileExists := statErr == nil
	if fileExists {
		var err error
		release, err = f.acquireLockSyncWithRetry(path)
		if err != nil {
			return err
		}
	}

	var current *string
	if fileExists {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(raw)
		current = &s
	}

	next, err := fn(current)
	if err != nil {
		return err
	}
	if next != nil {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		if release == nil {
			release, err = f.acquireLockSyncWithRetry(path)
			if err != nil {
				return err
			}
		}
		if err := os.WriteFile(path, []byte(*next), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// InMemorySettingsStorage holds global and project settings JSON strings in memory.
type InMemorySettingsStorage struct {
	mu         sync.Mutex
	hasGlobal  bool
	global     string
	hasProject bool
	project    string
}

// NewInMemorySettingsStorage creates an empty InMemorySettingsStorage.
func NewInMemorySettingsStorage() *InMemorySettingsStorage {
	return &InMemorySettingsStorage{}
}

// WithLock executes fn under storage lock for the specified scope.
func (m *InMemorySettingsStorage) WithLock(scope SettingsScope, fn func(current *string) (*string, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var current *string
	if scope == SettingsScopeGlobal {
		if m.hasGlobal {
			s := m.global
			current = &s
		}
	} else if m.hasProject {
		s := m.project
		current = &s
	}

	next, err := fn(current)
	if err != nil {
		return err
	}
	if next != nil {
		if scope == SettingsScopeGlobal {
			m.global = *next
			m.hasGlobal = true
		} else {
			m.project = *next
			m.hasProject = true
		}
	}
	return nil
}

// ResolvedBranchSummarySettings holds resolved branch summary settings.
type ResolvedBranchSummarySettings struct {
	ReserveTokens int
	SkipPrompt    bool
}

// ResolvedRetrySettings holds resolved agent retry loop settings.
type ResolvedRetrySettings struct {
	Enabled         bool
	MaxRetries      int
	BaseDelayMs     float64
	MaxAgentDelayMs float64
}

// ResolvedProviderRetrySettings holds resolved provider SDK retry settings.
type ResolvedProviderRetrySettings struct {
	TimeoutMs       *float64
	MaxRetries      *int
	MaxRetryDelayMs float64
}

// TerminalCapabilityOverrides is an alias for tui.TerminalCapabilityOverrides (Partial<TerminalCapabilities>).
type TerminalCapabilityOverrides = tui.TerminalCapabilityOverrides

// SettingsManager manages global and project settings.json persistence, migrations, overrides, and diagnostics.
type SettingsManager struct {
	mu                          sync.Mutex
	writeMu                     sync.Mutex
	storage                     SettingsStorage
	globalDoc                   *jsonx.Object
	projectDoc                  *jsonx.Object
	effectiveDoc                *jsonx.Object
	globalSettings              Settings
	projectSettings             Settings
	settings                    Settings
	projectTrusted              bool
	modifiedFields              map[string]struct{}
	modifiedNestedFields        map[string]map[string]struct{}
	modifiedProjectFields       map[string]struct{}
	modifiedProjectNestedFields map[string]map[string]struct{}
	globalSettingsLoadError     error
	projectSettingsLoadError    error
	errors                      []SettingsError
	settingsPaths               map[SettingsScope]string
}

func smaNewSettingsManager(
	storage SettingsStorage,
	initialGlobal Settings,
	initialGlobalDoc *jsonx.Object,
	initialProject Settings,
	initialProjectDoc *jsonx.Object,
	globalLoadError error,
	projectLoadError error,
	initialErrors []SettingsError,
	projectTrusted bool,
	settingsPaths map[SettingsScope]string,
) *SettingsManager {
	if initialGlobalDoc == nil {
		initialGlobalDoc = jsonx.NewObject()
	}
	if initialProjectDoc == nil {
		initialProjectDoc = jsonx.NewObject()
	}
	if settingsPaths == nil {
		settingsPaths = make(map[SettingsScope]string)
	}
	effectiveDoc := smaDeepMergeSettingsDoc(initialGlobalDoc, initialProjectDoc)
	return &SettingsManager{
		storage:                     storage,
		globalDoc:                   initialGlobalDoc,
		projectDoc:                  initialProjectDoc,
		effectiveDoc:                effectiveDoc,
		globalSettings:              initialGlobal,
		projectSettings:             initialProject,
		settings:                    smaDecodeSettingsTolerant(effectiveDoc),
		projectTrusted:              projectTrusted,
		modifiedFields:              make(map[string]struct{}),
		modifiedNestedFields:        make(map[string]map[string]struct{}),
		modifiedProjectFields:       make(map[string]struct{}),
		modifiedProjectNestedFields: make(map[string]map[string]struct{}),
		globalSettingsLoadError:     globalLoadError,
		projectSettingsLoadError:    projectLoadError,
		errors:                      slices.Clone(initialErrors),
		settingsPaths:               settingsPaths,
	}
}

// SettingsManagerCreate loads global (<agentDir>/settings.json) and project (<cwd>/.pi/settings.json) settings.
func SettingsManagerCreate(cwd string, agentDir ...string) *SettingsManager {
	dir := utils.GetAgentDir()
	if len(agentDir) > 0 && agentDir[0] != "" {
		dir = agentDir[0]
	}
	return SettingsManagerCreateWithOptions(cwd, dir, nil)
}

// SettingsManagerCreateWithOptions loads file-backed settings with explicit trust options.
func SettingsManagerCreateWithOptions(cwd, agentDir string, options *SettingsManagerCreateOptions) *SettingsManager {
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}
	resolvedCwd, err := utils.ResolvePath(cwd, "", nil)
	if err != nil {
		resolvedCwd = cwd
	}
	resolvedAgentDir, err := utils.ResolvePath(agentDir, "", nil)
	if err != nil {
		resolvedAgentDir = agentDir
	}
	storage := NewFileSettingsStorage(resolvedCwd, resolvedAgentDir)
	paths := map[SettingsScope]string{
		SettingsScopeGlobal:  filepath.Join(resolvedAgentDir, "settings.json"),
		SettingsScopeProject: filepath.Join(resolvedCwd, utils.ConfigDirName, "settings.json"),
	}
	return smaFromStorageWithPaths(storage, options, paths)
}

// SettingsManagerFromStorage constructs a SettingsManager from an arbitrary SettingsStorage backend.
func SettingsManagerFromStorage(storage SettingsStorage, options ...*SettingsManagerCreateOptions) *SettingsManager {
	var opts *SettingsManagerCreateOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return smaFromStorageWithPaths(storage, opts, nil)
}

func smaFromStorageWithPaths(
	storage SettingsStorage,
	options *SettingsManagerCreateOptions,
	settingsPaths map[SettingsScope]string,
) *SettingsManager {
	projectTrusted := true
	if options != nil && options.ProjectTrusted != nil {
		projectTrusted = *options.ProjectTrusted
	}
	globalSettings, globalDoc, globalErr := smaTryLoadFromStorage(storage, SettingsScopeGlobal, true)
	projectSettings, projectDoc, projectErr := smaTryLoadFromStorage(storage, SettingsScopeProject, projectTrusted)

	var initialErrors []SettingsError
	if globalErr != nil {
		var p *string
		if gp, ok := settingsPaths[SettingsScopeGlobal]; ok {
			p = &gp
		}
		initialErrors = append(initialErrors, smaToSettingsError(SettingsScopeGlobal, globalErr, p))
	}
	if projectErr != nil {
		var p *string
		if pp, ok := settingsPaths[SettingsScopeProject]; ok {
			p = &pp
		}
		initialErrors = append(initialErrors, smaToSettingsError(SettingsScopeProject, projectErr, p))
	}

	return smaNewSettingsManager(
		storage,
		globalSettings,
		globalDoc,
		projectSettings,
		projectDoc,
		globalErr,
		projectErr,
		initialErrors,
		projectTrusted,
		settingsPaths,
	)
}

// SettingsManagerInMemory constructs an in-memory SettingsManager seeded with optional initial settings.
func SettingsManagerInMemory(settings ...*Settings) *SettingsManager {
	var s *Settings
	if len(settings) > 0 {
		s = settings[0]
	}
	return SettingsManagerInMemoryWithOptions(s, nil)
}

// SettingsManagerInMemoryWithOptions constructs an in-memory SettingsManager with trust options.
func SettingsManagerInMemoryWithOptions(settings *Settings, options *SettingsManagerCreateOptions) *SettingsManager {
	storage := NewInMemorySettingsStorage()
	doc := smaSettingsToDoc(settings)
	smaMigrateObject(doc)
	encoded, err := jsonx.StringifyIndent(doc, "  ")
	if err == nil {
		_ = storage.WithLock(SettingsScopeGlobal, func(_ *string) (*string, error) {
			return &encoded, nil
		})
	}
	return SettingsManagerFromStorage(storage, options)
}

func smaTryLoadFromStorage(storage SettingsStorage, scope SettingsScope, projectTrusted bool) (Settings, *jsonx.Object, error) {
	if scope == SettingsScopeProject && !projectTrusted {
		return Settings{}, jsonx.NewObject(), nil
	}
	var content *string
	if err := storage.WithLock(scope, func(current *string) (*string, error) {
		content = current
		return nil, nil
	}); err != nil {
		return Settings{}, jsonx.NewObject(), err
	}
	if content == nil || *content == "" {
		return Settings{}, jsonx.NewObject(), nil
	}
	parsed, err := jsonx.ParseString(utils.StripBom(*content))
	if err != nil {
		return Settings{}, jsonx.NewObject(), err
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return Settings{}, jsonx.NewObject(), errors.New("Invalid settings.json: expected an object")
	}
	smaMigrateObject(obj)
	return smaDecodeSettingsTolerant(obj), obj, nil
}

func smaMigrateObject(obj *jsonx.Object) {
	if obj == nil {
		return
	}
	if obj.Has("queueMode") && !obj.Has("steeringMode") {
		if qm, ok := obj.Get("queueMode"); ok {
			obj.Set("steeringMode", qm)
		}
		obj.Delete("queueMode")
	}
	if !obj.Has("transport") {
		if ws, ok := obj.GetBool("websockets"); ok {
			if ws {
				obj.Set("transport", string(TransportWebsocket))
			} else {
				obj.Set("transport", string(TransportSse))
			}
			obj.Delete("websockets")
		}
	}
	if skillsObj, ok := obj.GetObject("skills"); ok && skillsObj != nil {
		if esc, hasEsc := skillsObj.Get("enableSkillCommands"); hasEsc && !obj.Has("enableSkillCommands") {
			obj.Set("enableSkillCommands", esc)
		}
		if dirs, ok := skillsObj.GetArray("customDirectories"); ok && len(dirs) > 0 {
			obj.Set("skills", dirs)
		} else {
			obj.Delete("skills")
		}
	}
	if retryObj, ok := obj.GetObject("retry"); ok && retryObj != nil {
		if maxDelay, ok := retryObj.GetNumber("maxDelayMs"); ok {
			provObj, hasProv := retryObj.GetObject("provider")
			var existing any
			if hasProv && provObj != nil {
				existing, _ = provObj.Get("maxRetryDelayMs")
			}
			if existing == nil {
				nextProv := jsonx.NewObject()
				if hasProv && provObj != nil {
					nextProv = provObj.Clone()
				}
				nextProv.Set("maxRetryDelayMs", maxDelay)
				retryObj.Set("provider", nextProv)
			}
		}
		retryObj.Delete("maxDelayMs")
	}
}

func smaEncodeFieldValue(v any) (any, bool) {
	encoded, err := jsonx.Encode(v)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func smaSettingsToDoc(s *Settings) *jsonx.Object {
	doc := jsonx.NewObject()
	if s == nil {
		return doc
	}
	// Encode typed fields via jsonx.Encode first, then overlay raw float64 values in compaction
	// so NaN/+Inf/-Inf overrides passed programmatically preserve exact JS runtime error text.
	if raw, err := jsonx.Encode(s); err == nil {
		if obj, ok := raw.(*jsonx.Object); ok && obj != nil {
			doc = obj
		}
	}
	if s.Compaction != nil {
		compObj, ok := doc.GetObject("compaction")
		if !ok || compObj == nil {
			compObj = jsonx.NewObject()
		} else {
			compObj = compObj.Clone()
		}
		if s.Compaction.ReserveTokens != nil {
			compObj.Set("reserveTokens", *s.Compaction.ReserveTokens)
		}
		if s.Compaction.KeepRecentTokens != nil {
			compObj.Set("keepRecentTokens", *s.Compaction.KeepRecentTokens)
		}
		if s.Compaction.ModelOverrides != nil {
			moObj, ok := compObj.GetObject("modelOverrides")
			if !ok || moObj == nil {
				moObj = jsonx.NewObject()
			} else {
				moObj = moObj.Clone()
			}
			for _, k := range s.Compaction.ModelOverrides.Keys() {
				ov, _ := s.Compaction.ModelOverrides.Get(k)
				if ov == nil {
					continue
				}
				ovObj, ok := moObj.GetObject(k)
				if !ok || ovObj == nil {
					ovObj = jsonx.NewObject()
				} else {
					ovObj = ovObj.Clone()
				}
				if ov.ReserveTokens != nil {
					ovObj.Set("reserveTokens", *ov.ReserveTokens)
				}
				if ov.KeepRecentTokens != nil {
					ovObj.Set("keepRecentTokens", *ov.KeepRecentTokens)
				}
				moObj.Set(k, ovObj)
			}
			compObj.Set("modelOverrides", moObj)
		}
		doc.Set("compaction", compObj)
	}
	return doc
}

func smaDecodeSettingsTolerant(doc *jsonx.Object) Settings {
	var out Settings
	if doc == nil {
		return out
	}
	_ = jsonx.Decode(doc, &out)
	// Ensure explicit empty array settings stay non-nil even if partial errors existed elsewhere.
	if arr, ok := doc.GetArray("defaultTools"); ok {
		tools := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				tools = append(tools, s)
			}
		}
		out.DefaultTools = tools
	}
	if compObj, ok := doc.GetObject("compaction"); ok && compObj != nil {
		var cs SettingsCompactionSettings
		if b, ok := compObj.GetBool("enabled"); ok {
			cs.Enabled = &b
		}
		if rt, ok := compObj.GetNumber("reserveTokens"); ok {
			cs.ReserveTokens = &rt
		}
		if kr, ok := compObj.GetNumber("keepRecentTokens"); ok {
			cs.KeepRecentTokens = &kr
		}
		if moObj, ok := compObj.GetObject("modelOverrides"); ok && moObj != nil {
			m := omap.NewMap[string, *CompactionModelOverride]()
			for _, k := range moObj.Keys() {
				if ovObj, ok := moObj.GetObject(k); ok && ovObj != nil {
					var ov CompactionModelOverride
					if rt, ok := ovObj.GetNumber("reserveTokens"); ok {
						ov.ReserveTokens = &rt
					}
					if kr, ok := ovObj.GetNumber("keepRecentTokens"); ok {
						ov.KeepRecentTokens = &kr
					}
					m.Set(k, &ov)
				}
			}
			cs.ModelOverrides = m
		}
		out.Compaction = &cs
	}
	return out
}

func smaCloneSettings(s Settings) Settings {
	doc := smaSettingsToDoc(&s)
	return smaDecodeSettingsTolerant(doc)
}

func smaRandomUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// GetSettings returns a copy of the effective settings (global merged with project and runtime overrides).
func (m *SettingsManager) GetSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return smaCloneSettings(m.settings)
}

// GetGlobalSettings returns a copy of the global settings layer.
func (m *SettingsManager) GetGlobalSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return smaCloneSettings(m.globalSettings)
}

// GetProjectSettings returns a copy of the project settings layer.
func (m *SettingsManager) GetProjectSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return smaCloneSettings(m.projectSettings)
}

// IsProjectTrusted reports whether project-scoped settings are loaded and writable.
func (m *SettingsManager) IsProjectTrusted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectTrusted
}

// SetProjectTrusted enables or disables project-scoped settings and reloads project settings when trusted.
func (m *SettingsManager) SetProjectTrusted(trusted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projectTrusted == trusted {
		return
	}
	m.projectTrusted = trusted
	clear(m.modifiedProjectFields)
	clear(m.modifiedProjectNestedFields)

	if !trusted {
		m.projectSettings = Settings{}
		m.projectDoc = jsonx.NewObject()
		m.projectSettingsLoadError = nil
		m.effectiveDoc = smaDeepMergeSettingsDoc(m.globalDoc, m.projectDoc)
		m.settings = smaDecodeSettingsTolerant(m.effectiveDoc)
		return
	}

	ps, pdoc, perr := smaTryLoadFromStorage(m.storage, SettingsScopeProject, trusted)
	m.projectSettings = ps
	m.projectDoc = pdoc
	m.projectSettingsLoadError = perr
	if perr != nil {
		m.recordErrorLocked(SettingsScopeProject, perr)
	}
	m.effectiveDoc = smaDeepMergeSettingsDoc(m.globalDoc, m.projectDoc)
	m.settings = smaDecodeSettingsTolerant(m.effectiveDoc)
}

// Reload flushes pending writes and reloads global and project settings from storage.
func (m *SettingsManager) Reload() error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	gs, gdoc, gerr := smaTryLoadFromStorage(m.storage, SettingsScopeGlobal, true)
	if gerr == nil {
		m.globalSettings = gs
		m.globalDoc = gdoc
		m.globalSettingsLoadError = nil
	} else {
		m.globalSettingsLoadError = gerr
		m.recordErrorLocked(SettingsScopeGlobal, gerr)
	}

	clear(m.modifiedFields)
	clear(m.modifiedNestedFields)
	clear(m.modifiedProjectFields)
	clear(m.modifiedProjectNestedFields)

	ps, pdoc, perr := smaTryLoadFromStorage(m.storage, SettingsScopeProject, m.projectTrusted)
	if perr == nil {
		m.projectSettings = ps
		m.projectDoc = pdoc
		m.projectSettingsLoadError = nil
	} else {
		m.projectSettingsLoadError = perr
		m.recordErrorLocked(SettingsScopeProject, perr)
	}

	m.effectiveDoc = smaDeepMergeSettingsDoc(m.globalDoc, m.projectDoc)
	m.settings = smaDecodeSettingsTolerant(m.effectiveDoc)
	return nil
}

// ApplyOverrides merges additional in-memory overrides on top of effective settings without persisting.
func (m *SettingsManager) ApplyOverrides(overrides *Settings) {
	if overrides == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	overrideDoc := smaSettingsToDoc(overrides)
	m.effectiveDoc = smaDeepMergeSettingsDoc(m.effectiveDoc, overrideDoc)
	m.settings = smaDecodeSettingsTolerant(m.effectiveDoc)
}

func (m *SettingsManager) markModifiedLocked(field string, nestedKey ...string) {
	m.modifiedFields[field] = struct{}{}
	if len(nestedKey) > 0 && nestedKey[0] != "" {
		nk := nestedKey[0]
		if m.modifiedNestedFields[field] == nil {
			m.modifiedNestedFields[field] = make(map[string]struct{})
		}
		m.modifiedNestedFields[field][nk] = struct{}{}
	}
}

func (m *SettingsManager) markProjectModifiedLocked(field string, nestedKey ...string) {
	m.modifiedProjectFields[field] = struct{}{}
	if len(nestedKey) > 0 && nestedKey[0] != "" {
		nk := nestedKey[0]
		if m.modifiedProjectNestedFields[field] == nil {
			m.modifiedProjectNestedFields[field] = make(map[string]struct{})
		}
		m.modifiedProjectNestedFields[field][nk] = struct{}{}
	}
}

func (m *SettingsManager) recordErrorLocked(scope SettingsScope, err error) {
	var p *string
	if sp, ok := m.settingsPaths[scope]; ok {
		p = &sp
	}
	m.errors = append(m.errors, smaToSettingsError(scope, err, p))
}

func (m *SettingsManager) clearModifiedScopeLocked(scope SettingsScope) {
	if scope == SettingsScopeGlobal {
		clear(m.modifiedFields)
		clear(m.modifiedNestedFields)
		return
	}
	clear(m.modifiedProjectFields)
	clear(m.modifiedProjectNestedFields)
}

func smaCloneModifiedFields(src map[string]struct{}) []string {
	out := make([]string, 0, len(src))
	for k := range src {
		out = append(out, k)
	}
	return out
}

func smaCloneModifiedNested(src map[string]map[string]struct{}) map[string][]string {
	out := make(map[string][]string, len(src))
	for k, v := range src {
		keys := make([]string, 0, len(v))
		for nk := range v {
			keys = append(keys, nk)
		}
		out[k] = keys
	}
	return out
}

func (m *SettingsManager) persistScopedSettings(
	scope SettingsScope,
	snapshotDoc *jsonx.Object,
	modifiedFields []string,
	modifiedNestedFields map[string][]string,
) error {
	return m.storage.WithLock(scope, func(current *string) (*string, error) {
		doc := jsonx.NewObject()
		if current != nil && *current != "" {
			parsed, err := jsonx.ParseString(utils.StripBom(*current))
			if err != nil {
				return nil, err
			}
			obj, ok := parsed.(*jsonx.Object)
			if !ok || obj == nil {
				return nil, errors.New("Invalid settings.json: expected an object")
			}
			smaMigrateObject(obj)
			doc = obj
		}

		for _, field := range modifiedFields {
			snapVal, hasSnap := snapshotDoc.Get(field)
			if nestedKeys, hasNested := modifiedNestedFields[field]; hasNested {
				inMemNested, okMem := snapVal.(*jsonx.Object)
				if hasSnap && okMem && inMemNested != nil {
					baseNested, okBase := doc.GetObject(field)
					if !okBase || baseNested == nil {
						baseNested = jsonx.NewObject()
					} else {
						baseNested = baseNested.Clone()
					}
					for _, nestedKey := range nestedKeys {
						if nv, ok := inMemNested.Get(nestedKey); ok {
							baseNested.Set(nestedKey, smaCloneValue(nv))
						} else {
							baseNested.Delete(nestedKey)
						}
					}
					doc.Set(field, baseNested)
					continue
				}
			}

			if !hasSnap {
				doc.Delete(field)
			} else {
				doc.Set(field, smaCloneValue(snapVal))
			}
		}

		encoded, err := jsonx.StringifyIndent(doc, "  ")
		if err != nil {
			return nil, err
		}
		return &encoded, nil
	})
}

func (m *SettingsManager) saveGlobalLocked() {
	m.effectiveDoc = smaDeepMergeSettingsDoc(m.globalDoc, m.projectDoc)
	m.settings = smaDecodeSettingsTolerant(m.effectiveDoc)
	if m.globalSettingsLoadError != nil {
		return
	}
	snapDoc := m.globalDoc.Clone()
	modFields := smaCloneModifiedFields(m.modifiedFields)
	modNested := smaCloneModifiedNested(m.modifiedNestedFields)
	m.clearModifiedScopeLocked(SettingsScopeGlobal)

	m.mu.Unlock()
	m.writeMu.Lock()
	err := m.persistScopedSettings(SettingsScopeGlobal, snapDoc, modFields, modNested)
	m.writeMu.Unlock()
	m.mu.Lock()
	if err != nil {
		m.recordErrorLocked(SettingsScopeGlobal, err)
	}
}

func (m *SettingsManager) saveProjectLocked() error {
	if !m.projectTrusted {
		return ErrProjectNotTrusted
	}
	m.effectiveDoc = smaDeepMergeSettingsDoc(m.globalDoc, m.projectDoc)
	m.settings = smaDecodeSettingsTolerant(m.effectiveDoc)
	if m.projectSettingsLoadError != nil {
		return nil
	}
	snapDoc := m.projectDoc.Clone()
	modFields := smaCloneModifiedFields(m.modifiedProjectFields)
	modNested := smaCloneModifiedNested(m.modifiedProjectNestedFields)
	m.clearModifiedScopeLocked(SettingsScopeProject)

	m.mu.Unlock()
	m.writeMu.Lock()
	err := m.persistScopedSettings(SettingsScopeProject, snapDoc, modFields, modNested)
	m.writeMu.Unlock()
	m.mu.Lock()
	if err != nil {
		m.recordErrorLocked(SettingsScopeProject, err)
	}
	return nil
}

// Flush waits for any in-flight serialized settings writes to complete.
func (m *SettingsManager) Flush() error {
	m.writeMu.Lock()
	m.writeMu.Unlock()
	return nil
}

// DrainErrors returns and clears all accumulated settings load/write errors.
func (m *SettingsManager) DrainErrors() []SettingsError {
	m.mu.Lock()
	defer m.mu.Unlock()
	drained := slices.Clone(m.errors)
	m.errors = nil
	return drained
}

func (m *SettingsManager) setGlobalFieldLocked(field string, value any) {
	if value == nil {
		m.globalDoc.Delete(field)
	} else {
		if encoded, ok := smaEncodeFieldValue(value); ok {
			m.globalDoc.Set(field, encoded)
		}
	}
	m.markModifiedLocked(field)
	m.saveGlobalLocked()
}

func (m *SettingsManager) setGlobalNestedFieldLocked(field, nestedKey string, value any) {
	nestedObj, ok := m.globalDoc.GetObject(field)
	if !ok || nestedObj == nil {
		nestedObj = jsonx.NewObject()
	} else {
		nestedObj = nestedObj.Clone()
	}
	if value == nil {
		nestedObj.Delete(nestedKey)
	} else if encoded, ok := smaEncodeFieldValue(value); ok {
		nestedObj.Set(nestedKey, encoded)
	}
	m.globalDoc.Set(field, nestedObj)
	m.markModifiedLocked(field, nestedKey)
	m.saveGlobalLocked()
}

// GetLastChangelogVersion returns the last acknowledged changelog version.
func (m *SettingsManager) GetLastChangelogVersion() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.LastChangelogVersion == nil {
		return nil
	}
	v := *m.settings.LastChangelogVersion
	return &v
}

// SetLastChangelogVersion persists lastChangelogVersion in global settings.
func (m *SettingsManager) SetLastChangelogVersion(version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.LastChangelogVersion = &version
	m.setGlobalFieldLocked("lastChangelogVersion", version)
}

// GetExtensionChangelogVersion returns the last seen changelog version for key.
func (m *SettingsManager) GetExtensionChangelogVersion(key string) *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.ExtensionChangelogVersions == nil {
		return nil
	}
	v, ok := m.settings.ExtensionChangelogVersions.Get(key)
	if !ok {
		return nil
	}
	return &v
}

// SetExtensionChangelogVersion persists extensionChangelogVersions[key] in global settings.
func (m *SettingsManager) SetExtensionChangelogVersion(key, version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.ExtensionChangelogVersions == nil {
		m.globalSettings.ExtensionChangelogVersions = omap.NewMap[string, string]()
	}
	m.globalSettings.ExtensionChangelogVersions.Set(key, version)
	m.setGlobalNestedFieldLocked("extensionChangelogVersions", key, version)
}

// GetSessionDir returns the normalized custom session directory setting, if set.
func (m *SettingsManager) GetSessionDir() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.SessionDir == nil {
		return nil
	}
	sd := *m.settings.SessionDir
	if sd != "" {
		if norm, err := utils.NormalizePath(sd, nil); err == nil {
			sd = norm
		}
	}
	return &sd
}

// GetDefaultProvider returns the default provider ID, if configured.
func (m *SettingsManager) GetDefaultProvider() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.DefaultProvider == nil {
		return nil
	}
	v := *m.settings.DefaultProvider
	return &v
}

// GetDefaultModel returns the default model ID, if configured.
func (m *SettingsManager) GetDefaultModel() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.DefaultModel == nil {
		return nil
	}
	v := *m.settings.DefaultModel
	return &v
}

// SetDefaultProvider persists defaultProvider in global settings.
func (m *SettingsManager) SetDefaultProvider(provider string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.DefaultProvider = &provider
	m.setGlobalFieldLocked("defaultProvider", provider)
}

// SetDefaultModel persists defaultModel in global settings.
func (m *SettingsManager) SetDefaultModel(modelId string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.DefaultModel = &modelId
	m.setGlobalFieldLocked("defaultModel", modelId)
}

// SetDefaultModelAndProvider persists both defaultProvider and defaultModel atomically in memory and global storage.
func (m *SettingsManager) SetDefaultModelAndProvider(provider, modelId string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.DefaultProvider = &provider
	m.globalSettings.DefaultModel = &modelId
	m.globalDoc.Set("defaultProvider", provider)
	m.globalDoc.Set("defaultModel", modelId)
	m.markModifiedLocked("defaultProvider")
	m.markModifiedLocked("defaultModel")
	m.saveGlobalLocked()
}

// GetSteeringMode returns the configured steering queue mode, defaulting to one-at-a-time.
func (m *SettingsManager) GetSteeringMode() agent.QueueMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.SteeringMode != nil && *m.settings.SteeringMode != "" {
		return *m.settings.SteeringMode
	}
	return SettingsDefaults.SteeringMode
}

// SetSteeringMode persists steeringMode in global settings.
func (m *SettingsManager) SetSteeringMode(mode agent.QueueMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.SteeringMode = &mode
	m.setGlobalFieldLocked("steeringMode", string(mode))
}

// GetFollowUpMode returns the configured follow-up queue mode, defaulting to one-at-a-time.
func (m *SettingsManager) GetFollowUpMode() agent.QueueMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.FollowUpMode != nil && *m.settings.FollowUpMode != "" {
		return *m.settings.FollowUpMode
	}
	return SettingsDefaults.FollowUpMode
}

// SetFollowUpMode persists followUpMode in global settings.
func (m *SettingsManager) SetFollowUpMode(mode agent.QueueMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.FollowUpMode = &mode
	m.setGlobalFieldLocked("followUpMode", string(mode))
}

// GetThemeSetting returns the raw configured theme string if present.
func (m *SettingsManager) GetThemeSetting() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.effectiveDoc.GetString("theme"); ok {
		return &s
	}
	return nil
}

// GetTheme returns the built-in theme name (returns nil if configured as a path containing '/').
func (m *SettingsManager) GetTheme() *string {
	t := m.GetThemeSetting()
	if t == nil || strings.Contains(*t, "/") {
		return nil
	}
	return t
}

// SetTheme persists theme in global settings.
func (m *SettingsManager) SetTheme(theme string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.Theme = &theme
	m.setGlobalFieldLocked("theme", theme)
}

// GetDefaultThinkingLevel returns the global default thinking level override, if set.
func (m *SettingsManager) GetDefaultThinkingLevel() *agent.ThinkingLevel {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.DefaultThinkingLevel == nil {
		return nil
	}
	v := *m.settings.DefaultThinkingLevel
	return &v
}

// SetDefaultThinkingLevel persists defaultThinkingLevel in global settings.
func (m *SettingsManager) SetDefaultThinkingLevel(level agent.ThinkingLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.DefaultThinkingLevel = &level
	m.setGlobalFieldLocked("defaultThinkingLevel", string(level))
}

// GetModelThinkingLevel returns the thinking level override for "provider/modelId", if configured.
func (m *SettingsManager) GetModelThinkingLevel(provider, modelId string) *agent.ThinkingLevel {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.ModelThinkingLevels == nil {
		return nil
	}
	v, ok := m.settings.ModelThinkingLevels.Get(provider + "/" + modelId)
	if !ok {
		return nil
	}
	return &v
}

// GetAllModelThinkingLevels returns a copy of all per-model thinking level overrides.
func (m *SettingsManager) GetAllModelThinkingLevels() map[string]agent.ThinkingLevel {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]agent.ThinkingLevel)
	if m.settings.ModelThinkingLevels != nil {
		for _, k := range m.settings.ModelThinkingLevels.Keys() {
			v, _ := m.settings.ModelThinkingLevels.Get(k)
			out[k] = v
		}
	}
	return out
}

// SetModelThinkingLevel persists a per-model thinking level override in global settings.
func (m *SettingsManager) SetModelThinkingLevel(provider, modelId string, level agent.ThinkingLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.ModelThinkingLevels == nil {
		m.globalSettings.ModelThinkingLevels = omap.NewMap[string, agent.ThinkingLevel]()
	}
	key := provider + "/" + modelId
	m.globalSettings.ModelThinkingLevels.Set(key, level)
	m.setGlobalFieldLocked("modelThinkingLevels", m.globalSettings.ModelThinkingLevels)
}

// RemoveModelThinkingLevel removes a per-model thinking level override, deleting modelThinkingLevels when empty.
func (m *SettingsManager) RemoveModelThinkingLevel(provider, modelId string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.ModelThinkingLevels == nil {
		return
	}
	m.globalSettings.ModelThinkingLevels.Delete(provider + "/" + modelId)
	if m.globalSettings.ModelThinkingLevels.Len() == 0 {
		m.globalSettings.ModelThinkingLevels = nil
		m.setGlobalFieldLocked("modelThinkingLevels", nil)
		return
	}
	m.setGlobalFieldLocked("modelThinkingLevels", m.globalSettings.ModelThinkingLevels)
}

// GetTransport returns the preferred wire transport setting.
func (m *SettingsManager) GetTransport() TransportSetting {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Transport != nil && *m.settings.Transport != "" {
		return *m.settings.Transport
	}
	return SettingsDefaults.Transport
}

// SetTransport persists transport in global settings.
func (m *SettingsManager) SetTransport(transport TransportSetting) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.Transport = &transport
	m.setGlobalFieldLocked("transport", string(transport))
}

// GetSessionShareDestination returns the /share destination setting.
func (m *SettingsManager) GetSessionShareDestination() SessionShareDestination {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.SessionShareDestination != nil {
		d := *m.settings.SessionShareDestination
		if d == SessionShareDestinationGist || d == SessionShareDestinationRadius {
			return d
		}
	}
	return SettingsDefaults.SessionShareDestination
}

// SetSessionShareDestination persists sessionShareDestination in global settings.
func (m *SettingsManager) SetSessionShareDestination(destination SessionShareDestination) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.SessionShareDestination = &destination
	m.setGlobalFieldLocked("sessionShareDestination", string(destination))
}

// GetCompactionEnabled reports whether automatic compaction is enabled.
func (m *SettingsManager) GetCompactionEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Compaction != nil && m.settings.Compaction.Enabled != nil {
		return *m.settings.Compaction.Enabled
	}
	return SettingsDefaults.Compaction.Enabled
}

// SetCompactionEnabled persists compaction.enabled in global settings.
func (m *SettingsManager) SetCompactionEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Compaction == nil {
		m.globalSettings.Compaction = &SettingsCompactionSettings{}
	}
	m.globalSettings.Compaction.Enabled = &enabled
	m.setGlobalNestedFieldLocked("compaction", "enabled", enabled)
}

func (m *SettingsManager) getCompactionTokenSettingLocked(field string, defaultVal int, model ...*ai.Model) (int, error) {
	compObj, _ := m.effectiveDoc.GetObject("compaction")
	var ordinarySet bool
	var ordinaryVal int
	if compObj != nil && compObj.Has(field) {
		rawOrd, _ := compObj.Get(field)
		if rawOrd != nil || compObj.Has(field) {
			parsed, ok := smaIsNonNegativeSafeInt(rawOrd)
			if !ok {
				return 0, fmt.Errorf(
					"Invalid compaction.%s setting: %s. Expected a non-negative safe integer.",
					field,
					smaFormatInvalidValue(rawOrd),
				)
			}
			ordinaryVal = parsed
			ordinarySet = true
		}
	}

	var mod *ai.Model
	if len(model) > 0 {
		mod = model[0]
	}
	if mod != nil {
		modelKey := string(mod.Provider) + "/" + mod.Id
		if compObj != nil {
			if moObj, ok := compObj.GetObject("modelOverrides"); ok && moObj != nil && moObj.Has(modelKey) {
				rawEntry, _ := moObj.Get(modelKey)
				entryObj, okObj := rawEntry.(*jsonx.Object)
				if !okObj || entryObj == nil {
					return 0, fmt.Errorf(
						`Invalid compaction.modelOverrides["%s"] setting: %s. Expected an object.`,
						modelKey,
						smaFormatInvalidValue(rawEntry),
					)
				}
				if entryObj.Has(field) {
					rawOv, _ := entryObj.Get(field)
					parsed, ok := smaIsNonNegativeSafeInt(rawOv)
					if !ok {
						return 0, fmt.Errorf(
							`Invalid compaction.modelOverrides["%s"].%s setting: %s. Expected a non-negative safe integer.`,
							modelKey,
							field,
							smaFormatInvalidValue(rawOv),
						)
					}
					return parsed, nil
				}
			}
		}
	}
	if ordinarySet {
		return ordinaryVal, nil
	}
	return defaultVal, nil
}

// GetCompactionReserveTokens returns the validated compaction.reserveTokens value for an optional model.
func (m *SettingsManager) GetCompactionReserveTokens(model ...*ai.Model) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getCompactionTokenSettingLocked("reserveTokens", SettingsDefaults.Compaction.ReserveTokens, model...)
}

// GetCompactionKeepRecentTokens returns the validated compaction.keepRecentTokens value for an optional model.
func (m *SettingsManager) GetCompactionKeepRecentTokens(model ...*ai.Model) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getCompactionTokenSettingLocked("keepRecentTokens", SettingsDefaults.Compaction.KeepRecentTokens, model...)
}

// GetCompactionSettings resolves compaction settings for an optional model through overrides, settings, then defaults.
func (m *SettingsManager) GetCompactionSettings(model ...*ai.Model) (CompactionSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reserveTokens, err := m.getCompactionTokenSettingLocked("reserveTokens", SettingsDefaults.Compaction.ReserveTokens, model...)
	if err != nil {
		return CompactionSettings{}, err
	}
	keepRecentTokens, err := m.getCompactionTokenSettingLocked("keepRecentTokens", SettingsDefaults.Compaction.KeepRecentTokens, model...)
	if err != nil {
		return CompactionSettings{}, err
	}
	enabled := SettingsDefaults.Compaction.Enabled
	if m.settings.Compaction != nil && m.settings.Compaction.Enabled != nil {
		enabled = *m.settings.Compaction.Enabled
	}
	return CompactionSettings{
		Enabled:          enabled,
		ReserveTokens:    reserveTokens,
		KeepRecentTokens: keepRecentTokens,
	}, nil
}

// GetBranchSummarySettings returns resolved branch summary settings.
func (m *SettingsManager) GetBranchSummarySettings() ResolvedBranchSummarySettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	reserveTokens := SettingsDefaults.BranchSummary.ReserveTokens
	skipPrompt := SettingsDefaults.BranchSummary.SkipPrompt
	if m.settings.BranchSummary != nil {
		if m.settings.BranchSummary.ReserveTokens != nil {
			reserveTokens = int(*m.settings.BranchSummary.ReserveTokens)
		}
		if m.settings.BranchSummary.SkipPrompt != nil {
			skipPrompt = *m.settings.BranchSummary.SkipPrompt
		}
	}
	return ResolvedBranchSummarySettings{
		ReserveTokens: reserveTokens,
		SkipPrompt:    skipPrompt,
	}
}

// GetBranchSummarySkipPrompt reports whether the branch summary confirmation prompt should be skipped.
func (m *SettingsManager) GetBranchSummarySkipPrompt() bool {
	return m.GetBranchSummarySettings().SkipPrompt
}

// GetRetryEnabled reports whether automatic agent retry on transient errors is enabled.
func (m *SettingsManager) GetRetryEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Retry != nil && m.settings.Retry.Enabled != nil {
		return *m.settings.Retry.Enabled
	}
	return SettingsDefaults.Retry.Enabled
}

// SetRetryEnabled persists retry.enabled in global settings.
func (m *SettingsManager) SetRetryEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Retry == nil {
		m.globalSettings.Retry = &RetrySettings{}
	}
	m.globalSettings.Retry.Enabled = &enabled
	m.setGlobalNestedFieldLocked("retry", "enabled", enabled)
}

// GetRetrySettings returns resolved agent retry settings.
func (m *SettingsManager) GetRetrySettings() ResolvedRetrySettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := ResolvedRetrySettings{
		Enabled:         SettingsDefaults.Retry.Enabled,
		MaxRetries:      SettingsDefaults.Retry.MaxRetries,
		BaseDelayMs:     SettingsDefaults.Retry.BaseDelayMs,
		MaxAgentDelayMs: SettingsDefaults.Retry.MaxAgentDelayMs,
	}
	if m.settings.Retry != nil {
		if m.settings.Retry.Enabled != nil {
			res.Enabled = *m.settings.Retry.Enabled
		}
		if m.settings.Retry.MaxRetries != nil {
			res.MaxRetries = *m.settings.Retry.MaxRetries
		}
		if m.settings.Retry.BaseDelayMs != nil {
			res.BaseDelayMs = *m.settings.Retry.BaseDelayMs
		}
		if m.settings.Retry.MaxAgentDelayMs != nil {
			res.MaxAgentDelayMs = *m.settings.Retry.MaxAgentDelayMs
		}
	}
	return res
}

// GetProviderRetrySettings returns resolved provider SDK retry/timeout settings.
func (m *SettingsManager) GetProviderRetrySettings() ResolvedProviderRetrySettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := ResolvedProviderRetrySettings{
		MaxRetryDelayMs: SettingsDefaults.Retry.Provider.MaxRetryDelayMs,
	}
	if m.settings.Retry != nil && m.settings.Retry.Provider != nil {
		p := m.settings.Retry.Provider
		if p.TimeoutMs != nil {
			v := *p.TimeoutMs
			res.TimeoutMs = &v
		}
		if p.MaxRetries != nil {
			v := *p.MaxRetries
			res.MaxRetries = &v
		}
		if p.MaxRetryDelayMs != nil {
			res.MaxRetryDelayMs = *p.MaxRetryDelayMs
		}
	}
	return res
}

// GetHttpIdleTimeoutMs returns the validated HTTP idle timeout in milliseconds (0 = disabled).
func (m *SettingsManager) GetHttpIdleTimeoutMs() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, err := smaParseTimeoutSettingDoc(m.effectiveDoc, "httpIdleTimeoutMs")
	if err != nil {
		return 0, err
	}
	if ms != nil {
		return *ms, nil
	}
	return DefaultHttpIdleTimeoutMs, nil
}

// SetHttpIdleTimeoutMs validates and persists httpIdleTimeoutMs in global settings.
func (m *SettingsManager) SetHttpIdleTimeoutMs(timeoutMs float64) error {
	if math.IsNaN(timeoutMs) || math.IsInf(timeoutMs, 0) || timeoutMs < 0 {
		return fmt.Errorf("Invalid httpIdleTimeoutMs setting: %s", smaFormatInvalidValue(timeoutMs))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	floored := math.Floor(timeoutMs)
	ts := TimeoutSettingMs(floored)
	m.globalSettings.HttpIdleTimeoutMs = &ts
	m.setGlobalFieldLocked("httpIdleTimeoutMs", floored)
	return nil
}

// GetCacheWarmingMode returns the cache-warming profile from global settings only.
func (m *SettingsManager) GetCacheWarmingMode() CacheWarmingMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.CacheWarming != nil && slices.Contains(CacheWarmingModes, *m.globalSettings.CacheWarming) {
		return *m.globalSettings.CacheWarming
	}
	return SettingsDefaults.CacheWarming
}

// SetCacheWarmingMode persists cacheWarming in global settings.
func (m *SettingsManager) SetCacheWarmingMode(mode CacheWarmingMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.CacheWarming = &mode
	m.setGlobalFieldLocked("cacheWarming", string(mode))
}

// GetWebSocketConnectTimeoutMs returns the validated websocketConnectTimeoutMs, or nil when unset.
func (m *SettingsManager) GetWebSocketConnectTimeoutMs() (*int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return smaParseTimeoutSettingDoc(m.effectiveDoc, "websocketConnectTimeoutMs")
}

// GetHideThinkingBlock reports whether thinking blocks are hidden by default.
func (m *SettingsManager) GetHideThinkingBlock() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.HideThinkingBlock != nil {
		return *m.settings.HideThinkingBlock
	}
	return SettingsDefaults.HideThinkingBlock
}

// SetHideThinkingBlock persists hideThinkingBlock in global settings.
func (m *SettingsManager) SetHideThinkingBlock(hide bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.HideThinkingBlock = &hide
	m.setGlobalFieldLocked("hideThinkingBlock", hide)
}

// GetShowCacheMissNotices reports whether cache cost and recovery notices are shown.
func (m *SettingsManager) GetShowCacheMissNotices() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.ShowCacheMissNotices != nil {
		return *m.settings.ShowCacheMissNotices
	}
	return SettingsDefaults.ShowCacheMissNotices
}

// SetShowCacheMissNotices persists showCacheMissNotices in global settings.
func (m *SettingsManager) SetShowCacheMissNotices(show bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.ShowCacheMissNotices = &show
	m.setGlobalFieldLocked("showCacheMissNotices", show)
}

// GetExternalEditorCommand resolves externalEditor > VISUAL > EDITOR > platform default ("notepad" | "nano").
func (m *SettingsManager) GetExternalEditorCommand() string {
	m.mu.Lock()
	configured := m.settings.ExternalEditor
	m.mu.Unlock()
	if configured != nil && strings.TrimSpace(*configured) != "" {
		return *configured
	}
	if visual := os.Getenv("VISUAL"); visual != "" {
		return visual
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	plat := smaRuntimePlatform()
	if plat == "win32" || plat == "windows" {
		return "notepad"
	}
	return "nano"
}

// GetShellPath returns the normalized custom shell path, if configured.
func (m *SettingsManager) GetShellPath() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.ShellPath == nil {
		return nil
	}
	sp := *m.settings.ShellPath
	if sp != "" {
		if norm, err := utils.NormalizePath(sp, nil); err == nil {
			sp = norm
		}
	}
	return &sp
}

// SetShellPath sets or clears shellPath in global settings.
func (m *SettingsManager) SetShellPath(path *string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path == nil {
		m.globalSettings.ShellPath = nil
		m.setGlobalFieldLocked("shellPath", nil)
		return
	}
	p := *path
	m.globalSettings.ShellPath = &p
	m.setGlobalFieldLocked("shellPath", p)
}

// GetQuietStartup returns the startup output quiet setting.
func (m *SettingsManager) GetQuietStartup() QuietStartup {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.QuietStartup != nil {
		if m.settings.QuietStartup.Header {
			return QuietStartupHeader()
		}
		if m.settings.QuietStartup.Silent {
			return QuietStartupBool(true)
		}
	}
	return SettingsDefaults.QuietStartup
}

// SetQuietStartup persists quietStartup in global settings.
func (m *SettingsManager) SetQuietStartup(quiet QuietStartup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := quiet
	m.globalSettings.QuietStartup = &q
	m.setGlobalFieldLocked("quietStartup", q)
}

// GetDefaultProjectTrust returns the global defaultProjectTrust setting.
func (m *SettingsManager) GetDefaultProjectTrust() DefaultProjectTrust {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.DefaultProjectTrust != nil {
		v := *m.globalSettings.DefaultProjectTrust
		if v == DefaultProjectTrustAlways || v == DefaultProjectTrustNever {
			return v
		}
	}
	return SettingsDefaults.DefaultProjectTrust
}

// SetDefaultProjectTrust persists defaultProjectTrust in global settings.
func (m *SettingsManager) SetDefaultProjectTrust(trust DefaultProjectTrust) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.DefaultProjectTrust = &trust
	m.setGlobalFieldLocked("defaultProjectTrust", string(trust))
}

// GetShellCommandPrefix returns the prefix prepended to bash commands, if set.
func (m *SettingsManager) GetShellCommandPrefix() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.ShellCommandPrefix == nil {
		return nil
	}
	v := *m.settings.ShellCommandPrefix
	return &v
}

// SetShellCommandPrefix sets or clears shellCommandPrefix in global settings.
func (m *SettingsManager) SetShellCommandPrefix(prefix *string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prefix == nil {
		m.globalSettings.ShellCommandPrefix = nil
		m.setGlobalFieldLocked("shellCommandPrefix", nil)
		return
	}
	p := *prefix
	m.globalSettings.ShellCommandPrefix = &p
	m.setGlobalFieldLocked("shellCommandPrefix", p)
}

// GetNpmCommand returns a copy of the configured npm argv command, or nil.
func (m *SettingsManager) GetNpmCommand() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.NpmCommand == nil {
		return nil
	}
	return slices.Clone(m.settings.NpmCommand)
}

// SetNpmCommand sets or clears npmCommand in global settings.
func (m *SettingsManager) SetNpmCommand(command []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if command == nil {
		m.globalSettings.NpmCommand = nil
		m.setGlobalFieldLocked("npmCommand", nil)
		return
	}
	cp := slices.Clone(command)
	m.globalSettings.NpmCommand = cp
	m.setGlobalFieldLocked("npmCommand", cp)
}

// GetCollapseChangelog reports whether changelog updates are collapsed by default.
func (m *SettingsManager) GetCollapseChangelog() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.CollapseChangelog != nil {
		return *m.settings.CollapseChangelog
	}
	return SettingsDefaults.CollapseChangelog
}

// SetCollapseChangelog persists collapseChangelog in global settings.
func (m *SettingsManager) SetCollapseChangelog(collapse bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.CollapseChangelog = &collapse
	m.setGlobalFieldLocked("collapseChangelog", collapse)
}

// GetEnableInstallTelemetry reports whether anonymous install telemetry is enabled.
func (m *SettingsManager) GetEnableInstallTelemetry() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.EnableInstallTelemetry != nil {
		return *m.settings.EnableInstallTelemetry
	}
	return SettingsDefaults.EnableInstallTelemetry
}

// SetEnableInstallTelemetry persists enableInstallTelemetry in global settings.
func (m *SettingsManager) SetEnableInstallTelemetry(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.EnableInstallTelemetry = &enabled
	m.setGlobalFieldLocked("enableInstallTelemetry", enabled)
}

// GetEnableAnalytics reports whether analytics opt-in is enabled.
func (m *SettingsManager) GetEnableAnalytics() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.EnableAnalytics != nil {
		return *m.settings.EnableAnalytics
	}
	return SettingsDefaults.EnableAnalytics
}

// GetTrackingId returns the analytics tracking identifier, if generated.
func (m *SettingsManager) GetTrackingId() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.TrackingId == nil {
		return nil
	}
	v := *m.settings.TrackingId
	return &v
}

// SetEnableAnalytics updates enableAnalytics and generates a UUID trackingId on first opt-in.
func (m *SettingsManager) SetEnableAnalytics(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.EnableAnalytics = &enabled
	m.globalDoc.Set("enableAnalytics", enabled)
	m.markModifiedLocked("enableAnalytics")
	if enabled && (m.globalSettings.TrackingId == nil || *m.globalSettings.TrackingId == "") {
		tid := smaRandomUUIDv4()
		m.globalSettings.TrackingId = &tid
		m.globalDoc.Set("trackingId", tid)
		m.markModifiedLocked("trackingId")
	}
	m.saveGlobalLocked()
}

// GetOrCreateDeviceId returns the global installation UUID, generating and persisting one if absent.
func (m *SettingsManager) GetOrCreateDeviceId() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.DeviceId == nil || *m.globalSettings.DeviceId == "" {
		id := smaRandomUUIDv4()
		m.globalSettings.DeviceId = &id
		m.setGlobalFieldLocked("deviceId", id)
	}
	return *m.globalSettings.DeviceId
}

// GetPackages returns a copy of configured package sources.
func (m *SettingsManager) GetPackages() []PackageSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Packages == nil {
		return []PackageSource{}
	}
	return slices.Clone(m.settings.Packages)
}

// SetPackages persists packages in global settings.
func (m *SettingsManager) SetPackages(packages []PackageSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := slices.Clone(packages)
	if cp == nil {
		cp = []PackageSource{}
	}
	m.globalSettings.Packages = cp
	m.setGlobalFieldLocked("packages", cp)
}

// SetProjectPackages persists packages in project settings.
func (m *SettingsManager) SetProjectPackages(packages []PackageSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.projectTrusted {
		return ErrProjectNotTrusted
	}
	cp := slices.Clone(packages)
	if cp == nil {
		cp = []PackageSource{}
	}
	m.projectSettings.Packages = cp
	if encoded, ok := smaEncodeFieldValue(cp); ok {
		m.projectDoc.Set("packages", encoded)
	}
	m.markProjectModifiedLocked("packages")
	return m.saveProjectLocked()
}

// GetExtensionPaths returns a copy of configured local extension paths.
func (m *SettingsManager) GetExtensionPaths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Extensions == nil {
		return []string{}
	}
	return slices.Clone(m.settings.Extensions)
}

// SetExtensionPaths persists extensions in global settings.
func (m *SettingsManager) SetExtensionPaths(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.globalSettings.Extensions = cp
	m.setGlobalFieldLocked("extensions", cp)
}

// SetProjectExtensionPaths persists extensions in project settings.
func (m *SettingsManager) SetProjectExtensionPaths(paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.projectTrusted {
		return ErrProjectNotTrusted
	}
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.projectSettings.Extensions = cp
	if encoded, ok := smaEncodeFieldValue(cp); ok {
		m.projectDoc.Set("extensions", encoded)
	}
	m.markProjectModifiedLocked("extensions")
	return m.saveProjectLocked()
}

// GetSkillPaths returns a copy of configured local skill paths.
func (m *SettingsManager) GetSkillPaths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Skills == nil {
		return []string{}
	}
	return slices.Clone(m.settings.Skills)
}

// SetSkillPaths persists skills in global settings.
func (m *SettingsManager) SetSkillPaths(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.globalSettings.Skills = cp
	m.setGlobalFieldLocked("skills", cp)
}

// SetProjectSkillPaths persists skills in project settings.
func (m *SettingsManager) SetProjectSkillPaths(paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.projectTrusted {
		return ErrProjectNotTrusted
	}
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.projectSettings.Skills = cp
	if encoded, ok := smaEncodeFieldValue(cp); ok {
		m.projectDoc.Set("skills", encoded)
	}
	m.markProjectModifiedLocked("skills")
	return m.saveProjectLocked()
}

// GetPromptTemplatePaths returns a copy of configured prompt template paths.
func (m *SettingsManager) GetPromptTemplatePaths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Prompts == nil {
		return []string{}
	}
	return slices.Clone(m.settings.Prompts)
}

// SetPromptTemplatePaths persists prompts in global settings.
func (m *SettingsManager) SetPromptTemplatePaths(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.globalSettings.Prompts = cp
	m.setGlobalFieldLocked("prompts", cp)
}

// SetProjectPromptTemplatePaths persists prompts in project settings.
func (m *SettingsManager) SetProjectPromptTemplatePaths(paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.projectTrusted {
		return ErrProjectNotTrusted
	}
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.projectSettings.Prompts = cp
	if encoded, ok := smaEncodeFieldValue(cp); ok {
		m.projectDoc.Set("prompts", encoded)
	}
	m.markProjectModifiedLocked("prompts")
	return m.saveProjectLocked()
}

// GetThemePaths returns a copy of configured local theme paths.
func (m *SettingsManager) GetThemePaths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Themes == nil {
		return []string{}
	}
	return slices.Clone(m.settings.Themes)
}

// SetThemePaths persists themes in global settings.
func (m *SettingsManager) SetThemePaths(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.globalSettings.Themes = cp
	m.setGlobalFieldLocked("themes", cp)
}

// SetProjectThemePaths persists themes in project settings.
func (m *SettingsManager) SetProjectThemePaths(paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.projectTrusted {
		return ErrProjectNotTrusted
	}
	cp := slices.Clone(paths)
	if cp == nil {
		cp = []string{}
	}
	m.projectSettings.Themes = cp
	if encoded, ok := smaEncodeFieldValue(cp); ok {
		m.projectDoc.Set("themes", encoded)
	}
	m.markProjectModifiedLocked("themes")
	return m.saveProjectLocked()
}

// GetEnableSkillCommands reports whether skills are registered as /skill:name commands.
func (m *SettingsManager) GetEnableSkillCommands() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.EnableSkillCommands != nil {
		return *m.settings.EnableSkillCommands
	}
	return SettingsDefaults.EnableSkillCommands
}

// SetEnableSkillCommands persists enableSkillCommands in global settings.
func (m *SettingsManager) SetEnableSkillCommands(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.EnableSkillCommands = &enabled
	m.setGlobalFieldLocked("enableSkillCommands", enabled)
}

// GetThinkingBudgets returns configured custom thinking token budgets, if set.
func (m *SettingsManager) GetThinkingBudgets() *ThinkingBudgetsSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.ThinkingBudgets == nil {
		return nil
	}
	cp := *m.settings.ThinkingBudgets
	return &cp
}

// GetTerminalCapabilityOverrides maps explicit terminal settings while omitting "auto" entries.
func (m *SettingsManager) GetTerminalCapabilityOverrides() TerminalCapabilityOverrides {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out TerminalCapabilityOverrides
	t := m.settings.Terminal
	if t == nil {
		return out
	}
	if t.Images != nil {
		if t.Images.Disabled {
			var disabled tui.ImageProtocol
			out.Images = &disabled
		} else if t.Images.Protocol == string(tui.ImageProtocolKitty) || t.Images.Protocol == string(tui.ImageProtocolIterm2) {
			proto := tui.ImageProtocol(t.Images.Protocol)
			out.Images = &proto
		}
	}
	if t.TrueColor != nil && !t.TrueColor.Auto {
		v := t.TrueColor.Enabled
		out.TrueColor = &v
	}
	if t.Hyperlinks != nil && !t.Hyperlinks.Auto {
		v := t.Hyperlinks.Enabled
		out.Hyperlinks = &v
	}
	return out
}

// GetShowImages reports whether inline terminal images are shown when supported.
func (m *SettingsManager) GetShowImages() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Terminal != nil && m.settings.Terminal.ShowImages != nil {
		return *m.settings.Terminal.ShowImages
	}
	return SettingsDefaults.Terminal.ShowImages
}

// SetShowImages persists terminal.showImages in global settings.
func (m *SettingsManager) SetShowImages(show bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Terminal == nil {
		m.globalSettings.Terminal = &TerminalSettings{}
	}
	m.globalSettings.Terminal.ShowImages = &show
	m.setGlobalNestedFieldLocked("terminal", "showImages", show)
}

// GetImageWidthCells returns the preferred inline image width in terminal cells (>= 1).
func (m *SettingsManager) GetImageWidthCells() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Terminal != nil && m.settings.Terminal.ImageWidthCells != nil {
		return max(1, *m.settings.Terminal.ImageWidthCells)
	}
	return SettingsDefaults.Terminal.ImageWidthCells
}

// SetImageWidthCells clamps width to >= 1 and persists terminal.imageWidthCells.
func (m *SettingsManager) SetImageWidthCells(width float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clamped := max(1, int(math.Floor(width)))
	if m.globalSettings.Terminal == nil {
		m.globalSettings.Terminal = &TerminalSettings{}
	}
	m.globalSettings.Terminal.ImageWidthCells = &clamped
	m.setGlobalNestedFieldLocked("terminal", "imageWidthCells", clamped)
}

// GetClearOnShrink reports whether empty rows are cleared when terminal content shrinks.
func (m *SettingsManager) GetClearOnShrink() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Terminal != nil && m.settings.Terminal.ClearOnShrink != nil {
		return *m.settings.Terminal.ClearOnShrink
	}
	if os.Getenv("PI_CLEAR_ON_SHRINK") == "1" {
		return true
	}
	return SettingsDefaults.Terminal.ClearOnShrink
}

// SetClearOnShrink persists terminal.clearOnShrink in global settings.
func (m *SettingsManager) SetClearOnShrink(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Terminal == nil {
		m.globalSettings.Terminal = &TerminalSettings{}
	}
	m.globalSettings.Terminal.ClearOnShrink = &enabled
	m.setGlobalNestedFieldLocked("terminal", "clearOnShrink", enabled)
}

// GetShowTerminalProgress reports whether OSC 9;4 terminal progress indicators are emitted.
func (m *SettingsManager) GetShowTerminalProgress() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Terminal != nil && m.settings.Terminal.ShowTerminalProgress != nil {
		return *m.settings.Terminal.ShowTerminalProgress
	}
	return SettingsDefaults.Terminal.ShowTerminalProgress
}

// SetShowTerminalProgress persists terminal.showTerminalProgress in global settings.
func (m *SettingsManager) SetShowTerminalProgress(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Terminal == nil {
		m.globalSettings.Terminal = &TerminalSettings{}
	}
	m.globalSettings.Terminal.ShowTerminalProgress = &enabled
	m.setGlobalNestedFieldLocked("terminal", "showTerminalProgress", enabled)
}

// GetTuiMode returns the TUI screen mode ("regular" | "fullscreen").
func (m *SettingsManager) GetTuiMode() TuiMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.TuiMode != nil && *m.settings.TuiMode == tui.TuiModeRegular {
		return tui.TuiModeRegular
	}
	return SettingsDefaults.TuiMode
}

// SetTuiMode persists tuiMode in global settings.
func (m *SettingsManager) SetTuiMode(mode TuiMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.TuiMode = &mode
	m.setGlobalFieldLocked("tuiMode", string(mode))
}

// GetFullscreenExitOutput returns the fullscreen exit output preference.
func (m *SettingsManager) GetFullscreenExitOutput() FullscreenExitOutput {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.FullscreenExitOutput != nil && *m.settings.FullscreenExitOutput == FullscreenExitOutputResumeHint {
		return FullscreenExitOutputResumeHint
	}
	return SettingsDefaults.FullscreenExitOutput
}

// SetFullscreenExitOutput persists fullscreenExitOutput in global settings.
func (m *SettingsManager) SetFullscreenExitOutput(output FullscreenExitOutput) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.FullscreenExitOutput = &output
	m.setGlobalFieldLocked("fullscreenExitOutput", string(output))
}

// GetFullscreenScrollbar returns the fullscreen scroll bar visibility setting.
func (m *SettingsManager) GetFullscreenScrollbar() tui.ScrollViewScrollbar {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.FullscreenScrollbar != nil {
		mode := *m.settings.FullscreenScrollbar
		if mode == tui.ScrollViewScrollbarAlways || mode == tui.ScrollViewScrollbarHidden {
			return mode
		}
	}
	return SettingsDefaults.FullscreenScrollbar
}

// SetFullscreenScrollbar persists fullscreenScrollbar in global settings.
func (m *SettingsManager) SetFullscreenScrollbar(mode tui.ScrollViewScrollbar) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.FullscreenScrollbar = &mode
	m.setGlobalFieldLocked("fullscreenScrollbar", string(mode))
}

// GetFullscreenCopyOnSelect reports whether selecting text copies automatically in fullscreen mode.
func (m *SettingsManager) GetFullscreenCopyOnSelect() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.FullscreenCopyOnSelect != nil {
		return *m.settings.FullscreenCopyOnSelect
	}
	return SettingsDefaults.FullscreenCopyOnSelect
}

// SetFullscreenCopyOnSelect persists fullscreenCopyOnSelect in global settings.
func (m *SettingsManager) SetFullscreenCopyOnSelect(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.FullscreenCopyOnSelect = &enabled
	m.setGlobalFieldLocked("fullscreenCopyOnSelect", enabled)
}

// GetFullscreenWheelScrollLines returns the clamped fullscreen mouse wheel line count or "auto".
func (m *SettingsManager) GetFullscreenWheelScrollLines() tui.WheelScrollLines {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.FullscreenWheelScrollLines != nil && !m.settings.FullscreenWheelScrollLines.Auto {
		lines := m.settings.FullscreenWheelScrollLines.Lines
		if !math.IsNaN(lines) && !math.IsInf(lines, 0) {
			clamped := max(1.0, min(100.0, math.Floor(lines)))
			return tui.WheelScrollLines{Lines: clamped}
		}
	}
	return SettingsDefaults.FullscreenWheelScrollLines
}

// SetFullscreenWheelScrollLines clamps numeric values to [1, 100] and persists fullscreenWheelScrollLines.
func (m *SettingsManager) SetFullscreenWheelScrollLines(lines tui.WheelScrollLines) {
	m.mu.Lock()
	defer m.mu.Unlock()
	val := lines
	if !val.Auto {
		val = tui.WheelScrollLines{Lines: max(1.0, min(100.0, math.Floor(val.Lines)))}
	}
	m.globalSettings.FullscreenWheelScrollLines = &val
	m.setGlobalFieldLocked("fullscreenWheelScrollLines", val)
}

// GetImageAutoResize reports whether images are automatically resized before sending to models.
func (m *SettingsManager) GetImageAutoResize() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Images != nil && m.settings.Images.AutoResize != nil {
		return *m.settings.Images.AutoResize
	}
	return SettingsDefaults.Images.AutoResize
}

// SetImageAutoResize persists images.autoResize in global settings.
func (m *SettingsManager) SetImageAutoResize(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Images == nil {
		m.globalSettings.Images = &ImageSettings{}
	}
	m.globalSettings.Images.AutoResize = &enabled
	m.setGlobalNestedFieldLocked("images", "autoResize", enabled)
}

// GetBlockImages reports whether all images are blocked from being sent to LLM providers.
func (m *SettingsManager) GetBlockImages() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Images != nil && m.settings.Images.BlockImages != nil {
		return *m.settings.Images.BlockImages
	}
	return SettingsDefaults.Images.BlockImages
}

// SetBlockImages persists images.blockImages in global settings.
func (m *SettingsManager) SetBlockImages(blocked bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Images == nil {
		m.globalSettings.Images = &ImageSettings{}
	}
	m.globalSettings.Images.BlockImages = &blocked
	m.setGlobalNestedFieldLocked("images", "blockImages", blocked)
}

// GetEnabledModels returns configured model cycling patterns, or nil if unset.
func (m *SettingsManager) GetEnabledModels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.EnabledModels == nil {
		return nil
	}
	return slices.Clone(m.settings.EnabledModels)
}

// SetEnabledModels sets or clears enabledModels in global settings.
func (m *SettingsManager) SetEnabledModels(patterns []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if patterns == nil {
		m.globalSettings.EnabledModels = nil
		m.setGlobalFieldLocked("enabledModels", nil)
		return
	}
	cp := slices.Clone(patterns)
	m.globalSettings.EnabledModels = cp
	m.setGlobalFieldLocked("enabledModels", cp)
}

// GetDefaultTools resolves defaultTools selection across layers, returning nil if no layer sets defaultTools.
func (m *SettingsManager) GetDefaultTools() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.effectiveDoc.Has("defaultTools") {
		return nil
	}
	arr, ok := m.effectiveDoc.GetArray("defaultTools")
	if !ok {
		return []string{}
	}
	tools := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			tools = append(tools, s)
		}
	}
	return smaResolveDefaultTools(tools)
}

// GetDoubleEscapeAction returns the double-escape action when the editor is empty.
func (m *SettingsManager) GetDoubleEscapeAction() DoubleEscapeAction {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.DoubleEscapeAction != nil && *m.settings.DoubleEscapeAction != "" {
		return *m.settings.DoubleEscapeAction
	}
	return SettingsDefaults.DoubleEscapeAction
}

// SetDoubleEscapeAction persists doubleEscapeAction in global settings.
func (m *SettingsManager) SetDoubleEscapeAction(action DoubleEscapeAction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.DoubleEscapeAction = &action
	m.setGlobalFieldLocked("doubleEscapeAction", string(action))
}

// GetTreeFilterMode returns the default /tree filter mode.
func (m *SettingsManager) GetTreeFilterMode() TreeFilterMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.TreeFilterMode != nil {
		switch *m.settings.TreeFilterMode {
		case TreeFilterModeDefault, TreeFilterModeNoTools, TreeFilterModeUserOnly, TreeFilterModeLabeledOnly, TreeFilterModeAll:
			return *m.settings.TreeFilterMode
		}
	}
	return SettingsDefaults.TreeFilterMode
}

// SetTreeFilterMode persists treeFilterMode in global settings.
func (m *SettingsManager) SetTreeFilterMode(mode TreeFilterMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.TreeFilterMode = &mode
	m.setGlobalFieldLocked("treeFilterMode", string(mode))
}

// GetShowHardwareCursor reports whether the terminal hardware cursor is kept visible for IME.
func (m *SettingsManager) GetShowHardwareCursor() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.ShowHardwareCursor != nil {
		return *m.settings.ShowHardwareCursor
	}
	return os.Getenv("PI_HARDWARE_CURSOR") == "1"
}

// SetShowHardwareCursor persists showHardwareCursor in global settings.
func (m *SettingsManager) SetShowHardwareCursor(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.ShowHardwareCursor = &enabled
	m.setGlobalFieldLocked("showHardwareCursor", enabled)
}

// GetEditorPaddingX returns the horizontal padding for the input editor.
func (m *SettingsManager) GetEditorPaddingX() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.EditorPaddingX != nil {
		return *m.settings.EditorPaddingX
	}
	return SettingsDefaults.EditorPaddingX
}

// SetEditorPaddingX clamps padding to [0, 3] and persists editorPaddingX in global settings.
func (m *SettingsManager) SetEditorPaddingX(padding float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clamped := max(0, min(3, int(math.Floor(padding))))
	m.globalSettings.EditorPaddingX = &clamped
	m.setGlobalFieldLocked("editorPaddingX", clamped)
}

// GetOutputPad returns the transcript horizontal padding (0 or 1).
func (m *SettingsManager) GetOutputPad() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.OutputPad != nil && *m.settings.OutputPad == 0 {
		return 0
	}
	return SettingsDefaults.OutputPad
}

// SetOutputPad persists outputPad (0 or 1) in global settings.
func (m *SettingsManager) SetOutputPad(padding int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalSettings.OutputPad = &padding
	m.setGlobalFieldLocked("outputPad", padding)
}

// GetAutocompleteMaxVisible returns the max visible dropdown items in autocomplete.
func (m *SettingsManager) GetAutocompleteMaxVisible() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.AutocompleteMaxVisible != nil {
		return *m.settings.AutocompleteMaxVisible
	}
	return SettingsDefaults.AutocompleteMaxVisible
}

// SetAutocompleteMaxVisible clamps maxVisible to [3, 20] and persists autocompleteMaxVisible.
func (m *SettingsManager) SetAutocompleteMaxVisible(maxVisible float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clamped := max(3, min(20, int(math.Floor(maxVisible))))
	m.globalSettings.AutocompleteMaxVisible = &clamped
	m.setGlobalFieldLocked("autocompleteMaxVisible", clamped)
}

// GetCodeBlockIndent returns the markdown code block indentation string.
func (m *SettingsManager) GetCodeBlockIndent() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Markdown != nil && m.settings.Markdown.CodeBlockIndent != nil {
		return *m.settings.Markdown.CodeBlockIndent
	}
	return SettingsDefaults.Markdown.CodeBlockIndent
}

// GetMermaidRenderingMode returns the configured markdown mermaid diagram rendering mode.
func (m *SettingsManager) GetMermaidRenderingMode() MermaidRenderingMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Markdown != nil && m.settings.Markdown.Mermaid != nil {
		mode := *m.settings.Markdown.Mermaid
		if mode == MermaidRenderingOff || mode == MermaidRenderingFinal {
			return mode
		}
	}
	return SettingsDefaults.Markdown.Mermaid
}

// SetMermaidRenderingMode persists markdown.mermaid in global settings.
func (m *SettingsManager) SetMermaidRenderingMode(mode MermaidRenderingMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.globalSettings.Markdown == nil {
		m.globalSettings.Markdown = &MarkdownSettings{}
	}
	m.globalSettings.Markdown.Mermaid = &mode
	m.setGlobalNestedFieldLocked("markdown", "mermaid", string(mode))
}

// GetWarnings returns a copy of warning suppression flags.
func (m *SettingsManager) GetWarnings() WarningSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Warnings == nil {
		return WarningSettings{}
	}
	cp := *m.settings.Warnings
	return cp
}

// SetWarnings persists warnings in global settings.
func (m *SettingsManager) SetWarnings(warnings WarningSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := warnings
	m.globalSettings.Warnings = &cp
	m.setGlobalFieldLocked("warnings", cp)
}
