package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/tui"
)

func TestSelfUpdateUnavailableAndShareUrlParity(t *testing.T) {
	origExec := configExecPath
	origArgv := configArgv
	origPlat := configPlatform
	t.Cleanup(func() {
		configExecPath = origExec
		configArgv = origArgv
		configPlatform = origPlat
	})

	// 1. GetShareViewerUrl honors PI_SHARE_VIEWER_URL and passes raw gistId.
	t.Setenv("PI_SHARE_VIEWER_URL", "")
	if got := GetShareViewerUrl("abc-123"); got != "https://pi.dev/session/#abc-123" {
		t.Fatalf("default GetShareViewerUrl = %q", got)
	}
	t.Setenv("PI_SHARE_VIEWER_URL", "https://custom.viewer/view")
	if got := GetShareViewerUrl("raw id"); got != "https://custom.viewer/view#raw id" {
		t.Fatalf("custom PI_SHARE_VIEWER_URL = %q", got)
	}

	// 2. DetectInstallChange strips UTF-8 BOM.
	tmp := t.TempDir()
	pkgJson := filepath.Join(tmp, "package.json")
	if err := os.WriteFile(pkgJson, []byte("\uFEFF"+`{"name":"`+PackageName+`","version":"2.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ch := DetectInstallChange(pkgJson); ch == nil || ch.Kind != "updated" || ch.Version != "2.0.0" {
		t.Fatalf("BOM DetectInstallChange = %+v", ch)
	}

	// 3. Unknown install method unavailable guidance uses resolved installSpec.
	configPlatform = func() string { return "darwin" }
	configExecPath = func() (string, error) { return "/usr/local/bin/node", nil }
	configArgv = func() []string { return []string{"node", "/tmp/local-dev/src/cli.ts"} }
	if msg := GetSelfUpdateUnavailableInstruction(PackageName, nil, SelfUpdatePackageTarget{
		PackageName: "@fork/pi",
		InstallSpec: "@fork/pi@next",
	}); msg != "Update @fork/pi@next using the package manager, wrapper, or source checkout that provides this installation." {
		t.Fatalf("unexpected unknown unavailable instruction: %q", msg)
	}

	// 4. Read-only npm parent dir makes package non-writable and returns detailed instruction.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		nmDir := filepath.Join(tmp, "prefix", "lib", "node_modules", "@earendil-works")
		pkgDir := filepath.Join(nmDir, "pi-coding-agent")
		distDir := filepath.Join(pkgDir, "dist")
		if err := os.MkdirAll(distDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"`+PackageName+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cliPath := filepath.Join(distDir, "cli.js")
		if err := os.WriteFile(cliPath, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(nmDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(nmDir, 0o755) })

		configArgv = func() []string { return []string{"node", cliPath} }
		if cmd := GetSelfUpdateCommand(PackageName, nil); cmd != nil {
			t.Fatalf("expected nil self-update command when parent dir read-only, got %+v", cmd)
		}
		msg := GetSelfUpdateUnavailableInstruction(PackageName, nil)
		if !strings.Contains(msg, "This installation is managed by a global npm install, but the install path is not writable. Update it yourself with: ") ||
			!strings.Contains(msg, "npm ") {
			t.Fatalf("unexpected read-only unavailable instruction: %q", msg)
		}
	}
}

func TestToolsManagerParity(t *testing.T) {
	origFetch := tmFetch
	origPlat := tmPlatform
	origArch := tmArch
	origExists := tmStatExists
	origLookPath := tmLookPath
	origBinDir := tmBinDir
	t.Cleanup(func() {
		tmFetch = origFetch
		tmPlatform = origPlat
		tmArch = origArch
		tmStatExists = origExists
		tmLookPath = origLookPath
		tmBinDir = origBinDir
	})

	tmStatExists = func(string) bool { return false }

	// 1. GetToolPath falls back to fdfind on Linux/Debian systems.
	tmPlatform = func() string { return "linux" }
	tmLookPath = func(bin string) (string, error) {
		if bin == "fdfind" {
			return "/usr/bin/fdfind", nil
		}
		return "", errors.New("not found")
	}
	if got := GetToolPath("fd"); got == nil || *got != "/usr/bin/fdfind" {
		t.Fatalf("expected fdfind fallback, got %v", got)
	}
	tmLookPath = func(string) (string, error) { return "", errors.New("not found") }

	// 2. Linux fd asset uses unknown-linux-musl.tar.gz.
	if u := tmTools["fd"].GetAssetUrl("10.4.2", "linux", "x64"); !strings.HasSuffix(u, "x86_64-unknown-linux-musl.tar.gz") {
		t.Fatalf("expected linux x64 fd musl URL, got %q", u)
	}
	if u := tmTools["fd"].GetAssetUrl("10.4.2", "linux", "arm64"); !strings.HasSuffix(u, "aarch64-unknown-linux-musl.tar.gz") {
		t.Fatalf("expected linux arm64 fd musl URL, got %q", u)
	}

	// 3. GetLatestVersion sends User-Agent header.
	tmFetch = func(_ context.Context, _ string, opts *FetchWithRetryOptions) (*http.Response, error) {
		if opts == nil || !strings.HasPrefix(opts.Headers.Get("User-Agent"), "pi/") {
			t.Fatalf("expected User-Agent header in GetLatestVersion, got %+v", opts)
		}
		return &http.Response{
			StatusCode: 302,
			Header:     http.Header{"Location": {"https://github.com/sharkdp/fd/releases/tag/v10.4.2"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	if ver, err := GetLatestVersion(context.Background(), "sharkdp/fd"); err != nil || ver != "10.4.2" {
		t.Fatalf("GetLatestVersion = (%q, %v)", ver, err)
	}

	// 4. PI_OFFLINE=true/yes runs BEFORE android check.
	tmPlatform = func() string { return "android" }
	tmArch = func() string { return "arm64" }
	t.Setenv("PI_OFFLINE", "true")
	var statuses []ToolStatus
	if p := EnsureTool(context.Background(), "fd", func(s ToolStatus) { statuses = append(statuses, s) }); p != nil {
		t.Fatalf("expected nil with PI_OFFLINE=true, got %v", *p)
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0].Message, "Offline mode enabled") {
		t.Fatalf("expected offline warning before android check, got %+v", statuses)
	}

	// 5. Android Termux emits pkg install guidance without fetching.
	t.Setenv("PI_OFFLINE", "")
	fetchCalls := 0
	tmFetch = func(context.Context, string, *FetchWithRetryOptions) (*http.Response, error) {
		fetchCalls++
		return nil, nil
	}
	statuses = nil
	if p := EnsureTool(context.Background(), "rg", func(s ToolStatus) { statuses = append(statuses, s) }); p != nil || fetchCalls != 0 {
		t.Fatalf("expected android early exit, got p=%v calls=%d", p, fetchCalls)
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0].Message, "pkg install ripgrep") {
		t.Fatalf("expected Termux pkg install message, got %+v", statuses)
	}

	// 6. darwin/x64 fd pins 10.3.0 without calling releases/latest and reports installed status.
	tmpBin := t.TempDir()
	tmBinDir = func() string { return tmpBin }
	tmPlatform = func() string { return "darwin" }
	tmArch = func() string { return "x64" }

	var tgzBuf bytes.Buffer
	gz := gzip.NewWriter(&tgzBuf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho fd\n")
	if err := tw.WriteHeader(&tar.Header{Name: "fd-v10.3.0-x86_64-apple-darwin/fd", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()

	var requestedURLs []string
	tmFetch = func(_ context.Context, rawURL string, _ *FetchWithRetryOptions) (*http.Response, error) {
		requestedURLs = append(requestedURLs, rawURL)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(tgzBuf.Bytes())),
		}, nil
	}
	statuses = nil
	installed := EnsureTool(context.Background(), "fd", func(s ToolStatus) { statuses = append(statuses, s) })
	if installed == nil || len(requestedURLs) != 1 || !strings.Contains(requestedURLs[0], "v10.3.0") {
		t.Fatalf("expected pinned darwin/x64 fd 10.3.0 download without latest redirect, got path=%v urls=%v", installed, requestedURLs)
	}
	if !slices.ContainsFunc(statuses, func(s ToolStatus) bool { return s.Type == "info" && strings.Contains(s.Message, "fd installed to ") }) {
		t.Fatalf("expected installed info status, got %+v", statuses)
	}
}

func TestChangelogAndGitAndVersionCheckParity(t *testing.T) {
	// 1. Changelog only rewrites markdown links, handles master, keeps raw URLs and titles, encodes UTF-8 paths, rejects root escapes.
	md := strings.Join([]string{
		"Bare URL stays: https://github.com/badlogic/pi-mono/issues/1",
		`[Title](docs/café.md "Docs")`,
		`[Master](https://github.com/badlogic/pi-mono/tree/master/packages/ai/)`,
		`[Escape](../../../../etc/passwd)`,
	}, "\n")
	got := NormalizeChangelogLinks(md, "1.5.2")
	want := strings.Join([]string{
		"Bare URL stays: https://github.com/badlogic/pi-mono/issues/1",
		`[Title](https://github.com/earendil-works/pi/blob/v1.5.2/packages/coding-agent/docs/caf%C3%A9.md "Docs")`,
		`[Master](https://github.com/earendil-works/pi/tree/v1.5.2/packages/ai/)`,
		`[Escape](../../../../etc/passwd)`,
	}, "\n")
	if got != want {
		t.Fatalf("changelog parity mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	// 2. Git URL parsing parity.
	if g := ParseGitUrl("HTTPS://github.com/user/repo.git@feature/foo"); g == nil ||
		g.Type != "git" || !g.Pinned || g.Ref != "feature/foo" || g.Path != "user/repo" || g.Host != "github.com" {
		t.Fatalf("case-insensitive slash ref parse mismatch: %+v", g)
	}
	if g := ParseGitUrl("https://github.com/user/repo"); g == nil || g.Pinned || g.Type != "git" {
		t.Fatalf("unpinned git parse mismatch: %+v", g)
	}
	for _, bad := range []string{
		"git+ssh://git@github.com/user/repo",
		"git+https://github.com/user/repo",
		"git:git+ssh://git@github.com/user/repo",
		"https://github.com/onlyone",
		"https://github.com/onlyone/",
		"git:onlyone/repo",
	} {
		if g := ParseGitUrl(bad); g != nil {
			t.Fatalf("expected nil for %q, got %+v", bad, g)
		}
	}

	// 3. Version check error cause + PI_OFFLINE + TimeoutMs + trimmed blank version.
	plainNested := fmt.Errorf("fetch failed: %w", errors.New("self-signed cert"))
	if msg := FormatVersionCheckError(plainNested); !strings.Contains(msg, "(cause: self-signed cert)") {
		t.Fatalf("expected '(cause: ...)' suffix, got %q", msg)
	}

	origFetch := vcFetch
	t.Cleanup(func() { vcFetch = origFetch })

	t.Setenv("PI_OFFLINE", "1")
	vcCalls := 0
	vcFetch = func(context.Context, string, *FetchWithRetryOptions) (*http.Response, error) {
		vcCalls++
		return nil, nil
	}
	if rel, err := GetLatestPiRelease(context.Background(), "1.5.2", nil); err != nil || rel != nil || vcCalls != 0 {
		t.Fatalf("expected offline GetLatestPiRelease=(nil, nil), got (%+v, %v) calls=%d", rel, err, vcCalls)
	}
	if ver, err := GetLatestPiVersion(context.Background(), "1.5.2", nil); err != nil || ver != "" {
		t.Fatalf("expected offline GetLatestPiVersion=(\"\", nil), got (%q, %v)", ver, err)
	}

	t.Setenv("PI_OFFLINE", "")
	vcFetch = func(_ context.Context, _ string, opts *FetchWithRetryOptions) (*http.Response, error) {
		if opts == nil || opts.TimeoutMs != 10000 {
			t.Fatalf("expected 10000ms timeout, got %+v", opts)
		}
		return netJsonResponse(200, `{"version":"   "}`), nil
	}
	if _, err := GetLatestPiRelease(context.Background(), "1.5.2", nil); err == nil {
		t.Fatal("expected whitespace-only version to be rejected")
	}
}

func TestClipboardImageAndImageMimeAndHighlightParity(t *testing.T) {
	origPlat := ciPlatform
	origRun := ciRunCommand
	origNative := ciNativeClipboard
	origEnv := ciGetenv
	origWsl := ciIsWSL
	t.Cleanup(func() {
		ciPlatform = origPlat
		ciRunCommand = origRun
		ciNativeClipboard = origNative
		ciGetenv = origEnv
		ciIsWSL = origWsl
	})

	// 1. WSL without Wayland env skips wl-paste and passes 1000ms list timeout / 5000ms powershell timeout.
	ciPlatform = func() string { return "linux" }
	ciGetenv = func(string) string { return "" }
	ciIsWSL = func() bool { return true }
	nativeCalled := 0
	ciNativeClipboard = func() tui.NativeClipboard {
		nativeCalled++
		return &clipMockNative{image: testDecodeB64(t, test1x1PngB64), imageOk: true}
	}
	var calls []string
	var timeouts []int
	ciRunCommand = func(cmd string, args []string, opts *ClipboardCommandOptions) ([]byte, bool) {
		calls = append(calls, cmd)
		if opts != nil {
			timeouts = append(timeouts, opts.TimeoutMs)
		} else {
			timeouts = append(timeouts, 0)
		}
		if cmd == "xclip" {
			return []byte("text/plain\n"), true
		}
		if cmd == "powershell.exe" {
			return []byte(test1x1PngB64), true
		}
		return nil, false
	}
	img, err := ReadClipboardImage()
	if err != nil || img == nil || img.MimeType != "image/png" {
		t.Fatalf("WSL powershell fallback failed: img=%+v err=%v", img, err)
	}
	if slices.Contains(calls, "wl-paste") {
		t.Fatalf("wl-paste should not run outside Wayland env: calls=%v", calls)
	}
	if !slices.Equal(calls, []string{"xclip", "powershell.exe"}) || !slices.Equal(timeouts, []int{1000, 5000}) {
		t.Fatalf("unexpected calls/timeouts: calls=%v timeouts=%v", calls, timeouts)
	}
	if nativeCalled != 0 {
		t.Fatalf("expected native fallback skipped after xclip succeeded with no image")
	}

	// 2. Wayland succeeding with empty/text stops xclip and native fallback.
	ciIsWSL = func() bool { return false }
	ciGetenv = func(k string) string {
		if k == "WAYLAND_DISPLAY" {
			return "wayland-0"
		}
		return ""
	}
	calls = nil
	nativeCalled = 0
	ciRunCommand = func(cmd string, args []string, opts *ClipboardCommandOptions) ([]byte, bool) {
		calls = append(calls, cmd)
		if cmd == "wl-paste" {
			return []byte("text/plain\n"), true
		}
		return nil, false
	}
	img, err = ReadClipboardImage()
	if err != nil || img != nil || !slices.Equal(calls, []string{"wl-paste"}) || nativeCalled != 0 {
		t.Fatalf("expected Wayland empty to stop fallback, got img=%v err=%v calls=%v native=%d", img, err, calls, nativeCalled)
	}

	// 3. mimeIsPng requires ihdrLen == 13; mimeIsBmp validates DIB & size fields.
	validPng := testCreatePng(t, 2, 2)
	badIhdr := append([]byte(nil), validPng...)
	badIhdr[11] = 14
	if DetectSupportedImageMimeType(badIhdr) != "" {
		t.Fatal("expected PNG with ihdrLen=14 to be rejected")
	}
	badBmp := testCreateTinyBmp1x1()
	badBmp[26] = 2 // planes=2
	if DetectSupportedImageMimeType(badBmp) != "" {
		t.Fatal("expected BMP with planes=2 to be rejected")
	}

	// 4. ProcessImage normalizes image/jpg; charset=... and sets omit messages on failure.
	jpgBytes := testDecodeB64(t, testTinyJpgB64)
	autoFalse := false
	resJpg := ProcessImage(jpgBytes, " IMAGE/JPG ; charset=binary ", &ProcessImageOptions{AutoResizeImages: &autoFalse})
	if !resJpg.Ok || resJpg.MimeType != "image/jpeg" {
		t.Fatalf("expected normalized image/jpeg, got %+v", resJpg)
	}
	badConv := ProcessImage([]byte("not-an-image"), "image/tiff", nil)
	if badConv.Ok || badConv.Message != "[Image omitted: could not be converted to a supported inline image format.]" {
		t.Fatalf("unexpected conversion fail message: %+v", badConv)
	}
	max1 := 1
	badResize := ProcessImage(testCreatePng(t, 200, 200), "image/png", &ProcessImageOptions{Resize: &ImageResizeOptions{MaxBytes: &max1}})
	if badResize.Ok || badResize.Message != "[Image omitted: could not be resized below the inline image size limit.]" {
		t.Fatalf("unexpected resize fail message: %+v", badResize)
	}

	// 5. NormalizeToolResultImages combines conversion + dimension hints into ONE TextContent block.
	largeBmp := encodeLargeTestBmp(t, 2400, 2400)
	normalized := NormalizeToolResultImages([]ToolResultContent{
		&ai.ImageContent{Type: "image", Data: base64.StdEncoding.EncodeToString(largeBmp), MimeType: "image/bmp"},
	}, nil)
	if len(normalized) != 2 {
		t.Fatalf("expected image + 1 combined hint block, got %d blocks", len(normalized))
	}
	hintBlock, ok := normalized[1].(*ai.TextContent)
	if !ok || !strings.Contains(hintBlock.Text, "[Image converted from image/bmp to image/png.]\n") || !strings.Contains(hintBlock.Text, "original 2400x2400") {
		t.Fatalf("expected combined multiline hint block, got %#v", normalized[1])
	}

	// 6. SyntaxHighlight: dash fallback, default theme, unmapped nested inheritance, literal non-span tags preserved.
	hl := RenderHighlightedHtml(
		`func<T> <span class="hljs-selector-tag">div</span> <span class="hljs-string">a<span class="hljs-unknown">b</span></span>`,
		HighlightTheme{
			"default":  func(s string) string { return "[def:" + s + "]" },
			"selector": func(s string) string { return "[sel:" + s + "]" },
			"string":   func(s string) string { return "[str:" + s + "]" },
		},
	)
	if hl != "[def:func<T> ][sel:div][def: ][str:a][str:b]" {
		t.Fatalf("RenderHighlightedHtml parity mismatch: %q", hl)
	}
}

func encodeLargeTestBmp(t *testing.T, w, h int) []byte {
	t.Helper()
	// Use binary BMP encoding cleanly only if raw image conversion needs valid BMP;
	// Wait: ProcessImage takes raw bytes and NormalizeToolResultImages takes base64!
	_ = t
	rowStride := ((w*3 + 3) / 4) * 4
	pixSize := rowStride * h
	buf := make([]byte, 54+pixSize)
	buf[0] = 'B'
	buf[1] = 'M'
	buf[2] = byte(len(buf))
	buf[3] = byte(len(buf) >> 8)
	buf[4] = byte(len(buf) >> 16)
	buf[5] = byte(len(buf) >> 24)
	buf[10] = 54
	buf[14] = 40
	buf[18] = byte(w)
	buf[19] = byte(w >> 8)
	buf[22] = byte(h)
	buf[23] = byte(h >> 8)
	buf[26] = 1
	buf[28] = 24
	return buf
}
