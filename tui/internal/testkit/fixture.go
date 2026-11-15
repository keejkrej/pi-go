package testkit

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ModuleRoot returns the repository module root directory by walking up from this source file.
func ModuleRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if ok {
		dir := filepath.Dir(file)
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	tb.Fatalf("locate go.mod from testkit")
	return ""
}

// FixturePath returns the absolute path to a fixture file under tui/testdata.
func FixturePath(tb testing.TB, rel string) string {
	tb.Helper()
	return filepath.Join(ModuleRoot(tb), "tui", "testdata", rel)
}

// Fixture reads and returns a fixture file under tui/testdata.
func Fixture(tb testing.TB, rel string) []byte {
	tb.Helper()
	path := FixturePath(tb, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}
