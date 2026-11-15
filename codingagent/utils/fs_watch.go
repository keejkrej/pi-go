// Ported from packages/coding-agent/src/utils/fs-watch.ts (pi v1.5.2).

package utils

import (
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// FsWatchRetryDelayMs is the retry delay in milliseconds after a watch error.
const FsWatchRetryDelayMs = 5000

// FSWatcher wraps an fsnotify.Watcher with clean teardown.
type FSWatcher struct {
	watcher   *fsnotify.Watcher
	closeOnce sync.Once
	done      chan struct{}
}

// Close closes the underlying file watcher and waits for its event loop to exit.
func (w *FSWatcher) Close() error {
	if w == nil || w.watcher == nil {
		return nil
	}
	var closeErr error
	w.closeOnce.Do(func() {
		closeErr = w.watcher.Close()
		<-w.done
	})
	return closeErr
}

// CloseWatcher closes watcher if non-nil, ignoring close errors.
func CloseWatcher(watcher *FSWatcher) {
	if watcher == nil {
		return
	}
	_ = watcher.Close()
}

// WatchWithErrorHandler watches path, invoking listener on events and onError if setup or watching fails.
func WatchWithErrorHandler(
	path string,
	listener func(eventType string, filename string),
	onError func(),
) *FSWatcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		if onError != nil {
			onError()
		}
		return nil
	}

	if err := watcher.Add(path); err != nil {
		_ = watcher.Close()
		if onError != nil {
			onError()
		}
		return nil
	}

	fw := &FSWatcher{
		watcher: watcher,
		done:    make(chan struct{}),
	}

	go func() {
		defer close(fw.done)
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if listener != nil {
					eventType := "change"
					if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
						eventType = "rename"
					}
					listener(eventType, filepath.Base(ev.Name))
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				if err != nil && onError != nil {
					onError()
				}
			}
		}
	}()

	return fw
}
