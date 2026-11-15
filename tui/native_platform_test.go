package tui

import (
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestNativePlatformAndClipboardContracts(t *testing.T) {
	helper := GetNativePlatformHelper()
	clipboard := GetNativeClipboard()

	switch runtime.GOOS {
	case "darwin", "windows":
		if helper == nil {
			t.Fatalf("expected non-nil GetNativePlatformHelper on %s", runtime.GOOS)
		}
		if clipboard == nil {
			t.Fatalf("expected non-nil GetNativeClipboard on %s", runtime.GOOS)
		}
		if runtime.GOOS == "darwin" {
			if _, ok := clipboard.(NativeClipboardFilePaths); !ok {
				t.Fatalf("expected darwin clipboard helper to implement NativeClipboardFilePaths")
			}
		}

		for _, key := range []ModifierKey{
			ModifierKeyShift,
			ModifierKeyCommand,
			ModifierKeyControl,
			ModifierKeyOption,
			ModifierKey("unknown"),
		} {
			pressed, err := helper.IsModifierPressed(key)
			if err != nil {
				t.Fatalf("IsModifierPressed(%q) returned unexpected error: %v", key, err)
			}
			_ = pressed
			_ = IsNativeModifierPressed(key)
		}

		// Unknown modifier via global helper must return false without panicking.
		if IsNativeModifierPressed(ModifierKey("nonexistent")) {
			t.Fatal("expected IsNativeModifierPressed(nonexistent) to be false")
		}

		// Smoke-check read-only clipboard calls do not panic (do not mutate user clipboard).
		if _, _, err := clipboard.GetText(); err != nil {
			t.Logf("GetText returned error in current session: %v", err)
		}
		if _, _, err := clipboard.GetImage(); err != nil {
			t.Logf("GetImage returned error in current session: %v", err)
		}
		if fp, ok := clipboard.(NativeClipboardFilePaths); ok {
			if _, _, err := fp.GetFilePaths(); err != nil {
				t.Logf("GetFilePaths returned error in current session: %v", err)
			}
		}

	case "linux":
		if helper != nil {
			t.Fatalf("expected nil GetNativePlatformHelper on linux, got %#v", helper)
		}
		origDisplay := ""
		// Cache and DISPLAY checks without external X server dependency.
		t.Setenv("DISPLAY", "")
		if got := GetNativeClipboard(); got != nil {
			t.Fatalf("expected nil GetNativeClipboard when DISPLAY is unset, got %#v", got)
		}
		_ = origDisplay
		t.Setenv("DISPLAY", ":99")
		if got := GetNativeClipboard(); got == nil {
			t.Fatal("expected non-nil GetNativeClipboard when DISPLAY is set on linux")
		} else {
			if _, ok := got.(npNativePlatformHelper); ok {
				t.Fatal("linux clipboard helper must not implement npNativePlatformHelper")
			}
			if _, ok := got.(NativeClipboardFilePaths); ok {
				t.Fatal("linux clipboard helper must not implement NativeClipboardFilePaths")
			}
		}

	default:
		if helper != nil || clipboard != nil {
			t.Fatalf("expected nil helpers on unsupported OS %s", runtime.GOOS)
		}
	}
}

func TestNativePlatformLoaderCacheAndMismatches(t *testing.T) {
	cur := npCurrentPlatform()
	suffix := ""
	if cur == "linux" {
		suffix = "-x11"
	}
	first := npLoadNativePlatformHelper(cur, suffix)
	second := npLoadNativePlatformHelper(cur, suffix)
	if (cur == "darwin" || cur == "win32" || cur == "linux") && first == nil {
		t.Fatalf("expected non-nil helper for %s%q", cur, suffix)
	}
	if first != second {
		t.Fatalf("expected npLoadNativePlatformHelper to cache helper instance for (%q, %q)", cur, suffix)
	}

	if got := npLoadNativePlatformHelper("plan9", ""); got != nil {
		t.Fatalf("expected nil helper for unsupported platform plan9, got %#v", got)
	}
}

func TestGetNativeModuleCandidates(t *testing.T) {
	moduleURL := "file:///repo/packages/tui/dist/native-platform.js"
	execPath := "/app/bin/pi"
	pkgDir := "/repo/node_modules/@earendil-works/pi-tui/package.json"

	got := GetNativeModuleCandidates("native/darwin/pi-tui.node", &NativeModuleCandidateOptions{
		ModuleUrl: &moduleURL,
		ExecPath:  &execPath,
		ResolvePackage: func(specifier string) (string, error) {
			if specifier != nmpTuiPackageName+"/package.json" {
				t.Fatalf("unexpected package specifier: %s", specifier)
			}
			return pkgDir, nil
		},
	})

	want := []string{
		filepath.Join("/repo/node_modules/@earendil-works/pi-tui", "native/darwin/pi-tui.node"),
		filepath.Clean("/repo/packages/tui/native/darwin/pi-tui.node"),
		filepath.Clean("/repo/packages/native/darwin/pi-tui.node"),
		filepath.Join("/app/bin", "pi-tui.node"),
		filepath.Join("/app/bin/native", "pi-tui.node"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("GetNativeModuleCandidates:\ngot:  %v\nwant: %v", got, want)
	}

	// Failed package resolution is silently skipped and duplicates are removed.
	dupExec := "/repo/packages/tui/pi"
	gotNoPkg := GetNativeModuleCandidates("native/pi-tui.node", &NativeModuleCandidateOptions{
		ModuleUrl: &moduleURL,
		ExecPath:  &dupExec,
		ResolvePackage: func(specifier string) (string, error) {
			return "", errors.New("not found")
		},
	})
	wantNoPkg := []string{
		filepath.Clean("/repo/packages/tui/native/pi-tui.node"),
		filepath.Clean("/repo/packages/native/pi-tui.node"),
		filepath.Join("/repo/packages/tui", "pi-tui.node"),
	}
	if !slices.Equal(gotNoPkg, wantNoPkg) {
		t.Fatalf("GetNativeModuleCandidates without package:\ngot:  %v\nwant: %v", gotNoPkg, wantNoPkg)
	}

	// nil options works cleanly and yields unique paths ending with baseName.
	defaults := GetNativeModuleCandidates("foo.node", nil)
	if len(defaults) < 3 {
		t.Fatalf("expected at least 3 default candidates, got %v", defaults)
	}
}
