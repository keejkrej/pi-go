package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// LoadExtensionsOptions configures explicit and discovered extension loading.
type LoadExtensionsOptions struct {
	Runtime                   *ExtensionRuntime
	EventBus                  EventBus
	DisabledBuiltinExtensions []string
	Factories                 []InlineExtension
	Cwd                       string
	AgentDir                  string
	UseCache                  bool
	PathMetadata              map[string]utils.PathMetadata
}

var (
	elBuiltinMu   sync.RWMutex
	elBuiltinExts []InlineExtension

	elCacheMu         sync.Mutex
	elCacheGeneration atomic.Uint64
	elBundleCache     = make(map[string]elCachedBundle)
)

type elCachedBundle struct {
	generation uint64
	modTimeNs  int64
	size       int64
	code       string
	warnings   []string
}

// SetDefaultBuiltinExtensions registers the built-in Go extension factories discovered automatically
// unless disabled. Safe for concurrent use (intended to be called by codingagent/extensions init).
func SetDefaultBuiltinExtensions(exts []InlineExtension) {
	elBuiltinMu.Lock()
	elBuiltinExts = slices.Clone(exts)
	elBuiltinMu.Unlock()
}

// GetDefaultBuiltinExtensions returns a snapshot of currently registered default built-in extensions.
func GetDefaultBuiltinExtensions() []InlineExtension {
	elBuiltinMu.RLock()
	out := slices.Clone(elBuiltinExts)
	elBuiltinMu.RUnlock()
	return out
}

// ClearExtensionCache invalidates cached bundled JS outputs used by LoadExtensionsCached.
func ClearExtensionCache() {
	elCacheGeneration.Add(1)
	elCacheMu.Lock()
	clear(elBundleCache)
	elCacheMu.Unlock()
}

// ShutdownExtensionRuntime runs all registered runtime cleanup callbacks (such as closing QuickJS VMs)
// for callers that loaded extensions without creating an ExtensionRunner.
func ShutdownExtensionRuntime(rt *ExtensionRuntime) {
	erRunRuntimeCleanups(rt)
}

// CreateExtensionRuntime creates an ExtensionRuntime whose host-bound actions are no-ops until bound.
func CreateExtensionRuntime() *ExtensionRuntime {
	rt := &ExtensionRuntime{
		FlagValues:   make(map[string]any),
		McpServers:   NewMcpServerRegistry(),
		AssertActive: func() {},
		Invalidate:   func(...string) {},
		TrackEventBusSubscription: func(unsub func()) func() {
			if unsub == nil {
				return func() {}
			}
			return unsub
		},
		RegisterProvider:       func(string, ProviderConfig, ...string) {},
		RegisterNativeProvider: func(*ai.Provider, ...string) {},
		UnregisterProvider:     func(string, ...string) {},
		RegisterVirtualModel:   func(VirtualModelDefinition, ...string) {},
		UnregisterVirtualModel: func(string, string) {},
		SendMessage:            func(CustomMessageInput, ...*SendMessageOptions) {},
		SendUserMessage:        func(any, ...*SendUserMessageOptions) {},
		AppendEntry:            func(string, any) {},
		SetSessionName:         func(string) {},
		GetSessionName:         func() *string { return nil },
		SetLabel:               func(string, *string) {},
		GetActiveTools:         func() []string { return nil },
		GetAllTools:            func() []ToolInfo { return nil },
		GetSettings:            func() Settings { return Settings{} },
		SetActiveTools:         func([]string) {},
		RefreshTools:           func() {},
		GetCommands:            func() []SlashCommandInfo { return nil },
		SetModel:               func(context.Context, *ai.Model) (bool, error) { return false, nil },
		GetThinkingLevel:       func() agent.ThinkingLevel { return agent.ThinkingLevelOff },
		SetThinkingLevel:       func(agent.ThinkingLevel) {},
	}
	rt.CreateContext = func() ExtensionContext {
		return NewExtensionRunner(nil, rt, "", nil, nil).CreateContext()
	}
	return rt
}

// LoadExtensionFromFactory loads a Go InlineExtension into a fresh ExtensionRuntime.
func LoadExtensionFromFactory(ctx context.Context, inline InlineExtension, cwd string, eventBus ...EventBus) (*Extension, error) {
	return LoadExtensionFromFactoryWithRuntime(ctx, inline, cwd, nil, eventBus...)
}

