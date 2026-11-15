// Ported from packages/coding-agent/src/core/export-html/index.ts (pi v1.5.2).

package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ExportOptions configures ExportSessionToHtml and ExportFromFile.
type ExportOptions struct {
	OutputPath   *string
	ThemeName    *string
	ToolRenderer ToolHtmlRenderer
}

type ehiRenderedToolHtml struct {
	CallHtml            *string `json:"callHtml,omitzero"`
	ResultHtmlCollapsed *string `json:"resultHtmlCollapsed,omitzero"`
	ResultHtmlExpanded  *string `json:"resultHtmlExpanded,omitzero"`
}

type ehiToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  *typebox.Schema `json:"parameters"`
}

type ehiSessionData struct {
	Header        *SessionHeader                 `json:"header"`
	Entries       []SessionEntry                 `json:"entries"`
	LeafId        *string                        `json:"leafId"`
	SystemPrompt  *string                        `json:"systemPrompt,omitzero"`
	Tools         []ehiToolSchema                `json:"tools,omitzero"`
	RenderedTools map[string]ehiRenderedToolHtml `json:"renderedTools,omitzero"`
}

type ehiRGB struct {
	r int
	g int
	b int
}

type ehiExportColors struct {
	pageBg string
	cardBg string
	infoBg string
}

var (
	ehiHexColorRegex = regexp.MustCompile(`^#([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$`)
	ehiRgbColorRegex = regexp.MustCompile(`^rgb\s*\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$`)
)

func ehiParseColor(color string) (ehiRGB, bool) {
	if m := ehiHexColorRegex.FindStringSubmatch(color); m != nil {
		r, _ := strconv.ParseInt(m[1], 16, 64)
		g, _ := strconv.ParseInt(m[2], 16, 64)
		b, _ := strconv.ParseInt(m[3], 16, 64)
		return ehiRGB{r: int(r), g: int(g), b: int(b)}, true
	}
	if m := ehiRgbColorRegex.FindStringSubmatch(color); m != nil {
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		return ehiRGB{r: r, g: g, b: b}, true
	}
	return ehiRGB{}, false
}

