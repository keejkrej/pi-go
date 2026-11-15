// Ported from packages/coding-agent/src/utils/image-resize.ts (pi v1.5.2).

package utils

import (
	"fmt"
	"sync"
)

var (
	irPendingMu sync.Mutex
)

// ResizeImage resizes image bytes to fit dimension and base64 size constraints.
// Returns (nil, nil) when the image cannot be reduced below MaxBytes; returns an error
// when the input bytes cannot be decoded.
func ResizeImage(data []byte, mimeType string, options *ImageResizeOptions) (*ResizedImage, error) {
	// Run inside a dedicated goroutine and await full completion before returning, matching
	// the TS worker lifecycle guarantee so serial callers never overlap teardown.
	irPendingMu.Lock()
	defer irPendingMu.Unlock()

	ch := make(chan ImageResizeWorkerResponse, 1)
	go func() {
		ch <- RunImageResizeWorker(ImageResizeWorkerRequest{
			Bytes:    data,
			MimeType: mimeType,
			Options:  options,
		})
	}()
	resp := <-ch
	return resp.Result, resp.Error
}

// FormatDimensionNote returns a coordinate-scaling note for resized images, or nil when
// the image was not resized.
func FormatDimensionNote(result ResizedImage) *string {
	if !result.WasResized || result.Width <= 0 || result.Height <= 0 {
		return nil
	}
	scale := float64(result.OriginalWidth) / float64(result.Width)
	note := fmt.Sprintf(
		"[Image: original %dx%d, displayed at %dx%d. Multiply coordinates by %.2f to map to original image.]",
		result.OriginalWidth,
		result.OriginalHeight,
		result.Width,
		result.Height,
		scale,
	)
	return &note
}
