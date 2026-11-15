// Ported from packages/durable/test/tools.test.ts (pi v1.0.0).

package tools_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsdiff"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func ttNewEnv(t *testing.T, opts ...env.NodeExecutionEnvOptions) *env.NodeExecutionEnv {
	t.Helper()
	dir := t.TempDir()
	var opt env.NodeExecutionEnvOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.Cwd == "" {
		opt.Cwd = dir
	}
	executionEnv := env.NewNodeExecutionEnv(opt)
	t.Cleanup(func() {
		executionEnv.Cleanup(chord.BackgroundContext)
	})
	return executionEnv
}

type ttFakeApi struct {
	durable.ToolExecutionApi
	executionEnv env.ExecutionEnv
	mu           sync.Mutex
	output       []string
	diagnostics  []durable.ToolDiagnostic
}

func ttNewFakeApi(executionEnv env.ExecutionEnv) *ttFakeApi {
	return &ttFakeApi{executionEnv: executionEnv}
}

func (a *ttFakeApi) TaskId() durable.TaskId                 { return durable.IdFromNumber[durable.TaskId](1) }
func (a *ttFakeApi) ConversationId() durable.ConversationId { return durable.RootConversationId }
func (a *ttFakeApi) CallId() string                         { return "call" }
func (a *ttFakeApi) Env(_ chord.Context) (env.ExecutionEnv, error) {
	return a.executionEnv, nil
}
func (a *ttFakeApi) Output(text string) {
	a.mu.Lock()
	a.output = append(a.output, text)
	a.mu.Unlock()
}
func (a *ttFakeApi) OutputBytes(data []byte) {
	a.mu.Lock()
	a.output = append(a.output, string(data))
	a.mu.Unlock()
}
func (a *ttFakeApi) Diagnostic(d durable.ToolDiagnostic) {
	a.mu.Lock()
	a.diagnostics = append(a.diagnostics, d)
	a.mu.Unlock()
}
func (a *ttFakeApi) Details(_ durable.JsonValue, _ chord.Context) error { return nil }

type ttRunResult struct {
	*durable.ToolExecutionResult
	output   []string
	reported []durable.ToolDiagnostic
}

func ttRun(t *testing.T, tool *durable.ToolRegistration, args any, executionEnv env.ExecutionEnv, ctx ...chord.Context) ttRunResult {
	t.Helper()
	callCtx := chord.BackgroundContext
	if len(ctx) > 0 && ctx[0] != nil {
		callCtx = ctx[0]
	}
	api := ttNewFakeApi(executionEnv)
	res, err := tool.Execute(args, api, callCtx)
	if err != nil {
		t.Fatalf("tool %s failed unexpectedly: %v", tool.Name, err)
	}
	return ttRunResult{
		ToolExecutionResult: res,
		output:              api.output,
		reported:            api.diagnostics,
	}
}

type ttFailResult struct {
	err      error
	output   []string
	reported []durable.ToolDiagnostic
}

func ttRunFailing(t *testing.T, tool *durable.ToolRegistration, args any, executionEnv env.ExecutionEnv, ctx ...chord.Context) ttFailResult {
	t.Helper()
	callCtx := chord.BackgroundContext
	if len(ctx) > 0 && ctx[0] != nil {
		callCtx = ctx[0]
	}
	api := ttNewFakeApi(executionEnv)
	_, err := tool.Execute(args, api, callCtx)
	if err == nil {
		t.Fatalf("expected tool %s to return an error", tool.Name)
	}
	return ttFailResult{
		err:      err,
		output:   api.output,
		reported: api.diagnostics,
	}
}

func ttTextOutput(res *durable.ToolExecutionResult) string {
	var parts []string
	for _, part := range res.Content {
		if tc, ok := part.(*ai.TextContent); ok && tc.Type == "text" {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func ttDiagnosticText(res *durable.ToolExecutionResult) string {
	var parts []string
	for _, d := range res.Diagnostics {
		parts = append(parts, d.Message)
	}
	return strings.Join(parts, "\n")
}

func ttMustWriteText(t *testing.T, executionEnv env.ExecutionEnv, path, content string) {
	t.Helper()
	if _, err := env.GetOrThrow(executionEnv.WriteFile(path, []byte(content), chord.BackgroundContext)); err != nil {
		t.Fatalf("WriteFile(%q) failed: %v", path, err)
	}
}

func ttMustReadText(t *testing.T, executionEnv env.ExecutionEnv, path string) string {
	t.Helper()
	got, err := env.GetOrThrow(executionEnv.ReadTextFile(path, chord.BackgroundContext))
	if err != nil {
		t.Fatalf("ReadTextFile(%q) failed: %v", path, err)
	}
	return got
}

type ttSlowReadEnv struct {
	*env.NodeExecutionEnv
}

func (e *ttSlowReadEnv) ReadTextFile(path string, ctx chord.Context) env.Result[string, *env.FileError] {
	time.Sleep(20 * time.Millisecond)
	return e.NodeExecutionEnv.ReadTextFile(path, ctx)
}

type ttBlockingWriteEnv struct {
	*env.NodeExecutionEnv
	firstWriteStarted chan struct{}
	firstOnce         sync.Once
	finishFirstWrite  chan struct{}
	mu                sync.Mutex
	secondWriteCalled bool
}

func ttNewBlockingWriteEnv(t *testing.T, cwd string) *ttBlockingWriteEnv {
	t.Helper()
	base := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd})
	t.Cleanup(func() { base.Cleanup(chord.BackgroundContext) })
	return &ttBlockingWriteEnv{
		NodeExecutionEnv:  base,
		firstWriteStarted: make(chan struct{}),
		finishFirstWrite:  make(chan struct{}),
	}
}

func (e *ttBlockingWriteEnv) WriteFile(path string, content []byte, ctx chord.Context) env.Result[struct{}, *env.FileError] {
	s := string(content)
	if s == "first\n" {
		e.firstOnce.Do(func() { close(e.firstWriteStarted) })
		<-e.finishFirstWrite
	} else if s == "second\n" {
		e.mu.Lock()
		e.secondWriteCalled = true
		e.mu.Unlock()
	}
	return e.NodeExecutionEnv.WriteFile(path, content, ctx)
}

func (e *ttBlockingWriteEnv) secondWriteStarted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.secondWriteCalled
}

