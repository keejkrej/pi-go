// Ported from packages/coding-agent/src/utils/clipboard-image.ts (pi v1.5.2).

package utils

import (
	"encoding/base64"
	"os"
	"runtime"
	"strings"

	"github.com/keejkrej/pi-go/tui"
)

// ClipboardImage holds raw image bytes and their MIME type read from the system clipboard.
type ClipboardImage struct {
	Bytes    []byte
	MimeType string
}

// ExtensionForImageMimeType returns the file extension (without leading dot) for a
// supported image MIME type, defaulting to "png".
func ExtensionForImageMimeType(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

// SupportedImageMimeTypes lists the image MIME types accepted directly without format conversion.
var SupportedImageMimeTypes = []string{
	"image/png",
	"image/jpeg",
	"image/webp",
	"image/gif",
}

// Test hooks scoped to clipboard_image.go.
var (
	ciPlatform          = func() string { return runtime.GOOS }
	ciGetenv            = os.Getenv
	ciIsWSL             = func() bool { return IsWSL() }
	ciRunCommand        = RunClipboardCommand
	ciNativeClipboard   = tui.GetNativeClipboard
	ciConvertBytesToPng = ConvertImageBytesToPng
)

// IsWaylandSession reports whether the process environment is running inside a Wayland session.
func IsWaylandSession(env ...map[string]string) bool {
	var lookup func(string) string
	if len(env) > 0 && env[0] != nil {
		m := env[0]
		lookup = func(k string) string { return m[k] }
	} else {
		lookup = ciGetenv
	}
	return lookup("XDG_SESSION_TYPE") == "wayland" || lookup("WAYLAND_DISPLAY") != ""
}

func ciIsWindowsPlatform(plat string) bool {
	return plat == "windows" || plat == "win32"
}

func ciIsSupportedImageMime(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func ciNormalizeMimeToken(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if idx := strings.IndexByte(trimmed, ';'); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return strings.ToLower(trimmed)
}

func ciSelectImageMimeType(candidates []string) (rawType, normalized string) {
	// Prefer supported image types in canonical order.
	for _, preferred := range SupportedImageMimeTypes {
		for _, c := range candidates {
			norm := ciNormalizeMimeToken(c)
			if norm == preferred || (preferred == "image/jpeg" && norm == "image/jpg") {
				return strings.TrimSpace(c), preferred
			}
		}
	}
	// Fall back to other raster image types (e.g. image/bmp, image/tiff) for PNG conversion.
	for _, c := range candidates {
		norm := ciNormalizeMimeToken(c)
		if strings.HasPrefix(norm, "image/") && norm != "image/svg+xml" {
			return strings.TrimSpace(c), norm
		}
	}
	return "", ""
}

func ciFinalizeImage(data []byte) (*ClipboardImage, error) {
	if len(data) == 0 {
		return nil, nil
	}
	detected := DetectSupportedImageMimeType(data)
	if ciIsSupportedImageMime(detected) {
		return &ClipboardImage{Bytes: data, MimeType: detected}, nil
	}
	// Non-supported formats such as BMP or TIFF are converted to PNG.
	pngBytes, err := ciConvertBytesToPng(data)
	if err != nil || len(pngBytes) == 0 {
		return nil, nil
	}
	return &ClipboardImage{Bytes: pngBytes, MimeType: "image/png"}, nil
}

const (
	ciDefaultListTimeoutMs       = 1000
	ciDefaultPowerShellTimeoutMs = 5000
)

func ciReadViaCommandTypes(listCmd string, listArgs []string, readCmd string, readArgs func(rawMime string) []string) (*ClipboardImage, bool, error) {
	out, ok := ciRunCommand(listCmd, listArgs, &ClipboardCommandOptions{TimeoutMs: ciDefaultListTimeoutMs})
	if !ok {
		return nil, false, nil
	}
	if len(out) == 0 {
		return nil, true, nil
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	rawMime, _ := ciSelectImageMimeType(lines)
	if rawMime == "" {
		return nil, true, nil
	}
	data, ok := ciRunCommand(readCmd, readArgs(rawMime), nil)
	if !ok || len(data) == 0 {
		return nil, true, nil
	}
	img, err := ciFinalizeImage(data)
	return img, true, err
}

const ciPowerShellImageScript = `Add-Type -AssemblyName System.Windows.Forms
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -ne $null) {
	$ms = New-Object System.IO.MemoryStream
	$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
	[Convert]::ToBase64String($ms.ToArray())
}`

func ciReadViaPowerShell(executable string) (*ClipboardImage, error) {
	out, ok := ciRunCommand(executable, []string{"-NoProfile", "-Command", ciPowerShellImageScript}, &ClipboardCommandOptions{TimeoutMs: ciDefaultPowerShellTimeoutMs})
	if !ok || len(out) == 0 {
		return nil, nil
	}
	b64 := strings.TrimSpace(string(out))
	if b64 == "" {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	return ciFinalizeImage(data)
}

// ReadClipboardImage reads an image from the system clipboard, converting non-LLM formats
// (such as BMP) to PNG when possible. Returns (nil, nil) when the clipboard contains no image,
// and propagates errors from the native clipboard backend.
func ReadClipboardImage() (*ClipboardImage, error) {
	plat := ciPlatform()

	if plat == "darwin" || ciIsWindowsPlatform(plat) {
		if native := ciNativeClipboard(); native != nil {
			data, ok, err := native.GetImage()
			if err != nil {
				return nil, err
			}
			if ok {
				return ciFinalizeImage(data)
			}
		}
		if ciIsWindowsPlatform(plat) {
			return ciReadViaPowerShell("powershell")
		}
		return nil, nil
	}

	if plat == "linux" {
		if ciGetenv("TERMUX_VERSION") != "" {
			return nil, nil
		}

		var (
			img     *ClipboardImage
			handled bool
			err     error
		)

		if IsWaylandSession() {
			img, handled, err = ciReadViaCommandTypes(
				"wl-paste",
				[]string{"--list-types"},
				"wl-paste",
				func(rawMime string) []string { return []string{"--type", rawMime, "--no-newline"} },
			)
			if err != nil {
				return nil, err
			}
			if img != nil {
				return img, nil
			}
		}

		if !handled {
			img, handled, err = ciReadViaCommandTypes(
				"xclip",
				[]string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
				"xclip",
				func(rawMime string) []string { return []string{"-selection", "clipboard", "-t", rawMime, "-o"} },
			)
			if err != nil {
				return nil, err
			}
			if img != nil {
				return img, nil
			}
		}

		if ciIsWSL() {
			psImg, err := ciReadViaPowerShell("powershell.exe")
			if err != nil {
				return nil, err
			}
			if psImg != nil {
				return psImg, nil
			}
		}

		if !handled {
			if native := ciNativeClipboard(); native != nil {
				data, ok, err := native.GetImage()
				if err != nil {
					return nil, err
				}
				if ok && len(data) > 0 {
					return ciFinalizeImage(data)
				}
			}
		}
	}

	return nil, nil
}
