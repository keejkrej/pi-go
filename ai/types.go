// Ported from packages/ai/src/types.ts (pi v1.5.2).

package ai

import (
	"context"
	"fmt"
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
	KnownApiOpenaiCompletions     KnownApi = "openai-completions"
	KnownApiMistralConversations  KnownApi = "mistral-conversations"
	KnownApiOpenaiResponses       KnownApi = "openai-responses"
	KnownApiAzureOpenaiResponses  KnownApi = "azure-openai-responses"
	KnownApiOpenaiCodexResponses  KnownApi = "openai-codex-responses"
	KnownApiAnthropicMessages     KnownApi = "anthropic-messages"
	KnownApiBedrockConverseStream KnownApi = "bedrock-converse-stream"
	KnownApiGoogleGenerativeAi    KnownApi = "google-generative-ai"
	KnownApiGoogleVertex          KnownApi = "google-vertex"
	KnownApiPiMessages            KnownApi = "pi-messages"
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
	KnownClassifierApiOpenaiDecisions              KnownClassifierApi = "openai-decisions"
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
	KnownProviderAzure                     KnownProvider = "azure"
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
	KnownProviderQwenTokenPlanIndividualCn KnownProvider = "qwen-token-plan-individual-cn"
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

// SamplingParams is arbitrary sampling parameters merged into provider requests.
type SamplingParams = *jsonx.Object

// SamplingParamsByThinkingLevel maps pi thinking levels to sampling parameter overrides.
type SamplingParamsByThinkingLevel struct {
	Off     SamplingParams `json:"off,omitzero"`
	Minimal SamplingParams `json:"minimal,omitzero"`
	Low     SamplingParams `json:"low,omitzero"`
	Medium  SamplingParams `json:"medium,omitzero"`
	High    SamplingParams `json:"high,omitzero"`
	Xhigh   SamplingParams `json:"xhigh,omitzero"`
	Max     SamplingParams `json:"max,omitzero"`
}

// Get returns the sampling params configured for the given thinking level, if any.
func (s *SamplingParamsByThinkingLevel) Get(level ModelThinkingLevel) SamplingParams {
	if s == nil {
		return nil
	}
	switch level {
	case ModelThinkingLevelOff:
		return s.Off
	case ModelThinkingLevelMinimal:
		return s.Minimal
	case ModelThinkingLevelLow:
		return s.Low
	case ModelThinkingLevelMedium:
		return s.Medium
	case ModelThinkingLevelHigh:
		return s.High
	case ModelThinkingLevelXhigh:
		return s.Xhigh
	case ModelThinkingLevelMax:
		return s.Max
	default:
		return nil
	}
}

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

func (u *UnknownChatTemplateKwargValue) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownChatTemplateKwargValue) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// UnmarshalChatTemplateKwargValue decodes one kwarg value.
func UnmarshalChatTemplateKwargValue(data []byte) (ChatTemplateKwargValue, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeChatTemplateKwargValue(v)
}

// DecodeChatTemplateKwargValue decodes one kwarg value from a jsonx value.
func DecodeChatTemplateKwargValue(v any) (ChatTemplateKwargValue, error) {
	switch x := v.(type) {
	case nil:
		return ChatTemplateKwargNull{}, nil
	case string:
		return ChatTemplateKwargString(x), nil
	case float64:
		return ChatTemplateKwargNumber(x), nil
	case int:
		return ChatTemplateKwargNumber(float64(x)), nil
	case int64:
		return ChatTemplateKwargNumber(float64(x)), nil
	case bool:
		return ChatTemplateKwargBool(x), nil
	case *jsonx.Object:
		if rawVar, ok := x.Get("$var"); ok {
			if name, ok := rawVar.(string); ok {
				switch ChatTemplateKwargVarName(name) {
				case ChatTemplateKwargVarNameThinkingEnabled,
					ChatTemplateKwargVarNameThinkingEffort,
					ChatTemplateKwargVarNameThinkingBudget:
					var out ChatTemplateKwargVar
					if err := jsonx.Decode(x, &out); err != nil {
						return nil, err
					}
					return &out, nil
				}
			}
		}
		return &UnknownChatTemplateKwargValue{Raw: x}, nil
	default:
		return nil, fmt.Errorf("invalid chat template kwarg value: %T", v)
	}
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
	SessionAffinityFormatOpenai          SessionAffinityFormat = "openai"
	SessionAffinityFormatOpenaiNosession SessionAffinityFormat = "openai-nosession"
	SessionAffinityFormatOpenrouter      SessionAffinityFormat = "openrouter"
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
	TelemetryContext telemetry.TelemetryContext                            `json:"-"`
	ApiKey           *string                                               `json:"apiKey,omitzero"`
	HTTPClient       *http.Client                                          `json:"-"`
	Env              ProviderEnv                                           `json:"env,omitzero"`
	OnPayload        func(payload any, model *TModel) (any, error)         `json:"-"`
	OnResponse       func(response *ProviderResponse, model *TModel) error `json:"-"`
	Headers          ProviderHeaders                                       `json:"headers,omitzero"`
	Identity         *ClientIdentity                                       `json:"identity,omitzero"`
	TimeoutMs        *int64                                                `json:"timeoutMs,omitzero"`
	MaxRetries       *int                                                  `json:"maxRetries,omitzero"`
	MaxRetryDelayMs  *int64                                                `json:"maxRetryDelayMs,omitzero"`
}

// StreamOptions is the shared streaming request.
// SamplingParams merges into the body after named fields; keys here win.
// CacheRetention defaults to "short" when unset. MaxRetryDelayMs defaults to
// 60000 at the call site; zero disables the cap.
type StreamOptions struct {
	ProviderRequestOptions[Model]
	OnProviderStreamEvent     func(data any, model *Model) error `json:"-"`
	Temperature               *float64                           `json:"temperature,omitzero"`
	SamplingParams            *jsonx.Object                      `json:"samplingParams,omitzero"`
	MaxTokens                 *int                               `json:"maxTokens,omitzero"`
	Transport                 *Transport                         `json:"transport,omitzero"`
	CacheRetention            *CacheRetention                    `json:"cacheRetention,omitzero"`
	SessionId                 *string                            `json:"sessionId,omitzero"`
	DisableContinuation       *bool                              `json:"disableContinuation,omitzero"`
	WebsocketConnectTimeoutMs *int64                             `json:"websocketConnectTimeoutMs,omitzero"`
	Metadata                  *jsonx.Object                      `json:"metadata,omitzero"`
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
	Stream         func(ctx context.Context, model *Model, c *TranscriptContext, options ProviderStreamOptions) *AssistantMessageEventStream
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

func (u *UnknownSimpleStreamDeferred) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownSimpleStreamDeferred) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// SimpleStreamDeferredValue is the JSON form of StreamOptions-level deferred.
// The zero value is omitted.
type SimpleStreamDeferredValue struct {
	V SimpleStreamDeferred
}

