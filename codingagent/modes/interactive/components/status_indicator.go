// Ported from packages/coding-agent/src/modes/interactive/components/status-indicator.ts (pi v1.5.2).

package components

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// StatusIndicatorKind identifies the semantic category of an active status indicator.
type StatusIndicatorKind string

const (
	StatusIndicatorKindWorking       StatusIndicatorKind = "working"
	StatusIndicatorKindRetry         StatusIndicatorKind = "retry"
	StatusIndicatorKindCompaction    StatusIndicatorKind = "compaction"
	StatusIndicatorKindBranchSummary StatusIndicatorKind = "branchSummary"
)

// CompactionStatusReason describes why context compaction started.
type CompactionStatusReason string

const (
	CompactionStatusReasonManual    CompactionStatusReason = "manual"
	CompactionStatusReasonThreshold CompactionStatusReason = "threshold"
	CompactionStatusReasonOverflow  CompactionStatusReason = "overflow"
)

func siToLoaderOptions(indicator *core.WorkingIndicatorOptions) *tui.LoaderIndicatorOptions {
	if indicator == nil {
		return nil
	}
	return &tui.LoaderIndicatorOptions{
		Frames:     indicator.Frames,
		IntervalMs: indicator.IntervalMs,
	}
}

// StatusIndicator is a Loader-backed animated status row that can render standalone or inside an editor border.
type StatusIndicator struct {
	*tui.Loader

	kind StatusIndicatorKind

	mu             sync.Mutex
	ui             tui.TUI
	message        string
	messageColorFn func(string) string
	text           *tui.Text
	lastDisplay    string
	hasLastDisplay bool
}

// NewStatusIndicator creates a StatusIndicator whose animation is driven by an embedded tui.Loader.
func NewStatusIndicator(
	kind StatusIndicatorKind,
	ui tui.TUI,
	spinnerColorFn func(string) string,
	messageColorFn func(string) string,
	message string,
	indicator *core.WorkingIndicatorOptions,
) *StatusIndicator {
	if spinnerColorFn == nil {
		spinnerColorFn = func(s string) string { return theme.CurrentTheme().Fg(theme.FgAccent, s) }
	}
	if messageColorFn == nil {
		messageColorFn = func(s string) string { return theme.CurrentTheme().Fg(theme.FgMuted, s) }
	}
	// Keep the underlying Loader's message empty so Loader.Render isolates the current rendered spinner glyph.
	loader := tui.NewLoader(ui, spinnerColorFn, func(string) string { return "" }, "", siToLoaderOptions(indicator))
	return &StatusIndicator{
		Loader:         loader,
		kind:           kind,
		ui:             ui,
		message:        message,
		messageColorFn: messageColorFn,
		text:           tui.NewText("", 1, 0, nil),
	}
}

// Kind returns the indicator category.
func (s *StatusIndicator) Kind() StatusIndicatorKind {
	return s.kind
}

// SetMessage updates the status label and requests a render.
func (s *StatusIndicator) SetMessage(message string) {
	s.mu.Lock()
	s.message = message
	s.hasLastDisplay = false
	ui := s.ui
	s.mu.Unlock()
	s.Invalidate()
	if ui != nil {
		ui.RequestRender(nil)
	}
}

// Invalidate clears cached text layout for the indicator.
func (s *StatusIndicator) Invalidate() {
	s.Loader.Invalidate()
	s.mu.Lock()
	s.hasLastDisplay = false
	s.mu.Unlock()
	s.text.Invalidate()
}

func (s *StatusIndicator) renderedIndicator() string {
	lines := s.Loader.Render(256)
	var line string
	if len(lines) > 1 {
		line = lines[1]
	} else if len(lines) == 1 {
		line = lines[0]
	}
	// Loader.Text uses paddingX=1, so strip the single leading space and right-padding spaces.
	line = strings.TrimPrefix(line, " ")
	return strings.TrimRight(line, " ")
}

func (s *StatusIndicator) updateRenderText() {
	spinner := s.renderedIndicator()
	s.mu.Lock()
	msg := s.message
	msgColorFn := s.messageColorFn
	s.mu.Unlock()

	var styledMsg string
	if msgColorFn != nil {
		styledMsg = msgColorFn(msg)
	} else {
		styledMsg = msg
	}
	display := styledMsg
	if spinner != "" {
		display = spinner + " " + styledMsg
	}

	s.mu.Lock()
	changed := !s.hasLastDisplay || s.lastDisplay != display
	if changed {
		s.lastDisplay = display
		s.hasLastDisplay = true
	}
	s.mu.Unlock()

	if changed {
		s.text.SetText(display)
	}
}

// Render returns a blank top line followed by the padded indicator and message lines.
func (s *StatusIndicator) Render(width int) []string {
	s.updateRenderText()
	body := s.text.Render(width)
	out := make([]string, 0, 1+len(body))
	out = append(out, "")
	out = append(out, body...)
	return out
}

// RenderInBorder returns the single-line spinner + message formatted to fit inside an editor border.
func (s *StatusIndicator) RenderInBorder(width int) string {
	lines := s.Render(width + 2)
	var line string
	if len(lines) > 1 {
		line = lines[1]
	}
	trimmed := strings.TrimRight(line, " ")
	trimmed = strings.TrimPrefix(trimmed, " ")
	return tui.TruncateToWidth(trimmed, width, "", false)
}

// RenderSpinnerInBorder returns only the styled spinner glyph truncated to width.
func (s *StatusIndicator) RenderSpinnerInBorder(width int) string {
	return tui.TruncateToWidth(s.renderedIndicator(), width, "", false)
}

// Dispose stops the spinner animation ticker.
func (s *StatusIndicator) Dispose() {
	s.Stop()
}

