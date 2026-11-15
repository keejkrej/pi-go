// Ported from packages/coding-agent/src/modes/interactive/interactive-mode.ts (pi v1.5.2).

package interactive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// ShowLoadedResourcesOptions controls startup and /reload resource listing verbosity.
type ShowLoadedResourcesOptions struct {
	ExtensionPaths            []string
	Force                     bool
	ShowDiagnosticsWhenSilent bool
}

func (m *InteractiveMode) imrShowStartupResourceDiagnostics() {
	sm := m.imSettingsManager()
	silent := sm != nil && sm.GetQuietStartup().Silent
	m.ShowLoadedResources(&ShowLoadedResourcesOptions{
		Force:                     false,
		ShowDiagnosticsWhenSilent: !silent || m.options.Verbose,
	})
}

// ShowLoadedResources displays discovered skills, prompts, themes, context files, extensions,
// and resource diagnostics when verbose or requested.
func (m *InteractiveMode) ShowLoadedResources(options ...*ShowLoadedResourcesOptions) {
	rl := m.imResourceLoader()
	if rl == nil {
		return
	}
	var opts ShowLoadedResourcesOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	sm := m.imSettingsManager()
	silent := sm != nil && sm.GetQuietStartup().Silent
	showList := opts.Force || (m.options.Verbose && !silent)
	showDiagnostics := !silent || opts.Force || opts.ShowDiagnosticsWhenSilent

	skillsRes := rl.GetSkills()
	promptsRes := rl.GetPrompts()
	themesRes := rl.GetThemes()
	ctxRes := rl.GetAgentsFiles()
	extRes := rl.GetExtensions()

	if showList {
		var sections []string

		if len(ctxRes.AgentsFiles) > 0 {
			lines := []string{theme.CurrentTheme().Fg(theme.FgAccent, "[Context]")}
			for _, f := range ctxRes.AgentsFiles {
				lines = append(lines, "  "+theme.CurrentTheme().Fg(theme.FgDim, m.imrFormatDisplayPath(f.Path)))
			}
			sections = append(sections, strings.Join(lines, "\n"))
		}

		if len(skillsRes.Skills) > 0 {
			lines := []string{theme.CurrentTheme().Fg(theme.FgAccent, "[Skills]")}
			for _, sk := range skillsRes.Skills {
				lines = append(lines, m.imrFormatResourceLine(sk.FilePath, sk.SourceInfo, sk.Name))
			}
			sections = append(sections, strings.Join(lines, "\n"))
		}

		if len(promptsRes.Templates) > 0 {
			lines := []string{theme.CurrentTheme().Fg(theme.FgAccent, "[Prompts]")}
			for _, pt := range promptsRes.Templates {
				lines = append(lines, m.imrFormatResourceLine(pt.FilePath, pt.SourceInfo, "/"+pt.Name))
			}
			sections = append(sections, strings.Join(lines, "\n"))
		}

		if len(themesRes.Themes) > 0 {
			lines := []string{theme.CurrentTheme().Fg(theme.FgAccent, "[Themes]")}
			meta := rl.GetPathMetadata()
			for _, th := range themesRes.Themes {
				if th == nil {
					continue
				}
				lines = append(lines, m.imrFormatThemeLine(th, meta))
			}
			sections = append(sections, strings.Join(lines, "\n"))
		}

		extPaths := opts.ExtensionPaths
		if len(extPaths) == 0 {
			for _, ext := range extRes.Extensions {
				extPaths = append(extPaths, ext.Path)
			}
		}
		if len(extPaths) > 0 {
			lines := []string{theme.CurrentTheme().Fg(theme.FgAccent, "[Extensions]")}
			meta := rl.GetPathMetadata()
			for _, p := range extPaths {
				lines = append(lines, m.imrFormatExtensionPathLine(p, meta))
			}
			sections = append(sections, strings.Join(lines, "\n"))
		}

		if len(sections) > 0 {
			text := strings.Join(sections, "\n\n")
			m.imPostUI(func() {
				m.chatContainer.AddChild(tui.NewSpacer(1))
				m.chatContainer.AddChild(components.NewThemedText(func() string { return text }, 1, 0))
				m.imRequestRender()
			})
		}
	}

	if !showDiagnostics {
		return
	}

	m.imrEmitDiagnostics("Skills", skillsRes.Diagnostics)
	m.imrEmitDiagnostics("Prompts", promptsRes.Diagnostics)
	m.imrEmitDiagnostics("Themes", themesRes.Diagnostics)

	if runner := m.imExtensionRunner(); runner != nil {
		m.imrEmitDiagnostics("Shortcuts", runner.GetShortcutDiagnostics())
		m.imrEmitDiagnostics("Commands", runner.GetCommandDiagnostics())
	}

	for _, w := range extRes.Warnings {
		m.ShowWarning(fmt.Sprintf("%s (%s)", w.Warning, m.imrFormatDisplayPath(w.Path)))
	}
	for _, e := range extRes.Errors {
		m.ShowError(fmt.Sprintf("Failed to load extension %s: %s", m.imrFormatDisplayPath(e.Path), e.Error))
	}
}

func (m *InteractiveMode) imrEmitDiagnostics(category string, diags []core.ResourceDiagnostic) {
	if len(diags) == 0 {
		return
	}
	for _, diag := range diags {
		loc := ""
		if diag.Path != nil && *diag.Path != "" {
			loc = " (" + m.imrFormatDisplayPath(*diag.Path) + ")"
		}
		msg := fmt.Sprintf("%s: %s%s", category, diag.Message, loc)
		switch string(diag.Type) {
		case "error":
			m.ShowError(msg)
		case "collision":
			m.ShowWarning(msg)
		default:
			m.ShowWarning(msg)
		}
	}
}

