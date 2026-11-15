// Ported from packages/ai/src/api/bedrock-converse-stream.ts (pi v1.5.2).

package api

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/auth/bearer"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// BedrockThinkingDisplay controls how Claude's thinking content is returned in Bedrock responses.
type BedrockThinkingDisplay string

const (
	BedrockThinkingDisplaySummarized BedrockThinkingDisplay = "summarized"
	BedrockThinkingDisplayOmitted    BedrockThinkingDisplay = "omitted"
)

// BedrockToolChoice represents an explicit tool choice directive for Bedrock Converse.
type BedrockToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitzero"`
}

// BedrockOptions configures Amazon Bedrock ConverseStream requests.
type BedrockOptions struct {
	ai.StreamOptions
	Region              *string                 `json:"region,omitzero"`
	Profile             *string                 `json:"profile,omitzero"`
	ToolChoice          *BedrockToolChoice      `json:"toolChoice,omitzero"`
	Reasoning           *ai.ThinkingLevel       `json:"reasoning,omitzero"`
	ThinkingBudgets     *ai.ThinkingBudgets     `json:"thinkingBudgets,omitzero"`
	InterleavedThinking *bool                   `json:"interleavedThinking,omitzero"`
	ThinkingDisplay     *BedrockThinkingDisplay `json:"thinkingDisplay,omitzero"`
	RequestMetadata     map[string]string       `json:"requestMetadata,omitzero"`
	BearerToken         *string                 `json:"bearerToken,omitzero"`
}

const (
	bcsEmptyTextPlaceholder          = "<empty>"
	bcsThinkingBindingControlsBeta   = "thinking-binding-controls-2026-08-01"
	bcsRedactedThinkingPlaceholder   = "[Reasoning redacted]"
	bcsDataRetentionDocsURL          = "https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html"
	bcsMaxBedrockDiagnosticValueChar = 200
	bcsCustomHeadersMiddlewareName   = "pi-ai-custom-headers"
	bcsResponseHeadersMiddlewareName = "pi-ai-response-headers"
)

var (
	bcsArnRegionRegex             = regexp.MustCompile(`^arn:aws(?:-[a-z0-9-]+)?:bedrock:([a-z0-9-]+):`)
	bcsStandardEndpointRegex      = regexp.MustCompile(`^bedrock-runtime(?:-fips)?\.([a-z0-9-]+)\.amazonaws\.com(?:\.cn)?$`)
	bcsNormalizeToolCallCharRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	bcsModelCandidateSepRegex     = regexp.MustCompile(`[\s_.:]+`)
	bcsDataRetentionRegex         = regexp.MustCompile(`(?i)data retention mode`)
)

var bcsBedrockErrorPrefixes = map[string]string{
	"InternalServerException":     "Internal server error",
	"ModelStreamErrorException":   "Model stream error",
	"ValidationException":         "Validation error",
	"ThrottlingException":         "Throttling error",
	"ServiceUnavailableException": "Service unavailable",
}

type bcsOpenBlock struct {
	contentIndex   int
	arguments      *ai.ToolCallArgumentsStream
	redactedChunks [][]byte
}

type bcsOpenBlocks map[int32]*bcsOpenBlock

type bcsResolvedClientSettings struct {
	profile                     string
	optionsProfile              string
	hasAmbientConfiguredProfile bool
	region                      string
	baseEndpoint                string
	skipAuth                    bool
	bearerToken                 string
	useBearerToken              bool
	staticAccessKeyID           string
	staticSecretAccessKey       string
	staticSessionToken          string
	hasStaticCredentials        bool
	appID                       string
	customUserAgent             string
	proxyURL                    *url.URL
	forceHTTP1                  bool
	customHeaders               []bcsHeaderPair
}

type bcsHeaderPair struct {
	key   string
	value string
}

// StreamBedrockConverseStream streams responses using the Amazon Bedrock ConverseStream API.
func StreamBedrockConverseStream(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	opts := bcsNormalizeOptions(options)
	normalizedContext := ai.CollapseSystemMessages(c)

	go func() {
		output := &ai.AssistantMessage{
			Role:     "assistant",
			Content:  ai.ContentList{},
			Api:      ai.KnownApiBedrockConverseStream,
			Provider: model.Provider,
			Model:    model.Id,
			Usage: ai.Usage{
				Input:       0,
				Output:      0,
				CacheRead:   0,
				CacheWrite:  0,
				TotalTokens: 0,
				Cost:        ai.UsageCost{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0, Total: 0},
			},
			StopReason: ai.StopReasonPending,
			Timestamp:  time.Now().UnixMilli(),
		}

		openBlocks := make(bcsOpenBlocks)
		var responseRequestId *string

		failStream := func(err error) {
			bcsFlushOpenBlocks(openBlocks, output)
			if ctx != nil && ctx.Err() != nil {
				output.StopReason = ai.StopReasonAborted
			} else {
				output.StopReason = ai.StopReasonError
			}
			msg := bcsFormatBedrockError(err)
			output.ErrorMessage = &msg
			if output.StopReason == ai.StopReasonError {
				bcsAppendBedrockFailureDiagnostic(output, err, responseRequestId)
			}
			stream.Push(&ai.AssistantMessageEventError{
				Type:   "error",
				Reason: output.StopReason,
				Error:  output,
			})
			stream.End()
		}

		settings, err := bcsResolveClientSettings(model, opts)
		if err != nil {
			failStream(err)
			return
		}

		var observedRawResponse bool
		var reqCtx context.Context = ctx
		if reqCtx == nil {
			reqCtx = context.Background()
		}
		// TimeoutMs bounds waiting for the response headers via Transport.ResponseHeaderTimeout
		// inside bcsCreateBedrockClient, not the full streaming lifetime.
		client, err := bcsCreateBedrockClient(reqCtx, model, opts, settings, &observedRawResponse)
		if err != nil {
			failStream(err)
			return
		}

		var env ai.ProviderEnv
		if opts != nil {
			env = opts.Env
		}
		supportsStrictMode := bcsSupportsStrictMode(model)
		cacheRetention := bcsResolveCacheRetention(bcsGetCacheRetention(opts), env)
		inferenceMaxTokens := bcsResolveInferenceMaxTokens(model, opts)
		initialSystemMessage := ai.GetInitialSystemMessage(normalizedContext.Messages)
		var initialSystemPrompt string
		if initialSystemMessage != nil {
			initialSystemPrompt = ai.GetSystemMessageText(initialSystemMessage)
		}

		var sessionId *string
		if opts != nil {
			sessionId = opts.SessionId
		}
		disablePreferredStrictTools := HasExceededStrictToolLimits(sessionId)

		var response *bedrockruntime.ConverseStreamOutput
		for {
			currentTools := ai.GetCurrentTools(normalizedContext.Messages)
			var toolChoice *BedrockToolChoice
			if opts != nil {
				toolChoice = opts.ToolChoice
			}
			toolConfig, hasPreferredStrictTools, err := bcsConvertToolConfig(
				currentTools,
				toolChoice,
				supportsStrictMode,
				disablePreferredStrictTools,
			)
			if err != nil {
				failStream(err)
				return
			}

			msgs, err := bcsConvertMessages(normalizedContext, model, cacheRetention, env)
			if err != nil {
				failStream(err)
				return
			}

			var infCfg *bedrocktypes.InferenceConfiguration
			if inferenceMaxTokens != nil || (opts != nil && opts.Temperature != nil) {
				infCfg = &bedrocktypes.InferenceConfiguration{}
				if inferenceMaxTokens != nil {
					v := int32(*inferenceMaxTokens)
					infCfg.MaxTokens = &v
				}
				if opts != nil && opts.Temperature != nil {
					v := float32(*opts.Temperature)
					infCfg.Temperature = &v
				}
			}

			commandInput := &bedrockruntime.ConverseStreamInput{
				ModelId:                      aws.String(model.Id),
				Messages:                     msgs,
				System:                       bcsBuildSystemPrompt(initialSystemPrompt, model, cacheRetention, env),
				InferenceConfig:              infCfg,
				ToolConfig:                   toolConfig,
				AdditionalModelRequestFields: bcsBuildAdditionalModelRequestFields(model, opts),
			}
			if opts != nil && len(opts.RequestMetadata) > 0 {
				commandInput.RequestMetadata = opts.RequestMetadata
			}

			if opts != nil && opts.OnPayload != nil {
				nextPayload, err := opts.OnPayload(commandInput, model)
				if err != nil {
					failStream(err)
					return
				}
				if nextInput, ok := nextPayload.(*bedrockruntime.ConverseStreamInput); ok && nextInput != nil {
					commandInput = nextInput
				}
			}

			response, err = client.ConverseStream(reqCtx, commandInput)
			if err == nil {
				break
			}

			if (reqCtx.Err() != nil) ||
				disablePreferredStrictTools ||
				!hasPreferredStrictTools ||
				!bcsIsStrictToolValidationError(err) {
				failStream(err)
				return
			}

			disablePreferredStrictTools = true
			MarkStrictToolLimitsExceeded(sessionId)
			errMsg := bcsExtractAPIErrorMessage(err)
			ai.AppendAssistantMessageDiagnostic(output, ai.CreateAssistantMessageDiagnostic(
				"bedrock_strict_tools_fallback",
				ai.DiagnosticErrorInfo{Message: errMsg},
				jsonx.ObjectOf("reason", "strict_tool_limits_exceeded"),
			))
		}

		if response == nil {
			failStream(errors.New("Bedrock returned empty response"))
			return
		}

		if reqId, ok := awsmiddleware.GetRequestIDMetadata(response.ResultMetadata); ok {
			responseRequestId = bcsNormalizeDiagnosticValue(reqId)
		}

		if !observedRawResponse {
			if opts != nil && opts.OnResponse != nil {
				status := bcsExtractStatusFromMetadata(response.ResultMetadata)
				if status != 0 || responseRequestId != nil {
					if status == 0 {
						status = http.StatusOK
					}
					headers := map[string]string{}
					if responseRequestId != nil {
						headers["x-amzn-requestid"] = *responseRequestId
					}
					if cbErr := opts.OnResponse(&ai.ProviderResponse{
						Status:  status,
						Headers: headers,
					}, model); cbErr != nil {
						failStream(cbErr)
						return
					}
				}
			}
		}

		eventStream := response.GetStream()
		if eventStream == nil {
			failStream(errors.New("Bedrock stream ended without a stop reason"))
			return
		}
		defer eventStream.Close()

		for event := range eventStream.Events() {
			if opts != nil && opts.OnProviderStreamEvent != nil {
				if err := opts.OnProviderStreamEvent(event, model); err != nil {
					failStream(err)
					return
				}
			}
			if err := bcsHandleStreamEvent(event, model, openBlocks, output, stream); err != nil {
				failStream(err)
				return
			}
		}

		if streamErr := eventStream.Err(); streamErr != nil {
			failStream(streamErr)
			return
		}

		if reqCtx.Err() != nil {
			failStream(errors.New("Request was aborted"))
			return
		}

		if output.StopReason == ai.StopReasonPending {
			failStream(errors.New("Bedrock stream ended without a stop reason"))
			return
		}
		if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
			msg := "An unknown error occurred"
			if output.ErrorMessage != nil && *output.ErrorMessage != "" {
				msg = *output.ErrorMessage
			}
			failStream(errors.New(msg))
			return
		}

		bcsFlushOpenBlocks(openBlocks, output)
		stream.Push(&ai.AssistantMessageEventDone{
			Type:    "done",
			Reason:  output.StopReason,
			Message: output,
		})
		stream.End(output)
	}()

	return stream
}

