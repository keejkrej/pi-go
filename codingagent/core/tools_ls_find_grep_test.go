package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestLsTool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tool := CreateLsTool(dir)

	t.Run("lists dotfiles and marks directories alphabetically", func(t *testing.T) {
		sub := filepath.Join(dir, "basic")
		if err := os.MkdirAll(filepath.Join(sub, "subdir"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{".hidden", "beta.txt", "alpha.txt", "éclair.txt", "zulu.txt"} {
			if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		res, err := tool.Execute(ctx, "ls-1", jsonx.ObjectOf("path", "basic"), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := ttrTestTextOutput(res)
		want := ".hidden\nalpha.txt\nbeta.txt\néclair.txt\nsubdir/\nzulu.txt"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("respects limit and populates EntryLimitReached", func(t *testing.T) {
		sub := filepath.Join(dir, "limited")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
			if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		res, err := tool.Execute(ctx, "ls-2", jsonx.ObjectOf("path", "limited", "limit", 2), nil)
		if err != nil {
			t.Fatal(err)
		}
		out := ttrTestTextOutput(res)
		if !strings.Contains(out, "a.txt\nb.txt") || !strings.Contains(out, "2 entries limit reached") {
			t.Fatalf("unexpected output: %q", out)
		}
		details, ok := res.Details.(*LsToolDetails)
		if !ok || details == nil || details.EntryLimitReached == nil || *details.EntryLimitReached != 2 {
			t.Fatalf("unexpected details: %#v", res.Details)
		}
	})

	t.Run("returns empty directory message and errors on file or missing path", func(t *testing.T) {
		emptyDir := filepath.Join(dir, "empty")
		if err := os.MkdirAll(emptyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := tool.Execute(ctx, "ls-3", jsonx.ObjectOf("path", "empty"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(res); got != "(empty directory)" {
			t.Fatalf("got %q", got)
		}

		filePath := filepath.Join(dir, "single.txt")
		if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Execute(ctx, "ls-4", jsonx.ObjectOf("path", "single.txt"), nil); err == nil {
			t.Fatalf("expected error listing non-directory")
		}
		if _, err := tool.Execute(ctx, "ls-5", jsonx.ObjectOf("path", "missing-dir"), nil); err == nil {
			t.Fatalf("expected error listing missing path")
		}
	})

	t.Run("supports custom LsOperations", func(t *testing.T) {
		customTool := CreateLsTool(dir, &LsToolOptions{
			Operations: &LsOperations{
				Exists: func(_ context.Context, _ string) (bool, error) { return true, nil },
				Stat: func(_ context.Context, p string) (LsFileInfo, error) {
					return os.Stat(dir)
				},
				Readdir: func(_ context.Context, _ string) ([]string, error) {
					return []string{"z.go", "a.go"}, nil
				},
			},
		})
		res, err := customTool.Execute(ctx, "ls-custom", jsonx.ObjectOf("path", "virtual"), nil)
		if err != nil {
			t.Fatal(err)
		}
		out := ttrTestTextOutput(res)
		if !strings.Contains(out, "a.go/") || !strings.Contains(out, "z.go/") {
			t.Fatalf("unexpected custom ls output: %q", out)
		}
	})
}

func TestFindTool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t.Run("RelativizeFindResultPath preserves trailing slash and relative structure", func(t *testing.T) {
		searchPath := filepath.Join(dir, "repo")
		if got := RelativizeFindResultPath(filepath.Join(searchPath, "src", "main.go"), searchPath); got != "src/main.go" {
			t.Fatalf("got %q", got)
		}
		if got := RelativizeFindResultPath(filepath.Join(searchPath, "src")+string(os.PathSeparator), searchPath); got != "src/" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("does not spawn when context is already aborted", func(t *testing.T) {
		abortedCtx, cancel := context.WithCancel(context.Background())
		cancel()

		globCalled := false
		tool := CreateFindTool(dir, &FindToolOptions{
			Operations: &FindOperations{
				Exists: func(_ context.Context, _ string) (bool, error) { return true, nil },
				Glob: func(_ context.Context, _ string, _ string, _ FindGlobOptions) ([]string, error) {
					globCalled = true
					return nil, nil
				},
			},
		})
		_, err := tool.Execute(abortedCtx, "find-abort", jsonx.ObjectOf("pattern", "*.go"), nil)
		if err == nil || !strings.Contains(err.Error(), "Operation aborted") {
			t.Fatalf("expected Operation aborted, got %v", err)
		}
		if globCalled {
			t.Fatalf("Glob must not be called when already aborted")
		}
	})

	t.Run("custom FindOperations relays pattern, ignore, limit, and relativizes results", func(t *testing.T) {
		var gotPattern, gotCwd string
		var gotOpts FindGlobOptions

		tool := CreateFindTool(dir, &FindToolOptions{
			Operations: &FindOperations{
				Exists: func(_ context.Context, _ string) (bool, error) { return true, nil },
				Glob: func(_ context.Context, pattern, cwd string, options FindGlobOptions) ([]string, error) {
					gotPattern = pattern
					gotCwd = cwd
					gotOpts = options
					return []string{
						filepath.Join(cwd, "nested", "file.txt"),
						filepath.Join(cwd, "sub") + string(os.PathSeparator),
						filepath.Join(cwd, "extra.txt"),
					}, nil
				},
			},
		})
		res, err := tool.Execute(ctx, "find-1", jsonx.ObjectOf("pattern", "*.txt", "limit", 2), nil)
		if err != nil {
			t.Fatal(err)
		}
		if gotPattern != "*.txt" || gotCwd != dir || gotOpts.Limit != 3 {
			t.Fatalf("unexpected glob args: pattern=%q cwd=%q opts=%+v", gotPattern, gotCwd, gotOpts)
		}
		if !slices.Contains(gotOpts.Ignore, "**/node_modules/**") || !slices.Contains(gotOpts.Ignore, "**/.git/**") {
			t.Fatalf("missing default ignore patterns: %v", gotOpts.Ignore)
		}
		out := ttrTestTextOutput(res)
		wantOut := "nested/file.txt\nsub/\n\n[2 results limit reached]"
		if out != wantOut {
			t.Fatalf("got output %q, want %q", out, wantOut)
		}
		details, ok := res.Details.(*FindToolDetails)
		if !ok || details == nil || details.ResultLimitReached == nil || *details.ResultLimitReached != 2 {
			t.Fatalf("unexpected details: %#v", res.Details)
		}
	})

	t.Run("tfMatchWindowsPathPattern uses minimatch Dot, NoExt, and NoCase semantics", func(t *testing.T) {
		if !tfMatchWindowsPathPattern("src/**/*", "src/.hidden/file.ts", false) {
			t.Fatalf("expected Dot=true match for hidden directory")
		}
		if !tfMatchWindowsPathPattern("src/*.ts", "SRC/A.TS", true) {
			t.Fatalf("expected NoCase=true match")
		}
		if !tfMatchWindowsPathPattern("a+(b).ts", "a+(b).ts", false) || tfMatchWindowsPathPattern("a+(b).ts", "abb.ts", false) {
			t.Fatalf("expected NoExt=true literal extglob behavior")
		}
	})

	t.Run("runs default fd backend when fd is available", func(t *testing.T) {
		if utils.GetToolPath("fd") == nil {
			t.Setenv("PI_OFFLINE", "1")
			t.Skip("fd binary not installed")
		}
		repo := filepath.Join(dir, "fdrepo")
		if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "sub", "kept.txt"), []byte("ok"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "sub", "ignored.txt"), []byte("no"), 0o600); err != nil {
			t.Fatal(err)
		}

		tool := CreateFindTool(repo)
		res, err := tool.Execute(ctx, "find-fd", jsonx.ObjectOf("pattern", "*.txt"), nil)
		if err != nil {
			t.Fatal(err)
		}
		out := ttrTestTextOutput(res)
		if !strings.Contains(out, "sub/kept.txt") {
			t.Fatalf("expected sub/kept.txt in output: %q", out)
		}
	})
}

func TestGrepTool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t.Run("returns error offline when rg is missing", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
		t.Setenv("PATH", t.TempDir())
		t.Setenv("PI_OFFLINE", "1")

		tool := CreateGrepTool(dir)
		_, err := tool.Execute(ctx, "grep-offline", jsonx.ObjectOf("pattern", "hello"), nil)
		if err == nil || !strings.Contains(err.Error(), "ripgrep (rg) is not available") {
			t.Fatalf("expected rg unavailable error, got %v", err)
		}
	})

	t.Run("supports custom GrepOperations without invoking deprecated ReadFile", func(t *testing.T) {
		if utils.GetToolPath("rg") == nil {
			t.Setenv("PI_OFFLINE", "1")
			t.Skip("rg binary not installed")
		}
		target := filepath.Join(dir, "sample.txt")
		if err := os.WriteFile(target, []byte("alpha\nbeta needle\ngamma\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		isDirCalled := false
		readFileCalled := false
		tool := CreateGrepTool(dir, &GrepToolOptions{
			Operations: &GrepOperations{
				IsDirectory: func(_ context.Context, _ string) (bool, error) {
					isDirCalled = true
					return false, nil
				},
				ReadFile: func(_ context.Context, _ string) (string, error) {
					readFileCalled = true
					return "", os.ErrPermission
				},
			},
		})
		res, err := tool.Execute(ctx, "grep-ops", jsonx.ObjectOf("pattern", "needle", "path", "sample.txt", "context", 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !isDirCalled {
			t.Fatalf("expected IsDirectory to be called")
		}
		if readFileCalled {
			t.Fatalf("deprecated ReadFile callback should not be invoked by grep")
		}
		out := ttrTestTextOutput(res)
		if !strings.Contains(out, "sample.txt-1- alpha") ||
			!strings.Contains(out, "sample.txt:2: beta needle") ||
			!strings.Contains(out, "sample.txt-3- gamma") {
			t.Fatalf("unexpected grep output:\n%s", out)
		}
	})

	t.Run("handles glob, ignoreCase, literal, limit, and no-match messages", func(t *testing.T) {
		if utils.GetToolPath("rg") == nil {
			t.Setenv("PI_OFFLINE", "1")
			t.Skip("rg binary not installed")
		}
		searchRoot := filepath.Join(dir, "src")
		if err := os.MkdirAll(searchRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(searchRoot, "a.go"), []byte("needle[1] foo\nNeedle[1] bar\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(searchRoot, "b.md"), []byte("needle[1] ignored by glob\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		tool := CreateGrepTool(dir)
		res, err := tool.Execute(ctx, "grep-1", jsonx.ObjectOf(
			"pattern", "needle[1]",
			"path", "src",
			"glob", "*.go",
			"ignoreCase", true,
			"literal", true,
			"limit", 1,
		), nil)
		if err != nil {
			t.Fatal(err)
		}
		out := ttrTestTextOutput(res)
		if !strings.Contains(out, "a.go:1: needle[1] foo") ||
			strings.Contains(out, "b.md") ||
			!strings.Contains(out, "1 matches limit reached") {
			t.Fatalf("unexpected grep output:\n%s", out)
		}
		details, ok := res.Details.(*GrepToolDetails)
		if !ok || details == nil || details.MatchLimitReached == nil || *details.MatchLimitReached != 1 {
			t.Fatalf("unexpected grep details: %#v", res.Details)
		}

		noMatch, err := tool.Execute(ctx, "grep-2", jsonx.ObjectOf("pattern", "definitely-missing-xyz", "path", "src"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(noMatch); got != "No matches found" {
			t.Fatalf("got %q, want %q", got, "No matches found")
		}
	})

	t.Run("honors pre-canceled context", func(t *testing.T) {
		abortedCtx, cancel := context.WithTimeout(context.Background(), 0)
		cancel()
		time.Sleep(time.Millisecond)
		tool := CreateGrepTool(dir)
		_, err := tool.Execute(abortedCtx, "grep-abort", jsonx.ObjectOf("pattern", "x"), nil)
		if err == nil || !strings.Contains(err.Error(), "Operation aborted") {
			t.Fatalf("expected Operation aborted, got %v", err)
		}
	})
}