func (m *InteractiveMode) imrFormatDisplayPath(p string) string {
	if p == "" {
		return ""
	}
	if utils.IsSyntheticPath(p) {
		return p
	}
	cwd := m.imCwd()
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			if rel == "." {
				return "./"
			}
			return filepath.ToSlash(rel)
		}
	}
	return imhNormalizePathForDisplay(p)
}

func (m *InteractiveMode) imrScopeBadge(scope utils.SourceScope) string {
	switch scope {
	case utils.SourceScopeUser:
		return "user"
	case utils.SourceScopeProject:
		return "project"
	case utils.SourceScopeTemporary:
		return "temp"
	default:
		return string(scope)
	}
}

func (m *InteractiveMode) imrFormatResourceLine(filePath string, info utils.SourceInfo, label string) string {
	th := theme.CurrentTheme()
	disp := m.imrFormatDisplayPath(filePath)
	badge := ""
	if info.Scope != "" {
		badge = th.Fg(theme.FgMuted, " ["+m.imrScopeBadge(info.Scope)+"]")
	}
	if label != "" {
		return fmt.Sprintf("  %s%s %s", th.Fg(theme.FgText, label), badge, th.Fg(theme.FgDim, disp))
	}
	return fmt.Sprintf("  %s%s", th.Fg(theme.FgDim, disp), badge)
}

func (m *InteractiveMode) imrFormatThemeLine(th *theme.Theme, meta map[string]utils.PathMetadata) string {
	cur := theme.CurrentTheme()
	name := th.Name
	if th.SourcePath != "" {
		disp := m.imrFormatDisplayPath(th.SourcePath)
		scope := ""
		if md, ok := meta[th.SourcePath]; ok && md.Scope != "" {
			scope = cur.Fg(theme.FgMuted, " ["+m.imrScopeBadge(md.Scope)+"]")
		}
		return fmt.Sprintf("  %s%s %s", cur.Fg(theme.FgText, name), scope, cur.Fg(theme.FgDim, disp))
	}
	return fmt.Sprintf("  %s", cur.Fg(theme.FgText, name))
}

func (m *InteractiveMode) imrFormatExtensionPathLine(p string, meta map[string]utils.PathMetadata) string {
	cur := theme.CurrentTheme()
	disp := m.imrFormatDisplayPath(p)
	scope := ""
	if md, ok := meta[p]; ok && md.Scope != "" {
		scope = cur.Fg(theme.FgMuted, " ["+m.imrScopeBadge(md.Scope)+"]")
	} else if !filepath.IsAbs(p) && !utils.IsSyntheticPath(p) {
		if cwd := m.imCwd(); cwd != "" {
			if abs := filepath.Join(cwd, p); abs != "" {
				if md, ok := meta[abs]; ok && md.Scope != "" {
					scope = cur.Fg(theme.FgMuted, " ["+m.imrScopeBadge(md.Scope)+"]")
				}
			}
		}
	}
	return fmt.Sprintf("  %s%s", cur.Fg(theme.FgDim, disp), scope)
}

func (m *InteractiveMode) imrCheckExtensionChangelogs() {
	sm := m.imSettingsManager()
	rl := m.imResourceLoader()
	if sm == nil || rl == nil {
		return
	}
	exts := rl.GetExtensions().Extensions
	for _, ext := range exts {
		extDir := filepath.Dir(ext.Path)
		pkgPath := filepath.Join(extDir, "package.json")
		if _, err := os.Stat(pkgPath); err != nil {
			continue
		}
		changelogPath := filepath.Join(extDir, "CHANGELOG.md")
		if _, err := os.Stat(changelogPath); err != nil {
			continue
		}
		entries, err := utils.ParseChangelog(changelogPath)
		if err != nil || len(entries) == 0 {
			continue
		}
		key := ext.Path
		if ext.ResolvedPath != "" {
			key = ext.ResolvedPath
		}
		last := imhDerefStr(sm.GetExtensionChangelogVersion(key))
		latest := fmt.Sprintf("%d.%d.%d", entries[0].Major, entries[0].Minor, entries[0].Patch)
		if latest == "" || latest == last {
			continue
		}
		newEntries, _ := utils.GetNewEntries(entries, last)
		if len(newEntries) > 0 && last != "" {
			title := filepath.Base(extDir)
			formatted := m.imcFormatChangelogEntries(newEntries)
			if formatted != "" {
				m.imPostUI(func() {
					m.chatContainer.AddChild(tui.NewSpacer(1))
					m.chatContainer.AddChild(components.NewDynamicBorder())
					m.chatContainer.AddChild(components.NewThemedText(func() string {
						return theme.CurrentTheme().Bold(theme.CurrentTheme().Fg(theme.FgAccent, fmt.Sprintf("Extension %s updated", title)))
					}, 1, 0))
					m.chatContainer.AddChild(tui.NewMarkdown(formatted, 1, 1, theme.GetMarkdownTheme(), nil, nil))
					m.chatContainer.AddChild(components.NewDynamicBorder())
					m.imRequestRender()
				})
			}
		}
		sm.SetExtensionChangelogVersion(key, latest)
	}
}
