// Ported from packages/tui/src/terminal-image.ts (pi v1.5.2).

package tui

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/omap"
)

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

var tiMu sync.Mutex

// tiCachedCapabilities is the memo of GetCapabilities. Nil means not yet detected.
var tiCachedCapabilities *TerminalCapabilities

// tiCapabilitiesEpoch increments when cache/overrides change so concurrent detection does not write stale results.
var tiCapabilitiesEpoch uint64

// tiCapabilityOverrides is applied on top of environment detection.
var tiCapabilityOverrides TerminalCapabilityOverrides

// tiCellDimensions is the cell size, updated when the terminal answers a size query.
// The TypeScript default is 9 by 18.
var tiCellDimensions = CellDimensions{WidthPx: 9, HeightPx: 18}

// GetCellDimensions returns the current cell size.
func GetCellDimensions() CellDimensions {
	tiMu.Lock()
	dims := tiCellDimensions
	tiMu.Unlock()
	return dims
}

// SetCellDimensions replaces the current cell size.
func SetCellDimensions(dims CellDimensions) {
	tiMu.Lock()
	tiCellDimensions = dims
	tiMu.Unlock()
}

// tiProbeTmuxHyperlinks reports whether the attached tmux client forwards OSC 8 hyperlinks.
// On any error it returns false.
func tiProbeTmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}").Output()
	if err != nil {
		return false
	}
	for _, feat := range strings.Split(string(out), ",") {
		if strings.TrimSpace(feat) == "hyperlinks" {
			return true
		}
	}
	return false
}

func tiDetectCapabilitiesFromEnvironment(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	terminalEmulator := strings.ToLower(os.Getenv("TERMINAL_EMULATOR"))
	term := strings.ToLower(os.Getenv("TERM"))
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))
	hasTrueColorHint := colorTerm == "truecolor" || colorTerm == "24bit" || strings.HasSuffix(term, "-direct")
	isWindowsConsole := runtime.GOOS == "windows"

	if os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux") {
		return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: tmuxForwardsHyperlink()}
	}

	if strings.HasPrefix(term, "screen") {
		return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: false}
	}

	if termProgram == "herdr" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: hasTrueColorHint, Hyperlinks: true}
	}

	if termProgram == "rex" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if termProgram == "orca" && os.Getenv("ORCA_IMAGE_PROTOCOL") == "kitty" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if os.Getenv("KITTY_WINDOW_ID") != "" || termProgram == "kitty" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if termProgram == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if os.Getenv("WEZTERM_PANE") != "" || termProgram == "wezterm" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if termProgram == "warpterminal" || os.Getenv("WARP_SESSION_ID") != "" || os.Getenv("WARP_TERMINAL_SESSION_UUID") != "" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if os.Getenv("ITERM_SESSION_ID") != "" || termProgram == "iterm.app" {
		return TerminalCapabilities{Images: ImageProtocolIterm2, TrueColor: true, Hyperlinks: true}
	}

	if os.Getenv("WT_SESSION") != "" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true}
	}

	isAlacritty := termProgram == "alacritty" || strings.HasPrefix(term, "alacritty") || os.Getenv("ALACRITTY_WINDOW_ID") != ""
	if isAlacritty || termProgram == "vscode" || termProgram == "zed" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true}
	}

	if terminalEmulator == "jetbrains-jediterm" {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: false}
	}

	if isWindowsConsole {
		return TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: false}
	}

	return TerminalCapabilities{Images: "", TrueColor: hasTrueColorHint, Hyperlinks: false}
}

// tiParseBooleanCapabilityOverride parses "1" and "0". Nil means any other value, including empty.
func tiParseBooleanCapabilityOverride(value *string) *bool {
	if value == nil {
		return nil
	}
	if *value == "1" {
		v := true
		return &v
	}
	if *value == "0" {
		v := false
		return &v
	}
	return nil
}

func tiLookupEnvPtr(name string) *string {
	v, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	return &v
}

// DetectCapabilities reads the environment and an optional tmux hyperlink probe.
// tmuxForwardsHyperlink nil means tiProbeTmuxHyperlinks, the TypeScript default.
func DetectCapabilities(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	if tmuxForwardsHyperlink == nil {
		tmuxForwardsHyperlink = tiProbeTmuxHyperlinks
	}
	hyperlinks := tiParseBooleanCapabilityOverride(tiLookupEnvPtr("PI_HYPERLINKS"))
	probe := tmuxForwardsHyperlink
	if hyperlinks != nil {
		h := *hyperlinks
		probe = func() bool { return h }
	}
	detected := tiDetectCapabilitiesFromEnvironment(probe)

	imageProtocol := strings.ToLower(os.Getenv("PI_IMAGE_PROTOCOL"))
	if imageProtocol == string(ImageProtocolKitty) || imageProtocol == string(ImageProtocolIterm2) {
		detected.Images = ImageProtocol(imageProtocol)
	} else if imageProtocol == "none" || imageProtocol == "0" {
		detected.Images = ""
	}

	if trueColor := tiParseBooleanCapabilityOverride(tiLookupEnvPtr("PI_TRUE_COLOR")); trueColor != nil {
		detected.TrueColor = *trueColor
	}
	if hyperlinks != nil {
		detected.Hyperlinks = *hyperlinks
	}
	return detected
}

