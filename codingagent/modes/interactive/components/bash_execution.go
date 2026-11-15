// Ported from packages/coding-agent/src/modes/interactive/components/bash-execution.ts (pi v1.5.2).

package components

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

const bePreviewLines = 20

// BashExecutionComponent renders a user-initiated bash command with live output, collapsed preview, and exit status.
type BashExecutionComponent struct {
	*tui.Container
	command            string
	ui                 tui.TUI
	excludeFromContext bool

	mu               sync.Mutex
	outputPad        int
	expanded         bool
	state            string // "running" | "complete"
	outputLines      []string
	rawOutput        strings.Builder
	exitCode         *int
	cancelled        bool
	truncationResult *core.TruncationResult
	fullOutputPath   *string

	contentContainer *tui.Container
	loader           *tui.Loader
}

// NewBashExecutionComponent creates a BashExecutionComponent for a running command.
func NewBashExecutionComponent(command string, ui tui.TUI, excludeFromContext bool, outputPad ...int) *BashExecutionComponent {
	pad := 1
	if len(outputPad) > 0 {
		pad = max(0, outputPad[0])
	}

	root := tui.NewContainer()
	contentContainer := tui.NewContainer()

	colorFn := func(text string) string {
		if excludeFromContext {
			return theme.CurrentTheme().Fg(theme.FgDim, text)
		}
		return theme.CurrentTheme().Fg(theme.FgBashMode, text)
	}

	root.AddChild(tui.NewSpacer(1))
	root.AddChild(NewDynamicBorder(colorFn))
	root.AddChild(contentContainer)
	root.AddChild(NewDynamicBorder(colorFn))

	var loader *tui.Loader
	if ui != nil {
		loader = tui.NewLoader(
			ui,
			func(s string) string {
				if excludeFromContext {
					return theme.CurrentTheme().Fg(theme.FgDim, s)
				}
				return theme.CurrentTheme().Fg(theme.FgBashMode, s)
			},
			func(s string) string {
				return theme.CurrentTheme().Fg(theme.FgMuted, s)
			},
			"Running...",
			nil,
		)
	}

	b := &BashExecutionComponent{
		Container:          root,
		command:            command,
		ui:                 ui,
		excludeFromContext: excludeFromContext,
		outputPad:          pad,
		state:              "running",
		contentContainer:   contentContainer,
		loader:             loader,
	}
	b.rebuildLocked()
	if loader != nil {
		loader.Start()
	}
	return b
}

// SetExpanded toggles between collapsed preview (20 visual lines) and full expanded output.
func (b *BashExecutionComponent) SetExpanded(expanded bool) {
	b.mu.Lock()
	if b.expanded == expanded {
		b.mu.Unlock()
		return
	}
	b.expanded = expanded
	b.rebuildLocked()
	b.mu.Unlock()
}

// SetOutputPad updates horizontal padding applied to text rows inside the borders.
func (b *BashExecutionComponent) SetOutputPad(padding int) {
	b.mu.Lock()
	b.outputPad = max(0, padding)
	b.rebuildLocked()
	b.mu.Unlock()
}

// AppendOutput appends a chunk of sanitized stdout/stderr output.
func (b *BashExecutionComponent) AppendOutput(chunk string) {
	if chunk == "" {
		return
	}
	clean := utils.SanitizeBinaryOutput(strings.ReplaceAll(chunk, "\r", ""))
	clean = utils.StripAnsi(clean)

	b.mu.Lock()
	b.rawOutput.WriteString(clean)
	parts := strings.Split(clean, "\n")
	if len(b.outputLines) == 0 {
		b.outputLines = parts
	} else {
		b.outputLines[len(b.outputLines)-1] += parts[0]
		b.outputLines = append(b.outputLines, parts[1:]...)
	}
	b.rebuildLocked()
	b.mu.Unlock()
}

// SetComplete marks the bash command complete and stops the spinner.
func (b *BashExecutionComponent) SetComplete(
	exitCode *int,
	cancelled bool,
	truncationResult *core.TruncationResult,
	fullOutputPath *string,
) {
	if b.loader != nil {
		b.loader.Stop()
	}
	b.mu.Lock()
	b.state = "complete"
	if exitCode != nil {
		code := *exitCode
		b.exitCode = &code
	} else {
		b.exitCode = nil
	}
	b.cancelled = cancelled
	b.truncationResult = truncationResult
	b.fullOutputPath = fullOutputPath
	b.rebuildLocked()
	b.mu.Unlock()
}

// GetOutput returns the collected plain command output.
func (b *BashExecutionComponent) GetOutput() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rawOutput.String()
}

