// Ported from packages/ai/src/types.ts (pi v1.0.0).

package ai

import (
	"context"
	"net/http"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/telemetry"
)

// JsonValue is a JSON value. It holds a jsonx untyped value: nil, bool,
// float64, string, []any, or *jsonx.Object.
type JsonValue = any

// JsonObject is a JSON object in JavaScript key order.
type JsonObject = *jsonx.Object

// JsonRepresentation is a TypeScript type-level mapping and has no Go form.
// Callers use the concrete Go type.

// Api is an open provider API id. KnownApi names the built-in ids.
type Api string

// KnownApi is Api restricted to built-in API ids.
type KnownApi = Api

const (
	KnownApiOpenaiCompletions    KnownApi = "openai-completions"
	KnownApiMistralConversations KnownApi = "mistral-conversations"
	KnownApiOpenaiResponses      KnownApi = "openai-responses"
	KnownApiAzureOpenaiResponses KnownApi = "azure-openai-responses"
	KnownApiOpenaiCodexResponses KnownApi = "openai-codex-responses"
	KnownApiAnthropicMessages    KnownApi = "anthropic-messages"
	KnownApiBedrockConverseStream KnownApi = "bedrock-converse-stream"
	KnownApiGoogleGenerativeAi   KnownApi = "google-generative-ai"
	KnownApiGoogleVertex         KnownApi = "google-vertex"
	KnownApiPiMessages           KnownApi = "pi-messages"
)

// ImageApi is an open image-generation API id.
type ImageApi string

// KnownImageApi is ImageApi restricted to built-in ids.
type KnownImageApi = ImageApi

const KnownImageApiOpenrouterImages KnownImageApi = "openrouter-images"

// ClassifierApi is an open classifier API id.
type ClassifierApi string

// KnownClassifierApi is ClassifierApi restricted to built-in ids.
type KnownClassifierApi = ClassifierApi

const (
	KnownClassifierApiTypesafeSystemOne            KnownClassifierApi = "typesafe-system-one"
	KnownClassifierApiCloudflareWorkersAiSystemOne KnownClassifierApi = "cloudflare-workers-ai-system-one"
	KnownClassifierApiLlamaCppClassify             KnownClassifierApi = "llama-cpp-classify"
)

// ProviderId is a provider id, including custom ids.
type ProviderId string

// KnownProvider is ProviderId restricted to built-in provider ids.
type KnownProvider = ProviderId

const (
	KnownProviderAmazonBedrock             KnownProvider = "amazon-bedrock"
	KnownProviderAntLing                   KnownProvider = "ant-ling"
	KnownProviderAnthropic                 KnownProvider = "anthropic"
	KnownProviderGoogle                    KnownProvider = "google"
	KnownProviderGoogleVertex              KnownProvider = "google-vertex"
	KnownProviderOpenai                    KnownProvider = "openai"
	KnownProviderAzureOpenaiResponses      KnownProvider = "azure-openai-responses"
	KnownProviderOpenaiCodex               KnownProvider = "openai-codex"
	KnownProviderRadius                    KnownProvider = "radius"
	KnownProviderTypesafe                  KnownProvider = "typesafe"
	KnownProviderNvidia                    KnownProvider = "nvidia"
	KnownProviderDeepseek                  KnownProvider = "deepseek"
	KnownProviderGithubCopilot             KnownProvider = "github-copilot"
	KnownProviderXai                       KnownProvider = "xai"
	KnownProviderGroq                      KnownProvider = "groq"
	KnownProviderCerebras                  KnownProvider = "cerebras"
	KnownProviderOpenrouter                KnownProvider = "openrouter"
	KnownProviderVercelAiGateway           KnownProvider = "vercel-ai-gateway"
	KnownProviderZai                       KnownProvider = "zai"
	KnownProviderZaiCodingCn               KnownProvider = "zai-coding-cn"
	KnownProviderMistral                   KnownProvider = "mistral"
	KnownProviderMinimax                   KnownProvider = "minimax"
	KnownProviderMinimaxCn                 KnownProvider = "minimax-cn"
	KnownProviderMoonshotai                KnownProvider = "moonshotai"
	KnownProviderMoonshotaiCn              KnownProvider = "moonshotai-cn"
	KnownProviderHuggingface               KnownProvider = "huggingface"
	KnownProviderFireworks                 KnownProvider = "fireworks"
	KnownProviderTogether                  KnownProvider = "together"
	KnownProviderBaseten                   KnownProvider = "baseten"
	KnownProviderOpencode                  KnownProvider = "opencode"
	KnownProviderOpencodeGo                KnownProvider = "opencode-go"
	KnownProviderKimiCoding                KnownProvider = "kimi-coding"
	KnownProviderMeta                      KnownProvider = "meta"
	KnownProviderCloudflareWorkersAi       KnownProvider = "cloudflare-workers-ai"
	KnownProviderCloudflareAiGateway       KnownProvider = "cloudflare-ai-gateway"
	KnownProviderQwenTokenPlan             KnownProvider = "qwen-token-plan"
	KnownProviderQwenTokenPlanCn           KnownProvider = "qwen-token-plan-cn"
	KnownProviderQwenTokenPlanIndividual   KnownProvider = "qwen-token-plan-individual"
	KnownProviderXiaomi                    KnownProvider = "xiaomi"
	KnownProviderXiaomiTokenPlanCn         KnownProvider = "xiaomi-token-plan-cn"
	KnownProviderXiaomiTokenPlanAms        KnownProvider = "xiaomi-token-plan-ams"
	KnownProviderXiaomiTokenPlanSgp        KnownProvider = "xiaomi-token-plan-sgp"
)

// ToolChoice is provider-neutral tool selection for simple requests.
type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceNone ToolChoice = "none"
)

// ThinkingLevel is a pi reasoning effort.
type ThinkingLevel string

const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXhigh   ThinkingLevel = "xhigh"
	ThinkingLevelMax     ThinkingLevel = "max"
)

// ModelThinkingLevel is "off" or a ThinkingLevel.
type ModelThinkingLevel string

const (
	ModelThinkingLevelOff     ModelThinkingLevel = "off"
	ModelThinkingLevelMinimal ModelThinkingLevel = "minimal"
	ModelThinkingLevelLow     ModelThinkingLevel = "low"
	ModelThinkingLevelMedium  ModelThinkingLevel = "medium"
	ModelThinkingLevelHigh    ModelThinkingLevel = "high"
	ModelThinkingLevelXhigh   ModelThinkingLevel = "xhigh"
	ModelThinkingLevelMax     ModelThinkingLevel = "max"
)

// ThinkingLevelMap maps pi thinking levels to provider values.
// A null entry marks the level unsupported. Absent keys use provider defaults.
// JSON null is the Opt null state; an absent key is the Opt zero value.
type ThinkingLevelMap struct {
	Off     jsonx.Opt[string] `json:"off,omitzero"`
	Minimal jsonx.Opt[string] `json:"minimal,omitzero"`
	Low     jsonx.Opt[string] `json:"low,omitzero"`
	Medium  jsonx.Opt[string] `json:"medium,omitzero"`
	High    jsonx.Opt[string] `json:"high,omitzero"`
	Xhigh   jsonx.Opt[string] `json:"xhigh,omitzero"`
	Max     jsonx.Opt[string] `json:"max,omitzero"`
}

// ChatTemplateKwargVarName is a pi-controlled chat-template variable.
type ChatTemplateKwargVarName string

const (
	ChatTemplateKwargVarNameThinkingEnabled ChatTemplateKwargVarName = "thinking.enabled"
	ChatTemplateKwargVarNameThinkingEffort  ChatTemplateKwargVarName = "thinking.effort"
	ChatTemplateKwargVarNameThinkingBudget  ChatTemplateKwargVarName = "thinking.budget"
)

// ChatTemplateKwargValue is a chat-template kwarg: a JSON scalar, null, or a
// {$var, omitWhenOff?} object. Scalars are the named primitive types.
type ChatTemplateKwargValue interface{ isChatTemplateKwargValue() }

// ChatTemplateKwargString is a string kwarg.
type ChatTemplateKwargString string

func (ChatTemplateKwargString) isChatTemplateKwargValue() {}

// ChatTemplateKwargNumber is a numeric kwarg.
type ChatTemplateKwargNumber float64

func (ChatTemplateKwargNumber) isChatTemplateKwargValue() {}

// ChatTemplateKwargBool is a boolean kwarg.
type ChatTemplateKwargBool bool

func (ChatTemplateKwargBool) isChatTemplateKwargValue() {}

// ChatTemplateKwargNull is a JSON null kwarg.
type ChatTemplateKwargNull struct{}

func (ChatTemplateKwargNull) isChatTemplateKwargValue() {}