// StreamSimpleBedrockConverseStream maps provider-neutral SimpleStreamOptions to BedrockOptions.
func StreamSimpleBedrockConverseStream(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	base := BuildBaseOptions(model, c, options, nil)
	var bedrockToolChoice *BedrockToolChoice
	if options != nil && options.ToolChoice != nil {
		bedrockToolChoice = &BedrockToolChoice{Type: string(*options.ToolChoice)}
	}
	bedrockOpts := &BedrockOptions{
		StreamOptions: base,
		ToolChoice:    bedrockToolChoice,
	}

	if options == nil || options.Reasoning == nil {
		return StreamBedrockConverseStream(ctx, model, c, bedrockOpts)
	}

	if bcsIsAnthropicClaudeModel(model) {
		if bcsSupportsAdaptiveThinking(model.Id, model.Name) {
			bedrockOpts.Reasoning = options.Reasoning
			bedrockOpts.ThinkingBudgets = options.ThinkingBudgets
			return StreamBedrockConverseStream(ctx, model, c, bedrockOpts)
		}

		maxTokens, thinkingBudget := AdjustMaxTokensForThinking(
			base.MaxTokens,
			model.MaxTokens,
			*options.Reasoning,
			options.ThinkingBudgets,
		)
		maxTokens = ClampMaxTokensToContext(model, c, maxTokens)
		bedrockOpts.MaxTokens = &maxTokens
		bedrockOpts.Reasoning = options.Reasoning

		clamped := ClampReasoning(options.Reasoning)
		var clonedBudgets ai.ThinkingBudgets
		if options.ThinkingBudgets != nil {
			clonedBudgets = *options.ThinkingBudgets
		}
		budgetCap := max(0, maxTokens-1024)
		adjustedBudget := min(thinkingBudget, budgetCap)
		if clamped != nil {
			switch *clamped {
			case ai.ThinkingLevelMinimal:
				clonedBudgets.Minimal = &adjustedBudget
			case ai.ThinkingLevelLow:
				clonedBudgets.Low = &adjustedBudget
			case ai.ThinkingLevelMedium:
				clonedBudgets.Medium = &adjustedBudget
			case ai.ThinkingLevelHigh:
				clonedBudgets.High = &adjustedBudget
			}
		}
		bedrockOpts.ThinkingBudgets = &clonedBudgets
		return StreamBedrockConverseStream(ctx, model, c, bedrockOpts)
	}

	bedrockOpts.Reasoning = options.Reasoning
	bedrockOpts.ThinkingBudgets = options.ThinkingBudgets
	return StreamBedrockConverseStream(ctx, model, c, bedrockOpts)
}

func bcsNormalizeOptions(options ai.ProviderStreamOptions) *BedrockOptions {
	if options == nil {
		return &BedrockOptions{}
	}
	if typed, ok := options.(*BedrockOptions); ok {
		if typed == nil {
			return &BedrockOptions{}
		}
		return typed
	}
	if base := options.BaseStreamOptions(); base != nil {
		return &BedrockOptions{StreamOptions: *base}
	}
	return &BedrockOptions{}
}

func bcsResolveClientSettings(model *ai.Model, options *BedrockOptions) (bcsResolvedClientSettings, error) {
	var env ai.ProviderEnv
	if options != nil {
		env = options.Env
	}

	var optionsProfile string
	if options != nil && options.Profile != nil && *options.Profile != "" {
		optionsProfile = *options.Profile
	} else if env != nil && env["AWS_PROFILE"] != "" {
		optionsProfile = env["AWS_PROFILE"]
	}
	profile := optionsProfile
	if profile == "" {
		profile = ai.GetProviderEnvValue("AWS_PROFILE", env)
	}

	configuredRegion := bcsGetConfiguredBedrockRegion(options)
	hasAmbientConfiguredProfile := ai.GetProviderEnvValue("AWS_PROFILE", nil) != ""
	endpointRegion := bcsGetStandardBedrockEndpointRegion(model.BaseUrl)
	useExplicitEndpoint := bcsShouldUseExplicitBedrockEndpoint(
		model.BaseUrl,
		configuredRegion,
		hasAmbientConfiguredProfile,
	)

	var region string
	if m := bcsArnRegionRegex.FindStringSubmatch(model.Id); len(m) > 1 {
		region = m[1]
	} else if configuredRegion != "" {
		region = configuredRegion
	} else if endpointRegion != "" && useExplicitEndpoint {
		region = endpointRegion
	} else if !hasAmbientConfiguredProfile {
		region = "us-east-1"
	}

	var baseEndpoint string
	if useExplicitEndpoint && model.BaseUrl != "" {
		baseEndpoint = model.BaseUrl
	}

	skipAuth := ai.GetProviderEnvValue("AWS_BEDROCK_SKIP_AUTH", env) == "1"
	var bearerToken string
	if options != nil && options.BearerToken != nil && *options.BearerToken != "" {
		bearerToken = *options.BearerToken
	} else if options != nil && options.ApiKey != nil && *options.ApiKey != "" && *options.ApiKey != "<authenticated>" {
		bearerToken = *options.ApiKey
	} else {
		bearerToken = ai.GetProviderEnvValue("AWS_BEARER_TOKEN_BEDROCK", env)
	}
	useBearerToken := bearerToken != "" && !skipAuth

	var ak, sk, st string
	var hasStatic bool
	if skipAuth {
		ak = "dummy-access-key"
		sk = "dummy-secret-key"
		hasStatic = true
	} else if optionsProfile == "" {
		ak, sk, st, hasStatic = bcsGetConfiguredBedrockCredentials(env)
	}

	proxyURL, err := ai.ResolveHttpProxyUrlForTarget(model.BaseUrl, env)
	if err != nil {
		return bcsResolvedClientSettings{}, err
	}

	var orderedHeaders []bcsHeaderPair
	if options != nil && options.Headers != nil {
		if ordered := ai.ProviderHeadersToOrderedRecord(options.Headers); ordered != nil {
			for _, k := range ordered.Keys() {
				if v, ok := ordered.Get(k); ok {
					orderedHeaders = append(orderedHeaders, bcsHeaderPair{key: k, value: v})
				}
			}
		}
	}

	var identity *ai.ClientIdentity
	if options != nil {
		identity = options.Identity
	}
	appID, customUA := bcsBedrockUserAgent(identity)

	return bcsResolvedClientSettings{
		profile:                     profile,
		optionsProfile:              optionsProfile,
		hasAmbientConfiguredProfile: hasAmbientConfiguredProfile,
		region:                      region,
		baseEndpoint:                baseEndpoint,
		skipAuth:                    skipAuth,
		bearerToken:                 bearerToken,
		useBearerToken:              useBearerToken,
		staticAccessKeyID:           ak,
		staticSecretAccessKey:       sk,
		staticSessionToken:          st,
		hasStaticCredentials:        hasStatic,
		appID:                       appID,
		customUserAgent:             customUA,
		proxyURL:                    proxyURL,
		forceHTTP1:                  ai.GetProviderEnvValue("AWS_BEDROCK_FORCE_HTTP1", env) == "1",
		customHeaders:               orderedHeaders,
	}, nil
}

