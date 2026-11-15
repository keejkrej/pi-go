// Ported from packages/coding-agent/test/test-harness.ts (pi v1.0.0).

package testkit

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	thFauxProvider = "faux"
	thFauxModelId  = "faux-1"
	thFauxApi      = "anthropic-messages"
)

var thToolCallIdCounter atomic.Int64

// FauxModel returns a fresh default faux model matching test-harness.ts fauxModel.
func FauxModel() *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       thFauxModelId,
			Name:     "Faux Model",
			Api:      thFauxApi,
			Provider: ai.ProviderId(thFauxProvider),
			BaseUrl:  "http://localhost:0",
			Input:    []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
			Cost:     ai.ModelCost{},
		},
		Reasoning:     false,
		ContextWindow: 128000,
		MaxTokens:     16384,
	}
}

// FauxToolCallSpec describes a tool call included in a scripted faux response.
type FauxToolCallSpec struct {
	Id         string
	Name       string
	Args       *jsonx.Object
	ArgsFields map[string]any
}

// FauxModelOverride overrides provider/id metadata on a scripted assistant response.
type FauxModelOverride struct {
	Provider string
	Id       string
}

// FauxResponse describes a scripted assistant response for CreateFauxStreamFn.
type FauxResponse struct {
	Text       *string
	ToolCalls  []FauxToolCallSpec
	Thinking   string
	StopReason ai.StopReason
	Error      string
	Usage      *ai.Usage
	DelayMs    int64
	Model      *FauxModelOverride
}

// FauxText returns a FauxResponse containing a single text block.
func FauxText(text string) FauxResponse {
	t := text
	return FauxResponse{Text: &t}
}

// FauxError returns a FauxResponse with stopReason "error" and the given error message.
func FauxError(message string) FauxResponse {
	return FauxResponse{
		StopReason: ai.StopReasonError,
		Error:      message,
	}
}

func thBuildUsage(partial *ai.Usage) ai.Usage {
	input := 100
	output := 50
	cacheRead := 0
	cacheWrite := 0
	if partial != nil {
		if partial.Input != 0 {
			input = partial.Input
		}
		if partial.Output != 0 {
			output = partial.Output
		}
		cacheRead = partial.CacheRead
		cacheWrite = partial.CacheWrite
	}
	total := input + output + cacheRead + cacheWrite
	var cost ai.UsageCost
	if partial != nil {
		if partial.TotalTokens != 0 {
			total = partial.TotalTokens
		}
		cost = partial.Cost
	}
	return ai.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: total,
		Cost:        cost,
	}
}

func thMapToJsonObject(m map[string]any) *jsonx.Object {
	obj := jsonx.NewObject()
	if len(m) == 0 {
		return obj
	}
	raw, err := jsonx.Marshal(m)
	if err != nil {
		return obj
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return obj
	}
	if parsedObj, ok := parsed.(*jsonx.Object); ok && parsedObj != nil {
		return parsedObj
	}
	return obj
}

func thBuildAssistantMessage(resp FauxResponse) *ai.AssistantMessage {
	content := make(ai.ContentList, 0, len(resp.ToolCalls)+2)
	if resp.Thinking != "" {
		content = append(content, &ai.ThinkingContent{
			Type:     "thinking",
			Thinking: resp.Thinking,
		})
	}
	if resp.Text != nil {
		content = append(content, &ai.TextContent{
			Type: "text",
			Text: *resp.Text,
		})
	}
	for _, tc := range resp.ToolCalls {
		id := tc.Id
		if id == "" {
			id = fmt.Sprintf("faux_tc_%d", thToolCallIdCounter.Add(1))
		}
		args := tc.Args
		if args == nil {
			args = thMapToJsonObject(tc.ArgsFields)
		}
		content = append(content, &ai.ToolCall{
			Type:      "toolCall",
			Id:        id,
			Name:      tc.Name,
			Arguments: args,
		})
	}
	if len(content) == 0 && resp.Error == "" {
		content = append(content, &ai.TextContent{Type: "text", Text: ""})
	}

	stopReason := ai.StopReasonStop
	switch {
	case resp.StopReason != "":
		stopReason = resp.StopReason
	case resp.Error != "":
		stopReason = ai.StopReasonError
	case len(resp.ToolCalls) > 0:
		stopReason = ai.StopReasonToolUse
	}

	provider := ai.ProviderId(thFauxProvider)
	modelId := thFauxModelId
	if resp.Model != nil {
		if resp.Model.Provider != "" {
			provider = ai.ProviderId(resp.Model.Provider)
		}
		if resp.Model.Id != "" {
			modelId = resp.Model.Id
		}
	}

	var errMsg *string
	if resp.Error != "" {
		m := resp.Error
		errMsg = &m
	}

	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      content,
		Api:          ai.Api(thFauxApi),
		Provider:     provider,
		Model:        modelId,
		Usage:        thBuildUsage(resp.Usage),
		StopReason:   stopReason,
		ErrorMessage: errMsg,
		Timestamp:    time.Now().UnixMilli(),
	}
}

