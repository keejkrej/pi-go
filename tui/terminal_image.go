// Ported from packages/tui/src/terminal-image.ts (pi v1.0.0).

package tui

import "github.com/keejkrej/pi-go/internal/omap"

// ImageProtocol is the inline image protocol.
// The zero value is TS null (no image protocol), matching TerminalCapabilities.Images.
type ImageProtocol string

const (
	ImageProtocolKitty  ImageProtocol = "kitty"
	ImageProtocolIterm2 ImageProtocol = "iterm2"
)

// TerminalCapabilities is what the attached terminal can render.
// Images empty means no image protocol (TS null).
type TerminalCapabilities struct {
	Images     ImageProtocol `json:"images"`
	TrueColor  bool          `json:"trueColor"`
	Hyperlinks bool          `json:"hyperlinks"`
}

// TerminalCapabilityOverrides is Partial<TerminalCapabilities>.
// A nil field is absent. Images pointing at ImageProtocol("") forces images to null.
type TerminalCapabilityOverrides struct {
	Images     *ImageProtocol `json:"images,omitzero"`
	TrueColor  *bool          `json:"trueColor,omitzero"`
	Hyperlinks *bool          `json:"hyperlinks,omitzero"`
}

// CellDimensions is the pixel size of one terminal cell.
type CellDimensions struct {
	WidthPx  int `json:"widthPx"`
	HeightPx int `json:"heightPx"`
}

// ImageDimensions is the pixel size of an image.
type ImageDimensions struct {
	WidthPx  int `json:"widthPx"`
	HeightPx int `json:"heightPx"`
}

// ImageRenderOptions sizes an inline image.
// A nil *ImageRenderOptions means the TypeScript default {}.
type ImageRenderOptions struct {
	// MaxWidthCells defaults to 80 inside RenderImage when nil.
	MaxWidthCells *int `json:"maxWidthCells,omitzero"`
	// MaxHeightCells nil means no explicit row cap.
	MaxHeightCells *int `json:"maxHeightCells,omitzero"`
	// PreserveAspectRatio nil means true for iTerm2.
	PreserveAspectRatio *bool `json:"preserveAspectRatio,omitzero"`
	// ImageId is a Kitty image id. When set, the placement reuses that id.
	ImageId *int `json:"imageId,omitzero"`
	// MoveCursor nil means Kitty's default cursor movement. False suppresses it.
	MoveCursor *bool `json:"moveCursor,omitzero"`
}

// tiCachedCapabilities is the memo of GetCapabilities. Nil means not yet detected.
var tiCachedCapabilities *TerminalCapabilities

// tiCapabilityOverrides is applied on top of environment detection.
var tiCapabilityOverrides TerminalCapabilityOverrides

// tiCellDimensions is the cell size, updated when the terminal answers a size query.
// The TypeScript default is 9 by 18.
var tiCellDimensions = CellDimensions{WidthPx: 9, HeightPx: 18}

// GetCellDimensions returns the current cell size.
func GetCellDimensions() CellDimensions { return tiCellDimensions }

// SetCellDimensions replaces the current cell size.
func SetCellDimensions(dims CellDimensions) { tiCellDimensions = dims }

// tiProbeTmuxHyperlinks reports whether the attached tmux client forwards OSC 8 hyperlinks.
// On any error it returns false.
func tiProbeTmuxHyperlinks() bool {
	panic("unported: tiProbeTmuxHyperlinks")
}

func tiDetectCapabilitiesFromEnvironment(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	panic("unported: tiDetectCapabilitiesFromEnvironment")
}

// tiParseBooleanCapabilityOverride parses "1" and "0". Nil means any other value, including empty.
func tiParseBooleanCapabilityOverride(value *string) *bool {
	panic("unported: tiParseBooleanCapabilityOverride")
}

// DetectCapabilities reads the environment and an optional tmux hyperlink probe.
// tmuxForwardsHyperlink nil means tiProbeTmuxHyperlinks, the TypeScript default.
func DetectCapabilities(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	panic("unported: DetectCapabilities")
}

// GetCapabilities returns the cached capabilities, detecting them on first use.
func GetCapabilities() TerminalCapabilities {
	panic("unported: GetCapabilities")
}

// GetTerminalColorMode returns truecolor when capabilities report true color, otherwise 256color.
// capabilities nil means GetCapabilities(), the TypeScript default.
func GetTerminalColorMode(capabilities *TerminalCapabilities) TerminalColorMode {
	panic("unported: GetTerminalColorMode")
}

// ResetCapabilitiesCache forgets the cached detection result.
func ResetCapabilitiesCache() { tiCachedCapabilities = nil }