func bcsCreateBedrockClient(
	ctx context.Context,
	model *ai.Model,
	options *BedrockOptions,
	settings bcsResolvedClientSettings,
	observedRawResponse *bool,
) (*bedrockruntime.Client, error) {
	httpClient, err := bcsResolveHttpClient(options, settings)
	if err != nil {
		return nil, err
	}

	var loadOpts []func(*config.LoadOptions) error
	if settings.profile != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(settings.profile))
	}
	if settings.region != "" {
		loadOpts = append(loadOpts, config.WithRegion(settings.region))
	}
	if settings.hasStaticCredentials {
		loadOpts = append(loadOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				settings.staticAccessKeyID,
				settings.staticSecretAccessKey,
				settings.staticSessionToken,
			),
		))
	}
	if options != nil && options.Env != nil {
		if credFile := options.Env["AWS_SHARED_CREDENTIALS_FILE"]; credFile != "" {
			loadOpts = append(loadOpts, config.WithSharedCredentialsFiles([]string{credFile}))
		}
		if cfgFile := options.Env["AWS_CONFIG_FILE"]; cfgFile != "" {
			loadOpts = append(loadOpts, config.WithSharedConfigFiles([]string{cfgFile}))
		}
	}
	if httpClient != nil {
		loadOpts = append(loadOpts, config.WithHTTPClient(httpClient))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}

	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		if settings.region != "" {
			o.Region = settings.region
		}
		if settings.baseEndpoint != "" {
			o.BaseEndpoint = aws.String(settings.baseEndpoint)
		}
		if httpClient != nil {
			o.HTTPClient = httpClient
		}
		if settings.appID != "" {
			o.AppID = settings.appID
		}
		if settings.useBearerToken {
			o.BearerAuthTokenProvider = bearer.StaticTokenProvider{
				Token: bearer.Token{Value: settings.bearerToken},
			}
			o.AuthSchemePreference = []string{"httpBearerAuth"}
		}
		if settings.customUserAgent != "" {
			o.APIOptions = append(o.APIOptions, bcsAddCustomUserAgentMiddleware(settings.customUserAgent))
		}
		if options != nil && options.OnResponse != nil {
			o.APIOptions = append(o.APIOptions, bcsAddResponseHeadersMiddleware(options.OnResponse, model, func() {
				if observedRawResponse != nil {
					*observedRawResponse = true
				}
			}))
		}
		if len(settings.customHeaders) > 0 {
			o.APIOptions = append(o.APIOptions, bcsAddCustomHeadersMiddleware(settings.customHeaders))
		}
	})
	return client, nil
}

func bcsResolveHttpClient(options *BedrockOptions, settings bcsResolvedClientSettings) (*http.Client, error) {
	if options != nil && options.HTTPClient != nil {
		return options.HTTPClient, nil
	}
	hasTimeout := options != nil && options.TimeoutMs != nil && *options.TimeoutMs > 0
	if settings.proxyURL == nil && !settings.forceHTTP1 && !hasTimeout {
		return ai.NodeHttpProxyDefaultClient(), nil
	}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	var transport *http.Transport
	if ok {
		transport = baseTransport.Clone()
	} else {
		transport = &http.Transport{}
	}
	if settings.proxyURL != nil {
		transport.Proxy = http.ProxyURL(settings.proxyURL)
	}
	if settings.proxyURL != nil || settings.forceHTTP1 {
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	if hasTimeout {
		transport.ResponseHeaderTimeout = time.Duration(*options.TimeoutMs) * time.Millisecond
	}
	return &http.Client{Transport: transport}, nil
}

func bcsIsReservedHeader(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasPrefix(lower, "x-amz-") || lower == "authorization" || lower == "host"
}

func bcsBedrockUserAgent(identity *ai.ClientIdentity) (appID string, customUA string) {
	if identity == nil || identity.UserAgent.Val == nil {
		return "pi/" + ai.GetClientVersion(identity), ""
	}
	switch v := identity.UserAgent.Val.(type) {
	case ai.UserAgentModeKind:
		if v == ai.UserAgentModeShort {
			return "pi", ""
		}
		return "pi/" + ai.GetClientVersion(identity), ""
	case *ai.UserAgentModeCustom:
		if v != nil {
			return "", v.Custom
		}
	}
	return "pi/" + ai.GetClientVersion(identity), ""
}

func bcsAddCustomUserAgentMiddleware(customUA string) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc("pi-ai-custom-user-agent", func(
			ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler,
		) (middleware.BuildOutput, middleware.Metadata, error) {
			if req, ok := in.Request.(*smithyhttp.Request); ok && req != nil && req.Header != nil {
				req.Header.Set("User-Agent", customUA)
			}
			return next.HandleBuild(ctx, in)
		}), middleware.After)
	}
}

func bcsAddCustomHeadersMiddleware(headers []bcsHeaderPair) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc(bcsCustomHeadersMiddlewareName, func(
			ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler,
		) (middleware.BuildOutput, middleware.Metadata, error) {
			if req, ok := in.Request.(*smithyhttp.Request); ok && req != nil && req.Header != nil {
				for _, pair := range headers {
					if !bcsIsReservedHeader(pair.key) {
						req.Header.Set(pair.key, pair.value)
					}
				}
			}
			return next.HandleBuild(ctx, in)
		}), middleware.After)
	}
}

func bcsAddResponseHeadersMiddleware(
	onResponse func(response *ai.ProviderResponse, model *ai.Model) error,
	model *ai.Model,
	onObserved func(),
) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Deserialize.Insert(middleware.DeserializeMiddlewareFunc(bcsResponseHeadersMiddlewareName, func(
			ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
		) (middleware.DeserializeOutput, middleware.Metadata, error) {
			out, md, err := next.HandleDeserialize(ctx, in)
			if rawResp, ok := out.RawResponse.(*smithyhttp.Response); ok && rawResp != nil {
				if onObserved != nil {
					onObserved()
				}
				if cbErr := onResponse(&ai.ProviderResponse{
					Status:  rawResp.StatusCode,
					Headers: ai.HeadersToRecord(rawResp.Header),
				}, model); cbErr != nil {
					return out, md, cbErr
				}
			}
			return out, md, err
		}), "OperationDeserializer", middleware.After)
	}
}

