package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPathAndResolveToCwd(t *testing.T) {
	if got := ExpandPath("~"); strings.Contains(got, "~") {
		t.Fatalf("expected ~ expansion, got %q", got)
	}
	if got := ExpandPath("~/Documents/file.txt"); strings.Contains(got, "~/") {
		t.Fatalf("expected ~/ expansion, got %q", got)
	}
	if got := ExpandPath("~draft.md"); got != "~draft.md" {
		t.Fatalf("got %q, want ~draft.md", got)
	}
	if got := ExpandPath("@~draft.md"); got != "~draft.md" {
		t.Fatalf("got %q, want ~draft.md", got)
	}
	if got := ExpandPath("file\u00A0name.txt"); got != "file name.txt" {
		t.Fatalf("got %q, want %q", got, "file name.txt")
	}

	absTarget := filepath.Join(os.TempDir(), "abs-test", "file.txt")
	if got := ResolveToCwd(absTarget, filepath.Join(os.TempDir(), "other")); got != absTarget {
		t.Fatalf("got %q, want %q", got, absTarget)
	}
	cwd := filepath.Join(os.TempDir(), "pi-path-utils-cwd")
	if got := ResolveToCwd("@~draft.md", cwd); got != filepath.Join(cwd, "~draft.md") {
		t.Fatalf("got %q, want %q", got, filepath.Join(cwd, "~draft.md"))
	}
}

func TestResolveReadPath_Variants(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("existing file", func(t *testing.T) {
		p := filepath.Join(tempDir, "test-file.txt")
		if err := os.WriteFile(p, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ResolveReadPath("test-file.txt", tempDir); got != p {
			t.Fatalf("got %q, want %q", got, p)
		}
	})

	t.Run("NFD vs NFC Unicode normalization", func(t *testing.T) {
		nfdName := "file\u0065\u0301.txt"
		nfcName := "file\u00e9.txt"
		if err := os.WriteFile(filepath.Join(tempDir, nfdName), []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
		got := ResolveReadPath(nfcName, tempDir)
		if !strings.HasPrefix(got, tempDir) || !strings.HasSuffix(got, ".txt") {
			t.Fatalf("unexpected resolved path %q", got)
		}
		if _, err := os.Stat(got); err != nil {
			t.Fatalf("resolved path %q does not stat: %v", got, err)
		}
	})

	t.Run("curly quotes vs straight quotes", func(t *testing.T) {
		curly := "Capture d\u2019cran.txt"
		straight := "Capture d'cran.txt"
		want := filepath.Join(tempDir, curly)
		if err := os.WriteFile(want, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ResolveReadPath(straight, tempDir); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("combined NFC + curly quote", func(t *testing.T) {
		curly := "Capture d\u2019\u00e9cran.txt"
		straight := "Capture d'\u00e9cran.txt"
		want := filepath.Join(tempDir, curly)
		if err := os.WriteFile(want, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ResolveReadPath(straight, tempDir); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("macOS screenshot NNBSP AM/PM variants", func(t *testing.T) {
		for _, suffix := range []string{"AM.png", "am.png"} {
			macosName := "Screenshot 2024-01-01 at 10.00.00\u202F" + suffix
			userName := "Screenshot 2024-01-01 at 10.00.00 " + suffix
			want := filepath.Join(tempDir, macosName)
			if err := os.WriteFile(want, []byte("content"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := ResolveReadPath(userName, tempDir); got != want {
				t.Fatalf("suffix %s: got %q, want %q", suffix, got, want)
			}
		}
	})
}
