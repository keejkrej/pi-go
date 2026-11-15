// Ported from packages/coding-agent/src/modes/interactive/components/visual-truncate.ts (pi v1.5.2).

package theme

import "github.com/keejkrej/pi-go/tui"

// VisualTruncateResult holds visually wrapped lines capped at maxVisualLines along with
// how many visual lines were skipped (hidden).
type VisualTruncateResult struct {
	VisualLines  []string
	SkippedCount int
}

// TruncateToVisualLines wraps text at width (using paddingX, default 0) and returns up to
// maxVisualLines visual lines kept from the end.
func TruncateToVisualLines(text string, maxVisualLines, width int, paddingX ...int) VisualTruncateResult {
	padX := 0
	if len(paddingX) > 0 {
		padX = paddingX[0]
	}
	return TruncateToVisualLinesKeep(text, maxVisualLines, width, padX, "end")
}

// TruncateToVisualLinesKeep wraps text at width (using paddingX) and returns up to
// maxVisualLines visual lines from either "start" or "end" (default "end").
func TruncateToVisualLinesKeep(text string, maxVisualLines, width, paddingX int, keep string) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{
			VisualLines:  []string{},
			SkippedCount: 0,
		}
	}

	tempText := tui.NewText(text, paddingX, 0, nil)
	allVisualLines := tempText.Render(width)

	if len(allVisualLines) <= maxVisualLines {
		return VisualTruncateResult{
			VisualLines:  allVisualLines,
			SkippedCount: 0,
		}
	}

	var kept []string
	if keep == "start" {
		if maxVisualLines <= 0 {
			kept = []string{}
		} else {
			kept = allVisualLines[:maxVisualLines]
		}
	} else {
		if maxVisualLines <= 0 {
			kept = allVisualLines
		} else {
			kept = allVisualLines[len(allVisualLines)-maxVisualLines:]
		}
	}
	skipped := len(allVisualLines) - len(kept)

	return VisualTruncateResult{
		VisualLines:  kept,
		SkippedCount: skipped,
	}
}

// VisualLinePreviewOptions configures a VisualLinePreview component.
type VisualLinePreviewOptions struct {
	// Styled text; may contain newlines.
	Text string
	// Maximum number of visual lines to display.
	MaxVisualLines int
	// Which visual lines to keep ("start" or "end"). The hint goes before kept "end"
	// lines and after kept "start" lines.
	Keep string
	// Styled hint line for the given number of hidden visual lines.
	FormatHint func(hidden int) string
}

// VisualLinePreview renders collapsed tool output limited to a number of visual lines,
// caching its rendered lines per width.
type VisualLinePreview struct {
	options     VisualLinePreviewOptions
	hasCache    bool
	cachedWidth int
	cachedLines []string
}

var _ tui.Component = (*VisualLinePreview)(nil)

// NewVisualLinePreview creates a VisualLinePreview component with the given options.
func NewVisualLinePreview(options VisualLinePreviewOptions) *VisualLinePreview {
	return &VisualLinePreview{options: options}
}

// Render renders up to MaxVisualLines visual lines at width, with FormatHint truncated to width
// placed after kept "start" lines or before kept "end" lines when any lines are hidden.
func (p *VisualLinePreview) Render(width int) []string {
	if !p.hasCache || p.cachedWidth != width {
		preview := TruncateToVisualLinesKeep(p.options.Text, p.options.MaxVisualLines, width, 0, p.options.Keep)
		lines := preview.VisualLines
		if preview.SkippedCount > 0 {
			hintText := ""
			if p.options.FormatHint != nil {
				hintText = p.options.FormatHint(preview.skippedHintCount())
			}
			hint := tui.TruncateToWidth(hintText, width, "...", false)
			if p.options.Keep == "start" {
				combined := make([]string, 0, len(lines)+1)
				combined = append(combined, lines...)
				combined = append(combined, hint)
				p.cachedLines = combined
			} else {
				combined := make([]string, 0, len(lines)+1)
				combined = append(combined, hint)
				combined = append(combined, lines...)
				p.cachedLines = combined
			}
		} else {
			p.cachedLines = lines
		}
		p.cachedWidth = width
		p.hasCache = true
	}
	return p.cachedLines
}

func (r VisualTruncateResult) skippedHintCount() int {
	return r.SkippedCount
}

// Invalidate clears the cached lines so the next Render recomputes them.
func (p *VisualLinePreview) Invalidate() {
	p.hasCache = false
	p.cachedWidth = 0
	p.cachedLines = nil
}