func thChunkString(text string) []string {
	if text == "" {
		return []string{""}
	}
	chunks := make([]string, 0, len(text)/3+1)
	for i := 0; i < len(text); {
		size := 3 + rand.IntN(3)
		end := min(i+size, len(text))
		chunks = append(chunks, text[i:end])
		i = end
	}
	return chunks
}

func thClonePartial(base *ai.AssistantMessage, content ai.ContentList, stopReason ai.StopReason) *ai.AssistantMessage {
	clonedContent := make(ai.ContentList, len(content))
	for i, c := range content {
		switch blk := c.(type) {
		case *ai.TextContent:
			cp := *blk
			clonedContent[i] = &cp
		case *ai.ThinkingContent:
			cp := *blk
			clonedContent[i] = &cp
		case *ai.ToolCall:
			cp := *blk
			clonedContent[i] = &cp
		default:
			clonedContent[i] = c
		}
	}
	return &ai.AssistantMessage{
		Role:         base.Role,
		Content:      clonedContent,
		Api:          base.Api,
		Provider:     base.Provider,
		Model:        base.Model,
		Usage:        base.Usage,
		StopReason:   stopReason,
		ErrorMessage: base.ErrorMessage,
		Timestamp:    base.Timestamp,
	}
}

func thStreamWithDeltas(ctx context.Context, stream *ai.AssistantMessageEventStream, message *ai.AssistantMessage) {
	liveContent := make(ai.ContentList, 0, len(message.Content))
	stream.Push(&ai.AssistantMessageEventStart{
		Type:    "start",
		Partial: thClonePartial(message, liveContent, ai.StopReasonPending),
	})

	for i, block := range message.Content {
		if ctx != nil && ctx.Err() != nil {
			aborted := thClonePartial(message, liveContent, ai.StopReasonAborted)
			msg := "aborted"
			aborted.ErrorMessage = &msg
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: ai.StopReasonAborted,
				Error:  aborted,
			})
			return
		}
		switch blk := block.(type) {
		case *ai.ThinkingContent:
			liveBlk := &ai.ThinkingContent{Type: "thinking", Thinking: ""}
			liveContent = append(liveContent, liveBlk)
			stream.Push(&ai.AssistantMessageEventThinkingStart{
				Type:         "thinking_start",
				ContentIndex: i,
				Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
			})
			for _, chunk := range thChunkString(blk.Thinking) {
				liveBlk.Thinking += chunk
				stream.Push(&ai.AssistantMessageEventThinkingDelta{
					Type:         "thinking_delta",
					ContentIndex: i,
					Delta:        chunk,
					Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
				})
			}
			stream.Push(&ai.AssistantMessageEventThinkingEnd{
				Type:         "thinking_end",
				ContentIndex: i,
				Content:      blk.Thinking,
				Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
			})

		case *ai.TextContent:
			liveBlk := &ai.TextContent{Type: "text", Text: ""}
			liveContent = append(liveContent, liveBlk)
			stream.Push(&ai.AssistantMessageEventTextStart{
				Type:         "text_start",
				ContentIndex: i,
				Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
			})
			for _, chunk := range thChunkString(blk.Text) {
				liveBlk.Text += chunk
				stream.Push(&ai.AssistantMessageEventTextDelta{
					Type:         "text_delta",
					ContentIndex: i,
					Delta:        chunk,
					Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
				})
			}
			stream.Push(&ai.AssistantMessageEventTextEnd{
				Type:         "text_end",
				ContentIndex: i,
				Content:      blk.Text,
				Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
			})

		case *ai.ToolCall:
			argsRaw, _ := jsonx.Marshal(blk.Arguments)
			liveBlk := &ai.ToolCall{
				Type:      "toolCall",
				Id:        blk.Id,
				Name:      blk.Name,
				Arguments: jsonx.NewObject(),
			}
			liveContent = append(liveContent, liveBlk)
			stream.Push(&ai.AssistantMessageEventToolcallStart{
				Type:         "toolcall_start",
				ContentIndex: i,
				Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
			})
			for _, chunk := range thChunkString(string(argsRaw)) {
				stream.Push(&ai.AssistantMessageEventToolcallDelta{
					Type:         "toolcall_delta",
					ContentIndex: i,
					Delta:        chunk,
					Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
				})
			}
			liveBlk.Arguments = blk.Arguments
			stream.Push(&ai.AssistantMessageEventToolcallEnd{
				Type:         "toolcall_end",
				ContentIndex: i,
				ToolCall:     blk,
				Partial:      thClonePartial(message, liveContent, ai.StopReasonPending),
			})
		}
	}

	if message.StopReason == ai.StopReasonPending {
		msg := "Faux response ended without a stop reason"
		errMessage := thClonePartial(message, liveContent, ai.StopReasonError)
		errMessage.ErrorMessage = &msg
		stream.Push(&ai.AssistantMessageEventError{
			Type:   "error",
			Reason: ai.StopReasonError,
			Error:  errMessage,
		})
		return
	}
	if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
		stream.Push(&ai.AssistantMessageEventError{
			Type:   "error",
			Reason: message.StopReason,
			Error:  message,
		})
		return
	}
	stream.Push(&ai.AssistantMessageEventDone{
		Type:    "done",
		Reason:  message.StopReason,
		Message: message,
	})
}