func (v SimpleStreamDeferredValue) IsZero() bool { return v.V == nil }

func (v SimpleStreamDeferredValue) MarshalJSON() ([]byte, error) {
	if v.V == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(v.V)
}

func (v *SimpleStreamDeferredValue) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		v.V = nil
		return nil
	}
	dec, err := DecodeSimpleStreamDeferred(parsed)
	if err != nil {
		return err
	}
	v.V = dec
	return nil
}

// UnmarshalSimpleStreamDeferred decodes a deferred flag.
func UnmarshalSimpleStreamDeferred(data []byte) (SimpleStreamDeferred, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeSimpleStreamDeferred(parsed)
}

// DecodeSimpleStreamDeferred decodes a deferred flag from a jsonx value.
func DecodeSimpleStreamDeferred(v any) (SimpleStreamDeferred, error) {
	switch x := v.(type) {
	case bool:
		return SimpleStreamDeferredBool(x), nil
	case *jsonx.Object:
		if rawWin, hasWin := x.Get("window"); hasWin {
			if s, ok := rawWin.(string); ok {
				switch DeferredWindow(s) {
				case DeferredWindow15m, DeferredWindow1h, DeferredWindow24h:
					var out SimpleStreamDeferredWindow
					if err := jsonx.Decode(x, &out); err != nil {
						return nil, err
					}
					return &out, nil
				}
			}
			return &UnknownSimpleStreamDeferred{Raw: x}, nil
		}
		if x.Len() == 0 {
			return &SimpleStreamDeferredWindow{}, nil
		}
		return &UnknownSimpleStreamDeferred{Raw: x}, nil
	default:
		return nil, fmt.Errorf("invalid deferred value: %T", v)
	}
}

// SimpleStreamOptions is the uniform request passed to streamSimple.
type SimpleStreamOptions struct {
	StreamOptions
	ToolChoice      *ToolChoice               `json:"toolChoice,omitzero"`
	Reasoning       *ThinkingLevel            `json:"reasoning,omitzero"`
	Deferred        SimpleStreamDeferredValue `json:"deferred,omitzero"`
	ThinkingBudgets *ThinkingBudgets          `json:"thinkingBudgets,omitzero"`
}

// StreamFunction is the uniform chat stream contract from docs/porting.md.
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
	TextSignaturePhaseCommentary  TextSignaturePhase = "commentary"
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
	ArgumentsError   *string       `json:"argumentsError,omitzero"`
}

func (*ToolCall) isContent() {}

// UnknownContent keeps a content block whose type is not recognized.
type UnknownContent struct{ Raw *jsonx.Object }

func (*UnknownContent) isContent() {}

func (u *UnknownContent) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownContent) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// ContentList is a JSON array of content blocks.
type ContentList []Content

func (l *ContentList) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*l = nil
		return nil
	}
	arr, ok := parsed.([]any)
	if !ok {
		return fmt.Errorf("expected content array, got %T", parsed)
	}
	out := make(ContentList, len(arr))
	for i, elem := range arr {
		c, err := DecodeContent(elem)
		if err != nil {
			return err
		}
		out[i] = c
	}
	*l = out
	return nil
}

// UnmarshalContent decodes one content block.
func UnmarshalContent(data []byte) (Content, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeContent(parsed)
}

// DecodeContent decodes one content block from a jsonx value.
func DecodeContent(v any) (Content, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected content object, got %T", v)
	}
	rawType, _ := obj.Get("type")
	typ, _ := rawType.(string)
	switch typ {
	case "text":
		var out TextContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "thinking":
		var out ThinkingContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "image":
		var out ImageContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolCall":
		var out ToolCall
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownContent{Raw: obj}, nil
	}
}

// ImagesInputContent is a text or image block on an image request.
type ImagesInputContent = Content

// ImagesOutputContent is a text or image block on an image response.
type ImagesOutputContent = Content

// UsageCost is the dollar cost breakdown inside Usage.
type UsageCost struct {
	Input             float64 `json:"input"`
	Output            float64 `json:"output"`
	CacheRead         float64 `json:"cacheRead"`
	CacheWrite        float64 `json:"cacheWrite"`
	Total             float64 `json:"total"`
	TotalFromProvider *bool   `json:"totalFromProvider,omitzero"`
}

// Usage is token accounting. Token counts are integers. Cost is dollars.
// Reasoning, when set, is a subset of Output. CacheWrite1h is the 1h portion
// of CacheWrite and is set only when the provider reports the split.
type Usage struct {
	Input        int       `json:"input"`
	Output       int       `json:"output"`
	CacheRead    int       `json:"cacheRead"`
	CacheWrite   int       `json:"cacheWrite"`
	CacheWrite1h *int      `json:"cacheWrite1h,omitzero"`
	Reasoning    *int      `json:"reasoning,omitzero"`
	TotalTokens  int       `json:"totalTokens"`
	Cost         UsageCost `json:"cost"`
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
	if c.Text != nil {
		return jsonx.Marshal(*c.Text)
	}
	if c.Blocks != nil {
		return jsonx.Marshal(c.Blocks)
	}
	return []byte("null"), nil
}

func (c *SystemMessageContent) UnmarshalJSON(data []byte) error {
	return typUnmarshalTextOrBlocks(data, &c.Text, &c.Blocks)
}

// UserMessageContent is a user message body: a string, or text and image blocks.
// Text and Blocks are interpreted the same way as SystemMessageContent.
type UserMessageContent struct {
	Text   *string
	Blocks ContentList
}

func (c UserMessageContent) MarshalJSON() ([]byte, error) {
	if c.Text != nil {
		return jsonx.Marshal(*c.Text)
	}
	if c.Blocks != nil {
		return jsonx.Marshal(c.Blocks)
	}
	return []byte("null"), nil
}

