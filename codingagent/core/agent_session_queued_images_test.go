// Ported from packages/coding-agent/test/agent-session-queued-images.test.ts (pi v1.5.2).

package core_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
)

func tasqiStrPtr(s string) *string { return &s }

func tasqiPngChunk(chunkType string, data []byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	body := make([]byte, 0, len(chunkType)+len(data))
	body = append(body, chunkType...)
	body = append(body, data...)
	buf.Write(body)
	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(body))
	return buf.Bytes()
}

// tasqiCreatePng builds a solid gray RGB PNG of the given size, base64 encoded.
func tasqiCreatePng(width, height int) string {
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:4], uint32(width))
	binary.BigEndian.PutUint32(header[4:8], uint32(height))
	header[8] = 8
	header[9] = 2

	rowLen := 1 + width*3
	raw := make([]byte, height*rowLen)
	for y := range height {
		base := y * rowLen
		raw[base] = 0
		for x := 1; x < rowLen; x++ {
			raw[base+x] = 0x80
		}
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, _ = zw.Write(raw)
	_ = zw.Close()

	var out bytes.Buffer
	out.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	out.Write(tasqiPngChunk("IHDR", header))
	out.Write(tasqiPngChunk("IDAT", compressed.Bytes()))
	out.Write(tasqiPngChunk("IEND", nil))
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

func tasqiPersistedUserContent(t *testing.T, h *testkit.Harness, text string) ai.ContentList {
	t.Helper()
	for _, entry := range h.SessionManager.GetEntries() {
		msgEntry, ok := entry.(*core.SessionMessageEntry)
		if !ok {
			continue
		}
		userMsg, ok := msgEntry.Message.(*ai.UserMessage)
		if !ok {
			continue
		}
		if userMsg.Content.Text != nil || len(userMsg.Content.Blocks) == 0 {
			continue
		}
		if tb, ok := userMsg.Content.Blocks[0].(*ai.TextContent); ok {
			firstLine := strings.Split(tb.Text, "\n")[0]
			if firstLine == text {
				return userMsg.Content.Blocks
			}
		}
	}
	t.Fatalf("No user message %q found in persisted entries", text)
	return nil
}

func tasqiWaitFor(t *testing.T, condition func() bool) {
	t.Helper()
	for range 200 {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for condition")
}

func TestAgentSessionQueuedMessageImages(t *testing.T) {
	ctx := context.Background()

	// Regression test for #11738: queued messages skipped the image resize idle prompts get.
	t.Run("resizes images in steering, follow-up, and streaming prompt messages", func(t *testing.T) {
		h, err := testkit.CreateHarness(ctx, &testkit.HarnessOptions{
			Responses: []testkit.FauxResponse{
				{Text: tasqiStrPtr("first"), DelayMs: 300},
				testkit.FauxText("second"),
				testkit.FauxText("third"),
				testkit.FauxText("fourth"),
			},
		})
		if err != nil {
			t.Fatalf("CreateHarness: %v", err)
		}
		defer h.Cleanup()

		origData := tasqiCreatePng(1200, 9000)
		image := &ai.ImageContent{
			Type:     "image",
			Data:     origData,
			MimeType: "image/png",
		}

		runErrCh := make(chan error, 1)
		go func() {
			runErrCh <- h.Session.Prompt(ctx, "idle")
		}()

		tasqiWaitFor(t, func() bool { return h.Session.IsStreaming() })

		if _, err := h.Session.Steer(ctx, "steer", []*ai.ImageContent{image}); err != nil {
			t.Fatalf("Steer: %v", err)
		}
		if _, err := h.Session.FollowUp(ctx, "follow up", []*ai.ImageContent{image}); err != nil {
			t.Fatalf("FollowUp: %v", err)
		}
		if err := h.Session.Prompt(ctx, "streaming prompt", &core.PromptOptions{
			Images:            []*ai.ImageContent{image},
			StreamingBehavior: "followUp",
		}); err != nil {
			t.Fatalf("Prompt(streaming): %v", err)
		}

		if err := <-runErrCh; err != nil {
			t.Fatalf("initial Prompt: %v", err)
		}
		tasqiWaitFor(t, func() bool {
			return !h.Session.IsStreaming() && h.Faux.CallCount() >= 4
		})

		for _, text := range []string{"steer", "follow up", "streaming prompt"} {
			content := tasqiPersistedUserContent(t, h, text)
			var images []*ai.ImageContent
			for _, block := range content {
				if img, ok := block.(*ai.ImageContent); ok {
					images = append(images, img)
				}
			}
			if len(images) != 1 {
				t.Fatalf("%q: expected 1 image block, got %d", text, len(images))
			}
			if images[0].Data == origData {
				t.Fatalf("%q: expected resized image data, got unmodified original", text)
			}
			tb, ok := content[0].(*ai.TextContent)
			if !ok || !strings.Contains(tb.Text, "original 1200x9000, displayed at 267x2000") {
				t.Fatalf("%q: expected dimension note in first text block, got %#v", text, content[0])
			}
		}

		sentImageCount := 0
		for _, tr := range h.Faux.Contexts() {
			for _, msg := range tr.Messages {
				userMsg, ok := msg.(*ai.UserMessage)
				if !ok || userMsg.Content.Text != nil {
					continue
				}
				for _, block := range userMsg.Content.Blocks {
					if img, ok := block.(*ai.ImageContent); ok {
						sentImageCount++
						if img.Data == origData {
							t.Fatalf("expected all sent images to be resized, found unresized original")
						}
					}
				}
			}
		}
		if sentImageCount == 0 {
			t.Fatalf("expected at least one sent image block in provider contexts")
		}
		if got := h.Session.PendingMessageCount(); got != 0 {
			t.Fatalf("PendingMessageCount = %d, want 0", got)
		}
	})
}
