// Ported from packages/ai/src/providers/faux.ts (pi v1.5.2).

package providers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	// fauxDefaultApi is the randomId prefix when Api is omitted.
	fauxDefaultApi = "faux"
	// fauxDefaultProvider is the provider id when Provider is omitted.
	fauxDefaultProvider     = "faux"
	fauxDefaultModelId      = "faux-1"
	fauxDefaultModelName    = "Faux Model"
	fauxDefaultBaseUrl      = "http://localhost:0"
	fauxDefaultMinTokenSize = 3
	fauxDefaultMaxTokenSize = 5
)

// fauxDefaultUsage is DEFAULT_USAGE: zero token counters and zero cost rates.
var fauxDefaultUsage = ai.Usage{}

// FauxModelDefinition is one model a faux provider serves.
type FauxModelDefinition struct {
	Id            string               `json:"id"`
	Name          *string              `json:"name,omitzero"`
	Reasoning     *bool                `json:"reasoning,omitzero"`
	Input         []string             `json:"input,omitzero"`
	InputLimits   *ai.ModelInputLimits `json:"inputLimits,omitzero"`
	Cost          *ai.ModelCostRates   `json:"cost,omitzero"`
	ContextWindow *int                 `json:"contextWindow,omitzero"`
	MaxTokens     *int                 `json:"maxTokens,omitzero"`
}

// FauxContentBlock is a text, thinking, or tool-call block.
// Values are *TextContent, *ThinkingContent, or *ToolCall.
// This alias cannot exclude image blocks; those variants live on ai.Content.
type FauxContentBlock = ai.Content

// FauxAssistantContent is the content argument of FauxAssistantMessage:
// a string, one block, or several blocks.
type FauxAssistantContent interface{ isFauxAssistantContent() }

// FauxAssistantText is the string form of faux assistant content.
type FauxAssistantText string

func (FauxAssistantText) isFauxAssistantContent() {}

// FauxAssistantBlock is one faux content block.
type FauxAssistantBlock struct {
	Block ai.Content
}

func (FauxAssistantBlock) isFauxAssistantContent() {}

// FauxAssistantBlocks is faux content blocks in order.
type FauxAssistantBlocks []ai.Content

func (FauxAssistantBlocks) isFauxAssistantContent() {}

// FauxToolCallOptions is the options argument of FauxToolCall. Nil means {}.
type FauxToolCallOptions struct {
	Id *string `json:"id,omitzero"`
}

// FauxAssistantMessageOptions is the options argument of FauxAssistantMessage.
// Nil means {}. Timestamp is epoch milliseconds.
type FauxAssistantMessageOptions struct {
	StopReason   *ai.StopReason     `json:"stopReason,omitzero"`
	Deferred     *ai.DeferredHandle `json:"deferred,omitzero"`
	ErrorMessage *string            `json:"errorMessage,omitzero"`
	ResponseId   *string            `json:"responseId,omitzero"`
	Timestamp    *int64             `json:"timestamp,omitzero"`
}

// FauxProviderState is the mutable counters of one faux provider. Shared across calls.
type FauxProviderState struct {
	mu                 sync.Mutex
	CallCount          int                  `json:"callCount"`
	DeferredFetchCount int                  `json:"deferredFetchCount"`
	CancelledDeferred  []*ai.DeferredHandle `json:"cancelledDeferred"`
}

// FauxResponseFactory builds the assistant message for one call.
// ctx is SimpleStreamOptions.signal. A factory error becomes an error assistant message.
type FauxResponseFactory func(ctx context.Context, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions, state *FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error)

// FauxResponseStep is one queued response: *FauxResponseMessage or FauxResponseFactory.
type FauxResponseStep interface{ isFauxResponseStep() }

// FauxResponseMessage is a queued assistant message.
type FauxResponseMessage struct {
	Message *ai.AssistantMessage
}

func (FauxResponseMessage) isFauxResponseStep() {}

func (FauxResponseFactory) isFauxResponseStep() {}

// FauxDeferredOptions is RegisterFauxProviderOptions.Deferred.
// PendingFetches is how many fetches return the original handle before the scripted response is ready.
type FauxDeferredOptions struct {
	PendingFetches *int   `json:"pendingFetches,omitzero"`
	PollAfterMs    *int64 `json:"pollAfterMs,omitzero"`
}

// FauxTokenSizeOptions is RegisterFauxProviderOptions.TokenSize.
type FauxTokenSizeOptions struct {
	Min *int `json:"min,omitzero"`
	Max *int `json:"max,omitzero"`
}

// RegisterFauxProviderOptions configures a faux provider. Nil means {}.
type RegisterFauxProviderOptions struct {
	Api             *string               `json:"api,omitzero"`
	Provider        *string               `json:"provider,omitzero"`
	Models          []FauxModelDefinition `json:"models,omitzero"`
	Deferred        *FauxDeferredOptions  `json:"deferred,omitzero"`
	TokensPerSecond *float64              `json:"tokensPerSecond,omitzero"`
	TokenSize       *FauxTokenSizeOptions `json:"tokenSize,omitzero"`
}

// FauxProviderRegistration is the scripted-provider handle compat returns.
// Function fields are copied from FauxCore; Unregister is supplied by the caller.
// GetModel is getModel(). GetModelId is getModel(modelId).
type FauxProviderRegistration struct {
	Api                     string                             `json:"api"`
	Models                  []*ai.Model                        `json:"models"`
	GetModel                func() *ai.Model                   `json:"-"`
	GetModelId              func(modelId string) *ai.Model     `json:"-"`
	State                   *FauxProviderState                 `json:"state"`
	SetResponses            func(responses []FauxResponseStep) `json:"-"`
	AppendResponses         func(responses []FauxResponseStep) `json:"-"`
	GetPendingResponseCount func() int                         `json:"-"`
	Unregister              func()                             `json:"-"`
}