func (c *UserMessageContent) UnmarshalJSON(data []byte) error {
	return typUnmarshalTextOrBlocks(data, &c.Text, &c.Blocks)
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
	Role                  string                       `json:"role"`
	Content               ContentList                  `json:"content"`
	Api                   Api                          `json:"api"`
	Provider              ProviderId                   `json:"provider"`
	Model                 string                       `json:"model"`
	ResponseModel         *string                      `json:"responseModel,omitzero"`
	ResponseId            *string                      `json:"responseId,omitzero"`
	ProviderThinkingLevel *string                      `json:"providerThinkingLevel,omitzero"`
	ThinkingLevel         *ModelThinkingLevel          `json:"thinkingLevel,omitzero"`
	Diagnostics           []AssistantMessageDiagnostic `json:"diagnostics,omitzero"`
	Usage                 Usage                        `json:"usage"`
	StopReason            StopReason                   `json:"stopReason"`
	Deferred              *DeferredHandle              `json:"deferred,omitzero"`
	ErrorMessage          *string                      `json:"errorMessage,omitzero"`
	RawStopReason         *string                      `json:"rawStopReason,omitzero"`
	EndTurn               *bool                        `json:"endTurn,omitzero"`
	Timestamp             int64                        `json:"timestamp"`
	DurationMs            *int64                       `json:"durationMs,omitzero"`
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
	Role        string           `json:"role"`
	ToolCallId  string           `json:"toolCallId"`
	ToolName    string           `json:"toolName"`
	Content     ContentList      `json:"content"`
	Details     jsonx.Opt[any]   `json:"details,omitzero"`
	Usage       *Usage           `json:"usage,omitzero"`
	NestedCalls *NestedToolCalls `json:"nestedCalls,omitzero"`
	IsError     bool             `json:"isError"`
	Timestamp   int64            `json:"timestamp"`
	DurationMs  *int64           `json:"durationMs,omitzero"`
}

func (*ToolResultMessage) isMessage() {}

func (*ToolResultMessage) GetRole() string { return "toolResult" }

// UnknownMessage keeps a message whose role is not recognized.
type UnknownMessage struct{ Raw *jsonx.Object }

func (*UnknownMessage) isMessage() {}

func (u *UnknownMessage) GetRole() string {
	if u == nil || u.Raw == nil {
		return ""
	}
	raw, _ := u.Raw.Get("role")
	role, _ := raw.(string)
	return role
}

func (u *UnknownMessage) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownMessage) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// MessageList is a JSON array of messages.
type MessageList []Message

func (l *MessageList) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*l = nil
		return nil
	}
	arr, ok := parsed.([]any)
	if !ok {
		return fmt.Errorf("expected message array, got %T", parsed)
	}
	out := make(MessageList, len(arr))
	for i, elem := range arr {
		m, err := DecodeMessage(elem)
		if err != nil {
			return err
		}
		out[i] = m
	}
	*l = out
	return nil
}

// UnmarshalMessage decodes one message.
func UnmarshalMessage(data []byte) (Message, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeMessage(parsed)
}

// DecodeMessage decodes one message from a jsonx value.
func DecodeMessage(v any) (Message, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected message object, got %T", v)
	}
	rawRole, _ := obj.Get("role")
	role, _ := rawRole.(string)
	switch role {
	case "system":
		var out SystemMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "user":
		var out UserMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "assistant":
		var out AssistantMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolResult":
		var out ToolResultMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownMessage{Raw: obj}, nil
	}
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
	Type         string                    `json:"type"`
	Instructions string                    `json:"instructions"`
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

func (u *UnknownClassifierQuestion) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownClassifierQuestion) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// UnmarshalClassifierQuestion decodes one question.
func UnmarshalClassifierQuestion(data []byte) (ClassifierQuestion, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeClassifierQuestion(parsed)
}

// DecodeClassifierQuestion decodes one question from a jsonx value.
func DecodeClassifierQuestion(v any) (ClassifierQuestion, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected classifier question object, got %T", v)
	}
	rawType, _ := obj.Get("type")
	typ, _ := rawType.(string)
	switch typ {
	case "choice":
		var out ClassifierChoiceQuestion
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "score":
		var out ClassifierScoreQuestion
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "bool":
		var out ClassifierBoolQuestion
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownClassifierQuestion{Raw: obj}, nil
	}
}

// ClassifierContext is the input of a classification.
type ClassifierContext struct {
	State     *jsonx.Object                         `json:"state"`
	Images    []*ImageContent                       `json:"images,omitzero"`
	Questions *omap.Map[string, ClassifierQuestion] `json:"questions"`
}

func (c *ClassifierContext) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*c = ClassifierContext{}
		return nil
	}
	type alias ClassifierContext
	var raw struct {
		alias
		Questions *jsonx.Object `json:"questions"`
	}
	if err := jsonx.Decode(parsed, &raw); err != nil {
		return err
	}
	*c = ClassifierContext(raw.alias)
	if raw.Questions != nil {
		qMap := omap.NewMap[string, ClassifierQuestion]()
		for _, k := range raw.Questions.Keys() {
			val, _ := raw.Questions.Get(k)
			q, err := DecodeClassifierQuestion(val)
			if err != nil {
				return err
			}
			qMap.Set(k, q)
		}
		c.Questions = qMap
	}
	return nil
}

// ClassifierAnswer is one answer. The discriminator is "type".
type ClassifierAnswer interface{ isClassifierAnswer() }

// ClassifierChoiceAnswer is a choice plus a distribution.
type ClassifierChoiceAnswer struct {
	Type          string                     `json:"type"`
	Choice        string                     `json:"choice"`
	Probabilities *omap.Map[string, float64] `json:"probabilities"`
	Confidence    float64                    `json:"confidence"`
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

func (u *UnknownClassifierAnswer) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownClassifierAnswer) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// UnmarshalClassifierAnswer decodes one answer.
func UnmarshalClassifierAnswer(data []byte) (ClassifierAnswer, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeClassifierAnswer(parsed)
}

// DecodeClassifierAnswer decodes one answer from a jsonx value.
func DecodeClassifierAnswer(v any) (ClassifierAnswer, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected classifier answer object, got %T", v)
	}
	rawType, _ := obj.Get("type")
	typ, _ := rawType.(string)
	switch typ {
	case "choice":
		var out ClassifierChoiceAnswer
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "score":
		var out ClassifierScoreAnswer
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "bool":
		var out ClassifierBoolAnswer
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownClassifierAnswer{Raw: obj}, nil
	}
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
	Api          ClassifierApi                       `json:"api"`
	Provider     ProviderId                          `json:"provider"`
	Model        string                              `json:"model"`
	Answers      *omap.Map[string, ClassifierAnswer] `json:"answers"`
	Usage        *Usage                              `json:"usage,omitzero"`
	StopReason   ClassifierStopReason                `json:"stopReason"`
	ErrorMessage *string                             `json:"errorMessage,omitzero"`
	Timestamp    int64                               `json:"timestamp"`
}