// LoadExtensionFromFactoryWithRuntime loads a Go InlineExtension using runtime (creating one if nil).
func LoadExtensionFromFactoryWithRuntime(
	ctx context.Context,
	inline InlineExtension,
	cwd string,
	runtime *ExtensionRuntime,
	eventBus ...EventBus,
) (*Extension, error) {
	if inline.Factory == nil {
		return nil, fmt.Errorf("inline extension %q has nil factory", inline.Name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime == nil {
		runtime = CreateExtensionRuntime()
	}
	var bus EventBus
	if len(eventBus) > 0 {
		bus = eventBus[0]
	}
	if bus == nil {
		bus = CreateEventBus()
	}
	extPath := inline.Name
	if inline.Builtin && !strings.HasPrefix(extPath, "builtin:") {
		extPath = "builtin:" + extPath
	}
	if extPath == "" {
		extPath = "inline:extension"
	}
	ext := elNewEmptyExtension(extPath, extPath, inline.Hidden, inline.Replaceable, utils.CreateSyntheticSourceInfo(extPath, utils.SyntheticSourceOptions{}))
	api, state := elCreateExtensionAPI(ext, runtime, cwd, bus)
	if err := elRunGoFactorySafely(inline.Factory, api); err != nil {
		elAbortExtensionAPI(state)
		return nil, err
	}
	elFinishExtensionAPI(state)
	return ext, nil
}

// LoadExtensions loads the given extension paths without using the bundle cache.
func LoadExtensions(ctx context.Context, paths []string, cwd string, eventBus ...EventBus) LoadExtensionsResult {
	var bus EventBus
	if len(eventBus) > 0 {
		bus = eventBus[0]
	}
	return LoadExtensionsWithOptions(ctx, paths, &LoadExtensionsOptions{
		Cwd:      cwd,
		EventBus: bus,
	})
}

// LoadExtensionsCached loads the given extension paths, caching bundled JS per canonical path & mtime.
func LoadExtensionsCached(ctx context.Context, paths []string, cwd string, eventBus ...EventBus) LoadExtensionsResult {
	var bus EventBus
	if len(eventBus) > 0 {
		bus = eventBus[0]
	}
	return elLoadExtensions(ctx, paths, &LoadExtensionsOptions{
		Cwd:      cwd,
		EventBus: bus,
	}, true)
}

// LoadExtensionsWithOptions loads explicit paths and/or inline factories without caching bundles.
func LoadExtensionsWithOptions(ctx context.Context, paths []string, opts *LoadExtensionsOptions) LoadExtensionsResult {
	return elLoadExtensions(ctx, paths, opts, false)
}

// DiscoverAndLoadExtensions discovers configuredPaths, enabled default built-in extensions,
// <agentDir>/extensions, and <cwd>/.pi/extensions, then loads all unique entries.
func DiscoverAndLoadExtensions(
	ctx context.Context,
	configuredPaths []string,
	cwd string,
	agentDir string,
	eventBus ...EventBus,
) LoadExtensionsResult {
	var bus EventBus
	if len(eventBus) > 0 {
		bus = eventBus[0]
	}
	return DiscoverAndLoadExtensionsWithOptions(ctx, configuredPaths, &LoadExtensionsOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		EventBus: bus,
	})
}

// DiscoverAndLoadExtensionsWithOptions discovers and loads extensions with full control over
// runtime, event bus, disabled built-in names, and custom factories.
func DiscoverAndLoadExtensionsWithOptions(
	ctx context.Context,
	configuredPaths []string,
	opts *LoadExtensionsOptions,
) LoadExtensionsResult {
	if ctx == nil {
		ctx = context.Background()
	}
	var cwd, agentDir string
	var bus EventBus
	var runtime *ExtensionRuntime
	var disabledList []string
	var customFactories []InlineExtension
	if opts != nil {
		cwd = opts.Cwd
		agentDir = opts.AgentDir
		bus = opts.EventBus
		runtime = opts.Runtime
		disabledList = opts.DisabledBuiltinExtensions
		customFactories = opts.Factories
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}
	if bus == nil {
		bus = CreateEventBus()
	}
	if runtime == nil {
		runtime = CreateExtensionRuntime()
	}

	disabledSet := make(map[string]struct{}, len(disabledList))
	for _, name := range disabledList {
		n := strings.TrimPrefix(strings.TrimSpace(name), "builtin:")
		if n != "" {
			disabledSet[n] = struct{}{}
		}
	}

	builtinsByName := make(map[string]InlineExtension)
	var builtinOrder []string
	for _, def := range GetDefaultBuiltinExtensions() {
		key := strings.TrimPrefix(def.Name, "builtin:")
		if key == "" {
			continue
		}
		if _, exists := builtinsByName[key]; !exists {
			builtinOrder = append(builtinOrder, key)
		}
		builtinsByName[key] = def
	}
	for _, def := range customFactories {
		key := strings.TrimPrefix(def.Name, "builtin:")
		if key == "" {
			continue
		}
		if _, exists := builtinsByName[key]; !exists {
			builtinOrder = append(builtinOrder, key)
		}
		builtinsByName[key] = def
	}

	type discoveryTarget struct {
		isInline bool
		inline   InlineExtension
		path     string
		source   string // original configured/discovered identifier
	}

	var targets []discoveryTarget
	var preErrors []ExtensionLoadError
	seenPaths := make(map[string]struct{})
	seenBuiltins := make(map[string]struct{})

	addFileTarget := func(p string) {
		canon := elCanonicalPath(p)
		if _, dup := seenPaths[canon]; dup {
			return
		}
		seenPaths[canon] = struct{}{}
		targets = append(targets, discoveryTarget{path: canon, source: p})
	}

	addBuiltinTarget := func(name string) {
		clean := strings.TrimPrefix(strings.TrimSpace(name), "builtin:")
		if clean == "" {
			return
		}
		if _, off := disabledSet[clean]; off {
			return
		}
		if _, dup := seenBuiltins[clean]; dup {
			return
		}
		inline, ok := builtinsByName[clean]
		if !ok {
			preErrors = append(preErrors, ExtensionLoadError{
				Path:  "builtin:" + clean,
				Error: fmt.Sprintf("unknown built-in extension %q", clean),
			})
			return
		}
		seenBuiltins[clean] = struct{}{}
		targets = append(targets, discoveryTarget{isInline: true, inline: inline, source: "builtin:" + clean})
	}

	// 1. Configured paths (explicit paths, directories, or builtin:<name>).
	for _, raw := range configuredPaths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "builtin:") {
			name := strings.TrimPrefix(raw, "builtin:")
			if name == "*" {
				for _, b := range builtinOrder {
					addBuiltinTarget(b)
				}
			} else {
				addBuiltinTarget(name)
			}
			continue
		}
		resolved := elResolveConfiguredPath(raw, cwd)
		info, err := os.Stat(resolved)
		if err == nil && info.IsDir() {
			entries, derr := elDiscoverExtensionsInDir(resolved)
			if derr != nil {
				preErrors = append(preErrors, ExtensionLoadError{Path: resolved, Error: derr.Error()})
				continue
			}
			for _, ent := range entries {
				addFileTarget(ent)
			}
			continue
		}
		addFileTarget(resolved)
	}

	// 2. Auto-discovered agentDir/extensions and <cwd>/.pi/extensions.
	globalDir := filepath.Join(agentDir, "extensions")
	if entries, err := elDiscoverExtensionsInDir(globalDir); err == nil {
		for _, ent := range entries {
			addFileTarget(ent)
		}
	}
	projectDir := filepath.Join(cwd, utils.ConfigDirName, "extensions")
	if elCanonicalPath(projectDir) != elCanonicalPath(globalDir) {
		if entries, err := elDiscoverExtensionsInDir(projectDir); err == nil {
			for _, ent := range entries {
				addFileTarget(ent)
			}
		}
	}

	// 3. Enabled default / supplied built-in factories not disabled or already added.
	for _, b := range builtinOrder {
		addBuiltinTarget(b)
	}

	result := LoadExtensionsResult{
		Extensions: []Extension{},
		Errors:     append([]ExtensionLoadError{}, preErrors...),
		Warnings:   []ExtensionLoadWarning{},
		Runtime:    runtime,
	}

	for _, t := range targets {
		if t.isInline {
			ext, err := LoadExtensionFromFactoryWithRuntime(ctx, t.inline, cwd, runtime, bus)
			if err != nil {
				result.Errors = append(result.Errors, ExtensionLoadError{
					Path:  t.source,
					Error: err.Error(),
				})
				continue
			}
			result.Extensions = append(result.Extensions, *ext)
			continue
		}
		ext, warnings, err := elLoadScriptExtension(ctx, t.path, runtime, cwd, bus, false)
		for _, w := range warnings {
			result.Warnings = append(result.Warnings, ExtensionLoadWarning{Path: t.path, Warning: w})
		}
		if err != nil {
			result.Errors = append(result.Errors, ExtensionLoadError{
				Path:  t.path,
				Error: err.Error(),
			})
			continue
		}
		result.Extensions = append(result.Extensions, *ext)
	}

	kept, omitWarnings := OmitReplacedExtensions(result.Extensions)
	result.Extensions = kept
	result.Warnings = append(result.Warnings, omitWarnings...)
	result.Errors = append(result.Errors, DetectExtensionConflicts(result.Extensions)...)
	return result
}