// WorkingStatusIndicator displays the active agent turn status.
type WorkingStatusIndicator struct {
	*StatusIndicator
}

// NewWorkingStatusIndicator creates a working indicator with optional custom frames and color function.
func NewWorkingStatusIndicator(
	ui tui.TUI,
	message string,
	indicator *core.WorkingIndicatorOptions,
	colorFn ...func(string) string,
) *WorkingStatusIndicator {
	var custom func(string) string
	if len(colorFn) > 0 {
		custom = colorFn[0]
	}
	spinnerFn := custom
	if spinnerFn == nil {
		spinnerFn = func(s string) string { return theme.CurrentTheme().Fg(theme.FgAccent, s) }
	}
	msgFn := custom
	if msgFn == nil {
		msgFn = func(s string) string { return theme.CurrentTheme().Fg(theme.FgMuted, s) }
	}
	return &WorkingStatusIndicator{
		StatusIndicator: NewStatusIndicator(StatusIndicatorKindWorking, ui, spinnerFn, msgFn, message, indicator),
	}
}

// RetryStatusIndicator displays an automatic retry countdown.
type RetryStatusIndicator struct {
	*StatusIndicator
	mu        sync.Mutex
	countdown *CountdownTimer
}

// NewRetryStatusIndicator creates a retry countdown indicator and starts ticking down delayMs.
func NewRetryStatusIndicator(ui tui.TUI, attempt, maxAttempts int, delayMs int64) *RetryStatusIndicator {
	retryMessage := func(seconds int) string {
		return fmt.Sprintf(
			"Retrying (%d/%d) in %ds... (%s to cancel)",
			attempt,
			maxAttempts,
			seconds,
			theme.KeyText(core.KeybindingAppInterrupt),
		)
	}
	si := NewStatusIndicator(
		StatusIndicatorKindRetry,
		ui,
		func(spinner string) string { return theme.CurrentTheme().Fg(theme.FgWarning, spinner) },
		func(text string) string { return theme.CurrentTheme().Fg(theme.FgMuted, text) },
		retryMessage(int(math.Ceil(float64(delayMs)/1000.0))),
		nil,
	)
	r := &RetryStatusIndicator{StatusIndicator: si}
	r.countdown = NewCountdownTimer(
		delayMs,
		ui,
		func(seconds int) {
			r.SetMessage(retryMessage(seconds))
		},
		func() {
			r.mu.Lock()
			r.countdown = nil
			r.mu.Unlock()
		},
	)
	return r
}

// Dispose stops both the countdown timer and the spinner loader.
func (r *RetryStatusIndicator) Dispose() {
	r.mu.Lock()
	cd := r.countdown
	r.countdown = nil
	r.mu.Unlock()
	if cd != nil {
		cd.Dispose()
	}
	r.StatusIndicator.Dispose()
}

// CompactionStatusIndicator displays context compaction progress.
type CompactionStatusIndicator struct {
	*StatusIndicator
}

// NewCompactionStatusIndicator creates an indicator for manual, threshold, or overflow compaction.
func NewCompactionStatusIndicator(ui tui.TUI, reason CompactionStatusReason, colorFn ...func(string) string) *CompactionStatusIndicator {
	cancelHint := fmt.Sprintf("(%s to cancel)", theme.KeyText(core.KeybindingAppInterrupt))
	var label string
	switch reason {
	case CompactionStatusReasonManual:
		label = "Compacting context... " + cancelHint
	case CompactionStatusReasonOverflow:
		label = "Context overflow detected, Auto-compacting... " + cancelHint
	default:
		label = "Auto-compacting... " + cancelHint
	}

	var custom func(string) string
	if len(colorFn) > 0 {
		custom = colorFn[0]
	}
	spinnerFn := custom
	if spinnerFn == nil {
		spinnerFn = func(spinner string) string { return theme.CurrentTheme().Fg(theme.FgAccent, spinner) }
	}
	msgFn := custom
	if msgFn == nil {
		msgFn = func(text string) string { return theme.CurrentTheme().Fg(theme.FgMuted, text) }
	}
	return &CompactionStatusIndicator{
		StatusIndicator: NewStatusIndicator(StatusIndicatorKindCompaction, ui, spinnerFn, msgFn, label, nil),
	}
}

// BranchSummaryStatusIndicator displays branch summarization progress.
type BranchSummaryStatusIndicator struct {
	*StatusIndicator
}

// NewBranchSummaryStatusIndicator creates a branch summary status indicator.
func NewBranchSummaryStatusIndicator(ui tui.TUI) *BranchSummaryStatusIndicator {
	label := fmt.Sprintf("Summarizing branch... (%s to cancel)", theme.KeyText(core.KeybindingAppInterrupt))
	return &BranchSummaryStatusIndicator{
		StatusIndicator: NewStatusIndicator(
			StatusIndicatorKindBranchSummary,
			ui,
			func(spinner string) string { return theme.CurrentTheme().Fg(theme.FgAccent, spinner) },
			func(text string) string { return theme.CurrentTheme().Fg(theme.FgMuted, text) },
			label,
			nil,
		),
	}
}

// IdleStatus preserves two lines of vertical spacing matching standalone StatusIndicator height.
type IdleStatus struct{}

var _ tui.Component = (*IdleStatus)(nil)

// NewIdleStatus returns an IdleStatus placeholder component.
func NewIdleStatus() *IdleStatus {
	return &IdleStatus{}
}

// Invalidate is a no-op because IdleStatus keeps no cached state.
func (i *IdleStatus) Invalidate() {}

// Render returns two empty space-filled lines of the requested width.
func (i *IdleStatus) Render(width int) []string {
	emptyLine := strings.Repeat(" ", max(0, width))
	return []string{emptyLine, emptyLine}
}
