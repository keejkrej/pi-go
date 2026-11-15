// Ported from packages/tui/src/tui-main-screen.ts (pi v1.5.2).

package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	tmsKittySequencePrefix = "\x1b_G"
	tmsMaxRenderWriteChars = 1024 * 1024
)

// OSC 133 prompt markers at the start of a rendered line. Fullscreen uses them for prompt navigation.
var tmsOsc133ZonePrefix = regexp.MustCompile(`^(?:\x1b\]133;[ABC](?:\x07|\x1b\\))+`)

// tmsBoundedTerminalWriter streams terminal output in 1 MiB chunks without splitting UTF-8 runes.
type tmsBoundedTerminalWriter struct {
	buf          strings.Builder
	writtenChars int
	write        func(data string)
}

func (w *tmsBoundedTerminalWriter) append(value string) {
	offset := 0
	for offset < len(value) {
		capacity := tmsMaxRenderWriteChars - w.buf.Len()
		if capacity <= 0 {
			w.flush()
			continue
		}

		end := min(len(value), offset+capacity)
		for end < len(value) && end > offset && !utf8.RuneStart(value[end]) {
			end--
		}
		if end == offset {
			if w.buf.Len() > 0 {
				w.flush()
				continue
			}
			_, size := utf8.DecodeRuneInString(value[offset:])
			end = offset + size
		}

		w.buf.WriteString(value[offset:end])
		offset = end
		if w.buf.Len() >= tmsMaxRenderWriteChars {
			w.flush()
		}
	}
}

func (w *tmsBoundedTerminalWriter) flush() {
	if w.buf.Len() == 0 {
		return
	}
	data := w.buf.String()
	w.write(data)
	w.writtenChars += len(data)
	w.buf.Reset()
}

func (w *tmsBoundedTerminalWriter) length() int {
	return w.writtenChars + w.buf.Len()
}

type tmsKittyImageHeader struct {
	ids  []int
	rows int
}

func tmsParseKittyImageHeader(line string) *tmsKittyImageHeader {
	sequenceStart := strings.Index(line, tmsKittySequencePrefix)
	if sequenceStart == -1 {
		return nil
	}
	paramsStart := sequenceStart + len(tmsKittySequencePrefix)
	paramsEndRel := strings.IndexByte(line[paramsStart:], ';')
	if paramsEndRel == -1 {
		return nil
	}
	paramsStr := line[paramsStart : paramsStart+paramsEndRel]

	var ids []int
	rows := 1
	for _, param := range strings.Split(paramsStr, ",") {
		eq := strings.IndexByte(param, '=')
		if eq == -1 {
			continue
		}
		key := param[:eq]
		val := param[eq+1:]
		if val == "" {
			continue
		}
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil || n <= 0 || n > 0xffffffff {
			continue
		}
		switch key {
		case "i":
			ids = append(ids, int(n))
		case "r":
			rows = int(n)
		}
	}
	return &tmsKittyImageHeader{ids: ids, rows: rows}
}

func tmsExtractKittyImageIds(line string) []int {
	h := tmsParseKittyImageHeader(line)
	if h == nil {
		return nil
	}
	return h.ids
}

func tmsExtractKittyImageRows(line string) int {
	h := tmsParseKittyImageHeader(line)
	if h == nil {
		return 1
	}
	return h.rows
}

func tmsIsTermuxSession() bool {
	return os.Getenv("TERMUX_VERSION") != ""
}

// TuiMainScreenRenderState is a snapshot of the main-screen differential renderer.
// Field order matches captureRenderState.
type TuiMainScreenRenderState struct {
	PreviousLines       []string `json:"previousLines"`
	PreviousWidth       int      `json:"previousWidth"`
	PreviousHeight      int      `json:"previousHeight"`
	CursorRow           int      `json:"cursorRow"`
	HardwareCursorRow   int      `json:"hardwareCursorRow"`
	MaxLinesRendered    int      `json:"maxLinesRendered"`
	PreviousViewportTop int      `json:"previousViewportTop"`
}

