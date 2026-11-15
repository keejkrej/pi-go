// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
	"golang.org/x/term"
)

const imhAnthropicSubscriptionAuthWarning = "Warning: You are using an Anthropic subscription token. Anthropic has restricted third-party harness use under subscription plans; consider switching to an API key (`ANTHROPIC_API_KEY`) or another provider if you run into access errors."

// FormatCrashExtensionHint summarizes extension stack matches for crash recovery notices.
func FormatCrashExtensionHint(extensionMatches []string) *string {
	if len(extensionMatches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(extensionMatches))
	unique := make([]string, 0, len(extensionMatches))
	for _, match := range extensionMatches {
		trimmed := strings.TrimSpace(match)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		unique = append(unique, trimmed)
	}
	if len(unique) == 0 {
		return nil
	}
	var out string
	if len(unique) == 1 {
		out = fmt.Sprintf("Likely caused by extension: %s", unique[0])
	} else {
		out = fmt.Sprintf("Likely caused by extensions: %s", strings.Join(unique, ", "))
	}
	return &out
}

// FormatResumeCommand returns the shell-safe command to resume the active persisted session,
// or nil if the session has no persisted session file.
func FormatResumeCommand(sessionManager *core.SessionManager) *string {
	if sessionManager == nil {
		return nil
	}
	sessionFile := sessionManager.GetSessionFile()
	if sessionFile == nil || strings.TrimSpace(*sessionFile) == "" {
		return nil
	}
	parts := []string{utils.AppName}
	cwd := sessionManager.GetCwd()
	if cwd != "" {
		if procCwd, err := os.Getwd(); err == nil && procCwd != "" && cwd != procCwd {
			parts = append(parts, "--cwd", imhQuoteIfNeeded(cwd))
		}
	}
	parts = append(parts, "--session", imhQuoteIfNeeded(*sessionFile))
	cmd := strings.Join(parts, " ")
	return &cmd
}

func imhStrPtr(s string) *string {
	v := s
	return &v
}

func imhBoolPtr(b bool) *bool {
	v := b
	return &v
}

func imhIntPtr(i int) *int {
	v := i
	return &v
}

func imhCloneStrPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func imhDerefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func imhQuoteIfNeeded(value string) string {
	if value == "" {
		return `""`
	}
	needsQuote := false
	for _, r := range value {
		if unicode.IsSpace(r) || strings.ContainsRune(`"'$`+"`"+`\()[]{}*?<>|;&!#~`, r) {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return value
	}
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	return `"` + escaped + `"`
}

func imhIsAnthropicSubscriptionAuthKey(apiKey *string) bool {
	if apiKey == nil {
		return false
	}
	return strings.HasPrefix(*apiKey, "sk-ant-oat")
}

func imhIsUnknownModel(model *ai.Model) bool {
	if model == nil {
		return false
	}
	return strings.Contains(strings.ToLower(model.Name), "unknown") ||
		(model.Id != "" && strings.EqualFold(model.Name, model.Id) &&
			model.Cost.Input == 0 && model.Cost.Output == 0 && model.ContextWindow == 0)
}

func imhIsDeadTerminalError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ENOTCONN) || errors.Is(err, syscall.ENOTTY) {
		return true
	}
	msg := err.Error()
	for _, code := range []string{"EIO", "EPIPE", "ENOTCONN", "ENOTTY"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

func imhErrorMessage(err error) string {
	if err == nil {
		return "Unknown error"
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "Unknown error"
	}
	return msg
}

func imhRandomID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf[:])
}

func imhIsTerminalFD() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func imhExpandTildePath(p string) string {
	if p == "" {
		return ""
	}
	return utils.ExpandTildePath(p)
}

func imhHasDefaultModelProvider(providerId string) (string, bool) {
	id, ok := core.DefaultModelPerProvider[ai.KnownProvider(providerId)]
	return id, ok
}

func imhLlamaCppPostLoginGuidance(actionLabel string, loadedModelCount int) string {
	if loadedModelCount > 0 {
		plural := ""
		if loadedModelCount != 1 {
			plural = "s"
		}
		return fmt.Sprintf("%s; discovered %d llama.cpp model%s. Use /model to choose one.", actionLabel, loadedModelCount, plural)
	}
	return fmt.Sprintf("%s, but no llama.cpp models are currently loaded. Load a model in llama.cpp and run /model or /reload.", actionLabel)
}

