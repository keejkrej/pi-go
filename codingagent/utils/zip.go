// Ported from packages/coding-agent/src/utils/zip.ts (pi v1.5.2).

package utils

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ZipEntry represents a single named file inside an in-memory ZIP archive.
type ZipEntry struct {
	Name string
	Data []byte
}

// CreateZip builds a compressed ZIP archive containing the provided entries.
func CreateZip(entries []ZipEntry) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		name := filepath.ToSlash(entry.Name)
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := w.Write(entry.Data); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExtractZip extracts a ZIP archive at archivePath into destDir, rejecting Zip Slip paths.
func ExtractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return err
	}

	for _, f := range r.File {
		targetPath := filepath.Join(absDest, f.Name)
		rel, err := filepath.Rel(absDest, targetPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("zip entry escapes destination directory: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeOutErr := out.Close()
		closeRcErr := rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		if closeRcErr != nil {
			return closeRcErr
		}
	}
	return nil
}
