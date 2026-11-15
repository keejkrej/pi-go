package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func ttrTestTextOutput(res agent.AgentToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*ai.TextContent); ok && tc != nil {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestReadTool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tool := CreateReadTool(dir)

	t.Run("reads file contents within limits", func(t *testing.T) {
		p := filepath.Join(dir, "small.txt")
		if err := os.WriteFile(p, []byte("line 1\nline 2\nline 3"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := tool.Execute(ctx, "r1", jsonx.ObjectOf("path", "small.txt"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(res); got != "line 1\nline 2\nline 3" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("errors on missing file", func(t *testing.T) {
		if _, err := tool.Execute(ctx, "r2", jsonx.ObjectOf("path", "does-not-exist.txt"), nil); err == nil {
			t.Fatalf("expected error for missing file")
		}
	})

	t.Run("truncates files exceeding line limit and supports offset and limit", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 2100; i++ {
			b.WriteString("Line ")
			b.WriteString(strings.Repeat("0", 4-len(ttrIntStr(i))))
			b.WriteString(ttrIntStr(i))
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(strings.TrimSuffix(b.String(), "\n")), 0o600); err != nil {
			t.Fatal(err)
		}

		res, err := tool.Execute(ctx, "r3", jsonx.ObjectOf("path", "long.txt"), nil)
		if err != nil {
			t.Fatal(err)
		}
		text := ttrTestTextOutput(res)
		if !strings.Contains(text, "Use offset=2001 to continue") {
			t.Fatalf("expected continuation notice in:\n%s", text[max(0, len(text)-200):])
		}
		details, ok := res.Details.(*ReadToolDetails)
		if !ok || details == nil || details.Truncation == nil || !details.Truncation.Truncated {
			t.Fatalf("expected ReadToolDetails with Truncated=true, got %#v", res.Details)
		}

		sliceRes, err := tool.Execute(ctx, "r4", jsonx.ObjectOf("path", "long.txt", "offset", 10, "limit", 5), nil)
		if err != nil {
			t.Fatal(err)
		}
		sliceText := ttrTestTextOutput(sliceRes)
		if !strings.Contains(sliceText, "Line 0010") || !strings.Contains(sliceText, "Line 0014") || strings.Contains(sliceText, "Line 0015") {
			t.Fatalf("unexpected offset+limit output:\n%s", sliceText)
		}
		if !strings.Contains(sliceText, "Use offset=15 to continue") {
			t.Fatalf("expected offset=15 notice, got:\n%s", sliceText)
		}
	})

	t.Run("errors when offset exceeds file length", func(t *testing.T) {
		p := filepath.Join(dir, "short.txt")
		if err := os.WriteFile(p, []byte("a\nb\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Execute(ctx, "r5", jsonx.ObjectOf("path", "short.txt", "offset", 99), nil); err == nil {
			t.Fatalf("expected error for offset beyond EOF")
		}
	})

	t.Run("detects PNG magic even with txt extension and flags text-only models", func(t *testing.T) {
		// 1x1 transparent PNG
		pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "secret.txt"), pngBytes, 0o600); err != nil {
			t.Fatal(err)
		}

		resImg, err := tool.Execute(ctx, "r6", jsonx.ObjectOf("path", "secret.txt"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(resImg.Content) < 2 {
			t.Fatalf("expected text + image content, got %d blocks", len(resImg.Content))
		}
		imgBlock, ok := resImg.Content[1].(*ai.ImageContent)
		if !ok || imgBlock.MimeType != "image/png" {
			t.Fatalf("expected image/png block, got %#v", resImg.Content[1])
		}

		// Text-only model context preserves image block and flags UI detail with model.Id
		def := CreateReadToolDefinition(dir)
		textOnlyModel := &ai.Model{
			Id:    "text-only",
			Name:  "Text Only Model",
			Input: []ai.ModelModality{"text"},
		}
		resPlaceholder, err := def.Execute(ctx, "r7", jsonx.ObjectOf("path", "secret.txt"), nil, &ttrMockToolContext{model: textOnlyModel})
		if err != nil {
			t.Fatal(err)
		}
		if len(resPlaceholder.Content) < 2 {
			t.Fatalf("expected text + image blocks, got %d", len(resPlaceholder.Content))
		}
		details, _ := resPlaceholder.Details.(*ReadToolDetails)
		if details == nil || details.ImageUnsupportedByModel == nil || *details.ImageUnsupportedByModel != "text-only" {
			t.Fatalf("expected ImageUnsupportedByModel='text-only', got %+v", details)
		}
	})

	t.Run("handles image extension with non-image content as text", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, "fake.png"), []byte("not really an image"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := tool.Execute(ctx, "r8", jsonx.ObjectOf("path", "fake.png"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(res); got != "not really an image" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("streaming decodes UTF-8 split across chunks and respects abort", func(t *testing.T) {
		payload := []byte("alpha\n€beta\ngamma")
		customOps := &ReadOperations{
			Access: func(context.Context, string) error { return nil },
			DetectImageMimeType: func(context.Context, string) (*string, error) {
				return nil, nil
			},
			CreateReadStream: func(context.Context, string) (io.ReadCloser, error) {
				return &ttrChunkedReader{chunks: [][]byte{payload[:7], payload[7:8], payload[8:]}}, nil
			},
		}
		streamTool := CreateReadTool(dir, &ReadToolOptions{Operations: customOps})
		res, err := streamTool.Execute(ctx, "r9", jsonx.ObjectOf("path", "any.txt", "offset", 2, "limit", 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(res); !strings.HasPrefix(got, "€beta\n") {
			t.Fatalf("expected €beta, got %q", got)
		}

		abortCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := streamTool.Execute(abortCtx, "r10", jsonx.ObjectOf("path", "any.txt"), nil); err == nil {
			t.Fatalf("expected abort error")
		}
	})

	t.Run("preserves UTF-8 BOM across stream and buffer read paths", func(t *testing.T) {
		p := filepath.Join(dir, "bom.txt")
		if err := os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, []byte("first\nsecond")...), 0o600); err != nil {
			t.Fatal(err)
		}
		// Stream path (offset=1)
		resStream, err := tool.Execute(ctx, "r11", jsonx.ObjectOf("path", "bom.txt", "offset", 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(resStream); got != "\uFEFFfirst\nsecond" {
			t.Fatalf("stream path got %q", got)
		}
		// Buffer fallback path (negative limit is non-streamable range)
		resBuf, err := tool.Execute(ctx, "r12", jsonx.ObjectOf("path", "bom.txt", "limit", -1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(resBuf); !strings.HasPrefix(got, "\uFEFFfirst") {
			t.Fatalf("buffer path expected BOM prefix, got %q", got)
		}
	})
}

type ttrChunkedReader struct {
	chunks [][]byte
}

func (r *ttrChunkedReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if len(r.chunks[0]) == 0 {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

func (r *ttrChunkedReader) Close() error { return nil }

type ttrMockToolContext struct {
	cwd   string
	model *ai.Model
}

func (m *ttrMockToolContext) UI() ExtensionUIContext                 { return NoOpUIContext() }
func (m *ttrMockToolContext) Mode() ExtensionMode                    { return ExtensionModePrint }
func (m *ttrMockToolContext) HasUI() bool                            { return false }
func (m *ttrMockToolContext) Cwd() string                            { return m.cwd }
func (m *ttrMockToolContext) SessionManager() ReadonlySessionManager { return nil }
func (m *ttrMockToolContext) ModelRegistry() *ModelRegistry          { return nil }
func (m *ttrMockToolContext) Model() *ai.Model                       { return m.model }
func (m *ttrMockToolContext) ScopedModels() []ScopedModel            { return nil }
func (m *ttrMockToolContext) ThinkingLevel() agent.ThinkingLevel     { return "" }
func (m *ttrMockToolContext) IsIdle() bool                           { return true }
func (m *ttrMockToolContext) IsProjectTrusted() bool                 { return true }
func (m *ttrMockToolContext) Signal() context.Context                { return nil }
func (m *ttrMockToolContext) Abort()                                 {}
func (m *ttrMockToolContext) HasPendingMessages() bool               { return false }
func (m *ttrMockToolContext) Shutdown()                              {}
func (m *ttrMockToolContext) GetContextUsage() *ContextUsage         { return nil }
func (m *ttrMockToolContext) Compact(...*CompactOptions)             {}
func (m *ttrMockToolContext) GetSystemPrompt() string                { return "" }
func (m *ttrMockToolContext) Tools() []*agent.AgentTool              { return nil }
func (m *ttrMockToolContext) ExecuteTool(context.Context, string, any, ...*ExecuteToolOptions) (agent.AgentToolCallOutcome, error) {
	return agent.AgentToolCallOutcome{}, nil
}

func ttrIntStr(n int) string {
	var buf bytes.Buffer
	for i := n; i > 0; i /= 10 {
		buf.WriteByte(byte('0' + i%10))
	}
	b := buf.Bytes()
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