// FauxStreamFnState tracks invocations and captured contexts for CreateFauxStreamFn.
type FauxStreamFnState struct {
	mu        sync.Mutex
	callCount int
	contexts  []*ai.TranscriptContext
}

// CallCount returns the number of stream calls recorded so far.
func (s *FauxStreamFnState) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.callCount
}

// Contexts returns a shallow copy of the TranscriptContext passed to each stream call in order.
func (s *FauxStreamFnState) Contexts() []*ai.TranscriptContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*ai.TranscriptContext, len(s.contexts))
	copy(out, s.contexts)
	return out
}

// CreateFauxStreamFn returns a cycling StreamFn and inspection state for unit testing AgentSession.
func CreateFauxStreamFn(responses []FauxResponse) (agent.StreamFn, *FauxStreamFnState, error) {
	if len(responses) == 0 {
		return nil, nil, errors.New("createFauxStreamFn requires at least one response")
	}
	scripted := append([]FauxResponse(nil), responses...)
	state := &FauxStreamFnState{}

	streamFn := func(ctx context.Context, _ *ai.Model, c *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
		state.mu.Lock()
		index := state.callCount % len(scripted)
		state.callCount++
		state.contexts = append(state.contexts, c)
		state.mu.Unlock()

		resp := scripted[index]
		message := thBuildAssistantMessage(resp)
		stream := ai.NewAssistantMessageEventStream()

		go func() {
			if resp.DelayMs > 0 {
				timer := time.NewTimer(time.Duration(resp.DelayMs) * time.Millisecond)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					aborted := thClonePartial(message, nil, ai.StopReasonAborted)
					msg := "aborted"
					aborted.ErrorMessage = &msg
					stream.Push(&ai.AssistantMessageEventError{
						Type:   "error",
						Reason: ai.StopReasonAborted,
						Error:  aborted,
					})
					return
				}
			}
			thStreamWithDeltas(ctx, stream, message)
		}()

		return stream, nil
	}

	return streamFn, state, nil
}

// HarnessOptions configures CreateHarness.
type HarnessOptions struct {
	Responses         []FauxResponse
	Model             *ai.Model
	ContextWindow     int
	Settings          *core.Settings
	SystemPrompt      *string
	Tools             []*agent.AgentTool
	BaseToolsOverride map[string]*agent.AgentTool
	ResourceLoader    core.ResourceLoader
}

// Harness bundles an in-memory AgentSession, faux stream state, event capture, and cleanup.
type Harness struct {
	Session         *core.AgentSession
	Agent           *agent.Agent
	SessionManager  *core.SessionManager
	SettingsManager *core.SettingsManager
	Faux            *FauxStreamFnState
	TempDir         string
	Cleanup         func()

	mu     sync.Mutex
	events []core.AgentSessionEvent
}

// Events returns a snapshot of all events emitted by the session in order.
func (h *Harness) Events() []core.AgentSessionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]core.AgentSessionEvent, len(h.events))
	copy(out, h.events)
	return out
}