func (ChatTemplateKwargNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// ChatTemplateKwargVar is a pi-controlled thinking placeholder.
type ChatTemplateKwargVar struct {
	Var         ChatTemplateKwargVarName `json:"$var"`
	OmitWhenOff *bool                    `json:"omitWhenOff,omitzero"`
}

func (*ChatTemplateKwargVar) isChatTemplateKwargValue() {}

// UnknownChatTemplateKwargValue keeps an unrecognized kwarg object.
type UnknownChatTemplateKwargValue struct{ Raw *jsonx.Object }

func (*UnknownChatTemplateKwargValue) isChatTemplateKwargValue() {}

func (*UnknownChatTemplateKwargValue) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownChatTemplateKwargValue.MarshalJSON")
}

func (*UnknownChatTemplateKwargValue) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownChatTemplateKwargValue.UnmarshalJSON")
}

// UnmarshalChatTemplateKwargValue decodes one kwarg value.
func UnmarshalChatTemplateKwargValue(data []byte) (ChatTemplateKwargValue, error) {
	panic("unported: UnmarshalChatTemplateKwargValue")
}

// DecodeChatTemplateKwargValue decodes one kwarg value from a jsonx value.
func DecodeChatTemplateKwargValue(v any) (ChatTemplateKwargValue, error) {
	panic("unported: DecodeChatTemplateKwargValue")
}

// ThinkingTokenBudgetField is the request field that caps reasoning tokens.
type ThinkingTokenBudgetField string

const (
	ThinkingTokenBudgetFieldThinkingTokenBudget  ThinkingTokenBudgetField = "thinking_token_budget"
	ThinkingTokenBudgetFieldThinkingBudget       ThinkingTokenBudgetField = "thinking_budget"
	ThinkingTokenBudgetFieldThinkingBudgetTokens ThinkingTokenBudgetField = "thinking_budget_tokens"
)

// ThinkingBudgets is per-level token budgets for token-based providers.
type ThinkingBudgets struct {
	Minimal *int `json:"minimal,omitzero"`
	Low     *int `json:"low,omitzero"`
	Medium  *int `json:"medium,omitzero"`
	High    *int `json:"high,omitzero"`
}

// CacheRetention is the prompt-cache lifetime a request asks for.
type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

// ModelPromptCache is the best-effort lifetime in seconds for each retention
// tier. A missing tier means the lifetime is unknown; pi does not warm it.
type ModelPromptCache struct {
	Short *int `json:"short,omitzero"`
	Long  *int `json:"long,omitzero"`
}

// Transport is the preferred wire transport.
type Transport string

const (
	TransportSse             Transport = "sse"
	TransportWebsocket       Transport = "websocket"
	TransportWebsocketCached Transport = "websocket-cached"
	TransportAuto            Transport = "auto"
)

// ProviderEnv holds provider-scoped environment overrides.
// Values take precedence over the process environment. Lookup is by name.
type ProviderEnv map[string]string

// ProviderHeaders are extra HTTP headers. A nil value suppresses a default
// header of the same name. Key order follows insertion, with integer-like keys first.
type ProviderHeaders = *omap.Map[string, *string]

// FetchFunction is the TS fetch signature as a Go round trip.
// Provider request options carry HTTPClient instead of fetch.
type FetchFunction func(ctx context.Context, req *http.Request) (*http.Response, error)

// SessionAffinityFormat selects session-affinity headers.
type SessionAffinityFormat string

const (
	SessionAffinityFormatOpenai         SessionAffinityFormat = "openai"
	SessionAffinityFormatOpenaiNosession SessionAffinityFormat = "openai-nosession"
	SessionAffinityFormatOpenrouter     SessionAffinityFormat = "openrouter"
)

// ProviderResponse is the HTTP response handed to onResponse.
type ProviderResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

// ProviderRequestOptions is authentication, HTTP transport, and lifecycle
// callbacks shared by provider requests. The TS signal is the ctx argument of
// the call, not a field. Nil HTTPClient means the process default client.
// OnPayload returns a nil any to keep the payload unchanged.
type ProviderRequestOptions[TModel any] struct {
	TelemetryContext telemetry.TelemetryContext                                      `json:"-"`
	ApiKey           *string                                                         `json:"apiKey,omitzero"`
	HTTPClient       *http.Client                                                    `json:"-"`
	Env              ProviderEnv                                                     `json:"env,omitzero"`
	OnPayload        func(payload any, model *TModel) (any, error)                   `json:"-"`
	OnResponse       func(response *ProviderResponse, model *TModel) error           `json:"-"`
	Headers          ProviderHeaders                                                 `json:"headers,omitzero"`
	TimeoutMs        *int64                                                          `json:"timeoutMs,omitzero"`
	MaxRetries       *int                                                            `json:"maxRetries,omitzero"`
	MaxRetryDelayMs  *int64                                                          `json:"maxRetryDelayMs,omitzero"`
}

// StreamOptions is the shared streaming request.
// SamplingParams merges into the body after named fields; keys here win.
// CacheRetention defaults to "short" when unset. MaxRetryDelayMs defaults to
// 60000 at the call site; zero disables the cap.
type StreamOptions struct {
	ProviderRequestOptions[Model]
	OnProviderStreamEvent    func(data any, model *Model) error `json:"-"`
	Temperature              *float64                           `json:"temperature,omitzero"`
	SamplingParams           *jsonx.Object                      `json:"samplingParams,omitzero"`
	MaxTokens                *int                               `json:"maxTokens,omitzero"`
	Transport                *Transport                         `json:"transport,omitzero"`
	CacheRetention           *CacheRetention                    `json:"cacheRetention,omitzero"`
	SessionId                *string                            `json:"sessionId,omitzero"`
	WebsocketConnectTimeoutMs *int64                            `json:"websocketConnectTimeoutMs,omitzero"`
	Metadata                 *jsonx.Object                      `json:"metadata,omitzero"`
}

// BaseStreamOptions returns the embedded shared options.
func (o *StreamOptions) BaseStreamOptions() *StreamOptions { return o }

// ProviderStreamOptions is the stream-option surface API modules extend.
// ApiOptionsMap and ApiStreamOptions are not ported: per-API structs live in
// package api, embed StreamOptions, and implement this interface.
type ProviderStreamOptions interface {
	BaseStreamOptions() *StreamOptions
}

// DeferredFetchOptions is a deferred-response poll.
// Wait is the maximum long-poll duration in milliseconds. Zero, the default,
// performs one status check.
type DeferredFetchOptions struct {
	ProviderRequestOptions[Model]
	Wait *int64 `json:"wait,omitzero"`
}

// DeferredCancelOptions is a best-effort deferred-response cancellation.
type DeferredCancelOptions = ProviderRequestOptions[Model]