// SetCapabilityOverrides replaces the selected auto-detected capabilities.
// overrides nil means an empty partial. Identical overrides leave the cache alone.
func SetCapabilityOverrides(overrides *TerminalCapabilityOverrides) {
	panic("unported: SetCapabilityOverrides")
}

// SetCapabilities replaces the cached capabilities. Tests use this to force both code paths.
func SetCapabilities(caps TerminalCapabilities) { tiCachedCapabilities = &caps }

const (
	tiKittyPrefix    = "\x1b_G"
	tiIterm2Prefix   = "\x1b]1337;File="
	tiKittyChunkSize = 4096
)

// IsImageLine reports whether line contains a Kitty or iTerm2 inline-image sequence.
func IsImageLine(line string) bool {
	panic("unported: IsImageLine")
}

// AllocateImageId returns a random Kitty image id in [1, 0xffffffff].
func AllocateImageId() int {
	panic("unported: AllocateImageId")
}

// EncodeKittyOptions is the options object of EncodeKitty.
// A nil *EncodeKittyOptions means {}. Columns, Rows, and ImageId of 0 are omitted, matching JS truthiness.
type EncodeKittyOptions struct {
	Columns    int   `json:"columns,omitzero"`
	Rows       int   `json:"rows,omitzero"`
	ImageId    int   `json:"imageId,omitzero"`
	MoveCursor *bool `json:"moveCursor,omitzero"`
}

// EncodeKitty encodes base64 image bytes as a Kitty graphics transmission.
// Direct transmissions fit in one sequence; longer payloads are chunked at tiKittyChunkSize.
func EncodeKitty(base64Data string, options *EncodeKittyOptions) string {
	panic("unported: EncodeKitty")
}

// DeleteKittyImage deletes one Kitty image by id and frees its data (uppercase I).
func DeleteKittyImage(imageId int) string {
	panic("unported: DeleteKittyImage")
}

// DeleteAllKittyImages deletes every visible Kitty image and frees the image data.
func DeleteAllKittyImages() string { return "\x1b_Ga=d,d=A,q=2\x1b\\" }

// DeleteAllKittyPlacements deletes every visible Kitty placement and keeps the uploaded image data.
func DeleteAllKittyPlacements() string { return "\x1b_Ga=d,d=a,q=2\x1b\\" }

// EncodeITerm2Options is the options object of EncodeITerm2.
// A nil *EncodeITerm2Options means {}.
// Width and Height are an int cell count or a string such as "auto". Nil means omitted.
// Name nil or empty is omitted. PreserveAspectRatio nil keeps the protocol default; false emits 0.
// Inline nil means inline=1.
type EncodeITerm2Options struct {
	Width               any     `json:"width,omitzero"`
	Height              any     `json:"height,omitzero"`
	Name                *string `json:"name,omitzero"`
	PreserveAspectRatio *bool   `json:"preserveAspectRatio,omitzero"`
	Inline              *bool   `json:"inline,omitzero"`
}

// EncodeITerm2 encodes base64 image bytes as an iTerm2 inline-file sequence.
func EncodeITerm2(base64Data string, options *EncodeITerm2Options) string {
	panic("unported: EncodeITerm2")
}

// ImageCellSize is an image's size in terminal cells.
type ImageCellSize struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// KittyImageMetadata is the cell and pixel size recorded for one transmitted Kitty image.
// Field order matches the renderImage registration literal.
type KittyImageMetadata struct {
	ImageId  int `json:"imageId"`
	Columns  int `json:"columns"`
	Rows     int `json:"rows"`
	WidthPx  int `json:"widthPx"`
	HeightPx int `json:"heightPx"`
}

// tiRegisteredKittyImageMetadata is KittyImageMetadata plus the transmission generation.
type tiRegisteredKittyImageMetadata struct {
	KittyImageMetadata
	TransmissionGeneration int
}

// KittyImagePlacement is a placement-only Kitty command for an image line emitted by RenderImage.
type KittyImagePlacement struct {
	ImageId                int    `json:"imageId"`
	TransmissionGeneration int    `json:"transmissionGeneration"`
	TransmissionBytes      int    `json:"transmissionBytes"`
	EstimatedDecodedBytes  int    `json:"estimatedDecodedBytes"`
	Sequence               string `json:"sequence"`
	ReplacementLine        string `json:"replacementLine"`
}

// tiKittyImageMetadata is insertion-ordered. Re-register deletes then sets so the id moves to the end.
// More than 1000 entries drops the oldest key.
var tiKittyImageMetadata = omap.NewMap[int, tiRegisteredKittyImageMetadata]()

// tiKittyTransmissionGeneration is the last assigned transmission generation. It starts at 0.
var tiKittyTransmissionGeneration int

// RegisterKittyImageMetadata records metadata for a Kitty image id, replacing any previous record.
func RegisterKittyImageMetadata(metadata KittyImageMetadata) {
	panic("unported: RegisterKittyImageMetadata")
}