// FauxProviderHandle is the provider returned by FauxProvider.
// Provider is the ai.Provider to pass to Models.SetProvider.
type FauxProviderHandle struct {
	mu       sync.Mutex
	core     *FauxCore
	Provider *ai.Provider       `json:"provider"`
	Api      string             `json:"api"`
	Models   []*ai.Model        `json:"models"`
	State    *FauxProviderState `json:"state"`
}

// FauxCore is the scripted stream implementation shared by FauxProvider and compat.
// Provider is the provider id string, not an *ai.Provider. Shared across calls.
type FauxCore struct {
	mu sync.Mutex

	Api      string             `json:"api"`
	Provider string             `json:"provider"`
	Models   []*ai.Model        `json:"models"`
	State    *FauxProviderState `json:"state"`

	minTokenSize      int
	maxTokenSize      int
	tokensPerSecond   *float64
	pendingResponses  []FauxResponseStep
	promptCache       map[string][]string
	deferredResponses map[string]*fauxDeferredEntry
	deferred          *FauxDeferredOptions
}

// fauxDeferredEntry is one accepted deferred response.
type fauxDeferredEntry struct {
	Handle         *ai.DeferredHandle
	Step           FauxResponseStep
	Context        *ai.TranscriptContext
	Options        *ai.SimpleStreamOptions
	Model          *ai.Model
	PendingFetches int
	Cancelled      bool
	Final          *ai.AssistantMessage
}

// FauxText returns a text block.
func FauxText(text string) *ai.TextContent {
	return &ai.TextContent{Type: "text", Text: text}
}

// FauxThinking returns a thinking block.
func FauxThinking(thinking string) *ai.ThinkingContent {
	return &ai.ThinkingContent{Type: "thinking", Thinking: thinking}
}

// FauxToolCall returns a tool-call block. Nil options means {}.
// arguments is the tool-call arguments object.
func FauxToolCall(name string, arguments *jsonx.Object, options *FauxToolCallOptions) *ai.ToolCall {
	id := ""
	if options != nil && options.Id != nil {
		id = *options.Id
	} else {
		id = fauxRandomId("tool")
	}
	return &ai.ToolCall{
		Type:      "toolCall",
		Id:        id,
		Name:      name,
		Arguments: arguments,
	}
}

// FauxAssistantMessage builds an assistant message tagged with the faux defaults.
// Nil options means {}.
func FauxAssistantMessage(content FauxAssistantContent, options *FauxAssistantMessageOptions) *ai.AssistantMessage {
	stopReason := ai.StopReasonStop
	timestamp := js.DateNow()
	var deferred *ai.DeferredHandle
	var errorMessage *string
	var responseId *string
	if options != nil {
		if options.StopReason != nil {
			stopReason = *options.StopReason
		}
		if options.Deferred != nil {
			deferred = options.Deferred
		}
		if options.ErrorMessage != nil {
			msg := *options.ErrorMessage
			errorMessage = &msg
		}
		if options.ResponseId != nil {
			id := *options.ResponseId
			responseId = &id
		}
		if options.Timestamp != nil {
			timestamp = *options.Timestamp
		}
	}
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      ai.ContentList(fauxNormalizeAssistantContent(content)),
		Api:          ai.Api(fauxDefaultApi),
		Provider:     ai.ProviderId(fauxDefaultProvider),
		Model:        fauxDefaultModelId,
		Usage:        fauxDefaultUsage,
		StopReason:   stopReason,
		Deferred:     deferred,
		ErrorMessage: errorMessage,
		ResponseId:   responseId,
		Timestamp:    timestamp,
	}
}

func fauxNormalizeAssistantContent(content FauxAssistantContent) []ai.Content {
	switch v := content.(type) {
	case nil:
		return []ai.Content{}
	case FauxAssistantText:
		return []ai.Content{FauxText(string(v))}
	case *FauxAssistantText:
		if v == nil {
			return []ai.Content{}
		}
		return []ai.Content{FauxText(string(*v))}
	case FauxAssistantBlock:
		if v.Block == nil {
			return []ai.Content{}
		}
		return []ai.Content{v.Block}
	case *FauxAssistantBlock:
		if v == nil || v.Block == nil {
			return []ai.Content{}
		}
		return []ai.Content{v.Block}
	case FauxAssistantBlocks:
		if v == nil {
			return []ai.Content{}
		}
		return slices.Clone(v)
	case *FauxAssistantBlocks:
		if v == nil || *v == nil {
			return []ai.Content{}
		}
		return slices.Clone(*v)
	default:
		return []ai.Content{}
	}
}

func fauxEstimateTokens(text string) int {
	return (js.Len(text) + 3) / 4
}

func fauxRandomId(prefix string) string {
	return fmt.Sprintf("%s:%d:%s", prefix, js.DateNow(), strconv.FormatUint(rand.Uint64(), 36))
}