func bcsHandleStreamEvent(
	event bedrocktypes.ConverseStreamOutput,
	model *ai.Model,
	openBlocks bcsOpenBlocks,
	output *ai.AssistantMessage,
	stream *ai.AssistantMessageEventStream,
) error {
	switch ev := event.(type) {
	case *bedrocktypes.ConverseStreamOutputMemberMessageStart:
		if ev.Value.Role != bedrocktypes.ConversationRoleAssistant {
			return errors.New("Unexpected assistant message start but got user message start instead")
		}
		stream.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: output})
	case *bedrocktypes.ConverseStreamOutputMemberContentBlockStart:
		bcsHandleContentBlockStart(ev.Value, openBlocks, output, stream)
	case *bedrocktypes.ConverseStreamOutputMemberContentBlockDelta:
		bcsHandleContentBlockDelta(ev.Value, openBlocks, output, stream)
	case *bedrocktypes.ConverseStreamOutputMemberContentBlockStop:
		bcsHandleContentBlockStop(ev.Value, openBlocks, output, stream)
	case *bedrocktypes.ConverseStreamOutputMemberMessageStop:
		raw := string(ev.Value.StopReason)
		if raw != "" {
			output.RawStopReason = &raw
		}
		stopReason, errMsg := bcsMapStopReason(raw)
		output.StopReason = stopReason
		if errMsg != "" {
			output.ErrorMessage = &errMsg
		}
	case *bedrocktypes.ConverseStreamOutputMemberMetadata:
		bcsHandleMetadata(ev.Value, model, output)
	}
	return nil
}

func bcsHandleContentBlockStart(
	event bedrocktypes.ContentBlockStartEvent,
	openBlocks bcsOpenBlocks,
	output *ai.AssistantMessage,
	stream *ai.AssistantMessageEventStream,
) {
	if event.ContentBlockIndex == nil {
		return
	}
	toolStart, ok := event.Start.(*bedrocktypes.ContentBlockStartMemberToolUse)
	if !ok {
		return
	}
	var id, name string
	if toolStart.Value.ToolUseId != nil {
		id = *toolStart.Value.ToolUseId
	}
	if toolStart.Value.Name != nil {
		name = *toolStart.Value.Name
	}
	output.Content = append(output.Content, &ai.ToolCall{
		Type:      "toolCall",
		Id:        id,
		Name:      name,
		Arguments: jsonx.NewObject(),
	})
	contentIndex := len(output.Content) - 1
	openBlocks[*event.ContentBlockIndex] = &bcsOpenBlock{
		contentIndex: contentIndex,
		arguments:    ai.NewToolCallArgumentsStream(),
	}
	stream.Push(&ai.AssistantMessageEventToolcallStart{
		Type:         "toolcall_start",
		ContentIndex: contentIndex,
		Partial:      output,
	})
}

func bcsHandleContentBlockDelta(
	event bedrocktypes.ContentBlockDeltaEvent,
	openBlocks bcsOpenBlocks,
	output *ai.AssistantMessage,
	stream *ai.AssistantMessageEventStream,
) {
	if event.ContentBlockIndex == nil {
		return
	}
	blockIdx := *event.ContentBlockIndex
	open := openBlocks[blockIdx]
	var block ai.Content
	if open != nil && open.contentIndex < len(output.Content) {
		block = output.Content[open.contentIndex]
	}

	switch d := event.Delta.(type) {
	case *bedrocktypes.ContentBlockDeltaMemberText:
		if open == nil {
			output.Content = append(output.Content, &ai.TextContent{
				Type: "text",
				Text: "",
			})
			open = &bcsOpenBlock{contentIndex: len(output.Content) - 1}
			openBlocks[blockIdx] = open
			block = output.Content[open.contentIndex]
			stream.Push(&ai.AssistantMessageEventTextStart{
				Type:         "text_start",
				ContentIndex: open.contentIndex,
				Partial:      output,
			})
		}
		if tc, ok := block.(*ai.TextContent); ok {
			tc.Text += d.Value
			stream.Push(&ai.AssistantMessageEventTextDelta{
				Type:         "text_delta",
				ContentIndex: open.contentIndex,
				Delta:        d.Value,
				Partial:      output,
			})
		}
	case *bedrocktypes.ContentBlockDeltaMemberToolUse:
		if tc, ok := block.(*ai.ToolCall); ok && open != nil && open.arguments != nil {
			var inputDelta string
			if d.Value.Input != nil {
				inputDelta = *d.Value.Input
			}
			tc.Arguments = open.arguments.Append(inputDelta)
			stream.Push(&ai.AssistantMessageEventToolcallDelta{
				Type:         "toolcall_delta",
				ContentIndex: open.contentIndex,
				Delta:        inputDelta,
				Partial:      output,
			})
		}
	case *bedrocktypes.ContentBlockDeltaMemberReasoningContent:
		if open == nil {
			emptySig := ""
			output.Content = append(output.Content, &ai.ThinkingContent{
				Type:              "thinking",
				Thinking:          "",
				ThinkingSignature: &emptySig,
			})
			open = &bcsOpenBlock{contentIndex: len(output.Content) - 1}
			openBlocks[blockIdx] = open
			block = output.Content[open.contentIndex]
			stream.Push(&ai.AssistantMessageEventThinkingStart{
				Type:         "thinking_start",
				ContentIndex: open.contentIndex,
				Partial:      output,
			})
		}
		th, ok := block.(*ai.ThinkingContent)
		if !ok {
			return
		}
		thinkingIndex := open.contentIndex
		isRedacted := th.Redacted != nil && *th.Redacted
		switch rc := d.Value.(type) {
		case *bedrocktypes.ReasoningContentBlockDeltaMemberText:
			if rc.Value != "" {
				th.Thinking += rc.Value
				stream.Push(&ai.AssistantMessageEventThinkingDelta{
					Type:         "thinking_delta",
					ContentIndex: thinkingIndex,
					Delta:        rc.Value,
					Partial:      output,
				})
			}
		case *bedrocktypes.ReasoningContentBlockDeltaMemberSignature:
			if rc.Value != "" && !isRedacted {
				prev := ""
				if th.ThinkingSignature != nil {
					prev = *th.ThinkingSignature
				}
				next := prev + rc.Value
				th.ThinkingSignature = &next
			}
		case *bedrocktypes.ReasoningContentBlockDeltaMemberRedactedContent:
			if len(rc.Value) > 0 {
				if !isRedacted {
					redacted := true
					emptySig := ""
					th.Redacted = &redacted
					th.ThinkingSignature = &emptySig
					th.Thinking += bcsRedactedThinkingPlaceholder
					stream.Push(&ai.AssistantMessageEventThinkingDelta{
						Type:         "thinking_delta",
						ContentIndex: thinkingIndex,
						Delta:        bcsRedactedThinkingPlaceholder,
						Partial:      output,
					})
				}
				open.redactedChunks = append(open.redactedChunks, rc.Value)
			}
		}
	}
}

func bcsFlushRedactedContent(open *bcsOpenBlock, output *ai.AssistantMessage) {
	if open == nil || open.contentIndex < 0 || open.contentIndex >= len(output.Content) || len(open.redactedChunks) == 0 {
		return
	}
	th, ok := output.Content[open.contentIndex].(*ai.ThinkingContent)
	if !ok {
		return
	}
	totalLen := 0
	for _, c := range open.redactedChunks {
		totalLen += len(c)
	}
	buf := make([]byte, 0, totalLen)
	for _, c := range open.redactedChunks {
		buf = append(buf, c...)
	}
	encoded := base64.StdEncoding.EncodeToString(buf)
	th.ThinkingSignature = &encoded
	open.redactedChunks = nil
}

func bcsFlushOpenBlocks(openBlocks bcsOpenBlocks, output *ai.AssistantMessage) {
	for _, open := range openBlocks {
		bcsFlushRedactedContent(open, output)
	}
	clear(openBlocks)
}

func bcsHandleMetadata(
	event bedrocktypes.ConverseStreamMetadataEvent,
	model *ai.Model,
	output *ai.AssistantMessage,
) {
	if event.Usage == nil {
		return
	}
	if event.Usage.InputTokens != nil {
		output.Usage.Input = int(*event.Usage.InputTokens)
	}
	if event.Usage.OutputTokens != nil {
		output.Usage.Output = int(*event.Usage.OutputTokens)
	}
	if event.Usage.CacheReadInputTokens != nil {
		output.Usage.CacheRead = int(*event.Usage.CacheReadInputTokens)
	}
	if event.Usage.CacheWriteInputTokens != nil {
		output.Usage.CacheWrite = int(*event.Usage.CacheWriteInputTokens)
	}
	if event.Usage.CacheDetails != nil {
		sum1h := 0
		for _, detail := range event.Usage.CacheDetails {
			if detail.Ttl == bedrocktypes.CacheTTLOneHour && detail.InputTokens != nil {
				sum1h += int(*detail.InputTokens)
			}
		}
		output.Usage.CacheWrite1h = &sum1h
	}
	if event.Usage.TotalTokens != nil && *event.Usage.TotalTokens != 0 {
		output.Usage.TotalTokens = int(*event.Usage.TotalTokens)
	} else {
		output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output
	}
	ai.CalculateCost(model, &output.Usage)
}

