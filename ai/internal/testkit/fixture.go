package testkit

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// Update controls whether Golden overwrites golden fixture files on disk instead of comparing.
var Update = flag.Bool("update", false, "update golden fixtures")

// RepoRoot walks upward from the current working directory until go.mod is found.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("testkit.RepoRoot: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("testkit.RepoRoot: go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// FixturePath returns the absolute path to rel inside ai/testdata.
func FixturePath(t testing.TB, rel string) string {
	t.Helper()
	return filepath.Join(RepoRoot(t), "ai", "testdata", filepath.FromSlash(rel))
}

// Fixture reads and returns the bytes of rel inside ai/testdata.
func Fixture(t testing.TB, rel string) []byte {
	t.Helper()
	path := FixturePath(t, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("testkit.Fixture(%q): %v", rel, err)
	}
	return data
}

// Golden compares got against ai/testdata/golden/<rel>, updating the file when -update is set.
func Golden(t testing.TB, rel string, got []byte) {
	t.Helper()
	path := FixturePath(t, filepath.Join("golden", filepath.FromSlash(rel)))
	if *Update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("testkit.Golden mkdir(%q): %v", path, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("testkit.Golden write(%q): %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("testkit.Golden read(%q): %v (run with -update to create)", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("golden mismatch for %s:\n--- want ---\n%s\n--- got ---\n%s", rel, want, got)
	}
}
