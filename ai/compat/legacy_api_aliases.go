// Ported from packages/ai/src/legacy-api-aliases.ts (pi v1.5.2).

package compat

import (
	"context"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
)

var (
	laaAnthropicMessagesStreams    = api.AnthropicMessagesApi()
	laaAzureOpenAIResponsesStreams = api.AzureOpenAIResponsesApi()
	laaGoogleGenerativeAIStreams   = api.GoogleGenerativeAiApi()
	laaGoogleVertexStreams         = api.GoogleVertexApi()
	laaMistralConversationsStreams = api.MistralConversationsApi()
	laaOpenAICodexResponsesStreams = api.OpenAICodexResponsesApi()
	laaOpenAICompletionsStreams    = api.OpenAICompletionsApi()
	laaOpenAIResponsesStreams      = api.OpenAIResponsesApi()
)

// StreamAnthropic is deprecated: use api.StreamAnthropicMessages or api.AnthropicMessagesApi().Stream.
func StreamAnthropic(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaAnthropicMessagesStreams.Stream(ctx, model, c, options)
}

// StreamSimpleAnthropic is deprecated: use api.StreamSimpleAnthropicMessages or api.AnthropicMessagesApi().StreamSimple.
func StreamSimpleAnthropic(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaAnthropicMessagesStreams.StreamSimple(ctx, model, c, options)
}

// StreamAzureOpenAIResponses is deprecated: use api.StreamAzureOpenaiResponses or api.AzureOpenAIResponsesApi().Stream.
func StreamAzureOpenAIResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaAzureOpenAIResponsesStreams.Stream(ctx, model, c, options)
}

// StreamSimpleAzureOpenAIResponses is deprecated: use api.StreamSimpleAzureOpenaiResponses or api.AzureOpenAIResponsesApi().StreamSimple.
func StreamSimpleAzureOpenAIResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaAzureOpenAIResponsesStreams.StreamSimple(ctx, model, c, options)
}

// StreamGoogle is deprecated: use api.StreamGoogleGenerativeAi or api.GoogleGenerativeAiApi().Stream.
func StreamGoogle(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaGoogleGenerativeAIStreams.Stream(ctx, model, c, options)
}

// StreamSimpleGoogle is deprecated: use api.StreamSimpleGoogleGenerativeAi or api.GoogleGenerativeAiApi().StreamSimple.
func StreamSimpleGoogle(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaGoogleGenerativeAIStreams.StreamSimple(ctx, model, c, options)
}

// StreamGoogleVertex is deprecated: use api.StreamGoogleVertex or api.GoogleVertexApi().Stream.
func StreamGoogleVertex(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaGoogleVertexStreams.Stream(ctx, model, c, options)
}

// StreamSimpleGoogleVertex is deprecated: use api.StreamSimpleGoogleVertex or api.GoogleVertexApi().StreamSimple.
func StreamSimpleGoogleVertex(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaGoogleVertexStreams.StreamSimple(ctx, model, c, options)
}

// StreamMistral is deprecated: use api.StreamMistralConversations or api.MistralConversationsApi().Stream.
func StreamMistral(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaMistralConversationsStreams.Stream(ctx, model, c, options)
}

// StreamSimpleMistral is deprecated: use api.StreamSimpleMistralConversations or api.MistralConversationsApi().StreamSimple.
func StreamSimpleMistral(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaMistralConversationsStreams.StreamSimple(ctx, model, c, options)
}

// StreamOpenAICodexResponses is deprecated: use api.StreamOpenaiCodexResponses or api.OpenAICodexResponsesApi().Stream.
func StreamOpenAICodexResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaOpenAICodexResponsesStreams.Stream(ctx, model, c, options)
}

// StreamSimpleOpenAICodexResponses is deprecated: use api.StreamSimpleOpenaiCodexResponses or api.OpenAICodexResponsesApi().StreamSimple.
func StreamSimpleOpenAICodexResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaOpenAICodexResponsesStreams.StreamSimple(ctx, model, c, options)
}

// StreamOpenAICompletions is deprecated: use api.StreamOpenaiCompletions or api.OpenAICompletionsApi().Stream.
func StreamOpenAICompletions(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaOpenAICompletionsStreams.Stream(ctx, model, c, options)
}

// StreamSimpleOpenAICompletions is deprecated: use api.StreamSimpleOpenaiCompletions or api.OpenAICompletionsApi().StreamSimple.
func StreamSimpleOpenAICompletions(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaOpenAICompletionsStreams.StreamSimple(ctx, model, c, options)
}

// StreamOpenAIResponses is deprecated: use api.StreamOpenaiResponses or api.OpenAIResponsesApi().Stream.
func StreamOpenAIResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaOpenAIResponsesStreams.Stream(ctx, model, c, options)
}

// StreamSimpleOpenAIResponses is deprecated: use api.StreamSimpleOpenaiResponses or api.OpenAIResponsesApi().StreamSimple.
func StreamSimpleOpenAIResponses(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	return laaOpenAIResponsesStreams.StreamSimple(ctx, model, c, options)
}
