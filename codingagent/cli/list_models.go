// Ported from packages/coding-agent/src/cli/list-models.ts (pi v1.5.2).

package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/ansi"
	"github.com/keejkrej/pi-go/tui"
)

type lmRow struct {
	provider string
	model    string
	context  string
	maxOut   string
	thinking string
	images   string
}

func lmFormatTokenCount(count int) string {
	if count >= 1_000_000 {
		millions := float64(count) / 1_000_000.0
		if math.Mod(millions, 1) == 0 {
			return fmt.Sprintf("%.0fM", millions)
		}
		return fmt.Sprintf("%.1fM", millions)
	}
	if count >= 1_000 {
		thousands := float64(count) / 1_000.0
		if math.Mod(thousands, 1) == 0 {
			return fmt.Sprintf("%.0fK", thousands)
		}
		return fmt.Sprintf("%.1fK", thousands)
	}
	return strconv.Itoa(count)
}

func lmPadRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func lmYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// ListModels prints available models from modelRuntime, optionally filtered by fuzzy searchPattern.
func ListModels(ctx context.Context, modelRuntime *core.ModelRuntime, searchPattern *string) error {
	return ListModelsTo(ctx, os.Stdout, os.Stderr, modelRuntime, searchPattern)
}

// ListModelsTo prints available models from modelRuntime to out and warnings to errOut.
func ListModelsTo(
	ctx context.Context,
	out io.Writer,
	errOut io.Writer,
	modelRuntime *core.ModelRuntime,
	searchPattern *string,
) error {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	return lmListModelsTo(ctx, out, errOut, modelRuntime, searchPattern)
}

func lmListModelsTo(
	ctx context.Context,
	out io.Writer,
	errOut io.Writer,
	modelRuntime *core.ModelRuntime,
	searchPattern *string,
) error {
	if loadErr := modelRuntime.GetError(); loadErr != nil && *loadErr != "" {
		_, _ = fmt.Fprintln(errOut, ansi.Yellow("Warning: errors loading models.json:\n"+*loadErr))
	}

	available, err := modelRuntime.GetAvailable(ctx, nil, nil)
	if err != nil {
		return err
	}
	models := append([]*ai.Model(nil), available...)

	if len(models) == 0 {
		_, _ = fmt.Fprintln(out, core.FormatNoModelsAvailableMessage())
		return nil
	}

	filteredModels := models
	if searchPattern != nil && *searchPattern != "" {
		filteredModels = tui.FuzzyFilter(models, *searchPattern, func(m *ai.Model) string {
			return string(m.Provider) + " " + m.Id
		})
	}

	if len(filteredModels) == 0 {
		pattern := ""
		if searchPattern != nil {
			pattern = *searchPattern
		}
		_, _ = fmt.Fprintf(out, "No models matching %q\n", pattern)
		return nil
	}

	sort.Slice(filteredModels, func(i, j int) bool {
		if filteredModels[i].Provider != filteredModels[j].Provider {
			return strings.Compare(string(filteredModels[i].Provider), string(filteredModels[j].Provider)) < 0
		}
		return strings.Compare(filteredModels[i].Id, filteredModels[j].Id) < 0
	})

	rows := make([]lmRow, len(filteredModels))
	for i, m := range filteredModels {
		rows[i] = lmRow{
			provider: string(m.Provider),
			model:    m.Id,
			context:  lmFormatTokenCount(m.ContextWindow),
			maxOut:   lmFormatTokenCount(m.MaxTokens),
			thinking: lmYesNo(m.Reasoning),
			images:   lmYesNo(slices.Contains(m.Input, ai.ModelModalityImage)),
		}
	}

	headers := lmRow{
		provider: "provider",
		model:    "model",
		context:  "context",
		maxOut:   "max-out",
		thinking: "thinking",
		images:   "images",
	}

	widths := struct{ provider, model, context, maxOut, thinking, images int }{
		provider: len(headers.provider),
		model:    len(headers.model),
		context:  len(headers.context),
		maxOut:   len(headers.maxOut),
		thinking: len(headers.thinking),
		images:   len(headers.images),
	}
	for _, r := range rows {
		widths.provider = max(widths.provider, len(r.provider))
		widths.model = max(widths.model, len(r.model))
		widths.context = max(widths.context, len(r.context))
		widths.maxOut = max(widths.maxOut, len(r.maxOut))
		widths.thinking = max(widths.thinking, len(r.thinking))
		widths.images = max(widths.images, len(r.images))
	}

	formatRow := func(r lmRow) string {
		return strings.Join([]string{
			lmPadRight(r.provider, widths.provider),
			lmPadRight(r.model, widths.model),
			lmPadRight(r.context, widths.context),
			lmPadRight(r.maxOut, widths.maxOut),
			lmPadRight(r.thinking, widths.thinking),
			lmPadRight(r.images, widths.images),
		}, "  ")
	}

	_, _ = fmt.Fprintln(out, formatRow(headers))
	for _, r := range rows {
		_, _ = fmt.Fprintln(out, formatRow(r))
	}
	return nil
}