// text non-nil selects the string form, including "". Indexes elsewhere in this file that
// mirror JavaScript string indexes are UTF-16 code units.
func fauxContentToText(text *string, blocks []ai.Content) string {
	if text != nil {
		return *text
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case *ai.TextContent:
			if b != nil {
				parts = append(parts, b.Text)
			}
		case *ai.ImageContent:
			if b != nil {
				parts = append(parts, fmt.Sprintf("[image:%s:%d]", b.MimeType, js.Len(b.Data)))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func fauxAssistantContentToText(content []ai.Content) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch b := block.(type) {
		case *ai.TextContent:
			if b != nil {
				parts = append(parts, b.Text)
			}
		case *ai.ThinkingContent:
			if b != nil {
				parts = append(parts, b.Thinking)
			}
		case *ai.ToolCall:
			if b != nil {
				parts = append(parts, b.Name+":"+jsonx.MustStringify(b.Arguments))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func fauxToolResultToText(message *ai.ToolResultMessage) string {
	if message == nil {
		return ""
	}
	parts := make([]string, 0, 1+len(message.Content))
	parts = append(parts, message.ToolName)
	for _, block := range message.Content {
		parts = append(parts, fauxContentToText(nil, []ai.Content{block}))
	}
	return strings.Join(parts, "\n")
}

func fauxMessageToText(message ai.Message) string {
	if message == nil {
		return ""
	}
	switch m := message.(type) {
	case *ai.SystemMessage:
		if m == nil {
			return ""
		}
		var parts []string
		if s := ai.GetSystemMessageText(m); len(s) > 0 {
			parts = append(parts, s)
		}
		for _, tool := range m.ToolsRemoved {
			parts = append(parts, "tool-:"+jsonx.MustStringify(tool))
		}
		for _, tool := range m.ToolsAdded {
			parts = append(parts, "tool+:"+jsonx.MustStringify(tool))
		}
		return strings.Join(parts, "\n")
	case *ai.UserMessage:
		if m == nil {
			return ""
		}
		return fauxContentToText(m.Content.Text, m.Content.Blocks)
	case *ai.AssistantMessage:
		if m == nil {
			return ""
		}
		return fauxAssistantContentToText(m.Content)
	case *ai.ToolResultMessage:
		return fauxToolResultToText(m)
	default:
		return ""
	}
}

func fauxJoinedLength(messages []string, count int) int {
	length := 0
	if count > 0 {
		length = (count - 1) * 2
	}
	for i := 0; i < count; i++ {
		length += js.Len(messages[i])
	}
	return length
}

// fauxCommonPrefixLength returns a JavaScript string index (UTF-16 code units).
func fauxCommonPrefixLength(a, b string) int {
	ua := js.ToUTF16(a)
	ub := js.ToUTF16(b)
	n := min(len(ua), len(ub))
	i := 0
	for i < n && ua[i] == ub[i] {
		i++
	}
	return i
}

func fauxCommonPromptPrefixLength(previous, current []string) int {
	index := 0
	for index < len(previous) && index < len(current) && previous[index] == current[index] {
		index++
	}
	rest := func(messages []string) string {
		if index == len(messages) {
			return ""
		}
		prefix := ""
		if index > 0 {
			prefix = "\n\n"
		}
		return prefix + strings.Join(messages[index:], "\n\n")
	}
	return fauxJoinedLength(previous, index) + fauxCommonPrefixLength(rest(previous), rest(current))
}

func fauxWithUsageEstimate(mu *sync.Mutex, message *ai.AssistantMessage, transcript *ai.TranscriptContext, options *ai.StreamOptions, promptCache map[string][]string) *ai.AssistantMessage {
	var msgs []ai.Message
	if transcript != nil {
		msgs = transcript.Messages
	}
	prompt := make([]string, len(msgs))
	for i, msg := range msgs {
		role := ""
		if msg != nil {
			role = msg.GetRole()
		}
		prompt[i] = role + ":" + fauxMessageToText(msg)
	}
	promptLength := fauxJoinedLength(prompt, len(prompt))
	promptTokens := (promptLength + 3) / 4
	outputTokens := fauxEstimateTokens(fauxAssistantContentToText(message.Content))
	input := promptTokens
	cacheRead := 0
	cacheWrite := 0

	var sessionId string
	if options != nil && options.SessionId != nil {
		sessionId = *options.SessionId
	}
	var cacheRetention ai.CacheRetention
	if options != nil && options.CacheRetention != nil {
		cacheRetention = *options.CacheRetention
	}

	if sessionId != "" && cacheRetention != ai.CacheRetentionNone {
		if mu != nil {
			mu.Lock()
		}
		previousPrompt, ok := promptCache[sessionId]
		if ok {
			cachedChars := fauxCommonPromptPrefixLength(previousPrompt, prompt)
			cacheRead = (cachedChars + 3) / 4
			cacheWrite = max(0, promptTokens-cacheRead)
		} else {
			cacheWrite = promptTokens
		}
		// Every prompt token is either read from or written to the simulated cache.
		input = 0
		promptCache[sessionId] = prompt
		if mu != nil {
			mu.Unlock()
		}
	}

	out := fauxShallowCopyAssistant(message)
	out.Usage = ai.Usage{
		Input:       input,
		Output:      outputTokens,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + outputTokens + cacheRead + cacheWrite,
		Cost:        ai.UsageCost{},
	}
	return out
}

// fauxSplitStringByTokenSize slices text with JavaScript string indexes (UTF-16 code units).
func fauxSplitStringByTokenSize(text string, minTokenSize, maxTokenSize int) []string {
	n := js.Len(text)
	var chunks []string
	for index := 0; index < n; {
		tokenSize := minTokenSize + rand.IntN(maxTokenSize-minTokenSize+1)
		charSize := max(1, tokenSize*4)
		end := min(n, index+charSize)
		chunks = append(chunks, js.Slice(text, index, end))
		index += charSize
	}
	if len(chunks) == 0 {
		return []string{""}
	}
	return chunks
}

func fauxClonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func fauxCloneContentBlock(block ai.Content) ai.Content {
	switch b := block.(type) {
	case nil:
		return nil
	case *ai.TextContent:
		if b == nil {
			return nil
		}
		cp := *b
		cp.TextSignature = fauxClonePtr(b.TextSignature)
		return &cp
	case *ai.ThinkingContent:
		if b == nil {
			return nil
		}
		cp := *b
		cp.ThinkingSignature = fauxClonePtr(b.ThinkingSignature)
		cp.Redacted = fauxClonePtr(b.Redacted)
		return &cp
	case *ai.ImageContent:
		if b == nil {
			return nil
		}
		cp := *b
		return &cp
	case *ai.ToolCall:
		if b == nil {
			return nil
		}
		cp := *b
		if b.Arguments != nil {
			cp.Arguments = b.Arguments.Clone()
		}
		cp.ThoughtSignature = fauxClonePtr(b.ThoughtSignature)
		cp.Namespace = fauxClonePtr(b.Namespace)
		cp.ArgumentsError = fauxClonePtr(b.ArgumentsError)
		return &cp
	case *ai.UnknownContent:
		if b == nil {
			return nil
		}
		var raw *jsonx.Object
		if b.Raw != nil {
			raw = b.Raw.Clone()
		}
		return &ai.UnknownContent{Raw: raw}
	default:
		return block
	}
}

func fauxCloneDeferredHandle(h *ai.DeferredHandle) *ai.DeferredHandle {
	if h == nil {
		return nil
	}
	cp := *h
	cp.ExpiresAt = fauxClonePtr(h.ExpiresAt)
	cp.PollAfterMs = fauxClonePtr(h.PollAfterMs)
	if val, ok := h.Data.Get(); ok {
		cp.Data = jsonx.Some(jsonx.CloneValue(val))
	}
	return &cp
}

func fauxShallowCopyAssistant(m *ai.AssistantMessage) *ai.AssistantMessage {
	if m == nil {
		return nil
	}
	return &ai.AssistantMessage{
		Role:                  m.Role,
		Content:               m.Content,
		Api:                   m.Api,
		Provider:              m.Provider,
		Model:                 m.Model,
		ResponseModel:         m.ResponseModel,
		ResponseId:            m.ResponseId,
		ProviderThinkingLevel: m.ProviderThinkingLevel,
		ThinkingLevel:         m.ThinkingLevel,
		Diagnostics:           m.Diagnostics,
		Usage:                 m.Usage,
		StopReason:            m.StopReason,
		Deferred:              m.Deferred,
		ErrorMessage:          m.ErrorMessage,
		RawStopReason:         m.RawStopReason,
		EndTurn:               m.EndTurn,
		Timestamp:             m.Timestamp,
		DurationMs:            m.DurationMs,
	}
}

func fauxSnapshotAssistant(m *ai.AssistantMessage) *ai.AssistantMessage {
	if m == nil {
		return nil
	}
	cp := fauxShallowCopyAssistant(m)
	blocks := make(ai.ContentList, len(m.Content))
	for i, b := range m.Content {
		blocks[i] = fauxCloneContentBlock(b)
	}
	cp.Content = blocks
	return cp
}

func fauxCloneMessage(message *ai.AssistantMessage, apiName, provider, modelId string) *ai.AssistantMessage {
	if message == nil {
		return nil
	}
	clonedContent := make(ai.ContentList, len(message.Content))
	for i, b := range message.Content {
		clonedContent[i] = fauxCloneContentBlock(b)
	}
	var diagnostics []ai.AssistantMessageDiagnostic
	if message.Diagnostics != nil {
		diagnostics = make([]ai.AssistantMessageDiagnostic, len(message.Diagnostics))
		for i, d := range message.Diagnostics {
			diag := d
			if d.Error != nil {
				errCopy := *d.Error
				errCopy.Name = fauxClonePtr(d.Error.Name)
				errCopy.Stack = fauxClonePtr(d.Error.Stack)
				errCopy.Code = jsonx.CloneValue(d.Error.Code)
				diag.Error = &errCopy
			}
			if d.Details != nil {
				diag.Details = d.Details.Clone()
			}
			diagnostics[i] = diag
		}
	}
	ts := message.Timestamp
	if ts == 0 {
		ts = js.DateNow()
	}
	usage := message.Usage
	usage.CacheWrite1h = fauxClonePtr(message.Usage.CacheWrite1h)
	usage.Reasoning = fauxClonePtr(message.Usage.Reasoning)
	return &ai.AssistantMessage{
		Role:                  message.Role,
		Content:               clonedContent,
		Api:                   ai.Api(apiName),
		Provider:              ai.ProviderId(provider),
		Model:                 modelId,
		ResponseModel:         fauxClonePtr(message.ResponseModel),
		ResponseId:            fauxClonePtr(message.ResponseId),
		ProviderThinkingLevel: fauxClonePtr(message.ProviderThinkingLevel),
		ThinkingLevel:         fauxClonePtr(message.ThinkingLevel),
		Diagnostics:           diagnostics,
		Usage:                 usage,
		StopReason:            message.StopReason,
		Deferred:              fauxCloneDeferredHandle(message.Deferred),
		ErrorMessage:          fauxClonePtr(message.ErrorMessage),
		RawStopReason:         fauxClonePtr(message.RawStopReason),
		EndTurn:               fauxClonePtr(message.EndTurn),
		Timestamp:             ts,
		DurationMs:            fauxClonePtr(message.DurationMs),
	}
}

func fauxCreateDeferredMessage(model *ai.Model, handle *ai.DeferredHandle) *ai.AssistantMessage {
	var apiName ai.Api
	var provider ai.ProviderId
	var modelId string
	if model != nil {
		apiName = ai.Api(model.Api)
		provider = model.Provider
		modelId = model.Id
	}
	return &ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{},
		Api:        apiName,
		Provider:   provider,
		Model:      modelId,
		Usage:      fauxDefaultUsage,
		StopReason: ai.StopReasonDeferred,
		Deferred:   handle,
		Timestamp:  js.DateNow(),
	}
}

func fauxCreateErrorMessage(err any, apiName, provider, modelId string) *ai.AssistantMessage {
	msg := ai.FormatThrownValue(err)
	return &ai.AssistantMessage{
		Role:         "assistant",
		Content:      ai.ContentList{},
		Api:          ai.Api(apiName),
		Provider:     ai.ProviderId(provider),
		Model:        modelId,
		Usage:        fauxDefaultUsage,
		StopReason:   ai.StopReasonError,
		ErrorMessage: &msg,
		Timestamp:    js.DateNow(),
	}
}

func fauxCreateAbortedMessage(partial *ai.AssistantMessage) *ai.AssistantMessage {
	out := fauxSnapshotAssistant(partial)
	msg := "Request was aborted"
	out.StopReason = ai.StopReasonAborted
	out.ErrorMessage = &msg
	out.Timestamp = js.DateNow()
	return out
}

func fauxScheduleChunk(ctx context.Context, chunk string, tokensPerSecond *float64) error {
	if tokensPerSecond == nil || *tokensPerSecond <= 0 {
		runtime.Gosched()
		return nil
	}
	delayMs := (float64(fauxEstimateTokens(chunk)) / *tokensPerSecond) * 1000
	dur := time.Duration(delayMs * float64(time.Millisecond))
	if dur < time.Millisecond {
		dur = time.Millisecond
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func fauxStreamWithDeltas(ctx context.Context, stream *ai.AssistantMessageEventStream, message *ai.AssistantMessage, minTokenSize, maxTokenSize int, tokensPerSecond *float64) error {
	partial := fauxShallowCopyAssistant(message)
	partial.Content = ai.ContentList{}
	partial.StopReason = ai.StopReasonPending

	if ctx != nil && ctx.Err() != nil {
		aborted := fauxCreateAbortedMessage(partial)
		stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: aborted})
		stream.End(aborted)
		return nil
	}

	stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: fauxSnapshotAssistant(partial)})

	for index, rawBlock := range message.Content {
		if ctx != nil && ctx.Err() != nil {
			aborted := fauxCreateAbortedMessage(partial)
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: aborted})
			stream.End(aborted)
			return nil
		}

		switch block := rawBlock.(type) {
		case *ai.ThinkingContent:
			if block == nil {
				continue
			}
			thinkingBlock := &ai.ThinkingContent{Type: "thinking", Thinking: ""}
			partial.Content = append(partial.Content, thinkingBlock)
			stream.Push(&ai.AssistantMessageEventThinkingStart{
				Type:         "thinking_start",
				ContentIndex: index,
				Partial:      fauxSnapshotAssistant(partial),
			})
			for _, chunk := range fauxSplitStringByTokenSize(block.Thinking, minTokenSize, maxTokenSize) {
				_ = fauxScheduleChunk(ctx, chunk, tokensPerSecond)
				if ctx != nil && ctx.Err() != nil {
					aborted := fauxCreateAbortedMessage(partial)
					stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: aborted})
					stream.End(aborted)
					return nil
				}
				thinkingBlock.Thinking += chunk
				stream.Push(&ai.AssistantMessageEventThinkingDelta{
					Type:         "thinking_delta",
					ContentIndex: index,
					Delta:        chunk,
					Partial:      fauxSnapshotAssistant(partial),
				})
			}
			stream.Push(&ai.AssistantMessageEventThinkingEnd{
				Type:         "thinking_end",
				ContentIndex: index,
				Content:      block.Thinking,
				Partial:      fauxSnapshotAssistant(partial),
			})

		case *ai.TextContent:
			if block == nil {
				continue
			}
			textBlock := &ai.TextContent{Type: "text", Text: ""}
			partial.Content = append(partial.Content, textBlock)
			stream.Push(&ai.AssistantMessageEventTextStart{
				Type:         "text_start",
				ContentIndex: index,
				Partial:      fauxSnapshotAssistant(partial),
			})
			for _, chunk := range fauxSplitStringByTokenSize(block.Text, minTokenSize, maxTokenSize) {
				_ = fauxScheduleChunk(ctx, chunk, tokensPerSecond)
				if ctx != nil && ctx.Err() != nil {
					aborted := fauxCreateAbortedMessage(partial)
					stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: aborted})
					stream.End(aborted)
					return nil
				}
				textBlock.Text += chunk
				stream.Push(&ai.AssistantMessageEventTextDelta{
					Type:         "text_delta",
					ContentIndex: index,
					Delta:        chunk,
					Partial:      fauxSnapshotAssistant(partial),
				})
			}
			stream.Push(&ai.AssistantMessageEventTextEnd{
				Type:         "text_end",
				ContentIndex: index,
				Content:      block.Text,
				Partial:      fauxSnapshotAssistant(partial),
			})

		case *ai.ToolCall:
			if block == nil {
				continue
			}
			tcBlock := &ai.ToolCall{
				Type:      "toolCall",
				Id:        block.Id,
				Name:      block.Name,
				Arguments: jsonx.NewObject(),
			}
			partial.Content = append(partial.Content, tcBlock)
			stream.Push(&ai.AssistantMessageEventToolcallStart{
				Type:         "toolcall_start",
				ContentIndex: index,
				Partial:      fauxSnapshotAssistant(partial),
			})
			// Partial arguments come from the same parser real providers use, so consumers see them grow.
			toolArguments := ai.NewToolCallArgumentsStream()
			argsJSON := "{}"
			if block.Arguments != nil {
				argsJSON = jsonx.MustStringify(block.Arguments)
			}
			for _, chunk := range fauxSplitStringByTokenSize(argsJSON, minTokenSize, maxTokenSize) {
				_ = fauxScheduleChunk(ctx, chunk, tokensPerSecond)
				if ctx != nil && ctx.Err() != nil {
					aborted := fauxCreateAbortedMessage(partial)
					stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: aborted})
					stream.End(aborted)
					return nil
				}
				tcBlock.Arguments = toolArguments.Append(chunk)
				stream.Push(&ai.AssistantMessageEventToolcallDelta{
					Type:         "toolcall_delta",
					ContentIndex: index,
					Delta:        chunk,
					Partial:      fauxSnapshotAssistant(partial),
				})
			}
			tcBlock.Arguments = block.Arguments
			stream.Push(&ai.AssistantMessageEventToolcallEnd{
				Type:         "toolcall_end",
				ContentIndex: index,
				ToolCall:     fauxCloneContentBlock(block).(*ai.ToolCall),
				Partial:      fauxSnapshotAssistant(partial),
			})
		}
	}

	if message.StopReason == ai.StopReasonPending || message.StopReason == "" {
		return errors.New("Faux response ended without a stop reason")
	}
	if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
		stream.Push(&ai.AssistantMessageEventError{
			Type:   "error",
			Reason: message.StopReason,
			Error:  message,
		})
		stream.End(message)
		return nil
	}

	stream.Push(&ai.AssistantMessageEventDone{
		Type:    "done",
		Reason:  message.StopReason,
		Message: message,
	})
	stream.End(message)
	return nil
}

