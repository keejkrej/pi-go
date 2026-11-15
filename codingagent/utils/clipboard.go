// Ported from packages/coding-agent/src/utils/clipboard.ts (pi v1.5.2).

package utils

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/tui"
)

const clipMaxOsc52EncodedLength = 100_000

var (
	clipRunCommand      = RunClipboardCommand
	clipNativeClipboard = tui.GetNativeClipboard
	clipPlatform        = js.Platform
	clipWriteStdout     = func(seq string) {
		_, _ = os.Stdout.WriteString(seq)
	}
)

func clipIsRemoteSession() bool {
	return os.Getenv("SSH_CONNECTION") != "" ||
		os.Getenv("SSH_CLIENT") != "" ||
		os.Getenv("MOSH_CONNECTION") != "" ||
		os.Getenv("PI_CLIPBOARD_OSC52") == "1"
}

func clipEmitOsc52(text string) bool {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if len(encoded) > clipMaxOsc52EncodedLength {
		return false
	}
	clipWriteStdout("\x1b]52;c;" + encoded + "\x07")
	return true
}

func clipRandomHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clipCopyViaWindowsClipboard(text string) bool {
	tmpFile := filepath.Join(os.TempDir(), "pi-wsl-clip-"+clipRandomHex()+".txt")
	if err := os.WriteFile(tmpFile, []byte(text), 0o600); err != nil {
		return false
	}
	defer func() {
		_ = os.Remove(tmpFile)
	}()

	winBytes, ok := clipRunCommand("wslpath", []string{"-w", tmpFile}, &ClipboardCommandOptions{TimeoutMs: 1000})
	if !ok {
		return false
	}
	winPath := js.Trim(string(winBytes))
	if winPath == "" {
		return false
	}
	quoted := strings.ReplaceAll(winPath, "'", "''")
	script := fmt.Sprintf("Set-Clipboard -Value ([System.IO.File]::ReadAllText('%s', [System.Text.Encoding]::UTF8))", quoted)
	_, ok = clipRunCommand("powershell.exe", []string{"-NoProfile", "-Command", script}, &ClipboardCommandOptions{TimeoutMs: 5000})
	return ok
}

type clipCommandSpec struct {
	command string
	args    []string
}

// ReadClipboardText reads plain text from the system clipboard, returning nil if empty or unavailable.
func ReadClipboardText() *string {
	var commands []clipCommandSpec
	if os.Getenv("TERMUX_VERSION") != "" {
		commands = append(commands, clipCommandSpec{command: "termux-clipboard-get"})
	}
	if clipPlatform() == "linux" {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, clipCommandSpec{
				command: "wl-paste",
				args:    []string{"--no-newline", "--type", "text"},
			})
		}
		if os.Getenv("DISPLAY") != "" {
			commands = append(commands,
				clipCommandSpec{command: "xclip", args: []string{"-selection", "clipboard", "-out"}},
				clipCommandSpec{command: "xsel", args: []string{"--clipboard", "--output"}},
			)
		}
	}

	for _, c := range commands {
		bytes, ok := clipRunCommand(c.command, c.args, &ClipboardCommandOptions{TimeoutMs: 5000})
		if ok {
			if len(bytes) == 0 {
				return nil
			}
			s := string(bytes)
			return &s
		}
	}

	cb := clipNativeClipboard()
	if cb == nil {
		return nil
	}
	text, ok, err := cb.GetText()
	if err != nil || !ok || text == nil || *text == "" {
		return nil
	}
	return text
}

// ReadClipboardFilePaths reads file paths (such as Finder file copies) from the native clipboard,
// propagating any native clipboard read error.
func ReadClipboardFilePaths() ([]string, error) {
	cb := clipNativeClipboard()
	if cb == nil {
		return nil, nil
	}
	fp, ok := cb.(tui.NativeClipboardFilePaths)
	if !ok {
		return nil, nil
	}
	paths, ok, err := fp.GetFilePaths()
	if err != nil {
		return nil, err
	}
	if !ok || len(paths) == 0 {
		return nil, nil
	}
	return paths, nil
}

// CopyToClipboard writes text to the system clipboard and/or OSC 52 terminal escape sequence.
func CopyToClipboard(text string) error {
	p := clipPlatform()
	copied := false

	if p != "linux" {
		if cb := clipNativeClipboard(); cb != nil {
			if setter, ok := cb.(tui.NativeClipboardSetText); ok {
				if err := setter.SetText(text); err == nil {
					copied = true
				}
			}
		}
	}

	if !copied {
		var commands []clipCommandSpec
		switch p {
		case "darwin":
			commands = append(commands, clipCommandSpec{command: "pbcopy"})
		case "win32":
			commands = append(commands, clipCommandSpec{command: "clip"})
		default:
			if os.Getenv("TERMUX_VERSION") != "" {
				commands = append(commands, clipCommandSpec{command: "termux-clipboard-set"})
			}
			if os.Getenv("WAYLAND_DISPLAY") != "" {
				commands = append(commands, clipCommandSpec{command: "wl-copy"})
			}
			if os.Getenv("DISPLAY") != "" {
				commands = append(commands,
					clipCommandSpec{command: "xclip", args: []string{"-selection", "clipboard"}},
					clipCommandSpec{command: "xsel", args: []string{"--clipboard", "--input"}},
				)
			}
		}

		input := text
		for _, c := range commands {
			if _, ok := clipRunCommand(c.command, c.args, &ClipboardCommandOptions{Input: &input, TimeoutMs: 5000}); ok {
				copied = true
				break
			}
		}
	}

	osc52Emitted := false
	if !copied && p == "linux" && IsWSL() {
		if os.Getenv("WT_SESSION") != "" {
			osc52Emitted = clipEmitOsc52(text)
		}
		if osc52Emitted || clipCopyViaWindowsClipboard(text) {
			copied = true
		}
	}

	headless := p == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("TERMUX_VERSION") == ""
	oversized := false
	if !osc52Emitted && (clipIsRemoteSession() || (!copied && headless)) {
		if clipEmitOsc52(text) {
			copied = true
		} else {
			oversized = true
		}
	}

	if copied {
		return nil
	}
	if oversized {
		return errors.New("Clipboard unavailable: text exceeds the OSC 52 size limit")
	}
	if os.Getenv("TERMUX_VERSION") != "" {
		return errors.New("Clipboard unavailable: install the Termux:API app and `termux-api` package")
	}
	if p == "linux" {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return errors.New("Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access")
		}
		if os.Getenv("DISPLAY") != "" {
			return errors.New("Clipboard unavailable: install `xclip` or `xsel`, or check X11 access")
		}
	}
	return errors.New("Clipboard unavailable")
}