func ehiGetLuminance(c ehiRGB) float64 {
	toLinear := func(v int) float64 {
		s := float64(v) / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*toLinear(c.r) + 0.7152*toLinear(c.g) + 0.0722*toLinear(c.b)
}

func ehiAdjustBrightness(color string, factor float64) string {
	c, ok := ehiParseColor(color)
	if !ok {
		return color
	}
	adjust := func(v int) int {
		n := int(math.Round(float64(v) * factor))
		if n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return n
	}
	return fmt.Sprintf("rgb(%d, %d, %d)", adjust(c.r), adjust(c.g), adjust(c.b))
}

func ehiClampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func ehiDeriveExportColors(baseColor string) ehiExportColors {
	parsed, ok := ehiParseColor(baseColor)
	if !ok {
		return ehiExportColors{
			pageBg: "rgb(24, 24, 30)",
			cardBg: "rgb(30, 30, 36)",
			infoBg: "rgb(60, 55, 40)",
		}
	}

	if ehiGetLuminance(parsed) > 0.5 {
		return ehiExportColors{
			pageBg: ehiAdjustBrightness(baseColor, 0.96),
			cardBg: baseColor,
			infoBg: fmt.Sprintf("rgb(%d, %d, %d)", ehiClampByte(parsed.r+10), ehiClampByte(parsed.g+5), ehiClampByte(parsed.b-20)),
		}
	}
	return ehiExportColors{
		pageBg: ehiAdjustBrightness(baseColor, 0.7),
		cardBg: ehiAdjustBrightness(baseColor, 0.85),
		infoBg: fmt.Sprintf("rgb(%d, %d, %d)", ehiClampByte(parsed.r+20), ehiClampByte(parsed.g+15), parsed.b),
	}
}

func ehiResolveThemeNameArgs(themeName *string) []string {
	if themeName != nil && *themeName != "" {
		return []string{*themeName}
	}
	return nil
}

func ehiResolveExportColors(themeName *string, colors map[string]string) ehiExportColors {
	args := ehiResolveThemeNameArgs(themeName)
	themeExport := theme.GetThemeExportColors(args...)
	userMessageBg := colors["userMessageBg"]
	if userMessageBg == "" {
		userMessageBg = "#343541"
	}
	derived := ehiDeriveExportColors(userMessageBg)
	out := derived
	if themeExport.PageBg != "" {
		out.pageBg = themeExport.PageBg
	}
	if themeExport.CardBg != "" {
		out.cardBg = themeExport.CardBg
	}
	if themeExport.InfoBg != "" {
		out.infoBg = themeExport.InfoBg
	}
	return out
}

func ehiGenerateThemeVars(themeName *string) string {
	args := ehiResolveThemeNameArgs(themeName)
	colors := theme.GetResolvedThemeColors(args...)
	lines := make([]string, 0, len(colors)+3)
	for key, value := range colors {
		lines = append(lines, fmt.Sprintf("--%s: %s;", key, value))
	}
	resolved := ehiResolveExportColors(themeName, colors)
	lines = append(lines, fmt.Sprintf("--exportPageBg: %s;", resolved.pageBg))
	lines = append(lines, fmt.Sprintf("--exportCardBg: %s;", resolved.cardBg))
	lines = append(lines, fmt.Sprintf("--exportInfoBg: %s;", resolved.infoBg))
	return strings.Join(lines, "\n      ")
}

func ehiReadTemplateAsset(templateDir string, relParts ...string) (string, error) {
	diskPath := filepath.Join(append([]string{templateDir}, relParts...)...)
	if data, err := os.ReadFile(diskPath); err == nil {
		return string(data), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	embeddedPath := path.Join(append([]string{"export-html"}, relParts...)...)
	data, err := fs.ReadFile(utils.ConfigAssetsFS(), embeddedPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ehiGenerateHtml(sessionData ehiSessionData, themeName *string) (string, error) {
	templateDir := utils.GetExportTemplateDir()
	template, err := ehiReadTemplateAsset(templateDir, "template.html")
	if err != nil {
		return "", err
	}
	templateCss, err := ehiReadTemplateAsset(templateDir, "template.css")
	if err != nil {
		return "", err
	}
	templateJs, err := ehiReadTemplateAsset(templateDir, "template.js")
	if err != nil {
		return "", err
	}
	markedJs, err := ehiReadTemplateAsset(templateDir, "vendor", "marked.min.js")
	if err != nil {
		return "", err
	}
	markedCjkFriendlyJs, err := ehiReadTemplateAsset(templateDir, "vendor", "marked-cjk-friendly.min.js")
	if err != nil {
		return "", err
	}
	hljsJs, err := ehiReadTemplateAsset(templateDir, "vendor", "highlight.min.js")
	if err != nil {
		return "", err
	}

	args := ehiResolveThemeNameArgs(themeName)
	colors := theme.GetResolvedThemeColors(args...)
	themeVars := ehiGenerateThemeVars(themeName)
	exportColors := ehiResolveExportColors(themeName, colors)

	sessionJSON, err := jsonx.Marshal(sessionData)
	if err != nil {
		return "", err
	}
	sessionDataBase64 := base64.StdEncoding.EncodeToString(sessionJSON)

	css := strings.NewReplacer(
		"{{THEME_VARS}}", themeVars,
		"{{BODY_BG}}", exportColors.pageBg,
		"{{CONTAINER_BG}}", exportColors.cardBg,
		"{{INFO_BG}}", exportColors.infoBg,
	).Replace(templateCss)

	return strings.NewReplacer(
		"{{CSS}}", css,
		"{{JS}}", templateJs,
		"{{SESSION_DATA}}", sessionDataBase64,
		"{{MARKED_JS}}", markedJs,
		"{{MARKED_CJK_FRIENDLY_JS}}", markedCjkFriendlyJs,
		"{{HIGHLIGHT_JS}}", hljsJs,
	).Replace(template), nil
}

var ehiTemplateRenderedTools = map[string]struct{}{
	"bash":  {},
	"read":  {},
	"write": {},
	"edit":  {},
	"ls":    {},
}

func ehiPreRenderCustomTools(entries []SessionEntry, toolRenderer ToolHtmlRenderer) map[string]ehiRenderedToolHtml {
	renderedTools := make(map[string]ehiRenderedToolHtml)

	for _, entry := range entries {
		sm, ok := entry.(*SessionMessageEntry)
		if !ok || sm == nil || sm.Message == nil {
			continue
		}

		switch msg := sm.Message.(type) {
		case *ai.AssistantMessage:
			if msg != nil {
				for _, block := range msg.Content {
					tc, ok := block.(*ai.ToolCall)
					if !ok || tc == nil {
						continue
					}
					if _, builtIn := ehiTemplateRenderedTools[tc.Name]; builtIn {
						continue
					}
					if callHtml := toolRenderer.RenderCall(tc.Id, tc.Name, tc.Arguments); callHtml != nil {
						renderedTools[tc.Id] = ehiRenderedToolHtml{CallHtml: callHtml}
					}
				}
			}
		case *ai.ToolResultMessage:
			if msg != nil && msg.ToolCallId != "" {
				toolName := msg.ToolName
				existing, hasExisting := renderedTools[msg.ToolCallId]
				_, builtIn := ehiTemplateRenderedTools[toolName]
				if hasExisting || !builtIn {
					if rendered := toolRenderer.RenderResult(msg.ToolCallId, toolName, msg.Content, msg.Details.OrElse(nil), msg.IsError); rendered != nil {
						existing.ResultHtmlCollapsed = rendered.Collapsed
						existing.ResultHtmlExpanded = rendered.Expanded
						renderedTools[msg.ToolCallId] = existing
					}
				}
			}
		}
	}

	return renderedTools
}

// ExportSessionToHtml exports the active SessionManager and AgentState to a standalone HTML file.
func ExportSessionToHtml(sm *SessionManager, state *agent.AgentState, options ...*ExportOptions) (string, error) {
	if sm == nil {
		return "", errors.New("Cannot export in-memory session to HTML")
	}
	var opts ExportOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	sessionFile := sm.GetSessionFile()
	if sessionFile == nil || *sessionFile == "" {
		return "", errors.New("Cannot export in-memory session to HTML")
	}
	if _, err := os.Stat(*sessionFile); err != nil {
		return "", errors.New("Nothing to export yet - start a conversation first")
	}

	entries := sm.GetEntries()

	var renderedTools map[string]ehiRenderedToolHtml
	if opts.ToolRenderer != nil {
		rt := ehiPreRenderCustomTools(entries, opts.ToolRenderer)
		if len(rt) > 0 {
			renderedTools = rt
		}
	}

	var sysPrompt *string
	var tools []ehiToolSchema
	if state != nil {
		if state.SystemPrompt != "" {
			sp := state.SystemPrompt
			sysPrompt = &sp
		}
		if len(state.Tools) > 0 {
			tools = make([]ehiToolSchema, 0, len(state.Tools))
			for _, t := range state.Tools {
				if t != nil {
					tools = append(tools, ehiToolSchema{
						Name:        t.Name,
						Description: t.Description,
						Parameters:  t.Parameters,
					})
				}
			}
		}
	}

	sessionData := ehiSessionData{
		Header:        sm.GetHeader(),
		Entries:       entries,
		LeafId:        sm.GetLeafId(),
		SystemPrompt:  sysPrompt,
		Tools:         tools,
		RenderedTools: renderedTools,
	}

	html, err := ehiGenerateHtml(sessionData, opts.ThemeName)
	if err != nil {
		return "", err
	}

	var outputPath string
	if opts.OutputPath != nil && *opts.OutputPath != "" {
		normalized, normErr := utils.NormalizePath(*opts.OutputPath, nil)
		if normErr != nil {
			return "", normErr
		}
		outputPath = normalized
	} else {
		base := filepath.Base(*sessionFile)
		sessionBasename := strings.TrimSuffix(base, ".jsonl")
		outputPath = fmt.Sprintf("%s-session-%s.html", utils.AppName, sessionBasename)
	}

	if err := os.WriteFile(outputPath, []byte(html), 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}

// ExportFromFile exports a saved session JSONL file to standalone HTML without active AgentState.
func ExportFromFile(inputPath string, options ...*ExportOptions) (string, error) {
	var opts ExportOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	resolvedInputPath, resErr := utils.ResolvePath(inputPath, "", nil)
	if resErr != nil {
		return "", resErr
	}
	if _, err := os.Stat(resolvedInputPath); err != nil {
		return "", fmt.Errorf("File not found: %s", resolvedInputPath)
	}

	sm, err := SessionManagerOpen(resolvedInputPath)
	if err != nil {
		return "", err
	}

	sessionData := ehiSessionData{
		Header:  sm.GetHeader(),
		Entries: sm.GetEntries(),
		LeafId:  sm.GetLeafId(),
	}

	html, err := ehiGenerateHtml(sessionData, opts.ThemeName)
	if err != nil {
		return "", err
	}

	var outputPath string
	if opts.OutputPath != nil && *opts.OutputPath != "" {
		normalized, normErr := utils.NormalizePath(*opts.OutputPath, nil)
		if normErr != nil {
			return "", normErr
		}
		outputPath = normalized
	} else {
		base := filepath.Base(resolvedInputPath)
		inputBasename := strings.TrimSuffix(base, ".jsonl")
		outputPath = fmt.Sprintf("%s-session-%s.html", utils.AppName, inputBasename)
	}

	if err := os.WriteFile(outputPath, []byte(html), 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}
