// Ported from packages/coding-agent/src/utils/image-resize-core.ts (pi v1.5.2).

package utils

import (
	"encoding/base64"
	"math"

	"github.com/keejkrej/pi-go/ai"
)

// ImageResizeOptions aliases the AI model resize constraints (dimensions, base64 cap, quality).
type ImageResizeOptions = ai.ModelImageResizeOptions

// Default image resize limits matching Anthropic / multimodal provider limits.
const (
	DefaultImageMaxWidth    = 2000
	DefaultImageMaxHeight   = 2000
	DefaultImageMaxBytes    = int(4.5 * 1024 * 1024) // 4.5 MiB base64 payload cap
	DefaultImageJpegQuality = 80
)

// ResizedImage describes the output of ResizeImage / ResizeImageInProcess.
type ResizedImage struct {
	Data           string `json:"data"`
	MimeType       string `json:"mimeType"`
	OriginalWidth  int    `json:"originalWidth"`
	OriginalHeight int    `json:"originalHeight"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	WasResized     bool   `json:"wasResized"`
}

func ircResolveOptions(options *ImageResizeOptions) (maxWidth, maxHeight, maxBytes, jpegQuality int) {
	maxWidth = DefaultImageMaxWidth
	maxHeight = DefaultImageMaxHeight
	maxBytes = DefaultImageMaxBytes
	jpegQuality = DefaultImageJpegQuality
	if options == nil {
		return
	}
	if options.MaxWidth != nil && *options.MaxWidth > 0 {
		maxWidth = *options.MaxWidth
	}
	if options.MaxHeight != nil && *options.MaxHeight > 0 {
		maxHeight = *options.MaxHeight
	}
	if options.MaxBytes != nil && *options.MaxBytes > 0 {
		maxBytes = *options.MaxBytes
	}
	if options.JpegQuality != nil && *options.JpegQuality > 0 {
		jpegQuality = *options.JpegQuality
	}
	return
}

func ircPickSmaller(pngBytes, jpegBytes []byte) ([]byte, string) {
	if len(pngBytes) > 0 && (len(jpegBytes) == 0 || len(pngBytes) <= len(jpegBytes)) {
		return pngBytes, "image/png"
	}
	return jpegBytes, "image/jpeg"
}

func ircFitsMaxBytes(rawLen, maxBytes int) bool {
	return base64.StdEncoding.EncodedLen(rawLen) <= maxBytes
}

// ResizeImageInProcess decodes image bytes, applies EXIF orientation if needed, and
// scales/re-encodes when dimensions or base64 payload size exceed the limits.
// Returns (nil, nil) if the image cannot be reduced below MaxBytes; returns an error
// if the input cannot be decoded.
func ResizeImageInProcess(data []byte, mimeType string, options *ImageResizeOptions) (*ResizedImage, error) {
	maxWidth, maxHeight, maxBytes, jpegQuality := ircResolveOptions(options)

	if err := LoadPhoton(); err != nil {
		// Without an imaging backend, pass through original image as best effort.
		return &ResizedImage{
			Data:     base64.StdEncoding.EncodeToString(data),
			MimeType: mimeType,
		}, nil
	}

	img, err := PhotonImageFromBytes(data)
	if err != nil {
		return nil, err
	}
	defer img.Free()

	oriented := ApplyExifOrientation(img, data, mimeType)
	origW := oriented.Width()
	origH := oriented.Height()
	orientationChanged := oriented != img

	// When already within all limits (and orientation needed no correction), return original bytes untouched.
	if !orientationChanged && origW <= maxWidth && origH <= maxHeight && ircFitsMaxBytes(len(data), maxBytes) {
		return &ResizedImage{
			Data:           base64.StdEncoding.EncodeToString(data),
			MimeType:       mimeType,
			OriginalWidth:  origW,
			OriginalHeight: origH,
			Width:          origW,
			Height:         origH,
			WasResized:     false,
		}, nil
	}

	// Calculate aspect-preserving target dimensions bounded by maxWidth x maxHeight.
	targetW := origW
	targetH := origH
	if targetW > maxWidth {
		targetH = max(1, int(math.Round(float64(targetH)*float64(maxWidth)/float64(targetW))))
		targetW = maxWidth
	}
	if targetH > maxHeight {
		targetW = max(1, int(math.Round(float64(targetW)*float64(maxHeight)/float64(targetH))))
		targetH = maxHeight
	}

	tryEncode := func(w, h int) ([]byte, string, bool) {
		var working *PhotonImage
		if w == origW && h == origH {
			working = oriented
		} else {
			working = oriented.Resize(w, h)
		}

		pngBytes, pngErr := working.GetBytes()
		jpegBytes, jpegErr := working.GetBytesJpeg(jpegQuality)
		if pngErr != nil && jpegErr != nil {
			return nil, "", false
		}
		if pngErr != nil {
			pngBytes = nil
		}
		if jpegErr != nil {
			jpegBytes = nil
		}

		bestBytes, bestMime := ircPickSmaller(pngBytes, jpegBytes)
		if len(bestBytes) > 0 && ircFitsMaxBytes(len(bestBytes), maxBytes) {
			return bestBytes, bestMime, true
		}

		// Try lower JPEG qualities when initial encoding exceeds maxBytes.
		for _, q := range []int{60, 40} {
			if q >= jpegQuality {
				continue
			}
			if qBytes, qErr := working.GetBytesJpeg(q); qErr == nil && ircFitsMaxBytes(len(qBytes), maxBytes) {
				return qBytes, "image/jpeg", true
			}
		}
		return bestBytes, bestMime, false
	}

	if bestBytes, bestMime, ok := tryEncode(targetW, targetH); ok {
		return &ResizedImage{
			Data:           base64.StdEncoding.EncodeToString(bestBytes),
			MimeType:       bestMime,
			OriginalWidth:  origW,
			OriginalHeight: origH,
			Width:          targetW,
			Height:         targetH,
			WasResized:     targetW != origW || targetH != origH || !ircFitsMaxBytes(len(data), maxBytes) || orientationChanged,
		}, nil
	}

	// Progressively reduce scale when byte budget is still exceeded.
	for _, scale := range []float64{0.75, 0.5, 0.35, 0.25, 0.15, 0.1} {
		w := max(1, int(math.Round(float64(targetW)*scale)))
		h := max(1, int(math.Round(float64(targetH)*scale)))
		if bestBytes, bestMime, ok := tryEncode(w, h); ok {
			return &ResizedImage{
				Data:           base64.StdEncoding.EncodeToString(bestBytes),
				MimeType:       bestMime,
				OriginalWidth:  origW,
				OriginalHeight: origH,
				Width:          w,
				Height:         h,
				WasResized:     true,
			}, nil
		}
	}

	return nil, nil
}