// TuiMainScreen renders into the terminal's main screen and scrollback.
type TuiMainScreen struct {
	TuiBase
	previousLines         []string
	previousKittyImageIds *omap.Set[int]
	previousWidth         int
	previousHeight        int
	cursorRow             int
	hardwareCursorRow     int
	maxLinesRendered      int
	previousViewportTop   int
}

// NewTuiMainScreen constructs a regular-mode TUI.
// showHardwareCursor nil leaves the hardware cursor hidden.
// logDirectory nil disables debug logging and sends crash dumps to the OS temp directory.
func NewTuiMainScreen(terminal Terminal, showHardwareCursor *bool, logDirectory *string) *TuiMainScreen {
	s := &TuiMainScreen{
		TuiBase: TuiBase{
			Container:           *NewContainer(),
			terminal:            terminal,
			logDirectory:        logDirectory,
			overlayFocusRestore: &tuiOverlayFocusRestoreInactive{},
		},
		previousLines:         []string{},
		previousKittyImageIds: omap.NewSet[int](),
	}
	if showHardwareCursor != nil {
		s.showHardwareCursor = *showHardwareCursor
	}
	s.setOverride(s)
	return s
}

// Mode is the TUI mode, always regular.
func (s *TuiMainScreen) Mode() TuiMode { return TuiModeRegular }

// CaptureRenderState copies the differential-render cursors and previous frame.
func (s *TuiMainScreen) CaptureRenderState() *TuiMainScreenRenderState {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := make([]string, len(s.previousLines))
	copy(prev, s.previousLines)
	return &TuiMainScreenRenderState{
		PreviousLines:       prev,
		PreviousWidth:       s.previousWidth,
		PreviousHeight:      s.previousHeight,
		CursorRow:           s.cursorRow,
		HardwareCursorRow:   s.hardwareCursorRow,
		MaxLinesRendered:    s.maxLinesRendered,
		PreviousViewportTop: s.previousViewportTop,
	}
}

// RestoreRenderState installs a previously captured frame. Image lines are blanked.
func (s *TuiMainScreen) RestoreRenderState(state *TuiMainScreenRenderState) {
	if state == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	restored := make([]string, len(state.PreviousLines))
	for i, line := range state.PreviousLines {
		if IsImageLine(line) {
			restored[i] = ""
		} else {
			restored[i] = line
		}
	}
	s.previousLines = restored
	s.previousKittyImageIds = omap.NewSet[int]()
	s.previousWidth = state.PreviousWidth
	s.previousHeight = state.PreviousHeight
	s.cursorRow = state.CursorRow
	s.hardwareCursorRow = state.HardwareCursorRow
	s.maxLinesRendered = state.MaxLinesRendered
	s.previousViewportTop = state.PreviousViewportTop
}

func (s *TuiMainScreen) resetRenderState() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previousLines = []string{}
	s.previousWidth = -1
	s.previousHeight = -1
	s.cursorRow = 0
	s.hardwareCursorRow = 0
	s.maxLinesRendered = 0
	s.previousViewportTop = 0
}

func (s *TuiMainScreen) beforeTerminalStop(options *TuiStopOptions) {
	s.mu.Lock()
	preserve := options != nil && options.PreserveScreen != nil && *options.PreserveScreen
	prevLen := len(s.previousLines)
	hwCursorRow := s.hardwareCursorRow
	term := s.terminal
	s.mu.Unlock()

	if preserve || prevLen == 0 {
		return
	}
	term.Write(" ")
	targetRow := prevLen
	lineDiff := targetRow - hwCursorRow
	if lineDiff > 0 {
		term.Write(fmt.Sprintf("\x1b[%dB", lineDiff))
	} else if lineDiff < 0 {
		term.Write(fmt.Sprintf("\x1b[%dA", -lineDiff))
	}
	term.Write("\r\n")
}

func (s *TuiMainScreen) collectKittyImageIds(lines []string) *omap.Set[int] {
	ids := omap.NewSet[int]()
	for _, line := range lines {
		for _, id := range tmsExtractKittyImageIds(line) {
			ids.Add(id)
		}
	}
	return ids
}