func elLoadExtensions(ctx context.Context, paths []string, opts *LoadExtensionsOptions, useCache bool) LoadExtensionsResult {
	if ctx == nil {
		ctx = context.Background()
	}
	var cwd string
	var bus EventBus
	var runtime *ExtensionRuntime
	var factories []InlineExtension
	var disabled []string
	var metaMap map[string]utils.PathMetadata
	if opts != nil {
		cwd = opts.Cwd
		bus = opts.EventBus
		runtime = opts.Runtime
		factories = opts.Factories
		disabled = opts.DisabledBuiltinExtensions
		metaMap = opts.PathMetadata
		if opts.UseCache {
			useCache = true
		}
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if bus == nil {
		bus = CreateEventBus()
	}
	if runtime == nil {
		runtime = CreateExtensionRuntime()
	}

	canonMeta := make(map[string]utils.PathMetadata, len(metaMap))
	for k, v := range metaMap {
		canonMeta[k] = v
		canonMeta[elCanonicalPath(k)] = v
	}

	disabledSet := make(map[string]struct{}, len(disabled))
	for _, n := range disabled {
		disabledSet[strings.TrimPrefix(strings.TrimSpace(n), "builtin:")] = struct{}{}
	}
	builtins := make(map[string]InlineExtension)
	for _, b := range GetDefaultBuiltinExtensions() {
		builtins[strings.TrimPrefix(b.Name, "builtin:")] = b
	}
	for _, f := range factories {
		builtins[strings.TrimPrefix(f.Name, "builtin:")] = f
	}

	result := LoadExtensionsResult{
		Extensions: []Extension{},
		Errors:     []ExtensionLoadError{},
		Warnings:   []ExtensionLoadWarning{},
		Runtime:    runtime,
	}

	for _, raw := range paths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "builtin:") {
			name := strings.TrimPrefix(raw, "builtin:")
			if _, off := disabledSet[name]; off {
				continue
			}
			inline, ok := builtins[name]
			if !ok {
				result.Errors = append(result.Errors, ExtensionLoadError{
					Path:  raw,
					Error: fmt.Sprintf("unknown built-in extension %q", name),
				})
				continue
			}
			ext, err := LoadExtensionFromFactoryWithRuntime(ctx, inline, cwd, runtime, bus)
			if err != nil {
				result.Errors = append(result.Errors, ExtensionLoadError{Path: raw, Error: err.Error()})
				continue
			}
			if meta, hasMeta := elLookupPathMetadata(canonMeta, raw, ext.Path, ext.ResolvedPath); hasMeta {
				elApplyExtensionMetadata(ext, meta)
			}
			result.Extensions = append(result.Extensions, *ext)
			continue
		}

		resolved := elResolveConfiguredPath(raw, cwd)
		info, err := os.Stat(resolved)
		if err == nil && info.IsDir() {
			entries, derr := elDiscoverExtensionsInDir(resolved)
			if derr != nil {
				result.Errors = append(result.Errors, ExtensionLoadError{Path: resolved, Error: derr.Error()})
				continue
			}
			for _, ent := range entries {
				canonEnt := elCanonicalPath(ent)
				ext, warns, lerr := elLoadScriptExtension(ctx, canonEnt, runtime, cwd, bus, useCache, canonMeta)
				for _, w := range warns {
					result.Warnings = append(result.Warnings, ExtensionLoadWarning{Path: ent, Warning: w})
				}
				if lerr != nil {
					result.Errors = append(result.Errors, ExtensionLoadError{Path: ent, Error: lerr.Error()})
					continue
				}
				if meta, hasMeta := elLookupPathMetadata(canonMeta, raw, resolved, ent, canonEnt); hasMeta {
					elApplyExtensionMetadata(ext, meta)
				}
				result.Extensions = append(result.Extensions, *ext)
			}
			continue
		}

		canon := elCanonicalPath(resolved)
		ext, warns, lerr := elLoadScriptExtension(ctx, canon, runtime, cwd, bus, useCache, canonMeta)
		for _, w := range warns {
			result.Warnings = append(result.Warnings, ExtensionLoadWarning{Path: canon, Warning: w})
		}
		if lerr != nil {
			result.Errors = append(result.Errors, ExtensionLoadError{Path: canon, Error: lerr.Error()})
			continue
		}
		if meta, hasMeta := elLookupPathMetadata(canonMeta, raw, resolved, canon); hasMeta {
			elApplyExtensionMetadata(ext, meta)
		}
		result.Extensions = append(result.Extensions, *ext)
	}

	// Load explicitly passed non-builtin inline factories in options if requested directly.
	for _, inline := range factories {
		if inline.Builtin {
			continue
		}
		ext, err := LoadExtensionFromFactoryWithRuntime(ctx, inline, cwd, runtime, bus)
		if err != nil {
			result.Errors = append(result.Errors, ExtensionLoadError{Path: inline.Name, Error: err.Error()})
			continue
		}
		if meta, hasMeta := elLookupPathMetadata(canonMeta, inline.Name, ext.Path, ext.ResolvedPath); hasMeta {
			elApplyExtensionMetadata(ext, meta)
		}
		result.Extensions = append(result.Extensions, *ext)
	}

	kept, omitWarnings := OmitReplacedExtensions(result.Extensions)
	result.Extensions = kept
	result.Warnings = append(result.Warnings, omitWarnings...)
	result.Errors = append(result.Errors, DetectExtensionConflicts(result.Extensions)...)
	return result
}