// ProviderStreams is the value shape of a chat API module.
// A nil FetchDeferred or CancelDeferred means that method is absent.
type ProviderStreams struct {
	Stream         func(ctx context.Context, model *Model, c *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream
	StreamSimple   func(ctx context.Context, model *Model, c *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream
	FetchDeferred  func(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream
	CancelDeferred func(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error
}

// ProviderImages is the value shape of an image API module.
type ProviderImages struct {
	GenerateImages ImagesFunction
}

// ProviderClassifier is the value shape of a classifier API module.
type ProviderClassifier struct {
	Classify ClassifierFunction
}

// ClassifierOptions is a classifier request.
type ClassifierOptions struct {
	ProviderRequestOptions[ClassifierModel]
	Temperature *float64 `json:"temperature,omitzero"`
}

// ImagesOptions is an image-generation request.
type ImagesOptions struct {
	ProviderRequestOptions[ImageModel]
	Metadata *jsonx.Object `json:"metadata,omitzero"`
}

// BaseImagesOptions returns the embedded image options.
func (o *ImagesOptions) BaseImagesOptions() *ImagesOptions { return o }

// ProviderImagesOptions is ImagesOptions widened the way ProviderStreamOptions
// widens StreamOptions. Image API option structs embed ImagesOptions.
type ProviderImagesOptions interface {
	BaseImagesOptions() *ImagesOptions
}

// AnthropicAllowedFallbackModel is one server-side refusal fallback target.
type AnthropicAllowedFallbackModel struct {
	Provider ProviderId `json:"provider"`
	Model    string     `json:"model"`
	Cost     ModelCost  `json:"cost"`
}

// DeferredWindow is a deferred-response window.
type DeferredWindow string

const (
	DeferredWindow15m DeferredWindow = "15m"
	DeferredWindow1h  DeferredWindow = "1h"
	DeferredWindow24h DeferredWindow = "24h"
)

// SimpleStreamDeferred is true, false, or a window object.
type SimpleStreamDeferred interface{ isSimpleStreamDeferred() }

// SimpleStreamDeferredBool is the boolean form of deferred.
type SimpleStreamDeferredBool bool

func (SimpleStreamDeferredBool) isSimpleStreamDeferred() {}

// SimpleStreamDeferredWindow is the object form of deferred.
type SimpleStreamDeferredWindow struct {
	Window *DeferredWindow `json:"window,omitzero"`
}

func (*SimpleStreamDeferredWindow) isSimpleStreamDeferred() {}

// UnknownSimpleStreamDeferred keeps an unrecognized deferred value.
type UnknownSimpleStreamDeferred struct{ Raw *jsonx.Object }

func (*UnknownSimpleStreamDeferred) isSimpleStreamDeferred() {}

func (*UnknownSimpleStreamDeferred) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownSimpleStreamDeferred.MarshalJSON")
}

func (*UnknownSimpleStreamDeferred) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownSimpleStreamDeferred.UnmarshalJSON")
}

// SimpleStreamDeferredValue is the JSON form of StreamOptions-level deferred.
// The zero value is omitted.
type SimpleStreamDeferredValue struct {
	V SimpleStreamDeferred
}

func (v SimpleStreamDeferredValue) IsZero() bool { return v.V == nil }

func (v SimpleStreamDeferredValue) MarshalJSON() ([]byte, error) {
	panic("unported: SimpleStreamDeferredValue.MarshalJSON")
}

func (v *SimpleStreamDeferredValue) UnmarshalJSON(data []byte) error {
	panic("unported: SimpleStreamDeferredValue.UnmarshalJSON")
}

// UnmarshalSimpleStreamDeferred decodes a deferred flag.
func UnmarshalSimpleStreamDeferred(data []byte) (SimpleStreamDeferred, error) {
	panic("unported: UnmarshalSimpleStreamDeferred")
}

// DecodeSimpleStreamDeferred decodes a deferred flag from a jsonx value.
func DecodeSimpleStreamDeferred(v any) (SimpleStreamDeferred, error) {
	panic("unported: DecodeSimpleStreamDeferred")
}

// SimpleStreamOptions is the uniform request passed to streamSimple.
type SimpleStreamOptions struct {
	StreamOptions
	ToolChoice      *ToolChoice               `json:"toolChoice,omitzero"`
	Reasoning       *ThinkingLevel            `json:"reasoning,omitzero"`
	Deferred        SimpleStreamDeferredValue `json:"deferred,omitzero"`
	ThinkingBudgets *ThinkingBudgets          `json:"thinkingBudgets,omitzero"`
}

// StreamFunction is the uniform chat stream contract from PORTING.md.
// Direct calls may still fail before a stream exists; once returned, failures
// are events on the stream. ProviderStreams.Stream takes *TranscriptContext.
type StreamFunction func(ctx context.Context, model *Model, c *Context, opts ProviderStreamOptions) *AssistantMessageEventStream

// ImagesFunction generates images for one model.
type ImagesFunction func(ctx context.Context, model *ImageModel, context *ImagesContext, options *ImagesOptions) (*AssistantImages, error)

// ClassifierFunction classifies one model request.
type ClassifierFunction func(ctx context.Context, model *ClassifierModel, context *ClassifierContext, options *ClassifierOptions) (*ClassifierResult, error)

// TextSignaturePhase is the phase stored in a version-1 text signature.
type TextSignaturePhase string

const (
	TextSignaturePhaseCommentary TextSignaturePhase = "commentary"
	TextSignaturePhaseFinalAnswer TextSignaturePhase = "final_answer"
)

// TextSignatureV1 is the structured form of a text signature.
type TextSignatureV1 struct {
	V     int                 `json:"v"`
	Id    string              `json:"id"`
	Phase *TextSignaturePhase `json:"phase,omitzero"`
}

// Content is a transcript content block. The discriminator is "type".
type Content interface{ isContent() }

// TextContent is a text block.
type TextContent struct {
	Type          string  `json:"type"`
	Text          string  `json:"text"`
	TextSignature *string `json:"textSignature,omitzero"`
}

func (*TextContent) isContent() {}

// ThinkingContent is a reasoning block.
// When Redacted is true, the opaque payload lives in ThinkingSignature.
type ThinkingContent struct {
	Type              string  `json:"type"`
	Thinking          string  `json:"thinking"`
	ThinkingSignature *string `json:"thinkingSignature,omitzero"`
	Redacted          *bool   `json:"redacted,omitzero"`
}

func (*ThinkingContent) isContent() {}

// ImageContent is a base64 image block.
type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func (*ImageContent) isContent() {}

// ToolCall is a model tool call. Arguments is a JSON object.
type ToolCall struct {
	Type             string        `json:"type"`
	Id               string        `json:"id"`
	Name             string        `json:"name"`
	Arguments        *jsonx.Object `json:"arguments"`
	ThoughtSignature *string       `json:"thoughtSignature,omitzero"`
	Namespace        *string       `json:"namespace,omitzero"`
}

func (*ToolCall) isContent() {}

// UnknownContent keeps a content block whose type is not recognized.
type UnknownContent struct{ Raw *jsonx.Object }

func (*UnknownContent) isContent() {}

func (*UnknownContent) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownContent.MarshalJSON")
}

func (*UnknownContent) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownContent.UnmarshalJSON")
}

// ContentList is a JSON array of content blocks.
type ContentList []Content

func (l *ContentList) UnmarshalJSON(data []byte) error {
	panic("unported: ContentList.UnmarshalJSON")
}

// UnmarshalContent decodes one content block.
func UnmarshalContent(data []byte) (Content, error) {
	panic("unported: UnmarshalContent")
}

// DecodeContent decodes one content block from a jsonx value.
func DecodeContent(v any) (Content, error) {
	panic("unported: DecodeContent")
}

// ImagesInputContent is a text or image block on an image request.
type ImagesInputContent = Content

// ImagesOutputContent is a text or image block on an image response.
type ImagesOutputContent = Content

// UsageCost is the dollar cost breakdown inside Usage.
type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// Usage is token accounting. Token counts are integers. Cost is dollars.
// Reasoning, when set, is a subset of Output. CacheWrite1h is the 1h portion
// of CacheWrite and is set only when the provider reports the split.
type Usage struct {
	Input         int       `json:"input"`
	Output        int       `json:"output"`
	CacheRead     int       `json:"cacheRead"`
	CacheWrite    int       `json:"cacheWrite"`
	CacheWrite1h  *int      `json:"cacheWrite1h,omitzero"`
	Reasoning     *int      `json:"reasoning,omitzero"`
	TotalTokens   int       `json:"totalTokens"`
	Cost          UsageCost `json:"cost"`
}

// StopReason is why an assistant turn ended.
type StopReason string

const (
	StopReasonPending  StopReason = "pending"
	StopReasonStop     StopReason = "stop"
	StopReasonLength   StopReason = "length"
	StopReasonToolUse  StopReason = "toolUse"
	StopReasonError    StopReason = "error"
	StopReasonAborted  StopReason = "aborted"
	StopReasonDeferred StopReason = "deferred"
)

// DeferredHandle is a durable provider handle for an in-flight response.
// ExpiresAt is epoch milliseconds. PollAfterMs is a delay in milliseconds.
// Data is provider conversion state and may be null.
type DeferredHandle struct {
	Provider    string         `json:"provider"`
	ModelId     string         `json:"modelId"`
	Api         string         `json:"api"`
	Id          string         `json:"id"`
	ExpiresAt   *int64         `json:"expiresAt,omitzero"`
	PollAfterMs *int64         `json:"pollAfterMs,omitzero"`
	Data        jsonx.Opt[any] `json:"data,omitzero"`
}

// SystemMessageContent is a system message body: a string, or an array of blocks.
// Text is set for the string form, including the empty string. Blocks is set
// for the array form, including an empty array.
type SystemMessageContent struct {
	Text   *string
	Blocks ContentList
}

func (c SystemMessageContent) MarshalJSON() ([]byte, error) {
	panic("unported: SystemMessageContent.MarshalJSON")
}

func (c *SystemMessageContent) UnmarshalJSON(data []byte) error {
	panic("unported: SystemMessageContent.UnmarshalJSON")
}

// UserMessageContent is a user message body: a string, or text and image blocks.
// Text and Blocks are interpreted the same way as SystemMessageContent.
type UserMessageContent struct {
	Text   *string
	Blocks ContentList
}

func (c UserMessageContent) MarshalJSON() ([]byte, error) {
	panic("unported: UserMessageContent.MarshalJSON")
}

func (c *UserMessageContent) UnmarshalJSON(data []byte) error {
	panic("unported: UserMessageContent.UnmarshalJSON")
}

// Message is one transcript message. The discriminator is "role".
// Every variant implements GetRole.
type Message interface {
	isMessage()
	GetRole() string
}