func bcsHandleContentBlockStop(
	event bedrocktypes.ContentBlockStopEvent,
	openBlocks bcsOpenBlocks,
	output *ai.AssistantMessage,
	stream *ai.AssistantMessageEventStream,
) {
	if event.ContentBlockIndex == nil {
		return
	}
	open := openBlocks[*event.ContentBlockIndex]
	if open == nil {
		return
	}
	delete(openBlocks, *event.ContentBlockIndex)
	index := open.contentIndex
	if index < 0 || index >= len(output.Content) {
		return
	}
	switch block := output.Content[index].(type) {
	case *ai.TextContent:
		stream.Push(&ai.AssistantMessageEventTextEnd{
			Type:         "text_end",
			ContentIndex: index,
			Content:      block.Text,
			Partial:      output,
		})
	case *ai.ThinkingContent:
		bcsFlushRedactedContent(open, output)
		stream.Push(&ai.AssistantMessageEventThinkingEnd{
			Type:         "thinking_end",
			ContentIndex: index,
			Content:      block.Thinking,
			Partial:      output,
		})
	case *ai.ToolCall:
		if open.arguments != nil {
			args, errMsg := open.arguments.Finish()
			block.Arguments = args
			block.ArgumentsError = errMsg
		}
		stream.Push(&ai.AssistantMessageEventToolcallEnd{
			Type:         "toolcall_end",
			ContentIndex: index,
			ToolCall:     block,
			Partial:      output,
		})
	}
}

func bcsFormatBedrockError(err error) string {
	if err == nil {
		return "Unknown error"
	}
	var respErr *awshttp.ResponseError
	var apiErr smithy.APIError
	hasAPI := errors.As(err, &apiErr)
	hasResp := errors.As(err, &respErr)

	var norm ai.NormalizedProviderError
	if hasResp && respErr != nil {
		status := respErr.HTTPStatusCode()
		sdkErr := &ai.ProviderSdkError{
			Message:    err.Error(),
			Cause:      err,
			StatusCode: &status,
		}
		if hasAPI && apiErr != nil {
			sdkErr.ErrName = apiErr.ErrorCode()
			if msg := apiErr.ErrorMessage(); msg != "" {
				sdkErr.Message = msg
			}
		}
		norm = ai.NormalizeProviderError(sdkErr)
	} else {
		norm = ai.NormalizeProviderError(err)
	}

	core := norm.Message
	if !norm.MessageCarriesBody && norm.Status != nil && norm.Body != nil {
		core = fmt.Sprintf("%d: %s", *norm.Status, *norm.Body)
	} else if hasAPI && apiErr != nil && apiErr.ErrorMessage() != "" {
		core = apiErr.ErrorMessage()
	}

	hint := ""
	if bcsDataRetentionRegex.MatchString(core) {
		hint = fmt.Sprintf(" See %s for supported data retention modes.", bcsDataRetentionDocsURL)
	}
	if hasAPI && apiErr != nil {
		code := apiErr.ErrorCode()
		if code != "" {
			prefix := code
			if mapped, ok := bcsBedrockErrorPrefixes[code]; ok {
				prefix = mapped
			}
			return fmt.Sprintf("%s: %s%s", prefix, core, hint)
		}
	}
	return core + hint
}

func bcsIsStrictToolValidationError(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return false
	}
	if apiErr.ErrorCode() != "ValidationException" {
		return false
	}
	return IsStrictToolLimitErrorMessage(apiErr.ErrorMessage()) || IsStrictToolLimitErrorMessage(err.Error())
}

func bcsExtractAPIErrorMessage(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr != nil && apiErr.ErrorMessage() != "" {
		return apiErr.ErrorMessage()
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func bcsNormalizeDiagnosticValue(value string) *string {
	trimmed := js.Trim(value)
	if trimmed == "" || len([]rune(trimmed)) > bcsMaxBedrockDiagnosticValueChar {
		return nil
	}
	return &trimmed
}

func bcsExtractBedrockErrorCode(err error) *string {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return nil
	}
	code := apiErr.ErrorCode()
	if !strings.HasSuffix(code, "Exception") {
		return nil
	}
	return bcsNormalizeDiagnosticValue(code)
}

func bcsExtractStatusFromMetadata(md middleware.Metadata) int {
	if rawResp, ok := awsmiddleware.GetRawResponse(md).(*smithyhttp.Response); ok && rawResp != nil {
		return rawResp.StatusCode
	}
	return 0
}

func bcsAppendBedrockFailureDiagnostic(
	output *ai.AssistantMessage,
	err error,
	fallbackRequestId *string,
) {
	details := jsonx.NewObject()
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) && respErr != nil && respErr.HTTPStatusCode() > 0 {
		details.Set("status", respErr.HTTPStatusCode())
	}
	if code := bcsExtractBedrockErrorCode(err); code != nil {
		details.Set("errorCode", *code)
	}
	var reqId *string
	if errors.As(err, &respErr) && respErr != nil {
		reqId = bcsNormalizeDiagnosticValue(respErr.ServiceRequestID())
	}
	if reqId == nil {
		reqId = fallbackRequestId
	}
	if reqId != nil {
		details.Set("requestId", *reqId)
	}
	if details.Len() == 0 {
		return
	}
	ai.AppendAssistantMessageDiagnostic(output, ai.CreateAssistantMessageDiagnostic("bedrock_response_failure", nil, details))
}

func bcsGetModelMatchCandidates(modelId, modelName string) []string {
	var values []string
	if modelName != "" {
		values = []string{modelId, modelName}
	} else {
		values = []string{modelId}
	}
	out := make([]string, 0, len(values)*2)
	for _, value := range values {
		lower := strings.ToLower(value)
		out = append(out, lower, bcsModelCandidateSepRegex.ReplaceAllString(lower, "-"))
	}
	return out
}

func bcsSupportsAdaptiveThinking(modelId, modelName string) bool {
	return slices.ContainsFunc(bcsGetModelMatchCandidates(modelId, modelName), func(s string) bool {
		return strings.Contains(s, "opus-4-6") ||
			strings.Contains(s, "opus-4-7") ||
			strings.Contains(s, "opus-4-8") ||
			strings.Contains(s, "opus-5") ||
			strings.Contains(s, "sonnet-4-6") ||
			strings.Contains(s, "sonnet-5") ||
			strings.Contains(s, "haiku-5") ||
			strings.Contains(s, "fable-5")
	})
}

func bcsSupportsNativeXhighEffort(model *ai.Model) bool {
	return slices.ContainsFunc(bcsGetModelMatchCandidates(model.Id, model.Name), func(s string) bool {
		return strings.Contains(s, "opus-4-7") ||
			strings.Contains(s, "opus-4-8") ||
			strings.Contains(s, "opus-5") ||
			strings.Contains(s, "sonnet-5") ||
			strings.Contains(s, "haiku-5") ||
			strings.Contains(s, "fable-5")
	})
}

func bcsSupportsThinkingBlockBinding(model *ai.Model) bool {
	return slices.ContainsFunc(bcsGetModelMatchCandidates(model.Id, model.Name), func(s string) bool {
		return strings.Contains(s, "opus-4-7") ||
			strings.Contains(s, "opus-4-8") ||
			strings.Contains(s, "opus-5") ||
			strings.Contains(s, "sonnet-5") ||
			strings.Contains(s, "haiku-5") ||
			strings.Contains(s, "fable-5")
	})
}

func bcsLookupThinkingLevelMap(m *ai.ThinkingLevelMap, level ai.ThinkingLevel) (string, bool) {
	if m == nil {
		return "", false
	}
	var opt jsonx.Opt[string]
	switch level {
	case ai.ThinkingLevelMinimal:
		opt = m.Minimal
	case ai.ThinkingLevelLow:
		opt = m.Low
	case ai.ThinkingLevelMedium:
		opt = m.Medium
	case ai.ThinkingLevelHigh:
		opt = m.High
	case ai.ThinkingLevelXhigh:
		opt = m.Xhigh
	case ai.ThinkingLevelMax:
		opt = m.Max
	}
	if val, ok := opt.Get(); ok {
		return val, true
	}
	return "", false
}

