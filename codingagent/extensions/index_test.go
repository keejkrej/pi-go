// Tests for codingagent/extensions/index.go.

package extensions_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions"
)

func TestBuiltInExtensionsRegisteredAndLoadable(t *testing.T) {
	if len(extensions.BuiltInExtensions) != 4 {
		t.Fatalf("expected 4 BuiltInExtensions factories, got %d", len(extensions.BuiltInExtensions))
	}
	if len(extensions.BuiltInInlineExtensions) != 4 {
		t.Fatalf("expected 4 BuiltInInlineExtensions, got %d", len(extensions.BuiltInInlineExtensions))
	}

	wantSpecs := []struct {
		name        string
		replaceable bool
	}{
		{name: "llama.cpp", replaceable: false},
		{name: "codemode", replaceable: true},
		{name: "tool-search", replaceable: true},
		{name: "mcp", replaceable: true},
	}

	defaults := core.GetDefaultBuiltinExtensions()
	if len(defaults) != len(wantSpecs) {
		t.Fatalf("GetDefaultBuiltinExtensions() len = %d, want %d", len(defaults), len(wantSpecs))
	}
	for i, spec := range wantSpecs {
		inline := extensions.BuiltInInlineExtensions[i]
		if inline.Name != spec.name || !inline.Builtin || inline.Replaceable != spec.replaceable || inline.Factory == nil {
			t.Fatalf("BuiltInInlineExtensions[%d] = %+v, want name=%q builtin=true replaceable=%v", i, inline, spec.name, spec.replaceable)
		}
		if defaults[i].Name != spec.name || !defaults[i].Builtin || defaults[i].Replaceable != spec.replaceable {
			t.Fatalf("default builtin[%d] = %+v, want name=%q builtin=true replaceable=%v", i, defaults[i], spec.name, spec.replaceable)
		}
	}

	t.Setenv("PI_OFFLINE", "1")
	cwd := t.TempDir()
	agentDir := t.TempDir()
	ctx := context.Background()

	// All 4 built-ins load cleanly when NoExtensions=false.
	rl := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: core.SettingsManagerInMemory(),
	})
	t.Cleanup(rl.Close)
	if err := rl.Reload(ctx); err != nil {
		t.Fatalf("Reload() failed: %v", err)
	}
	loaded := rl.GetExtensions()
	if len(loaded.Errors) != 0 {
		t.Fatalf("unexpected extension load errors: %+v", loaded.Errors)
	}
	var gotPaths []string
	for _, ext := range loaded.Extensions {
		gotPaths = append(gotPaths, ext.Path)
	}
	for _, spec := range wantSpecs {
		wantPath := "builtin:" + spec.name
		if !slices.Contains(gotPaths, wantPath) {
			t.Fatalf("missing built-in extension %q in loaded paths %v", wantPath, gotPaths)
		}
	}

	// Verify Replaceable allows custom extension defining /mcp without hard conflict.
	customReplace := filepath.Join(cwd, "custom-mcp.ts")
	customSrc := `export default function(pi) { pi.registerCommand("mcp", { description: "custom mcp", handler: async () => {} }); }`
	if err := os.WriteFile(customReplace, []byte(customSrc), 0o644); err != nil {
		t.Fatalf("write custom extension: %v", err)
	}
	rlReplace := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:                      cwd,
		AgentDir:                 agentDir,
		SettingsManager:          core.SettingsManagerInMemory(),
		AdditionalExtensionPaths: []string{customReplace},
	})
	t.Cleanup(rlReplace.Close)
	if err := rlReplace.Reload(ctx); err != nil {
		t.Fatalf("Reload(replace) failed: %v", err)
	}
	repRes := rlReplace.GetExtensions()
	if len(repRes.Errors) != 0 {
		t.Fatalf("unexpected errors when replacing replaceable built-in command: %+v", repRes.Errors)
	}
	for _, ext := range repRes.Extensions {
		if ext.Path == "builtin:mcp" {
			t.Fatalf("expected replaceable builtin:mcp to be omitted when /mcp is overridden, got %+v", repRes.Extensions)
		}
	}

	// Verify DisabledBuiltinExtensions selectively excludes built-ins.
	rlDisabled := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:                       cwd,
		AgentDir:                  agentDir,
		SettingsManager:           core.SettingsManagerInMemory(),
		DisabledBuiltinExtensions: []string{"mcp", "codemode"},
	})
	t.Cleanup(rlDisabled.Close)
	if err := rlDisabled.Reload(ctx); err != nil {
		t.Fatalf("Reload(disabled) failed: %v", err)
	}
	disRes := rlDisabled.GetExtensions()
	if len(disRes.Errors) != 0 {
		t.Fatalf("unexpected errors with DisabledBuiltinExtensions: %+v", disRes.Errors)
	}
	for _, ext := range disRes.Extensions {
		if ext.Path == "builtin:mcp" || ext.Path == "builtin:codemode" {
			t.Fatalf("disabled built-in %q should not be loaded", ext.Path)
		}
	}
	if !slices.ContainsFunc(disRes.Extensions, func(e core.Extension) bool { return e.Path == "builtin:llama.cpp" }) {
		t.Fatalf("expected builtin:llama.cpp to remain loaded")
	}

	// Verify NoExtensions suppresses discovered/built-in extensions unless explicitly added.
	rlNoExt := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: core.SettingsManagerInMemory(),
		NoExtensions:    true,
	})
	t.Cleanup(rlNoExt.Close)
	if err := rlNoExt.Reload(ctx); err != nil {
		t.Fatalf("Reload(NoExtensions) failed: %v", err)
	}
	if got := rlNoExt.GetExtensions().Extensions; len(got) != 0 {
		t.Fatalf("expected 0 built-in/discovered extensions when NoExtensions=true, got %d", len(got))
	}
}