func tiCopyOverrides(src TerminalCapabilityOverrides) TerminalCapabilityOverrides {
	var out TerminalCapabilityOverrides
	if src.Images != nil {
		v := *src.Images
		out.Images = &v
	}
	if src.TrueColor != nil {
		v := *src.TrueColor
		out.TrueColor = &v
	}
	if src.Hyperlinks != nil {
		v := *src.Hyperlinks
		out.Hyperlinks = &v
	}
	return out
}

func tiPtrEqual[T comparable](a, b *T) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// GetCapabilities returns the cached capabilities, detecting them on first use.
func GetCapabilities() TerminalCapabilities {
	tiMu.Lock()
	if tiCachedCapabilities != nil {
		caps := *tiCachedCapabilities
		tiMu.Unlock()
		return caps
	}
	overrides := tiCopyOverrides(tiCapabilityOverrides)
	epoch := tiCapabilitiesEpoch
	tiMu.Unlock()

	var probe func() bool
	if overrides.Hyperlinks != nil {
		h := *overrides.Hyperlinks
		probe = func() bool { return h }
	}
	caps := DetectCapabilities(probe)
	if overrides.Images != nil {
		caps.Images = *overrides.Images
	}
	if overrides.TrueColor != nil {
		caps.TrueColor = *overrides.TrueColor
	}
	if overrides.Hyperlinks != nil {
		caps.Hyperlinks = *overrides.Hyperlinks
	}

	tiMu.Lock()
	if epoch == tiCapabilitiesEpoch && tiCachedCapabilities == nil {
		c := caps
		tiCachedCapabilities = &c
	}
	tiMu.Unlock()
	return caps
}

// GetTerminalColorMode returns truecolor when capabilities report true color, otherwise 256color.
// capabilities nil means GetCapabilities(), the TypeScript default.
func GetTerminalColorMode(capabilities *TerminalCapabilities) TerminalColorMode {
	var caps TerminalCapabilities
	if capabilities != nil {
		caps = *capabilities
	} else {
		caps = GetCapabilities()
	}
	if caps.TrueColor {
		return TerminalColorModeTruecolor
	}
	return TerminalColorMode256color
}

// ResetCapabilitiesCache forgets the cached detection result.
func ResetCapabilitiesCache() {
	tiMu.Lock()
	tiCachedCapabilities = nil
	tiCapabilitiesEpoch++
	tiMu.Unlock()
}

// SetCapabilityOverrides replaces the selected auto-detected capabilities.
// overrides nil means an empty partial. Identical overrides leave the cache alone.
func SetCapabilityOverrides(overrides *TerminalCapabilityOverrides) {
	var next TerminalCapabilityOverrides
	if overrides != nil {
		next = tiCopyOverrides(*overrides)
	}
	tiMu.Lock()
	if tiPtrEqual(tiCapabilityOverrides.Images, next.Images) &&
		tiPtrEqual(tiCapabilityOverrides.TrueColor, next.TrueColor) &&
		tiPtrEqual(tiCapabilityOverrides.Hyperlinks, next.Hyperlinks) {
		tiMu.Unlock()
		return
	}
	tiCapabilityOverrides = next
	tiCachedCapabilities = nil
	tiCapabilitiesEpoch++
	tiMu.Unlock()
}

// SetCapabilities replaces the cached capabilities. Tests use this to force both code paths.
func SetCapabilities(caps TerminalCapabilities) {
	tiMu.Lock()
	c := caps
	tiCachedCapabilities = &c
	tiCapabilitiesEpoch++
	tiMu.Unlock()
}

const (
	tiKittyPrefix    = "\x1b_G"
	tiIterm2Prefix   = "\x1b]1337;File="
	tiKittyChunkSize = 4096
)

