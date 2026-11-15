// Ported from packages/codemode/test/sandbox.test.ts (pi v1.0.0).

package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codemode"
	codemoderuntime "github.com/keejkrej/pi-go/codemode/runtime"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	tsandPNG  = "iVBORw0KGgo="
	tsandJPEG = "/9j/4A=="
	tsandGIF  = "R0lGODlh"
	tsandWEBP = "UklGRgAAAABXRUJQ"
)

func tsandFloatPtr(v float64) *float64 { return &v }
func tsandIntPtr(v int) *int           { return &v }
func tsandStrPtr(v string) *string     { return &v }
func tsandBoolPtr(v bool) *bool        { return &v }

func tsandEchoTool() *codemode.CodemodeTool {
	return &codemode.CodemodeTool{
		Name: "echo",
		Execute: func(_ context.Context, args any) (any, error) {
			return args, nil
		},
	}
}

func tsandNewSandbox(t *testing.T, tools []*codemode.CodemodeTool, timeoutMs float64) *codemoderuntime.CodemodeSandbox {
	t.Helper()
	sb, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
		Tools:     tools,
		TimeoutMs: tsandFloatPtr(timeoutMs),
	})
	if err != nil {
		t.Fatalf("NewCodemodeSandbox: %v", err)
	}
	t.Cleanup(func() {
		_ = sb.Close()
	})
	return sb
}

func tsandMustSuccess(t *testing.T, res codemode.CodemodeResult, err error) *codemode.CodemodeSuccess {
	t.Helper()
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	succ, ok := res.(*codemode.CodemodeSuccess)
	if !ok {
		fail := res.(*codemode.CodemodeFailure)
		t.Fatalf("expected *CodemodeSuccess, got *CodemodeFailure: %+v", fail.Error)
	}
	return succ
}

func tsandMustFailure(t *testing.T, res codemode.CodemodeResult, err error) *codemode.CodemodeFailure {
	t.Helper()
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	fail, ok := res.(*codemode.CodemodeFailure)
	if !ok {
		t.Fatalf("expected *CodemodeFailure, got %T", res)
	}
	return fail
}

func tsandExecSuccess(t *testing.T, sb *codemoderuntime.CodemodeSandbox, ctx context.Context, code string, opts *codemode.CodemodeExecuteOptions) *codemode.CodemodeSuccess {
	t.Helper()
	res, err := sb.Execute(ctx, code, opts)
	return tsandMustSuccess(t, res, err)
}

func tsandExecFailure(t *testing.T, sb *codemoderuntime.CodemodeSandbox, ctx context.Context, code string, opts *codemode.CodemodeExecuteOptions) *codemode.CodemodeFailure {
	t.Helper()
	res, err := sb.Execute(ctx, code, opts)
	return tsandMustFailure(t, res, err)
}

func tsandToJSONString(t *testing.T, v any) string {
	t.Helper()
	s, err := jsonx.Stringify(v)
	if err != nil {
		t.Fatalf("jsonx.Stringify: %v", err)
	}
	return s
}

func TestEmbeddedSources(t *testing.T) {
	sb := tsandNewSandbox(t, nil, 10_000)
	if codemoderuntime.PreludeSource == "" {
		t.Fatal("PreludeSource should not be empty")
	}
	succ := tsandExecSuccess(t, sb, context.Background(), "return 1", nil)
	val, _ := succ.Value.Get()
	if val != float64(1) {
		t.Fatalf("got %v, want 1", val)
	}
}

