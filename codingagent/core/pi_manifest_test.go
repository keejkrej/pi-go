package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReadPiManifest(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file returns nil", func(t *testing.T) {
		if m := ReadPiManifest(filepath.Join(dir, "missing.json")); m != nil {
			t.Fatalf("expected nil, got %+v", m)
		}
	})

	t.Run("invalid JSON returns nil", func(t *testing.T) {
		p := filepath.Join(dir, "invalid.json")
		if err := os.WriteFile(p, []byte("{bad"), 0o644); err != nil {
			t.Fatal(err)
		}
		if m := ReadPiManifest(p); m != nil {
			t.Fatalf("expected nil, got %+v", m)
		}
	})

	t.Run("missing or non-object pi section returns nil", func(t *testing.T) {
		p := filepath.Join(dir, "no-pi.json")
		if err := os.WriteFile(p, []byte(`{"name":"pkg","pi":"bad"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if m := ReadPiManifest(p); m != nil {
			t.Fatalf("expected nil, got %+v", m)
		}
	})

	t.Run("parses BOM and validates string arrays and changelogPath", func(t *testing.T) {
		p := filepath.Join(dir, "package.json")
		content := "\uFEFF" + `{
			"name": "demo",
			"pi": {
				"extensions": ["./ext.ts"],
				"skills": ["./skills", 42],
				"prompts": [],
				"themes": ["./theme.json"],
				"changelogPath": "CHANGES.md"
			}
		}`
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		m := ReadPiManifest(p)
		if m == nil {
			t.Fatal("expected non-nil manifest")
		}
		if !slices.Equal(m.Extensions, []string{"./ext.ts"}) {
			t.Fatalf("Extensions = %v", m.Extensions)
		}
		if m.Skills != nil {
			t.Fatalf("expected invalid mixed Skills array to be ignored, got %v", m.Skills)
		}
		if m.Prompts == nil || len(m.Prompts) != 0 {
			t.Fatalf("expected empty non-nil Prompts slice, got %#v", m.Prompts)
		}
		if !slices.Equal(m.Themes, []string{"./theme.json"}) {
			t.Fatalf("Themes = %v", m.Themes)
		}
		if m.ChangelogPath == nil || *m.ChangelogPath != "CHANGES.md" {
			t.Fatalf("ChangelogPath = %v", m.ChangelogPath)
		}
	})
}
