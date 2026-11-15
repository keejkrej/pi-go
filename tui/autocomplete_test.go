package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func tautoBoolPtr(b bool) *bool { return &b }

func tautoSetupFolder(t *testing.T, baseDir string, dirs []string, files map[string]string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}
	for rel, content := range files {
		full := filepath.Join(baseDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", full, err)
		}
	}
}

func tautoResolveFdPath() *string {
	p, err := exec.LookPath("fd")
	if err != nil || strings.TrimSpace(p) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(p)
	return &trimmed
}

func tautoGetSuggestions(
	t *testing.T,
	provider *CombinedAutocompleteProvider,
	lines []string,
	cursorLine, cursorCol int,
	force bool,
) *AutocompleteSuggestions {
	t.Helper()
	res, err := provider.GetSuggestions(context.Background(), lines, cursorLine, cursorCol, &AutocompleteSuggestionsOptions{
		Force: &force,
	})
	if err != nil {
		t.Fatalf("GetSuggestions(%#v, %d, %d, force=%v) err = %v", lines, cursorLine, cursorCol, force, err)
	}
	return res
}

func TestAutocompleteUnit_ExtractPathPrefixAndTriggers(t *testing.T) {
	t.Run("extracts / from hey / when forced", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, os.TempDir(), nil)
		res := tautoGetSuggestions(t, provider, []string{"hey /"}, 0, 5, true)
		if res == nil || res.Prefix != "/" {
			t.Fatalf("res = %#v, want prefix '/'", res)
		}
	})

	t.Run("extracts /A from /A when forced", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, os.TempDir(), nil)
		res := tautoGetSuggestions(t, provider, []string{"/A"}, 0, 2, true)
		if res != nil && res.Prefix != "/A" {
			t.Fatalf("res.Prefix = %q, want '/A'", res.Prefix)
		}
	})

	t.Run("does not trigger file completion for slash commands", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, os.TempDir(), nil)
		res := tautoGetSuggestions(t, provider, []string{"/model"}, 0, 6, true)
		if res != nil {
			t.Fatalf("res = %#v, want nil for bare slash command without matches", res)
		}
		if provider.ShouldTriggerFileCompletion([]string{"/model"}, 0, 6) {
			t.Fatal("ShouldTriggerFileCompletion(/model) = true, want false")
		}
		if !provider.ShouldTriggerFileCompletion([]string{"/model arg"}, 0, 10) {
			t.Fatal("ShouldTriggerFileCompletion(/model arg) = false, want true")
		}
	})

	t.Run("triggers for absolute paths after slash command argument", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, os.TempDir(), nil)
		res := tautoGetSuggestions(t, provider, []string{"/command /"}, 0, 10, true)
		if res == nil || res.Prefix != "/" {
			t.Fatalf("res = %#v, want prefix '/'", res)
		}
	})

	t.Run("custom trigger characters accessor", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, t.TempDir(), nil)
		provider.SetTriggerCharacters([]string{"$", "#"})
		got := provider.TriggerCharacters()
		if !reflect.DeepEqual(got, []string{"$", "#"}) {
			t.Fatalf("TriggerCharacters = %#v, want [$ #]", got)
		}
	})
}

