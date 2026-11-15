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
		largePng := tsaspEncodePngBase64(t, 200, 200)
		var capturedEvents []core.BeforeAgentStartEvent
		var mu sync.Mutex

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Models: []providers.FauxModelDefinition{
				{
					Id:            "faux-permissive",
					Input:         []string{"text", "image"},
					ContextWindow: tsaspIntPtr(100_000),
					MaxTokens:     tsaspIntPtr(4096),
				},
				{
					Id:            "faux-strict",
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
			ExtensionFactories: []core.InlineExtension{
				{
					Name: "before-agent-start-ext",
					Factory: func(pi core.ExtensionAPI) error {
						pi.OnBeforeAgentStart(func(hookCtx context.Context, ev *core.BeforeAgentStartEvent, ectx core.ExtensionContext) (*core.BeforeAgentStartEventResult, error) {
							if ev == nil {
								return nil, nil
							}
							mu.Lock()
							capturedEvents = append(capturedEvents, *ev)
							mu.Unlock()

							if ev.Prompt == "switch model with image" {
								cur := ectx.Model()
								strict := ectx.ModelRegistry().Find(string(cur.Provider), "faux-strict")
								if strict == nil {
									return nil, fmt.Errorf("missing faux-strict model")
								}
								ok, setErr := pi.SetModel(hookCtx, strict)
								if setErr != nil {
									return nil, setErr
								}
								if !ok {
									return nil, fmt.Errorf("SetModel returned false")
								}
							}

							override := ev.SystemPrompt + "\n\n[before_agent_start_override]"
							return &core.BeforeAgentStartEventResult{
								SystemPrompt: &override,
								Message: &core.BeforeAgentStartCustomMessage{
									CustomType: "hook-context",
									Content:    "injected before agent start",
									Display:    true,
								},
							}, nil
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		var sawSystemOverride bool
		var sawCustomMsg bool
		var sentImageData string

		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					switch msg := m.(type) {
					case *ai.SystemMessage:
						if msg != nil {
							if msg.Content.Text != nil && strings.Contains(*msg.Content.Text, "[before_agent_start_override]") {
								sawSystemOverride = true
							}
							for _, blk := range msg.Content.Blocks {
								if tcBlk, ok := blk.(*ai.TextContent); ok && tcBlk != nil && strings.Contains(tcBlk.Text, "[before_agent_start_override]") {
									sawSystemOverride = true
								}
							}
						}
					case *ai.UserMessage:
						if msg != nil {
							for _, blk := range msg.Content.Blocks {
								if tcBlk, ok := blk.(*ai.TextContent); ok && tcBlk != nil && strings.Contains(tcBlk.Text, "injected before agent start") {
									sawCustomMsg = true
								}
							}
						}
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("first ok"), nil), nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				for _, m := range tc.Messages {
					if um, ok := m.(*ai.UserMessage); ok && um != nil {
						for _, blk := range um.Content.Blocks {
							if img, ok := blk.(*ai.ImageContent); ok && img != nil {
								sentImageData = img.Data
							}
						}
					}
				}
				return providers.FauxAssistantMessage(providers.FauxAssistantText("image ok"), nil), nil
			}),
		})

		if err := h.Session.Prompt(ctx, "hello hook"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		if !sawSystemOverride {
			t.Fatal("expected before_agent_start SystemPrompt override to reach LLM")
		}
		if !sawCustomMsg {
			t.Fatal("expected before_agent_start injected custom message to reach LLM")
		}

		if err := h.Session.Prompt(ctx, "switch model with image", &core.PromptOptions{
			Images: []*ai.ImageContent{{Type: "image", Data: largePng, MimeType: "image/png"}},
		}); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}

		if cur := h.Session.Model(); cur == nil || cur.Id != "faux-strict" {
			t.Fatalf("expected active model to switch to faux-strict, got %+v", cur)
		}
		if sentImageData == "" {
			t.Fatal("expected normalized user image in second LLM request")
		}
		if sentImageData == largePng {
			t.Fatalf("expected user image to be resized using switched faux-strict ModelInputLimits")
		}

		mu.Lock()
		defer mu.Unlock()
		if len(capturedEvents) != 2 {
			t.Fatalf("expected 2 before_agent_start events, got %d", len(capturedEvents))
		}
		if capturedEvents[0].MessageType != "user" || capturedEvents[0].Prompt != "hello hook" {
			t.Fatalf("unexpected first before_agent_start event: %+v", capturedEvents[0])
		}
		if len(capturedEvents[1].Images) != 1 {
			t.Fatalf("expected 1 trigger image on second before_agent_start event, got %d", len(capturedEvents[1].Images))
		}
	})

	t.Run("input hook", func(t *testing.T) {
		gateStarted := make(chan struct{})
		gateRelease := make(chan struct{})
		var gateOnce sync.Once

		waitTool := &agent.AgentTool{
			Name:        "wait_gate",
			Label:       "wait_gate",
			Description: "Waits until test releases gate",
			Parameters:  typebox.Object(nil),
			Execute: func(toolCtx context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
				gateOnce.Do(func() { close(gateStarted) })
				select {
				case <-gateRelease:
				case <-toolCtx.Done():
					return agent.AgentToolResult{}, toolCtx.Err()
				}
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "gate released"}},
				}, nil
			},
		}

		type inputRecord struct {
			text              string
			source            core.InputSource
			streamingBehavior *string
		}
		var mu sync.Mutex
		var recorded []inputRecord

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Tools: []*agent.AgentTool{waitTool},
			ExtensionFactories: []core.InlineExtension{
				{
					Name: "input-hook-ext",
					Factory: func(pi core.ExtensionAPI) error {
						pi.OnInput(func(_ context.Context, ev *core.InputEvent, _ core.ExtensionContext) (*core.InputEventResult, error) {
							if ev == nil {
								return nil, nil
							}
							var sbCopy *string
							if ev.StreamingBehavior != nil {
								s := *ev.StreamingBehavior
								sbCopy = &s
							}
							mu.Lock()
							recorded = append(recorded, inputRecord{
								text:              ev.Text,
								source:            ev.Source,
								streamingBehavior: sbCopy,
							})
							mu.Unlock()

							switch ev.Text {
							case "drop me":
								return &core.InputEventResult{Action: "handled"}, nil
							case "rewrite me":
								return &core.InputEventResult{
									Action: "transform",
									Text:   "transformed: " + ev.Text,
								}, nil
							default:
								return &core.InputEventResult{Action: "continue"}, nil
							}
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("idle reply"), nil)},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlocks{providers.FauxToolCall("wait_gate", jsonx.NewObject(), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("after gate"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("after queued"), nil)},
		})

		// When idle, caller-supplied StreamingBehavior is omitted from the InputEvent.
		if err := h.Session.Prompt(ctx, "idle msg", &core.PromptOptions{StreamingBehavior: "followUp"}); err != nil {
			t.Fatalf("idle Prompt: %v", err)
		}
		if err := h.Session.Prompt(ctx, "drop me"); err != nil {
			t.Fatalf("handled Prompt: %v", err)
		}
		if h.Faux.State.CallCount != 1 {
			t.Fatalf("expected handled input not to invoke LLM (calls=%d)", h.Faux.State.CallCount)
		}

		// Start a streaming turn and send followUp/handled inputs while streaming.
		promptErrCh := make(chan error, 1)
		go func() {
			promptErrCh <- h.Session.Prompt(ctx, "start wait")
		}()

		select {
		case <-gateStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for wait_gate tool to start")
		}

		if err := h.Session.Prompt(ctx, "rewrite me", &core.PromptOptions{
			StreamingBehavior: "followUp",
			Source:            core.InputSourceRpc,
		}); err != nil {
			t.Fatalf("queued rewrite Prompt: %v", err)
		}
		if err := h.Session.Prompt(ctx, "drop me", &core.PromptOptions{
			StreamingBehavior: "steer",
			Source:            core.InputSourceRpc,
		}); err != nil {
			t.Fatalf("queued drop Prompt: %v", err)
		}

		close(gateRelease)
		if err := <-promptErrCh; err != nil {
			t.Fatalf("streaming Prompt failed: %v", err)
		}

		wantUsers := []string{"idle msg", "start wait", "transformed: rewrite me"}
		gotUsers := testkit.GetUserTexts(h)
		if !slices.Equal(gotUsers, wantUsers) {
			t.Fatalf("GetUserTexts = %v, want %v", gotUsers, wantUsers)
		}

		mu.Lock()
		recs := slices.Clone(recorded)
		mu.Unlock()
		if len(recs) != 5 {
			t.Fatalf("expected 5 input events, got %d (%+v)", len(recs), recs)
		}
		if recs[0].streamingBehavior != nil || recs[0].source != core.InputSourceInteractive {
			t.Fatalf("expected idle prompt input event with nil StreamingBehavior & interactive source, got %+v", recs[0])
		}
		if recs[3].streamingBehavior == nil || *recs[3].streamingBehavior != "followUp" || recs[3].source != core.InputSourceRpc {
			t.Fatalf("expected streaming followUp rpc input event, got %+v", recs[3])
		}
		if recs[4].streamingBehavior == nil || *recs[4].streamingBehavior != "steer" || recs[4].source != core.InputSourceRpc {
			t.Fatalf("expected streaming steer rpc input event, got %+v", recs[4])
		}
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
		type cmdCall struct {
			args string
			cwd  string
		}
		var calls []cmdCall
		var mu sync.Mutex

		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			ExtensionFactories: []core.InlineExtension{
				{
					Name: "test-cmd-ext",
					Factory: func(pi core.ExtensionAPI) error {
						pi.RegisterCommand("testcmd", core.RegisteredCommand{
							Name:        "testcmd",
							Description: tsaspStrPtr("A test command"),
							Handler: func(_ context.Context, args string, ectx core.ExtensionCommandContext) error {
								mu.Lock()
								calls = append(calls, cmdCall{args: args, cwd: ectx.Cwd()})
								mu.Unlock()
								return nil
							},
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		defer h.Cleanup()

		if err := h.Session.Prompt(ctx, "/testcmd hello world"); err != nil {
			t.Fatalf("Prompt(/testcmd): %v", err)
		}
		if err := h.Session.SendUserMessage(ctx, "/testcmd from-send", nil, &core.SendUserMessageOptions{
			ExpandPromptTemplates: true,
		}); err != nil {
			t.Fatalf("SendUserMessage(/testcmd): %v", err)
		}

		mu.Lock()
		gotCalls := slices.Clone(calls)
		mu.Unlock()
		if len(gotCalls) != 2 {
			t.Fatalf("expected 2 command invocations, got %d (%+v)", len(gotCalls), gotCalls)
		}
		wantCwd := h.TempDir
		if gotCalls[0].args != "hello world" || gotCalls[0].cwd != wantCwd {
			t.Fatalf("unexpected first command invocation: %+v (want args=%q cwd=%q)", gotCalls[0], "hello world", wantCwd)
		}
		if gotCalls[1].args != "from-send" || gotCalls[1].cwd != wantCwd {
			t.Fatalf("unexpected second command invocation: %+v (want args=%q cwd=%q)", gotCalls[1], "from-send", wantCwd)
		}
		if h.Faux.State.CallCount != 0 {
			t.Fatalf("expected extension command not to invoke LLM (calls=%d)", h.Faux.State.CallCount)
		}
		if len(h.Session.Messages()) != 0 {
			t.Fatalf("expected extension command not to append session messages, got %d", len(h.Session.Messages()))
		}
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