// SystemMessage is a system prompt or a mid-transcript prompt update.
// Sections render after Content. A null section value removes that name.
// Integer-like section names are reordered by JSON object key order.
// Timestamp is epoch milliseconds.
type SystemMessage struct {
	Role         string                     `json:"role"`
	Content      SystemMessageContent       `json:"content"`
	Sections     *omap.Map[string, *string] `json:"sections,omitzero"`
	ToolsAdded   []*Tool                    `json:"toolsAdded,omitzero"`
	ToolsRemoved []ToolReference            `json:"toolsRemoved,omitzero"`
	Timestamp    int64                      `json:"timestamp"`
}

func (*SystemMessage) isMessage() {}

func (*SystemMessage) GetRole() string { return "system" }

// UserMessage is a user turn. Timestamp is epoch milliseconds.
type UserMessage struct {
	Role      string             `json:"role"`
	Content   UserMessageContent `json:"content"`
	Timestamp int64              `json:"timestamp"`
}

func (*UserMessage) isMessage() {}

func (*UserMessage) GetRole() string { return "user" }

// AssistantMessage is one model turn. mu guards the live partial that the
// stream producer mutates while consumers read it. Pass it by pointer.
// Timestamp is epoch milliseconds. ThinkingLevel is the level the agent loop
// requested. ProviderThinkingLevel is the provider-native effort.
type AssistantMessage struct {
	mu                    sync.Mutex
	Role                  string                        `json:"role"`
	Content               ContentList                   `json:"content"`
	Api                   Api                           `json:"api"`
	Provider              ProviderId                    `json:"provider"`
	Model                 string                        `json:"model"`
	ResponseModel         *string                       `json:"responseModel,omitzero"`
	ResponseId            *string                       `json:"responseId,omitzero"`
	ProviderThinkingLevel *string                       `json:"providerThinkingLevel,omitzero"`
	ThinkingLevel         *ModelThinkingLevel           `json:"thinkingLevel,omitzero"`
	Diagnostics           []AssistantMessageDiagnostic  `json:"diagnostics,omitzero"`
	Usage                 Usage                         `json:"usage"`
	StopReason            StopReason                    `json:"stopReason"`
	Deferred              *DeferredHandle               `json:"deferred,omitzero"`
	ErrorMessage          *string                       `json:"errorMessage,omitzero"`
	RawStopReason         *string                       `json:"rawStopReason,omitzero"`
	EndTurn               *bool                         `json:"endTurn,omitzero"`
	Timestamp             int64                         `json:"timestamp"`
}

func (*AssistantMessage) isMessage() {}

func (*AssistantMessage) GetRole() string { return "assistant" }

// NestedToolCallStatus is the outcome of a nested tool call.
type NestedToolCallStatus string

const (
	NestedToolCallStatusOk         NestedToolCallStatus = "ok"
	NestedToolCallStatusError      NestedToolCallStatus = "error"
	NestedToolCallStatusUnfinished NestedToolCallStatus = "unfinished"
)

// NestedToolCallRecord is one call a tool made while it ran.
// Arguments is omitted when over the size limit; ArgumentsBytes is then set.
type NestedToolCallRecord struct {
	Id             string               `json:"id"`
	Name           string               `json:"name"`
	Arguments      *jsonx.Object        `json:"arguments,omitzero"`
	ArgumentsBytes *int                 `json:"argumentsBytes,omitzero"`
	Status         NestedToolCallStatus `json:"status"`
	DurationMs     *int64               `json:"durationMs,omitzero"`
	Error          *string              `json:"error,omitzero"`
}

// NestedToolCalls is the bounded record of nested calls. Results are not stored.
type NestedToolCalls struct {
	Calls    []NestedToolCallRecord `json:"calls"`
	Complete bool                   `json:"complete"`
}

// ToolResultMessage is the result of one tool call.
// Details may be null. Usage is the tool's own usage, not the model's.
// NestedCalls is session state and is not sent to the model.
// Timestamp is epoch milliseconds.
type ToolResultMessage struct {
	Role       string            `json:"role"`
	ToolCallId string            `json:"toolCallId"`
	ToolName   string            `json:"toolName"`
	Content    ContentList       `json:"content"`
	Details    jsonx.Opt[any]    `json:"details,omitzero"`
	Usage      *Usage            `json:"usage,omitzero"`
	NestedCalls *NestedToolCalls `json:"nestedCalls,omitzero"`
	IsError    bool              `json:"isError"`
	Timestamp  int64             `json:"timestamp"`
}

func (*ToolResultMessage) isMessage() {}

func (*ToolResultMessage) GetRole() string { return "toolResult" }

// UnknownMessage keeps a message whose role is not recognized.
type UnknownMessage struct{ Raw *jsonx.Object }

func (*UnknownMessage) isMessage() {}

func (*UnknownMessage) GetRole() string { panic("unported: UnknownMessage.GetRole") }

func (*UnknownMessage) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownMessage.MarshalJSON")
}

func (*UnknownMessage) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownMessage.UnmarshalJSON")
}

// MessageList is a JSON array of messages.
type MessageList []Message

func (l *MessageList) UnmarshalJSON(data []byte) error {
	panic("unported: MessageList.UnmarshalJSON")
}

// UnmarshalMessage decodes one message.
func UnmarshalMessage(data []byte) (Message, error) {
	panic("unported: UnmarshalMessage")
}

// DecodeMessage decodes one message from a jsonx value.
func DecodeMessage(v any) (Message, error) {
	panic("unported: DecodeMessage")
}

// ImagesStopReason is why image generation stopped.
type ImagesStopReason string

const (
	ImagesStopReasonStop    ImagesStopReason = "stop"
	ImagesStopReasonError   ImagesStopReason = "error"
	ImagesStopReasonAborted ImagesStopReason = "aborted"
)

// ImagesContext is the input of an image generation.
type ImagesContext struct {
	Input ContentList `json:"input"`
}

// AssistantImages is one image-generation result. Timestamp is epoch milliseconds.
type AssistantImages struct {
	Api          ImageApi         `json:"api"`
	Provider     ProviderId       `json:"provider"`
	Model        string           `json:"model"`
	Output       ContentList      `json:"output"`
	ResponseId   *string          `json:"responseId,omitzero"`
	Usage        *Usage           `json:"usage,omitzero"`
	StopReason   ImagesStopReason `json:"stopReason"`
	ErrorMessage *string          `json:"errorMessage,omitzero"`
	Timestamp    int64            `json:"timestamp"`
}

// ClassifierQuestion is one classifier question. The discriminator is "type".
type ClassifierQuestion interface{ isClassifierQuestion() }

// ClassifierChoiceQuestion picks one labeled criterion.
type ClassifierChoiceQuestion struct {
	Type         string                   `json:"type"`
	Instructions string                   `json:"instructions"`
	Criteria     *omap.Map[string, string] `json:"criteria"`
}

func (*ClassifierChoiceQuestion) isClassifierQuestion() {}

// ClassifierScoreQuestion scores each criterion.
type ClassifierScoreQuestion struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria"`
}

func (*ClassifierScoreQuestion) isClassifierQuestion() {}

// ClassifierBoolCriteria is the true and false labels of a bool question.
type ClassifierBoolCriteria struct {
	True  string `json:"true"`
	False string `json:"false"`
}

// ClassifierBoolQuestion is a yes/no question.
type ClassifierBoolQuestion struct {
	Type         string                 `json:"type"`
	Instructions string                 `json:"instructions"`
	Criteria     ClassifierBoolCriteria `json:"criteria"`
}

func (*ClassifierBoolQuestion) isClassifierQuestion() {}

// UnknownClassifierQuestion keeps an unrecognized question.
type UnknownClassifierQuestion struct{ Raw *jsonx.Object }

func (*UnknownClassifierQuestion) isClassifierQuestion() {}

func (*UnknownClassifierQuestion) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownClassifierQuestion.MarshalJSON")
}

func (*UnknownClassifierQuestion) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownClassifierQuestion.UnmarshalJSON")
}

// UnmarshalClassifierQuestion decodes one question.
func UnmarshalClassifierQuestion(data []byte) (ClassifierQuestion, error) {
	panic("unported: UnmarshalClassifierQuestion")
}

// DecodeClassifierQuestion decodes one question from a jsonx value.
func DecodeClassifierQuestion(v any) (ClassifierQuestion, error) {
	panic("unported: DecodeClassifierQuestion")
}

// ClassifierContext is the input of a classification.
type ClassifierContext struct {
	State     *jsonx.Object                          `json:"state"`
	Questions *omap.Map[string, ClassifierQuestion] `json:"questions"`
}

// ClassifierAnswer is one answer. The discriminator is "type".
type ClassifierAnswer interface{ isClassifierAnswer() }

// ClassifierChoiceAnswer is a choice plus a distribution.
type ClassifierChoiceAnswer struct {
	Type          string                  `json:"type"`
	Choice        string                  `json:"choice"`
	Probabilities *omap.Map[string, float64] `json:"probabilities"`
	Confidence    float64                 `json:"confidence"`
}