type ttOtherFileSystemEnv struct {
	*ttBlockingWriteEnv
}

func (e *ttOtherFileSystemEnv) Id() string { return "other" }

type ttBlockingEditEnv struct {
	*env.NodeExecutionEnv
	firstEditStarted  chan struct{}
	firstOnce         sync.Once
	finishFirstEdit   chan struct{}
	mu                sync.Mutex
	firstSettled      bool
	secondWriteCalled bool
}

func ttNewBlockingEditEnv(t *testing.T, cwd string) *ttBlockingEditEnv {
	t.Helper()
	base := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd})
	t.Cleanup(func() { base.Cleanup(chord.BackgroundContext) })
	return &ttBlockingEditEnv{
		NodeExecutionEnv: base,
		firstEditStarted: make(chan struct{}),
		finishFirstEdit:  make(chan struct{}),
	}
}

func (e *ttBlockingEditEnv) WriteFile(path string, content []byte, ctx chord.Context) env.Result[struct{}, *env.FileError] {
	s := string(content)
	if s == "ALPHA\nbeta\n" {
		e.firstOnce.Do(func() { close(e.firstEditStarted) })
		<-e.finishFirstEdit
		res := e.NodeExecutionEnv.WriteFile(path, content, chord.BackgroundContext)
		e.mu.Lock()
		e.firstSettled = true
		e.mu.Unlock()
		return res
	}
	if s == "ALPHA\nBETA\n" || s == "alpha\nBETA\n" {
		e.mu.Lock()
		e.secondWriteCalled = true
		e.mu.Unlock()
	}
	return e.NodeExecutionEnv.WriteFile(path, content, ctx)
}

func (e *ttBlockingEditEnv) secondEditStarted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.secondWriteCalled
}

func (e *ttBlockingEditEnv) firstEditSettled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.firstSettled
}

const ttTruncatedOutputLines = durable.DefaultMaxLines + 1

type ttTimeoutOutputEnv struct {
	*env.NodeExecutionEnv
}

func (e *ttTimeoutOutputEnv) Exec(_ string, options *env.ShellExecOptions, ctx chord.Context) env.Result[env.ShellExecResult, *env.ExecutionError] {
	lines := make([]string, ttTruncatedOutputLines)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i+1)
	}
	output := strings.Join(lines, "\n") + "\n"
	spillPath, err := env.GetOrThrow(e.CreateTempFile(&env.CreateTempFileOptions{Prefix: "timeout-", Suffix: ".log"}, ctx))
	if err != nil {
		return env.Err[env.ShellExecResult](&env.ExecutionError{Code: env.ExecutionErrorUnknown, Message: err.Error(), Cause: err})
	}
	if _, err := env.GetOrThrow(e.WriteFile(spillPath, []byte(output), ctx)); err != nil {
		return env.Err[env.ShellExecResult](&env.ExecutionError{Code: env.ExecutionErrorUnknown, Message: err.Error(), Cause: err})
	}
	if options != nil && options.OnOutput != nil {
		_ = options.OnOutput(output, ctx)
	}
	var timeoutDisplay any
	if options != nil && options.Timeout != nil {
		timeoutDisplay = *options.Timeout
	}
	return env.Err[env.ShellExecResult](&env.ExecutionError{
		Code:      env.ExecutionErrorTimeout,
		Message:   fmt.Sprintf("timeout:%v", timeoutDisplay),
		SpillPath: spillPath,
	})
}