func (r *ClassifierResult) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*r = ClassifierResult{}
		return nil
	}
	type alias ClassifierResult
	var raw struct {
		alias
		Answers *jsonx.Object `json:"answers"`
	}
	if err := jsonx.Decode(parsed, &raw); err != nil {
		return err
	}
	*r = ClassifierResult(raw.alias)
	if raw.Answers != nil {
		aMap := omap.NewMap[string, ClassifierAnswer]()
		for _, k := range raw.Answers.Keys() {
			val, _ := raw.Answers.Get(k)
			a, err := DecodeClassifierAnswer(val)
			if err != nil {
				return err
			}
			aMap.Set(k, a)
		}
		r.Answers = aMap
	}
	return nil
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

func (u *UnknownConstrainedSamplingConfig) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownConstrainedSamplingConfig) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// UnmarshalConstrainedSamplingConfig decodes one constraint object.
func UnmarshalConstrainedSamplingConfig(data []byte) (ConstrainedSamplingConfig, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeConstrainedSamplingConfig(parsed)
}

// DecodeConstrainedSamplingConfig decodes one constraint from a jsonx value.
func DecodeConstrainedSamplingConfig(v any) (ConstrainedSamplingConfig, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected constrained sampling config object, got %T", v)
	}
	rawType, _ := obj.Get("type")
	typ, _ := rawType.(string)
	switch typ {
	case "json_schema":
		var out ConstrainedSamplingJsonSchema
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "grammar":
		var out ConstrainedSamplingGrammar
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownConstrainedSamplingConfig{Raw: obj}, nil
	}
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
	if v.V == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(v.V)
}

func (v *ToolConstrainedSamplingValue) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		v.V = nil
		return nil
	}
	dec, err := DecodeToolConstrainedSampling(parsed)
	if err != nil {
		return err
	}
	v.V = dec
	return nil
}

// UnmarshalToolConstrainedSampling decodes a tool sampling constraint.
func UnmarshalToolConstrainedSampling(data []byte) (ToolConstrainedSampling, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeToolConstrainedSampling(parsed)
}

// DecodeToolConstrainedSampling decodes a tool sampling constraint from a jsonx value.
func DecodeToolConstrainedSampling(v any) (ToolConstrainedSampling, error) {
	switch x := v.(type) {
	case bool:
		if !x {
			return ToolConstrainedSamplingFalse{}, nil
		}
		return nil, fmt.Errorf("invalid constrainedSampling bool: true")
	case *jsonx.Object:
		cfg, err := DecodeConstrainedSamplingConfig(x)
		if err != nil {
			return nil, err
		}
		return cfg.(ToolConstrainedSampling), nil
	default:
		return nil, fmt.Errorf("invalid constrainedSampling value: %T", v)
	}
}

// Tool is a tool declaration. Parameters is a TypeBox schema.
type Tool struct {
	Name                string                       `json:"name"`
	Description         string                       `json:"description"`
	Parameters          *typebox.Schema              `json:"parameters"`
	ConstrainedSampling ToolConstrainedSamplingValue `json:"constrainedSampling,omitzero"`
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
// The discriminator is "type". Partial on a live event from
// AssistantMessageEventStream is a snapshot of the response so far.
type AssistantMessageEvent interface{ isAssistantMessageEvent() }

// AssistantMessageEventStart opens a stream.
type AssistantMessageEventStart struct {
	Type    string            `json:"type"`
	Partial *AssistantMessage `json:"partial"`
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
	Type    string            `json:"type"`
	Reason  StopReason        `json:"reason"`
	Message *AssistantMessage `json:"message"`
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

func (u *UnknownAssistantMessageEvent) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownAssistantMessageEvent) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// UnmarshalAssistantMessageEvent decodes one stream event.
func UnmarshalAssistantMessageEvent(data []byte) (AssistantMessageEvent, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeAssistantMessageEvent(parsed)
}

// DecodeAssistantMessageEvent decodes one stream event from a jsonx value.
func DecodeAssistantMessageEvent(v any) (AssistantMessageEvent, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected assistant message event object, got %T", v)
	}
	rawType, _ := obj.Get("type")
	typ, _ := rawType.(string)
	switch typ {
	case "start":
		var out AssistantMessageEventStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "text_start":
		var out AssistantMessageEventTextStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "text_delta":
		var out AssistantMessageEventTextDelta
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "text_end":
		var out AssistantMessageEventTextEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "thinking_start":
		var out AssistantMessageEventThinkingStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "thinking_delta":
		var out AssistantMessageEventThinkingDelta
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "thinking_end":
		var out AssistantMessageEventThinkingEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolcall_start":
		var out AssistantMessageEventToolcallStart
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolcall_delta":
		var out AssistantMessageEventToolcallDelta
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "toolcall_end":
		var out AssistantMessageEventToolcallEnd
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "done":
		var out AssistantMessageEventDone
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "error":
		var out AssistantMessageEventError
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownAssistantMessageEvent{Raw: obj}, nil
	}
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
	ThinkingFormatOpenai           ThinkingFormat = "openai"
	ThinkingFormatOpenrouter       ThinkingFormat = "openrouter"
	ThinkingFormatDeepseek         ThinkingFormat = "deepseek"
	ThinkingFormatTogether         ThinkingFormat = "together"
	ThinkingFormatBaseten          ThinkingFormat = "baseten"
	ThinkingFormatZai              ThinkingFormat = "zai"
	ThinkingFormatQwen             ThinkingFormat = "qwen"
	ThinkingFormatChatTemplate     ThinkingFormat = "chat-template"
	ThinkingFormatQwenChatTemplate ThinkingFormat = "qwen-chat-template"
	ThinkingFormatStringThinking   ThinkingFormat = "string-thinking"
	ThinkingFormatAntLing          ThinkingFormat = "ant-ling"
)

// CacheControlFormat selects prompt-cache markers.
type CacheControlFormat string

const CacheControlFormatAnthropic CacheControlFormat = "anthropic"

// PromptCacheKeyMode controls prompt_cache_key for OpenAI APIs.
type PromptCacheKeyMode string

const (
	PromptCacheKeyModeAuto     PromptCacheKeyMode = "auto"
	PromptCacheKeyModeEnabled  PromptCacheKeyMode = "enabled"
	PromptCacheKeyModeDisabled PromptCacheKeyMode = "disabled"
)