func (*ClassifierChoiceAnswer) isClassifierAnswer() {}

// ClassifierScoreAnswer is a numeric score.
type ClassifierScoreAnswer struct {
	Type       string  `json:"type"`
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
}

func (*ClassifierScoreAnswer) isClassifierAnswer() {}

// ClassifierBoolAnswer is P(true).
type ClassifierBoolAnswer struct {
	Type        string  `json:"type"`
	Probability float64 `json:"probability"`
}

func (*ClassifierBoolAnswer) isClassifierAnswer() {}

// UnknownClassifierAnswer keeps an unrecognized answer.
type UnknownClassifierAnswer struct{ Raw *jsonx.Object }

func (*UnknownClassifierAnswer) isClassifierAnswer() {}

func (*UnknownClassifierAnswer) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownClassifierAnswer.MarshalJSON")
}

func (*UnknownClassifierAnswer) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownClassifierAnswer.UnmarshalJSON")
}

// UnmarshalClassifierAnswer decodes one answer.
func UnmarshalClassifierAnswer(data []byte) (ClassifierAnswer, error) {
	panic("unported: UnmarshalClassifierAnswer")
}

// DecodeClassifierAnswer decodes one answer from a jsonx value.
func DecodeClassifierAnswer(v any) (ClassifierAnswer, error) {
	panic("unported: DecodeClassifierAnswer")
}

// ClassifierStopReason is why classification stopped.
type ClassifierStopReason string

const (
	ClassifierStopReasonStop    ClassifierStopReason = "stop"
	ClassifierStopReasonError   ClassifierStopReason = "error"
	ClassifierStopReasonAborted ClassifierStopReason = "aborted"
)

// ClassifierResult is one classification. Timestamp is epoch milliseconds.
type ClassifierResult struct {
	Api          ClassifierApi                            `json:"api"`
	Provider     ProviderId                               `json:"provider"`
	Model        string                                   `json:"model"`
	Answers      *omap.Map[string, ClassifierAnswer]      `json:"answers"`
	Usage        *Usage                                   `json:"usage,omitzero"`
	StopReason   ClassifierStopReason                     `json:"stopReason"`
	ErrorMessage *string                                  `json:"errorMessage,omitzero"`
	Timestamp    int64                                    `json:"timestamp"`
}

// GrammarFormat names an OpenAI grammar encoding.
type GrammarFormat string

const (
	GrammarFormatOpenaiLark  GrammarFormat = "openai_lark"
	GrammarFormatOpenaiRegex GrammarFormat = "openai_regex"
)

// GrammarVariants holds provider grammar source keyed by format.
type GrammarVariants struct {
	OpenaiLark  *string `json:"openai_lark,omitzero"`
	OpenaiRegex *string `json:"openai_regex,omitzero"`
}

// ConstrainedSamplingStrict says whether strict sampling is preferred or required.
type ConstrainedSamplingStrict string

const (
	ConstrainedSamplingStrictPrefer  ConstrainedSamplingStrict = "prefer"
	ConstrainedSamplingStrictRequire ConstrainedSamplingStrict = "require"
)

// ConstrainedSamplingConfig is a provider-side sampling constraint.
// The discriminator is "type".
type ConstrainedSamplingConfig interface{ isConstrainedSamplingConfig() }

// ConstrainedSamplingJsonSchema asks for JSON-schema constrained sampling.
type ConstrainedSamplingJsonSchema struct {
	Type   string                    `json:"type"`
	Strict ConstrainedSamplingStrict `json:"strict"`
}

func (*ConstrainedSamplingJsonSchema) isConstrainedSamplingConfig() {}

// ConstrainedSamplingGrammar carries provider grammar variants.
type ConstrainedSamplingGrammar struct {
	Type     string          `json:"type"`
	Variants GrammarVariants `json:"variants"`
}

func (*ConstrainedSamplingGrammar) isConstrainedSamplingConfig() {}

// UnknownConstrainedSamplingConfig keeps an unrecognized constraint object.
type UnknownConstrainedSamplingConfig struct{ Raw *jsonx.Object }

func (*UnknownConstrainedSamplingConfig) isConstrainedSamplingConfig() {}

func (*UnknownConstrainedSamplingConfig) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownConstrainedSamplingConfig.MarshalJSON")
}

func (*UnknownConstrainedSamplingConfig) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownConstrainedSamplingConfig.UnmarshalJSON")
}

// UnmarshalConstrainedSamplingConfig decodes one constraint object.
func UnmarshalConstrainedSamplingConfig(data []byte) (ConstrainedSamplingConfig, error) {
	panic("unported: UnmarshalConstrainedSamplingConfig")
}

// DecodeConstrainedSamplingConfig decodes one constraint from a jsonx value.
func DecodeConstrainedSamplingConfig(v any) (ConstrainedSamplingConfig, error) {
	panic("unported: DecodeConstrainedSamplingConfig")
}

// ToolConstrainedSampling is false or a ConstrainedSamplingConfig.
// JSON false disables constraints. A JSON object is a config.
type ToolConstrainedSampling interface{ isToolConstrainedSampling() }

// ToolConstrainedSamplingFalse is the JSON false value.
type ToolConstrainedSamplingFalse struct{}

func (ToolConstrainedSamplingFalse) isToolConstrainedSampling() {}

func (ToolConstrainedSamplingFalse) MarshalJSON() ([]byte, error) { return []byte("false"), nil }

func (*ConstrainedSamplingJsonSchema) isToolConstrainedSampling() {}

func (*ConstrainedSamplingGrammar) isToolConstrainedSampling() {}

func (*UnknownConstrainedSamplingConfig) isToolConstrainedSampling() {}

// ToolConstrainedSamplingValue is the JSON form of Tool.constrainedSampling.
// The zero value is omitted.
type ToolConstrainedSamplingValue struct {
	V ToolConstrainedSampling
}

func (v ToolConstrainedSamplingValue) IsZero() bool { return v.V == nil }

func (v ToolConstrainedSamplingValue) MarshalJSON() ([]byte, error) {
	panic("unported: ToolConstrainedSamplingValue.MarshalJSON")
}

func (v *ToolConstrainedSamplingValue) UnmarshalJSON(data []byte) error {
	panic("unported: ToolConstrainedSamplingValue.UnmarshalJSON")
}

// UnmarshalToolConstrainedSampling decodes a tool sampling constraint.
func UnmarshalToolConstrainedSampling(data []byte) (ToolConstrainedSampling, error) {
	panic("unported: UnmarshalToolConstrainedSampling")
}

// DecodeToolConstrainedSampling decodes a tool sampling constraint from a jsonx value.
func DecodeToolConstrainedSampling(v any) (ToolConstrainedSampling, error) {
	panic("unported: DecodeToolConstrainedSampling")
}

// Tool is a tool declaration. Parameters is a TypeBox schema.
type Tool struct {
	Name                 string                       `json:"name"`
	Description          string                       `json:"description"`
	Parameters           *typebox.Schema              `json:"parameters"`
	ConstrainedSampling  ToolConstrainedSamplingValue `json:"constrainedSampling,omitzero"`
}

// ToolReference names a tool without its schema.
type ToolReference struct {
	Name string `json:"name"`
}

// Context is the public stream input. SystemPrompt and Tools are shorthand
// for a leading system message; normalizeContext folds them in.
type Context struct {
	SystemPrompt *string     `json:"systemPrompt,omitzero"`
	Messages     MessageList `json:"messages"`
	Tools        []*Tool     `json:"tools,omitzero"`
}

// TranscriptContext is the normalized context passed to providers.
// The prompt and tools live on the transcript's system messages.
// The unexported brand stops composite literals outside this package;
// only normalizeContext produces a value.
type TranscriptContext struct {
	Messages MessageList `json:"messages"`
	brand    struct{}
}

// AssistantMessageEvent is one event on an assistant stream.
// The discriminator is "type". Partial on a live event is the shared
// response-so-far, not a snapshot.
type AssistantMessageEvent interface{ isAssistantMessageEvent() }

// AssistantMessageEventStart opens a stream.
type AssistantMessageEventStart struct {
	Type    string             `json:"type"`
	Partial *AssistantMessage  `json:"partial"`
}

func (*AssistantMessageEventStart) isAssistantMessageEvent() {}

// AssistantMessageEventTextStart opens a text block. The block text is empty.
type AssistantMessageEventTextStart struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventTextStart) isAssistantMessageEvent() {}

// AssistantMessageEventTextDelta appends text.
type AssistantMessageEventTextDelta struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Delta        string            `json:"delta"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventTextDelta) isAssistantMessageEvent() {}

// AssistantMessageEventTextEnd closes a text block. Content is the full text.
type AssistantMessageEventTextEnd struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Content      string            `json:"content"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventTextEnd) isAssistantMessageEvent() {}