// EventsOfType filters captured events by AgentSessionEventType().
func (h *Harness) EventsOfType(eventType string) []core.AgentSessionEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []core.AgentSessionEvent
	for _, ev := range h.events {
		if ev != nil && ev.AgentSessionEventType() == eventType {
			out = append(out, ev)
		}
	}
	return out
}

// ResetEvents clears captured events without unsubscribing.
func (h *Harness) ResetEvents() {
	h.mu.Lock()
	h.events = nil
	h.mu.Unlock()
}

// CreateHarness constructs a fully wired in-memory AgentSession backed by a faux StreamFn.
func CreateHarness(ctx context.Context, options ...*HarnessOptions) (*Harness, error) {
	var opts HarnessOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	tempDir, err := os.MkdirTemp("", "pi-harness-*")
	if err != nil {
		return nil, err
	}
	var modelRuntime *core.ModelRuntime
	cleanupDir := func() {
		if modelRuntime != nil {
			modelRuntime.Close()
		}
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}

	model := FauxModel()
	if opts.Model != nil {
		cloned := *opts.Model
		model = &cloned
	}
	if opts.ContextWindow > 0 {
		model.ContextWindow = opts.ContextWindow
	}

	responses := opts.Responses
	if len(responses) == 0 {
		responses = []FauxResponse{FauxText("ok")}
	}
	streamFn, fauxState, err := CreateFauxStreamFn(responses)
	if err != nil {
		cleanupDir()
		return nil, err
	}

	systemPrompt := "You are a test assistant."
	if opts.SystemPrompt != nil {
		systemPrompt = *opts.SystemPrompt
	}
	tools := opts.Tools
	if tools == nil {
		tools = []*agent.AgentTool{}
	}

	ag := agent.NewAgent(&agent.AgentOptions{
		GetApiKey: func(context.Context, string) (string, error) {
			return "faux-key", nil
		},
		InitialState: &agent.AgentInitialState{
			Model:        model,
			SystemPrompt: systemPrompt,
			Tools:        tools,
		},
		StreamFn: streamFn,
		ConvertToLlm: func(_ context.Context, msgs []agent.AgentMessage) ([]ai.Message, error) {
			return core.ConvertToLlm(msgs), nil
		},
	})

	sessionManager := core.SessionManagerInMemory()
	settingsManager := core.SettingsManagerCreate(tempDir, tempDir)
	if opts.Settings != nil {
		settingsManager.ApplyOverrides(opts.Settings)
	}

	fauxKey := "faux-key"
	authStorage := core.AuthStorageInMemory(map[string]ai.Credential{
		string(model.Provider): &ai.ApiKeyCredential{
			Type: ai.AuthTypeApiKey,
			Key:  &fauxKey,
		},
	})
	modelRegistry, err := CreateInMemoryModelRegistry(ctx, authStorage)
	if err != nil {
		cleanupDir()
		return nil, err
	}
	modelRuntime, err = GetModelRuntime(modelRegistry)
	if err != nil {
		cleanupDir()
		return nil, err
	}
	apiStr := model.Api
	baseUrl := model.BaseUrl
	if err := modelRegistry.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
		BaseUrl: &baseUrl,
		Api:     &apiStr,
		Models: []core.ProviderModelConfig{
			&core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:      model.Id,
					Name:    model.Name,
					Api:     &apiStr,
					BaseUrl: &baseUrl,
					Input:   model.Input,
					Cost:    model.Cost,
				},
				Reasoning:     model.Reasoning,
				ContextWindow: model.ContextWindow,
				MaxTokens:     model.MaxTokens,
			},
		},
	}); err != nil {
		cleanupDir()
		return nil, err
	}

	resourceLoader := opts.ResourceLoader
	if resourceLoader == nil {
		resourceLoader = CreateTestResourceLoader()
	}

	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:             ag,
		SessionManager:    sessionManager,
		SettingsManager:   settingsManager,
		Cwd:               tempDir,
		ModelRuntime:      modelRuntime,
		ResourceLoader:    resourceLoader,
		BaseToolsOverride: opts.BaseToolsOverride,
	})

	h := &Harness{
		Session:         session,
		Agent:           ag,
		SessionManager:  sessionManager,
		SettingsManager: settingsManager,
		Faux:            fauxState,
		TempDir:         tempDir,
	}
	unsub := session.Subscribe(func(ev core.AgentSessionEvent) {
		h.mu.Lock()
		h.events = append(h.events, ev)
		h.mu.Unlock()
	})
	h.Cleanup = func() {
		if unsub != nil {
			unsub()
		}
		session.Dispose()
		cleanupDir()
	}

	return h, nil
}