func fauxHasDeferred(opts *ai.SimpleStreamOptions) bool {
	if opts == nil || opts.Deferred.V == nil {
		return false
	}
	if b, ok := opts.Deferred.V.(ai.SimpleStreamDeferredBool); ok {
		return bool(b)
	}
	return true
}

func (c *FauxCore) resolveResponse(
	ctx context.Context,
	step FauxResponseStep,
	transcript *ai.TranscriptContext,
	streamOpts *ai.SimpleStreamOptions,
	requestModel *ai.Model,
) (*ai.AssistantMessage, error) {
	var resolved *ai.AssistantMessage
	switch s := step.(type) {
	case FauxResponseFactory:
		if s == nil {
			return nil, errors.New("faux response factory is nil")
		}
		var err error
		resolved, err = s(ctx, transcript, streamOpts, c.State, requestModel)
		if err != nil {
			return nil, err
		}
	case FauxResponseMessage:
		resolved = s.Message
	case *FauxResponseMessage:
		if s != nil {
			resolved = s.Message
		}
	default:
		return nil, fmt.Errorf("unknown faux response step: %T", step)
	}
	if resolved == nil {
		return nil, errors.New("faux response message is nil")
	}
	var baseOpts *ai.StreamOptions
	if streamOpts != nil {
		baseOpts = &streamOpts.StreamOptions
	}
	modelId := ""
	if requestModel != nil {
		modelId = requestModel.Id
	}
	return fauxWithUsageEstimate(
		&c.mu,
		fauxCloneMessage(resolved, c.Api, c.Provider, modelId),
		transcript,
		baseOpts,
		c.promptCache,
	), nil
}