// AssistantMessageEventThinkingStart opens a thinking block.
type AssistantMessageEventThinkingStart struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventThinkingStart) isAssistantMessageEvent() {}

// AssistantMessageEventThinkingDelta appends thinking text.
type AssistantMessageEventThinkingDelta struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Delta        string            `json:"delta"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventThinkingDelta) isAssistantMessageEvent() {}

// AssistantMessageEventThinkingEnd closes a thinking block.
type AssistantMessageEventThinkingEnd struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Content      string            `json:"content"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventThinkingEnd) isAssistantMessageEvent() {}

// AssistantMessageEventToolcallStart opens a tool call.
// Arguments at start are provider-specific.
type AssistantMessageEventToolcallStart struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventToolcallStart) isAssistantMessageEvent() {}

// AssistantMessageEventToolcallDelta appends tool-call JSON.
type AssistantMessageEventToolcallDelta struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	Delta        string            `json:"delta"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventToolcallDelta) isAssistantMessageEvent() {}

// AssistantMessageEventToolcallEnd closes a tool call.
type AssistantMessageEventToolcallEnd struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex"`
	ToolCall     *ToolCall         `json:"toolCall"`
	Partial      *AssistantMessage `json:"partial"`
}

func (*AssistantMessageEventToolcallEnd) isAssistantMessageEvent() {}

// AssistantMessageEventDone ends a successful stream.
// Reason is stop, length, toolUse, or deferred.
type AssistantMessageEventDone struct {
	Type    string             `json:"type"`
	Reason  StopReason         `json:"reason"`
	Message *AssistantMessage  `json:"message"`
}

func (*AssistantMessageEventDone) isAssistantMessageEvent() {}

// AssistantMessageEventError ends a stream with error or aborted.
type AssistantMessageEventError struct {
	Type   string            `json:"type"`
	Reason StopReason        `json:"reason"`
	Error  *AssistantMessage `json:"error"`
}

func (*AssistantMessageEventError) isAssistantMessageEvent() {}

// UnknownAssistantMessageEvent keeps an unrecognized stream event.
type UnknownAssistantMessageEvent struct{ Raw *jsonx.Object }

func (*UnknownAssistantMessageEvent) isAssistantMessageEvent() {}

func (*UnknownAssistantMessageEvent) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownAssistantMessageEvent.MarshalJSON")
}

func (*UnknownAssistantMessageEvent) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownAssistantMessageEvent.UnmarshalJSON")
}

// UnmarshalAssistantMessageEvent decodes one stream event.
func UnmarshalAssistantMessageEvent(data []byte) (AssistantMessageEvent, error) {
	panic("unported: UnmarshalAssistantMessageEvent")
}

// DecodeAssistantMessageEvent decodes one stream event from a jsonx value.
func DecodeAssistantMessageEvent(v any) (AssistantMessageEvent, error) {
	panic("unported: DecodeAssistantMessageEvent")
}

// MaxTokensField selects the OpenAI-compatible max-token request field.
type MaxTokensField string

const (
	MaxTokensFieldMaxCompletionTokens MaxTokensField = "max_completion_tokens"
	MaxTokensFieldMaxTokens           MaxTokensField = "max_tokens"
)

// ThinkingFormat selects how reasoning is sent on OpenAI-compatible APIs.
type ThinkingFormat string

const (
	ThinkingFormatOpenai            ThinkingFormat = "openai"
	ThinkingFormatOpenrouter        ThinkingFormat = "openrouter"
	ThinkingFormatDeepseek          ThinkingFormat = "deepseek"
	ThinkingFormatTogether          ThinkingFormat = "together"
	ThinkingFormatBaseten           ThinkingFormat = "baseten"
	ThinkingFormatZai               ThinkingFormat = "zai"
	ThinkingFormatQwen              ThinkingFormat = "qwen"
	ThinkingFormatChatTemplate      ThinkingFormat = "chat-template"
	ThinkingFormatQwenChatTemplate  ThinkingFormat = "qwen-chat-template"
	ThinkingFormatStringThinking    ThinkingFormat = "string-thinking"
	ThinkingFormatAntLing           ThinkingFormat = "ant-ling"
)

// CacheControlFormat selects prompt-cache markers.
type CacheControlFormat string

const CacheControlFormatAnthropic CacheControlFormat = "anthropic"

// OpenAICompletionsCompat overrides URL-based detection for chat-completions APIs.
// Bool fields use the documented provider default when nil.
type OpenAICompletionsCompat struct {
	SupportsStore                              *bool                          `json:"supportsStore,omitzero"`
	SupportsDeveloperRole                      *bool                          `json:"supportsDeveloperRole,omitzero"`
	SupportsReasoningEffort                    *bool                          `json:"supportsReasoningEffort,omitzero"`
	SupportsUsageInStreaming                   *bool                          `json:"supportsUsageInStreaming,omitzero"`
	SupportsFinishReason                       *bool                          `json:"supportsFinishReason,omitzero"`
	MaxTokensField                             *MaxTokensField                `json:"maxTokensField,omitzero"`
	RequiresToolResultName                     *bool                          `json:"requiresToolResultName,omitzero"`
	RequiresAssistantAfterToolResult           *bool                          `json:"requiresAssistantAfterToolResult,omitzero"`
	RequiresThinkingAsText                     *bool                          `json:"requiresThinkingAsText,omitzero"`
	RequiresReasoningContentOnAssistantMessages *bool                         `json:"requiresReasoningContentOnAssistantMessages,omitzero"`
	ThinkingFormat                             *ThinkingFormat                `json:"thinkingFormat,omitzero"`
	ChatTemplateKwargs                         *omap.Map[string, ChatTemplateKwargValue] `json:"chatTemplateKwargs,omitzero"`
	ChatTemplateArgs                           *omap.Map[string, ChatTemplateKwargValue] `json:"chatTemplateArgs,omitzero"`
	OpenRouterRouting                          *OpenRouterRouting             `json:"openRouterRouting,omitzero"`
	VercelGatewayRouting                       *VercelGatewayRouting          `json:"vercelGatewayRouting,omitzero"`
	ZaiToolStream                              *bool                          `json:"zaiToolStream,omitzero"`
	ThinkingTokenBudgetField                   *ThinkingTokenBudgetField      `json:"thinkingTokenBudgetField,omitzero"`
	SupportsThinkingTokenBudget                *bool                          `json:"supportsThinkingTokenBudget,omitzero"`
	SupportsOpenAIGrammarTools                 *bool                          `json:"supportsOpenAIGrammarTools,omitzero"`
	SupportsMidConvoSystemMessages             *bool                          `json:"supportsMidConvoSystemMessages,omitzero"`
	SupportsMidConvoToolAdditions              *bool                          `json:"supportsMidConvoToolAdditions,omitzero"`
	SupportsStrictMode                         *bool                          `json:"supportsStrictMode,omitzero"`
	CacheControlFormat                         *CacheControlFormat            `json:"cacheControlFormat,omitzero"`
	SendSessionAffinityHeaders                 *bool                          `json:"sendSessionAffinityHeaders,omitzero"`
	SessionAffinityFormat                      *SessionAffinityFormat         `json:"sessionAffinityFormat,omitzero"`
	SupportsLongCacheRetention                 *bool                          `json:"supportsLongCacheRetention,omitzero"`
	VllmPriority                               *float64                       `json:"vllmPriority,omitzero"`
}

func (*OpenAICompletionsCompat) isModelCompat() {}

// OpenAIResponsesCompat is compatibility settings for OpenAI Responses APIs.
type OpenAIResponsesCompat struct {
	SupportsDeveloperRole           *bool                  `json:"supportsDeveloperRole,omitzero"`
	SupportsMidConvoSystemMessages  *bool                  `json:"supportsMidConvoSystemMessages,omitzero"`
	SessionAffinityFormat           *SessionAffinityFormat `json:"sessionAffinityFormat,omitzero"`
	SupportsLongCacheRetention      *bool                  `json:"supportsLongCacheRetention,omitzero"`
	SupportsStrictMode              *bool                  `json:"supportsStrictMode,omitzero"`
	SupportsOpenAIGrammarTools      *bool                  `json:"supportsOpenAIGrammarTools,omitzero"`
	SupportsAdditionalTools         *bool                  `json:"supportsAdditionalTools,omitzero"`
	SupportsToolSearch              *bool                  `json:"supportsToolSearch,omitzero"`
	SupportsExplicitPromptCacheMode *bool                  `json:"supportsExplicitPromptCacheMode,omitzero"`
	SupportsMaxOutputTokens         *bool                  `json:"supportsMaxOutputTokens,omitzero"`
}

