// Ported from packages/coding-agent/src/utils/image-convert.ts (pi v1.5.2).

package utils

import (
	"encoding/base64"

	"github.com/keejkrej/pi-go/tui"
)

// ConvertedImage holds base64-encoded PNG image bytes and its MIME type.
type ConvertedImage struct {
	Data     string
	MimeType string
}

// ConvertImageBytesToPng decodes arbitrary supported image bytes (PNG, JPEG, GIF, WebP, BMP),
// applies EXIF orientation if present, and encodes them as PNG bytes.
func ConvertImageBytesToPng(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	mime := DetectSupportedImageMimeType(data)
	img, err := PhotonImageFromBytes(data)
	if err != nil {
		return nil, err
	}
	defer img.Free()
	if mime != "" {
		img = ApplyExifOrientation(img, data, mime)
	}
	return img.GetBytes()
}

// ConvertToPng converts a base64-encoded image to PNG, applying EXIF orientation for JPEG/WebP.
// Returns the input unchanged when mimeType is already "image/png".
func ConvertToPng(base64Data, mimeType string) (*ConvertedImage, error) {
	if mimeType == "image/png" {
		return &ConvertedImage{Data: base64Data, MimeType: mimeType}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, err
	}
	img, err := PhotonImageFromBytes(raw)
	if err != nil {
		return nil, err
	}
	defer img.Free()
	img = ApplyExifOrientation(img, raw, mimeType)
	pngBytes, err := img.GetBytes()
	if err != nil {
		return nil, err
	}
	return &ConvertedImage{
		Data:     base64.StdEncoding.EncodeToString(pngBytes),
		MimeType: "image/png",
	}, nil
}

// LoadPngTranscoder returns a synchronous tui.ImageTranscoder that converts non-PNG
// base64 image payloads to EXIF-oriented base64 PNG strings, returning nil on failure.
func LoadPngTranscoder() tui.ImageTranscoder {
	if err := LoadPhoton(); err != nil {
		return nil
	}
	return func(base64Data, mimeType string) *string {
		if mimeType == "image/png" {
			return &base64Data
		}
		raw, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			return nil
		}
		img, err := PhotonImageFromBytes(raw)
		if err != nil {
			return nil
		}
		defer img.Free()
		img = ApplyExifOrientation(img, raw, mimeType)
		pngBytes, err := img.GetBytes()
		if err != nil {
			return nil
		}
		out := base64.StdEncoding.EncodeToString(pngBytes)
		return &out
	}
}

// EnsurePngTranscoder installs the PNG transcoder in pi-tui when the terminal uses
// the Kitty image protocol, which accepts only PNG images.
func EnsurePngTranscoder() {
	if tui.GetCapabilities().Images != tui.ImageProtocolKitty {
		return
	}
	if transcoder := LoadPngTranscoder(); transcoder != nil {
		tui.SetImageTranscoder(transcoder)
	}
}