func (s *TuiMainScreen) deleteKittyImages(ids *omap.Set[int]) string {
	if ids == nil || ids.Len() == 0 {
		return ""
	}
	var b strings.Builder
	for _, id := range ids.Values() {
		b.WriteString(DeleteKittyImage(id))
	}
	return b.String()
}

func (s *TuiMainScreen) getKittyImageReservedRows(lines []string, index int, maxIndex int) int {
	var line string
	if index >= 0 && index < len(lines) {
		line = lines[index]
	}
	rows := tmsExtractKittyImageRows(line)
	if rows <= 1 {
		return 1
	}

	maxRows := min(rows, maxIndex-index+1, len(lines)-index)
	reservedRows := 1
	for reservedRows < maxRows {
		nextLine := ""
		if index+reservedRows < len(lines) {
			nextLine = lines[index+reservedRows]
		}
		if IsImageLine(nextLine) || VisibleWidth(nextLine) > 0 {
			break
		}
		reservedRows++
	}
	return reservedRows
}

func (s *TuiMainScreen) expandChangedRangeForKittyImages(
	prevLines []string,
	firstChanged int,
	lastChanged int,
	newLines []string,
) (int, int) {
	expandedFirstChanged := firstChanged
	expandedLastChanged := lastChanged
	expandForLines := func(lines []string) {
		for i := 0; i < len(lines); i++ {
			if len(tmsExtractKittyImageIds(lines[i])) == 0 {
				continue
			}
			blockEnd := i + s.getKittyImageReservedRows(lines, i, len(lines)-1) - 1
			if i >= firstChanged || (i <= lastChanged && blockEnd >= firstChanged) {
				if i < expandedFirstChanged {
					expandedFirstChanged = i
				}
				if blockEnd > expandedLastChanged {
					expandedLastChanged = blockEnd
				}
			}
		}
	}

	expandForLines(prevLines)
	expandForLines(newLines)
	return expandedFirstChanged, expandedLastChanged
}

func (s *TuiMainScreen) deleteChangedKittyImages(prevLines []string, firstChanged, lastChanged int) string {
	if firstChanged < 0 || lastChanged < firstChanged {
		return ""
	}
	ids := omap.NewSet[int]()
	maxLine := min(lastChanged, len(prevLines)-1)
	for i := firstChanged; i <= maxLine; i++ {
		for _, id := range tmsExtractKittyImageIds(prevLines[i]) {
			ids.Add(id)
		}
	}
	return s.deleteKittyImages(ids)
}