// GetCommand returns the executed command string.
func (b *BashExecutionComponent) GetCommand() string {
	return b.command
}

// Invalidate drops cached rendering state.
func (b *BashExecutionComponent) Invalidate() {
	b.Container.Invalidate()
}

// Dispose stops any active spinner loader goroutine.
func (b *BashExecutionComponent) Dispose() {
	if b.loader != nil {
		b.loader.Stop()
	}
}

// Render renders the command header, dynamic-width preview or expanded output, status, and borders.
func (b *BashExecutionComponent) Render(width int) []string {
	b.mu.Lock()
	b.syncPreviewForWidthLocked(width)
	b.mu.Unlock()
	return b.Container.Render(width)
}

func (b *BashExecutionComponent) rebuildLocked() {
	b.contentContainer.Clear()

	th := theme.CurrentTheme()
	headerColor := theme.FgBashMode
	if b.excludeFromContext {
		headerColor = theme.FgDim
	}

	headerText := th.Fg(headerColor, th.Bold(fmt.Sprintf("$ %s", b.command)))
	b.contentContainer.AddChild(tui.NewText(headerText, b.outputPad, 0, nil))

	lines := b.displayLinesLocked()
	if len(lines) > 0 {
		b.contentContainer.AddChild(tui.NewSpacer(1))
		styled := make([]string, len(lines))
		for i, line := range lines {
			styled[i] = th.Fg(theme.FgToolOutput, line)
		}
		if b.expanded {
			b.contentContainer.AddChild(tui.NewText(strings.Join(styled, "\n"), b.outputPad, 0, nil))
		} else {
			// Preview component is replaced for render-time width in syncPreviewForWidthLocked.
			b.contentContainer.AddChild(&beCollapsedPreview{
				owner:  b,
				styled: strings.Join(styled, "\n"),
			})
		}
	}

	if b.state == "running" {
		if b.loader != nil {
			b.contentContainer.AddChild(b.loader)
		}
		return
	}

	var statusLines []string
	if b.cancelled {
		statusLines = append(statusLines, th.Fg(theme.FgWarning, "(cancelled)"))
	} else if b.exitCode != nil && *b.exitCode != 0 {
		statusLines = append(statusLines, th.Fg(theme.FgError, fmt.Sprintf("(exit %d)", *b.exitCode)))
	}

	if b.truncationResult != nil && b.truncationResult.Truncated {
		var notice string
		switch b.truncationResult.TruncatedBy.OrElse("") {
		case "lines":
			notice = fmt.Sprintf("Output truncated: showing %d of %d lines", b.truncationResult.OutputLines, b.truncationResult.TotalLines)
		case "bytes":
			notice = fmt.Sprintf("Output truncated: %s limit", core.FormatSize(b.truncationResult.MaxBytes))
		default:
			notice = "Output truncated"
		}
		if b.fullOutputPath != nil && *b.fullOutputPath != "" {
			notice += fmt.Sprintf(" (full output: %s)", *b.fullOutputPath)
		}
		statusLines = append(statusLines, th.Fg(theme.FgWarning, notice))
	}

	if len(statusLines) > 0 {
		b.contentContainer.AddChild(tui.NewText(strings.Join(statusLines, "\n"), b.outputPad, 0, nil))
	}
}

func (b *BashExecutionComponent) displayLinesLocked() []string {
	lines := slices.Clone(b.outputLines)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (b *BashExecutionComponent) syncPreviewForWidthLocked(_ int) {
	// Collapsed preview wraps lazily per render width inside beCollapsedPreview.Render.
}

type beCollapsedPreview struct {
	owner  *BashExecutionComponent
	styled string
}

func (p *beCollapsedPreview) Invalidate() {}

func (p *beCollapsedPreview) Render(width int) []string {
	p.owner.mu.Lock()
	outputPad := p.owner.outputPad
	p.owner.mu.Unlock()

	trunc := theme.TruncateToVisualLines(p.styled, bePreviewLines, width, outputPad)
	th := theme.CurrentTheme()
	var lines []string
	if trunc.SkippedCount > 0 {
		hint := fmt.Sprintf("... (%d earlier lines, %s)", trunc.SkippedCount, theme.AppKeyHint(th, core.KeybindingAppToolsExpand, "to expand"))
		lines = append(lines, tui.NewText(th.Fg(theme.FgMuted, hint), outputPad, 0, nil).Render(width)...)
	}
	if len(trunc.VisualLines) > 0 {
		body := strings.Join(trunc.VisualLines, "\n")
		lines = append(lines, tui.NewText(body, outputPad, 0, nil).Render(width)...)
	}
	return lines
}