// OpenAICompletionsCompat overrides URL-based detection for chat-completions APIs.
// Bool fields use the documented provider default when nil.
type OpenAICompletionsCompat struct {
	SupportsStore                               *bool                                     `json:"supportsStore,omitzero"`
	SupportsDeveloperRole                       *bool                                     `json:"supportsDeveloperRole,omitzero"`
	SupportsReasoningEffort                     *bool                                     `json:"supportsReasoningEffort,omitzero"`
	SupportsUsageInStreaming                    *bool                                     `json:"supportsUsageInStreaming,omitzero"`
	SupportsFinishReason                        *bool                                     `json:"supportsFinishReason,omitzero"`
	MaxTokensField                              *MaxTokensField                           `json:"maxTokensField,omitzero"`
	RequiresToolResultName                      *bool                                     `json:"requiresToolResultName,omitzero"`
	RequiresAssistantAfterToolResult            *bool                                     `json:"requiresAssistantAfterToolResult,omitzero"`
	RequiresNonNullAssistantContent             *bool                                     `json:"requiresNonNullAssistantContent,omitzero"`
	RequiresToolsWithToolHistory                *bool                                     `json:"requiresToolsWithToolHistory,omitzero"`
	RequiresThinkingAsText                      *bool                                     `json:"requiresThinkingAsText,omitzero"`
	RequiresReasoningContentOnAssistantMessages *bool                                     `json:"requiresReasoningContentOnAssistantMessages,omitzero"`
	ThinkingFormat                              *ThinkingFormat                           `json:"thinkingFormat,omitzero"`
	ChatTemplateKwargs                          *omap.Map[string, ChatTemplateKwargValue] `json:"chatTemplateKwargs,omitzero"`
	ChatTemplateArgs                            *omap.Map[string, ChatTemplateKwargValue] `json:"chatTemplateArgs,omitzero"`
	OpenRouterRouting                           *OpenRouterRouting                        `json:"openRouterRouting,omitzero"`
	VercelGatewayRouting                        *VercelGatewayRouting                     `json:"vercelGatewayRouting,omitzero"`
	ZaiToolStream                               *bool                                     `json:"zaiToolStream,omitzero"`
	ThinkingTokenBudgetField                    *ThinkingTokenBudgetField                 `json:"thinkingTokenBudgetField,omitzero"`
	SupportsThinkingTokenBudget                 *bool                                     `json:"supportsThinkingTokenBudget,omitzero"`
	SupportsOpenAIGrammarTools                  *bool                                     `json:"supportsOpenAIGrammarTools,omitzero"`
	SupportsMidConvoSystemMessages              *bool                                     `json:"supportsMidConvoSystemMessages,omitzero"`
	SupportsMidConvoToolAdditions               *bool                                     `json:"supportsMidConvoToolAdditions,omitzero"`
	SupportsStrictMode                          *bool                                     `json:"supportsStrictMode,omitzero"`
	CacheControlFormat                          *CacheControlFormat                       `json:"cacheControlFormat,omitzero"`
	SendSessionAffinityHeaders                  *bool                                     `json:"sendSessionAffinityHeaders,omitzero"`
	SessionAffinityFormat                       *SessionAffinityFormat                    `json:"sessionAffinityFormat,omitzero"`
	PromptCacheKeyMode                          *PromptCacheKeyMode                       `json:"promptCacheKeyMode,omitzero"`
	SupportsLongCacheRetention                  *bool                                     `json:"supportsLongCacheRetention,omitzero"`
	VllmPriority                                *float64                                  `json:"vllmPriority,omitzero"`
}

func (*OpenAICompletionsCompat) isModelCompat() {}

func (c *OpenAICompletionsCompat) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*c = OpenAICompletionsCompat{}
		return nil
	}
	type alias OpenAICompletionsCompat
	var raw struct {
		alias
		ChatTemplateKwargs *jsonx.Object `json:"chatTemplateKwargs,omitzero"`
		ChatTemplateArgs   *jsonx.Object `json:"chatTemplateArgs,omitzero"`
	}
	if err := jsonx.Decode(parsed, &raw); err != nil {
		return err
	}
	*c = OpenAICompletionsCompat(raw.alias)
	c.ChatTemplateKwargs, err = typDecodeChatTemplateKwargMap(raw.ChatTemplateKwargs)
	if err != nil {
		return err
	}
	c.ChatTemplateArgs, err = typDecodeChatTemplateKwargMap(raw.ChatTemplateArgs)
	return err
}

