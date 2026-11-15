// Ported from packages/coding-agent/test/suite/agent-session-prompt.test.ts and agent-session-tool-result-images.test.ts (pi v1.5.2).

package core_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	core "github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tsaspBoolPtr(v bool) *bool { return &v }

func tsaspIntPtr(v int) *int { return &v }

func tsaspFloatPtr(v float64) *float64 { return &v }

func tsaspStrPtr(v string) *string { return &v }

func tsaspEncodePngBase64(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Vary pixel values across coordinates so high-resolution images exceed compression thresholds if needed.
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 17) & 0xff),
				G: uint8((y * 31) & 0xff),
				B: uint8(((x + y) * 13) & 0xff),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode(%dx%d): %v", width, height, err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestAgentSessionPromptCharacterization(t *testing.T) {
	ctx := context.Background()

	t.Run("executes a single prompt and records the response", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Hi there!"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "Hello"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		userTexts := testkit.GetUserTexts(h)
		if len(userTexts) != 1 || userTexts[0] != "Hello" {
			t.Fatalf("expected user texts [Hello], got %#v", userTexts)
		}
		assistantTexts := testkit.GetAssistantTexts(h)
		if len(assistantTexts) != 1 || assistantTexts[0] != "Hi there!" {
			t.Fatalf("expected assistant texts [Hi there!], got %#v", assistantTexts)
		}
		if h.Faux.State.CallCount != 1 {
			t.Fatalf("expected 1 LLM call, got %d", h.Faux.State.CallCount)
		}
	})

	t.Run("executes tools and preserves multi-turn transcript order", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("value", typebox.String())
		echoTool := &agent.AgentTool{
			Name:        "echo",
			Label:       "Echo",
			Description: "Echo value",
			Parameters:  typebox.Object(props),
			Execute: func(_ context.Context, _ string, params any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				val := ""
				if obj, ok := params.(*jsonx.Object); ok && obj != nil {
					if s, ok := obj.GetString("value"); ok {
						val = s
					}
				}
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "echo:" + val}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{echoTool},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{
						Block: providers.FauxToolCall("echo", jsonx.ObjectOf("value", "ping"), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Got: echo:ping"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "Run echo"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		msgs := h.Session.Messages()
		var roles []string
		for _, m := range msgs {
			switch m.(type) {
			case *ai.UserMessage:
				roles = append(roles, "user")
			case *ai.AssistantMessage:
				roles = append(roles, "assistant")
			case *ai.ToolResultMessage:
				roles = append(roles, "toolResult")
			case *ai.SystemMessage:
				roles = append(roles, "system")
			}
		}
		// Expect system message (if present) followed by user, assistant, toolResult, assistant.
		if len(roles) < 4 {
			t.Fatalf("expected at least 4 transcript messages, got %v", roles)
		}
		tail := roles[len(roles)-4:]
		expectedTail := []string{"user", "assistant", "toolResult", "assistant"}
		for i, want := range expectedTail {
			if tail[i] != want {
				t.Fatalf("expected transcript tail %v, got %v (full: %v)", expectedTail, tail, roles)
			}
		}

		tr, err := testkit.GetToolResult(h, "echo")
		if err != nil {
			t.Fatalf("GetToolResult: %v", err)
		}
		if got := testkit.GetMessageText(tr); got != "echo:ping" {
			t.Fatalf("expected tool result 'echo:ping', got %q", got)
		}
		assistantTexts := testkit.GetAssistantTexts(h)
		if len(assistantTexts) != 2 || assistantTexts[1] != "Got: echo:ping" {
			t.Fatalf("unexpected assistant texts: %#v", assistantTexts)
		}
	})

	t.Run("executes multi-tool turns in parallel", func(t *testing.T) {
		var orderMu sync.Mutex
		var executed []string
		mkTool := func(name string) *agent.AgentTool {
			return &agent.AgentTool{
				Name:        name,
				Label:       name,
				Description: "Tool " + name,
				Parameters:  typebox.Object(nil),
				Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
					orderMu.Lock()
					executed = append(executed, name)
					orderMu.Unlock()
					return agent.AgentToolResult{
						Content: ai.ContentList{&ai.TextContent{Type: "text", Text: name + " ok"}},
					}, nil
				},
			}
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{mkTool("first"), mkTool("second")},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlocks{
						providers.FauxToolCall("first", jsonx.NewObject(), nil),
						providers.FauxToolCall("second", jsonx.NewObject(), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("both done"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "Run both"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		orderMu.Lock()
		got := slices.Clone(executed)
		orderMu.Unlock()
		slices.Sort(got)
		if strings.Join(got, ",") != "first,second" {
			t.Fatalf("expected both tools [first, second] to execute, got %v", got)
		}
		starts := h.EventsOfType("tool_execution_start")
		ends := h.EventsOfType("tool_execution_end")
		if len(starts) != 2 || len(ends) != 2 {
			t.Fatalf("expected 2 tool start/end events, got %d starts / %d ends", len(starts), len(ends))
		}
	})

	t.Run("accepts user images alongside text prompts", func(t *testing.T) {
		tinyPng := tsaspEncodePngBase64(t, 2, 2)
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Models: []providers.FauxModelDefinition{
				{
					Id:            "faux-vision",
					Input:         []string{"text", "image"},
					ContextWindow: tsaspIntPtr(100_000),
					MaxTokens:     tsaspIntPtr(4096),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		sawImage := false
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						for _, blk := range um.Content.Blocks {
							if img, ok := blk.(*ai.ImageContent); ok && img != nil && img.Data == tinyPng {
								sawImage = true
							}
						}
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("saw image"), nil), nil
			}),
		})

		err = h.Session.Prompt(ctx, "describe image", &core.PromptOptions{
			Images: []*ai.ImageContent{
				{Type: "image", Data: tinyPng, MimeType: "image/png"},
			},
		})
		if err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !sawImage {
			t.Fatal("expected LLM request to contain user image content block")
		}
	})

	t.Run("expands skill commands from the loaded resource loader", func(t *testing.T) {
		skillDir := t.TempDir()
		skillPath := filepath.Join(skillDir, "SKILL.md")
		skillBody := "---\nname: deploy\ndescription: Deploy the app\n---\n# Deploy Steps\nRun ./deploy.sh now."
		if err := os.WriteFile(skillPath, []byte(skillBody), 0o644); err != nil {
			t.Fatalf("WriteFile skill: %v", err)
		}

		loader := testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
			Skills: []core.Skill{
				{
					Name:        "deploy",
					Description: "Deploy the app",
					FilePath:    skillPath,
					BaseDir:     skillDir,
					SourceInfo: utils.CreateSyntheticSourceInfo(skillPath, utils.SyntheticSourceOptions{
						Source: "local",
					}),
				},
			},
		})

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			ResourceLoader: loader,
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		var seenUserText string
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						seenUserText = testkit.GetMessageText(um)
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("deployed"), nil), nil
			}),
		})

		if err := h.Session.Prompt(ctx, "/skill:deploy staging"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !strings.Contains(seenUserText, `<skill name="deploy"`) || !strings.Contains(seenUserText, "Run ./deploy.sh now.") || !strings.Contains(seenUserText, "staging") {
			t.Fatalf("expected expanded skill command in user text, got %q", seenUserText)
		}
	})

	t.Run("expands prompt templates when prompting and skips when disabled", func(t *testing.T) {
		loader := testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
			Prompts: []core.PromptTemplate{
				{
					Name:        "greet",
					Description: "Greet user",
					Content:     "Say hello to $1",
					FilePath:    "/tmp/greet.md",
					SourceInfo: utils.CreateSyntheticSourceInfo("/tmp/greet.md", utils.SyntheticSourceOptions{
						Source: "local",
					}),
				},
			},
		})

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			ResourceLoader: loader,
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("expanded reply"), nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("raw reply"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "/greet Ada"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		if err := h.Session.Prompt(ctx, "/greet Bob", &core.PromptOptions{
			ExpandPromptTemplates: tsaspBoolPtr(false),
		}); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}

		userTexts := testkit.GetUserTexts(h)
		if len(userTexts) != 2 {
			t.Fatalf("expected 2 user texts, got %#v", userTexts)
		}
		if userTexts[0] != "Say hello to Ada" {
			t.Fatalf("expected first prompt to expand to 'Say hello to Ada', got %q", userTexts[0])
		}
		if userTexts[1] != "/greet Bob" {
			t.Fatalf("expected second prompt to remain '/greet Bob', got %q", userTexts[1])
		}
	})

	t.Run("before_agent_start hook", func(t *testing.T) {
		t.Skip("barebone excludes extension hooks (before_agent_start)")
	})

	t.Run("input hook", func(t *testing.T) {
		t.Skip("barebone excludes extension input hooks")
	})

	t.Run("sendUserMessage while idle triggers a turn", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("extension user reply"), nil),
			},
		})

		if err := h.Session.SendUserMessage(ctx, "hello from sendUserMessage", nil); err != nil {
			t.Fatalf("SendUserMessage: %v", err)
		}

		userTexts := testkit.GetUserTexts(h)
		if len(userTexts) != 1 || userTexts[0] != "hello from sendUserMessage" {
			t.Fatalf("unexpected user texts: %#v", userTexts)
		}
	})

	t.Run("sendUserMessage while streaming requires deliverAs", func(t *testing.T) {
		toolStarted := make(chan struct{})
		releaseTool := make(chan struct{})
		waitTool := &agent.AgentTool{
			Name:        "wait_tool",
			Label:       "Wait",
			Description: "Wait for release",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				close(toolStarted)
				<-releaseTool
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "released"}},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{waitTool},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("wait_tool", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil),
			},
		})

		promptErrCh := make(chan error, 1)
		go func() {
			promptErrCh <- h.Session.Prompt(ctx, "start tool")
		}()

		select {
		case <-toolStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for tool start")
		}

		errNoDeliverAs := h.Session.SendUserMessage(ctx, "should fail", nil)
		if errNoDeliverAs == nil || !strings.Contains(errNoDeliverAs.Error(), "already processing") {
			close(releaseTool)
			t.Fatalf("expected 'already processing' error, got %v", errNoDeliverAs)
		}

		if err := h.Session.SendUserMessage(ctx, "queued steer", nil, &core.SendUserMessageOptions{
			DeliverAs: "steer",
		}); err != nil {
			close(releaseTool)
			t.Fatalf("SendUserMessage with deliverAs='steer' failed: %v", err)
		}

		close(releaseTool)
		if err := <-promptErrCh; err != nil {
			t.Fatalf("Prompt failed: %v", err)
		}

		userTexts := testkit.GetUserTexts(h)
		if len(userTexts) != 2 || userTexts[1] != "queued steer" {
			t.Fatalf("expected delivered steer text in transcript, got %#v", userTexts)
		}
	})

	t.Run("sendCustomMessage while idle with triggerTurn starts a turn", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		sawCustom := false
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if strings.Contains(testkit.GetMessageText(m), "custom prompt") {
						sawCustom = true
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("custom noted"), nil), nil
			}),
		})

		err = h.Session.SendCustomMessage(ctx, "note", "custom prompt", true, nil, &core.SendCustomMessageOptions{
			TriggerTurn: true,
		})
		if err != nil {
			t.Fatalf("SendCustomMessage: %v", err)
		}
		if !sawCustom {
			t.Fatal("expected LLM call to observe custom message triggered turn")
		}
		assistantTexts := testkit.GetAssistantTexts(h)
		if len(assistantTexts) != 1 || assistantTexts[0] != "custom noted" {
			t.Fatalf("unexpected assistant texts: %#v", assistantTexts)
		}
	})

	t.Run("throws when prompting during manual compaction", func(t *testing.T) {
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Settings: &core.Settings{
				Compaction: &core.SettingsCompactionSettings{
					KeepRecentTokens: tsaspFloatPtr(1),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		compactionStarted := make(chan struct{})
		releaseCompaction := make(chan struct{})
		var compactOnce sync.Once

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("seed reply 1"), nil),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("seed reply 2"), nil),
			},
			providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				compactOnce.Do(func() { close(compactionStarted) })
				<-releaseCompaction
				return providers.FauxAssistantMessage(providers.FauxAssistantText("Summary:\nseed"), nil), nil
			}),
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Turn prefix summary"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "seed prompt 1"); err != nil {
			t.Fatalf("seed Prompt 1: %v", err)
		}
		if err := h.Session.Prompt(ctx, "seed prompt 2"); err != nil {
			t.Fatalf("seed Prompt 2: %v", err)
		}

		compactErrCh := make(chan error, 1)
		go func() {
			_, err := h.Session.Compact(ctx)
			compactErrCh <- err
		}()

		select {
		case <-compactionStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for compaction LLM call")
		}

		errWhileCompacting := h.Session.Prompt(ctx, "during compaction")
		close(releaseCompaction)
		if compactErr := <-compactErrCh; compactErr != nil {
			t.Fatalf("Compact failed: %v", compactErr)
		}

		if errWhileCompacting == nil || !strings.Contains(errWhileCompacting.Error(), "compaction") {
			t.Fatalf("expected error mentioning compaction, got %v", errWhileCompacting)
		}
	})

	t.Run("throws when no model or no auth", func(t *testing.T) {
		hNoModel, err := testkit.CreateSuiteHarness(ctx)
		if err != nil {
			t.Fatalf("CreateSuiteHarness (no model): %v", err)
		}
		defer hNoModel.Cleanup()
		hNoModel.Session.Agent.SetModel(nil)
		if err := hNoModel.Session.Prompt(ctx, "hi"); err == nil || !strings.Contains(err.Error(), "No model selected") {
			t.Fatalf("expected 'No model selected' error, got %v", err)
		}

		hNoAuth, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			WithConfiguredAuth: tsaspBoolPtr(false),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness (no auth): %v", err)
		}
		defer hNoAuth.Cleanup()

		if err := hNoAuth.Session.Prompt(ctx, "hi"); err == nil {
			t.Fatal("expected error prompting without configured API key")
		}
	})

	t.Run("extension command execution", func(t *testing.T) {
		t.Skip("barebone excludes extension commands")
	})
}

