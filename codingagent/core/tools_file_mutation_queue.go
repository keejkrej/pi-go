// Ported from packages/coding-agent/src/core/tools/file-mutation-queue.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type tfmqQueueEntry struct {
	mu   sync.Mutex
	refs int
}

var (
	tfmqMapMu sync.Mutex
	tfmqMap   = make(map[string]*tfmqQueueEntry)
)

func tfmqIsMissingPathError(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR)
}

func tfmqGetMutationQueueKey(filePath string) (string, error) {
	resolvedPath, err := filepath.Abs(filePath)
	if err != nil {
		resolvedPath = filepath.Clean(filePath)
	}
	realPath, err := filepath.EvalSymlinks(resolvedPath)
	if err == nil {
		return realPath, nil
	}
	if tfmqIsMissingPathError(err) {
		return resolvedPath, nil
	}
	return "", err
}

// WithFileMutationQueue serializes file mutation operations targeting the same canonical file path
// while allowing operations for distinct files to execute concurrently.
func WithFileMutationQueue[T any](ctx context.Context, filePath string, fn func() (T, error)) (T, error) {
	var zero T
	key, err := tfmqGetMutationQueueKey(filePath)
	if err != nil {
		return zero, err
	}

	tfmqMapMu.Lock()
	entry := tfmqMap[key]
	if entry == nil {
		entry = &tfmqQueueEntry{}
		tfmqMap[key] = entry
	}
	entry.refs++
	tfmqMapMu.Unlock()

	defer func() {
		tfmqMapMu.Lock()
		entry.refs--
		if entry.refs == 0 && tfmqMap[key] == entry {
			delete(tfmqMap, key)
		}
		tfmqMapMu.Unlock()
	}()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if ctx != nil && ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return fn()
}
