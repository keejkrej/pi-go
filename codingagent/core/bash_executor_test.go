package core

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

type tbeMockBashOps struct {
	execFn func(ctx context.Context, command, cwd string, opts BashOperationsOptions) (BashOperationsResult, error)
}

func (m tbeMockBashOps) Exec(ctx context.Context, command, cwd string, opts BashOperationsOptions) (BashOperationsResult, error) {
	return m.execFn(ctx, command, cwd, opts)
}

func TestExecuteBashWithOperations_TruncationAndSanitization(t *testing.T) {
	cwd := t.TempDir()

	for _, cancelled := range []bool{false, true} {
		name := "completed"
		if cancelled {
			name = "cancelled"
		}
		t.Run("reports discarded chunks when "+name, func(t *testing.T) {
			first := "error: missing build dependency\n" + strings.Repeat("x", 64*1024-len("error: missing build dependency\n"))
			last := "remaining build output\n" + strings.Repeat("y", 44*1024-len("remaining build output\n"))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ops := tbeMockBashOps{
				execFn: func(_ context.Context, _, _ string, opts BashOperationsOptions) (BashOperationsResult, error) {
					opts.OnData([]byte(first))
					opts.OnData([]byte(last))
					if cancelled {
						cancel()
						return BashOperationsResult{}, errors.New("aborted")
					}
					code := 0
					return BashOperationsResult{ExitCode: &code}, nil
				},
			}

			res, err := ExecuteBashWithOperations(context.Background(), "cat build.log", cwd, ops, &BashExecutorOptions{
				Signal: ctx,
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.FullOutputPath == nil {
				t.Fatalf("expected FullOutputPath to be set")
			}
			defer os.Remove(*res.FullOutputPath)

			fullBytes, err := os.ReadFile(*res.FullOutputPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(fullBytes) != first+last {
				t.Fatalf("full output mismatch: len=%d want=%d", len(fullBytes), len(first)+len(last))
			}
			if res.Cancelled != cancelled {
				t.Fatalf("Cancelled=%v want=%v", res.Cancelled, cancelled)
			}
			if res.Output != last {
				t.Fatalf("Output len=%d want=%d", len(res.Output), len(last))
			}
			if !res.Truncated {
				t.Fatalf("expected Truncated=true")
			}
		})
	}

	t.Run("does not mark short output as truncated and strips ANSI", func(t *testing.T) {
		var streamed strings.Builder
		var streamMu sync.Mutex

		ops := tbeMockBashOps{
			execFn: func(_ context.Context, _, _ string, opts BashOperationsOptions) (BashOperationsResult, error) {
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					opts.OnData([]byte("\x1b[32mdone\x1b[0m\r\n"))
				}()
				go func() {
					defer wg.Done()
					opts.OnData([]byte("second\n"))
				}()
				wg.Wait()
				code := 0
				return BashOperationsResult{ExitCode: &code}, nil
			},
		}

		res, err := ExecuteBashWithOperations(context.Background(), "echo done", cwd, ops, &BashExecutorOptions{
			OnChunk: func(chunk string) {
				streamMu.Lock()
				streamed.WriteString(chunk)
				streamMu.Unlock()
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Truncated || res.FullOutputPath != nil {
			t.Fatalf("expected short output untruncated: %+v", res)
		}
		if strings.Contains(res.Output, "\x1b[") || strings.Contains(res.Output, "\r") {
			t.Fatalf("expected sanitized ANSI and CR stripped: %q", res.Output)
		}
		if !strings.Contains(streamed.String(), "done\n") || !strings.Contains(streamed.String(), "second\n") {
			t.Fatalf("unexpected streamed output: %q", streamed.String())
		}
	})
}