func elLookupPathMetadata(metaMap map[string]utils.PathMetadata, keys ...string) (utils.PathMetadata, bool) {
	if len(metaMap) == 0 {
		return utils.PathMetadata{}, false
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		if m, ok := metaMap[k]; ok {
			return m, true
		}
		if m, ok := metaMap[elCanonicalPath(k)]; ok {
			return m, true
		}
	}
	return utils.PathMetadata{}, false
}

func elApplyExtensionMetadata(ext *Extension, meta utils.PathMetadata) {
	if ext == nil {
		return
	}
	target := ext.ResolvedPath
	if target == "" {
		target = ext.Path
	}
	srcInfo := utils.CreateSourceInfo(target, meta)
	ext.SourceInfo = srcInfo
	for name, tool := range ext.Tools {
		tool.SourceInfo = srcInfo
		ext.Tools[name] = tool
	}
	for name, cmd := range ext.Commands {
		cmd.SourceInfo = srcInfo
		ext.Commands[name] = cmd
	}
}

func elLoadScriptExtension(
	ctx context.Context,
	resolvedPath string,
	runtime *ExtensionRuntime,
	cwd string,
	bus EventBus,
	useCache bool,
	metaMaps ...map[string]utils.PathMetadata,
) (*Extension, []string, error) {
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("extension path %q is a directory", resolvedPath)
	}

	var bundled string
	var warnings []string
	var cached bool
	gen := elCacheGeneration.Load()
	if useCache {
		elCacheMu.Lock()
		ent, ok := elBundleCache[resolvedPath]
		elCacheMu.Unlock()
		if ok && ent.generation == gen && ent.modTimeNs == info.ModTime().UnixNano() && ent.size == info.Size() {
			bundled = ent.code
			warnings = slices.Clone(ent.warnings)
			cached = true
		}
	}
	if !cached {
		bundled, warnings, err = eqjsBundleExtension(resolvedPath)
		if err != nil {
			return nil, warnings, err
		}
		if useCache {
			elCacheMu.Lock()
			elBundleCache[resolvedPath] = elCachedBundle{
				generation: gen,
				modTimeNs:  info.ModTime().UnixNano(),
				size:       info.Size(),
				code:       bundled,
				warnings:   slices.Clone(warnings),
			}
			elCacheMu.Unlock()
		}
	}

	meta := utils.PathMetadata{Source: "local", Scope: "project"}
	if len(metaMaps) > 0 {
		if found, ok := elLookupPathMetadata(metaMaps[0], resolvedPath); ok {
			meta = found
		}
	}
	srcInfo := utils.CreateSourceInfo(resolvedPath, meta)
	ext := elNewEmptyExtension(resolvedPath, resolvedPath, false, false, srcInfo)
	api, state := elCreateExtensionAPI(ext, runtime, cwd, bus)
	if err := eqjsExecuteBundledExtension(ctx, ext, api, runtime, cwd, bundled); err != nil {
		elAbortExtensionAPI(state)
		return nil, warnings, err
	}
	elFinishExtensionAPI(state)
	return ext, warnings, nil
}

// OmitReplacedExtensions drops any Replaceable extension whose tool, command, shortcut,
// or flag name conflicts with a later-preceding / non-replaceable extension, returning warnings.
func OmitReplacedExtensions(extensions []Extension) ([]Extension, []ExtensionLoadWarning) {
	return erFilterReplacedExtensions(extensions)
}

// DetectExtensionConflicts checks for duplicate tools, commands, flags, or shortcuts
// across loaded non-replaceable extensions.
func DetectExtensionConflicts(extensions []Extension) []ExtensionLoadError {
	var errs []ExtensionLoadError
	tools := make(map[string]string)
	commands := make(map[string]string)
	flags := make(map[string]string)

	for _, ext := range extensions {
		for _, k := range erSortedKeys(ext.Tools) {
			if first, dup := tools[k]; dup {
				errs = append(errs, ExtensionLoadError{
					Path:  ext.Path,
					Error: fmt.Sprintf("Tool %q conflicts with %s", k, first),
				})
			} else {
				tools[k] = ext.Path
			}
		}
		for _, k := range erSortedKeys(ext.Commands) {
			if first, dup := commands[k]; dup {
				errs = append(errs, ExtensionLoadError{
					Path:  ext.Path,
					Error: fmt.Sprintf("Command /%s conflicts with %s", k, first),
				})
			} else {
				commands[k] = ext.Path
			}
		}
		for _, k := range erSortedKeys(ext.Flags) {
			if first, dup := flags[k]; dup {
				errs = append(errs, ExtensionLoadError{
					Path:  ext.Path,
					Error: fmt.Sprintf("Flag --%s conflicts with %s", k, first),
				})
			} else {
				flags[k] = ext.Path
			}
		}
	}
	return errs
}

func elResolveConfiguredPath(raw, cwd string) string {
	p := raw
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			p = home
		}
	} else if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

func elCanonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

func elIsSupportedExtFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func elDiscoverExtensionsInDir(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	// Check if dir itself is a package/index extension root when directly pointed to.
	if manifest := ReadPiManifest(filepath.Join(dir, "package.json")); manifest != nil && len(manifest.Extensions) > 0 {
		var out []string
		for _, rel := range manifest.Extensions {
			cand := filepath.Clean(filepath.Join(dir, rel))
			if st, serr := os.Stat(cand); serr == nil && !st.IsDir() {
				out = append(out, cand)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var out []string
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		full := filepath.Join(dir, name)
		st, serr := os.Stat(full) // follows symlinks
		if serr != nil {
			continue
		}
		if !st.IsDir() {
			if elIsSupportedExtFile(name) {
				out = append(out, full)
			}
			continue
		}

		// Subdirectory: check package.json manifest first, then index.ts / index.js.
		if manifest := ReadPiManifest(filepath.Join(full, "package.json")); manifest != nil && len(manifest.Extensions) > 0 {
			addedFromManifest := false
			for _, rel := range manifest.Extensions {
				cand := filepath.Clean(filepath.Join(full, rel))
				if cst, cerr := os.Stat(cand); cerr == nil && !cst.IsDir() {
					out = append(out, cand)
					addedFromManifest = true
				}
			}
			if addedFromManifest {
				continue
			}
		}
		for _, idx := range []string{"index.ts", "index.tsx", "index.mts", "index.js", "index.mjs", "index.cjs"} {
			cand := filepath.Join(full, idx)
			if cst, cerr := os.Stat(cand); cerr == nil && !cst.IsDir() {
				out = append(out, cand)
				break
			}
		}
	}
	return out, nil
}

func elNewEmptyExtension(path, resolvedPath string, hidden, replaceable bool, srcInfo utils.SourceInfo) *Extension {
	return &Extension{
		Path:             path,
		ResolvedPath:     resolvedPath,
		Hidden:           hidden,
		Replaceable:      replaceable,
		SourceInfo:       srcInfo,
		Handlers:         make(map[string][]any),
		Tools:            make(map[string]RegisteredTool),
		MessageRenderers: make(map[string]MessageRenderer),
		EntryRenderers:   make(map[string]EntryRenderer),
		Commands:         make(map[string]RegisteredCommand),
		Flags:            make(map[string]ExtensionFlag),
		Shortcuts:        make(map[tui.KeyId]ExtensionShortcut),
	}
}

func elRunGoFactorySafely(factory ExtensionFactory, api ExtensionAPI) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in extension factory: %v", r)
		}
	}()
	return factory(api)
}