func (s *TuiMainScreen) doRender() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	term := s.terminal
	prevLines := make([]string, len(s.previousLines))
	copy(prevLines, s.previousLines)
	prevKittyIds := omap.NewSet[int]()
	if s.previousKittyImageIds != nil {
		for _, id := range s.previousKittyImageIds.Values() {
			prevKittyIds.Add(id)
		}
	}
	prevWidth := s.previousWidth
	prevHeight := s.previousHeight
	stateCursorRow := s.cursorRow
	stateHwCursorRow := s.hardwareCursorRow
	stateMaxLinesRendered := s.maxLinesRendered
	statePrevViewportTop := s.previousViewportTop
	logDir := s.logDirectory
	clearOnShrink := s.clearOnShrink
	hasOverlays := len(s.overlayStack) > 0
	s.mu.Unlock()

	width := term.Columns()
	height := term.Rows()
	widthChanged := prevWidth != 0 && prevWidth != width
	heightChanged := prevHeight != 0 && prevHeight != height
	previousBufferLength := height
	if prevHeight > 0 {
		previousBufferLength = statePrevViewportTop + prevHeight
	}
	prevViewportTop := statePrevViewportTop
	if heightChanged {
		prevViewportTop = max(0, previousBufferLength-height)
	}
	viewportTop := prevViewportTop
	hardwareCursorRow := stateHwCursorRow
	computeLineDiff := func(targetRow int) int {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		targetScreenRow := targetRow - viewportTop
		return targetScreenRow - currentScreenRow
	}

	// Render all components to get new lines.
	// Resolve fake cursors before compositing so overlays and line slicing see plain SGR codes.
	// Drop OSC 133 markers: the terminal reads 133;A as a new shell prompt, which ends program status (OSC 7501).
	rendered := s.Render(width)
	stripped := make([]string, len(rendered))
	for i, line := range rendered {
		stripped[i] = tmsOsc133ZonePrefix.ReplaceAllString(line, "")
	}
	newLines := s.resolveFakeCursors(stripped)

	// Composite overlays into the rendered lines (before differential compare).
	if s.HasOverlayEntries() {
		newLines = s.compositeOverlays(newLines, width, height)
		hasOverlays = true
	}

	// Extract cursor position before applying line resets (marker must be found first).
	cursorPos := s.extractCursorPosition(newLines, height)
	newLines = s.applyLineResets(newLines)

	commitState := func(
		nextLines []string,
		nextCursorRow int,
		nextHwCursorRow int,
		nextMaxLines int,
		nextViewportTop int,
		nextWidth int,
		nextHeight int,
	) {
		nextKittyIds := s.collectKittyImageIds(nextLines)
		s.mu.Lock()
		s.previousLines = nextLines
		s.previousKittyImageIds = nextKittyIds
		s.previousWidth = nextWidth
		s.previousHeight = nextHeight
		s.cursorRow = nextCursorRow
		s.hardwareCursorRow = nextHwCursorRow
		s.maxLinesRendered = nextMaxLines
		s.previousViewportTop = nextViewportTop
		s.mu.Unlock()
	}

	// Helper to clear scrollback and viewport and render all new lines.
	fullRender := func(clear bool) {
		s.mu.Lock()
		s.fullRedrawCount++
		s.mu.Unlock()

		output := &tmsBoundedTerminalWriter{write: term.Write}
		output.append("\x1b[?2026h") // Begin synchronized output
		if clear {
			output.append(s.deleteKittyImages(prevKittyIds))
			output.append("\x1b[2J\x1b[H\x1b[3J") // Clear screen, home, then clear scrollback
		}
		for i := 0; i < len(newLines); i++ {
			if i > 0 {
				output.append("\r\n")
			}
			line := newLines[i]
			isImage := IsImageLine(line)
			imageReservedRows := 1
			if isImage {
				imageReservedRows = s.getKittyImageReservedRows(newLines, i, len(newLines)-1)
			}
			if imageReservedRows > 1 && imageReservedRows <= height {
				for row := 1; row < imageReservedRows; row++ {
					output.append("\r\n")
				}
				output.append(fmt.Sprintf("\x1b[%dA", imageReservedRows-1))
				output.append(line)
				output.append(fmt.Sprintf("\x1b[%dB", imageReservedRows-1))
				i += imageReservedRows - 1
				continue
			}
			output.append(line)
		}
		output.append("\x1b[?2026l") // End synchronized output
		output.flush()

		nextCursorRow := max(0, len(newLines)-1)
		nextHwCursorRow := nextCursorRow
		nextMaxLines := max(stateMaxLinesRendered, len(newLines))
		if clear {
			nextMaxLines = len(newLines)
		}
		bufferLength := max(height, len(newLines))
		nextViewportTop := max(0, bufferLength-height)

		nextHwCursorRow = s.positionHardwareCursor(term, cursorPos, len(newLines), nextHwCursorRow)
		commitState(newLines, nextCursorRow, nextHwCursorRow, nextMaxLines, nextViewportTop, width, height)
	}

	var redrawLogDir *string
	if os.Getenv("PI_TUI_DEBUG_REDRAW") == "1" {
		redrawLogDir = logDir
	}
	logRedraw := func(reason string) {
		if redrawLogDir == nil {
			return
		}
		logPath := filepath.Join(*redrawLogDir, "pi-tui-debug.log")
		msg := fmt.Sprintf("[%s] fullRender: %s (prev=%d, new=%d, height=%d)\n",
			time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), reason, len(prevLines), len(newLines), height)
		_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_, _ = f.WriteString(msg)
			_ = f.Close()
		}
	}

	// First render - just output everything without clearing (assumes clean screen).
	if len(prevLines) == 0 && !widthChanged && !heightChanged {
		logRedraw("first render")
		fullRender(false)
		return
	}

	// Width changes always need a full re-render because wrapping changes.
	if widthChanged {
		logRedraw(fmt.Sprintf("terminal width changed (%d -> %d)", prevWidth, width))
		fullRender(true)
		return
	}

	// Height changes normally need a full re-render to keep the visible viewport aligned,
	// but Termux changes height when the software keyboard shows or hides.
	if heightChanged && !tmsIsTermuxSession() {
		logRedraw(fmt.Sprintf("terminal height changed (%d -> %d)", prevHeight, height))
		fullRender(true)
		return
	}

	// Content shrunk below the working area and no overlays - re-render to clear empty rows.
	if clearOnShrink && len(newLines) < stateMaxLinesRendered && !hasOverlays {
		logRedraw(fmt.Sprintf("clearOnShrink (maxLinesRendered=%d)", stateMaxLinesRendered))
		fullRender(true)
		return
	}

	// Find first and last changed lines.
	firstChanged := -1
	lastChanged := -1
	maxLines := max(len(newLines), len(prevLines))
	for i := 0; i < maxLines; i++ {
		oldLine := ""
		if i < len(prevLines) {
			oldLine = prevLines[i]
		}
		newLine := ""
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(prevLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(prevLines)
		}
		lastChanged = len(newLines) - 1
	}
	if firstChanged != -1 {
		firstChanged, lastChanged = s.expandChangedRangeForKittyImages(prevLines, firstChanged, lastChanged, newLines)
	}
	appendStart := appendedLines && firstChanged == len(prevLines) && firstChanged > 0

	// No changes - but still need to update hardware cursor position if it moved.
	if firstChanged == -1 {
		nextHwCursorRow := s.positionHardwareCursor(term, cursorPos, len(newLines), stateHwCursorRow)
		s.mu.Lock()
		s.hardwareCursorRow = nextHwCursorRow
		s.previousViewportTop = prevViewportTop
		s.previousHeight = height
		s.mu.Unlock()
		return
	}

	// All changes are in deleted lines (nothing to render, just clear).
	if firstChanged >= len(newLines) {
		nextCursorRow := stateCursorRow
		nextHwCursorRow := stateHwCursorRow
		if len(prevLines) > len(newLines) {
			output := &tmsBoundedTerminalWriter{write: term.Write}
			output.append("\x1b[?2026h")
			output.append(s.deleteChangedKittyImages(prevLines, firstChanged, lastChanged))
			targetRow := max(0, len(newLines)-1)
			if targetRow < prevViewportTop {
				logRedraw(fmt.Sprintf("deleted lines moved viewport up (%d < %d)", targetRow, prevViewportTop))
				fullRender(true)
				return
			}
			lineDiff := computeLineDiff(targetRow)
			if lineDiff > 0 {
				output.append(fmt.Sprintf("\x1b[%dB", lineDiff))
			} else if lineDiff < 0 {
				output.append(fmt.Sprintf("\x1b[%dA", -lineDiff))
			}
			output.append("\r")
			extraLines := len(prevLines) - len(newLines)
			if extraLines > height {
				logRedraw(fmt.Sprintf("extraLines > height (%d > %d)", extraLines, height))
				fullRender(true)
				return
			}
			clearStartOffset := 1
			if len(newLines) == 0 {
				clearStartOffset = 0
			}
			if extraLines > 0 && clearStartOffset > 0 {
				output.append(fmt.Sprintf("\x1b[%dB", clearStartOffset))
			}
			for i := 0; i < extraLines; i++ {
				output.append("\r\x1b[2K")
				if i < extraLines-1 {
					output.append("\x1b[1B")
				}
			}
			moveBack := max(0, extraLines-1+clearStartOffset)
			if moveBack > 0 {
				output.append(fmt.Sprintf("\x1b[%dA", moveBack))
			}
			output.append("\x1b[?2026l")
			output.flush()
			nextCursorRow = targetRow
			nextHwCursorRow = targetRow
		}
		nextHwCursorRow = s.positionHardwareCursor(term, cursorPos, len(newLines), nextHwCursorRow)
		commitState(newLines, nextCursorRow, nextHwCursorRow, stateMaxLinesRendered, prevViewportTop, width, height)
		return
	}

	// Differential rendering can only touch what was actually visible.
	if firstChanged < prevViewportTop {
		logRedraw(fmt.Sprintf("firstChanged < viewportTop (%d < %d)", firstChanged, prevViewportTop))
		fullRender(true)
		return
	}

	// Render from first changed line to end.
	output := &tmsBoundedTerminalWriter{write: term.Write}
	output.append("\x1b[?2026h") // Begin synchronized output
	output.append(s.deleteChangedKittyImages(prevLines, firstChanged, lastChanged))
	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := max(0, min(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreenRow
		if moveToBottom > 0 {
			output.append(fmt.Sprintf("\x1b[%dB", moveToBottom))
		}
		scroll := moveTargetRow - prevViewportBottom
		output.append(strings.Repeat("\r\n", scroll))
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	// Move cursor to first changed line (use hardwareCursorRow for actual position).
	lineDiff := computeLineDiff(moveTargetRow)
	if lineDiff > 0 {
		output.append(fmt.Sprintf("\x1b[%dB", lineDiff))
	} else if lineDiff < 0 {
		output.append(fmt.Sprintf("\x1b[%dA", -lineDiff))
	}
	if appendStart {
		output.append("\r\n")
	} else {
		output.append("\r")
	}

	// Only render changed lines (firstChanged to lastChanged), not all lines to end.
	renderEnd := min(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			output.append("\r\n")
		}
		line := newLines[i]
		isImage := IsImageLine(line)
		imageReservedRows := 1
		if isImage {
			imageReservedRows = s.getKittyImageReservedRows(newLines, i, renderEnd)
		}
		if imageReservedRows > 1 {
			imageStartScreenRow := i - viewportTop
			if imageStartScreenRow < 0 || imageStartScreenRow+imageReservedRows > height {
				logRedraw(fmt.Sprintf("kitty image pre-clear would scroll (%d + %d > %d)",
					imageStartScreenRow, imageReservedRows, height))
				fullRender(true)
				return
			}
			output.append("\x1b[2K")
			for row := 1; row < imageReservedRows; row++ {
				output.append("\r\n\x1b[2K")
			}
			output.append(fmt.Sprintf("\x1b[%dA", imageReservedRows-1))
			output.append(line)
			output.append(fmt.Sprintf("\x1b[%dB", imageReservedRows-1))
			i += imageReservedRows - 1
			continue
		}

		output.append("\x1b[2K")
		if !isImage && VisibleWidth(line) > width {
			crashDir := os.TempDir()
			if logDir != nil {
				crashDir = *logDir
			}
			crashLogPath := filepath.Join(crashDir, "pi-tui-crash.log")
			var crashBuf strings.Builder
			fmt.Fprintf(&crashBuf, "Crash at %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
			fmt.Fprintf(&crashBuf, "Terminal width: %d\n", width)
			fmt.Fprintf(&crashBuf, "Line %d visible width: %d\n\n", i, VisibleWidth(line))
			crashBuf.WriteString("=== All rendered lines ===\n")
			for idx, l := range newLines {
				fmt.Fprintf(&crashBuf, "[%d] (w=%d) %s\n", idx, VisibleWidth(l), l)
			}
			crashBuf.WriteByte('\n')
			_ = os.MkdirAll(filepath.Dir(crashLogPath), 0o755)
			_ = os.WriteFile(crashLogPath, []byte(crashBuf.String()), 0o644)

			// Clean up terminal state before panicking.
			s.Stop(nil)

			errorMsg := strings.Join([]string{
				fmt.Sprintf("Rendered line %d exceeds terminal width (%d > %d).", i, VisibleWidth(line), width),
				"",
				"This is likely caused by a custom TUI component not truncating its output.",
				"Use visibleWidth() to measure and truncateToWidth() to truncate lines.",
				"",
				fmt.Sprintf("Debug log written to: %s", crashLogPath),
			}, "\n")
			panic(errors.New(errorMsg))
		}
		output.append(line)
	}

	// Track where cursor ended up after rendering.
	finalCursorRow := renderEnd

	// If we had more lines before, clear them and move cursor back.
	if len(prevLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			output.append(fmt.Sprintf("\x1b[%dB", moveDown))
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(prevLines) - len(newLines)
		for i := len(newLines); i < len(prevLines); i++ {
			output.append("\r\n\x1b[2K")
		}
		output.append(fmt.Sprintf("\x1b[%dA", extraLines))
	}

	output.append("\x1b[?2026l") // End synchronized output

	if os.Getenv("PI_TUI_DEBUG") == "1" {
		debugDir := "/tmp/tui"
		_ = os.MkdirAll(debugDir, 0o755)
		debugPath := filepath.Join(debugDir, fmt.Sprintf("render-%d-%d.log", time.Now().UnixMilli(), time.Now().UnixNano()%1_000_000))
		cursorJSON, _ := jsonx.Stringify(cursorPos)
		newLinesJSON, _ := jsonx.StringifyIndent(newLines, "  ")
		prevLinesJSON, _ := jsonx.StringifyIndent(prevLines, "  ")
		debugData := strings.Join([]string{
			fmt.Sprintf("firstChanged: %d", firstChanged),
			fmt.Sprintf("viewportTop: %d", viewportTop),
			fmt.Sprintf("cursorRow: %d", stateCursorRow),
			fmt.Sprintf("height: %d", height),
			fmt.Sprintf("lineDiff: %d", lineDiff),
			fmt.Sprintf("hardwareCursorRow: %d", hardwareCursorRow),
			fmt.Sprintf("renderEnd: %d", renderEnd),
			fmt.Sprintf("finalCursorRow: %d", finalCursorRow),
			fmt.Sprintf("cursorPos: %s", cursorJSON),
			fmt.Sprintf("newLines.length: %d", len(newLines)),
			fmt.Sprintf("previousLines.length: %d", len(prevLines)),
			"",
			"=== newLines ===",
			newLinesJSON,
			"",
			"=== previousLines ===",
			prevLinesJSON,
			"",
			"=== buffer ===",
			fmt.Sprintf("[%d chars written in bounded chunks]", output.length()),
		}, "\n")
		_ = os.WriteFile(debugPath, []byte(debugData), 0o644)
	}

	output.flush()

	nextCursorRow := max(0, len(newLines)-1)
	nextMaxLines := max(stateMaxLinesRendered, len(newLines))
	nextViewportTop := max(prevViewportTop, finalCursorRow-height+1)
	nextHwCursorRow := s.positionHardwareCursor(term, cursorPos, len(newLines), finalCursorRow)
	commitState(newLines, nextCursorRow, nextHwCursorRow, nextMaxLines, nextViewportTop, width, height)
}

func (s *TuiMainScreen) positionHardwareCursor(
	term Terminal,
	cursorPos *tuiCursorPosition,
	totalLines int,
	currentHwCursorRow int,
) int {
	if cursorPos == nil || totalLines <= 0 {
		term.HideCursor()
		return currentHwCursorRow
	}

	targetRow := max(0, min(cursorPos.Row, totalLines-1))
	targetCol := max(0, cursorPos.Col)

	rowDelta := targetRow - currentHwCursorRow
	var buf strings.Builder
	if rowDelta > 0 {
		fmt.Fprintf(&buf, "\x1b[%dB", rowDelta)
	} else if rowDelta < 0 {
		fmt.Fprintf(&buf, "\x1b[%dA", -rowDelta)
	}
	fmt.Fprintf(&buf, "\x1b[%dG", targetCol+1)

	if buf.Len() > 0 {
		term.Write(buf.String())
	}

	if s.GetShowHardwareCursor() {
		term.ShowCursor()
	} else {
		term.HideCursor()
	}
	return targetRow
}

var (
	_ TUI         = (*TuiMainScreen)(nil)
	_ tuiOverride = (*TuiMainScreen)(nil)
)
