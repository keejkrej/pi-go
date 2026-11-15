package utils

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

type clipMockNative struct {
	text       *string
	textOk     bool
	textErr    error
	image      []byte
	imageOk    bool
	imageErr   error
	filePaths  []string
	filesOk    bool
	filesErr   error
	setErr     error
	copiedText *string
}

func (m *clipMockNative) GetText() (*string, bool, error) { return m.text, m.textOk, m.textErr }
func (m *clipMockNative) GetImage() ([]byte, bool, error) { return m.image, m.imageOk, m.imageErr }
func (m *clipMockNative) GetFilePaths() ([]string, bool, error) {
	return m.filePaths, m.filesOk, m.filesErr
}
func (m *clipMockNative) SetText(text string) error {
	m.copiedText = &text
	return m.setErr
}

func testCreateTinyBmp1x1() []byte {
	buf := make([]byte, 58)
	buf[0] = 'B'
	buf[1] = 'M'
	binary.LittleEndian.PutUint32(buf[2:6], uint32(len(buf)))
	binary.LittleEndian.PutUint32(buf[10:14], 54)
	binary.LittleEndian.PutUint32(buf[14:18], 40)
	binary.LittleEndian.PutUint32(buf[18:22], 1)
	binary.LittleEndian.PutUint32(buf[22:26], 1)
	binary.LittleEndian.PutUint16(buf[26:28], 1)
	binary.LittleEndian.PutUint16(buf[28:30], 24)
	binary.LittleEndian.PutUint32(buf[34:38], 4)
	buf[56] = 0xff
	return buf
}

func TestClipboardCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command test")
	}
	// Runs real binary reading stdout when no input is given, and detaches stdout when input is piped.
	out, ok := RunClipboardCommand("printf", []string{"hello clipboard"}, &ClipboardCommandOptions{TimeoutMs: 2000})
	if !ok || string(out) != "hello clipboard" {
		t.Fatalf("RunClipboardCommand(printf) = (%q, %v)", string(out), ok)
	}
	input := "hello clipboard"
	out, ok = RunClipboardCommand("cat", nil, &ClipboardCommandOptions{Input: &input, TimeoutMs: 2000})
	if !ok || len(out) != 0 {
		t.Fatalf("RunClipboardCommand(cat with Input) = (%q, %v)", string(out), ok)
	}

	// Missing binary returns false without panic.
	if out, ok := RunClipboardCommand("nonexistent-clip-cmd-xyz", nil, nil); ok || len(out) != 0 {
		t.Fatalf("expected false for missing command, got (%q, %v)", string(out), ok)
	}
}

func TestClipboardTextFilePathsAndCopy(t *testing.T) {
	origPlat := clipPlatform
	origRun := clipRunCommand
	origNative := clipNativeClipboard
	origStdout := clipWriteStdout
	t.Cleanup(func() {
		clipPlatform = origPlat
		clipRunCommand = origRun
		clipNativeClipboard = origNative
		clipWriteStdout = origStdout
	})

	var oscOut bytes.Buffer
	clipWriteStdout = func(seq string) { oscOut.WriteString(seq) }

	// Native text & file paths
	expected := "native text"
	mock := &clipMockNative{
		text:      &expected,
		textOk:    true,
		filePaths: []string{"/tmp/a.png", "/tmp/b.png"},
		filesOk:   true,
	}
	clipPlatform = func() string { return "darwin" }
	clipNativeClipboard = func() tui.NativeClipboard { return mock }

	if got := ReadClipboardText(); got == nil || *got != expected {
		t.Fatalf("ReadClipboardText = %v", got)
	}
	paths, err := ReadClipboardFilePaths()
	if err != nil || len(paths) != 2 || paths[0] != "/tmp/a.png" {
		t.Fatalf("ReadClipboardFilePaths = (%v, %v)", paths, err)
	}

	// Propagates native file path errors without swallowing (#clipboard-paste-file-paths).
	mockErr := &clipMockNative{filesErr: errors.New("Native clipboard file read failed")}
	clipNativeClipboard = func() tui.NativeClipboard { return mockErr }
	if _, err := ReadClipboardFilePaths(); err == nil || !strings.Contains(err.Error(), "Native clipboard file read failed") {
		t.Fatalf("expected native file path error, got %v", err)
	}

	// CopyToClipboard on native darwin succeeds via SetText.
	clipNativeClipboard = func() tui.NativeClipboard { return mock }
	if err := CopyToClipboard("copied-value"); err != nil {
		t.Fatalf("CopyToClipboard failed: %v", err)
	}
	if mock.copiedText == nil || *mock.copiedText != "copied-value" {
		t.Fatalf("expected SetText called, got %v", mock.copiedText)
	}
}