// OpenAIResponsesCompat is compatibility settings for OpenAI Responses APIs.
type OpenAIResponsesCompat struct {
	SupportsDeveloperRole           *bool                  `json:"supportsDeveloperRole,omitzero"`
	SupportsMidConvoSystemMessages  *bool                  `json:"supportsMidConvoSystemMessages,omitzero"`
	SessionAffinityFormat           *SessionAffinityFormat `json:"sessionAffinityFormat,omitzero"`
	PromptCacheKeyMode              *PromptCacheKeyMode    `json:"promptCacheKeyMode,omitzero"`
	SupportsLongCacheRetention      *bool                  `json:"supportsLongCacheRetention,omitzero"`
	SupportsStrictMode              *bool                  `json:"supportsStrictMode,omitzero"`
	SupportsOpenAIGrammarTools      *bool                  `json:"supportsOpenAIGrammarTools,omitzero"`
	SupportsAdditionalTools         *bool                  `json:"supportsAdditionalTools,omitzero"`
	SupportsToolSearch              *bool                  `json:"supportsToolSearch,omitzero"`
	SupportsExplicitPromptCacheMode *bool                  `json:"supportsExplicitPromptCacheMode,omitzero"`
	SupportsMidConvoEffort          *bool                  `json:"supportsMidConvoEffort,omitzero"`
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
	SupportsMessageThreads          *bool                           `json:"supportsMessageThreads,omitzero"`
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

// SystemOneImageInput controls how System One classifier APIs receive images.
type SystemOneImageInput string

const (
	SystemOneImageInputAssets         SystemOneImageInput = "assets"
	SystemOneImageInputImages         SystemOneImageInput = "images"
	SystemOneImageInputStateImageUrls SystemOneImageInput = "state-image-urls"
)

// SystemOneCompat is compatibility settings for System One classifier APIs.
type SystemOneCompat struct {
	ImageInput           *SystemOneImageInput `json:"imageInput,omitzero"`
	SendImageOptInHeader *bool                `json:"sendImageOptInHeader,omitzero"`
}

func (*SystemOneCompat) isModelCompat() {}

// ProviderCompat holds API-agnostic provider/model compatibility overrides used in models.json.
type ProviderCompat struct {
	SupportsStore                               *bool                                     `json:"supportsStore,omitzero"`
	SupportsDeveloperRole                       *bool                                     `json:"supportsDeveloperRole,omitzero"`
	SupportsReasoningEffort                     *bool                                     `json:"supportsReasoningEffort,omitzero"`
	SupportsUsageInStreaming                    *bool                                     `json:"supportsUsageInStreaming,omitzero"`
	SupportsFinishReason                        *bool                                     `json:"supportsFinishReason,omitzero"`
	MaxTokensField                              *MaxTokensField                           `json:"maxTokensField,omitzero"`
	RequiresToolResultName                      *bool                                     `json:"requiresToolResultName,omitzero"`
	RequiresAssistantAfterToolResult            *bool                                     `json:"requiresAssistantAfterToolResult,omitzero"`
	RequiresNonNullAssistantContent             *bool                                     `json:"requiresNonNullAssistantContent,omitzero"`
	RequiresToolsWithToolHistory                *bool                                     `json:"requiresToolsWithToolHistory,omitzero"`
	RequiresThinkingAsText                      *bool                                     `json:"requiresThinkingAsText,omitzero"`
	RequiresReasoningContentOnAssistantMessages *bool                                     `json:"requiresReasoningContentOnAssistantMessages,omitzero"`
	ThinkingFormat                              *ThinkingFormat                           `json:"thinkingFormat,omitzero"`
	ChatTemplateKwargs                          *omap.Map[string, ChatTemplateKwargValue] `json:"chatTemplateKwargs,omitzero"`
	ChatTemplateArgs                            *omap.Map[string, ChatTemplateKwargValue] `json:"chatTemplateArgs,omitzero"`
	OpenRouterRouting                           *OpenRouterRouting                        `json:"openRouterRouting,omitzero"`
	VercelGatewayRouting                        *VercelGatewayRouting                     `json:"vercelGatewayRouting,omitzero"`
	ZaiToolStream                               *bool                                     `json:"zaiToolStream,omitzero"`
	ThinkingTokenBudgetField                    *ThinkingTokenBudgetField                 `json:"thinkingTokenBudgetField,omitzero"`
	SupportsThinkingTokenBudget                 *bool                                     `json:"supportsThinkingTokenBudget,omitzero"`
	SupportsOpenAIGrammarTools                  *bool                                     `json:"supportsOpenAIGrammarTools,omitzero"`
	SupportsMidConvoSystemMessages              *bool                                     `json:"supportsMidConvoSystemMessages,omitzero"`
	SupportsMidConvoToolAdditions               *bool                                     `json:"supportsMidConvoToolAdditions,omitzero"`
	SupportsStrictMode                          *bool                                     `json:"supportsStrictMode,omitzero"`
	CacheControlFormat                          *CacheControlFormat                       `json:"cacheControlFormat,omitzero"`
	SendSessionAffinityHeaders                  *bool                                     `json:"sendSessionAffinityHeaders,omitzero"`
	SessionAffinityFormat                       *SessionAffinityFormat                    `json:"sessionAffinityFormat,omitzero"`
	PromptCacheKeyMode                          *PromptCacheKeyMode                       `json:"promptCacheKeyMode,omitzero"`
	SupportsLongCacheRetention                  *bool                                     `json:"supportsLongCacheRetention,omitzero"`
	VllmPriority                                *float64                                  `json:"vllmPriority,omitzero"`
	SupportsAdditionalTools                     *bool                                     `json:"supportsAdditionalTools,omitzero"`
	SupportsToolSearch                          *bool                                     `json:"supportsToolSearch,omitzero"`
	SupportsExplicitPromptCacheMode             *bool                                     `json:"supportsExplicitPromptCacheMode,omitzero"`
	SupportsMidConvoEffort                      *bool                                     `json:"supportsMidConvoEffort,omitzero"`
	SupportsMaxOutputTokens                     *bool                                     `json:"supportsMaxOutputTokens,omitzero"`
	SupportsEagerToolInputStreaming             *bool                                     `json:"supportsEagerToolInputStreaming,omitzero"`
	SupportsCacheControlOnTools                 *bool                                     `json:"supportsCacheControlOnTools,omitzero"`
	SupportsTemperature                         *bool                                     `json:"supportsTemperature,omitzero"`
	ForceAdaptiveThinking                       *bool                                     `json:"forceAdaptiveThinking,omitzero"`
	AllowEmptySignature                         *bool                                     `json:"allowEmptySignature,omitzero"`
	SupportsStrictTools                         *bool                                     `json:"supportsStrictTools,omitzero"`
	SupportsMidConvoToolChanges                 *bool                                     `json:"supportsMidConvoToolChanges,omitzero"`
	SupportsMessageThreads                      *bool                                     `json:"supportsMessageThreads,omitzero"`
	AllowedFallbackModels                       []AnthropicAllowedFallbackModel           `json:"allowedFallbackModels,omitzero"`
	ImageInput                                  *SystemOneImageInput                      `json:"imageInput,omitzero"`
	SendImageOptInHeader                        *bool                                     `json:"sendImageOptInHeader,omitzero"`
}

func (*ProviderCompat) isModelCompat() {}

func (c *ProviderCompat) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*c = ProviderCompat{}
		return nil
	}
	type alias ProviderCompat
	var raw struct {
		alias
		ChatTemplateKwargs *jsonx.Object `json:"chatTemplateKwargs,omitzero"`
		ChatTemplateArgs   *jsonx.Object `json:"chatTemplateArgs,omitzero"`
	}
	if err := jsonx.Decode(parsed, &raw); err != nil {
		return err
	}
	*c = ProviderCompat(raw.alias)
	c.ChatTemplateKwargs, err = typDecodeChatTemplateKwargMap(raw.ChatTemplateKwargs)
	if err != nil {
		return err
	}
	c.ChatTemplateArgs, err = typDecodeChatTemplateKwargMap(raw.ChatTemplateArgs)
	return err
}

// ModelCompat is the untagged compat object on a chat or classifier model.
// The concrete type depends on the model's API.
type ModelCompat interface{ isModelCompat() }

// UnknownModelCompat keeps compat JSON that matches no known struct.
type UnknownModelCompat struct{ Raw *jsonx.Object }

func (*UnknownModelCompat) isModelCompat() {}

func (u *UnknownModelCompat) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownModelCompat) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// ModelCompatValue is the JSON form of Model.compat. The zero value is omitted.
type ModelCompatValue struct {
	V ModelCompat
}

func (v ModelCompatValue) IsZero() bool { return v.V == nil }

func (v ModelCompatValue) MarshalJSON() ([]byte, error) {
	if v.V == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(v.V)
}

func (v *ModelCompatValue) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		v.V = nil
		return nil
	}
	dec, err := DecodeModelCompat(parsed)
	if err != nil {
		return err
	}
	v.V = dec
	return nil
}

// UnmarshalModelCompat decodes a model compat object without API context.
func UnmarshalModelCompat(data []byte) (ModelCompat, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeModelCompat(parsed)
}

