package core

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestResourceLoaderThemesAndCollisions(t *testing.T) {
	darkBytes, err := fs.ReadFile(utils.ConfigAssetsFS(), "theme/dark.json")
	if err != nil {
		t.Fatalf("read bundled dark theme: %v", err)
	}
	customThemeJSON := strings.Replace(string(darkBytes), `"name": "dark"`, `"name": "custom-nord"`, 1)

	base := t.TempDir()
	t.Setenv("HOME", base)
	cwd := filepath.Join(base, "proj")
	agentDir := filepath.Join(base, "agent")
	projThemes := filepath.Join(cwd, ".pi", "themes")
	userThemes := filepath.Join(agentDir, "themes")
	for _, d := range []string{projThemes, userThemes} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projFile := filepath.Join(projThemes, "custom-nord.json")
	userFile := filepath.Join(userThemes, "custom-nord.json")
	invalidFile := filepath.Join(userThemes, "broken.json")
	if err := os.WriteFile(projFile, []byte(customThemeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, []byte(customThemeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalidFile, []byte(`{"not":"a theme"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := SettingsManagerInMemory()
	sm.SetProjectTrusted(true)

	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		Cwd:               cwd,
		AgentDir:          agentDir,
		SettingsManager:   sm,
		NoSkills:          true,
		NoPromptTemplates: true,
		NoContextFiles:    true,
	})
	if err := loader.Reload(context.Background()); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	themes := loader.GetThemes()
	if len(themes.Themes) != 1 || themes.Themes[0].Name != "custom-nord" {
		t.Fatalf("expected 1 deduplicated theme custom-nord, got %+v", themes.Themes)
	}
	if themes.Themes[0].SourcePath != projFile {
		t.Fatalf("expected project theme %q to win, got %q", projFile, themes.Themes[0].SourcePath)
	}

	var sawCollision, sawInvalidWarn bool
	for _, d := range themes.Diagnostics {
		if d.Type == ResourceDiagnosticTypeCollision && d.Collision != nil && d.Collision.ResourceType == ResourceCollisionTypeTheme && d.Collision.Name == "custom-nord" {
			sawCollision = true
		}
		if (d.Type == ResourceDiagnosticTypeWarning || d.Type == ResourceDiagnosticTypeError) && d.Path != nil && *d.Path == invalidFile {
			sawInvalidWarn = true
		}
	}
	if !sawCollision {
		t.Fatalf("expected theme collision diagnostic, got %+v", themes.Diagnostics)
	}
	if !sawInvalidWarn {
		t.Fatalf("expected invalid theme diagnostic for %s, got %+v", invalidFile, themes.Diagnostics)
	}
}