func TestScriptExecution(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the script's return value after a JSON round trip", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s1 := tsandExecSuccess(t, sb, ctx, "return { a: 1, b: [true, 'x'] }", nil)
		val1, _ := s1.Value.Get()
		if got := tsandToJSONString(t, val1); got != `{"a":1,"b":[true,"x"]}` {
			t.Fatalf("got %s", got)
		}
		if len(s1.Output) != 0 || len(s1.Calls) != 0 {
			t.Fatalf("expected empty output/calls, got %+v / %+v", s1.Output, s1.Calls)
		}

		s2 := tsandExecSuccess(t, sb, ctx, "return 'plain'", nil)
		if val2, _ := s2.Value.Get(); val2 != "plain" {
			t.Fatalf("got %v", val2)
		}

		s3 := tsandExecSuccess(t, sb, ctx, "", nil)
		if !s3.Value.IsZero() {
			t.Fatalf("expected absent Value for empty script, got %+v", s3.Value)
		}
	})

	t.Run("supports top-level await", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, "const x = await Promise.resolve(41); return x + 1", nil)
		if val, _ := s.Value.Get(); val != float64(42) {
			t.Fatalf("got %v", val)
		}
	})

	t.Run("collects text(), image(), and console output in order", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		code := fmt.Sprintf(`
			console.log("hello", 1, { a: 1 });
			text({ json: true });
			text(undefined);
			text(7);
			image("data:image/png;base64,%s");
			image({ image_url: "data:image/jpeg;base64,%s" });
			image({ type: "image", data: "%s", mimeType: "image/gif" });
			image("data:image/png;base64,%s");
			image({ type: "image", data: "%s" });
			console.error(new Error("bad"));
			return null;
		`, tsandPNG, tsandJPEG, tsandGIF, tsandWEBP, tsandPNG)
		s := tsandExecSuccess(t, sb, ctx, code, nil)
		if !s.Value.IsNull() {
			t.Fatalf("expected null Value, got %+v", s.Value)
		}
		if len(s.Output) != 10 {
			t.Fatalf("len(output) = %d, want 10", len(s.Output))
		}
		headJSON := tsandToJSONString(t, s.Output[:9])
		wantHead := fmt.Sprintf(
			`[{"type":"text","text":"hello 1 {\"a\":1}"},{"type":"text","text":"{\"json\":true}"},{"type":"text","text":"undefined"},{"type":"text","text":"7"},{"type":"image","data":%q,"mimeType":"image/png"},{"type":"image","data":%q,"mimeType":"image/jpeg"},{"type":"image","data":%q,"mimeType":"image/gif"},{"type":"image","data":%q,"mimeType":"image/webp"},{"type":"image","data":%q,"mimeType":"image/png"}]`,
			tsandPNG, tsandJPEG, tsandGIF, tsandWEBP, tsandPNG,
		)
		if headJSON != wantHead {
			t.Fatalf("output[:9] = %s\nwant: %s", headJSON, wantHead)
		}
		lastTxt, ok := s.Output[9].(*codemode.CodemodeTextOutput)
		if !ok || !strings.HasPrefix(lastTxt.Text, "Error: bad") {
			t.Fatalf("last output = %#v", s.Output[9])
		}
	})

	t.Run("rejects invalid text() and image() arguments", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			const errors = [];
			const circular = {};
			circular.self = circular;
			for (const run of [
				() => text(circular),
				() => image(""),
				() => image("https://example.com/a.png"),
				() => image("data:image/png,raw"),
				() => image({ type: "text", text: "x" }),
				() => image({ type: "image", data: "" }),
				() => image(42),
				() => image("data:image/png;base64,AAAA!"),
				() => image("data:image/png;base64,AAAAA"),
				() => image("data:image/png;base64,AA=A"),
				() => image("data:image/png;base64,"),
				() => image("data:image/png;base64,AAAA\n[Output truncated]"),
				() => image({ type: "image", data: "AAAA!", mimeType: "image/png" }),
				() => image("data:image/png;base64,AAAA"),
				() => image("data:image/png;base64,QUJD"),
				() => image("data:image/jpeg;base64,/9j/9w=="),
			]) {
				try { run(); errors.push("no error"); } catch (error) { errors.push(error.name + ": " + error.message); }
			}
			return errors;
		`, nil)
		if len(s.Output) != 0 {
			t.Fatalf("expected empty output, got %+v", s.Output)
		}
		val, _ := s.Value.Get()
		errs := val.([]any)
		if !regexp.MustCompile(`^TypeError: .*circular`).MatchString(errs[0].(string)) {
			t.Fatalf("errs[0] = %q", errs[0])
		}
		wantRest := []string{
			"TypeError: image expects a non-empty image URL string, an object with image_url, or a raw MCP image block",
			"TypeError: remote image URLs are not supported in tool outputs. Pass a base64 data URI instead",
			"TypeError: invalid image output. Pass a base64 data URI instead",
			`TypeError: image only accepts MCP image blocks, got "text"`,
			"TypeError: image expected MCP image data",
			"TypeError: image expects a non-empty image URL string, an object with image_url, or a raw MCP image block",
		}
		for range 6 {
			wantRest = append(wantRest, "TypeError: invalid image output. The image data is not valid base64 (truncated or corrupted?)")
		}
		for range 3 {
			wantRest = append(wantRest, "TypeError: invalid image output. The image data is not a PNG, JPEG, GIF, or WebP image")
		}
		if len(errs)-1 != len(wantRest) {
			t.Fatalf("len(errs[1:]) = %d, want %d", len(errs)-1, len(wantRest))
		}
		for i, want := range wantRest {
			if errs[i+1] != want {
				t.Fatalf("errs[%d] = %q, want %q", i+1, errs[i+1], want)
			}
		}
	})

	t.Run("accepts wrapped and large base64 image data", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		large := "iVBORw0KGgoA" + strings.Repeat("QUJD", 256*1024)
		code := fmt.Sprintf(`
			image("data:image/png;base64,iVBORw0K\r\nGgo=\n");
			image("data:image/png;base64,%s");
		`, large)
		s := tsandExecSuccess(t, sb, ctx, code, nil)
		if len(s.Output) != 2 {
			t.Fatalf("len(output) = %d", len(s.Output))
		}
		i0 := s.Output[0].(*codemode.CodemodeImageOutput)
		i1 := s.Output[1].(*codemode.CodemodeImageOutput)
		if i0.Data != tsandPNG || i0.MimeType != "image/png" || i1.Data != large || i1.MimeType != "image/png" {
			t.Fatalf("unexpected images: %+v / %+v", i0, i1)
		}
	})

	t.Run("ends the script successfully on exit(), keeping output and store writes", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{tsandEchoTool()}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			text("before");
			store("k", 1);
			await tools.echo(1);
			try { exit(); } catch {}
			text("after");
			return "unreachable";
		`, nil)
		if !s.Value.IsZero() {
			t.Fatalf("expected absent Value, got %+v", s.Value)
		}
		if len(s.Output) != 1 || s.Output[0].(*codemode.CodemodeTextOutput).Text != "before" {
			t.Fatalf("output = %+v", s.Output)
		}
		if v, ok := s.StoreWrites.Set.Get("k"); !ok || v != float64(1) || len(s.StoreWrites.Delete) != 0 {
			t.Fatalf("storeWrites = %+v", s.StoreWrites)
		}
	})

	t.Run("keeps output produced before a failure", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "text(\"partial\");\nthrow new Error(\"boom\")", nil)
		if len(f.Output) != 1 || f.Output[0].(*codemode.CodemodeTextOutput).Text != "partial" {
			t.Fatalf("output = %+v", f.Output)
		}
	})

	t.Run("reports syntax errors with the script's line number", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "const a = 1;\nconst b = ;\nreturn a", nil)
		if f.Error.Kind != codemode.CodemodeErrorKindScript {
			t.Fatalf("kind = %q", f.Error.Kind)
		}
		if f.Error.Name == nil || *f.Error.Name != "SyntaxError" {
			t.Fatalf("name = %v", f.Error.Name)
		}
		if f.Error.Stack == nil || !strings.Contains(*f.Error.Stack, "codemode.js:2") {
			t.Fatalf("stack = %v", f.Error.Stack)
		}
	})

	t.Run("reports thrown errors with the script's line number", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "const a = 1;\nthrow new TypeError('boom ' + a)", nil)
		if f.Error.Kind != codemode.CodemodeErrorKindScript || f.Error.Name == nil || *f.Error.Name != "TypeError" || f.Error.Message != "boom 1" {
			t.Fatalf("error = %+v", f.Error)
		}
		if f.Error.Stack == nil || !strings.Contains(*f.Error.Stack, "codemode.js:2") {
			t.Fatalf("stack = %v", f.Error.Stack)
		}
	})

	t.Run("formats stacks like V8 without prelude frames", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "console.log(new Error('inner'));\nthrow new RangeError('outer')", nil)
		if f.Error.Stack == nil || !regexp.MustCompile(`^RangeError: outer\n {4}at .*codemode\.js:2`).MatchString(*f.Error.Stack) {
			t.Fatalf("stack = %q", *f.Error.Stack)
		}
		if strings.Contains(*f.Error.Stack, "codemode-prelude.js") {
			t.Fatalf("stack leaked prelude frame: %q", *f.Error.Stack)
		}
		txt := f.Output[0].(*codemode.CodemodeTextOutput).Text
		if !regexp.MustCompile(`^Error: inner\n {4}at .*codemode\.js:1`).MatchString(txt) {
			t.Fatalf("output[0] = %q", txt)
		}
		if strings.Contains(tsandToJSONString(t, f.Output), "codemode-prelude.js") {
			t.Fatalf("output leaked prelude frame")
		}
	})

	t.Run("reports non-Error throws", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "throw { code: 7 }", nil)
		if f.Error.Kind != codemode.CodemodeErrorKindScript || f.Error.Message != `{"code":7}` {
			t.Fatalf("error = %+v", f.Error)
		}
	})

	t.Run("reports a non-serializable return value as a script error", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "return 10n", nil)
		if f.Error.Kind != codemode.CodemodeErrorKindScript || f.Error.Name == nil || *f.Error.Name != "TypeError" {
			t.Fatalf("error = %+v", f.Error)
		}
	})
}