// CreateFauxCore builds the scripted stream core. Nil options means {}.
// It does not register an ai.Provider and does not call a network provider.
func CreateFauxCore(options *RegisterFauxProviderOptions) *FauxCore {
	if options == nil {
		options = &RegisterFauxProviderOptions{}
	}
	apiName := fauxRandomId(fauxDefaultApi)
	if options.Api != nil {
		apiName = *options.Api
	}
	provider := fauxDefaultProvider
	if options.Provider != nil {
		provider = *options.Provider
	}
	rawMin := fauxDefaultMinTokenSize
	rawMax := fauxDefaultMaxTokenSize
	if options.TokenSize != nil {
		if options.TokenSize.Min != nil {
			rawMin = *options.TokenSize.Min
		}
		if options.TokenSize.Max != nil {
			rawMax = *options.TokenSize.Max
		}
	}
	minTokenSize := max(1, min(rawMin, rawMax))
	maxTokenSize := max(minTokenSize, rawMax)

	modelDefs := options.Models
	if len(modelDefs) == 0 {
		name := fauxDefaultModelName
		reasoning := false
		cw := 128000
		mt := 16384
		modelDefs = []FauxModelDefinition{
			{
				Id:            fauxDefaultModelId,
				Name:          &name,
				Reasoning:     &reasoning,
				Input:         []string{"text", "image"},
				Cost:          &ai.ModelCostRates{},
				ContextWindow: &cw,
				MaxTokens:     &mt,
			},
		}
	}

	models := make([]*ai.Model, len(modelDefs))
	for i, def := range modelDefs {
		name := def.Id
		if def.Name != nil {
			name = *def.Name
		}
		reasoning := false
		if def.Reasoning != nil {
			reasoning = *def.Reasoning
		}
		var input []ai.ModelModality
		if def.Input != nil {
			input = make([]ai.ModelModality, len(def.Input))
			for j, m := range def.Input {
				input[j] = ai.ModelModality(m)
			}
		} else {
			input = []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage}
		}
		var cost ai.ModelCost
		if def.Cost != nil {
			cost.ModelCostRates = *def.Cost
		}
		cw := 128000
		if def.ContextWindow != nil {
			cw = *def.ContextWindow
		}
		mt := 16384
		if def.MaxTokens != nil {
			mt = *def.MaxTokens
		}
		models[i] = &ai.Model{
			BaseModel: ai.BaseModel{
				Id:          def.Id,
				Name:        name,
				Api:         apiName,
				Provider:    ai.ProviderId(provider),
				BaseUrl:     fauxDefaultBaseUrl,
				Input:       input,
				InputLimits: def.InputLimits,
				Cost:        cost,
			},
			Reasoning:     reasoning,
			ContextWindow: cw,
			MaxTokens:     mt,
		}
	}

	var tokensPerSecond *float64
	if options.TokensPerSecond != nil {
		v := *options.TokensPerSecond
		tokensPerSecond = &v
	}
	var deferred *FauxDeferredOptions
	if options.Deferred != nil {
		d := *options.Deferred
		d.PendingFetches = fauxClonePtr(options.Deferred.PendingFetches)
		d.PollAfterMs = fauxClonePtr(options.Deferred.PollAfterMs)
		deferred = &d
	}

	return &FauxCore{
		Api:      apiName,
		Provider: provider,
		Models:   models,
		State: &FauxProviderState{
			CancelledDeferred: []*ai.DeferredHandle{},
		},
		minTokenSize:      minTokenSize,
		maxTokenSize:      maxTokenSize,
		tokensPerSecond:   tokensPerSecond,
		pendingResponses:  nil,
		promptCache:       make(map[string][]string),
		deferredResponses: make(map[string]*fauxDeferredEntry),
		deferred:          deferred,
	}
}

