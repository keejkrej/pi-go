// Ported from packages/coding-agent/src/modes/rpc/jsonl.ts (pi v1.5.2).

package rpc

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// SerializeJsonLine serializes a single strict JSONL record ending with '\n'.
//
// Framing is LF-only. Payload strings may contain other Unicode separators
// such as U+2028 and U+2029 without escaping.
func SerializeJsonLine(value any) (string, error) {
	s, err := jsonx.Stringify(value)
	if err != nil {
		return "", err
	}
	return s + "\n", nil
}

// AttachJsonlLineReader reads LF-delimited lines from r and invokes onLine for
// each line (with at most one trailing '\r' stripped). It splits strictly on
// '\n' so embedded U+2028/U+2029 remain intact, handles arbitrary line lengths,
// and emits a trailing line without '\n' on EOF.
//
// Returns a stop function that suppresses further callbacks and waits if wait
// is unneeded; onEOF (when non-nil) is invoked once after the reader exits.
func AttachJsonlLineReader(
	ctx context.Context,
	r io.Reader,
	onLine func(line string),
	onEOF ...func(),
) (stop func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	var eofFn func()
	if len(onEOF) > 0 {
		eofFn = onEOF[0]
	}

	var stopped atomic.Bool
	var once sync.Once
	cancelCtx, cancel := context.WithCancel(ctx)

	stop = func() {
		once.Do(func() {
			stopped.Store(true)
			cancel()
		})
	}

	if r == nil {
		if eofFn != nil {
			eofFn()
		}
		return stop
	}

	br := bufio.NewReaderSize(r, 64*1024)
	go func() {
		defer func() {
			if eofFn != nil && !stopped.Load() {
				eofFn()
			}
			cancel()
		}()

		for {
			if stopped.Load() || cancelCtx.Err() != nil {
				return
			}
			chunk, err := br.ReadString('\n')
			if len(chunk) > 0 && !stopped.Load() {
				if strings.HasSuffix(chunk, "\n") {
					line := strings.TrimSuffix(chunk, "\n")
					line = strings.TrimSuffix(line, "\r")
					if onLine != nil {
						onLine(line)
					}
				} else if err == io.EOF {
					line := strings.TrimSuffix(chunk, "\r")
					if onLine != nil {
						onLine(line)
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return stop
}
