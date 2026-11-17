package utils

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFrontmatterAndBom(t *testing.T) {
	content := "---\nname: demo-skill\ndescription: A useful skill\n---\n\n# Heading\nBody text\n"
	parsed, err := ParseFrontmatter[map[string]any](content)
	if err != nil {
		t.Fatalf("ParseFrontmatter failed: %v", err)
	}
	if parsed.Frontmatter["name"] != "demo-skill" || parsed.Frontmatter["description"] != "A useful skill" {
		t.Fatalf("unexpected frontmatter: %+v", parsed.Frontmatter)
	}
	if !strings.HasPrefix(parsed.Body, "# Heading") {
		t.Fatalf("unexpected body: %q", parsed.Body)
	}

	// UTF-8 BOM stripped cleanly before frontmatter parsing (#8337)
	withBom := "\ufeff" + content
	parsedBom, err := ParseFrontmatter[map[string]any](withBom)
	if err != nil || parsedBom.Frontmatter["name"] != "demo-skill" {
		t.Fatalf("ParseFrontmatter with BOM failed: got=%+v err=%v", parsedBom, err)
	}

	// Plain content without frontmatter
	plain := "# No frontmatter\nJust body"
	parsedPlain, err := ParseFrontmatter[map[string]any](plain)
	if err != nil || parsedPlain.Body != plain {
		t.Fatalf("plain frontmatter parse mismatch: %+v err=%v", parsedPlain, err)
	}

	// Unclosed frontmatter leaves content intact as body.
	unclosed := "---\nname: unclosed\nbody"
	if _, err := ParseFrontmatter[map[string]any](unclosed); err != nil {
		t.Fatalf("unexpected error on unclosed frontmatter: %v", err)
	}

	// StripFrontmatter
	stripped, err := StripFrontmatter(content)
	if err != nil || stripped != parsed.Body {
		t.Fatalf("StripFrontmatter mismatch: got=%q want=%q err=%v", stripped, parsed.Body, err)
	}

	// Malformed YAML returns error.
	if _, err := ParseFrontmatter[map[string]any]("---\nfoo: [bar\n---\n"); err == nil {
		t.Fatal("expected error for malformed YAML frontmatter")
	}

	// BOM helpers
	split := SplitBom("\ufeffhello")
	if split.Bom != "\ufeff" || split.Text != "hello" || StripBom("\ufeffhello") != "hello" {
		t.Fatalf("BOM split mismatch: %+v", split)
	}
}

func TestAnsiHelpers(t *testing.T) {
	if got := StripAnsi("\x1b[31mred\x1b[0m plain"); got != "red plain" {
		t.Fatalf("StripAnsi = %q", got)
	}
	if got := StripAnsi("\x1b]8;;https://example.com\x07link\x1b]8;;\x07"); got != "link" {
		t.Fatalf("StripAnsi OSC8 = %q", got)
	}

	cases := []struct {
		input    string
		complete string
		pending  string
	}{
		{"plain", "plain", ""},
		{"hello\x1b", "hello", "\x1b"},
		{"hello\x1b[38;2;1", "hello", "\x1b[38;2;1"},
		{"hello\x1b[31m", "hello\x1b[31m", ""},
		{"hello\x1b]0;title", "hello", "\x1b]0;title"},
		{"hello\x1b]0;title\x07", "hello\x1b]0;title\x07", ""},
		{"hello\x1b]8;;url\x1b\\", "hello\x1b]8;;url\x1b\\", ""},
	}
	for _, tc := range cases {
		res := SplitIncompleteAnsiSuffix(tc.input)
		if res.Complete != tc.complete || res.Pending != tc.pending {
			t.Fatalf("SplitIncompleteAnsiSuffix(%q) = %+v, want (%q, %q)", tc.input, res, tc.complete, tc.pending)
		}
	}
}

func TestPathsAndNormalization(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := NormalizePath("~/project/file.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != filepath.Join(home, "project", "file.txt") {
		t.Fatalf("NormalizePath tilde = %q", expanded)
	}

	atExpanded, err := NormalizePath("  @~/foo.txt  ", &PathInputOptions{
		Trim:          true,
		StripAtPrefix: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if atExpanded != filepath.Join(home, "foo.txt") {
		t.Fatalf("NormalizePath @~ = %q", atExpanded)
	}

	shortened := ShortenHomePath(filepath.Join(home, "work", "app"))
	if shortened != "~/work/app" {
		t.Fatalf("ShortenHomePath = %q", shortened)
	}

	tmp := t.TempDir()
	sub := filepath.Join(tmp, "a", "b.txt")
	if err := os.MkdirAll(filepath.Dir(sub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsPathWithin(tmp, sub) {
		t.Fatalf("expected %s to be within %s", sub, tmp)
	}
	if IsPathWithin(sub, tmp) {
		t.Fatalf("parent dir %s must not be within child %s", tmp, sub)
	}

	rel := GetCwdRelativePath(sub, tmp)
	if rel == nil || *rel != filepath.Join("a", "b.txt") {
		t.Fatalf("GetCwdRelativePath = %v", rel)
	}

	rev1 := GetFileRevision(sub)
	if rev1 == nil || *rev1 == "" {
		t.Fatal("expected non-empty GetFileRevision")
	}

	resolved, err := ResolvePath("a/b.txt", tmp, nil)
	if err != nil || !PathsEqual(resolved, sub) {
		t.Fatalf("ResolvePath mismatch: resolved=%q err=%v", resolved, err)
	}

	if got := NormalizeWindowsShellPath("/cygdrive/c/Users/alice"); got == "" {
		t.Fatal("expected non-empty NormalizeWindowsShellPath")
	}
	if !IsLocalPath("./relative/file.go") {
		t.Fatal("expected relative file to be local path")
	}
}

func TestJsonCommentsWslDeprecationAbortAndSleep(t *testing.T) {
	stripped := StripJsonComments(`{
		// line comment
		"url": "https://example.com//path",
		"ok": true,
	}`)
	if strings.Contains(stripped, "line comment") || strings.Contains(stripped, ",\n\t}") {
		t.Fatalf("comments or trailing comma not stripped: %q", stripped)
	}
	if !strings.Contains(stripped, `"https://example.com//path"`) {
		t.Fatalf("string URL corrupted by comment stripper: %q", stripped)
	}

	if !IsWSL(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}) {
		t.Fatal("expected WSL_DISTRO_NAME to report IsWSL=true")
	}

	// Deprecation deduplication
	ClearDeprecationWarningsForTests()
	origWarn := deprWarnFn
	var warnings []string
	deprWarnFn = func(msg string) { warnings = append(warnings, msg) }
	t.Cleanup(func() {
		deprWarnFn = origWarn
		ClearDeprecationWarningsForTests()
	})
	WarnDeprecation("deprecated feature X")
	WarnDeprecation("deprecated feature X")
	if len(warnings) != 1 {
		t.Fatalf("expected warning once, got %d", len(warnings))
	}

	// Sleep & Abort
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sleep(ctx, 5000); err == nil || err.Error() != "Aborted" {
		t.Fatalf("expected Aborted error on Sleep, got %v", err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel2()
	}()
	_, err := RaceWithAbortSignal(ctx2, func() (int, error) {
		time.Sleep(200 * time.Millisecond)
		return 42, nil
	})
	var abortErr *AbortError
	if !errors.As(err, &abortErr) {
		t.Fatalf("expected *AbortError, got %v", err)
	}
}