func TestAutocompleteUnit_DotSlashAndQuotedPathCompletion(t *testing.T) {
	t.Run("completes Chinese path prefixes after whitespace or CJK punctuation on Tab", func(t *testing.T) {
		baseDir := t.TempDir()
		tautoSetupFolder(t, baseDir, nil, map[string]string{
			"说明.md":    "file",
			"文档/说明.md": "nested file",
		})
		provider := NewCombinedAutocompleteProvider(nil, baseDir, nil)
		completions := [][2]string{
			{"说", "说明.md"},
			{"文", "文档/"},
			{"文档/说", "文档/说明.md"},
			{"./文档/说", "./文档/说明.md"},
			{baseDir + "/文档/说", baseDir + "/文档/说明.md"},
		}
		for _, sep := range []rune{' ', '\t', '\u3000', '\u00a0', '，', '：', '；', '。', '！', '？', '（', '「', '《'} {
			for _, tc := range completions {
				before := "查看𠮷" + string(sep)
				line := before + tc[0] + " 后文"
				cursorCol := len(tautoStrToUTF16Len(before + tc[0]))
				_ = cursorCol
				col := len([]uint16{})
				_ = col
				// Use UTF-16 code unit offset matching Editor/Autocomplete convention
				u16Col := tautoUTF16Len(before + tc[0])
				res := tautoGetSuggestions(t, provider, []string{line}, 0, u16Col, true)
				if res == nil {
					t.Fatalf("line %q (col=%d) returned nil", line, u16Col)
				}
				if res.Prefix != tc[0] {
					t.Fatalf("line %q prefix = %q, want %q", line, res.Prefix, tc[0])
				}
				if len(res.Items) != 1 || res.Items[0].Value != tc[1] {
					t.Fatalf("line %q items = %#v, want [%q]", line, res.Items, tc[1])
				}
				applied := provider.ApplyCompletion([]string{line}, 0, u16Col, res.Items[0], res.Prefix)
				wantLine := before + tc[1] + " 后文"
				if applied.Lines[0] != wantLine || applied.CursorCol != tautoUTF16Len(before+tc[1]) {
					t.Fatalf("ApplyCompletion(%q) = %#v, want %q", line, applied, wantLine)
				}
			}
		}
	})

	t.Run("treats unquoted separators as boundaries even when literal path exists", func(t *testing.T) {
		baseDir := t.TempDir()
		tautoSetupFolder(t, baseDir, nil, map[string]string{"归档/说明.md": "other"})
		provider := NewCombinedAutocompleteProvider(nil, baseDir, nil)
		for _, sep := range []string{" ", "\u3000", "，", "。"} {
			directory := "资料" + sep + "归档"
			tautoSetupFolder(t, baseDir, nil, map[string]string{directory + "/说明.md": "archive"})
			for _, marker := range []string{"", "@"} {
				line := marker + directory + "/说"
				res := tautoGetSuggestions(t, provider, []string{line}, 0, tautoUTF16Len(line), true)
				if res == nil || res.Prefix != "归档/说" || len(res.Items) != 1 || res.Items[0].Value != "归档/说明.md" {
					t.Fatalf("unquoted %q got %#v", line, res)
				}
			}
			quoted := `查看，"` + directory + `/说"后文`
			idx := strings.Index(quoted, `"后文`)
			cursorCol := tautoUTF16Len(quoted[:idx])
			res := tautoGetSuggestions(t, provider, []string{quoted}, 0, cursorCol, true)
			if res == nil || res.Prefix != `"`+directory+`/说` || len(res.Items) != 1 || res.Items[0].Value != `"`+directory+`/说明.md"` {
				t.Fatalf("quoted %q got %#v", quoted, res)
			}
			applied := provider.ApplyCompletion([]string{quoted}, 0, cursorCol, res.Items[0], res.Prefix)
			want := `查看，"` + directory + `/说明.md"后文`
			if applied.Lines[0] != want {
				t.Fatalf("applied = %q, want %q", applied.Lines[0], want)
			}
			missing := `查看，"不存在` + sep + `归档/说`
			if got := tautoGetSuggestions(t, provider, []string{missing}, 0, tautoUTF16Len(missing), true); got != nil {
				t.Fatalf("missing quoted path %q returned %#v, want nil", missing, got)
			}
		}
	})

	t.Run("handles empty prefix, opening wrappers, CJK paths, and ./ prefixes", func(t *testing.T) {
		baseDir := t.TempDir()
		tautoSetupFolder(t, baseDir, []string{"src"}, map[string]string{
			"src/main.ts":       "x",
			"src/index.ts":      "x",
			"update.sh":         "x",
			"文档/说明.md":          "x",
			"[slug]/page.tsx":   "x",
			"(group)/layout.ts": "x",
		})
		provider := NewCombinedAutocompleteProvider(nil, baseDir, nil)

		if got := tautoGetSuggestions(t, provider, []string{""}, 0, 0, false); got != nil {
			t.Fatalf("empty unforced returned %#v, want nil", got)
		}

		for _, wrapper := range []string{"(", "[", "{", "<", "`", "((", "(`"} {
			for _, prefix := range []string{"src/ma", "./src/ma"} {
				before := "see " + wrapper
				line := before + prefix
				res := tautoGetSuggestions(t, provider, []string{line}, 0, tautoUTF16Len(line), true)
				wantVal := strings.Replace(prefix, "src/ma", "src/main.ts", 1)
				if res == nil || res.Prefix != prefix || len(res.Items) != 1 || res.Items[0].Value != wantVal {
					t.Fatalf("wrapper line %q -> %#v", line, res)
				}
				applied := provider.ApplyCompletion([]string{line}, 0, tautoUTF16Len(line), res.Items[0], res.Prefix)
				if applied.Lines[0] != before+wantVal {
					t.Fatalf("applied = %q, want %q", applied.Lines[0], before+wantVal)
				}
			}
		}

		resUp := tautoGetSuggestions(t, provider, []string{"./up"}, 0, 4, true)
		if resUp == nil || !tautoHasValue(resUp.Items, "./update.sh") {
			t.Fatalf("./up -> %#v", resUp)
		}
		resDir := tautoGetSuggestions(t, provider, []string{"./sr"}, 0, 4, true)
		if resDir == nil || !tautoHasValue(resDir.Items, "./src/") {
			t.Fatalf("./sr -> %#v", resDir)
		}
		resCjk := tautoGetSuggestions(t, provider, []string{"文档/说"}, 0, tautoUTF16Len("文档/说"), true)
		if resCjk == nil || len(resCjk.Items) != 1 || resCjk.Items[0].Value != "文档/说明.md" {
			t.Fatalf("文档/说 -> %#v", resCjk)
		}
	})

	t.Run("quoted path completion", func(t *testing.T) {
		baseDir := t.TempDir()
		tautoSetupFolder(t, baseDir, []string{"z folder", "z，folder"}, map[string]string{
			"a.txt":               "text",
			"my folder/test.txt":  "content",
			"my folder/other.txt": "content",
		})
		provider := NewCombinedAutocompleteProvider(nil, baseDir, nil)

		res := tautoGetSuggestions(t, provider, []string{""}, 0, 0, true)
		if res == nil || len(res.Items) < 3 {
			t.Fatalf("empty forced suggestions = %#v", res)
		}
		if !strings.HasSuffix(res.Items[0].Label, "/") || !strings.HasSuffix(res.Items[1].Label, "/") {
			t.Fatalf("expected quoted directories sorted first: %#v", res.Items)
		}

		line := `"my folder/"`
		resCont := tautoGetSuggestions(t, provider, []string{line}, 0, tautoUTF16Len(line)-1, true)
		if resCont == nil || !tautoHasValue(resCont.Items, `"my folder/test.txt"`) || !tautoHasValue(resCont.Items, `"my folder/other.txt"`) {
			t.Fatalf("quoted folder continuation = %#v", resCont)
		}

		line2 := `"my folder/te"`
		cursorCol := tautoUTF16Len(line2) - 1
		resApply := tautoGetSuggestions(t, provider, []string{line2}, 0, cursorCol, true)
		if resApply == nil || len(resApply.Items) == 0 {
			t.Fatalf("expected item for %q", line2)
		}
		var item AutocompleteItem
		for _, it := range resApply.Items {
			if it.Value == `"my folder/test.txt"` {
				item = it
				break
			}
		}
		if item.Value == "" {
			t.Fatalf("missing test.txt item in %#v", resApply.Items)
		}
		applied := provider.ApplyCompletion([]string{line2}, 0, cursorCol, item, resApply.Prefix)
		if applied.Lines[0] != `"my folder/test.txt"` {
			t.Fatalf("applied = %q, want without duplicate quote", applied.Lines[0])
		}
	})
}

