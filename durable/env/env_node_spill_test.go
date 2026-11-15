// Ported from packages/durable/test/env-node-spill.test.ts (pi v1.0.0).

package env

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/chord"
)

const tensSpillWriteDelay = 600 * time.Millisecond

type tensSlowSpillWriter struct {
	file           *os.File
	rejectedWrites *atomic.Int32
}

func (w *tensSlowSpillWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		w.rejectedWrites.Add(1)
	}
	time.Sleep(tensSpillWriteDelay)
	return w.file.Write(p)
}

func (w *tensSlowSpillWriter) Close() error {
	return w.file.Close()
}

func TestNodeExecutionEnvSpillBackpressure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix subshell test")
	}
	root := t.TempDir()
	e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
	var rejectedWrites atomic.Int32
	e.hooks.wrapSpillWriter = func(_ string, f *os.File) (nodeSpillWriter, int) {
		return &tensSlowSpillWriter{file: f, rejectedWrites: &rejectedWrites}, 1
	}

	// The shell exits after the first chunk crosses the spill threshold and backpressures the spill.
	// A background descendant retains stdout and writes after the post-exit grace period.
	command := "printf '%020d' 0 | tr 0 a; (sleep 0.2; printf '%01000d' 0 | tr 0 b) &"

	var mu sync.Mutex
	var out strings.Builder
	res := e.Exec(command, &ShellExecOptions{
		Spill: &ShellSpillOptions{AfterBytes: 10, AfterLines: 10},
		OnOutput: func(text string, _ chord.Context) error {
			mu.Lock()
			out.WriteString(text)
			mu.Unlock()
			return nil
		},
	}, chord.BackgroundContext)

	got, err := GetOrThrow(res)
	if err != nil {
		t.Fatalf("unexpected execution error: %+v", err)
	}
	if got.SpillPath == "" {
		t.Fatalf("expected SpillPath")
	}
	defer os.RemoveAll(filepath.Dir(got.SpillPath))

	if rejectedWrites.Load() <= 0 {
		t.Fatalf("expected rejectedWrites > 0, got %d", rejectedWrites.Load())
	}
	if out.Len() != 1020 {
		t.Fatalf("expected output length 1020, got %d (%q)", out.Len(), out.String())
	}
	spilled, fErr := GetOrThrow(e.ReadTextFile(got.SpillPath, chord.BackgroundContext))
	if fErr != nil {
		t.Fatalf("ReadTextFile: %+v", fErr)
	}
	want := strings.Repeat("a", 20) + strings.Repeat("b", 1000)
	if spilled != want {
		t.Fatalf("spilled len=%d, want %d", len(spilled), len(want))
	}
}
