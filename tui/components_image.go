// Ported from packages/tui/src/components/image.ts (pi v1.0.0).

package tui

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
	cachedLines []string
	cachedWidth *int
}

// NewImage constructs an image component.
// options nil means the default options object. dimensions nil means the
// terminal-image probe, then 800 by 600 when that probe fails.
func NewImage(base64Data string, mimeType string, theme ImageTheme, options *ImageOptions, dimensions *ImageDimensions) *Image {
	panic("unported: NewImage")
}

// GetImageId is the Kitty image id used by this image, or nil when none is allocated.
func (img *Image) GetImageId() *int { return img.imageId }

func (img *Image) Invalidate() {
	panic("unported: Image.Invalidate")
}

func (img *Image) Render(width int) []string {
	panic("unported: Image.Render")
}

var _ Component = (*Image)(nil)