func TestAutocomplete_FdFuzzyFileSuggestions(t *testing.T) {
	fdPath := tautoResolveFdPath()
	if fdPath == nil {
		t.Skip("fd is not available in PATH")
	}

	t.Run("returns files and folders for empty @ query", func(t *testing.T) {
		baseDir := t.TempDir()
		tautoSetupFolder(t, baseDir, []string{"src"}, map[string]string{"README.md": "readme"})
		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		res := tautoGetSuggestions(t, provider, []string{"@"}, 0, 1, false)
		if res == nil {
			t.Fatal("expected suggestions for @")
		}
		var vals []string
		for _, it := range res.Items {
			vals = append(vals, it.Value)
		}
		slices.Sort(vals)
		want := []string{"@README.md", "@src/"}
		if !reflect.DeepEqual(vals, want) {
			t.Fatalf("vals = %#v, want %#v", vals, want)
		}
	})

	t.Run("regex metacharacters, case sensitivity, dir ranking, hidden/symlinks, and CJK @ attachments", func(t *testing.T) {
		rootDir := t.TempDir()
		baseDir := filepath.Join(rootDir, "cwd")
		outsideDir := filepath.Join(rootDir, "outside")
		tautoSetupFolder(t, baseDir, []string{"src", ".pi", ".git"}, map[string]string{
			"README.md":          "readme",
			"src.txt":            "text",
			"pages/[id].tsx":     "x",
			"pages/(group).ts":   "x",
			".pi/config.json":    "{}",
			".git/config":        "[core]",
			"文档/说明.md":           "text",
			"my folder/test.txt": "content",
			"src/index.ts":       "export {}",
		})
		tautoSetupFolder(t, outsideDir, nil, map[string]string{
			"some_file.txt": "symlinked",
		})
		_ = os.Symlink("../outside", filepath.Join(baseDir, "symlinked_dir"))

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)

		if res := tautoGetSuggestions(t, provider, []string{"@pages/["}, 0, 8, false); res == nil || len(res.Items) != 1 || res.Items[0].Value != "@pages/[id].tsx" {
			t.Fatalf("literal [ query = %#v", res)
		}
		if res := tautoGetSuggestions(t, provider, []string{"@src"}, 0, 4, false); res == nil || res.Items[0].Value != "@src/" || !tautoHasValue(res.Items, "@src.txt") {
			t.Fatalf("@src ranking = %#v", res)
		}
		if res := tautoGetSuggestions(t, provider, []string{"@"}, 0, 1, false); res == nil || !tautoHasValue(res.Items, "@.pi/") || tautoHasValue(res.Items, "@.git/") {
			t.Fatalf("hidden/.git filter = %#v", res)
		}
		if res := tautoGetSuggestions(t, provider, []string{"@some"}, 0, 5, false); res == nil || !tautoHasValue(res.Items, "@symlinked_dir/some_file.txt") {
			t.Fatalf("symlink dir query = %#v", res)
		}
		if res := tautoGetSuggestions(t, provider, []string{"user@example.com"}, 0, 16, false); res != nil {
			t.Fatalf("email should not trigger @ completion: %#v", res)
		}
		if res := tautoGetSuggestions(t, provider, []string{"查看@REA"}, 0, tautoUTF16Len("查看@REA"), false); res != nil {
			t.Fatalf("CJK letter before @ should not trigger: %#v", res)
		}
		resCjkPunct := tautoGetSuggestions(t, provider, []string{"查看，@REA"}, 0, tautoUTF16Len("查看，@REA"), false)
		if resCjkPunct == nil || resCjkPunct.Prefix != "@REA" || !tautoHasValue(resCjkPunct.Items, "@README.md") {
			t.Fatalf("CJK punct before @ = %#v", resCjkPunct)
		}
		resQuoted := tautoGetSuggestions(t, provider, []string{`@"my folder/te"`}, 0, tautoUTF16Len(`@"my folder/te`), false)
		if resQuoted == nil || !tautoHasValue(resQuoted.Items, `@"my folder/test.txt"`) {
			t.Fatalf("quoted @ completion = %#v", resQuoted)
		}
	})
}

func tautoHasValue(items []AutocompleteItem, val string) bool {
	for _, it := range items {
		if it.Value == val {
			return true
		}
	}
	return false
}

func tautoUTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func tautoStrToUTF16Len(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}
