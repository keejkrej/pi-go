// Ported from packages/tui/src/components/image.ts (pi v1.0.0).

package tui

import (
	"fmt"
	"math"
	"sync"

	"github.com/keejkrej/pi-go/internal/omap"
)

// ImageTranscoder converts base64 image data to base64 PNG data, or returns nil if it cannot.
// Called synchronously during rendering.
type ImageTranscoder func(base64Data string, mimeType string) *string

var (
	ciTranscoderMu sync.Mutex
	ciTranscoder   ImageTranscoder
	ciPngCache     = omap.NewMap[string, *string]()
)

// SetImageTranscoder registers the converter used for non-PNG images on Kitty-protocol terminals,
// which only accept PNG. Without one, such images render as text fallbacks.
func SetImageTranscoder(transcoder ImageTranscoder) {
	ciTranscoderMu.Lock()
	defer ciTranscoderMu.Unlock()
	ciTranscoder = transcoder
	ciPngCache = omap.NewMap[string, *string]()
}

func ciToPng(base64Data string, mimeType string) *string {
	ciTranscoderMu.Lock()
	transcoder := ciTranscoder
	if transcoder == nil {
		ciTranscoderMu.Unlock()
		return nil
	}
	cached, found := ciPngCache.Get(base64Data)
	ciTranscoderMu.Unlock()

	png := cached
	if !found {
		png = transcoder(base64Data, mimeType)
	}

	ciTranscoderMu.Lock()
	defer ciTranscoderMu.Unlock()
	if ciTranscoder == nil {
		return png
	}
	ciPngCache.Delete(base64Data)
	ciPngCache.Set(base64Data, png)
	if ciPngCache.Len() > 32 {
		for _, k := range ciPngCache.Keys() {
			ciPngCache.Delete(k)
			break
		}
	}
	return png
}

// ImageTheme colors the text fallback used when the terminal cannot draw an image.
type ImageTheme struct {
	FallbackColor func(str string) string
}

// ImageOptions sizes an image and optionally reuses a Kitty image id.
// Field order matches the ImageOptions interface.
type ImageOptions struct {
	MaxWidthCells  *int    `json:"maxWidthCells,omitzero"`
	MaxHeightCells *int    `json:"maxHeightCells,omitzero"`
	Filename       *string `json:"filename,omitzero"`
	ImageId        *int    `json:"imageId,omitzero"`
}

// Image renders inline terminal image content, or a one-line text fallback.
type Image struct {
	base64Data  string
	mimeType    string
	dimensions  ImageDimensions
	theme       ImageTheme
	options     ImageOptions
	imageId     *int
	pngData     *string
	cachedLines []string
	cachedWidth *int
}

// NewImage constructs an image component.
// options nil means the default options object. dimensions nil means the
// terminal-image probe, then 800 by 600 when that probe fails.
func NewImage(base64Data string, mimeType string, theme ImageTheme, options *ImageOptions, dimensions *ImageDimensions) *Image {
	opts := ImageOptions{}
	if options != nil {
		opts = *options
	}
	dims := ImageDimensions{WidthPx: 800, HeightPx: 600}
	if dimensions != nil {
		dims = *dimensions
	} else if probed := GetImageDimensions(base64Data, mimeType); probed != nil {
		dims = *probed
	}
	return &Image{
		base64Data: base64Data,
		mimeType:   mimeType,
		dimensions: dims,
		theme:      theme,
		options:    opts,
		imageId:    opts.ImageId,
	}
}

// GetImageId is the Kitty image id used by this image, or nil when none is allocated.
func (img *Image) GetImageId() *int { return img.imageId }

func (img *Image) Invalidate() {
	img.cachedLines = nil
	img.cachedWidth = nil
}

func (img *Image) Render(width int) []string {
	if img.cachedLines != nil && img.cachedWidth != nil && *img.cachedWidth == width {
		return img.cachedLines
	}

	maxWidthCap := 60
	if img.options.MaxWidthCells != nil {
		maxWidthCap = *img.options.MaxWidthCells
	}
	maxWidth := max(1, min(width-2, maxWidthCap))
	cellDims := GetCellDimensions()
	defaultMaxHeight := max(1, int(math.Ceil(float64(maxWidth)*float64(cellDims.WidthPx)/float64(cellDims.HeightPx))))
	maxHeight := defaultMaxHeight
	if img.options.MaxHeightCells != nil {
		maxHeight = *img.options.MaxHeightCells
	}

	caps := GetCapabilities()
	data := img.base64Data
	hasData := data != ""
	dimensions := img.dimensions
	if caps.Images == ImageProtocolKitty && img.mimeType != "image/png" {
		if img.pngData == nil {
			img.pngData = ciToPng(img.base64Data, img.mimeType)
		}
		if img.pngData != nil {
			data = *img.pngData
			hasData = data != ""
			if pngDims := GetPngDimensions(data); pngDims != nil {
				dimensions = *pngDims
			}
		} else {
			data = ""
			hasData = false
		}
	}

	var lines []string
	var result *RenderImageResult
	if caps.Images != "" && hasData {
		if caps.Images == ImageProtocolKitty && img.imageId == nil {
			id := AllocateImageId()
			img.imageId = &id
		}
		moveCursor := false
		result = RenderImage(data, dimensions, &ImageRenderOptions{
			MaxWidthCells:  &maxWidth,
			MaxHeightCells: &maxHeight,
			ImageId:        img.imageId,
			MoveCursor:     &moveCursor,
		})
	}

	if result != nil {
		if result.ImageId != nil && *result.ImageId != 0 {
			id := *result.ImageId
			img.imageId = &id
		}
		if caps.Images == ImageProtocolKitty {
			lines = make([]string, 0, max(1, result.Rows))
			lines = append(lines, result.Sequence)
			for i := 0; i < result.Rows-1; i++ {
				lines = append(lines, "")
			}
		} else {
			lines = make([]string, 0, max(1, result.Rows))
			for i := 0; i < result.Rows-1; i++ {
				lines = append(lines, "")
			}
			rowOffset := result.Rows - 1
			moveUp := ""
			if rowOffset > 0 {
				moveUp = fmt.Sprintf("\x1b[%dA", rowOffset)
			}
			lines = append(lines, moveUp+result.Sequence)
		}
	} else {
		fallback := ImageFallback(img.mimeType, &img.dimensions, img.options.Filename)
		if img.theme.FallbackColor != nil {
			fallback = img.theme.FallbackColor(fallback)
		}
		lines = []string{TruncateToWidth(fallback, width, "...", false)}
	}

	img.cachedLines = lines
	w := width
	img.cachedWidth = &w
	return lines
}

var _ Component = (*Image)(nil)