func (*OpenAIResponsesCompat) isModelCompat() {}

// AnthropicSessionAffinityFormat is the Anthropic-compat session header format.
// Unset sends x-session-affinity. Openrouter sends x-session-id.
type AnthropicSessionAffinityFormat string

const AnthropicSessionAffinityFormatOpenrouter AnthropicSessionAffinityFormat = "openrouter"

// AnthropicMessagesCompat is compatibility settings for Anthropic Messages APIs.
type AnthropicMessagesCompat struct {
	SupportsEagerToolInputStreaming *bool                           `json:"supportsEagerToolInputStreaming,omitzero"`
	SupportsLongCacheRetention      *bool                           `json:"supportsLongCacheRetention,omitzero"`
	SendSessionAffinityHeaders      *bool                           `json:"sendSessionAffinityHeaders,omitzero"`
	SessionAffinityFormat           *AnthropicSessionAffinityFormat `json:"sessionAffinityFormat,omitzero"`
	SupportsCacheControlOnTools     *bool                           `json:"supportsCacheControlOnTools,omitzero"`
	SupportsTemperature             *bool                           `json:"supportsTemperature,omitzero"`
	ForceAdaptiveThinking           *bool                           `json:"forceAdaptiveThinking,omitzero"`
	AllowEmptySignature             *bool                           `json:"allowEmptySignature,omitzero"`
	SupportsStrictTools             *bool                           `json:"supportsStrictTools,omitzero"`
	SupportsMidConvoEffort          *bool                           `json:"supportsMidConvoEffort,omitzero"`
	SupportsMidConvoSystemMessages  *bool                           `json:"supportsMidConvoSystemMessages,omitzero"`
	SupportsMidConvoToolChanges     *bool                           `json:"supportsMidConvoToolChanges,omitzero"`
	AllowedFallbackModels           []AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitzero"`
}

func (*AnthropicMessagesCompat) isModelCompat() {}

// BedrockCompat is compatibility settings for Amazon Bedrock models.
type BedrockCompat struct {
	SupportsStrictMode *bool `json:"supportsStrictMode,omitzero"`
}

func (*BedrockCompat) isModelCompat() {}

// MistralConversationsCompat is compatibility settings for the Mistral chat API.
type MistralConversationsCompat struct {
	SupportsMidConvoSystemMessages *bool `json:"supportsMidConvoSystemMessages,omitzero"`
}

func (*MistralConversationsCompat) isModelCompat() {}

// ModelCompat is the untagged compat object on a chat model.
// The concrete type depends on the model's API.
type ModelCompat interface{ isModelCompat() }

// UnknownModelCompat keeps compat JSON that matches no known struct.
type UnknownModelCompat struct{ Raw *jsonx.Object }

func (*UnknownModelCompat) isModelCompat() {}

func (*UnknownModelCompat) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownModelCompat.MarshalJSON")
}

func (*UnknownModelCompat) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownModelCompat.UnmarshalJSON")
}

// ModelCompatValue is the JSON form of Model.compat. The zero value is omitted.
type ModelCompatValue struct {
	V ModelCompat
}

func (v ModelCompatValue) IsZero() bool { return v.V == nil }

func (v ModelCompatValue) MarshalJSON() ([]byte, error) {
	panic("unported: ModelCompatValue.MarshalJSON")
}

func (v *ModelCompatValue) UnmarshalJSON(data []byte) error {
	panic("unported: ModelCompatValue.UnmarshalJSON")
}

// UnmarshalModelCompat decodes a model compat object.
func UnmarshalModelCompat(data []byte) (ModelCompat, error) {
	panic("unported: UnmarshalModelCompat")
}

// DecodeModelCompat decodes a model compat object from a jsonx value.
func DecodeModelCompat(v any) (ModelCompat, error) {
	panic("unported: DecodeModelCompat")
}

// OpenRouterDataCollection is the OpenRouter data-collection routing flag.
type OpenRouterDataCollection string

const (
	OpenRouterDataCollectionDeny  OpenRouterDataCollection = "deny"
	OpenRouterDataCollectionAllow OpenRouterDataCollection = "allow"
)

// OpenRouterRoutingSort is a sort string or a {by, partition} object.
type OpenRouterRoutingSort interface{ isOpenRouterRoutingSort() }

// OpenRouterRoutingSortString is a named sort strategy.
type OpenRouterRoutingSortString string

func (OpenRouterRoutingSortString) isOpenRouterRoutingSort() {}

// OpenRouterRoutingSortObject is a structured sort.
// Partition null is the Opt null state.
type OpenRouterRoutingSortObject struct {
	By        *string           `json:"by,omitzero"`
	Partition jsonx.Opt[string] `json:"partition,omitzero"`
}

func (*OpenRouterRoutingSortObject) isOpenRouterRoutingSort() {}

// UnknownOpenRouterRoutingSort keeps an unrecognized sort value.
type UnknownOpenRouterRoutingSort struct{ Raw *jsonx.Object }

func (*UnknownOpenRouterRoutingSort) isOpenRouterRoutingSort() {}

func (*UnknownOpenRouterRoutingSort) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownOpenRouterRoutingSort.MarshalJSON")
}

func (*UnknownOpenRouterRoutingSort) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownOpenRouterRoutingSort.UnmarshalJSON")
}

// OpenRouterRoutingSortValue is the JSON form of OpenRouterRouting.sort.
type OpenRouterRoutingSortValue struct {
	V OpenRouterRoutingSort
}

func (v OpenRouterRoutingSortValue) IsZero() bool { return v.V == nil }

func (v OpenRouterRoutingSortValue) MarshalJSON() ([]byte, error) {
	panic("unported: OpenRouterRoutingSortValue.MarshalJSON")
}

func (v *OpenRouterRoutingSortValue) UnmarshalJSON(data []byte) error {
	panic("unported: OpenRouterRoutingSortValue.UnmarshalJSON")
}

// UnmarshalOpenRouterRoutingSort decodes a sort value.
func UnmarshalOpenRouterRoutingSort(data []byte) (OpenRouterRoutingSort, error) {
	panic("unported: UnmarshalOpenRouterRoutingSort")
}

// DecodeOpenRouterRoutingSort decodes a sort value from a jsonx value.
func DecodeOpenRouterRoutingSort(v any) (OpenRouterRoutingSort, error) {
	panic("unported: DecodeOpenRouterRoutingSort")
}

// OpenRouterPrice is a number or a string price.
type OpenRouterPrice interface{ isOpenRouterPrice() }

// OpenRouterPriceNumber is a numeric price.
type OpenRouterPriceNumber float64

func (OpenRouterPriceNumber) isOpenRouterPrice() {}

// OpenRouterPriceString is a string price.
type OpenRouterPriceString string

func (OpenRouterPriceString) isOpenRouterPrice() {}

// UnknownOpenRouterPrice keeps an unrecognized price.
type UnknownOpenRouterPrice struct{ Raw *jsonx.Object }

func (*UnknownOpenRouterPrice) isOpenRouterPrice() {}

func (*UnknownOpenRouterPrice) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownOpenRouterPrice.MarshalJSON")
}

func (*UnknownOpenRouterPrice) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownOpenRouterPrice.UnmarshalJSON")
}

// OpenRouterPriceValue is one optional max_price field.
type OpenRouterPriceValue struct {
	V OpenRouterPrice
}

func (v OpenRouterPriceValue) IsZero() bool { return v.V == nil }

func (v OpenRouterPriceValue) MarshalJSON() ([]byte, error) {
	panic("unported: OpenRouterPriceValue.MarshalJSON")
}

func (v *OpenRouterPriceValue) UnmarshalJSON(data []byte) error {
	panic("unported: OpenRouterPriceValue.UnmarshalJSON")
}

// UnmarshalOpenRouterPrice decodes a price.
func UnmarshalOpenRouterPrice(data []byte) (OpenRouterPrice, error) {
	panic("unported: UnmarshalOpenRouterPrice")
}

// DecodeOpenRouterPrice decodes a price from a jsonx value.
func DecodeOpenRouterPrice(v any) (OpenRouterPrice, error) {
	panic("unported: DecodeOpenRouterPrice")
}

// OpenRouterMaxPrice is the maximum price per million tokens, in USD.
type OpenRouterMaxPrice struct {
	Prompt     OpenRouterPriceValue `json:"prompt,omitzero"`
	Completion OpenRouterPriceValue `json:"completion,omitzero"`
	Image      OpenRouterPriceValue `json:"image,omitzero"`
	Audio      OpenRouterPriceValue `json:"audio,omitzero"`
	Request    OpenRouterPriceValue `json:"request,omitzero"`
}

// OpenRouterThroughput is a number (p50) or percentile cutoffs.
// The same shape is used for latency, whose number is seconds.
type OpenRouterThroughput interface{ isOpenRouterThroughput() }

