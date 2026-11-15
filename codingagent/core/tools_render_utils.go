// Ported from packages/coding-agent/src/core/tools/render-utils.ts (pi v1.5.2).

package core

import (
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

const truCollapsedArgsChars = 100

// DefaultKeybindingHints returns KeybindingHints backed by the interactive theme keybinding helpers.
func DefaultKeybindingHints() KeybindingHints {
	return KeybindingHints{
		KeyText: func(keybinding tui.Keybinding) string {
			return theme.KeyText(keybinding)
		},
		KeyHint: func(keybinding tui.Keybinding, description string) string {
			return theme.AppKeyHint(theme.CurrentTheme(), keybinding, description)
		},
	}
}

func truResolveTheme(th *theme.Theme) *theme.Theme {
	if th != nil {
		return th
	}
	return theme.CurrentTheme()
}

func truKeyHint(th *theme.Theme, rctx ToolRenderContext, keybinding tui.Keybinding, description string) string {
	if rctx.KeyHint != nil {
		return rctx.KeyHint(keybinding, description)
	}
	return theme.AppKeyHint(truResolveTheme(th), keybinding, description)
}

func truKeyText(rctx ToolRenderContext, keybinding tui.Keybinding) string {
	if rctx.KeyText != nil {
		return rctx.KeyText(keybinding)
	}
	return theme.KeyText(keybinding)
}

func truGetArg(args *jsonx.Object, key string) (any, bool) {
	if args == nil {
		return nil, false
	}
	return args.Get(key)
}

func truGetPathArg(args *jsonx.Object) *string {
	if raw, ok := truGetArg(args, "file_path"); ok && !truIsNilArg(raw) {
		return Str(raw)
	}
	raw, _ := truGetArg(args, "path")
	return Str(raw)
}

func truTrimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}

func truDecodeDetails[T any](details any) *T {
	switch d := details.(type) {
	case *T:
		return d
	case T:
		cp := d
		return &cp
	case *jsonx.Object:
		if d == nil {
			return nil
		}
		if encoded, err := jsonx.Marshal(d); err == nil {
			var parsed T
			if jsonx.Unmarshal(encoded, &parsed) == nil {
				return &parsed
			}
		}
	case map[string]any:
		if encoded, err := jsonx.Marshal(d); err == nil {
			var parsed T
			if jsonx.Unmarshal(encoded, &parsed) == nil {
				return &parsed
			}
		}
	}
	return nil
}

func truPathToFileURL(filePath string) string {
	slashed := filepath.ToSlash(filePath)
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

// ShortenPath replaces a leading home directory with "~" when path is a string.
func ShortenPath(path any) string {
	s, ok := path.(string)
	if !ok {
		return ""
	}
	return utils.ShortenHomePath(s)
}

// LinkPath wraps styledText in an OSC 8 terminal hyperlink to rawPath when hyperlinks are supported.
func LinkPath(styledText, rawPath, cwd string) string {
	if !tui.GetCapabilities().Hyperlinks {
		return styledText
	}
	absPath, err := utils.ResolvePath(rawPath, cwd, nil)
	if err != nil {
		absPath = rawPath
	}
	return tui.Hyperlink(styledText, truPathToFileURL(absPath))
}

// Str converts a tool argument to a displayable string, returning nil when it has an invalid non-string type.
func Str(value any) *string {
	if value == nil {
		empty := ""
		return &empty
	}
	switch v := value.(type) {
	case string:
		s := v
		return &s
	case *string:
		if v == nil {
			empty := ""
			return &empty
		}
		s := *v
		return &s
	default:
		rv := reflect.ValueOf(value)
		if rv.Kind() == reflect.Ptr && rv.IsNil() {
			empty := ""
			return &empty
		}
		return nil
	}
}

func truParseLineNumber(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case *float64:
		if v == nil {
			return 0, false
		}
		value = *v
	case *int:
		if v == nil {
			return 0, false
		}
		value = float64(*v)
	case *int64:
		if v == nil {
			return 0, false
		}
		value = float64(*v)
	case *string:
		if v == nil {
			return 0, false
		}
		value = *v
	}
	if s, ok := value.(string); ok {
		if strings.TrimSpace(s) == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, false
		}
		return parsed, true
	}
	switch n := value.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	case float32:
		f := float64(n)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func truFormatNumber(n float64) string {
	return jsonx.FormatNumber(n)
}

func truIsNilArg(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map:
		return rv.IsNil()
	default:
		return false
	}
}

// FormatReadLineRange formats finite numbers or numeric strings as a read line range, omitting malformed ranges.
func FormatReadLineRange(offset, limit any) string {
	if truIsNilArg(offset) && truIsNilArg(limit) {
		return ""
	}
	var start float64 = 1
	if !truIsNilArg(offset) {
		parsed, ok := truParseLineNumber(offset)
		if !ok {
			return ""
		}
		start = parsed
	}
	if truIsNilArg(limit) {
		return ":" + truFormatNumber(start)
	}
	count, ok := truParseLineNumber(limit)
	if !ok {
		return ""
	}
	end := start + count - 1
	if end == 0 {
		return ":" + truFormatNumber(start)
	}
	return fmt.Sprintf(":%s-%s", truFormatNumber(start), truFormatNumber(end))
}