func bcsMapThinkingLevelToEffort(model *ai.Model, level *ai.ThinkingLevel) string {
	if level != nil && *level == ai.ThinkingLevelXhigh && bcsSupportsNativeXhighEffort(model) {
		return "xhigh"
	}
	if level != nil {
		if mapped, ok := bcsLookupThinkingLevelMap(model.ThinkingLevelMap, *level); ok {
			return mapped
		}
		switch *level {
		case ai.ThinkingLevelMinimal, ai.ThinkingLevelLow:
			return "low"
		case ai.ThinkingLevelMedium:
			return "medium"
		case ai.ThinkingLevelHigh:
			return "high"
		}
	}
	return "high"
}

func bcsResolveCacheRetention(retention *ai.CacheRetention, env ai.ProviderEnv) ai.CacheRetention {
	if retention != nil && *retention != "" {
		return *retention
	}
	if ai.GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return ai.CacheRetentionLong
	}
	return ai.CacheRetentionShort
}

func bcsIsAnthropicClaudeModel(model *ai.Model) bool {
	if model == nil {
		return false
	}
	id := strings.ToLower(model.Id)
	name := strings.ToLower(model.Name)
	return strings.Contains(id, "anthropic.claude") ||
		strings.Contains(id, "anthropic/claude") ||
		strings.Contains(name, "anthropic.claude") ||
		strings.Contains(name, "anthropic/claude") ||
		strings.Contains(name, "claude")
}

func bcsSupportsPromptCaching(model *ai.Model, env ai.ProviderEnv) bool {
	candidates := bcsGetModelMatchCandidates(model.Id, model.Name)
	hasClaudeRef := slices.ContainsFunc(candidates, func(s string) bool {
		return strings.Contains(s, "claude")
	})
	if !hasClaudeRef {
		return ai.GetProviderEnvValue("AWS_BEDROCK_FORCE_CACHE", env) == "1"
	}
	if slices.ContainsFunc(candidates, func(s string) bool {
		return strings.Contains(s, "fable-5") ||
			strings.Contains(s, "opus-5") ||
			strings.Contains(s, "sonnet-5") ||
			strings.Contains(s, "haiku-5")
	}) {
		return true
	}
	if slices.ContainsFunc(candidates, func(s string) bool {
		return strings.Contains(s, "-4-")
	}) {
		return true
	}
	if slices.ContainsFunc(candidates, func(s string) bool {
		return strings.Contains(s, "claude-3-7-sonnet")
	}) {
		return true
	}
	if slices.ContainsFunc(candidates, func(s string) bool {
		return strings.Contains(s, "claude-3-5-haiku")
	}) {
		return true
	}
	return false
}

func bcsBuildSystemPrompt(
	systemPrompt string,
	model *ai.Model,
	cacheRetention ai.CacheRetention,
	env ai.ProviderEnv,
) []bedrocktypes.SystemContentBlock {
	if systemPrompt == "" {
		return nil
	}
	blocks := []bedrocktypes.SystemContentBlock{
		&bedrocktypes.SystemContentBlockMemberText{
			Value: ai.SanitizeSurrogates(systemPrompt),
		},
	}
	if cacheRetention != ai.CacheRetentionNone && bcsSupportsPromptCaching(model, env) {
		cp := bedrocktypes.CachePointBlock{
			Type: bedrocktypes.CachePointTypeDefault,
		}
		if cacheRetention == ai.CacheRetentionLong {
			cp.Ttl = bedrocktypes.CacheTTLOneHour
		}
		blocks = append(blocks, &bedrocktypes.SystemContentBlockMemberCachePoint{Value: cp})
	}
	return blocks
}

func bcsNormalizeToolCallId(id string, _ *ai.Model, _ *ai.AssistantMessage) string {
	sanitized := bcsNormalizeToolCallCharRegex.ReplaceAllString(id, "_")
	if len(sanitized) > 64 {
		return sanitized[:64]
	}
	return sanitized
}

func bcsCreateNonBlankTextBlock(text string) (string, bool) {
	sanitized := ai.SanitizeSurrogates(text)
	if js.Trim(sanitized) == "" {
		return "", false
	}
	return sanitized, true
}

func bcsCreateRequiredTextBlock(text string) string {
	if nonBlank, ok := bcsCreateNonBlankTextBlock(text); ok {
		return nonBlank
	}
	return bcsEmptyTextPlaceholder
}

func bcsSanitizeBedrockDocument(value any) any {
	switch v := value.(type) {
	case *typebox.Schema:
		if v == nil || v.Object == nil {
			return nil
		}
		return bcsSanitizeBedrockDocument(v.Object)
	case *jsonx.Object:
		if v == nil {
			return map[string]any{}
		}
		out := make(map[string]any, v.Len())
		for k, nested := range v.All() {
			if k == "" {
				continue
			}
			out[k] = bcsSanitizeBedrockDocument(nested)
		}
		return out
	case jsonx.Object:
		out := make(map[string]any, v.Len())
		for k, nested := range v.All() {
			if k == "" {
				continue
			}
			out[k] = bcsSanitizeBedrockDocument(nested)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, nested := range v {
			if k == "" {
				continue
			}
			out[k] = bcsSanitizeBedrockDocument(nested)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = bcsSanitizeBedrockDocument(item)
		}
		return out
	case []*jsonx.Object:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = bcsSanitizeBedrockDocument(item)
		}
		return out
	default:
		return value
	}
}