func (c *FauxCore) Stream(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
	outer := ai.NewAssistantMessageEventStream()
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	var step FauxResponseStep
	if len(c.pendingResponses) > 0 {
		step = c.pendingResponses[0]
		c.pendingResponses[0] = nil
		c.pendingResponses = c.pendingResponses[1:]
	}
	c.mu.Unlock()

	c.State.mu.Lock()
	c.State.CallCount++
	c.State.mu.Unlock()

	var streamOpts *ai.StreamOptions
	var simpleOpts *ai.SimpleStreamOptions
	if opts != nil {
		if s, ok := opts.(*ai.SimpleStreamOptions); ok {
			simpleOpts = s
		}
		streamOpts = opts.BaseStreamOptions()
	}
	if simpleOpts == nil && streamOpts != nil {
		simpleOpts = &ai.SimpleStreamOptions{StreamOptions: *streamOpts}
	}

	modelId := ""
	if model != nil {
		modelId = model.Id
	}

	go func() {
		if streamOpts != nil && streamOpts.OnResponse != nil {
			if err := streamOpts.OnResponse(&ai.ProviderResponse{Status: 200, Headers: map[string]string{}}, model); err != nil {
				errMsg := fauxCreateErrorMessage(err, c.Api, c.Provider, modelId)
				outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
				outer.End(errMsg)
				return
			}
		}

		if step == nil {
			errMsg := fauxCreateErrorMessage(errors.New("No more faux responses queued"), c.Api, c.Provider, modelId)
			errMsg = fauxWithUsageEstimate(&c.mu, errMsg, transcript, streamOpts, c.promptCache)
			outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
			outer.End(errMsg)
			return
		}

		if fauxHasDeferred(simpleOpts) {
			var pollAfterMs *int64
			pendingFetches := 0
			if c.deferred != nil {
				pollAfterMs = fauxClonePtr(c.deferred.PollAfterMs)
				if c.deferred.PendingFetches != nil {
					pendingFetches = max(0, *c.deferred.PendingFetches)
				}
			}
			var reqProvider, reqApi string
			if model != nil {
				reqProvider = string(model.Provider)
				reqApi = model.Api
			}
			handle := &ai.DeferredHandle{
				Provider:    reqProvider,
				ModelId:     modelId,
				Api:         reqApi,
				Id:          fauxRandomId("deferred"),
				PollAfterMs: pollAfterMs,
			}
			c.mu.Lock()
			c.deferredResponses[handle.Id] = &fauxDeferredEntry{
				Handle:         handle,
				Step:           step,
				Context:        transcript,
				Options:        simpleOpts,
				Model:          model,
				PendingFetches: pendingFetches,
				Cancelled:      false,
			}
			c.mu.Unlock()
			if err := fauxStreamWithDeltas(ctx, outer, fauxCreateDeferredMessage(model, fauxCloneDeferredHandle(handle)), c.minTokenSize, c.maxTokenSize, c.tokensPerSecond); err != nil {
				errMsg := fauxCreateErrorMessage(err, c.Api, c.Provider, modelId)
				outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
				outer.End(errMsg)
			}
			return
		}

		message, err := c.resolveResponse(ctx, step, transcript, simpleOpts, model)
		if err != nil {
			errMsg := fauxCreateErrorMessage(err, c.Api, c.Provider, modelId)
			outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
			outer.End(errMsg)
			return
		}
		if err := fauxStreamWithDeltas(ctx, outer, message, c.minTokenSize, c.maxTokenSize, c.tokensPerSecond); err != nil {
			errMsg := fauxCreateErrorMessage(err, c.Api, c.Provider, modelId)
			outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
			outer.End(errMsg)
		}
	}()

	return outer
}