func TestTools(t *testing.T) {
	ctx := context.Background()

	t.Run("exposes tools as async functions and records calls", func(t *testing.T) {
		var seen []any
		var mu sync.Mutex
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			{
				Name: "add",
				Execute: func(_ context.Context, args any) (any, error) {
					mu.Lock()
					seen = append(seen, args)
					mu.Unlock()
					obj := args.(*jsonx.Object)
					a, _ := obj.Get("a")
					b, _ := obj.Get("b")
					return map[string]any{"sum": a.(float64) + b.(float64)}, nil
				},
			},
		}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			const first = await tools.add({ a: 1, b: 2 });
			const second = await tools.add({ a: first.sum, b: 10 });
			return second.sum;
		`, nil)
		if val, _ := s.Value.Get(); val != float64(13) {
			t.Fatalf("got %v", val)
		}
		if tsandToJSONString(t, seen) != `[{"a":1,"b":2},{"a":3,"b":10}]` {
			t.Fatalf("seen = %s", tsandToJSONString(t, seen))
		}
		if len(s.Calls) != 2 || s.Calls[0].Name != "add" || s.Calls[0].Status != codemode.CodemodeCallStatusOk || s.Calls[1].Name != "add" || s.Calls[1].Status != codemode.CodemodeCallStatusOk {
			t.Fatalf("calls = %+v", s.Calls)
		}
		for _, c := range s.Calls {
			if c.DurationMs < 0 {
				t.Fatalf("negative duration: %+v", c)
			}
		}
	})

	t.Run("runs concurrent calls and lists tool names", func(t *testing.T) {
		var inFlight atomic.Int32
		var maxInFlight atomic.Int32
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			tsandEchoTool(),
			{
				Name: "delay",
				Execute: func(ctx context.Context, args any) (any, error) {
					cur := inFlight.Add(1)
					for {
						prev := maxInFlight.Load()
						if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
							break
						}
					}
					defer inFlight.Add(-1)
					select {
					case <-time.After(25 * time.Millisecond):
						return args, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
			},
		}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			const [a, b, c] = await Promise.all([tools.delay(1), tools.delay(2), tools.echo(3)]);
			return { values: [a, b, c], names: Object.keys(tools) };
		`, nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `{"values":[1,2,3],"names":["echo","delay"]}` {
			t.Fatalf("got %s", got)
		}
		if maxInFlight.Load() < 2 {
			t.Fatalf("expected concurrent Promise.all execution, maxInFlight=%d", maxInFlight.Load())
		}
	})

	t.Run("exposes tools under normalized identifiers and lists them in ALL_TOOLS", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			{Name: "my-tool", Description: tsandStrPtr("Dashes"), Execute: func(_ context.Context, _ any) (any, error) { return "dash", nil }},
			{Name: "my_tool", Description: tsandStrPtr("Shadowed"), Execute: func(_ context.Context, _ any) (any, error) { return "underscore", nil }},
			{Name: "mcp__docs__search", Execute: func(_ context.Context, _ any) (any, error) { return "mcp", nil }},
		}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			try { ALL_TOOLS.push({}); } catch {}
			return {
				all: ALL_TOOLS,
				calls: [await tools.my_tool(), await tools["my-tool"](), await tools.mcp__docs__search()],
			};
		`, nil)
		val, _ := s.Value.Get()
		want := `{"all":[{"name":"my_tool","description":"Dashes"},{"name":"mcp__docs__search","description":""}],"calls":["dash","dash","mcp"]}`
		if got := tsandToJSONString(t, val); got != want {
			t.Fatalf("got %s\nwant %s", got, want)
		}
	})

	t.Run("passes undefined arguments and results through", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			{Name: "noop", Execute: func(_ context.Context, args any) (any, error) { return args, nil }},
		}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, "return [await tools.noop(), await tools.noop(null)]", nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != "[null,null]" {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("turns tool errors into catchable Errors in the script", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			{Name: "fail", Execute: func(_ context.Context, _ any) (any, error) { return nil, errors.New("tool exploded") }},
		}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			try {
				await tools.fail();
				return "no error";
			} catch (error) {
				return { isError: error instanceof Error, message: error.message };
			}
		`, nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `{"isError":true,"message":"tool exploded"}` {
			t.Fatalf("got %s", got)
		}
		if len(s.Calls) != 1 || s.Calls[0].Name != "fail" || s.Calls[0].Status != codemode.CodemodeCallStatusError {
			t.Fatalf("calls = %+v", s.Calls)
		}
	})

	t.Run("rejects calls to unknown tools", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "return await tools.missing()", nil)
		if f.Error.Kind != codemode.CodemodeErrorKindScript || f.Error.Name == nil || *f.Error.Name != "TypeError" {
			t.Fatalf("error = %+v", f.Error)
		}
	})

	t.Run("aborts unawaited calls when the script returns", func(t *testing.T) {
		aborted := make(chan struct{}, 1)
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			{
				Name: "slow",
				Execute: func(ctx context.Context, _ any) (any, error) {
					<-ctx.Done()
					select {
					case aborted <- struct{}{}:
					default:
					}
					return nil, ctx.Err()
				},
			},
		}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, "tools.slow(); return 'early'", nil)
		if val, _ := s.Value.Get(); val != "early" {
			t.Fatalf("val = %v", val)
		}
		if len(s.Calls) != 1 || s.Calls[0].Name != "slow" || s.Calls[0].Status != codemode.CodemodeCallStatusCancelled {
			t.Fatalf("calls = %+v", s.Calls)
		}
		select {
		case <-aborted:
		case <-time.After(time.Second):
			t.Fatal("expected unawaited tool call ctx to be cancelled")
		}
	})

	t.Run("names close matches when a script reads a tool that does not exist", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			tsandEchoTool(),
			{Name: "web-search", Execute: func(_ context.Context, _ any) (any, error) { return "", nil }},
		}, 10_000)
		attempt := func(expr string) any {
			res, err := sb.Execute(ctx, "return "+expr+";", nil)
			if err != nil {
				t.Fatal(err)
			}
			if s, ok := res.(*codemode.CodemodeSuccess); ok {
				v, _ := s.Value.Get()
				return v
			}
			return res.(*codemode.CodemodeFailure).Error.Message
		}
		if got := attempt("tools.Echo"); got != `tools.Echo does not exist. Did you mean tools.echo? ALL_TOOLS lists every tool; searchTools(query) finds tools by topic. Check for a member with "Echo" in tools.` {
			t.Fatalf("got %q", got)
		}
		if got := attempt("tools.websearch").(string); !strings.Contains(got, "Did you mean tools.web_search?") {
			t.Fatalf("got %q", got)
		}
		if got := attempt("tools.nothing").(string); !strings.Contains(got, "Available: echo, web_search.") {
			t.Fatalf("got %q", got)
		}
		if got := tsandToJSONString(t, attempt("['echo' in tools, 'nothing' in tools, String(tools.toString), JSON.stringify(tools)]")); got != `[true,false,"undefined","{}"]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("supports register and unregister between executions", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		if err := sb.RegisterTool(tsandEchoTool()); err != nil {
			t.Fatal(err)
		}
		if err := sb.RegisterTool(tsandEchoTool()); err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("expected duplicate error, got %v", err)
		}
		tools := sb.Tools()
		if len(tools) != 1 || tools[0].Name != "echo" {
			t.Fatalf("tools = %+v", tools)
		}
		s1 := tsandExecSuccess(t, sb, ctx, "return await tools.echo('a')", nil)
		if val, _ := s1.Value.Get(); val != "a" {
			t.Fatalf("val = %v", val)
		}
		if !sb.UnregisterTool("echo") {
			t.Fatal("expected UnregisterTool to return true")
		}
		if sb.UnregisterTool("echo") {
			t.Fatal("expected second UnregisterTool to return false")
		}
		s2 := tsandExecSuccess(t, sb, ctx, "return 'echo' in tools", nil)
		if val, _ := s2.Value.Get(); val != false {
			t.Fatalf("val = %v", val)
		}
	})
}

func TestStoreAndLoad(t *testing.T) {
	ctx := context.Background()

	t.Run("reads the snapshot and reports writes", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			const seen = load("counter");
			store("counter", seen + 1);
			store("list", [1, { a: null }]);
			store("old", undefined);
			return [seen, load("counter"), load("missing"), load("old")];
		`, &codemode.CodemodeExecuteOptions{
			Store: map[string]any{"counter": 41, "old": "x"},
		})
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != "[41,42,null,null]" {
			t.Fatalf("val = %s", got)
		}
		if got := tsandToJSONString(t, s.StoreWrites); got != `{"set":{"counter":42,"list":[1,{"a":null}]},"delete":["old"]}` {
			t.Fatalf("storeWrites = %s", got)
		}
	})

	t.Run("returns copies, so mutating a loaded value does not change the store", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			const value = load("obj"); value.a = 2; const kept = { b: 1 }; store("kept", kept); kept.b = 2;
			return [load("obj").a, load("kept").b];
		`, &codemode.CodemodeExecuteOptions{
			Store: map[string]any{"obj": map[string]any{"a": 1}},
		})
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != "[1,1]" {
			t.Fatalf("val = %s", got)
		}
		if got := tsandToJSONString(t, s.StoreWrites.Set); got != `{"kept":{"b":1}}` {
			t.Fatalf("set = %s", got)
		}
	})

	t.Run("rejects invalid keys, values, and oversized writes inside the script", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			const attempt = (fn) => { try { fn(); return "ok"; } catch (error) { return error.name; } };
			return [
				attempt(() => store(1, "x")),
				attempt(() => load({})),
				attempt(() => store("fn", () => 1)),
				attempt(() => store("big", "x".repeat(300 * 1024))),
				attempt(() => { for (let i = 0; i < 8; i++) store("k" + i, "x".repeat(200 * 1024)); }),
			];
		`, nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `["TypeError","TypeError","TypeError","RangeError","RangeError"]` {
			t.Fatalf("val = %s", got)
		}
	})

	t.Run("explains oversized writes", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, `store("img", "x".repeat(300 * 1024));`, nil)
		if !strings.Contains(f.Error.Message, `store("img") value has 307202 characters of JSON`) || !strings.Contains(f.Error.Message, "Show images with image()") {
			t.Fatalf("message = %q", f.Error.Message)
		}
	})

	t.Run("reserves the store and load names", func(t *testing.T) {
		noop := func(_ context.Context, _ any) (any, error) { return nil, nil }
		for _, name := range []string{"store", "load"} {
			if _, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
				Globals: []*codemode.CodemodeTool{{Name: name, Execute: noop}},
			}); err == nil || !strings.Contains(err.Error(), "Invalid global") {
				t.Fatalf("name %q: expected Invalid global error, got %v", name, err)
			}
		}
	})
}