// ReplaceTabs replaces horizontal tabs with three spaces for terminal rendering.
func ReplaceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

// NormalizeDisplayText strips carriage returns for consistent line rendering.
func NormalizeDisplayText(text string) string {
	return strings.ReplaceAll(text, "\r", "")
}

func truExtractTextAndImages(content ai.ContentList) ([]string, []ai.ImageContent) {
	var texts []string
	var images []ai.ImageContent
	for _, block := range content {
		switch b := block.(type) {
		case *ai.TextContent:
			if b != nil {
				texts = append(texts, b.Text)
			}
		case *ai.ImageContent:
			if b != nil {
				images = append(images, *b)
			}
		}
	}
	return texts, images
}

// GetTextOutput collects sanitized text blocks from result plus image fallback notices when images are not shown inline.
func GetTextOutput(result *agent.AgentToolResult, showImages bool) string {
	if result == nil {
		return ""
	}
	textBlocks, imageBlocks := truExtractTextAndImages(result.Content)
	sanitized := make([]string, len(textBlocks))
	for i, t := range textBlocks {
		sanitized[i] = strings.ReplaceAll(utils.SanitizeBinaryOutput(utils.StripAnsi(t)), "\r", "")
	}
	output := strings.Join(sanitized, "\n")

	caps := tui.GetCapabilities()
	if len(imageBlocks) > 0 && (caps.Images == "" || !showImages) {
		indicators := make([]string, len(imageBlocks))
		for i, img := range imageBlocks {
			mimeType := img.MimeType
			if mimeType == "" {
				mimeType = "image/unknown"
			}
			var dims *tui.ImageDimensions
			if img.Data != "" && img.MimeType != "" {
				dims = tui.GetImageDimensions(img.Data, img.MimeType)
			}
			indicators[i] = tui.ImageFallback(mimeType, dims, nil)
		}
		joined := strings.Join(indicators, "\n")
		if output != "" {
			output = output + "\n" + joined
		} else {
			output = joined
		}
	}
	return output
}

// FormatToolCallWithArgs formats a generic tool call header with collapsed key=value or expanded key: value arguments.
func FormatToolCallWithArgs(title string, args any, th *theme.Theme, expanded bool) string {
	t := truResolveTheme(th)
	header := t.Fg(theme.FgToolTitle, t.Bold(title))
	if truIsNilArg(args) {
		return header
	}

	type kvEntry struct {
		key   string
		value any
	}
	var entries []kvEntry
	switch v := args.(type) {
	case *jsonx.Object:
		if v == nil {
			return header
		}
		for k, val := range v.All() {
			entries = append(entries, kvEntry{key: k, value: val})
		}
	case map[string]any:
		for k, val := range v {
			entries = append(entries, kvEntry{key: k, value: val})
		}
	default:
		rv := reflect.ValueOf(args)
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			entries = []kvEntry{{key: "args", value: args}}
		} else if encoded, err := jsonx.Stringify(args); err == nil {
			if parsedAny, err := jsonx.ParseString(encoded); err == nil {
				if parsed, ok := parsedAny.(*jsonx.Object); ok && parsed != nil {
					for k, val := range parsed.All() {
						entries = append(entries, kvEntry{key: k, value: val})
					}
				} else {
					entries = []kvEntry{{key: "args", value: args}}
				}
			} else {
				entries = []kvEntry{{key: "args", value: args}}
			}
		} else {
			entries = []kvEntry{{key: "args", value: args}}
		}
	}

	if len(entries) == 0 {
		return header
	}
	if expanded {
		lines := make([]string, len(entries))
		for i, entry := range entries {
			var text string
			if s, ok := entry.value.(string); ok {
				text = s
			} else if encoded, err := jsonx.StringifyIndent(entry.value, "  "); err == nil && encoded != "" {
				text = encoded
			} else {
				text = fmt.Sprint(entry.value)
			}
			formatted := strings.ReplaceAll(strings.ReplaceAll(ReplaceTabs(text), "\r", ""), "\n", "\n    ")
			lines[i] = fmt.Sprintf("  %s: %s", entry.key, formatted)
		}
		return header + "\n" + t.Fg(theme.FgMuted, strings.Join(lines, "\n"))
	}

	pairs := make([]string, len(entries))
	for i, entry := range entries {
		var valStr string
		if encoded, err := jsonx.Stringify(entry.value); err == nil && encoded != "" {
			valStr = encoded
		} else {
			valStr = fmt.Sprint(entry.value)
		}
		pairs[i] = fmt.Sprintf("%s=%s", entry.key, valStr)
	}
	joined := strings.Join(pairs, " ")
	preview := joined
	if len([]rune(joined)) > truCollapsedArgsChars {
		runes := []rune(joined)
		preview = string(runes[:truCollapsedArgsChars-3]) + "..."
	}
	return header + " " + t.Fg(theme.FgMuted, preview)
}