type elHandlerEntry struct {
	id uint64
	fn any
}

type elAPIState struct {
	mu                 sync.Mutex
	phase              string // "loading" | "active" | "failed"
	ext                *Extension
	runtime            *ExtensionRuntime
	cwd                string
	eventBus           EventBus
	nextHandlerID      uint64
	handlerLists       map[string][]elHandlerEntry
	pendingFlagValues  map[string]any
	loadingUnsubs      []func()
	pendingProviders   int
	pendingNativeProvs int
	pendingVirtuals    int
	registeredMcpNames []string
}

func elCreateExtensionAPI(ext *Extension, runtime *ExtensionRuntime, cwd string, bus EventBus) (ExtensionAPI, *elAPIState) {
	if runtime == nil {
		runtime = CreateExtensionRuntime()
	}
	if runtime.FlagValues == nil {
		runtime.FlagValues = make(map[string]any)
	}
	if runtime.McpServers == nil {
		runtime.McpServers = NewMcpServerRegistry()
	}
	if bus == nil {
		bus = CreateEventBus()
	}
	state := &elAPIState{
		phase:              "loading",
		ext:                ext,
		runtime:            runtime,
		cwd:                cwd,
		eventBus:           bus,
		handlerLists:       make(map[string][]elHandlerEntry),
		pendingFlagValues:  make(map[string]any),
		pendingProviders:   len(runtime.PendingProviderRegistrations),
		pendingNativeProvs: len(runtime.PendingNativeProviderRegistrations),
		pendingVirtuals:    len(runtime.PendingVirtualModelRegistrations),
	}
	return &elExtensionAPI{state: state}, state
}

func elFinishExtensionAPI(state *elAPIState) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.phase == "failed" {
		return
	}
	state.phase = "active"
	for k, v := range state.pendingFlagValues {
		if _, exists := state.runtime.FlagValues[k]; !exists {
			state.runtime.FlagValues[k] = v
		}
	}
	clear(state.pendingFlagValues)
}

func elAbortExtensionAPI(state *elAPIState) {
	if state == nil {
		return
	}
	state.mu.Lock()
	state.phase = "failed"
	unsubs := slices.Clone(state.loadingUnsubs)
	state.loadingUnsubs = nil
	mcpNames := slices.Clone(state.registeredMcpNames)
	state.registeredMcpNames = nil
	if state.runtime != nil {
		if len(state.runtime.PendingProviderRegistrations) > state.pendingProviders {
			state.runtime.PendingProviderRegistrations = state.runtime.PendingProviderRegistrations[:state.pendingProviders]
		}
		if len(state.runtime.PendingNativeProviderRegistrations) > state.pendingNativeProvs {
			state.runtime.PendingNativeProviderRegistrations = state.runtime.PendingNativeProviderRegistrations[:state.pendingNativeProvs]
		}
		if len(state.runtime.PendingVirtualModelRegistrations) > state.pendingVirtuals {
			state.runtime.PendingVirtualModelRegistrations = state.runtime.PendingVirtualModelRegistrations[:state.pendingVirtuals]
		}
	}
	extPath := state.ext.Path
	rt := state.runtime
	state.mu.Unlock()

	for _, u := range unsubs {
		if u != nil {
			u()
		}
	}
	if rt != nil && rt.McpServers != nil {
		for _, name := range mcpNames {
			rt.McpServers.Unregister(name, extPath)
		}
	}
}

func (s *elAPIState) addHandler(eventType string, fn any) func() {
	if fn == nil {
		return func() {}
	}
	s.mu.Lock()
	if s.phase == "failed" {
		s.mu.Unlock()
		return func() {}
	}
	s.nextHandlerID++
	id := s.nextHandlerID
	s.handlerLists[eventType] = append(s.handlerLists[eventType], elHandlerEntry{id: id, fn: fn})
	s.syncHandlersLocked(eventType)
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		list := s.handlerLists[eventType]
		filtered := list[:0]
		for _, item := range list {
			if item.id != id {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			delete(s.handlerLists, eventType)
			delete(s.ext.Handlers, eventType)
		} else {
			s.handlerLists[eventType] = filtered
			s.syncHandlersLocked(eventType)
		}
		s.mu.Unlock()
	}
}

func (s *elAPIState) syncHandlersLocked(eventType string) {
	list := s.handlerLists[eventType]
	raw := make([]any, len(list))
	for i, item := range list {
		raw[i] = item.fn
	}
	s.ext.Handlers[eventType] = raw
}

func elMcpNamespace(name string) string {
	var b strings.Builder
	b.WriteString("mcp:")
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func elValidateMcpServerConfig(name string, cfg McpServerConfig) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("MCP server name cannot be empty")
	}
	hasCmd := cfg.Command != nil && strings.TrimSpace(*cfg.Command) != ""
	hasURL := cfg.Url != nil && strings.TrimSpace(*cfg.Url) != ""
	if hasCmd == hasURL {
		return fmt.Errorf("MCP server %q must specify either command or url", name)
	}
	return nil
}

type elWrappedEventBus struct {
	state *elAPIState
	inner EventBus
}

