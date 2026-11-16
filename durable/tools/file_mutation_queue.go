// Ported from packages/durable/src/tools/file-mutation-queue.ts (pi v1.0.0).

package tools

import (
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
)

var (
	fmqMu     sync.Mutex
	fmqQueues = make(map[string]chan struct{})
)

func fmqMutationKey(executionEnv env.ExecutionEnv, path string, ctx chord.Context) (string, error) {
	absolutePath, err := env.GetOrThrow(executionEnv.AbsolutePath(path, ctx))
	if err != nil {
		return "", err
	}
	canon, err := fmqCanonical(executionEnv, absolutePath, ctx)
	if err != nil {
		return "", err
	}
	return executionEnv.Id() + "\x00" + canon, nil
}

// fmqCanonical resolves canonicalPath, falling back to canonical parent + child name when not found.
func fmqCanonical(executionEnv env.ExecutionEnv, absolutePath string, ctx chord.Context) (string, error) {
	result := executionEnv.CanonicalPath(absolutePath, ctx)
	if result.Ok {
		return result.Value, nil
	}
	if result.Error != nil && result.Error.Code == env.FileErrorNotSupported {
		return absolutePath, nil
	}
	if result.Error == nil || result.Error.Code != env.FileErrorNotFound {
		return "", env.ToError(result.Error)
	}

	parent, err := env.GetOrThrow(executionEnv.JoinPath([]string{absolutePath, ".."}, ctx))
	if err != nil {
		return "", err
	}
	if parent == absolutePath || !strings.HasPrefix(absolutePath, parent) {
		return absolutePath, nil
	}

	sepLen := 1
	if strings.HasSuffix(parent, "/") || strings.HasSuffix(parent, "\\") {
		sepLen = 0
	}
	if len(absolutePath) < len(parent)+sepLen {
		return absolutePath, nil
	}
	name := absolutePath[len(parent)+sepLen:]

	canonParent, err := fmqCanonical(executionEnv, parent, ctx)
	if err != nil {
		return "", err
	}
	return env.GetOrThrow(executionEnv.JoinPath([]string{canonParent, name}, ctx))
}

// WithFileMutationQueue serializes edits and writes to one canonical file path per file system within this process.
func WithFileMutationQueue[T any](
	executionEnv env.ExecutionEnv,
	path string,
	fn func() (T, error),
	ctx chord.Context,
) (T, error) {
	key, err := fmqMutationKey(executionEnv, path, ctx)
	if err != nil {
		var zero T
		return zero, err
	}

	// Claim our tail slot before waiting so queued callers preserve registration order.
	fmqMu.Lock()
	previous := fmqQueues[key]
	done := make(chan struct{})
	fmqQueues[key] = done
	fmqMu.Unlock()

	if previous != nil {
		<-previous
	}

	defer func() {
		fmqMu.Lock()
		if fmqQueues[key] == done {
			delete(fmqQueues, key)
		}
		fmqMu.Unlock()
		close(done)
	}()

	return fn()
}