func TestGlobals(t *testing.T) {
	ctx := context.Background()
	noop := func(_ context.Context, _ any) (any, error) { return nil, nil }

	t.Run("exposes globals as top-level functions without recording them as calls", func(t *testing.T) {
		var mu sync.Mutex
		var seen []any
		var wg sync.WaitGroup
		wg.Add(2)
		sb, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			Tools: []*codemode.CodemodeTool{tsandEchoTool()},
			Globals: []*codemode.CodemodeTool{
				{
					Name: "attach",
					Execute: func(_ context.Context, args any) (any, error) {
						mu.Lock()
						seen = append(seen, args)
						mu.Unlock()
						wg.Done()
						return nil, nil
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sb.Close()

		s := tsandExecSuccess(t, sb, ctx, `
			await attach({ ref: 1 });
			attach("not awaited");
			return [typeof attach, typeof globalThis.attach, await tools.echo(2)];
		`, nil)
		wg.Wait()
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `["function","function",2]` {
			t.Fatalf("val = %s", got)
		}
		if len(s.Calls) != 1 || s.Calls[0].Name != "echo" {
			t.Fatalf("calls = %+v", s.Calls)
		}
		mu.Lock()
		seenJSON := tsandToJSONString(t, seen)
		mu.Unlock()
		if seenJSON != `[{"ref":1},"not awaited"]` {
			t.Fatalf("seen = %s", seenJSON)
		}
	})

	t.Run("groups namespaced globals and spreads arguments on request", func(t *testing.T) {
		var mu sync.Mutex
		var seen []any
		sb, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			Globals: []*codemode.CodemodeTool{
				{
					Name:   "models.list",
					Spread: tsandBoolPtr(true),
					Execute: func(_ context.Context, args any) (any, error) {
						mu.Lock()
						seen = append(seen, args)
						mu.Unlock()
						return nil, nil
					},
				},
				{
					Name: "models.first",
					Execute: func(_ context.Context, args any) (any, error) {
						return args, nil
					},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sb.Close()
		if len(sb.Globals()) != 2 {
			t.Fatalf("Globals() len = %d", len(sb.Globals()))
		}

		s := tsandExecSuccess(t, sb, ctx, `
			await models.list("classifier", undefined, 3);
			await models.list();
			try { models.extra = 1; } catch {}
			return [Object.keys(models), await models.first("a", "ignored"), "extra" in models];
		`, nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `[["list","first"],"a",false]` {
			t.Fatalf("val = %s", got)
		}
		mu.Lock()
		seenJSON := tsandToJSONString(t, seen)
		mu.Unlock()
		if seenJSON != `[["classifier",null,3],[]]` {
			t.Fatalf("seen = %s", seenJSON)
		}
	})

	t.Run("names the members of a namespace when a script reads one that does not exist", func(t *testing.T) {
		sb, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			Globals: []*codemode.CodemodeTool{
				{Name: "models.classify", Execute: noop},
				{Name: "models.generateImages", Execute: noop},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sb.Close()

		f := tsandExecFailure(t, sb, ctx, "await models.generateImage();", nil)
		want := `models.generateImage does not exist. Did you mean models.generateImages? Check for a member with "generateImage" in models.`
		if f.Error.Message != want {
			t.Fatalf("message = %q, want %q", f.Error.Message, want)
		}
	})

	t.Run("rejects invalid and reserved global names", func(t *testing.T) {
		for _, name := range []string{"a.b.c", "a.", ".a", "tools.x", "store.x", "a.not-valid", "not-valid", "tools", "console"} {
			if _, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
				Globals: []*codemode.CodemodeTool{{Name: name, Execute: noop}},
			}); err == nil || !strings.Contains(err.Error(), "Invalid global") {
				t.Fatalf("name %q: expected Invalid global error, got %v", name, err)
			}
		}
		if _, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			Globals: []*codemode.CodemodeTool{
				{Name: "models", Execute: noop},
				{Name: "models.list", Execute: noop},
			},
		}); err == nil || !strings.Contains(err.Error(), "conflicts with the namespace") {
			t.Fatalf("expected namespace conflict error, got %v", err)
		}
		if _, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			Globals: []*codemode.CodemodeTool{
				{Name: "models.list", Execute: noop},
				{Name: "models.list", Execute: noop},
			},
		}); err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("expected duplicate global error, got %v", err)
		}
	})
}