func bcsCreateImageBlock(mimeType string, data string) (bedrocktypes.ImageBlock, error) {
	var format bedrocktypes.ImageFormat
	switch mimeType {
	case "image/jpeg", "image/jpg":
		format = bedrocktypes.ImageFormatJpeg
	case "image/png":
		format = bedrocktypes.ImageFormatPng
	case "image/gif":
		format = bedrocktypes.ImageFormatGif
	case "image/webp":
		format = bedrocktypes.ImageFormatWebp
	default:
		return bedrocktypes.ImageBlock{}, fmt.Errorf("Unknown image type: %s", mimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return bedrocktypes.ImageBlock{}, err
	}
	return bedrocktypes.ImageBlock{
		Format: format,
		Source: &bedrocktypes.ImageSourceMemberBytes{Value: decoded},
	}, nil
}

func bcsConvertToolResultContent(content ai.ContentList) ([]bedrocktypes.ToolResultContentBlock, error) {
	var result []bedrocktypes.ToolResultContentBlock
	for _, c := range content {
		switch item := c.(type) {
		case *ai.ImageContent:
			img, err := bcsCreateImageBlock(item.MimeType, item.Data)
			if err != nil {
				return nil, err
			}
			result = append(result, &bedrocktypes.ToolResultContentBlockMemberImage{Value: img})
		case *ai.TextContent:
			if text, ok := bcsCreateNonBlankTextBlock(item.Text); ok {
				result = append(result, &bedrocktypes.ToolResultContentBlockMemberText{Value: text})
			}
		}
	}
	if len(result) == 0 {
		result = append(result, &bedrocktypes.ToolResultContentBlockMemberText{Value: bcsEmptyTextPlaceholder})
	}
	return result, nil
}

func bcsConvertMessages(
	c *ai.TranscriptContext,
	model *ai.Model,
	cacheRetention ai.CacheRetention,
	env ai.ProviderEnv,
) ([]bedrocktypes.Message, error) {
	var result []bedrocktypes.Message
	transformed := TransformMessages(
		ai.WithoutInitialSystemMessage(c.Messages),
		model,
		bcsNormalizeToolCallId,
	)

	for i := 0; i < len(transformed); i++ {
		switch m := transformed[i].(type) {
		case *ai.UserMessage:
			var content []bedrocktypes.ContentBlock
			if m.Content.Text != nil {
				content = append(content, &bedrocktypes.ContentBlockMemberText{
					Value: bcsCreateRequiredTextBlock(*m.Content.Text),
				})
			} else {
				for _, item := range m.Content.Blocks {
					switch cItem := item.(type) {
					case *ai.TextContent:
						if text, ok := bcsCreateNonBlankTextBlock(cItem.Text); ok {
							content = append(content, &bedrocktypes.ContentBlockMemberText{Value: text})
						}
					case *ai.ImageContent:
						img, err := bcsCreateImageBlock(cItem.MimeType, cItem.Data)
						if err != nil {
							return nil, err
						}
						content = append(content, &bedrocktypes.ContentBlockMemberImage{Value: img})
					}
				}
				if len(content) == 0 {
					content = append(content, &bedrocktypes.ContentBlockMemberText{Value: bcsEmptyTextPlaceholder})
				}
			}
			result = append(result, bedrocktypes.Message{
				Role:    bedrocktypes.ConversationRoleUser,
				Content: content,
			})

		case *ai.AssistantMessage:
			if len(m.Content) == 0 {
				continue
			}
			var contentBlocks []bedrocktypes.ContentBlock
			for _, item := range m.Content {
				switch cItem := item.(type) {
				case *ai.TextContent:
					if text, ok := bcsCreateNonBlankTextBlock(cItem.Text); ok {
						contentBlocks = append(contentBlocks, &bedrocktypes.ContentBlockMemberText{Value: text})
					}
				case *ai.ToolCall:
					sanitizedInput := bcsSanitizeBedrockDocument(cItem.Arguments)
					contentBlocks = append(contentBlocks, &bedrocktypes.ContentBlockMemberToolUse{
						Value: bedrocktypes.ToolUseBlock{
							ToolUseId: aws.String(cItem.Id),
							Name:      aws.String(cItem.Name),
							Input:     document.NewLazyDocument(sanitizedInput),
						},
					})
				case *ai.ThinkingContent:
					if cItem.Redacted != nil && *cItem.Redacted {
						if cItem.ThinkingSignature != nil && *cItem.ThinkingSignature != "" {
							if decoded, err := base64.StdEncoding.DecodeString(*cItem.ThinkingSignature); err == nil && len(decoded) > 0 {
								contentBlocks = append(contentBlocks, &bedrocktypes.ContentBlockMemberReasoningContent{
									Value: &bedrocktypes.ReasoningContentBlockMemberRedactedContent{Value: decoded},
								})
							}
						}
						continue
					}
					thinking := ai.SanitizeSurrogates(cItem.Thinking)
					if js.Trim(thinking) == "" {
						continue
					}
					if bcsIsAnthropicClaudeModel(model) {
						if cItem.ThinkingSignature == nil || js.Trim(*cItem.ThinkingSignature) == "" {
							contentBlocks = append(contentBlocks, &bedrocktypes.ContentBlockMemberText{Value: thinking})
						} else {
							contentBlocks = append(contentBlocks, &bedrocktypes.ContentBlockMemberReasoningContent{
								Value: &bedrocktypes.ReasoningContentBlockMemberReasoningText{
									Value: bedrocktypes.ReasoningTextBlock{
										Text:      aws.String(thinking),
										Signature: aws.String(*cItem.ThinkingSignature),
									},
								},
							})
						}
					} else {
						contentBlocks = append(contentBlocks, &bedrocktypes.ContentBlockMemberReasoningContent{
							Value: &bedrocktypes.ReasoningContentBlockMemberReasoningText{
								Value: bedrocktypes.ReasoningTextBlock{
									Text: aws.String(thinking),
								},
							},
						})
					}
				}
			}
			if len(contentBlocks) == 0 {
				continue
			}
			result = append(result, bedrocktypes.Message{
				Role:    bedrocktypes.ConversationRoleAssistant,
				Content: contentBlocks,
			})

		case *ai.ToolResultMessage:
			var toolResults []bedrocktypes.ContentBlock
			var errorImages []bedrocktypes.ContentBlock
			j := i
			for j < len(transformed) {
				toolMsg, ok := transformed[j].(*ai.ToolResultMessage)
				if !ok {
					break
				}
				var split *SplitErrorToolResult
				if bcsIsAnthropicClaudeModel(model) {
					split = SplitErrorToolResultImages(toolMsg)
				}
				var contentToConvert ai.ContentList
				if split != nil {
					contentToConvert = make(ai.ContentList, len(split.Content))
					for idx, tc := range split.Content {
						contentToConvert[idx] = tc
					}
				} else {
					contentToConvert = toolMsg.Content
				}
				convertedContent, err := bcsConvertToolResultContent(contentToConvert)
				if err != nil {
					return nil, err
				}
				status := bedrocktypes.ToolResultStatusSuccess
				if toolMsg.IsError {
					status = bedrocktypes.ToolResultStatusError
				}
				toolResults = append(toolResults, &bedrocktypes.ContentBlockMemberToolResult{
					Value: bedrocktypes.ToolResultBlock{
						ToolUseId: aws.String(toolMsg.ToolCallId),
						Content:   convertedContent,
						Status:    status,
					},
				})
				if split != nil {
					errorImages = append(errorImages, &bedrocktypes.ContentBlockMemberText{Value: split.Caption})
					for _, imgItem := range split.Images {
						img, err := bcsCreateImageBlock(imgItem.MimeType, imgItem.Data)
						if err != nil {
							return nil, err
						}
						errorImages = append(errorImages, &bedrocktypes.ContentBlockMemberImage{Value: img})
					}
				}
				j++
			}
			i = j - 1
			combined := make([]bedrocktypes.ContentBlock, 0, len(toolResults)+len(errorImages))
			combined = append(combined, toolResults...)
			combined = append(combined, errorImages...)
			result = append(result, bedrocktypes.Message{
				Role:    bedrocktypes.ConversationRoleUser,
				Content: combined,
			})
		}
	}

	if cacheRetention != ai.CacheRetentionNone && bcsSupportsPromptCaching(model, env) && len(result) > 0 {
		lastIdx := len(result) - 1
		if result[lastIdx].Role == bedrocktypes.ConversationRoleUser {
			cp := bedrocktypes.CachePointBlock{
				Type: bedrocktypes.CachePointTypeDefault,
			}
			if cacheRetention == ai.CacheRetentionLong {
				cp.Ttl = bedrocktypes.CacheTTLOneHour
			}
			result[lastIdx].Content = append(result[lastIdx].Content, &bedrocktypes.ContentBlockMemberCachePoint{Value: cp})
		}
	}

	return result, nil
}

func bcsConvertToolConfig(
	tools []*ai.Tool,
	toolChoice *BedrockToolChoice,
	supportsStrictMode bool,
	disablePreferredStrictTools bool,
) (*bedrocktypes.ToolConfiguration, bool, error) {
	if len(tools) == 0 || (toolChoice != nil && toolChoice.Type == "none") {
		return nil, false, nil
	}

	hasPreferredStrictTools := false
	bedrockTools := make([]bedrocktypes.Tool, 0, len(tools))
	for _, tool := range tools {
		preferred := PrefersJsonSchemaStrictSampling(tool)
		var strict *bool
		if disablePreferredStrictTools && preferred {
			strict = nil
		} else {
			resolved, err := ResolveJsonSchemaStrictSampling(tool, supportsStrictMode, nil)
			if err != nil {
				return nil, false, err
			}
			strict = resolved
		}
		if strict != nil && *strict && preferred {
			hasPreferredStrictTools = true
		}
		params, err := GetJsonSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, false, err
		}
		sanitizedParams := bcsSanitizeBedrockDocument(params)
		spec := bedrocktypes.ToolSpecification{
			Name:        aws.String(tool.Name),
			Description: aws.String(tool.Description),
			InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{
				Value: document.NewLazyDocument(sanitizedParams),
			},
		}
		if strict != nil && *strict {
			b := true
			spec.Strict = &b
		}
		bedrockTools = append(bedrockTools, &bedrocktypes.ToolMemberToolSpec{Value: spec})
	}

	var bedrockToolChoice bedrocktypes.ToolChoice
	if toolChoice != nil {
		switch toolChoice.Type {
		case "auto":
			bedrockToolChoice = &bedrocktypes.ToolChoiceMemberAuto{Value: bedrocktypes.AutoToolChoice{}}
		case "any":
			bedrockToolChoice = &bedrocktypes.ToolChoiceMemberAny{Value: bedrocktypes.AnyToolChoice{}}
		case "tool":
			bedrockToolChoice = &bedrocktypes.ToolChoiceMemberTool{
				Value: bedrocktypes.SpecificToolChoice{Name: aws.String(toolChoice.Name)},
			}
		}
	}

	return &bedrocktypes.ToolConfiguration{
		Tools:      bedrockTools,
		ToolChoice: bedrockToolChoice,
	}, hasPreferredStrictTools, nil
}

