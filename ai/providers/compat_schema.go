// Ported from packages/ai/src/providers/compat-schema.ts (pi v1.5.2).

package providers

import (
	"fmt"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func comschSessionAffinityFormat(options ...*jsonx.Object) *typebox.Schema {
	return typebox.Union([]*typebox.Schema{
		StringLiteral("openai"),
		StringLiteral("openai-nosession"),
		StringLiteral("openrouter"),
	}, options...)
}

func comschThinkingTokenBudgetField(options ...*jsonx.Object) *typebox.Schema {
	return typebox.Union([]*typebox.Schema{
		StringLiteral("thinking_token_budget"),
		StringLiteral("thinking_budget"),
		StringLiteral("thinking_budget_tokens"),
	}, options...)
}

func comschOptPromptCacheKeyMode(options ...*jsonx.Object) *typebox.Schema {
	return typebox.Optional(typebox.Union([]*typebox.Schema{
		StringLiteral("auto"),
		StringLiteral("enabled"),
		StringLiteral("disabled"),
	}, options...))
}

func comschOptBool(desc string, def ...bool) *typebox.Schema {
	opts := jsonx.ObjectOf("description", desc)
	if len(def) > 0 {
		opts.Set("default", def[0])
	}
	return typebox.Optional(typebox.Boolean(opts))
}

func comschPercentileCutoffs(metric string) *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("p50", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", metric+" at the 50th percentile."))))
	props.Set("p75", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", metric+" at the 75th percentile."))))
	props.Set("p90", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", metric+" at the 90th percentile."))))
	props.Set("p99", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", metric+" at the 99th percentile."))))
	return OpenObject(props)
}

func comschOptPrice(desc string) *typebox.Schema {
	return typebox.Optional(typebox.Union(
		[]*typebox.Schema{typebox.Number(), typebox.String()},
		jsonx.ObjectOf("description", desc),
	))
}

func comschVarObjectSchema() *typebox.Schema {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("$var", typebox.Union([]*typebox.Schema{
		StringLiteral("thinking.enabled"),
		StringLiteral("thinking.effort"),
		StringLiteral("thinking.budget"),
	}))
	props.Set("omitWhenOff", typebox.Optional(typebox.Boolean()))
	return OpenObject(props)
}

func comschMergeCompatProperties(
	propertyGroups []*omap.Map[string, *typebox.Schema],
	overrides *omap.Map[string, *typebox.Schema],
) *omap.Map[string, *typebox.Schema] {
	merged := omap.NewMap[string, *typebox.Schema]()
	duplicateNames := omap.NewSet[string]()

	for _, properties := range propertyGroups {
		if properties == nil {
			continue
		}
		for name, schema := range properties.All() {
			if merged.Has(name) {
				duplicateNames.Add(name)
				if !overrides.Has(name) {
					panic(fmt.Sprintf("Duplicate compatibility schema property requires an override: %s", name))
				}
			}
			merged.Set(name, schema)
		}
	}

	for name, schema := range overrides.All() {
		if !duplicateNames.Has(name) {
			panic(fmt.Sprintf("Compatibility schema override does not resolve a duplicate property: %s", name))
		}
		merged.Set(name, schema)
	}

	return merged
}

var (
	SessionAffinityFormatSchema = comschSessionAffinityFormat(jsonx.ObjectOf(
		"description", "Session-affinity header format used to route related requests consistently.",
	))

	ThinkingTokenBudgetFieldSchema = comschThinkingTokenBudgetField(jsonx.ObjectOf(
		"description", `Top-level request field name used by OpenAI-compatible endpoints to cap reasoning tokens. "thinking_token_budget" is used by vLLM, "thinking_budget" by Qwen, DashScope, or SGLang, and "thinking_budget_tokens" by llama.cpp.`,
	))

	ChatTemplateKwargValueSchema = typebox.Union([]*typebox.Schema{
		typebox.String(),
		typebox.Number(),
		typebox.Boolean(),
		typebox.Null(),
		comschVarObjectSchema(),
	})

	OpenRouterRoutingSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("allow_fallbacks", comschOptBool("Whether to allow backup providers to serve requests.", true))
		props.Set("require_parameters", comschOptBool("Whether to filter providers to only those that support all parameters in the request.", false))
		props.Set("data_collection", typebox.Optional(typebox.Union([]*typebox.Schema{
			StringLiteral("deny"),
			StringLiteral("allow"),
		}, jsonx.ObjectOf(
			"description", `Data collection setting. "allow": allow providers that may store or train on data. "deny": only use providers that do not collect user data.`,
			"default", "allow",
		))))
		props.Set("zdr", comschOptBool("Whether to restrict routing to only ZDR (Zero Data Retention) endpoints."))
		props.Set("enforce_distillable_text", comschOptBool("Whether to restrict routing to only models that allow text distillation."))
		props.Set("order", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
			"description", "An ordered list of provider names or slugs to try in sequence, falling back to the next if unavailable.",
		))))
		props.Set("only", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
			"description", "List of provider names or slugs to exclusively allow for this request.",
		))))
		props.Set("ignore", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
			"description", "List of provider names or slugs to skip for this request.",
		))))
		props.Set("quantizations", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
			"description", `A list of quantization levels to filter providers by, for example ["fp16", "bf16", "fp8", "fp6", "int8", "int4", "fp4", "fp32"].`,
		))))

		sortObjProps := omap.NewMap[string, *typebox.Schema]()
		sortObjProps.Set("by", typebox.Optional(typebox.String(jsonx.ObjectOf(
			"description", `The sorting metric, such as "price", "throughput", or "latency".`,
		))))
		sortObjProps.Set("partition", typebox.Optional(typebox.Union(
			[]*typebox.Schema{typebox.String(), typebox.Null()},
			jsonx.ObjectOf("description", `Partitioning strategy: "model" or "none".`, "default", "model"),
		)))
		props.Set("sort", typebox.Optional(typebox.Union([]*typebox.Schema{
			typebox.String(),
			OpenObject(sortObjProps),
		}, jsonx.ObjectOf(
			"description", `Sorting strategy. Can be a string such as "price", "throughput", or "latency", or an object.`,
		))))

		maxPriceProps := omap.NewMap[string, *typebox.Schema]()
		maxPriceProps.Set("prompt", comschOptPrice("Price per million prompt tokens."))
		maxPriceProps.Set("completion", comschOptPrice("Price per million completion tokens."))
		maxPriceProps.Set("image", comschOptPrice("Price per image."))
		maxPriceProps.Set("audio", comschOptPrice("Price per audio unit."))
		maxPriceProps.Set("request", comschOptPrice("Price per request."))
		props.Set("max_price", typebox.Optional(OpenObject(maxPriceProps, jsonx.ObjectOf(
			"description", "Maximum price per million tokens in USD.",
		))))

		props.Set("preferred_min_throughput", typebox.Optional(typebox.Union([]*typebox.Schema{
			typebox.Number(),
			comschPercentileCutoffs("Minimum tokens per second"),
		}, jsonx.ObjectOf(
			"description", "Preferred minimum throughput in tokens per second. A number applies to p50.",
		))))
		props.Set("preferred_max_latency", typebox.Optional(typebox.Union([]*typebox.Schema{
			typebox.Number(),
			comschPercentileCutoffs("Maximum latency in seconds"),
		}, jsonx.ObjectOf(
			"description", "Preferred maximum latency in seconds. A number applies to p50.",
		))))

		return OpenObject(props, jsonx.ObjectOf(
			"description", "OpenRouter provider routing preferences. Controls which upstream providers OpenRouter routes requests to. Sent as the provider field in the OpenRouter API request body. See https://openrouter.ai/docs/guides/routing/provider-selection.",
		))
	}()

	VercelGatewayRoutingSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("only", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
			"description", `List of provider slugs to exclusively use for this request, for example ["bedrock", "anthropic"].`,
		))))
		props.Set("order", typebox.Optional(typebox.Array(typebox.String(), jsonx.ObjectOf(
			"description", `List of provider slugs to try in order, for example ["anthropic", "openai"].`,
		))))
		return OpenObject(props, jsonx.ObjectOf(
			"description", "Vercel AI Gateway routing preferences. Controls which upstream providers the gateway routes requests to. See https://vercel.com/docs/ai-gateway/models-and-providers/provider-options.",
		))
	}()

	AnthropicAllowedFallbackModelSchema = func() *typebox.Schema {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("provider", typebox.String(jsonx.ObjectOf("minLength", 1)))
		props.Set("model", typebox.String(jsonx.ObjectOf("minLength", 1)))
		props.Set("cost", ModelCostSchema)
		return OpenObject(props, jsonx.ObjectOf(
			"description", "An Anthropic server-side refusal fallback model with local pricing metadata.",
		))
	}()

	comschOpenAICompletionsCompatProperties = func() *omap.Map[string, *typebox.Schema] {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("supportsStore", comschOptBool("Whether the provider supports the store field. Default: auto-detected from URL."))
		props.Set("supportsDeveloperRole", comschOptBool("Whether the provider supports the developer role instead of system. Default: auto-detected from URL."))
		props.Set("supportsReasoningEffort", comschOptBool("Whether the provider supports reasoning_effort. Default: auto-detected from URL."))
		props.Set("supportsUsageInStreaming", comschOptBool("Whether the provider supports stream_options.include_usage for token usage in streaming responses.", true))
		props.Set("supportsFinishReason", comschOptBool("Whether streamed responses include finish_reason. When false, pi infers stop or toolUse when the stream ends.", true))
		props.Set("maxTokensField", typebox.Optional(typebox.Union([]*typebox.Schema{
			StringLiteral("max_completion_tokens"),
			StringLiteral("max_tokens"),
		}, jsonx.ObjectOf("description", "Which field to use for max tokens. Default: auto-detected from URL."))))
		props.Set("requiresToolResultName", comschOptBool("Whether tool results require the name field. Default: auto-detected from URL."))
		props.Set("requiresAssistantAfterToolResult", comschOptBool("Whether a user message after tool results requires an assistant message in between. Default: auto-detected from URL."))
		props.Set("requiresNonNullAssistantContent", comschOptBool(`Whether replayed assistant messages with no text (e.g. tool-call-only messages) must send "" instead of null content. Default: false.`))
		props.Set("requiresToolsWithToolHistory", comschOptBool(`Whether requests without tools must send "tools": [] when the conversation contains tool calls or tool results. Needed by some proxies in front of Anthropic, such as older LiteLLM versions. Default: false, the field is omitted.`))
		props.Set("requiresThinkingAsText", comschOptBool("Whether thinking blocks must be converted to text blocks with <thinking> delimiters. Default: auto-detected from URL."))
		props.Set("requiresReasoningContentOnAssistantMessages", comschOptBool("Whether all replayed assistant messages must include an empty reasoning_content field when reasoning is enabled. Default: auto-detected from URL."))
		props.Set("thinkingFormat", typebox.Optional(typebox.Union([]*typebox.Schema{
			StringLiteral("openai"),
			StringLiteral("openrouter"),
			StringLiteral("deepseek"),
			StringLiteral("together"),
			StringLiteral("baseten"),
			StringLiteral("zai"),
			StringLiteral("qwen"),
			StringLiteral("chat-template"),
			StringLiteral("qwen-chat-template"),
			StringLiteral("string-thinking"),
			StringLiteral("ant-ling"),
		}, jsonx.ObjectOf(
			"description", `Format for reasoning or thinking parameters. When omitted, Pi auto-detects the format from the provider URL. "openai" uses reasoning_effort, "openrouter" uses reasoning.effort, "deepseek" uses thinking.type plus reasoning_effort when supported, "together" uses reasoning.enabled plus reasoning_effort when supported, "baseten" uses configurable chat_template_args plus reasoning_effort when supported, "zai" uses thinking.type, "qwen" uses top-level enable_thinking, "qwen-chat-template" uses chat_template_kwargs.enable_thinking and preserve_thinking, "chat-template" uses configurable chat_template_kwargs, "string-thinking" uses top-level thinking, and "ant-ling" uses reasoning.effort only when the mapped effort is non-null.`,
		))))
		props.Set("chatTemplateKwargs", typebox.Optional(StringRecord(ChatTemplateKwargValueSchema, jsonx.ObjectOf(
			"description", `Kwargs sent as chat_template_kwargs when thinkingFormat is "chat-template". Use $var with "thinking.enabled", "thinking.effort", or "thinking.budget" for pi-controlled values.`,
		))))
		props.Set("chatTemplateArgs", typebox.Optional(StringRecord(ChatTemplateKwargValueSchema, jsonx.ObjectOf(
			"description", `Arguments sent as chat_template_args when thinkingFormat is "baseten". Use $var with "thinking.enabled", "thinking.effort", or "thinking.budget" for pi-controlled values.`,
		))))
		props.Set("openRouterRouting", typebox.Optional(OpenRouterRoutingSchema))
		props.Set("vercelGatewayRouting", typebox.Optional(VercelGatewayRoutingSchema))
		props.Set("zaiToolStream", comschOptBool("Whether z.ai supports top-level tool_stream for streaming tool call deltas.", false))
		props.Set("thinkingTokenBudgetField", typebox.Optional(comschThinkingTokenBudgetField(jsonx.ObjectOf(
			"description", `Top-level request field used to cap reasoning tokens from thinkingBudgets. Reasoning and the answer share max_tokens on these endpoints. "thinking_token_budget" is vLLM, "thinking_budget" is Qwen, DashScope, or SGLang, and "thinking_budget_tokens" is llama.cpp. Off by default and not set on the generated catalog.`,
		))))
		props.Set("supportsThinkingTokenBudget", comschOptBool(`Alias for thinkingTokenBudgetField: "thinking_token_budget" (vLLM). Prefer thinkingTokenBudgetField.`, false))
		props.Set("supportsOpenAIGrammarTools", comschOptBool("Whether the provider supports OpenAI custom tools with Lark or regex grammar formats. When false, grammar-constrained tools fall back to normal function tools. The generated catalog enables this for capable models.", false))
		props.Set("supportsMidConvoSystemMessages", comschOptBool("Whether the exact model accepts system or developer messages after the conversation has started. When false, later system messages are folded into the leading system message. The generated catalog enables this for verified models.", false))
		props.Set("supportsMidConvoToolAdditions", comschOptBool("Whether system messages can introduce additional tools mid-conversation. Requires supportsMidConvoSystemMessages. The generated catalog enables this for capable models.", false))
		props.Set("supportsStrictMode", comschOptBool("Whether the provider supports the strict field in tool definitions. Generated capable models enable it explicitly.", false))
		props.Set("cacheControlFormat", typebox.Optional(StringLiteral("anthropic", jsonx.ObjectOf(
			"description", "Cache control convention for prompt caching. Anthropic applies cache_control markers to the system prompt, last tool definition, and last user, assistant, or tool-result text content.",
		))))
		props.Set("sendSessionAffinityHeaders", comschOptBool("Whether to send session-affinity data from options.sessionId. Default: true for OpenRouter endpoints, false otherwise."))
		props.Set("sessionAffinityFormat", typebox.Optional(comschSessionAffinityFormat(jsonx.ObjectOf(
			"description", "Session-affinity header format. openai sends session_id, x-client-request-id, and x-session-affinity; openai-nosession sends x-client-request-id and x-session-affinity; openrouter sends x-session-id. Does not affect prompt_cache_key. Default: auto-detected.",
		))))
		props.Set("promptCacheKeyMode", comschOptPromptCacheKeyMode(jsonx.ObjectOf(
			"description", "Controls the prompt_cache_key request field. auto sends it to api.openai.com, or with long cache retention when supportsLongCacheRetention is true; enabled sends it whenever caching is active, for compatible proxies; disabled never sends it. Default: auto.",
		)))
		props.Set("supportsLongCacheRetention", comschOptBool(`Whether the provider supports long prompt cache retention (prompt_cache_retention: "24h" or Anthropic-style cache_control.ttl: "1h", depending on format). Default: auto-detected from provider and URL.`))
		props.Set("vllmPriority", typebox.Optional(typebox.Number(jsonx.ObjectOf(
			"description", "vLLM scheduler priority sent as the top-level priority request field. Lower values are handled earlier and the server default is 0. Only meaningful with --scheduling-policy priority. Off by default and not set on the generated catalog.",
		))))
		return props
	}()

	OpenAICompletionsCompatSchema = OpenObject(comschOpenAICompletionsCompatProperties, jsonx.ObjectOf(
		"description", "Compatibility settings for OpenAI-compatible completions APIs. Use this to override URL-based auto-detection for custom providers.",
		"additionalProperties", true,
	))

	comschOpenAIResponsesCompatProperties = func() *omap.Map[string, *typebox.Schema] {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("supportsDeveloperRole", comschOptBool("Whether the provider supports the developer role instead of system.", true))
		props.Set("supportsMidConvoSystemMessages", comschOptBool("Whether the exact model accepts developer or system messages after the conversation has started. When false, later system messages are folded into the leading system message. The generated catalog enables this for verified models.", false))
		props.Set("sessionAffinityFormat", typebox.Optional(comschSessionAffinityFormat(jsonx.ObjectOf(
			"description", "Session-affinity header format. openai sends session-id and x-client-request-id; openai-nosession sends x-client-request-id; openrouter sends x-session-id. Does not affect prompt_cache_key. Default: auto-detected.",
		))))
		props.Set("promptCacheKeyMode", comschOptPromptCacheKeyMode(jsonx.ObjectOf(
			"description", "Controls the prompt_cache_key request field. auto and enabled send it whenever caching is active; disabled never sends it, for compatible proxies that reject it. Default: auto.",
		)))
		props.Set("supportsLongCacheRetention", comschOptBool(`Whether the provider supports long prompt cache retention. This uses prompt_cache_options.ttl: "30m" on GPT-5.6+ and prompt_cache_retention: "24h" on earlier models.`, true))
		props.Set("supportsStrictMode", comschOptBool("Whether the provider supports strict JSON-schema function tools. Defaults are API-specific; generated OpenAI models enable it explicitly."))
		props.Set("supportsOpenAIGrammarTools", comschOptBool("Whether to emit OpenAI custom tools with Lark or regex grammar formats. When false, grammar-constrained tools fall back to normal function tools. The generated catalog enables this for capable models.", false))
		props.Set("supportsAdditionalTools", comschOptBool("Whether the model supports message-anchored additional_tools input items.", false))
		props.Set("supportsToolSearch", comschOptBool("Whether the model supports client-executed tool search for transcript-anchored additions.", false))
		props.Set("supportsExplicitPromptCacheMode", comschOptBool("Whether the model accepts prompt_cache_options. Older OpenAI models reject the parameter.", false))
		props.Set("supportsMidConvoEffort", comschOptBool("Whether thinking level changes are sent as configuration_update input items instead of changing reasoning.effort, so a change keeps the prompt cache. Only used by the openai-responses API, and ignored with Sign in with ChatGPT on the openai provider.", false))
		props.Set("supportsMaxOutputTokens", comschOptBool("Whether the provider accepts max_output_tokens. Some Codex-protocol gateways reject it.", true))
		return props
	}()

	OpenAIResponsesCompatSchema = OpenObject(comschOpenAIResponsesCompatProperties, jsonx.ObjectOf(
		"description", "Compatibility settings for OpenAI Responses APIs.",
		"additionalProperties", true,
	))

	comschAnthropicMessagesCompatProperties = func() *omap.Map[string, *typebox.Schema] {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("supportsEagerToolInputStreaming", comschOptBool("Whether the provider accepts per-tool eager_input_streaming. When false, the Anthropic provider omits tools[].eager_input_streaming and sends the legacy fine-grained-tool-streaming-2025-05-14 beta header for tool-enabled requests.", true))
		props.Set("supportsLongCacheRetention", comschOptBool("Whether the provider supports Anthropic long cache retention through cache_control.ttl.", true))
		props.Set("sendSessionAffinityHeaders", comschOptBool("Whether to send x-session-affinity from options.sessionId when caching is enabled. Required for providers like Fireworks that use session affinity for prompt cache routing; requests to the same replica maximize cache hits. Default: true for OpenRouter endpoints, false otherwise."))
		props.Set("sessionAffinityFormat", typebox.Optional(StringLiteral("openrouter", jsonx.ObjectOf(
			"description", "Session-affinity format. openrouter sends x-session-id; when unset, sends x-session-affinity.",
		))))
		props.Set("supportsCacheControlOnTools", comschOptBool("Whether the provider supports Anthropic-style cache_control markers on tool definitions. When false, cache_control is omitted from tool parameters. Some Anthropic-compatible providers, such as Fireworks, do not support this field on tools and may reject or ignore it.", true))
		props.Set("supportsTemperature", comschOptBool("Whether the model accepts the Anthropic temperature request field. Claude Opus 4.7+ rejects non-default values.", true))
		props.Set("forceAdaptiveThinking", comschOptBool("Whether to force adaptive thinking (thinking.type: adaptive plus output_config.effort) regardless of model ID. Built-in models that require adaptive thinking set this in generated metadata. Custom Anthropic-compatible providers can set this to true for any model whose upstream requires the adaptive format. Set false to opt out on overridden built-in models.", false))
		props.Set("allowEmptySignature", comschOptBool("Whether to replay empty thinking signatures instead of converting thinking to text.", false))
		props.Set("supportsStrictTools", comschOptBool("Whether the provider supports Anthropic strict tool schemas. Generated Anthropic models enable it explicitly.", false))
		props.Set("supportsMidConvoEffort", comschOptBool("Whether the exact model transport supports effort-only system messages and thinking binding controls.", false))
		props.Set("supportsMidConvoSystemMessages", comschOptBool("Whether the exact model accepts system-role messages inside the conversation. When false, later system messages are folded into the top-level system prompt.", false))
		props.Set("supportsMidConvoToolChanges", comschOptBool("Whether the exact model accepts mid-conversation tool_addition and tool_removal blocks. Requires supportsMidConvoSystemMessages.", false))
		props.Set("supportsMessageThreads", comschOptBool("Whether the endpoint supports Anthropic message threads, which let a request with a session id send only the new messages and continue from the previous reply. Default: true for a baseUrl on api.anthropic.com, false otherwise. Set true for gateways that forward the thread request field."))
		props.Set("allowedFallbackModels", typebox.Optional(typebox.Array(AnthropicAllowedFallbackModelSchema, jsonx.ObjectOf(
			"maxItems", 3,
			"description", "Models Anthropic accepts for server-side refusal fallback, with local pricing metadata for returned fallback responses. When absent or empty, callers must omit fallbacks; Anthropic rejects the field for models with no permitted fallback targets.",
		))))
		return props
	}()

	AnthropicMessagesCompatSchema = OpenObject(comschAnthropicMessagesCompatProperties, jsonx.ObjectOf(
		"description", "Compatibility settings for Anthropic Messages-compatible APIs.",
		"additionalProperties", true,
	))

	comschBedrockCompatProperties = func() *omap.Map[string, *typebox.Schema] {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("supportsStrictMode", comschOptBool("Whether the model supports Bedrock strict tool schemas.", false))
		return props
	}()

	BedrockCompatSchema = OpenObject(comschBedrockCompatProperties, jsonx.ObjectOf(
		"description", "Compatibility settings for Amazon Bedrock models.",
		"additionalProperties", true,
	))

	comschMistralConversationsCompatProperties = func() *omap.Map[string, *typebox.Schema] {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("supportsMidConvoSystemMessages", comschOptBool("Whether the exact model accepts system messages after the conversation has started. When false, later system messages are folded into the leading system message.", false))
		return props
	}()

	MistralConversationsCompatSchema = OpenObject(comschMistralConversationsCompatProperties, jsonx.ObjectOf(
		"description", "Compatibility settings for the Mistral chat API.",
		"additionalProperties", true,
	))

	comschSystemOneCompatProperties = func() *omap.Map[string, *typebox.Schema] {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("imageInput", typebox.Optional(typebox.Union([]*typebox.Schema{
			StringLiteral("assets"),
			StringLiteral("images"),
			StringLiteral("state-image-urls"),
		}, jsonx.ObjectOf(
			"description", `How the service takes classifier images. "assets" sends TypeSafe's assets map (PNG and JPEG) and references the images from the state, as TypeSafe and Jev on Workers AI expect. "images" sends Clef's images list of data URLs (PNG, JPEG, and WebP, at most four), as Clef on Workers AI expects. "state-image-urls" places image_url parts before the state, as OpenRouter expects; OpenRouter drops assets without an error. Auto-detected from the provider, baseUrl, and model ID. Classifier models read it from the provider-level compat; models and modelOverrides entries apply to chat models only.`,
		))))
		props.Set("sendImageOptInHeader", comschOptBool("Whether requests with images send the TypeSafe-Danger-Zone-Images: true header, which TypeSafe's own API requires for its experimental image input. Defaults to true for the typesafe-system-one API unless the model is detected as OpenRouter."))
		return props
	}()

	SystemOneCompatSchema = OpenObject(comschSystemOneCompatProperties, jsonx.ObjectOf(
		"description", "Compatibility settings for System One classifier APIs.",
		"additionalProperties", true,
	))

	comschProviderCompatPropertyOverrides = func() *omap.Map[string, *typebox.Schema] {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("supportsDeveloperRole", comschOptBool("Whether the provider supports the developer role instead of system. Defaults are API-specific."))
		props.Set("supportsMidConvoSystemMessages", comschOptBool("Whether the exact model accepts system or developer messages after the conversation has started. When false, later system messages are folded into the leading system message.", false))
		props.Set("sessionAffinityFormat", typebox.Optional(comschSessionAffinityFormat(jsonx.ObjectOf(
			"description", "Session-affinity header format. Defaults are API-specific or auto-detected.",
		))))
		props.Set("promptCacheKeyMode", comschOptPromptCacheKeyMode(jsonx.ObjectOf(
			"description", "Controls the prompt_cache_key request field for openai-completions and openai-responses: auto keeps each API's default, enabled sends it whenever caching is active, disabled never sends it. Default: auto.",
		)))
		props.Set("supportsMidConvoEffort", comschOptBool("Whether thinking level changes are sent inside the conversation instead of changing the request's effort, so a change keeps the prompt cache. Used by the anthropic-messages and openai-responses APIs.", false))
		props.Set("supportsLongCacheRetention", comschOptBool("Whether the provider supports long prompt cache retention. Defaults are API-specific or auto-detected."))
		props.Set("supportsStrictMode", comschOptBool("Whether the provider supports strict tool schemas. Defaults are API-specific."))
		props.Set("supportsOpenAIGrammarTools", comschOptBool("Whether the provider supports OpenAI custom tools with Lark or regex grammar formats. When false, grammar-constrained tools fall back to normal function tools.", false))
		props.Set("sendSessionAffinityHeaders", comschOptBool("Whether to send session-affinity data from options.sessionId. Defaults are API-specific."))
		return props
	}()

	CompatSchemasByApi = func() *omap.Map[string, *typebox.Schema] {
		m := omap.NewMap[string, *typebox.Schema]()
		m.Set("openai-completions", OpenAICompletionsCompatSchema)
		m.Set("openai-responses", OpenAIResponsesCompatSchema)
		m.Set("azure-openai-responses", OpenAIResponsesCompatSchema)
		m.Set("openai-codex-responses", OpenAIResponsesCompatSchema)
		m.Set("anthropic-messages", AnthropicMessagesCompatSchema)
		m.Set("bedrock-converse-stream", BedrockCompatSchema)
		m.Set("mistral-conversations", MistralConversationsCompatSchema)
		m.Set("typesafe-system-one", SystemOneCompatSchema)
		m.Set("cloudflare-workers-ai-system-one", SystemOneCompatSchema)
		return m
	}()

	comschProviderCompatProperties = comschMergeCompatProperties([]*omap.Map[string, *typebox.Schema]{
		comschOpenAICompletionsCompatProperties,
		comschOpenAIResponsesCompatProperties,
		comschAnthropicMessagesCompatProperties,
		comschBedrockCompatProperties,
		comschMistralConversationsCompatProperties,
		comschSystemOneCompatProperties,
	}, comschProviderCompatPropertyOverrides)

	ProviderCompatSchema = OpenObject(comschProviderCompatProperties, jsonx.ObjectOf(
		"description", "Provider and model compatibility overrides.",
		"additionalProperties", true,
	))
)
