// Ported from packages/ai/src/providers/faux.ts (pi v1.0.0).

package providers

import (
	"context"
	"sync"

	"github.com/keejkrej/pi-go/ai"
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
	promptCache       map[string]string
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
	panic("unported: FauxText")
}

// FauxThinking returns a thinking block.
func FauxThinking(thinking string) *ai.ThinkingContent {
	panic("unported: FauxThinking")
}

// FauxToolCall returns a tool-call block. Nil options means {}.
// arguments is the tool-call arguments object.
func FauxToolCall(name string, arguments *jsonx.Object, options *FauxToolCallOptions) *ai.ToolCall {
	panic("unported: FauxToolCall")
}

// FauxAssistantMessage builds an assistant message tagged with the faux defaults.
// Nil options means {}.
func FauxAssistantMessage(content FauxAssistantContent, options *FauxAssistantMessageOptions) *ai.AssistantMessage {
	panic("unported: FauxAssistantMessage")
}

func fauxNormalizeAssistantContent(content FauxAssistantContent) []ai.Content {
	panic("unported: fauxNormalizeAssistantContent")
}

func fauxEstimateTokens(text string) int {
	panic("unported: fauxEstimateTokens")
}

func fauxRandomId(prefix string) string {
	panic("unported: fauxRandomId")
}

// text non-nil selects the string form, including "". Indexes elsewhere in this file that
// mirror JavaScript string indexes are UTF-16 code units.
func fauxContentToText(text *string, blocks []ai.Content) string {
	panic("unported: fauxContentToText")
}

func fauxAssistantContentToText(content []ai.Content) string {
	panic("unported: fauxAssistantContentToText")
}

func fauxToolResultToText(message *ai.ToolResultMessage) string {
	panic("unported: fauxToolResultToText")
}

func fauxMessageToText(message ai.Message) string {
	panic("unported: fauxMessageToText")
}

func fauxSerializeContext(transcript *ai.TranscriptContext) string {
	panic("unported: fauxSerializeContext")
}

// fauxCommonPrefixLength returns a JavaScript string index (UTF-16 code units).
func fauxCommonPrefixLength(a, b string) int {
	panic("unported: fauxCommonPrefixLength")
}

func fauxWithUsageEstimate(message *ai.AssistantMessage, transcript *ai.TranscriptContext, options *ai.StreamOptions, promptCache map[string]string) *ai.AssistantMessage {
	panic("unported: fauxWithUsageEstimate")
}

// fauxSplitStringByTokenSize slices text with JavaScript string indexes (UTF-16 code units).
func fauxSplitStringByTokenSize(text string, minTokenSize, maxTokenSize int) []string {
	panic("unported: fauxSplitStringByTokenSize")
}

func fauxCloneMessage(message *ai.AssistantMessage, apiName, provider, modelId string) *ai.AssistantMessage {
	panic("unported: fauxCloneMessage")
}

func fauxCreateDeferredMessage(model *ai.Model, handle *ai.DeferredHandle) *ai.AssistantMessage {
	panic("unported: fauxCreateDeferredMessage")
}

func fauxCreateErrorMessage(err any, apiName, provider, modelId string) *ai.AssistantMessage {
	panic("unported: fauxCreateErrorMessage")
}

func fauxCreateAbortedMessage(partial *ai.AssistantMessage) *ai.AssistantMessage {
	panic("unported: fauxCreateAbortedMessage")
}

func fauxScheduleChunk(chunk string, tokensPerSecond *float64) error {
	panic("unported: fauxScheduleChunk")
}

func fauxStreamWithDeltas(ctx context.Context, stream *ai.AssistantMessageEventStream, message *ai.AssistantMessage, minTokenSize, maxTokenSize int, tokensPerSecond *float64) error {
	panic("unported: fauxStreamWithDeltas")
}

// CreateFauxCore builds the scripted stream core. Nil options means {}.
// It does not register an ai.Provider and does not call a network provider.
func CreateFauxCore(options *RegisterFauxProviderOptions) *FauxCore {
	panic("unported: CreateFauxCore")
}

func (c *FauxCore) Stream(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
	panic("unported: FauxCore.Stream")
}

func (c *FauxCore) StreamSimple(ctx context.Context, model *ai.Model, transcript *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	panic("unported: FauxCore.StreamSimple")
}

func (c *FauxCore) FetchDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, opts *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	panic("unported: FauxCore.FetchDeferred")
}

func (c *FauxCore) CancelDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, opts *ai.DeferredCancelOptions) error {
	panic("unported: FauxCore.CancelDeferred")
}

func (c *FauxCore) GetModel() *ai.Model {
	panic("unported: FauxCore.GetModel")
}

func (c *FauxCore) GetModelId(modelId string) *ai.Model {
	panic("unported: FauxCore.GetModelId")
}

func (c *FauxCore) SetResponses(responses []FauxResponseStep) {
	panic("unported: FauxCore.SetResponses")
}

func (c *FauxCore) AppendResponses(responses []FauxResponseStep) {
	panic("unported: FauxCore.AppendResponses")
}

func (c *FauxCore) GetPendingResponseCount() int {
	panic("unported: FauxCore.GetPendingResponseCount")
}

// GetModel returns the first model. GetModelId is the getModel(modelId) overload.
func (h *FauxProviderHandle) GetModel() *ai.Model {
	panic("unported: FauxProviderHandle.GetModel")
}

func (h *FauxProviderHandle) GetModelId(modelId string) *ai.Model {
	panic("unported: FauxProviderHandle.GetModelId")
}

func (h *FauxProviderHandle) SetResponses(responses []FauxResponseStep) {
	panic("unported: FauxProviderHandle.SetResponses")
}

func (h *FauxProviderHandle) AppendResponses(responses []FauxResponseStep) {
	panic("unported: FauxProviderHandle.AppendResponses")
}

func (h *FauxProviderHandle) GetPendingResponseCount() int {
	panic("unported: FauxProviderHandle.GetPendingResponseCount")
}

// FauxProvider returns a test provider for an explicit Models collection.
// Nil options means {}. It does not call a network provider.
func FauxProvider(options *RegisterFauxProviderOptions) (*FauxProviderHandle, error) {
	panic("unported: FauxProvider")
}