// OpenRouterThroughputNumber is a single cutoff applied at p50.
type OpenRouterThroughputNumber float64

func (OpenRouterThroughputNumber) isOpenRouterThroughput() {}

// OpenRouterPercentiles is percentile-specific cutoffs.
type OpenRouterPercentiles struct {
	P50 *float64 `json:"p50,omitzero"`
	P75 *float64 `json:"p75,omitzero"`
	P90 *float64 `json:"p90,omitzero"`
	P99 *float64 `json:"p99,omitzero"`
}

func (*OpenRouterPercentiles) isOpenRouterThroughput() {}

// UnknownOpenRouterThroughput keeps an unrecognized cutoff value.
type UnknownOpenRouterThroughput struct{ Raw *jsonx.Object }

func (*UnknownOpenRouterThroughput) isOpenRouterThroughput() {}

func (*UnknownOpenRouterThroughput) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownOpenRouterThroughput.MarshalJSON")
}

func (*UnknownOpenRouterThroughput) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownOpenRouterThroughput.UnmarshalJSON")
}

// OpenRouterThroughputValue is the JSON form of a throughput or latency preference.
type OpenRouterThroughputValue struct {
	V OpenRouterThroughput
}

func (v OpenRouterThroughputValue) IsZero() bool { return v.V == nil }

func (v OpenRouterThroughputValue) MarshalJSON() ([]byte, error) {
	panic("unported: OpenRouterThroughputValue.MarshalJSON")
}

func (v *OpenRouterThroughputValue) UnmarshalJSON(data []byte) error {
	panic("unported: OpenRouterThroughputValue.UnmarshalJSON")
}

// UnmarshalOpenRouterThroughput decodes a throughput or latency preference.
func UnmarshalOpenRouterThroughput(data []byte) (OpenRouterThroughput, error) {
	panic("unported: UnmarshalOpenRouterThroughput")
}

// DecodeOpenRouterThroughput decodes a preference from a jsonx value.
func DecodeOpenRouterThroughput(v any) (OpenRouterThroughput, error) {
	panic("unported: DecodeOpenRouterThroughput")
}

// OpenRouterRouting is OpenRouter provider routing, sent as the provider field.
// Field names keep the TS snake_case with only the first letter uppercased.
type OpenRouterRouting struct {
	Allow_fallbacks           *bool                         `json:"allow_fallbacks,omitzero"`
	Require_parameters        *bool                         `json:"require_parameters,omitzero"`
	Data_collection           *OpenRouterDataCollection     `json:"data_collection,omitzero"`
	Zdr                       *bool                         `json:"zdr,omitzero"`
	Enforce_distillable_text  *bool                         `json:"enforce_distillable_text,omitzero"`
	Order                     []string                      `json:"order,omitzero"`
	Only                      []string                      `json:"only,omitzero"`
	Ignore                    []string                      `json:"ignore,omitzero"`
	Quantizations             []string                      `json:"quantizations,omitzero"`
	Sort                      OpenRouterRoutingSortValue    `json:"sort,omitzero"`
	Max_price                 *OpenRouterMaxPrice           `json:"max_price,omitzero"`
	Preferred_min_throughput  OpenRouterThroughputValue     `json:"preferred_min_throughput,omitzero"`
	Preferred_max_latency     OpenRouterThroughputValue     `json:"preferred_max_latency,omitzero"`
}

// VercelGatewayRouting is Vercel AI Gateway provider routing.
type VercelGatewayRouting struct {
	Only  []string `json:"only,omitzero"`
	Order []string `json:"order,omitzero"`
}

// ModelCostRates is dollars per million tokens.
type ModelCostRates struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ModelCostTier applies when total input usage exceeds InputTokensAbove.
// The highest matching threshold applies to the whole request.
type ModelCostTier struct {
	ModelCostRates
	InputTokensAbove int `json:"inputTokensAbove"`
}

// ModelCost is catalog pricing, plus optional request-wide tiers.
type ModelCost struct {
	ModelCostRates
	Tiers []ModelCostTier `json:"tiers,omitzero"`
}

// ModelImageResizeOptions is the cache-safe resize applied before an image
// enters conversation history. MaxBytes is the base64 payload cap.
type ModelImageResizeOptions struct {
	MaxWidth    *int `json:"maxWidth,omitzero"`
	MaxHeight   *int `json:"maxHeight,omitzero"`
	MaxBytes    *int `json:"maxBytes,omitzero"`
	JpegQuality *int `json:"jpegQuality,omitzero"`
}

// ModelImageInputLimits caps images on a provider message or request.
type ModelImageInputLimits struct {
	Resize        *ModelImageResizeOptions `json:"resize,omitzero"`
	MaxPerMessage *int                     `json:"maxPerMessage,omitzero"`
	MaxPerRequest *int                     `json:"maxPerRequest,omitzero"`
}

// ModelInputLimits is provider input size and image preprocessing metadata.
type ModelInputLimits struct {
	MaxRequestBytes *int                   `json:"maxRequestBytes,omitzero"`
	Images          *ModelImageInputLimits `json:"images,omitzero"`
}

// ModelModality is a text or image modality.
type ModelModality string

const (
	ModelModalityText  ModelModality = "text"
	ModelModalityImage ModelModality = "image"
)

// BaseModel is the catalog fields shared by every model type.
// Api is a plain string: Model loses the TS API generic.
type BaseModel struct {
	Id          string                    `json:"id"`
	Name        string                    `json:"name"`
	Api         string                    `json:"api"`
	Provider    ProviderId                `json:"provider"`
	BaseUrl     string                    `json:"baseUrl"`
	Input       []ModelModality           `json:"input"`
	InputLimits *ModelInputLimits         `json:"inputLimits,omitzero"`
	Cost        ModelCost                 `json:"cost"`
	Headers     *omap.Map[string, string] `json:"headers,omitzero"`
}

// ModelType is what a catalog entry is for.
type ModelType string

const (
	ModelTypeChat       ModelType = "chat"
	ModelTypeImage      ModelType = "image"
	ModelTypeClassifier ModelType = "classifier"
)

// Model is a chat model. The TS generic API parameter is Api string on BaseModel.
// A missing Type means chat. ContextWindow and MaxTokens are token counts.
type Model struct {
	BaseModel
	Type             *ModelType        `json:"type,omitzero"`
	Reasoning        bool              `json:"reasoning"`
	ThinkingLevelMap *ThinkingLevelMap `json:"thinkingLevelMap,omitzero"`
	PromptCache      *ModelPromptCache `json:"promptCache,omitzero"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	SamplingParams   *jsonx.Object     `json:"samplingParams,omitzero"`
	Compat           ModelCompatValue  `json:"compat,omitzero"`
}

func (*Model) isAnyModel() {}

// ImageModel is an image-generation catalog entry. Type is "image".
// Output always includes image; text means the model can also return text.
type ImageModel struct {
	BaseModel
	Type   ModelType       `json:"type"`
	Output []ModelModality `json:"output"`
}

func (*ImageModel) isAnyModel() {}

// ClassifierModel is a classifier catalog entry. Type is "classifier".
// ContextWindow is a token count.
type ClassifierModel struct {
	BaseModel
	Type          ModelType `json:"type"`
	ContextWindow int       `json:"contextWindow"`
}

func (*ClassifierModel) isAnyModel() {}

// AnyModel is any catalog entry. Narrow with the model-operations helpers.
// ModelTypeMap is a TypeScript type-level map and has no Go form.
type AnyModel interface{ isAnyModel() }

// UnknownAnyModel keeps a catalog entry whose type is not chat, image, or classifier.
type UnknownAnyModel struct{ Raw *jsonx.Object }

func (*UnknownAnyModel) isAnyModel() {}

func (*UnknownAnyModel) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownAnyModel.MarshalJSON")
}

func (*UnknownAnyModel) UnmarshalJSON(data []byte) error {
	panic("unported: UnknownAnyModel.UnmarshalJSON")
}

// AnyModelList is a JSON array of catalog entries.
type AnyModelList []AnyModel

func (l *AnyModelList) UnmarshalJSON(data []byte) error {
	panic("unported: AnyModelList.UnmarshalJSON")
}

// UnmarshalAnyModel decodes one catalog entry.
func UnmarshalAnyModel(data []byte) (AnyModel, error) {
	panic("unported: UnmarshalAnyModel")
}

// DecodeAnyModel decodes one catalog entry from a jsonx value.
func DecodeAnyModel(v any) (AnyModel, error) {
	panic("unported: DecodeAnyModel")
}

var (
	_ ProviderStreamOptions = (*StreamOptions)(nil)
	_ ProviderStreamOptions = (*SimpleStreamOptions)(nil)
	_ ProviderImagesOptions = (*ImagesOptions)(nil)
)
