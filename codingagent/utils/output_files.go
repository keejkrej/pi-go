// Ported from packages/coding-agent/src/utils/output-files.ts (pi v1.5.2).

package utils

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

const ofOutputFileMode os.FileMode = 0o600

func ofCreateOutputFilePath(prefix, extension string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	name := prefix + "-" + hex.EncodeToString(b[:]) + extension
	return filepath.Join(os.TempDir(), name), nil
}

// WriteOutputFile writes data to a newly created private temp output file (O_CREATE|O_EXCL, 0600)
// and returns its path.
func WriteOutputFile(prefix, extension string, data []byte) (string, error) {
	p, f, err := CreateOutputFileStream(prefix, extension)
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(p)
		return "", writeErr
	}
	if closeErr != nil {
		_ = os.Remove(p)
		return "", closeErr
	}
	return p, nil
}

// CreateOutputFileStream opens a new private temp output file (O_CREATE|O_EXCL, 0600) for streaming writes.
func CreateOutputFileStream(prefix, extension string) (string, *os.File, error) {
	p, err := ofCreateOutputFilePath(prefix, extension)
	if err != nil {
		return "", nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, ofOutputFileMode)
	if err != nil {
		return "", nil, err
	}
	return p, f, nil
}