func (c *FauxCore) StreamSimple(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	return c.Stream(ctx, model, transcript, opts)
}

func (c *FauxCore) FetchDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, opts *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	outer := ai.NewAssistantMessageEventStream()
	if ctx == nil {
		ctx = context.Background()
	}

	c.State.mu.Lock()
	c.State.DeferredFetchCount++
	c.State.mu.Unlock()

	modelId := ""
	if model != nil {
		modelId = model.Id
	}

	go func() {
		if opts != nil && opts.OnResponse != nil {
			if err := opts.OnResponse(&ai.ProviderResponse{Status: 200, Headers: map[string]string{}}, model); err != nil {
				errMsg := fauxCreateErrorMessage(err, c.Api, c.Provider, modelId)
				outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
				outer.End(errMsg)
				return
			}
		}

		handleId := ""
		if handle != nil {
			handleId = handle.Id
		}

		c.mu.Lock()
		entry, ok := c.deferredResponses[handleId]
		if !ok || handle == nil ||
			entry.Handle.Provider != handle.Provider ||
			entry.Handle.ModelId != handle.ModelId ||
			entry.Handle.Api != handle.Api {
			c.mu.Unlock()
			errMsg := fauxCreateErrorMessage(fmt.Errorf("Unknown faux deferred response: %s", handleId), c.Api, c.Provider, modelId)
			outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
			outer.End(errMsg)
			return
		}
		if entry.Cancelled {
			c.mu.Unlock()
			errMsg := fauxCreateErrorMessage(fmt.Errorf("Faux deferred response was cancelled: %s", handleId), c.Api, c.Provider, modelId)
			outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
			outer.End(errMsg)
			return
		}
		if entry.PendingFetches > 0 {
			entry.PendingFetches--
			handleCopy := fauxCloneDeferredHandle(entry.Handle)
			c.mu.Unlock()
			if err := fauxStreamWithDeltas(ctx, outer, fauxCreateDeferredMessage(model, handleCopy), c.minTokenSize, c.maxTokenSize, c.tokensPerSecond); err != nil {
				errMsg := fauxCreateErrorMessage(err, c.Api, c.Provider, modelId)
				outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
				outer.End(errMsg)
			}
			return
		}
		final := entry.Final
		step := entry.Step
		entryCtx := entry.Context
		entryOpts := entry.Options
		entryModel := entry.Model
		c.mu.Unlock()

		if final == nil {
			var submissionOpts *ai.SimpleStreamOptions
			if entryOpts != nil {
				cp := *entryOpts
				cp.Deferred = ai.SimpleStreamDeferredValue{}
				cp.OnResponse = nil
				submissionOpts = &cp
			}
			resolved, err := c.resolveResponse(ctx, step, entryCtx, submissionOpts, entryModel)
			if err != nil {
				entryModelId := ""
				if entryModel != nil {
					entryModelId = entryModel.Id
				}
				resolved = fauxCreateErrorMessage(err, c.Api, c.Provider, entryModelId)
			}
			c.mu.Lock()
			if entry.Final == nil {
				entry.Final = resolved
			}
			final = entry.Final
			c.mu.Unlock()
		}

		// Shallow-clone before streaming so EventStream's DurationMs stamp cannot race across repeated fetches.
		if err := fauxStreamWithDeltas(ctx, outer, fauxShallowCopyAssistant(final), c.minTokenSize, c.maxTokenSize, c.tokensPerSecond); err != nil {
			errMsg := fauxCreateErrorMessage(err, c.Api, c.Provider, modelId)
			outer.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: errMsg})
			outer.End(errMsg)
		}
	}()

	return outer
}

