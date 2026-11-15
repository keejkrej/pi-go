// Ported from packages/coding-agent/src/core/extensions/loader.ts (pi v1.0.0).

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/ai"
)

const (
	elJitiSkipWarningPrefix = "pi-go cannot load JavaScript/TypeScript extensions: "
)

// ClearExtensionCache clears any cached extension load state (no-op in barebone Go builds).
func ClearExtensionCache() {}

// CreateExtensionRuntime creates an empty ExtensionRuntime whose host-bound actions are no-ops until bound.
func CreateExtensionRuntime() *ExtensionRuntime {
	rt := &ExtensionRuntime{
		FlagValues: make(map[string]any),
		McpServers: NewMcpServerRegistry(),
	}
	rt.CreateContext = func() ExtensionContext {
		return nil
	}
	rt.AssertActive = func() {}
	rt.Invalidate = func(...string) {}
	rt.TrackEventBusSubscription = func(unsubscribe func()) func() {
		if unsubscribe == nil {
			return func() {}
		}
		return unsubscribe
	}
	rt.RegisterProvider = func(string, ProviderConfig, ...string) {}
	rt.RegisterNativeProvider = func(*ai.Provider, ...string) {}
	rt.UnregisterProvider = func(string, ...string) {}
	rt.RegisterVirtualModel = func(VirtualModelDefinition, ...string) {}
	rt.UnregisterVirtualModel = func(string, string) {}
	return rt
}

func elIsScriptExtensionPath(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".ts" || ext == ".js" || ext == ".mjs" || ext == ".cjs"
}

func elCheckScriptWarnings(paths []string) []ExtensionLoadWarning {
	var warnings []ExtensionLoadWarning
	for _, p := range paths {
		if elIsScriptExtensionPath(p) {
			if _, err := os.Stat(p); err == nil {
				warnings = append(warnings, ExtensionLoadWarning{
					Path:    p,
					Warning: elJitiSkipWarningPrefix + p,
				})
			}
		}
	}
	return warnings
}

// LoadExtensionFromFactory loads a barebone InlineExtension without invoking JS/TS modules.
// In barebone mode, no extension modules are loaded; an empty result is returned.
func LoadExtensionFromFactory(_ context.Context, _ InlineExtension, _ string, _ ...EventBus) (*Extension, error) {
	return nil, fmt.Errorf("pi-go barebone build does not execute extension factories")
}

// LoadExtensions returns an empty LoadExtensionsResult (0 extensions in barebone Go builds),
// emitting a warning for any explicit JS/TS extension file paths passed in paths.
func LoadExtensions(_ context.Context, paths []string, _ string, _ ...EventBus) LoadExtensionsResult {
	return LoadExtensionsResult{
		Extensions: []Extension{},
		Errors:     []ExtensionLoadError{},
		Warnings:   elCheckScriptWarnings(paths),
		Runtime:    CreateExtensionRuntime(),
	}
}

// LoadExtensionsCached mirrors LoadExtensions in barebone mode (0 extensions).
func LoadExtensionsCached(ctx context.Context, paths []string, cwd string, eventBus ...EventBus) LoadExtensionsResult {
	return LoadExtensions(ctx, paths, cwd, eventBus...)
}

// DiscoverAndLoadExtensions discovers configured paths without loading JS/TS extensions,
// returning an empty 0-extension LoadExtensionsResult.
func DiscoverAndLoadExtensions(ctx context.Context, configuredPaths []string, cwd string, _ string, eventBus ...EventBus) LoadExtensionsResult {
	return LoadExtensionsResult{
		Extensions: []Extension{},
		Errors:     []ExtensionLoadError{},
		Warnings:   elCheckScriptWarnings(configuredPaths),
		Runtime:    CreateExtensionRuntime(),
	}
}