func bcsMapStopReason(reason string) (ai.StopReason, string) {
	switch bedrocktypes.StopReason(reason) {
	case bedrocktypes.StopReasonEndTurn, bedrocktypes.StopReasonStopSequence:
		return ai.StopReasonStop, ""
	case bedrocktypes.StopReasonMaxTokens, bedrocktypes.StopReasonModelContextWindowExceeded:
		return ai.StopReasonLength, ""
	case bedrocktypes.StopReasonToolUse:
		return ai.StopReasonToolUse, ""
	default:
		if reason != "" {
			return ai.StopReasonError, "Provider stopped with: " + reason
		}
		return ai.StopReasonError, ""
	}
}

func bcsGetConfiguredBedrockRegion(options *BedrockOptions) string {
	var env ai.ProviderEnv
	if options != nil {
		if options.Region != nil && *options.Region != "" {
			return *options.Region
		}
		env = options.Env
	}
	if v := ai.GetProviderEnvValue("AWS_REGION", env); v != "" {
		return v
	}
	if v := ai.GetProviderEnvValue("AWS_DEFAULT_REGION", env); v != "" {
		return v
	}
	return ""
}

func bcsGetConfiguredBedrockCredentials(env ai.ProviderEnv) (string, string, string, bool) {
	ak := ai.GetProviderEnvValue("AWS_ACCESS_KEY_ID", env)
	sk := ai.GetProviderEnvValue("AWS_SECRET_ACCESS_KEY", env)
	if ak == "" || sk == "" {
		return "", "", "", false
	}
	st := ai.GetProviderEnvValue("AWS_SESSION_TOKEN", env)
	return ak, sk, st, true
}

func bcsGetStandardBedrockEndpointRegion(baseUrl string) string {
	if baseUrl == "" {
		return ""
	}
	parsed, err := url.Parse(baseUrl)
	if err != nil {
		return ""
	}
	m := bcsStandardEndpointRegex.FindStringSubmatch(strings.ToLower(parsed.Hostname()))
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func bcsShouldUseExplicitBedrockEndpoint(baseUrl, configuredRegion string, hasAmbientConfiguredProfile bool) bool {
	if bcsGetStandardBedrockEndpointRegion(baseUrl) == "" {
		return true
	}
	return configuredRegion == "" && !hasAmbientConfiguredProfile
}

func bcsIsGovCloudBedrockTarget(model *ai.Model, options *BedrockOptions) bool {
	if region := bcsGetConfiguredBedrockRegion(options); strings.HasPrefix(strings.ToLower(region), "us-gov-") {
		return true
	}
	modelId := strings.ToLower(model.Id)
	return strings.HasPrefix(modelId, "us-gov.") || strings.HasPrefix(modelId, "arn:aws-us-gov:")
}

func bcsBuildAdditionalModelRequestFields(model *ai.Model, options *BedrockOptions) document.Interface {
	if options == nil || options.Reasoning == nil || !model.Reasoning {
		return nil
	}

	if bcsIsAnthropicClaudeModel(model) {
		isGovCloud := bcsIsGovCloudBedrockTarget(model, options)
		var display string
		hasDisplay := !isGovCloud
		if hasDisplay {
			if options.ThinkingDisplay != nil && *options.ThinkingDisplay != "" {
				display = string(*options.ThinkingDisplay)
			} else {
				display = string(BedrockThinkingDisplaySummarized)
			}
		}

		useBlockBinding := !isGovCloud && bcsSupportsThinkingBlockBinding(model)
		fields := map[string]any{}
		if bcsSupportsAdaptiveThinking(model.Id, model.Name) {
			thinking := map[string]any{
				"type": "adaptive",
			}
			if hasDisplay {
				thinking["display"] = display
			}
			if useBlockBinding {
				thinking["block_binding"] = map[string]any{
					"prefix_mismatch_behavior": "drop_block",
				}
			}
			fields["thinking"] = thinking
			fields["output_config"] = map[string]any{
				"effort": bcsMapThinkingLevelToEffort(model, options.Reasoning),
			}
			if useBlockBinding {
				fields["anthropic_beta"] = []string{bcsThinkingBindingControlsBeta}
			}
		} else {
			budget := bcsTokenBudgetForBedrockClaude(*options.Reasoning, options.ThinkingBudgets)
			thinking := map[string]any{
				"type":          "enabled",
				"budget_tokens": budget,
			}
			if hasDisplay {
				thinking["display"] = display
			}
			fields["thinking"] = thinking
			if options.InterleavedThinking == nil || *options.InterleavedThinking {
				fields["anthropic_beta"] = []string{"interleaved-thinking-2025-05-14"}
			}
		}
		return document.NewLazyDocument(fields)
	}

	candidates := bcsGetModelMatchCandidates(model.Id, model.Name)
	if slices.ContainsFunc(candidates, func(s string) bool { return strings.Contains(s, "gpt-oss") }) {
		return document.NewLazyDocument(map[string]any{
			"reasoning_effort": bcsOpenAIGptOssEffort(*options.Reasoning),
		})
	}
	if slices.ContainsFunc(candidates, func(s string) bool { return strings.Contains(s, "gpt-") }) {
		effort := bcsOpenAIGptEffort(*options.Reasoning)
		if mapped, ok := bcsLookupThinkingLevelMap(model.ThinkingLevelMap, *options.Reasoning); ok {
			effort = mapped
		}
		return document.NewLazyDocument(map[string]any{
			"reasoning": map[string]any{
				"effort": effort,
			},
		})
	}

	return nil
}

func bcsTokenBudgetForBedrockClaude(level ai.ThinkingLevel, budgets *ai.ThinkingBudgets) int {
	var clampedKey ai.ThinkingLevel
	switch level {
	case ai.ThinkingLevelXhigh, ai.ThinkingLevelMax:
		clampedKey = ai.ThinkingLevelHigh
	default:
		clampedKey = level
	}
	if budgets != nil {
		switch clampedKey {
		case ai.ThinkingLevelMinimal:
			if budgets.Minimal != nil {
				return *budgets.Minimal
			}
		case ai.ThinkingLevelLow:
			if budgets.Low != nil {
				return *budgets.Low
			}
		case ai.ThinkingLevelMedium:
			if budgets.Medium != nil {
				return *budgets.Medium
			}
		case ai.ThinkingLevelHigh:
			if budgets.High != nil {
				return *budgets.High
			}
		}
	}
	switch level {
	case ai.ThinkingLevelMinimal:
		return 1024
	case ai.ThinkingLevelLow:
		return 2048
	case ai.ThinkingLevelMedium:
		return 8192
	default:
		return 16384
	}
}

func bcsOpenAIGptEffort(level ai.ThinkingLevel) string {
	switch level {
	case ai.ThinkingLevelMinimal, ai.ThinkingLevelLow:
		return "low"
	case ai.ThinkingLevelMedium:
		return "medium"
	case ai.ThinkingLevelHigh:
		return "high"
	case ai.ThinkingLevelXhigh:
		return "xhigh"
	case ai.ThinkingLevelMax:
		return "max"
	default:
		return "high"
	}
}

func bcsOpenAIGptOssEffort(level ai.ThinkingLevel) string {
	switch level {
	case ai.ThinkingLevelMinimal, ai.ThinkingLevelLow:
		return "low"
	case ai.ThinkingLevelMedium:
		return "medium"
	default:
		return "high"
	}
}

func bcsSupportsStrictMode(model *ai.Model) bool {
	if model == nil || model.Compat.V == nil {
		return false
	}
	switch c := model.Compat.V.(type) {
	case *ai.BedrockCompat:
		return c != nil && c.SupportsStrictMode != nil && *c.SupportsStrictMode
	case *ai.ProviderCompat:
		return c != nil && c.SupportsStrictMode != nil && *c.SupportsStrictMode
	}
	return false
}

func bcsGetCacheRetention(options *BedrockOptions) *ai.CacheRetention {
	if options == nil {
		return nil
	}
	return options.CacheRetention
}

func bcsResolveInferenceMaxTokens(model *ai.Model, options *BedrockOptions) *int {
	if options != nil && options.MaxTokens != nil {
		return options.MaxTokens
	}
	if bcsIsAnthropicClaudeModel(model) {
		v := model.MaxTokens
		return &v
	}
	return nil
}