func TestLimitsAndLifetime(t *testing.T) {
	ctx := context.Background()

	t.Run("terminates a synchronous infinite loop on timeout", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		started := time.Now()
		f := tsandExecFailure(t, sb, ctx, "while (true) {}", &codemode.CodemodeExecuteOptions{TimeoutMs: tsandFloatPtr(200)})
		if f.Error.Kind != codemode.CodemodeErrorKindTimeout {
			t.Fatalf("kind = %q, message = %q", f.Error.Kind, f.Error.Message)
		}
		if time.Since(started) > 5*time.Second {
			t.Fatalf("took too long: %v", time.Since(started))
		}
	})

	t.Run("runs without a deadline when timeoutMs is Infinity", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			{
				Name: "wait",
				Execute: func(_ context.Context, _ any) (any, error) {
					time.Sleep(50 * time.Millisecond)
					return "late", nil
				},
			},
		}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, "return await tools.wait()", &codemode.CodemodeExecuteOptions{TimeoutMs: tsandFloatPtr(math.Inf(1))})
		if val, _ := s.Value.Get(); val != "late" {
			t.Fatalf("val = %v", val)
		}
	})

	t.Run("fails a script that waits on a promise nothing can settle", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{tsandEchoTool()}, 10_000)
		f := tsandExecFailure(t, sb, ctx, "await tools.echo(1); await new Promise(() => {}); return 'never'", &codemode.CodemodeExecuteOptions{
			TimeoutMs: tsandFloatPtr(math.Inf(1)),
		})
		if f.Error.Kind != codemode.CodemodeErrorKindScript || !strings.Contains(f.Error.Message, "can never settle") {
			t.Fatalf("error = %+v", f.Error)
		}
		s := tsandExecSuccess(t, sb, ctx, "tools.echo(2); return 'early'", &codemode.CodemodeExecuteOptions{
			TimeoutMs: tsandFloatPtr(math.Inf(1)),
		})
		if val, _ := s.Value.Get(); val != "early" {
			t.Fatalf("val = %v", val)
		}
	})

	t.Run("terminates a microtask-spinning loop on timeout", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		f := tsandExecFailure(t, sb, ctx, "while (true) await null", &codemode.CodemodeExecuteOptions{TimeoutMs: tsandFloatPtr(200)})
		if f.Error.Kind != codemode.CodemodeErrorKindTimeout {
			t.Fatalf("error = %+v", f.Error)
		}
	})

	t.Run("aborts via signal and cancels in-flight calls", func(t *testing.T) {
		var toolCtxMu sync.Mutex
		var lastToolCtx context.Context
		calledCh := make(chan struct{}, 4)

		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{
			{
				Name: "hang",
				Execute: func(tctx context.Context, _ any) (any, error) {
					toolCtxMu.Lock()
					lastToolCtx = tctx
					toolCtxMu.Unlock()
					calledCh <- struct{}{}
					<-tctx.Done()
					return nil, tctx.Err()
				},
			},
		}, 10_000)

		var wg sync.WaitGroup
		wg.Add(1)
		var closeRes codemode.CodemodeResult
		var closeErr error
		go func() {
			defer wg.Done()
			closeRes, closeErr = sb.Execute(ctx, "await tools.hang(); return 'never'", nil)
		}()
		<-calledCh

		cancelCtx1, cancelCause1 := context.WithCancelCause(context.Background())
		cancelCause1(errors.New("user cancelled"))
		toolCtxMu.Lock()
		tool1Err := lastToolCtx.Err()
		toolCtxMu.Unlock()
		if tool1Err != nil {
			t.Fatalf("expected unrelated in-flight tool ctx not to be aborted, got %v", tool1Err)
		}
		f1 := tsandExecFailure(t, sb, cancelCtx1, "await tools.hang()", nil)
		if f1.Error.Kind != codemode.CodemodeErrorKindAborted || f1.Error.Message != "user cancelled" {
			t.Fatalf("f1 = %+v", f1.Error)
		}

		cancelCtx2, cancel2 := context.WithCancel(context.Background())
		var wg2 sync.WaitGroup
		wg2.Add(1)
		var f2Res codemode.CodemodeResult
		var f2Err error
		go func() {
			defer wg2.Done()
			f2Res, f2Err = sb.Execute(cancelCtx2, "await tools.hang(); return 'never'", nil)
		}()
		<-calledCh
		cancel2()
		wg2.Wait()
		f2 := tsandMustFailure(t, f2Res, f2Err)
		if f2.Error.Kind != codemode.CodemodeErrorKindAborted {
			t.Fatalf("f2 = %+v", f2.Error)
		}
		if len(f2.Calls) != 1 || f2.Calls[0].Name != "hang" || f2.Calls[0].Status != codemode.CodemodeCallStatusCancelled {
			t.Fatalf("f2 calls = %+v", f2.Calls)
		}
		toolCtxMu.Lock()
		tool2Err := lastToolCtx.Err()
		toolCtxMu.Unlock()
		if tool2Err == nil {
			t.Fatal("expected second tool ctx to be aborted")
		}

		if err := sb.Close(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		fClose := tsandMustFailure(t, closeRes, closeErr)
		if fClose.Error.Kind != codemode.CodemodeErrorKindAborted || fClose.Error.Message != "Sandbox closed" {
			t.Fatalf("fClose = %+v", fClose.Error)
		}
	})

	t.Run("rejects execute after close", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		if err := sb.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := sb.Execute(ctx, "return 1", nil); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("expected closed error, got %v", err)
		}
	})

	t.Run("runs executions in parallel without sharing state", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		scripts := []string{
			"globalThis.shared = 'a'; await null; return globalThis.shared",
			"globalThis.shared = 'b'; await null; return globalThis.shared",
			"return typeof globalThis.shared",
		}
		vals := make([]any, len(scripts))
		var wg sync.WaitGroup
		wg.Add(len(scripts))
		for i, script := range scripts {
			go func(idx int, code string) {
				defer wg.Done()
				s := tsandExecSuccess(t, sb, ctx, code, nil)
				v, _ := s.Value.Get()
				vals[idx] = v
			}(i, script)
		}
		wg.Wait()
		if got := tsandToJSONString(t, vals); got != `["a","b","undefined"]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("turns deep recursion into a catchable RangeError", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			let depth = 0;
			function dive() { depth++; dive(); }
			try { dive(); } catch (error) { return [error.name, depth > 1000]; }
		`, nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `["RangeError",true]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("enforces memoryLimitBytes", func(t *testing.T) {
		sb, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			MemoryLimitBytes: tsandIntPtr(512 * 1024),
			TimeoutMs:        tsandFloatPtr(5_000),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sb.Close()
		f := tsandExecFailure(t, sb, ctx, `const a = []; while (true) a.push("x".repeat(4096));`, nil)
		if f.Error.Kind != codemode.CodemodeErrorKindScript && f.Error.Kind != codemode.CodemodeErrorKindSandbox {
			t.Fatalf("unexpected error kind: %+v", f.Error)
		}
	})

	t.Run("accepts workerUrl and preloaded wasm options", func(t *testing.T) {
		wasmMod, err := codemode.LoadQuickJSWasm(nil)
		if err != nil {
			t.Fatal(err)
		}
		sb, err := codemoderuntime.NewCodemodeSandbox(&codemode.CodemodeSandboxOptions{
			Wasm:      wasmMod,
			WorkerUrl: tsandStrPtr("unused-in-go.js"),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sb.Close()
		s := tsandExecSuccess(t, sb, ctx, "return 1", nil)
		if val, _ := s.Value.Get(); val != float64(1) {
			t.Fatalf("val = %v", val)
		}
	})
}

func TestEscapeHatches(t *testing.T) {
	ctx := context.Background()

	t.Run("has no host globals", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			return [
				typeof process, typeof require, typeof module, typeof setTimeout, typeof fetch,
				typeof WebAssembly, typeof std, typeof os, typeof globalThis.constructor,
			]
		`, nil)
		val, _ := s.Value.Get()
		want := `["undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined","function"]`
		if got := tsandToJSONString(t, val); got != want {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("keeps eval and Function inside the VM", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{tsandEchoTool()}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			return [
				eval("typeof process"),
				new Function("return typeof process")(),
				tools.echo.constructor("return typeof require")(),
				(async () => {}).constructor("return typeof setTimeout")() instanceof Promise,
			];
		`, nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `["undefined","undefined","undefined",true]` {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("rejects dynamic import", func(t *testing.T) {
		sb := tsandNewSandbox(t, nil, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			try { await import("node:fs"); return "imported"; } catch (error) { return error.constructor.name; }
		`, nil)
		if val, _ := s.Value.Get(); val == "imported" {
			t.Fatalf("unexpected import success")
		}
	})

	t.Run("keeps tools and console frozen", func(t *testing.T) {
		sb := tsandNewSandbox(t, []*codemode.CodemodeTool{tsandEchoTool()}, 10_000)
		s := tsandExecSuccess(t, sb, ctx, `
			try { tools.echo = () => 'nope'; } catch {}
			try { tools.extra = () => 'nope'; } catch {}
			try { globalThis.tools = null; } catch {}
			return ["extra" in tools, await tools.echo('still')];
		`, nil)
		val, _ := s.Value.Get()
		if got := tsandToJSONString(t, val); got != `[false,"still"]` {
			t.Fatalf("got %s", got)
		}
	})
}