var (
	tiKittyControlsRegex    = regexp.MustCompile("\x1b_G([^;]*);")
	tiKittyColumnControlRe  = regexp.MustCompile(`(?:^|,)c=(\d+)(?:,|$)`)
	tiKittyImageIdControlRe = regexp.MustCompile(`(?:^|,)i=(\d+)(?:,|$)`)
	tiKittyRowsControlRe    = regexp.MustCompile(`(?:^|,)r=(\d+)(?:,|$)`)
	tiKittyMoreChunksRe     = regexp.MustCompile(`(?:^|,)m=1(?:,|$)`)
	tiKittyCropStripRe      = regexp.MustCompile(`^[yhr]=`)
	tiIterm2MoveUpSuffixRe  = regexp.MustCompile("\x1b\\[(\\d+)A$")
	tiIterm2WidthRe         = regexp.MustCompile(`[;=]width=(\d+)[;:]`)
	tiStripKittyRegex       = regexp.MustCompile("\x1b_G[^\x1b]*\x1b\\\\")
	tiStripIterm2Regex      = regexp.MustCompile("(?:\x1b\\[\\d+A)?\x1b\\]1337;File=[^\x07]*\x07")
)

// IsImageLine reports whether line contains a Kitty or iTerm2 inline-image sequence.
func IsImageLine(line string) bool {
	if strings.HasPrefix(line, tiKittyPrefix) || strings.HasPrefix(line, tiIterm2Prefix) {
		return true
	}
	return strings.Contains(line, tiKittyPrefix) || strings.Contains(line, tiIterm2Prefix)
}

// ImageLineExtent describes the terminal cells covered by an image placement on a line.
// Rows are relative to the line; EndCol is exclusive (math.MaxInt when width cannot be read).
type ImageLineExtent struct {
	FirstRow int `json:"firstRow"`
	LastRow  int `json:"lastRow"`
	StartCol int `json:"startCol"`
	EndCol   int `json:"endCol"`
}

// GetImageLineExtent returns the cell rectangle an image line draws over, or nil if not an image line.
// Kitty places the image at the line and extends downward; iTerm2 moves the cursor up before drawing.
func GetImageLineExtent(line string) *ImageLineExtent {
	if kittyStart := strings.Index(line, tiKittyPrefix); kittyStart != -1 {
		controls := ""
		if m := tiKittyControlsRegex.FindStringSubmatch(line); m != nil {
			controls = m[1]
		}
		var columns *int
		if cm := tiKittyColumnControlRe.FindStringSubmatch(controls); cm != nil {
			if c, err := strconv.Atoi(cm[1]); err == nil {
				columns = &c
			}
		} else {
			tiMu.Lock()
			if meta := tiGetRegisteredKittyImageMetadataFromControlsLocked(controls); meta != nil {
				c := meta.Columns
				columns = &c
			}
			tiMu.Unlock()
		}
		startCol := VisibleWidth(line[:kittyStart])
		rows := 1
		if r := GetKittyImagePlacementRows(line); r != nil {
			rows = *r
		}
		endCol := math.MaxInt
		if columns != nil {
			endCol = startCol + *columns
		}
		return &ImageLineExtent{
			FirstRow: 0,
			LastRow:  rows - 1,
			StartCol: startCol,
			EndCol:   endCol,
		}
	}

	iterm2Start := strings.Index(line, tiIterm2Prefix)
	if iterm2Start == -1 {
		return nil
	}
	before := line[:iterm2Start]
	moveUpIdx := tiIterm2MoveUpSuffixRe.FindStringSubmatchIndex(before)
	firstRow := 0
	beforeText := before
	if moveUpIdx != nil {
		if n, err := strconv.Atoi(before[moveUpIdx[2]:moveUpIdx[3]]); err == nil {
			firstRow = -n
		}
		beforeText = before[:moveUpIdx[0]]
	}
	startCol := VisibleWidth(beforeText)
	endCol := math.MaxInt
	if wm := tiIterm2WidthRe.FindStringSubmatch(line[iterm2Start:]); wm != nil {
		if w, err := strconv.Atoi(wm[1]); err == nil {
			endCol = startCol + w
		}
	}
	return &ImageLineExtent{
		FirstRow: firstRow,
		LastRow:  0,
		StartCol: startCol,
		EndCol:   endCol,
	}
}

// StripImageSequences removes Kitty and iTerm2 image sequences (including iTerm2's cursor-up prefix), keeping text.
func StripImageSequences(line string) string {
	s := tiStripKittyRegex.ReplaceAllString(line, "")
	return tiStripIterm2Regex.ReplaceAllString(s, "")
}