func TestDurableTools(t *testing.T) {
	t.Run("fail with an ordinary error when no environment is configured", func(t *testing.T) {
		fail := ttRunFailing(t, tools.CreateReadTool(), jsonx.ObjectOf("path", "x"), nil)
		if !strings.Contains(fail.err.Error(), "No execution environment") {
			t.Fatalf("expected 'No execution environment', got %v", fail.err)
		}
	})

	t.Run("read", func(t *testing.T) {
		for _, signature := range []string{"GIF87a", "GIF89a"} {
			t.Run("detects the complete "+signature+" signature", func(t *testing.T) {
				if got := tools.DetectSupportedImageMimeType([]byte(signature)); got != "image/gif" {
					t.Fatalf("expected image/gif for %s, got %q", signature, got)
				}
			})
		}

		t.Run("reads text with offsets and limits and reports continuation as a diagnostic", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			lines := make([]string, 100)
			for i := range lines {
				lines[i] = fmt.Sprintf("Line %d", i+1)
			}
			ttMustWriteText(t, executionEnv, "test.txt", strings.Join(lines, "\n"))

			result := ttRun(t, tools.CreateReadTool(), jsonx.ObjectOf("path", "test.txt", "offset", float64(41), "limit", float64(20)), executionEnv)
			output := ttTextOutput(result.ToolExecutionResult)
			if strings.Contains(output, "Line 40") {
				t.Fatalf("unexpected Line 40 in %q", output)
			}
			if !strings.Contains(output, "Line 41") || !strings.Contains(output, "Line 60") {
				t.Fatalf("expected Line 41..60 in %q", output)
			}
			if strings.Contains(output, "Line 61") {
				t.Fatalf("unexpected Line 61 in %q", output)
			}
			if strings.Contains(output, "more lines") {
				t.Fatalf("unexpected continuation note in content: %q", output)
			}
			if got := ttDiagnosticText(result.ToolExecutionResult); got != "40 more lines in file. Use offset=61 to continue." {
				t.Fatalf("unexpected diagnostic %q", got)
			}
		})

		t.Run("truncates large text by line count", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			lines := make([]string, 2500)
			for i := range lines {
				lines[i] = fmt.Sprintf("Line %d", i+1)
			}
			ttMustWriteText(t, executionEnv, "large.txt", strings.Join(lines, "\n"))

			result := ttRun(t, tools.CreateReadTool(), jsonx.ObjectOf("path", "large.txt"), executionEnv)
			if got := ttDiagnosticText(result.ToolExecutionResult); got != "Showing lines 1-2000 of 2500. Use offset=2001 to continue." {
				t.Fatalf("unexpected diagnostic %q", got)
			}
			if len(result.Diagnostics) == 0 || result.Diagnostics[0].Code == nil || *result.Diagnostics[0].Code != "truncated" {
				t.Fatalf("expected truncated diagnostic code, got %+v", result.Diagnostics)
			}
			details, ok := result.Details.(*tools.ReadToolDetails)
			if !ok || details == nil || details.Truncation == nil {
				t.Fatalf("expected *tools.ReadToolDetails with Truncation, got %T %#v", result.Details, result.Details)
			}
			truncBy, _ := details.Truncation.TruncatedBy.Get()
			if !details.Truncation.Truncated || truncBy != "lines" || details.Truncation.TotalLines != 2500 || details.Truncation.OutputLines != 2000 {
				t.Fatalf("unexpected truncation details: %+v", details.Truncation)
			}
		})

		t.Run("does not count a trailing newline as an extra line at the truncation limit", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			lines := make([]string, 2000)
			for i := range lines {
				lines[i] = "x"
			}
			ttMustWriteText(t, executionEnv, "exact.txt", strings.Join(lines, "\n")+"\n")

			result := ttRun(t, tools.CreateReadTool(), jsonx.ObjectOf("path", "exact.txt"), executionEnv)
			if result.Details != nil {
				t.Fatalf("expected nil Details, got %#v", result.Details)
			}
			if len(result.Diagnostics) != 0 {
				t.Fatalf("expected empty diagnostics, got %+v", result.Diagnostics)
			}
		})

		t.Run("shows the start of a line longer than the byte limit", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			ttMustWriteText(t, executionEnv, "long.txt", strings.Repeat("é", 40_000)+"\nnext\n")

			result := ttRun(t, tools.CreateReadTool(), jsonx.ObjectOf("path", "long.txt"), executionEnv)
			text := ttTextOutput(result.ToolExecutionResult)
			if text != strings.Repeat("é", 25_600) {
				t.Fatalf("expected 25600 'é' runes, got len=%d bytes=%d", len([]rune(text)), len(text))
			}
			wantDiag := "Line 1 is 78.1KB, exceeds the 50.0KB limit; showing its first 50.0KB. Use bash: sed -n '1p' long.txt | tail -c +51201"
			if got := ttDiagnosticText(result.ToolExecutionResult); got != wantDiag {
				t.Fatalf("expected diagnostic %q, got %q", wantDiag, got)
			}
			details, ok := result.Details.(*tools.ReadToolDetails)
			if !ok || details == nil || details.Truncation == nil {
				t.Fatalf("expected *tools.ReadToolDetails with Truncation, got %#v", result.Details)
			}
			tr := details.Truncation
			if !tr.Truncated || !tr.FirstLineExceedsLimit || tr.OutputBytes != 51_200 || tr.OutputLines != 1 {
				t.Fatalf("unexpected truncation: %+v", tr)
			}
			encoded, err := jsonx.Stringify(details)
			if err != nil {
				t.Fatalf("stringify details failed: %v", err)
			}
			if strings.Contains(encoded, `"content"`) {
				t.Fatalf("expected truncation not to carry content field, got %s", encoded)
			}
		})

		t.Run("rejects offsets beyond the file", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			ttMustWriteText(t, executionEnv, "short.txt", "one\ntwo\nthree")
			fail := ttRunFailing(t, tools.CreateReadTool(), jsonx.ObjectOf("path", "short.txt", "offset", float64(100)), executionEnv)
			if !strings.Contains(fail.err.Error(), "Offset 100 is beyond end of file (3 lines total)") {
				t.Fatalf("unexpected error %v", fail.err)
			}
		})

		t.Run("reports images by content as unsupported", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg==")
			if err != nil {
				t.Fatalf("decode png failed: %v", err)
			}
			if _, err := env.GetOrThrow(executionEnv.WriteFile("image.txt", png, chord.BackgroundContext)); err != nil {
				t.Fatalf("WriteFile png failed: %v", err)
			}
			result := ttRun(t, tools.CreateReadTool(), jsonx.ObjectOf("path", "image.txt"), executionEnv)
			if !result.IsError || len(result.Content) != 0 {
				t.Fatalf("expected error with empty content, got isError=%v content=%#v", result.IsError, result.Content)
			}
			want := "image.txt is an image (image/png); reading images is not supported"
			if got := ttDiagnosticText(result.ToolExecutionResult); got != want {
				t.Fatalf("expected %q, got %q", want, got)
			}
		})
	})

	t.Run("write", func(t *testing.T) {
		t.Run("writes files and creates parent directories", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			result := ttRun(t, tools.CreateWriteTool(), jsonx.ObjectOf("path", "nested/dir/file.txt", "content", "hello"), executionEnv)
			if got := ttTextOutput(result.ToolExecutionResult); got != "Successfully wrote to nested/dir/file.txt" {
				t.Fatalf("unexpected output %q", got)
			}
			if got := ttMustReadText(t, executionEnv, "nested/dir/file.txt"); got != "hello" {
				t.Fatalf("expected 'hello', got %q", got)
			}
		})

		t.Run("keeps the mutation queue locked until an aborted write settles", func(t *testing.T) {
			executionEnv := ttNewBlockingWriteEnv(t, t.TempDir())
			tool := tools.CreateWriteTool()
			sig, cancel := context.WithCancel(context.Background())
			defer cancel()

			type resErr struct {
				err error
			}
			firstCh := make(chan resErr, 1)
			go func() {
				api := ttNewFakeApi(executionEnv)
				_, err := tool.Execute(jsonx.ObjectOf("path", "file.txt", "content", "first\n"), api, chord.WithAbortSignal(sig, chord.BackgroundContext))
				firstCh <- resErr{err: err}
			}()

			<-executionEnv.firstWriteStarted
			cancel()

			secondDone := make(chan struct{})
			go func() {
				api := ttNewFakeApi(executionEnv)
				_, err := tool.Execute(jsonx.ObjectOf("path", "file.txt", "content", "second\n"), api, chord.BackgroundContext)
				if err != nil {
					t.Errorf("second write failed: %v", err)
				}
				close(secondDone)
			}()

			time.Sleep(20 * time.Millisecond)
			if executionEnv.secondWriteStarted() {
				t.Fatalf("expected second write to wait on file mutation queue")
			}
			close(executionEnv.finishFirstWrite)

			firstRes := <-firstCh
			if firstRes.err == nil {
				t.Fatalf("expected aborted first write to fail")
			}
			<-secondDone
			if got := ttMustReadText(t, executionEnv, "file.txt"); got != "second\n" {
				t.Fatalf("expected 'second\\n', got %q", got)
			}
		})
	})

	t.Run("edit", func(t *testing.T) {
		t.Run("applies disjoint edits and returns both diff formats", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			original := "alpha\nbeta\ngamma\ndelta\n"
			ttMustWriteText(t, executionEnv, "edit.txt", original)

			result := ttRun(t, tools.CreateEditTool(), jsonx.ObjectOf(
				"path", "edit.txt",
				"edits", []any{
					jsonx.ObjectOf("oldText", "alpha\n", "newText", "ALPHA\n"),
					jsonx.ObjectOf("oldText", "gamma\n", "newText", "GAMMA\n"),
				},
			), executionEnv)

			if got := ttTextOutput(result.ToolExecutionResult); got != "Successfully replaced 2 block(s) in edit.txt." {
				t.Fatalf("unexpected text output %q", got)
			}
			details, ok := result.Details.(*tools.EditToolDetails)
			if !ok || details == nil {
				t.Fatalf("expected *tools.EditToolDetails, got %#v", result.Details)
			}
			if !strings.Contains(details.Diff, "ALPHA") || !strings.Contains(details.Diff, "GAMMA") {
				t.Fatalf("unexpected diff %q", details.Diff)
			}
			patched, appliedOk, err := jsdiff.ApplyPatch(original, details.Patch, nil)
			if err != nil || !appliedOk || patched != "ALPHA\nbeta\nGAMMA\ndelta\n" {
				t.Fatalf("unexpected patch result patched=%q ok=%v err=%v", patched, appliedOk, err)
			}
			if got := ttMustReadText(t, executionEnv, "edit.txt"); got != "ALPHA\nbeta\nGAMMA\ndelta\n" {
				t.Fatalf("unexpected file contents %q", got)
			}
		})

		t.Run("repairs edits sent as a JSON string, a single object, or top-level oldText/newText without mutating them", func(t *testing.T) {
			prepare := tools.CreateEditTool().PrepareArguments
			if prepare == nil {
				t.Fatalf("expected PrepareArguments on edit tool")
			}
			edit := jsonx.ObjectOf("oldText", "a", "newText", "b")
			editsJSON, _ := jsonx.Stringify([]any{edit})
			editJSON, _ := jsonx.Stringify(edit)

			asString := jsonx.ObjectOf("path", "f", "edits", editsJSON)
			wantSingle := jsonx.ObjectOf("path", "f", "edits", []any{edit})
			if !jsonx.DeepEqual(prepare(asString), wantSingle) {
				t.Fatalf("string array repair mismatch: %#v", prepare(asString))
			}
			if got, _ := asString.Get("edits"); got != editsJSON {
				t.Fatalf("expected original input not mutated, got %#v", got)
			}
			if !jsonx.DeepEqual(prepare(jsonx.ObjectOf("path", "f", "edits", editJSON)), wantSingle) {
				t.Fatalf("string single object repair mismatch")
			}
			if !jsonx.DeepEqual(prepare(jsonx.ObjectOf("path", "f", "edits", edit)), wantSingle) {
				t.Fatalf("single object repair mismatch")
			}
			wantAppended := jsonx.ObjectOf("path", "f", "edits", []any{edit, jsonx.ObjectOf("oldText", "c", "newText", "d")})
			if !jsonx.DeepEqual(prepare(jsonx.ObjectOf("path", "f", "edits", []any{edit}, "oldText", "c", "newText", "d")), wantAppended) {
				t.Fatalf("top-level oldText/newText repair mismatch")
			}
			wantNotJson := jsonx.ObjectOf("path", "f", "edits", "not json")
			if !jsonx.DeepEqual(prepare(wantNotJson), wantNotJson) {
				t.Fatalf("non-JSON edits string should remain unchanged")
			}
		})

		t.Run("matches all edits against the original and rejects overlaps", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			ttMustWriteText(t, executionEnv, "edit.txt", "one\ntwo\nthree\n")

			fail := ttRunFailing(t, tools.CreateEditTool(), jsonx.ObjectOf(
				"path", "edit.txt",
				"edits", []any{
					jsonx.ObjectOf("oldText", "one\ntwo\n", "newText", "ONE\nTWO\n"),
					jsonx.ObjectOf("oldText", "two\nthree\n", "newText", "TWO\nTHREE\n"),
				},
			), executionEnv)
			if !strings.Contains(fail.err.Error(), "overlap") {
				t.Fatalf("expected overlap error, got %v", fail.err)
			}
			if got := ttMustReadText(t, executionEnv, "edit.txt"); got != "one\ntwo\nthree\n" {
				t.Fatalf("expected unchanged file, got %q", got)
			}
		})

		t.Run("rejects missing and duplicate target text", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			ttMustWriteText(t, executionEnv, "edit.txt", "foo foo foo")
			tool := tools.CreateEditTool()

			missing := ttRunFailing(t, tool, jsonx.ObjectOf(
				"path", "edit.txt",
				"edits", []any{jsonx.ObjectOf("oldText", "bar", "newText", "baz")},
			), executionEnv)
			if !strings.Contains(missing.err.Error(), "Could not find the exact text") {
				t.Fatalf("expected missing text error, got %v", missing.err)
			}

			dup := ttRunFailing(t, tool, jsonx.ObjectOf(
				"path", "edit.txt",
				"edits", []any{jsonx.ObjectOf("oldText", "foo", "newText", "bar")},
			), executionEnv)
			if !strings.Contains(dup.err.Error(), "Found 3 occurrences") {
				t.Fatalf("expected duplicate error, got %v", dup.err)
			}
		})

		t.Run("keeps the mutation queue locked until an aborted edit write settles", func(t *testing.T) {
			executionEnv := ttNewBlockingEditEnv(t, t.TempDir())
			ttMustWriteText(t, executionEnv, "file.txt", "alpha\nbeta\n")
			tool := tools.CreateEditTool()
			sig, cancel := context.WithCancel(context.Background())
			defer cancel()

			firstCh := make(chan error, 1)
			go func() {
				api := ttNewFakeApi(executionEnv)
				_, err := tool.Execute(jsonx.ObjectOf(
					"path", "file.txt",
					"edits", []any{jsonx.ObjectOf("oldText", "alpha", "newText", "ALPHA")},
				), api, chord.WithAbortSignal(sig, chord.BackgroundContext))
				firstCh <- err
			}()

			<-executionEnv.firstEditStarted
			cancel()

			secondDone := make(chan struct{})
			go func() {
				api := ttNewFakeApi(executionEnv)
				if _, err := tool.Execute(jsonx.ObjectOf(
					"path", "file.txt",
					"edits", []any{jsonx.ObjectOf("oldText", "beta", "newText", "BETA")},
				), api, chord.BackgroundContext); err != nil {
					t.Errorf("second edit failed: %v", err)
				}
				close(secondDone)
			}()

			time.Sleep(20 * time.Millisecond)
			if executionEnv.secondEditStarted() {
				t.Fatalf("expected second edit to wait for aborted first edit write to settle")
			}
			close(executionEnv.finishFirstEdit)

			firstErr := <-firstCh
			if firstErr == nil || !strings.Contains(firstErr.Error(), "Operation aborted") {
				t.Fatalf("expected Operation aborted, got %v", firstErr)
			}
			<-secondDone
			if !executionEnv.firstEditSettled() {
				t.Fatalf("expected firstEditSettled=true")
			}
			if got := ttMustReadText(t, executionEnv, "file.txt"); got != "ALPHA\nBETA\n" {
				t.Fatalf("expected 'ALPHA\\nBETA\\n', got %q", got)
			}
		})

		t.Run("serializes concurrent edits through canonical and symlink paths", func(t *testing.T) {
			executionEnv := &ttSlowReadEnv{NodeExecutionEnv: ttNewEnv(t)}
			ttMustWriteText(t, executionEnv, "target.txt", "alpha\nbeta\ngamma\n")
			if err := os.Symlink("target.txt", filepath.Join(executionEnv.Cwd(), "link.txt")); err != nil {
				t.Fatalf("symlink failed: %v", err)
			}
			tool := tools.CreateEditTool()
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				ttRun(t, tool, jsonx.ObjectOf("path", "target.txt", "edits", []any{jsonx.ObjectOf("oldText", "alpha", "newText", "ALPHA")}), executionEnv)
			}()
			go func() {
				defer wg.Done()
				ttRun(t, tool, jsonx.ObjectOf("path", "link.txt", "edits", []any{jsonx.ObjectOf("oldText", "beta", "newText", "BETA")}), executionEnv)
			}()
			wg.Wait()
			if got := ttMustReadText(t, executionEnv, "target.txt"); got != "ALPHA\nBETA\ngamma\n" {
				t.Fatalf("expected 'ALPHA\\nBETA\\ngamma\\n', got %q", got)
			}
		})

		t.Run("serializes edits of one file across environment objects of one file system", func(t *testing.T) {
			dir := t.TempDir()
			first := &ttSlowReadEnv{NodeExecutionEnv: ttNewEnv(t, env.NodeExecutionEnvOptions{Cwd: dir})}
			second := &ttSlowReadEnv{NodeExecutionEnv: ttNewEnv(t, env.NodeExecutionEnvOptions{Cwd: dir})}
			ttMustWriteText(t, first, "file.txt", "alpha\nbeta\n")
			tool := tools.CreateEditTool()
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				ttRun(t, tool, jsonx.ObjectOf("path", "file.txt", "edits", []any{jsonx.ObjectOf("oldText", "alpha", "newText", "ALPHA")}), first)
			}()
			go func() {
				defer wg.Done()
				ttRun(t, tool, jsonx.ObjectOf("path", "file.txt", "edits", []any{jsonx.ObjectOf("oldText", "beta", "newText", "BETA")}), second)
			}()
			wg.Wait()
			if got := ttMustReadText(t, first, "file.txt"); got != "ALPHA\nBETA\n" {
				t.Fatalf("expected 'ALPHA\\nBETA\\n', got %q", got)
			}
		})

		t.Run("serializes a new file created through a symlinked directory with its canonical path", func(t *testing.T) {
			executionEnv := ttNewBlockingWriteEnv(t, t.TempDir())
			realDir := filepath.Join(executionEnv.Cwd(), "real")
			linkDir := filepath.Join(executionEnv.Cwd(), "link")
			if err := os.Mkdir(realDir, 0o755); err != nil {
				t.Fatalf("mkdir failed: %v", err)
			}
			if err := os.Symlink(realDir, linkDir); err != nil {
				t.Fatalf("symlink failed: %v", err)
			}
			tool := tools.CreateWriteTool()
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				ttRun(t, tool, jsonx.ObjectOf("path", "link/new.txt", "content", "first\n"), executionEnv)
			}()
			<-executionEnv.firstWriteStarted
			go func() {
				defer wg.Done()
				ttRun(t, tool, jsonx.ObjectOf("path", "real/new.txt", "content", "second\n"), executionEnv)
			}()
			time.Sleep(20 * time.Millisecond)
			if executionEnv.secondWriteStarted() {
				t.Fatalf("expected second write on canonical path to wait on symlinked queue")
			}
			close(executionEnv.finishFirstWrite)
			wg.Wait()
			if got := ttMustReadText(t, executionEnv, "real/new.txt"); got != "second\n" {
				t.Fatalf("expected 'second\\n', got %q", got)
			}
		})

		t.Run("keys a missing file whose name contains a backslash like the created file", func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("backslash is a path separator on Windows")
			}
			executionEnv := ttNewEnv(t)
			created := make(chan struct{})
			release := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, err := tools.WithFileMutationQueue(executionEnv, `a\b.txt`, func() (struct{}, error) {
					ttMustWriteText(t, executionEnv, `a\b.txt`, "first\n")
					close(created)
					<-release
					return struct{}{}, nil
				}, chord.BackgroundContext)
				if err != nil {
					t.Errorf("first queued op failed: %v", err)
				}
			}()
			<-created

			var mu sync.Mutex
			entered := false
			go func() {
				defer wg.Done()
				_, err := tools.WithFileMutationQueue(executionEnv, `a\b.txt`, func() (struct{}, error) {
					mu.Lock()
					entered = true
					mu.Unlock()
					return struct{}{}, nil
				}, chord.BackgroundContext)
				if err != nil {
					t.Errorf("second queued op failed: %v", err)
				}
			}()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			enteredBeforeRelease := entered
			mu.Unlock()
			if enteredBeforeRelease {
				t.Fatalf("expected second op to wait until first released")
			}
			close(release)
			wg.Wait()
			mu.Lock()
			enteredAfter := entered
			mu.Unlock()
			if !enteredAfter {
				t.Fatalf("expected second op to have entered")
			}
		})

		t.Run("does not serialize the same path on different file systems", func(t *testing.T) {
			dir := t.TempDir()
			local := ttNewBlockingWriteEnv(t, dir)
			other := &ttOtherFileSystemEnv{ttBlockingWriteEnv: ttNewBlockingWriteEnv(t, dir)}
			tool := tools.CreateWriteTool()
			done := make(chan struct{})
			go func() {
				ttRun(t, tool, jsonx.ObjectOf("path", "file.txt", "content", "first\n"), local)
				close(done)
			}()
			<-local.firstWriteStarted
			ttRun(t, tool, jsonx.ObjectOf("path", "file.txt", "content", "second\n"), other)
			if !other.secondWriteStarted() {
				t.Fatalf("expected write on different filesystem to run immediately")
			}
			close(local.finishFirstWrite)
			<-done
		})

		t.Run("edits regular files through symlinks", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			ttMustWriteText(t, executionEnv, "target.txt", "before\n")
			if err := os.Symlink("target.txt", filepath.Join(executionEnv.Cwd(), "link.txt")); err != nil {
				t.Fatalf("symlink failed: %v", err)
			}
			ttRun(t, tools.CreateEditTool(), jsonx.ObjectOf("path", "link.txt", "edits", []any{jsonx.ObjectOf("oldText", "before", "newText", "after")}), executionEnv)
			if got := ttMustReadText(t, executionEnv, "target.txt"); got != "after\n" {
				t.Fatalf("expected 'after\\n', got %q", got)
			}
		})

		t.Run("preserves BOM and CRLF line endings", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			ttMustWriteText(t, executionEnv, "edit.txt", "\uFEFFone\r\ntwo\r\n")
			ttRun(t, tools.CreateEditTool(), jsonx.ObjectOf("path", "edit.txt", "edits", []any{jsonx.ObjectOf("oldText", "two", "newText", "TWO")}), executionEnv)
			if got := ttMustReadText(t, executionEnv, "edit.txt"); got != "\uFEFFone\r\nTWO\r\n" {
				t.Fatalf("expected preserved BOM+CRLF, got %q", got)
			}
		})
	})

	t.Run("bash", func(t *testing.T) {
		fullOutputRe := regexp.MustCompile(`^Full output: (.+)$`)

		t.Run("streams combined stdout and stderr and returns no content of its own", func(t *testing.T) {
			result := ttRun(t, tools.CreateBashTool(), jsonx.ObjectOf("command", "printf out; printf err >&2"), ttNewEnv(t))
			joined := strings.Join(result.output, "")
			if !strings.Contains(joined, "out") || !strings.Contains(joined, "err") {
				t.Fatalf("expected streamed out and err, got %q", joined)
			}
			if result.Content != nil {
				t.Fatalf("expected nil Content, got %#v", result.Content)
			}
		})

		t.Run("throws on nonzero exits and timeouts after streaming the output", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			tool := tools.CreateBashTool()
			failed := ttRunFailing(t, tool, jsonx.ObjectOf("command", "printf failed; exit 7"), executionEnv)
			if failed.err.Error() != "Command exited with code 7" {
				t.Fatalf("expected exit 7 error, got %v", failed.err)
			}
			if strings.Join(failed.output, "") != "failed" {
				t.Fatalf("expected streamed 'failed', got %q", strings.Join(failed.output, ""))
			}
			slow := ttRunFailing(t, tool, jsonx.ObjectOf("command", "sleep 2", "timeout", 0.01), executionEnv)
			if slow.err.Error() != "Command timed out after 0.01 seconds" {
				t.Fatalf("expected 0.01s timeout error, got %v", slow.err)
			}
		})

		t.Run("reports the spill of a command that times out", func(t *testing.T) {
			executionEnv := &ttTimeoutOutputEnv{NodeExecutionEnv: ttNewEnv(t)}
			failed := ttRunFailing(t, tools.CreateBashTool(), jsonx.ObjectOf("command", "emit-output-then-time-out", "timeout", 0.05), executionEnv)
			if failed.err.Error() != "Command timed out after 0.05 seconds" {
				t.Fatalf("expected timeout error, got %v", failed.err)
			}
			if len(failed.reported) == 0 {
				t.Fatalf("expected spill diagnostic")
			}
			matches := fullOutputRe.FindStringSubmatch(failed.reported[0].Message)
			if len(matches) != 2 {
				t.Fatalf("expected 'Full output: ...' diagnostic, got %q", failed.reported[0].Message)
			}
			fullOutput := ttMustReadText(t, executionEnv, matches[1])
			if !strings.Contains(fullOutput, "line-1\nline-2") {
				t.Fatalf("missing head lines in spill output")
			}
			wantTail := fmt.Sprintf("line-%d\nline-%d", durable.DefaultMaxLines, ttTruncatedOutputLines)
			if !strings.Contains(fullOutput, wantTail) {
				t.Fatalf("missing tail lines %q in spill output", wantTail)
			}
		})

		t.Run("prepares command, cwd, and an explicit environment with the call's api", func(t *testing.T) {
			executionEnv := ttNewEnv(t, env.NodeExecutionEnvOptions{
				ShellEnv: map[string]string{"PI_BASH_PREPARE_INHERITED": "inherited"},
			})
			if _, err := env.GetOrThrow(executionEnv.CreateDir("workspace", nil, chord.BackgroundContext)); err != nil {
				t.Fatalf("CreateDir failed: %v", err)
			}
			workspace := filepath.Join(executionEnv.Cwd(), "workspace")
			sig, cancel := context.WithCancel(context.Background())
			defer cancel()

			var receivedEnv env.ExecutionEnv
			var receivedSignal context.Context
			tool := tools.CreateBashTool(&tools.BashToolOptions{
				CommandPrefix: "prefix=ready",
				Prepare: func(execution *tools.BashExecution, api durable.ToolExecutionApi, callCtx chord.Context) error {
					var err error
					receivedEnv, err = api.Env(callCtx)
					if err != nil {
						return err
					}
					receivedSignal = callCtx.AbortSignal()
					execution.Cwd = workspace
					execution.Env = map[string]string{"PI_BASH_PREPARE_EXPLICIT": "explicit"}
					execution.InheritEnv = false
					execution.Command += "\n" + `printf '%s:%s:%s:%s' "$prefix" "${PI_BASH_PREPARE_INHERITED-}" "$PI_BASH_PREPARE_EXPLICIT" "$PWD"`
					return nil
				},
			})

			result := ttRun(t, tool, jsonx.ObjectOf("command", ":"), executionEnv, chord.WithAbortSignal(sig, chord.BackgroundContext))
			if receivedEnv != executionEnv {
				t.Fatalf("expected receivedEnv == executionEnv")
			}
			if receivedSignal != sig {
				t.Fatalf("expected receivedSignal == sig")
			}
			canonWorkspace, err := env.GetOrThrow(executionEnv.CanonicalPath(workspace, chord.BackgroundContext))
			if err != nil {
				t.Fatalf("CanonicalPath failed: %v", err)
			}
			want := "ready::explicit:" + canonWorkspace
			if got := strings.Join(result.output, ""); got != want {
				t.Fatalf("expected %q, got %q", want, got)
			}
		})

		t.Run("supports command prefixes", func(t *testing.T) {
			result := ttRun(t, tools.CreateBashTool(&tools.BashToolOptions{CommandPrefix: "value=hello"}), jsonx.ObjectOf("command", "printf $value"), ttNewEnv(t))
			if got := strings.Join(result.output, ""); got != "hello" {
				t.Fatalf("expected 'hello', got %q", got)
			}
		})

		t.Run("streams every byte and spills complete output beyond the default limits", func(t *testing.T) {
			executionEnv := ttNewEnv(t)
			result := ttRun(t, tools.CreateBashTool(), jsonx.ObjectOf("command", "i=1; while [ $i -le 3000 ]; do echo line-$i; i=$((i + 1)); done"), executionEnv)
			lines := make([]string, 3000)
			for i := range lines {
				lines[i] = fmt.Sprintf("line-%d", i+1)
			}
			expected := strings.Join(lines, "\n") + "\n"
			if got := strings.Join(result.output, ""); got != expected {
				t.Fatalf("streamed output mismatch (len %d vs %d)", len(got), len(expected))
			}
			if len(result.reported) == 0 {
				t.Fatalf("expected Full output diagnostic")
			}
			matches := fullOutputRe.FindStringSubmatch(result.reported[0].Message)
			if len(matches) != 2 {
				t.Fatalf("unexpected diagnostic %q", result.reported[0].Message)
			}
			if got := ttMustReadText(t, executionEnv, matches[1]); got != expected {
				t.Fatalf("spill file mismatch")
			}
		})

		t.Run("does not spill output within the limits", func(t *testing.T) {
			result := ttRun(t, tools.CreateBashTool(), jsonx.ObjectOf("command", "printf small"), ttNewEnv(t))
			if len(result.reported) != 0 {
				t.Fatalf("expected no diagnostics, got %+v", result.reported)
			}
		})
	})
}