func TestProtocolMessages(t *testing.T) {
	messages := []string{
		`{"type":"call","id":1,"target":"tool","name":"echo","args":"{\"a\":1}"}`,
		`{"type":"call","id":2,"target":"global","name":"attach"}`,
		`{"type":"output","item":{"type":"text","text":"hello"}}`,
		`{"type":"output","item":{"type":"image","data":"AQID","mimeType":"image/png"}}`,
		`{"type":"done","ok":true,"value":"42","writes":"[]"}`,
		`{"type":"done","ok":false,"error":"{\"message\":\"boom\"}"}`,
		`{"type":"crash","message":"trap"}`,
		`{"type":"future","extra":123}`,
	}
	for _, raw := range messages {
		msg, err := codemoderuntime.UnmarshalWorkerToHostMessage([]byte(raw))
		if err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		_, isUnk := msg.(*codemoderuntime.UnknownWorkerToHostMessage)
		if codemoderuntime.IsWorkerToHostMessage(msg) == isUnk {
			t.Fatalf("IsWorkerToHostMessage mismatch for %s (%T)", raw, msg)
		}
		re, err := jsonx.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal %T: %v", msg, err)
		}
		if string(re) != raw {
			t.Fatalf("round-trip %s -> %s", raw, re)
		}
	}

	h := &codemoderuntime.HostToWorkerMessage{
		Type:    "result",
		Id:      7,
		Ok:      true,
		Payload: tsandStrPtr(`"ok"`),
	}
	if !codemoderuntime.IsHostToWorkerMessage(h) {
		t.Fatal("expected IsHostToWorkerMessage true")
	}
	if codemoderuntime.IsHostToWorkerMessage(&codemoderuntime.HostToWorkerMessage{Type: "bad"}) {
		t.Fatal("expected IsHostToWorkerMessage false for bad type")
	}
	if codemoderuntime.IsHostToWorkerMessage(nil) || codemoderuntime.IsWorkerToHostMessage(nil) {
		t.Fatal("expected false for nil")
	}
}