// AllocateImageId returns a random Kitty image id in [1, 0xffffffff].
func AllocateImageId() int {
	return rand.IntN(0xfffffffe) + 1
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
	params := []string{"a=T", "f=100", "q=2"}
	if options != nil {
		if options.MoveCursor != nil && !*options.MoveCursor {
			params = append(params, "C=1")
		}
		if options.Columns != 0 {
			params = append(params, fmt.Sprintf("c=%d", options.Columns))
		}
		if options.Rows != 0 {
			params = append(params, fmt.Sprintf("r=%d", options.Rows))
		}
		if options.ImageId != 0 {
			params = append(params, fmt.Sprintf("i=%d", options.ImageId))
		}
	}

	paramStr := strings.Join(params, ",")
	if len(base64Data) <= tiKittyChunkSize {
		return "\x1b_G" + paramStr + ";" + base64Data + "\x1b\\"
	}

	var b strings.Builder
	isFirst := true
	for offset := 0; offset < len(base64Data); offset += tiKittyChunkSize {
		end := offset + tiKittyChunkSize
		if end > len(base64Data) {
			end = len(base64Data)
		}
		chunk := base64Data[offset:end]
		isLast := end >= len(base64Data)
		if isFirst {
			b.WriteString("\x1b_G")
			b.WriteString(paramStr)
			b.WriteString(",m=1;")
			b.WriteString(chunk)
			b.WriteString("\x1b\\")
			isFirst = false
		} else if isLast {
			b.WriteString("\x1b_Gm=0;")
			b.WriteString(chunk)
			b.WriteString("\x1b\\")
		} else {
			b.WriteString("\x1b_Gm=1;")
			b.WriteString(chunk)
			b.WriteString("\x1b\\")
		}
	}
	return b.String()
}

// DeleteKittyImage deletes one Kitty image by id and frees its data (uppercase I).
func DeleteKittyImage(imageId int) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", imageId)
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

func tiFilterBase64Bytes(s string) ([]byte, int) {
	cleaned := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '-' || c == '_' {
			if c == '-' {
				c = '+'
			} else if c == '_' {
				c = '/'
			}
			cleaned = append(cleaned, c)
		} else if c == '=' {
			break
		}
	}
	return cleaned, (len(cleaned) * 3) / 4
}

func tiDecodeBase64(s string) ([]byte, bool) {
	cleaned, _ := tiFilterBase64Bytes(s)
	// Ignore a trailing solitary base64 sextet like Node Buffer.from(..., "base64").
	if rem := len(cleaned) % 4; rem == 1 {
		cleaned = cleaned[:len(cleaned)-1]
	}
	if len(cleaned) == 0 {
		return nil, true
	}
	out, err := base64.RawStdEncoding.DecodeString(string(cleaned))
	if err != nil {
		return nil, false
	}
	return out, true
}

func tiBase64ByteLength(s string) int {
	_, n := tiFilterBase64Bytes(s)
	return n
}

// EncodeITerm2 encodes base64 image bytes as an iTerm2 inline-file sequence.
func EncodeITerm2(base64Data string, options *EncodeITerm2Options) string {
	inlineVal := 1
	if options != nil && options.Inline != nil && !*options.Inline {
		inlineVal = 0
	}
	params := []string{
		fmt.Sprintf("inline=%d", inlineVal),
		fmt.Sprintf("size=%d", tiBase64ByteLength(base64Data)),
	}
	if options != nil {
		if options.Width != nil {
			params = append(params, fmt.Sprintf("width=%v", options.Width))
		}
		if options.Height != nil {
			params = append(params, fmt.Sprintf("height=%v", options.Height))
		}
		if options.Name != nil && *options.Name != "" {
			params = append(params, "name="+base64.StdEncoding.EncodeToString([]byte(*options.Name)))
		}
		if options.PreserveAspectRatio != nil && !*options.PreserveAspectRatio {
			params = append(params, "preserveAspectRatio=0")
		}
	}
	return "\x1b]1337;File=" + strings.Join(params, ";") + ":" + base64Data + "\x07"
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
	Rows                   int    `json:"rows"`
	Sequence               string `json:"sequence"`
	ReplacementLine        string `json:"replacementLine"`
}

// tiKittyImageMetadata is insertion-ordered. Re-register deletes then sets so the id moves to the end.
// More than 1000 entries drops the oldest key.
var tiKittyImageMetadata = omap.NewMap[int, tiRegisteredKittyImageMetadata]()

// tiKittyTransmissionGeneration is the last assigned transmission generation. It starts at 0.
var tiKittyTransmissionGeneration int

// tiMaxKittyImageRows is the row height of the tallest Kitty image registered so far.
var tiMaxKittyImageRows int