func tiGetRegisteredKittyImageMetadata(line string) *tiRegisteredKittyImageMetadata {
	panic("unported: tiGetRegisteredKittyImageMetadata")
}

// GetKittyImageMetadata returns the recorded metadata for the Kitty image referenced by line.
// Nil means line has no registered image id.
func GetKittyImageMetadata(line string) *KittyImageMetadata {
	panic("unported: GetKittyImageMetadata")
}

// tiKittyPlacementControlKeys are the Kitty control keys copied into a placement command.
// Lookup only; not iterated.
var tiKittyPlacementControlKeys = map[string]struct{}{
	"i": {},
	"p": {},
	"x": {},
	"y": {},
	"w": {},
	"h": {},
	"X": {},
	"Y": {},
	"c": {},
	"r": {},
	"C": {},
	"U": {},
	"z": {},
	"P": {},
	"Q": {},
	"H": {},
	"V": {},
}

// GetKittyImagePlacement builds a placement-only command for an image line emitted by RenderImage.
// Nil means line is not a registered Kitty transmission.
func GetKittyImagePlacement(line string) *KittyImagePlacement {
	panic("unported: GetKittyImagePlacement")
}

// CropKittyImageLine rewrites a Kitty image line so only visibleRows rows are shown, skipping hiddenRows.
// Out-of-range crops return line unchanged.
func CropKittyImageLine(line string, hiddenRows int, visibleRows int) string {
	panic("unported: CropKittyImageLine")
}

func tiChooseLessDistortedCellCount(upperCount int, idealCount float64) int {
	panic("unported: tiChooseLessDistortedCellCount")
}

// CalculateImageCellSize fits imageDimensions into the cell budget.
// maxHeightCells nil means no row cap. cellDimensions nil means 9 by 18, the TypeScript default.
// optimizeAspectRatio defaults to false.
func CalculateImageCellSize(imageDimensions ImageDimensions, maxWidthCells int, maxHeightCells *int, cellDimensions *CellDimensions, optimizeAspectRatio bool) ImageCellSize {
	panic("unported: CalculateImageCellSize")
}

// CalculateImageRows is the row count for targetWidthCells.
// cellDimensions nil means 9 by 18, the TypeScript default.
func CalculateImageRows(imageDimensions ImageDimensions, targetWidthCells int, cellDimensions *CellDimensions) int {
	panic("unported: CalculateImageRows")
}

// GetPngDimensions reads a PNG IHDR size from base64 data. Nil means the bytes are not a large enough PNG.
func GetPngDimensions(base64Data string) *ImageDimensions {
	panic("unported: GetPngDimensions")
}

// GetJpegDimensions reads a JPEG SOF size from base64 data. Nil means the size could not be read.
func GetJpegDimensions(base64Data string) *ImageDimensions {
	panic("unported: GetJpegDimensions")
}

// GetGifDimensions reads a GIF logical-screen size from base64 data. Nil means the bytes are not a GIF.
func GetGifDimensions(base64Data string) *ImageDimensions {
	panic("unported: GetGifDimensions")
}

// GetWebpDimensions reads a VP8, VP8L, or VP8X size from base64 data. Nil means the size could not be read.
func GetWebpDimensions(base64Data string) *ImageDimensions {
	panic("unported: GetWebpDimensions")
}

// GetImageDimensions dispatches on mimeType. Nil means the type is unknown or the probe failed.
func GetImageDimensions(base64Data string, mimeType string) *ImageDimensions {
	panic("unported: GetImageDimensions")
}

// RenderImageResult is the sequence and cell size from RenderImage.
// ImageId nil means the result has no Kitty id (omitted, or iTerm2).
type RenderImageResult struct {
	Sequence string `json:"sequence"`
	Columns  int    `json:"columns"`
	Rows     int    `json:"rows"`
	ImageId  *int   `json:"imageId,omitzero"`
}

// RenderImage builds a Kitty or iTerm2 sequence for the current capabilities.
// Nil means the terminal has no image protocol. options nil means {}.
func RenderImage(base64Data string, imageDimensions ImageDimensions, options *ImageRenderOptions) *RenderImageResult {
	panic("unported: RenderImage")
}

// Hyperlink wraps text in an OSC 8 hyperlink to url.
func Hyperlink(text string, url string) string {
	panic("unported: Hyperlink")
}

func tiShortenImagePath(filename string) string {
	panic("unported: tiShortenImagePath")
}

// ImageFallback is the text fallback when the terminal cannot render an inline image.
// dimensions nil means no size suffix. filename nil or empty means no path.
func ImageFallback(mimeType string, dimensions *ImageDimensions, filename *string) string {
	panic("unported: ImageFallback")
}