func TestAgentSessionToolResultImages(t *testing.T) {
	ctx := context.Background()

	t.Run("preserves small PNG tool results when autoResize is disabled", func(t *testing.T) {
		smallPng := tsaspEncodePngBase64(t, 12, 12)
		imgTool := &agent.AgentTool{
			Name:        "img_tool",
			Label:       "Image",
			Description: "Returns PNG image",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{
						&ai.TextContent{Type: "text", Text: "image attached"},
						&ai.ImageContent{Type: "image", Data: smallPng, MimeType: "image/png"},
					},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{imgTool},
			Settings: &core.Settings{
				Images: &core.ImageSettings{
					AutoResize: tsaspBoolPtr(false),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("img_tool", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("seen"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "show image"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		tr, err := testkit.GetToolResult(h, "img_tool")
		if err != nil {
			t.Fatalf("GetToolResult: %v", err)
		}
		var foundImg *ai.ImageContent
		for _, blk := range tr.Content {
			if img, ok := blk.(*ai.ImageContent); ok {
				foundImg = img
			}
		}
		if foundImg == nil {
			t.Fatal("expected ImageContent in tool result")
		}
		if foundImg.Data != smallPng {
			t.Fatal("expected small PNG bytes to be preserved verbatim when autoResize is false")
		}
	})

	t.Run("normalizes oversized tool result image according to model inputLimits", func(t *testing.T) {
		largePng := tsaspEncodePngBase64(t, 128, 128)
		imgTool := &agent.AgentTool{
			Name:        "big_img_tool",
			Label:       "Big Image",
			Description: "Returns oversized PNG image",
			Parameters:  typebox.Object(nil),
			Execute: func(_ context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{
					Content: ai.ContentList{
						&ai.ImageContent{Type: "image", Data: largePng, MimeType: "image/png"},
					},
				}, nil
			},
		}

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{imgTool},
			Models: []providers.FauxModelDefinition{
				{
					Id:            "faux-img-limit",
					Input:         []string{"text", "image"},
					ContextWindow: tsaspIntPtr(100_000),
					MaxTokens:     tsaspIntPtr(4096),
					InputLimits: &ai.ModelInputLimits{
						Images: &ai.ModelImageInputLimits{
							Resize: &ai.ModelImageResizeOptions{
								MaxWidth:  tsaspIntPtr(32),
								MaxHeight: tsaspIntPtr(32),
							},
						},
					},
				},
			},
			Settings: &core.Settings{
				Images: &core.ImageSettings{
					AutoResize: tsaspBoolPtr(true),
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("big_img_tool", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(providers.FauxAssistantText("seen resized"), nil),
			},
		})

		if err := h.Session.Prompt(ctx, "show big image"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}

		tr, err := testkit.GetToolResult(h, "big_img_tool")
		if err != nil {
			t.Fatalf("GetToolResult: %v", err)
		}
		var foundImg *ai.ImageContent
		for _, blk := range tr.Content {
			if img, ok := blk.(*ai.ImageContent); ok {
				foundImg = img
			}
		}
		if foundImg == nil {
			t.Fatal("expected normalized ImageContent in tool result")
		}
		if foundImg.Data == largePng {
			t.Fatal(fmt.Sprintf("expected oversized image to be resized by ModelInputLimits.Images.Resize, still got identical %d-byte base64", len(largePng)))
		}
	})
}