func (b *elWrappedEventBus) On(channel string, handler func(data any)) func() {
	unsub := b.inner.On(channel, handler)
	b.state.mu.Lock()
	loading := b.state.phase == "loading"
	if loading {
		b.state.loadingUnsubs = append(b.state.loadingUnsubs, unsub)
	}
	rt := b.state.runtime
	b.state.mu.Unlock()
	if !loading && rt != nil && rt.TrackEventBusSubscription != nil {
		return rt.TrackEventBusSubscription(unsub)
	}
	return unsub
}

func (b *elWrappedEventBus) Emit(channel string, data any) {
	b.inner.Emit(channel, data)
}

func (b *elWrappedEventBus) Clear() {
	if ctrl, ok := b.inner.(EventBusController); ok {
		ctrl.Clear()
	}
}

type elExtensionAPI struct {
	state *elAPIState
}

func (a *elExtensionAPI) OnProjectTrust(h ProjectTrustHandler) func() {
	return a.state.addHandler("project_trust", h)
}
func (a *elExtensionAPI) OnResourcesDiscover(h ExtensionHandler[ResourcesDiscoverEvent, ResourcesDiscoverResult]) func() {
	return a.state.addHandler("resources_discover", h)
}
func (a *elExtensionAPI) OnSessionStart(h ExtensionHandler[SessionStartEvent, NoResult]) func() {
	return a.state.addHandler("session_start", h)
}
func (a *elExtensionAPI) OnSessionInfoChanged(h ExtensionHandler[SessionInfoChangedEvent, NoResult]) func() {
	return a.state.addHandler("session_info_changed", h)
}
func (a *elExtensionAPI) OnSessionBeforeSwitch(h ExtensionHandler[SessionBeforeSwitchEvent, SessionBeforeSwitchResult]) func() {
	return a.state.addHandler("session_before_switch", h)
}
func (a *elExtensionAPI) OnSessionBeforeFork(h ExtensionHandler[SessionBeforeForkEvent, SessionBeforeForkResult]) func() {
	return a.state.addHandler("session_before_fork", h)
}
func (a *elExtensionAPI) OnSessionBeforeCompact(h ExtensionHandler[SessionBeforeCompactEvent, SessionBeforeCompactResult]) func() {
	return a.state.addHandler("session_before_compact", h)
}
func (a *elExtensionAPI) OnSessionCompact(h ExtensionHandler[SessionCompactEvent, NoResult]) func() {
	return a.state.addHandler("session_compact", h)
}
func (a *elExtensionAPI) OnSessionCompactFailed(h ExtensionHandler[SessionCompactFailedEvent, NoResult]) func() {
	return a.state.addHandler("session_compact_failed", h)
}
func (a *elExtensionAPI) OnSessionShutdown(h ExtensionHandler[SessionShutdownEvent, NoResult]) func() {
	return a.state.addHandler("session_shutdown", h)
}
func (a *elExtensionAPI) OnMcpServersChange(h ExtensionHandler[McpServersChangeEvent, NoResult]) func() {
	return a.state.addHandler("mcp_servers_change", h)
}
func (a *elExtensionAPI) OnSessionBeforeTree(h ExtensionHandler[SessionBeforeTreeEvent, SessionBeforeTreeResult]) func() {
	return a.state.addHandler("session_before_tree", h)
}
func (a *elExtensionAPI) OnSessionTree(h ExtensionHandler[SessionTreeEvent, NoResult]) func() {
	return a.state.addHandler("session_tree", h)
}
func (a *elExtensionAPI) OnContext(h ExtensionHandler[ContextEvent, ContextEventResult]) func() {
	return a.state.addHandler("context", h)
}
func (a *elExtensionAPI) OnContextWithSystem(h ExtensionHandler[ContextWithSystemEvent, ContextEventResult]) func() {
	return a.state.addHandler("context_with_system", h)
}
func (a *elExtensionAPI) OnCacheWarmingDecision(h ExtensionHandler[CacheWarmingDecisionEvent, CacheWarmingDecisionEventResult]) func() {
	return a.state.addHandler("cache_warming_decision", h)
}
func (a *elExtensionAPI) OnBeforeProviderRequest(h ExtensionHandler[BeforeProviderRequestEvent, any]) func() {
	return a.state.addHandler("before_provider_request", h)
}
func (a *elExtensionAPI) OnBeforeProviderHeaders(h ExtensionHandler[BeforeProviderHeadersEvent, NoResult]) func() {
	return a.state.addHandler("before_provider_headers", h)
}
func (a *elExtensionAPI) OnAfterProviderResponse(h ExtensionHandler[AfterProviderResponseEvent, NoResult]) func() {
	return a.state.addHandler("after_provider_response", h)
}
func (a *elExtensionAPI) OnProviderStreamEvent(h ExtensionHandler[ProviderStreamEvent, NoResult]) func() {
	return a.state.addHandler("provider_stream_event", h)
}
func (a *elExtensionAPI) OnBeforeAgentStart(h ExtensionHandler[BeforeAgentStartEvent, BeforeAgentStartEventResult]) func() {
	return a.state.addHandler("before_agent_start", h)
}
func (a *elExtensionAPI) OnAgentStart(h ExtensionHandler[AgentStartEvent, NoResult]) func() {
	return a.state.addHandler("agent_start", h)
}
func (a *elExtensionAPI) OnAgentEnd(h ExtensionHandler[AgentEndEvent, NoResult]) func() {
	return a.state.addHandler("agent_end", h)
}
func (a *elExtensionAPI) OnAgentBeforeSettle(h ExtensionHandler[AgentBeforeSettleEvent, BoundaryResult]) func() {
	return a.state.addHandler("agent_before_settle", h)
}
func (a *elExtensionAPI) OnAgentSettled(h ExtensionHandler[AgentSettledEvent, NoResult]) func() {
	return a.state.addHandler("agent_settled", h)
}
func (a *elExtensionAPI) OnUIPromptStart(h ExtensionHandler[UIPromptStartEvent, NoResult]) func() {
	return a.state.addHandler("ui_prompt_start", h)
}
func (a *elExtensionAPI) OnUIPromptEnd(h ExtensionHandler[UIPromptEndEvent, NoResult]) func() {
	return a.state.addHandler("ui_prompt_end", h)
}
func (a *elExtensionAPI) OnTurnStart(h ExtensionHandler[TurnStartEvent, NoResult]) func() {
	return a.state.addHandler("turn_start", h)
}
func (a *elExtensionAPI) OnTurnEnd(h ExtensionHandler[TurnEndEvent, BoundaryResult]) func() {
	return a.state.addHandler("turn_end", h)
}
func (a *elExtensionAPI) OnMessageStart(h ExtensionHandler[MessageStartEvent, NoResult]) func() {
	return a.state.addHandler("message_start", h)
}
func (a *elExtensionAPI) OnMessageUpdate(h ExtensionHandler[MessageUpdateEvent, NoResult]) func() {
	return a.state.addHandler("message_update", h)
}
func (a *elExtensionAPI) OnMessageEnd(h ExtensionHandler[MessageEndEvent, MessageEndEventResult]) func() {
	return a.state.addHandler("message_end", h)
}
func (a *elExtensionAPI) OnToolExecutionStart(h ExtensionHandler[ToolExecutionStartEvent, NoResult]) func() {
	return a.state.addHandler("tool_execution_start", h)
}
func (a *elExtensionAPI) OnToolExecutionUpdate(h ExtensionHandler[ToolExecutionUpdateEvent, NoResult]) func() {
	return a.state.addHandler("tool_execution_update", h)
}
func (a *elExtensionAPI) OnToolExecutionEnd(h ExtensionHandler[ToolExecutionEndEvent, NoResult]) func() {
	return a.state.addHandler("tool_execution_end", h)
}
func (a *elExtensionAPI) OnModelSelect(h ExtensionHandler[ModelSelectEvent, NoResult]) func() {
	return a.state.addHandler("model_select", h)
}
func (a *elExtensionAPI) OnThinkingLevelSelect(h ExtensionHandler[ThinkingLevelSelectEvent, NoResult]) func() {
	return a.state.addHandler("thinking_level_select", h)
}
func (a *elExtensionAPI) OnToolCall(h ExtensionHandler[ToolCallEvent, ToolCallEventResult]) func() {
	return a.state.addHandler("tool_call", h)
}
func (a *elExtensionAPI) OnToolResult(h ExtensionHandler[ToolResultEvent, ToolResultEventResult]) func() {
	return a.state.addHandler("tool_result", h)
}
func (a *elExtensionAPI) OnUserBash(h ExtensionHandler[UserBashEvent, UserBashEventResult]) func() {
	return a.state.addHandler("user_bash", h)
}
func (a *elExtensionAPI) OnInput(h ExtensionHandler[InputEvent, InputEventResult]) func() {
	return a.state.addHandler("input", h)
}