func TestClipboardImageAndNativeErrorsAndBmpConversion(t *testing.T) {
	origPlat := clipPlatform
	origRun := clipRunCommand
	origNative := clipNativeClipboard
	origEnv := ciGetenv
	origWsl := ciIsWSL
	t.Cleanup(func() {
		clipPlatform = origPlat
		ciPlatform = func() string { return runtime.GOOS }
		ciRunCommand = origRun
		ciNativeClipboard = origNative
		ciGetenv = origEnv
		ciIsWSL = origWsl
	})

	// 1. Native errors must propagate on darwin, linux, and windows without falling back.
	nativeErr := errors.New("native image read rejected")
	for _, plat := range []string{"darwin", "linux", "windows"} {
		ciPlatform = func() string { return plat }
		ciGetenv = func(string) string { return "" }
		ciNativeClipboard = func() tui.NativeClipboard { return &clipMockNative{imageErr: nativeErr} }
		if _, err := ReadClipboardImage(); !errors.Is(err, nativeErr) {
			t.Fatalf("platform %s: expected native error propagation, got %v", plat, err)
		}
	}

	// 2. BMP bytes returned by native clipboard are converted to PNG.
	bmp := testCreateTinyBmp1x1()
	ciPlatform = func() string { return "darwin" }
	ciNativeClipboard = func() tui.NativeClipboard {
		return &clipMockNative{image: bmp, imageOk: true}
	}
	img, err := ReadClipboardImage()
	if err != nil || img == nil {
		t.Fatalf("expected BMP->PNG conversion, got img=%v err=%v", img, err)
	}
	if img.MimeType != "image/png" || len(img.Bytes) < 8 || img.Bytes[0] != 0x89 || img.Bytes[1] != 'P' {
		t.Fatalf("expected PNG header after BMP conversion, got mime=%q len=%d", img.MimeType, len(img.Bytes))
	}

	// 3. Wayland wl-paste selects image/png and reads raw PNG.
	ciPlatform = func() string { return "linux" }
	ciGetenv = func(k string) string {
		if k == "XDG_SESSION_TYPE" {
			return "wayland"
		}
		return ""
	}
	ciNativeClipboard = func() tui.NativeClipboard { return nil }
	ciRunCommand = func(cmd string, args []string, opts *ClipboardCommandOptions) ([]byte, bool) {
		if cmd == "wl-paste" && len(args) > 0 && args[0] == "--list-types" {
			return []byte("text/plain\nimage/jpeg\nimage/png\n"), true
		}
		if cmd == "wl-paste" && len(args) >= 2 && args[1] == "image/png" {
			return img.Bytes, true
		}
		return nil, false
	}
	wlImg, err := ReadClipboardImage()
	if err != nil || wlImg == nil || wlImg.MimeType != "image/png" {
		t.Fatalf("expected Wayland image/png, got img=%+v err=%v", wlImg, err)
	}

	if ExtensionForImageMimeType("image/jpeg") != "jpg" || ExtensionForImageMimeType("image/webp") != "webp" {
		t.Fatal("unexpected extension mapping")
	}
}