// DecodeModelCompat decodes a model compat object from a jsonx value without API context.
func DecodeModelCompat(v any) (ModelCompat, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected model compat object, got %T", v)
	}
	return &UnknownModelCompat{Raw: obj}, nil
}

// DecodeModelCompatForApi decodes a model compat object from a jsonx value for the given API id.
func DecodeModelCompatForApi(api string, v any) (ModelCompat, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected model compat object, got %T", v)
	}
	switch Api(api) {
	case KnownApiOpenaiCompletions:
		var out OpenAICompletionsCompat
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case KnownApiOpenaiResponses, KnownApiAzureOpenaiResponses, KnownApiOpenaiCodexResponses:
		var out OpenAIResponsesCompat
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case KnownApiAnthropicMessages:
		var out AnthropicMessagesCompat
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case KnownApiBedrockConverseStream:
		var out BedrockCompat
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case KnownApiMistralConversations:
		var out MistralConversationsCompat
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
	switch ClassifierApi(api) {
	case KnownClassifierApiTypesafeSystemOne, KnownClassifierApiCloudflareWorkersAiSystemOne:
		var out SystemOneCompat
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
	return &UnknownModelCompat{Raw: obj}, nil
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

func (u *UnknownOpenRouterRoutingSort) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownOpenRouterRoutingSort) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// OpenRouterRoutingSortValue is the JSON form of OpenRouterRouting.sort.
type OpenRouterRoutingSortValue struct {
	V OpenRouterRoutingSort
}

func (v OpenRouterRoutingSortValue) IsZero() bool { return v.V == nil }

func (v OpenRouterRoutingSortValue) MarshalJSON() ([]byte, error) {
	if v.V == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(v.V)
}

func (v *OpenRouterRoutingSortValue) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		v.V = nil
		return nil
	}
	dec, err := DecodeOpenRouterRoutingSort(parsed)
	if err != nil {
		return err
	}
	v.V = dec
	return nil
}

// UnmarshalOpenRouterRoutingSort decodes a sort value.
func UnmarshalOpenRouterRoutingSort(data []byte) (OpenRouterRoutingSort, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeOpenRouterRoutingSort(parsed)
}

// DecodeOpenRouterRoutingSort decodes a sort value from a jsonx value.
func DecodeOpenRouterRoutingSort(v any) (OpenRouterRoutingSort, error) {
	switch x := v.(type) {
	case string:
		return OpenRouterRoutingSortString(x), nil
	case *jsonx.Object:
		_, hasBy := x.Get("by")
		_, hasPart := x.Get("partition")
		if hasBy || hasPart || x.Len() == 0 {
			var out OpenRouterRoutingSortObject
			if err := jsonx.Decode(x, &out); err != nil {
				return nil, err
			}
			return &out, nil
		}
		return &UnknownOpenRouterRoutingSort{Raw: x}, nil
	default:
		return nil, fmt.Errorf("invalid OpenRouter routing sort: %T", v)
	}
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

func (u *UnknownOpenRouterPrice) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownOpenRouterPrice) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// OpenRouterPriceValue is one optional max_price field.
type OpenRouterPriceValue struct {
	V OpenRouterPrice
}

func (v OpenRouterPriceValue) IsZero() bool { return v.V == nil }

func (v OpenRouterPriceValue) MarshalJSON() ([]byte, error) {
	if v.V == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(v.V)
}

func (v *OpenRouterPriceValue) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		v.V = nil
		return nil
	}
	dec, err := DecodeOpenRouterPrice(parsed)
	if err != nil {
		return err
	}
	v.V = dec
	return nil
}

// UnmarshalOpenRouterPrice decodes a price.
func UnmarshalOpenRouterPrice(data []byte) (OpenRouterPrice, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeOpenRouterPrice(parsed)
}

// DecodeOpenRouterPrice decodes a price from a jsonx value.
func DecodeOpenRouterPrice(v any) (OpenRouterPrice, error) {
	switch x := v.(type) {
	case float64:
		return OpenRouterPriceNumber(x), nil
	case int:
		return OpenRouterPriceNumber(float64(x)), nil
	case int64:
		return OpenRouterPriceNumber(float64(x)), nil
	case string:
		return OpenRouterPriceString(x), nil
	case *jsonx.Object:
		return &UnknownOpenRouterPrice{Raw: x}, nil
	default:
		return nil, fmt.Errorf("invalid OpenRouter price: %T", v)
	}
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

func (u *UnknownOpenRouterThroughput) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownOpenRouterThroughput) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// OpenRouterThroughputValue is the JSON form of a throughput or latency preference.
type OpenRouterThroughputValue struct {
	V OpenRouterThroughput
}

func (v OpenRouterThroughputValue) IsZero() bool { return v.V == nil }

func (v OpenRouterThroughputValue) MarshalJSON() ([]byte, error) {
	if v.V == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(v.V)
}

func (v *OpenRouterThroughputValue) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		v.V = nil
		return nil
	}
	dec, err := DecodeOpenRouterThroughput(parsed)
	if err != nil {
		return err
	}
	v.V = dec
	return nil
}

// UnmarshalOpenRouterThroughput decodes a throughput or latency preference.
func UnmarshalOpenRouterThroughput(data []byte) (OpenRouterThroughput, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeOpenRouterThroughput(parsed)
}

// DecodeOpenRouterThroughput decodes a preference from a jsonx value.
func DecodeOpenRouterThroughput(v any) (OpenRouterThroughput, error) {
	switch x := v.(type) {
	case float64:
		return OpenRouterThroughputNumber(x), nil
	case int:
		return OpenRouterThroughputNumber(float64(x)), nil
	case int64:
		return OpenRouterThroughputNumber(float64(x)), nil
	case *jsonx.Object:
		_, hasP50 := x.Get("p50")
		_, hasP75 := x.Get("p75")
		_, hasP90 := x.Get("p90")
		_, hasP99 := x.Get("p99")
		if hasP50 || hasP75 || hasP90 || hasP99 || x.Len() == 0 {
			var out OpenRouterPercentiles
			if err := jsonx.Decode(x, &out); err != nil {
				return nil, err
			}
			return &out, nil
		}
		return &UnknownOpenRouterThroughput{Raw: x}, nil
	default:
		return nil, fmt.Errorf("invalid OpenRouter throughput: %T", v)
	}
}