func (a *elExtensionAPI) RegisterTool(tool ToolDefinition) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	srcInfo := elToolSourceInfo(tool, a.state.ext.SourceInfo)
	a.state.ext.Tools[tool.Name] = RegisteredTool{
		Definition: tool,
		SourceInfo: srcInfo,
	}
	if a.state.phase == "active" && a.state.runtime.RefreshTools != nil {
		refresh := a.state.runtime.RefreshTools
		a.state.mu.Unlock()
		refresh()
		a.state.mu.Lock()
	}
}

func elToolSourceInfo(_ ToolDefinition, fallback utils.SourceInfo) utils.SourceInfo {
	return fallback
}

func (a *elExtensionAPI) RegisterCommand(name string, options RegisteredCommand) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	options.Name = name
	if options.SourceInfo.Path == "" {
		options.SourceInfo = a.state.ext.SourceInfo
	}
	a.state.ext.Commands[name] = options
}

func (a *elExtensionAPI) RegisterShortcut(shortcut tui.KeyId, options ExtensionShortcutOptions) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	a.state.ext.Shortcuts[shortcut] = ExtensionShortcut{
		Shortcut:      shortcut,
		Description:   options.Description,
		Handler:       options.Handler,
		ExtensionPath: a.state.ext.Path,
	}
}

func (a *elExtensionAPI) RegisterFlag(name string, options ExtensionFlagOptions) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	a.state.ext.Flags[name] = ExtensionFlag{
		Name:          name,
		Description:   options.Description,
		Type:          options.Type,
		Default:       options.Default,
		ExtensionPath: a.state.ext.Path,
	}
	if options.Default != nil {
		if a.state.phase == "loading" {
			if _, exists := a.state.pendingFlagValues[name]; !exists {
				a.state.pendingFlagValues[name] = options.Default
			}
		} else if _, exists := a.state.runtime.FlagValues[name]; !exists {
			a.state.runtime.FlagValues[name] = options.Default
		}
	}
}

func (a *elExtensionAPI) GetFlag(name string) any {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if v, ok := a.state.runtime.FlagValues[name]; ok {
		return v
	}
	return a.state.pendingFlagValues[name]
}

func (a *elExtensionAPI) RegisterMessageRenderer(customType string, renderer MessageRenderer) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	a.state.ext.MessageRenderers[customType] = renderer
}

func (a *elExtensionAPI) RegisterMarkdownTransformer(transformer MarkdownTransformer, options ...*MarkdownTransformerOptions) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	msgTypes := map[MarkdownMessageType]struct{}{
		MarkdownMessageTypeCustom:    {},
		MarkdownMessageTypeAssistant: {},
	}
	if len(options) > 0 && options[0] != nil && len(options[0].MessageTypes) > 0 {
		msgTypes = make(map[MarkdownMessageType]struct{}, len(options[0].MessageTypes))
		for _, mt := range options[0].MessageTypes {
			msgTypes[mt] = struct{}{}
		}
	}
	a.state.ext.MarkdownTransformer = &MarkdownTransformerRegistration{
		Transformer:  transformer,
		MessageTypes: msgTypes,
	}
}

func (a *elExtensionAPI) RegisterEntryRenderer(customType string, renderer EntryRenderer) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	a.state.ext.EntryRenderers[customType] = renderer
}

func (a *elExtensionAPI) RegisterToolRenderer(resolver ToolRendererResolver) {
	if resolver == nil {
		return
	}
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	a.state.ext.ToolRenderers = append(a.state.ext.ToolRenderers, resolver)
}

func (a *elExtensionAPI) SendMessage(message CustomMessageInput, options ...*SendMessageOptions) {
	if a.state.runtime.SendMessage != nil {
		a.state.runtime.SendMessage(message, options...)
	}
}

func (a *elExtensionAPI) SendUserMessage(content any, options ...*SendUserMessageOptions) {
	if a.state.runtime.SendUserMessage != nil {
		a.state.runtime.SendUserMessage(content, options...)
	}
}

func (a *elExtensionAPI) AppendEntry(customType string, data any) {
	if a.state.runtime.AppendEntry != nil {
		a.state.runtime.AppendEntry(customType, data)
	}
}