func imhParseSkillCommand(text string) (skillName string, args string, ok bool) {
	if !strings.HasPrefix(text, "/skill:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(text, "/skill:")
	if rest == "" {
		return "", "", false
	}
	idx := strings.IndexAny(rest, " \t\n\r")
	if idx == -1 {
		return rest, "", true
	}
	name := rest[:idx]
	rem := strings.TrimSpace(rest[idx+1:])
	if name == "" {
		return "", "", false
	}
	return name, rem, true
}

func imhContainsAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func imhSanitizeTextForSingleLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

func imhIsExpandable(component any) (interface{ SetExpanded(bool) }, bool) {
	if component == nil {
		return nil, false
	}
	exp, ok := component.(interface{ SetExpanded(bool) })
	return exp, ok
}

func imhSetOutputPad(component any, outputPad int) {
	if component == nil {
		return
	}
	if padded, ok := component.(interface{ SetOutputPad(int) }); ok {
		padded.SetOutputPad(outputPad)
	}
}

func imhDisposeComponent(component any) {
	if component == nil {
		return
	}
	if d, ok := component.(interface{ Dispose() }); ok {
		d.Dispose()
	}
}

func imhNormalizePathForDisplay(p string) string {
	if p == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		prefix := home + string(filepath.Separator)
		if strings.HasPrefix(p, prefix) {
			return "~/" + filepath.ToSlash(strings.TrimPrefix(p, prefix))
		}
	}
	return p
}

func imhParseClipboardImageSavePath(ext string) string {
	if ext == "" {
		ext = "png"
	}
	tmpDir := os.TempDir()
	return filepath.Join(tmpDir, fmt.Sprintf("pi-clipboard-%s.%s", imhRandomID(), ext))
}

func imhIsImagePath(filePath string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))
	switch ext {
	case "png":
		return "image/png", true
	case "jpg", "jpeg":
		return "image/jpeg", true
	case "gif":
		return "image/gif", true
	case "webp":
		return "image/webp", true
	default:
		return "", false
	}
}

func imhStripPathQuotesAndSpaces(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 2 {
		if (trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') ||
			(trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') {
			trimmed = trimmed[1 : len(trimmed)-1]
		}
	}
	return trimmed
}

func imhAppKey(t *theme.Theme, kb *core.KeybindingsManager, action tui.Keybinding) string {
	if kb != nil {
		keys := kb.GetKeys(action)
		if len(keys) > 0 {
			formatted := theme.FormatKeys(keys)
			if t != nil {
				return t.Fg(theme.FgDim, formatted)
			}
			return formatted
		}
	}
	if t != nil {
		return theme.AppKey(t, action)
	}
	return theme.KeyDisplayText(action)
}

func imhAppKeyDisplay(kb *core.KeybindingsManager, action tui.Keybinding) string {
	if kb != nil {
		keys := kb.GetKeys(action)
		if len(keys) > 0 {
			return theme.FormatKeys(keys)
		}
	}
	return theme.KeyDisplayText(action)
}

func imhFormatModelListSort(models []*ai.Model) []*ai.Model {
	out := make([]*ai.Model, len(models))
	copy(out, models)
	slices.SortStableFunc(out, func(a, b *ai.Model) int {
		if a == nil && b == nil {
			return 0
		}
		if a == nil {
			return -1
		}
		if b == nil {
			return 1
		}
		if c := strings.Compare(string(a.Provider), string(b.Provider)); c != 0 {
			return c
		}
		return strings.Compare(a.Id, b.Id)
	})
	return out
}

func imhApplyEditorStylePrefix(promptStylePrefix *string, lines []string) []string {
	if promptStylePrefix == nil || *promptStylePrefix == "" {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = *promptStylePrefix + line
	}
	return out
}

func imhSetEditorOnSubmit(ed imEditor, fn func(string)) {
	if ed == nil {
		return
	}
	switch v := ed.(type) {
	case *components.CustomEditor:
		v.OnSubmit = fn
	case *tui.Editor:
		v.OnSubmit = fn
	case tui.EditorComponent:
		v.SetOnSubmit(fn)
	case interface{ SetOnSubmit(func(string)) }:
		v.SetOnSubmit(fn)
	}
}

func imhSetEditorOnChange(ed imEditor, fn func(string)) {
	if ed == nil {
		return
	}
	switch v := ed.(type) {
	case *components.CustomEditor:
		v.OnChange = fn
	case *tui.Editor:
		v.OnChange = fn
	case tui.EditorComponent:
		v.SetOnChange(fn)
	case interface{ SetOnChange(func(string)) }:
		v.SetOnChange(fn)
	}
}

func imhSetEditorBorderColor(ed imEditor, fn func(string) string) {
	if ed == nil {
		return
	}
	switch v := ed.(type) {
	case *components.CustomEditor:
		v.BorderColor = fn
	case *tui.Editor:
		v.BorderColor = fn
	case tui.EditorComponent:
		v.SetBorderColor(fn)
	case interface{ SetBorderColor(func(string) string) }:
		v.SetBorderColor(fn)
	}
}

func imhSetEditorDisableSubmit(ed imEditor, disabled bool) {
	if ed == nil {
		return
	}
	switch v := ed.(type) {
	case *components.CustomEditor:
		v.DisableSubmit = disabled
	case *tui.Editor:
		v.DisableSubmit = disabled
	case interface{ SetDisableSubmit(bool) }:
		v.SetDisableSubmit(disabled)
	}
}
