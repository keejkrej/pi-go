// Ported from packages/coding-agent/test/file-processor.test.ts (pi v1.5.2).

package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tfpWriteTinyPng(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func TestProcessFileArgumentsTextBomEmptyImageAndMissing(t *testing.T) {
	dir := t.TempDir()

	// Text file with UTF-8 BOM and relative path via Cwd option.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), append([]byte("\xef\xbb\xbf"), []byte("hello world\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	// Empty file should be skipped without output.
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// PNG image file.
	imgPath := filepath.Join(dir, "pixel.png")
	tfpWriteTinyPng(t, imgPath)

	resize := false
	res, err := ProcessFileArguments([]string{"notes.txt", "empty.txt", "pixel.png"}, &ProcessFileOptions{
		AutoResizeImages: &resize,
		Cwd:              dir,
	})
	if err != nil {
		t.Fatalf("ProcessFileArguments: %v", err)
	}

	absNotes := filepath.Join(dir, "notes.txt")
	if !strings.Contains(res.Text, "<file name=\""+absNotes+"\">\nhello world\n\n</file>") {
		t.Fatalf("unexpected text output for notes.txt:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "\xef\xbb\xbf") {
		t.Fatal("expected UTF-8 BOM stripped from file content")
	}
	if strings.Contains(res.Text, "empty.txt") {
		t.Fatalf("expected empty.txt skipped, got:\n%s", res.Text)
	}
	if len(res.Images) != 1 || res.Images[0].Type != "image" || res.Images[0].MimeType != "image/png" || res.Images[0].Data == "" {
		t.Fatalf("unexpected image attachments: %+v", res.Images)
	}
	if !strings.Contains(res.Text, "<file name=\""+imgPath+"\">") {
		t.Fatalf("expected XML tag for image path, got:\n%s", res.Text)
	}

	if _, err := ProcessFileArguments([]string{"does-not-exist.txt"}, &ProcessFileOptions{Cwd: dir}); err == nil || !strings.Contains(err.Error(), "File not found:") {
		t.Fatalf("expected File not found error, got %v", err)
	}
}
