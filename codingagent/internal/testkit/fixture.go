package testkit

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Update controls whether Golden overwrites golden fixture files on disk instead of comparing.
var Update = flag.Bool("update", false, "update golden fixtures")

// RepoRoot returns the repository module root directory by walking up from this source file,
// falling back to the current working directory.
func RepoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if ok {
		if root := fixFindGoModRoot(filepath.Dir(file)); root != "" {
			return root
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatalf("testkit.RepoRoot: %v", err)
	}
	if root := fixFindGoModRoot(dir); root != "" {
		return root
	}
	tb.Fatalf("testkit.RepoRoot: go.mod not found")
	return ""
}

// ModuleRoot is an alias for RepoRoot matching other internal/testkit helpers.
func ModuleRoot(tb testing.TB) string {
	tb.Helper()
	return RepoRoot(tb)
}

func fixFindGoModRoot(startDir string) string {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// FixturePath returns the absolute path to rel inside codingagent/testdata.
func FixturePath(tb testing.TB, rel string) string {
	tb.Helper()
	return filepath.Join(RepoRoot(tb), "codingagent", "testdata", filepath.FromSlash(rel))
}

// Fixture reads and returns the bytes of rel inside codingagent/testdata.
func Fixture(tb testing.TB, rel string) []byte {
	tb.Helper()
	path := FixturePath(tb, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("testkit.Fixture(%q): %v", rel, err)
	}
	return data
}

// Golden compares got against codingagent/testdata/golden/<rel>, updating the file when -update is set.
func Golden(tb testing.TB, rel string, got []byte) {
	tb.Helper()
	path := FixturePath(tb, filepath.Join("golden", filepath.FromSlash(rel)))
	if *Update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatalf("testkit.Golden mkdir(%q): %v", path, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			tb.Fatalf("testkit.Golden write(%q): %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("testkit.Golden read(%q): %v (run with -update to create)", path, err)
	}
	if !bytes.Equal(want, got) {
		tb.Fatalf("golden mismatch for %s:\n--- want ---\n%s\n--- got ---\n%s", rel, want, got)
	}
}