func (a *elExtensionAPI) SetSessionName(name string) {
	if a.state.runtime.SetSessionName != nil {
		a.state.runtime.SetSessionName(name)
	}
}

func (a *elExtensionAPI) GetSessionName() *string {
	if a.state.runtime.GetSessionName != nil {
		return a.state.runtime.GetSessionName()
	}
	return nil
}

func (a *elExtensionAPI) SetLabel(entryId string, label *string) {
	if a.state.runtime.SetLabel != nil {
		a.state.runtime.SetLabel(entryId, label)
	}
}

func (a *elExtensionAPI) Exec(ctx context.Context, command string, args []string, options ...*ExecOptions) (ExecResult, error) {
	effCwd := a.state.cwd
	if len(options) > 0 && options[0] != nil && options[0].Cwd != "" {
		effCwd = options[0].Cwd
	}
	return ExecCommand(ctx, command, args, effCwd, options...)
}

func (a *elExtensionAPI) GetActiveTools() []string {
	if a.state.runtime.GetActiveTools != nil {
		return a.state.runtime.GetActiveTools()
	}
	return nil
}

func (a *elExtensionAPI) GetAllTools() []ToolInfo {
	if a.state.runtime.GetAllTools != nil {
		return a.state.runtime.GetAllTools()
	}
	return nil
}

func (a *elExtensionAPI) GetSettings() Settings {
	if a.state.runtime.GetSettings != nil {
		return a.state.runtime.GetSettings()
	}
	return Settings{}
}

func (a *elExtensionAPI) SetActiveTools(toolNames []string) {
	if a.state.runtime.SetActiveTools != nil {
		a.state.runtime.SetActiveTools(toolNames)
	}
}

func (a *elExtensionAPI) GetCommands() []SlashCommandInfo {
	if a.state.runtime.GetCommands != nil {
		return a.state.runtime.GetCommands()
	}
	return nil
}

func (a *elExtensionAPI) SetModel(ctx context.Context, model *ai.Model) (bool, error) {
	if a.state.runtime.SetModel != nil {
		return a.state.runtime.SetModel(ctx, model)
	}
	return false, nil
}

func (a *elExtensionAPI) GetThinkingLevel() agent.ThinkingLevel {
	if a.state.runtime.GetThinkingLevel != nil {
		return a.state.runtime.GetThinkingLevel()
	}
	return agent.ThinkingLevelOff
}

func (a *elExtensionAPI) SetThinkingLevel(level agent.ThinkingLevel) {
	if a.state.runtime.SetThinkingLevel != nil {
		a.state.runtime.SetThinkingLevel(level)
	}
}

func (a *elExtensionAPI) RegisterNativeProvider(provider *ai.Provider) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if a.state.phase == "loading" {
		a.state.runtime.PendingNativeProviderRegistrations = append(a.state.runtime.PendingNativeProviderRegistrations, PendingNativeProviderRegistration{
			Provider:      provider,
			ExtensionPath: a.state.ext.Path,
		})
		return
	}
	if a.state.runtime.RegisterNativeProvider != nil {
		fn := a.state.runtime.RegisterNativeProvider
		extPath := a.state.ext.Path
		a.state.mu.Unlock()
		fn(provider, extPath)
		a.state.mu.Lock()
	}
}

func (a *elExtensionAPI) RegisterProvider(name string, config ProviderConfig) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if a.state.phase == "loading" {
		a.state.runtime.PendingProviderRegistrations = append(a.state.runtime.PendingProviderRegistrations, PendingProviderRegistration{
			Name:          name,
			Config:        config,
			ExtensionPath: a.state.ext.Path,
		})
		return
	}
	if a.state.runtime.RegisterProvider != nil {
		fn := a.state.runtime.RegisterProvider
		extPath := a.state.ext.Path
		a.state.mu.Unlock()
		fn(name, config, extPath)
		a.state.mu.Lock()
	}
}

func (a *elExtensionAPI) UnregisterProvider(name string) {
	if a.state.runtime.UnregisterProvider != nil {
		a.state.runtime.UnregisterProvider(name, a.state.ext.Path)
	}
}

func (a *elExtensionAPI) RegisterMcpServer(name string, config McpServerConfig) {
	if err := elValidateMcpServerConfig(name, config); err != nil {
		panic(err.Error())
	}
	ns := elMcpNamespace(name)
	for _, existing := range a.state.runtime.McpServers.List() {
		if existing.Name != name && elMcpNamespace(existing.Name) == ns {
			panic(fmt.Sprintf("MCP server %q conflicts with registered server %q", name, existing.Name))
		}
	}
	a.state.mu.Lock()
	a.state.registeredMcpNames = append(a.state.registeredMcpNames, name)
	extPath := a.state.ext.Path
	rt := a.state.runtime
	a.state.mu.Unlock()
	rt.McpServers.Register(RegisteredMcpServer{
		Name:          name,
		Config:        config,
		ExtensionPath: extPath,
	})
}

func (a *elExtensionAPI) UnregisterMcpServer(name string) {
	if a.state.runtime.McpServers != nil {
		a.state.runtime.McpServers.Unregister(name, a.state.ext.Path)
	}
}

func (a *elExtensionAPI) GetMcpServers() []RegisteredMcpServer {
	if a.state.runtime.McpServers != nil {
		return a.state.runtime.McpServers.List()
	}
	return nil
}

func (a *elExtensionAPI) RegisterVirtualModel(model VirtualModelDefinition) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if a.state.phase == "loading" {
		a.state.runtime.PendingVirtualModelRegistrations = append(a.state.runtime.PendingVirtualModelRegistrations, PendingVirtualModelRegistration{
			Definition:    model,
			ExtensionPath: a.state.ext.Path,
		})
		return
	}
	if a.state.runtime.RegisterVirtualModel != nil {
		fn := a.state.runtime.RegisterVirtualModel
		extPath := a.state.ext.Path
		a.state.mu.Unlock()
		fn(model, extPath)
		a.state.mu.Lock()
	}
}

func (a *elExtensionAPI) UnregisterVirtualModel(provider, id string) {
	if a.state.runtime.UnregisterVirtualModel != nil {
		a.state.runtime.UnregisterVirtualModel(provider, id)
	}
}

func (a *elExtensionAPI) Events() EventBus {
	return &elWrappedEventBus{state: a.state, inner: a.state.eventBus}
}