// RegisterKittyImageMetadata records metadata for a Kitty image id, replacing any previous record.
func RegisterKittyImageMetadata(metadata KittyImageMetadata) {
	tiMu.Lock()
	tiKittyTransmissionGeneration++
	if metadata.Rows > tiMaxKittyImageRows {
		tiMaxKittyImageRows = metadata.Rows
	}
	tiKittyImageMetadata.Delete(metadata.ImageId)
	tiKittyImageMetadata.Set(metadata.ImageId, tiRegisteredKittyImageMetadata{
		KittyImageMetadata:     metadata,
		TransmissionGeneration: tiKittyTransmissionGeneration,
	})
	if tiKittyImageMetadata.Len() > 1000 {
		keys := tiKittyImageMetadata.Keys()
		if len(keys) > 0 {
			tiKittyImageMetadata.Delete(keys[0])
		}
	}
	tiMu.Unlock()
}

// GetMaxKittyImageRows returns the row height of the tallest Kitty image registered so far.
func GetMaxKittyImageRows() int {
	tiMu.Lock()
	rows := tiMaxKittyImageRows
	tiMu.Unlock()
	return rows
}

func tiGetRegisteredKittyImageMetadataFromControlsLocked(controls string) *tiRegisteredKittyImageMetadata {
	m := tiKittyImageIdControlRe.FindStringSubmatch(controls)
	if m == nil {
		return nil
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	meta, ok := tiKittyImageMetadata.Get(id)
	if !ok {
		return nil
	}
	cp := meta
	return &cp
}

func tiGetRegisteredKittyImageMetadata(line string) *tiRegisteredKittyImageMetadata {
	m := tiKittyControlsRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	tiMu.Lock()
	meta := tiGetRegisteredKittyImageMetadataFromControlsLocked(m[1])
	tiMu.Unlock()
	return meta
}

func tiGetExplicitKittyImageRows(controls string) *int {
	m := tiKittyRowsControlRe.FindStringSubmatch(controls)
	if m == nil {
		return nil
	}
	rows, err := strconv.Atoi(m[1])
	if err != nil || rows <= 0 {
		return nil
	}
	return &rows
}

func tiGetKittyImageRowsFromControls(controls string, fallbackRows int) int {
	if explicit := tiGetExplicitKittyImageRows(controls); explicit != nil {
		return *explicit
	}
	return fallbackRows
}

// GetKittyImageMetadata returns the recorded metadata for the Kitty image referenced by line.
// Nil means line has no registered image id.
func GetKittyImageMetadata(line string) *KittyImageMetadata {
	meta := tiGetRegisteredKittyImageMetadata(line)
	if meta == nil {
		return nil
	}
	cp := meta.KittyImageMetadata
	return &cp
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

// GetKittyImagePlacementRows reads the number of rows covered by an image placement without scanning its payload.
func GetKittyImagePlacementRows(line string) *int {
	m := tiKittyControlsRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	controls := m[1]
	if explicit := tiGetExplicitKittyImageRows(controls); explicit != nil {
		return explicit
	}
	tiMu.Lock()
	meta := tiGetRegisteredKittyImageMetadataFromControlsLocked(controls)
	tiMu.Unlock()
	if meta == nil {
		return nil
	}
	rows := meta.Rows
	return &rows
}

// GetKittyImagePlacement builds a placement-only command for an image line emitted by RenderImage.
// Nil means line is not a registered Kitty transmission.
func GetKittyImagePlacement(line string) *KittyImagePlacement {
	matchIdx := tiKittyControlsRegex.FindStringSubmatchIndex(line)
	if matchIdx == nil {
		return nil
	}
	matchStart := matchIdx[0]
	matchControls := line[matchIdx[2]:matchIdx[3]]

	tiMu.Lock()
	meta := tiGetRegisteredKittyImageMetadataFromControlsLocked(matchControls)
	tiMu.Unlock()
	if meta == nil {
		return nil
	}

	commandStart := matchStart
	commandControls := matchControls
	var transmissionEnd int
	for {
		searchFrom := commandStart + len(tiKittyPrefix)
		if searchFrom > len(line) {
			return nil
		}
		termRel := strings.Index(line[searchFrom:], "\x1b\\")
		if termRel == -1 {
			return nil
		}
		terminator := searchFrom + termRel
		transmissionEnd = terminator + 2
		if !tiKittyMoreChunksRe.MatchString(commandControls) {
			break
		}
		commandStart = transmissionEnd
		if !strings.HasPrefix(line[commandStart:], tiKittyPrefix) {
			return nil
		}
		ctrlSearchFrom := commandStart + len(tiKittyPrefix)
		ctrlRel := strings.IndexByte(line[ctrlSearchFrom:], ';')
		if ctrlRel == -1 {
			return nil
		}
		commandControls = line[ctrlSearchFrom : ctrlSearchFrom+ctrlRel]
	}

	var filteredControls []string
	for _, control := range strings.Split(matchControls, ",") {
		key := control
		if eq := strings.IndexByte(control, '='); eq != -1 {
			key = control[:eq]
		}
		if _, ok := tiKittyPlacementControlKeys[key]; ok {
			filteredControls = append(filteredControls, control)
		}
	}
	sequence := "\x1b_Ga=p,q=2," + strings.Join(filteredControls, ",") + "\x1b\\"
	return &KittyImagePlacement{
		ImageId:                meta.ImageId,
		TransmissionGeneration: meta.TransmissionGeneration,
		TransmissionBytes:      transmissionEnd - matchStart,
		EstimatedDecodedBytes:  meta.WidthPx * meta.HeightPx * 4,
		Rows:                   tiGetKittyImageRowsFromControls(matchControls, meta.Rows),
		Sequence:               sequence,
		ReplacementLine:        line[:matchStart] + sequence + line[transmissionEnd:],
	}
}

// CropKittyImageLine rewrites a Kitty image line so only visibleRows rows are shown, skipping hiddenRows.
// Out-of-range crops return line unchanged.
func CropKittyImageLine(line string, hiddenRows int, visibleRows int) string {
	meta := GetKittyImageMetadata(line)
	matchIdx := tiKittyControlsRegex.FindStringSubmatchIndex(line)
	if meta == nil || matchIdx == nil || hiddenRows < 0 || hiddenRows >= meta.Rows || visibleRows <= 0 {
		return line
	}
	croppedRows := meta.Rows - hiddenRows
	if visibleRows < croppedRows {
		croppedRows = visibleRows
	}
	if hiddenRows == 0 && croppedRows == meta.Rows {
		return line
	}
	sourceY := int(math.Floor(float64(meta.HeightPx*hiddenRows) / float64(meta.Rows)))
	sourceEnd := int(math.Ceil(float64(meta.HeightPx*(hiddenRows+croppedRows)) / float64(meta.Rows)))
	cappedEnd := meta.HeightPx
	if sourceEnd < cappedEnd {
		cappedEnd = sourceEnd
	}
	sourceHeight := cappedEnd - sourceY
	if sourceHeight < 1 {
		sourceHeight = 1
	}

	rawControls := line[matchIdx[2]:matchIdx[3]]
	var controls []string
	for _, c := range strings.Split(rawControls, ",") {
		if !tiKittyCropStripRe.MatchString(c) {
			controls = append(controls, c)
		}
	}
	controls = append(controls, fmt.Sprintf("y=%d", sourceY), fmt.Sprintf("h=%d", sourceHeight), fmt.Sprintf("r=%d", croppedRows))
	return line[:matchIdx[0]] + "\x1b_G" + strings.Join(controls, ",") + ";" + line[matchIdx[1]:]
}

func tiChooseLessDistortedCellCount(upperCount int, idealCount float64) int {
	if upperCount <= 1 {
		return upperCount
	}
	lowerCount := upperCount - 1
	upperDistortion := math.Max(float64(upperCount)/idealCount, idealCount/float64(upperCount))
	lowerDistortion := math.Max(float64(lowerCount)/idealCount, idealCount/float64(lowerCount))
	if lowerDistortion < upperDistortion {
		return lowerCount
	}
	return upperCount
}

// CalculateImageCellSize fits imageDimensions into the cell budget.
// maxHeightCells nil means no row cap. cellDimensions nil means 9 by 18, the TypeScript default.
// optimizeAspectRatio defaults to false.
func CalculateImageCellSize(imageDimensions ImageDimensions, maxWidthCells int, maxHeightCells *int, cellDimensions *CellDimensions, optimizeAspectRatio bool) ImageCellSize {
	cellDims := CellDimensions{WidthPx: 9, HeightPx: 18}
	if cellDimensions != nil {
		cellDims = *cellDimensions
	}
	maxWidth := maxWidthCells
	if maxWidth < 1 {
		maxWidth = 1
	}
	var maxHeight *int
	if maxHeightCells != nil {
		mh := *maxHeightCells
		if mh < 1 {
			mh = 1
		}
		maxHeight = &mh
	}
	imageWidth := imageDimensions.WidthPx
	if imageWidth < 1 {
		imageWidth = 1
	}
	imageHeight := imageDimensions.HeightPx
	if imageHeight < 1 {
		imageHeight = 1
	}

	widthScale := float64(maxWidth*cellDims.WidthPx) / float64(imageWidth)
	heightScale := widthScale
	if maxHeight != nil {
		heightScale = float64((*maxHeight)*cellDims.HeightPx) / float64(imageHeight)
	}
	scale := math.Min(widthScale, heightScale)

	scaledWidthPx := float64(imageWidth) * scale
	scaledHeightPx := float64(imageHeight) * scale
	columns := int(math.Ceil(scaledWidthPx / float64(cellDims.WidthPx)))
	if columns > maxWidth {
		columns = maxWidth
	}
	if columns < 1 {
		columns = 1
	}
	rows := int(math.Ceil(scaledHeightPx / float64(cellDims.HeightPx)))
	if rows < 1 {
		rows = 1
	}
	if maxHeight != nil && *maxHeight < rows {
		rows = *maxHeight
	}

	if !optimizeAspectRatio {
		return ImageCellSize{Columns: columns, Rows: rows}
	}

	if widthScale <= heightScale {
		idealRows := float64(columns*cellDims.WidthPx*imageHeight) / float64(imageWidth*cellDims.HeightPx)
		rows = tiChooseLessDistortedCellCount(rows, idealRows)
	} else {
		idealColumns := float64(rows*cellDims.HeightPx*imageWidth) / float64(imageHeight*cellDims.WidthPx)
		columns = tiChooseLessDistortedCellCount(columns, idealColumns)
	}

	return ImageCellSize{Columns: columns, Rows: rows}
}

// CalculateImageRows is the row count for targetWidthCells.
// cellDimensions nil means 9 by 18, the TypeScript default.
func CalculateImageRows(imageDimensions ImageDimensions, targetWidthCells int, cellDimensions *CellDimensions) int {
	if cellDimensions == nil {
		d := CellDimensions{WidthPx: 9, HeightPx: 18}
		cellDimensions = &d
	}
	return CalculateImageCellSize(imageDimensions, targetWidthCells, nil, cellDimensions, false).Rows
}

// GetPngDimensions reads a PNG IHDR size from base64 data. Nil means the bytes are not a large enough PNG.
func GetPngDimensions(base64Data string) *ImageDimensions {
	buf, ok := tiDecodeBase64(base64Data)
	if !ok || len(buf) < 24 {
		return nil
	}
	if buf[0] != 0x89 || buf[1] != 0x50 || buf[2] != 0x4e || buf[3] != 0x47 {
		return nil
	}
	w := int(binary.BigEndian.Uint32(buf[16:20]))
	h := int(binary.BigEndian.Uint32(buf[20:24]))
	return &ImageDimensions{WidthPx: w, HeightPx: h}
}

// GetJpegDimensions reads a JPEG SOF size from base64 data. Nil means the size could not be read.
func GetJpegDimensions(base64Data string) *ImageDimensions {
	buf, ok := tiDecodeBase64(base64Data)
	if !ok || len(buf) < 2 {
		return nil
	}
	if buf[0] != 0xff || buf[1] != 0xd8 {
		return nil
	}
	offset := 2
	for offset < len(buf)-9 {
		if buf[offset] != 0xff {
			offset++
			continue
		}
		marker := buf[offset+1]
		if marker >= 0xc0 && marker <= 0xc2 {
			h := int(binary.BigEndian.Uint16(buf[offset+5 : offset+7]))
			w := int(binary.BigEndian.Uint16(buf[offset+7 : offset+9]))
			return &ImageDimensions{WidthPx: w, HeightPx: h}
		}
		if offset+3 >= len(buf) {
			return nil
		}
		segLen := int(binary.BigEndian.Uint16(buf[offset+2 : offset+4]))
		if segLen < 2 {
			return nil
		}
		offset += 2 + segLen
	}
	return nil
}

// GetGifDimensions reads a GIF logical-screen size from base64 data. Nil means the bytes are not a GIF.
func GetGifDimensions(base64Data string) *ImageDimensions {
	buf, ok := tiDecodeBase64(base64Data)
	if !ok || len(buf) < 10 {
		return nil
	}
	sig := string(buf[:6])
	if sig != "GIF87a" && sig != "GIF89a" {
		return nil
	}
	w := int(binary.LittleEndian.Uint16(buf[6:8]))
	h := int(binary.LittleEndian.Uint16(buf[8:10]))
	return &ImageDimensions{WidthPx: w, HeightPx: h}
}

// GetWebpDimensions reads a VP8, VP8L, or VP8X size from base64 data. Nil means the size could not be read.
func GetWebpDimensions(base64Data string) *ImageDimensions {
	buf, ok := tiDecodeBase64(base64Data)
	if !ok || len(buf) < 30 {
		return nil
	}
	if string(buf[0:4]) != "RIFF" || string(buf[8:12]) != "WEBP" {
		return nil
	}
	chunk := string(buf[12:16])
	switch chunk {
	case "VP8 ":
		w := int(binary.LittleEndian.Uint16(buf[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(buf[28:30]) & 0x3fff)
		return &ImageDimensions{WidthPx: w, HeightPx: h}
	case "VP8L":
		bits := binary.LittleEndian.Uint32(buf[21:25])
		w := int(bits&0x3fff) + 1
		h := int((bits>>14)&0x3fff) + 1
		return &ImageDimensions{WidthPx: w, HeightPx: h}
	case "VP8X":
		w := (int(buf[24]) | (int(buf[25]) << 8) | (int(buf[26]) << 16)) + 1
		h := (int(buf[27]) | (int(buf[28]) << 8) | (int(buf[29]) << 16)) + 1
		return &ImageDimensions{WidthPx: w, HeightPx: h}
	}
	return nil
}

// GetImageDimensions dispatches on mimeType. Nil means the type is unknown or the probe failed.
func GetImageDimensions(base64Data string, mimeType string) *ImageDimensions {
	switch mimeType {
	case "image/png":
		return GetPngDimensions(base64Data)
	case "image/jpeg":
		return GetJpegDimensions(base64Data)
	case "image/gif":
		return GetGifDimensions(base64Data)
	case "image/webp":
		return GetWebpDimensions(base64Data)
	default:
		return nil
	}
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
	caps := GetCapabilities()
	if caps.Images == "" {
		return nil
	}

	maxWidth := 80
	var maxHeight *int
	var preserveAspectRatio *bool
	var imageId *int
	var moveCursor *bool
	if options != nil {
		if options.MaxWidthCells != nil {
			maxWidth = *options.MaxWidthCells
		}
		maxHeight = options.MaxHeightCells
		preserveAspectRatio = options.PreserveAspectRatio
		imageId = options.ImageId
		moveCursor = options.MoveCursor
	}

	cellDims := GetCellDimensions()
	size := CalculateImageCellSize(
		imageDimensions,
		maxWidth,
		maxHeight,
		&cellDims,
		caps.Images == ImageProtocolKitty,
	)

	if caps.Images == ImageProtocolKitty {
		imgIdVal := 0
		if imageId != nil {
			imgIdVal = *imageId
			RegisterKittyImageMetadata(KittyImageMetadata{
				ImageId:  *imageId,
				Columns:  size.Columns,
				Rows:     size.Rows,
				WidthPx:  imageDimensions.WidthPx,
				HeightPx: imageDimensions.HeightPx,
			})
		}
		seq := EncodeKitty(base64Data, &EncodeKittyOptions{
			Columns:    size.Columns,
			Rows:       size.Rows,
			ImageId:    imgIdVal,
			MoveCursor: moveCursor,
		})
		return &RenderImageResult{
			Sequence: seq,
			Columns:  size.Columns,
			Rows:     size.Rows,
			ImageId:  imageId,
		}
	}

	if caps.Images == ImageProtocolIterm2 {
		par := true
		if preserveAspectRatio != nil {
			par = *preserveAspectRatio
		}
		seq := EncodeITerm2(base64Data, &EncodeITerm2Options{
			Width:               size.Columns,
			Height:              "auto",
			PreserveAspectRatio: &par,
		})
		return &RenderImageResult{
			Sequence: seq,
			Columns:  size.Columns,
			Rows:     size.Rows,
		}
	}

	return nil
}

// Hyperlink wraps text in an OSC 8 hyperlink to url.
func Hyperlink(text string, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func tiShortenImagePath(filename string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && (filename == home || strings.HasPrefix(filename, home+"/") || strings.HasPrefix(filename, home+"\\")) {
		return "~" + filename[len(home):]
	}
	return filename
}

func tiPathToFileURL(filename string) string {
	slashed := filepath.ToSlash(filename)
	if strings.HasPrefix(slashed, "//") {
		without := strings.TrimPrefix(slashed, "//")
		if idx := strings.IndexByte(without, '/'); idx != -1 {
			return (&url.URL{Scheme: "file", Host: without[:idx], Path: without[idx:]}).String()
		}
		return (&url.URL{Scheme: "file", Host: without, Path: "/"}).String()
	}
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

// ImageFallback is the text fallback when the terminal cannot render an inline image.
// dimensions nil means no size suffix. filename nil or empty means no path.
func ImageFallback(mimeType string, dimensions *ImageDimensions, filename *string) string {
	var parts []string
	if filename != nil && *filename != "" {
		display := tiShortenImagePath(*filename)
		if GetCapabilities().Hyperlinks && filepath.IsAbs(*filename) {
			parts = append(parts, Hyperlink(display, tiPathToFileURL(*filename)))
		} else {
			parts = append(parts, display)
		}
	}
	parts = append(parts, "["+mimeType+"]")
	if dimensions != nil {
		parts = append(parts, fmt.Sprintf("%dx%d", dimensions.WidthPx, dimensions.HeightPx))
	}
	return "[Image: " + strings.Join(parts, " ") + "]"
}