func (c *FauxCore) CancelDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, opts *ai.DeferredCancelOptions) error {
	c.State.mu.Lock()
	c.State.CancelledDeferred = append(c.State.CancelledDeferred, fauxCloneDeferredHandle(handle))
	c.State.mu.Unlock()

	if handle != nil {
		c.mu.Lock()
		if entry, ok := c.deferredResponses[handle.Id]; ok {
			entry.Cancelled = true
		}
		c.mu.Unlock()
	}

	if opts != nil && opts.OnResponse != nil {
		return opts.OnResponse(&ai.ProviderResponse{Status: 200, Headers: map[string]string{}}, model)
	}
	return nil
}

func (c *FauxCore) GetModel() *ai.Model {
	if len(c.Models) == 0 {
		return nil
	}
	return c.Models[0]
}

func (c *FauxCore) GetModelId(modelId string) *ai.Model {
	for _, candidate := range c.Models {
		if candidate != nil && candidate.Id == modelId {
			return candidate
		}
	}
	return nil
}

func (c *FauxCore) SetResponses(responses []FauxResponseStep) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingResponses = slices.Clone(responses)
}

func (c *FauxCore) AppendResponses(responses []FauxResponseStep) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingResponses = append(c.pendingResponses, responses...)
}

func (c *FauxCore) GetPendingResponseCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pendingResponses)
}

// GetModel returns the first model. GetModelId is the getModel(modelId) overload.
func (h *FauxProviderHandle) GetModel() *ai.Model {
	if h == nil || h.core == nil {
		return nil
	}
	return h.core.GetModel()
}

func (h *FauxProviderHandle) GetModelId(modelId string) *ai.Model {
	if h == nil || h.core == nil {
		return nil
	}
	return h.core.GetModelId(modelId)
}

func (h *FauxProviderHandle) SetResponses(responses []FauxResponseStep) {
	if h == nil || h.core == nil {
		return
	}
	h.core.SetResponses(responses)
}

func (h *FauxProviderHandle) AppendResponses(responses []FauxResponseStep) {
	if h == nil || h.core == nil {
		return
	}
	h.core.AppendResponses(responses)
}

func (h *FauxProviderHandle) GetPendingResponseCount() int {
	if h == nil || h.core == nil {
		return 0
	}
	return h.core.GetPendingResponseCount()
}

// FauxProvider returns a test provider for an explicit Models collection.
// Nil options means {}. It does not call a network provider.
func FauxProvider(options *RegisterFauxProviderOptions) (*FauxProviderHandle, error) {
	core := CreateFauxCore(options)
	anyModels := make([]ai.AnyModel, len(core.Models))
	for i, m := range core.Models {
		anyModels[i] = m
	}
	provider, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id: core.Provider,
		Auth: ai.ProviderAuth{
			ApiKey: &ai.ApiKeyAuth{
				Name: "Faux",
				Resolve: func(ctx context.Context, input ai.ApiKeyResolveInput) (*ai.AuthResult, error) {
					return &ai.AuthResult{}, nil
				},
			},
		},
		Models: anyModels,
		Api: ai.ProviderStreams{
			Stream:         core.Stream,
			StreamSimple:   core.StreamSimple,
			FetchDeferred:  core.FetchDeferred,
			CancelDeferred: core.CancelDeferred,
		},
	})
	if err != nil {
		return nil, err
	}
	return &FauxProviderHandle{
		core:     core,
		Provider: provider,
		Api:      core.Api,
		Models:   core.Models,
		State:    core.State,
	}, nil
}