// FormatToolDuration formats milliseconds as `4.2s` below a minute, then `1m 30s` and `2h 3m 4s`.
func FormatToolDuration(ms float64) string {
	seconds := ms / 1000.0
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	totalSeconds := int64(math.Floor(seconds))
	minutes := totalSeconds / 60
	remainder := totalSeconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, remainder)
	}
	return fmt.Sprintf("%dh %dm %ds", minutes/60, minutes%60, remainder)
}

// ToolTimerState tracks elapsed timing and periodic invalidation while a tool row streams.
type ToolTimerState struct {
	mu        sync.Mutex
	StartedAt *time.Time
	EndedAt   *time.Time
	stopCh    chan struct{}
}

// Stop cancels any active background invalidation ticker on state.
func (s *ToolTimerState) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh = nil
	}
}

func truResolveTimerState(state any) *ToolTimerState {
	switch s := state.(type) {
	case *ToolTimerState:
		return s
	case map[string]any:
		if existing, ok := s["toolTimer"].(*ToolTimerState); ok && existing != nil {
			return existing
		}
		timer := &ToolTimerState{}
		s["toolTimer"] = timer
		return timer
	default:
		return nil
	}
}

// StartToolTimer records the start time in rctx.State once tool execution begins.
func StartToolTimer(rctx ToolRenderContext) {
	timer := truResolveTimerState(rctx.State)
	if timer == nil {
		return
	}
	timer.mu.Lock()
	defer timer.mu.Unlock()
	if rctx.ExecutionStarted && timer.StartedAt == nil {
		now := time.Now()
		timer.StartedAt = &now
		timer.EndedAt = nil
	}
}

// ToolTimerText returns the elapsed/final timing label and manages the 1s partial redraw ticker.
func ToolTimerText(rctx ToolRenderContext, isPartial bool) *string {
	if !isPartial && rctx.DurationMs != nil {
		if timer := truResolveTimerState(rctx.State); timer != nil {
			timer.mu.Lock()
			if timer.EndedAt == nil {
				now := time.Now()
				timer.EndedAt = &now
			}
			if timer.stopCh != nil {
				close(timer.stopCh)
				timer.stopCh = nil
			}
			timer.mu.Unlock()
		}
		text := "Took " + FormatToolDuration(float64(*rctx.DurationMs))
		return &text
	}

	timer := truResolveTimerState(rctx.State)
	if timer == nil {
		return nil
	}

	timer.mu.Lock()
	if timer.StartedAt != nil && isPartial && !rctx.IsError && timer.stopCh == nil && rctx.Invalidate != nil {
		stopCh := make(chan struct{})
		timer.stopCh = stopCh
		invalidate := rctx.Invalidate
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stopCh:
					return
				case <-ticker.C:
					invalidate()
				}
			}
		}()
	}
	if !isPartial || rctx.IsError {
		if timer.EndedAt == nil {
			now := time.Now()
			timer.EndedAt = &now
		}
		if timer.stopCh != nil {
			close(timer.stopCh)
			timer.stopCh = nil
		}
	}
	if timer.StartedAt == nil {
		timer.mu.Unlock()
		return nil
	}
	endTime := time.Now()
	if timer.EndedAt != nil {
		endTime = *timer.EndedAt
	}
	startedAt := *timer.StartedAt
	timer.mu.Unlock()

	elapsedMs := float64(endTime.Sub(startedAt).Nanoseconds()) / float64(time.Millisecond)
	if elapsedMs < 0 {
		elapsedMs = 0
	}
	prefix := "Took "
	if isPartial {
		prefix = "Elapsed "
	}
	text := prefix + FormatToolDuration(elapsedMs)
	return &text
}

// InvalidArgText formats the standard "[invalid arg]" placeholder.
func InvalidArgText(th *theme.Theme) string {
	return truResolveTheme(th).Fg(theme.FgError, "[invalid arg]")
}

// RenderToolPathOptions configures RenderToolPath.
type RenderToolPathOptions struct {
	EmptyFallback string
}

// RenderToolPath renders a raw path argument with home shortening and OSC 8 hyperlinks.
func RenderToolPath(rawPath *string, th *theme.Theme, cwd string, options ...RenderToolPathOptions) string {
	t := truResolveTheme(th)
	if rawPath == nil {
		return InvalidArgText(t)
	}
	value := *rawPath
	if value == "" && len(options) > 0 {
		value = options[0].EmptyFallback
	}
	if value == "" {
		return t.Fg(theme.FgToolOutput, "...")
	}
	return LinkPath(t.Fg(theme.FgAccent, ShortenPath(value)), value, cwd)
}