// OpenRouterRouting is OpenRouter provider routing, sent as the provider field.
// Field names keep the TS snake_case with only the first letter uppercased.
type OpenRouterRouting struct {
	Allow_fallbacks          *bool                      `json:"allow_fallbacks,omitzero"`
	Require_parameters       *bool                      `json:"require_parameters,omitzero"`
	Data_collection          *OpenRouterDataCollection  `json:"data_collection,omitzero"`
	Zdr                      *bool                      `json:"zdr,omitzero"`
	Enforce_distillable_text *bool                      `json:"enforce_distillable_text,omitzero"`
	Order                    []string                   `json:"order,omitzero"`
	Only                     []string                   `json:"only,omitzero"`
	Ignore                   []string                   `json:"ignore,omitzero"`
	Quantizations            []string                   `json:"quantizations,omitzero"`
	Sort                     OpenRouterRoutingSortValue `json:"sort,omitzero"`
	Max_price                *OpenRouterMaxPrice        `json:"max_price,omitzero"`
	Preferred_min_throughput OpenRouterThroughputValue  `json:"preferred_min_throughput,omitzero"`
	Preferred_max_latency    OpenRouterThroughputValue  `json:"preferred_max_latency,omitzero"`
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
	Type                          *ModelType                     `json:"type,omitzero"`
	Reasoning                     bool                           `json:"reasoning"`
	ThinkingLevelMap              *ThinkingLevelMap              `json:"thinkingLevelMap,omitzero"`
	PromptCache                   *ModelPromptCache              `json:"promptCache,omitzero"`
	ContextWindow                 int                            `json:"contextWindow"`
	MaxTokens                     int                            `json:"maxTokens"`
	SamplingParams                *jsonx.Object                  `json:"samplingParams,omitzero"`
	SamplingParamsByThinkingLevel *SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitzero"`
	Compat                        ModelCompatValue               `json:"compat,omitzero"`
}

func (*Model) isAnyModel() {}

func (m *Model) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*m = Model{}
		return nil
	}
	return typDecodeModel(parsed, m)
}

func typDecodeModel(v any, out *Model) error {
	type alias Model
	var raw struct {
		alias
		Compat any `json:"compat,omitzero"`
	}
	if err := jsonx.Decode(v, &raw); err != nil {
		return err
	}
	*out = Model(raw.alias)
	if raw.Compat != nil {
		c, err := DecodeModelCompatForApi(out.Api, raw.Compat)
		if err != nil {
			return err
		}
		out.Compat = ModelCompatValue{V: c}
	}
	return nil
}

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
	Type          ModelType        `json:"type"`
	ContextWindow int              `json:"contextWindow"`
	Compat        ModelCompatValue `json:"compat,omitzero"`
}

func (*ClassifierModel) isAnyModel() {}

func (m *ClassifierModel) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*m = ClassifierModel{}
		return nil
	}
	return typDecodeClassifierModel(parsed, m)
}

func typDecodeClassifierModel(v any, out *ClassifierModel) error {
	type alias ClassifierModel
	var raw struct {
		alias
		Compat any `json:"compat,omitzero"`
	}
	if err := jsonx.Decode(v, &raw); err != nil {
		return err
	}
	*out = ClassifierModel(raw.alias)
	if raw.Compat != nil {
		c, err := DecodeModelCompatForApi(out.Api, raw.Compat)
		if err != nil {
			return err
		}
		out.Compat = ModelCompatValue{V: c}
	}
	return nil
}

// AnyModel is any catalog entry. Narrow with the model-operations helpers.
// ModelTypeMap is a TypeScript type-level map and has no Go form.
type AnyModel interface{ isAnyModel() }

// UnknownAnyModel keeps a catalog entry whose type is not chat, image, or classifier.
type UnknownAnyModel struct{ Raw *jsonx.Object }

func (*UnknownAnyModel) isAnyModel() {}

func (u *UnknownAnyModel) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownAnyModel) UnmarshalJSON(data []byte) error {
	return typUnmarshalUnknownRaw(data, &u.Raw)
}

// AnyModelList is a JSON array of catalog entries.
type AnyModelList []AnyModel

func (l *AnyModelList) UnmarshalJSON(data []byte) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*l = nil
		return nil
	}
	arr, ok := parsed.([]any)
	if !ok {
		return fmt.Errorf("expected model array, got %T", parsed)
	}
	out := make(AnyModelList, len(arr))
	for i, elem := range arr {
		m, err := DecodeAnyModel(elem)
		if err != nil {
			return err
		}
		out[i] = m
	}
	*l = out
	return nil
}

// UnmarshalAnyModel decodes one catalog entry.
func UnmarshalAnyModel(data []byte) (AnyModel, error) {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeAnyModel(parsed)
}

// DecodeAnyModel decodes one catalog entry from a jsonx value.
func DecodeAnyModel(v any) (AnyModel, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("expected model object, got %T", v)
	}
	rawType, _ := obj.Get("type")
	typ, _ := rawType.(string)
	switch typ {
	case "", string(ModelTypeChat):
		var out Model
		if err := typDecodeModel(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case string(ModelTypeImage):
		var out ImageModel
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case string(ModelTypeClassifier):
		var out ClassifierModel
		if err := typDecodeClassifierModel(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownAnyModel{Raw: obj}, nil
	}
}

func typUnmarshalUnknownRaw(data []byte, dst **jsonx.Object) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if parsed == nil {
		*dst = nil
		return nil
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		return fmt.Errorf("expected JSON object, got %T", parsed)
	}
	*dst = obj
	return nil
}

func typUnmarshalTextOrBlocks(data []byte, textDst **string, blocksDst *ContentList) error {
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	switch x := parsed.(type) {
	case nil:
		*textDst = nil
		*blocksDst = nil
		return nil
	case string:
		s := x
		*textDst = &s
		*blocksDst = nil
		return nil
	case []any:
		blocks := make(ContentList, len(x))
		for i, elem := range x {
			c, err := DecodeContent(elem)
			if err != nil {
				return err
			}
			blocks[i] = c
		}
		*textDst = nil
		*blocksDst = blocks
		return nil
	default:
		return fmt.Errorf("expected string or content array, got %T", parsed)
	}
}

func typDecodeChatTemplateKwargMap(obj *jsonx.Object) (*omap.Map[string, ChatTemplateKwargValue], error) {
	if obj == nil {
		return nil, nil
	}
	out := omap.NewMap[string, ChatTemplateKwargValue]()
	for _, k := range obj.Keys() {
		rawVal, _ := obj.Get(k)
		decoded, err := DecodeChatTemplateKwargValue(rawVal)
		if err != nil {
			return nil, err
		}
		out.Set(k, decoded)
	}
	return out, nil
}

var (
	_ ProviderStreamOptions = (*StreamOptions)(nil)
	_ ProviderStreamOptions = (*SimpleStreamOptions)(nil)
	_ ProviderImagesOptions = (*ImagesOptions)(nil)
)
