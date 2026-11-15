// Ported from packages/ai/src/api/google-vertex.ts (pi v1.5.2).

package api

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"golang.org/x/oauth2/google"
)

// GoogleVertexOptions configures Google Vertex AI (Gemini) streaming requests.
type GoogleVertexOptions struct {
	ai.StreamOptions
	ToolChoice *string               `json:"toolChoice,omitzero"`
	Thinking   *GoogleThinkingOption `json:"thinking,omitzero"`
	Project    *string               `json:"project,omitzero"`
	Location   *string               `json:"location,omitzero"`
}

const (
	gvVertexCloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
	gvGeneratedBaseUrlPattern  = `https://{location}-aiplatform.googleapis.com`
)

var (
	gvApiVersionInPathRe    = regexp.MustCompile(`/v\d+(?:[a-zA-Z]+\d*)?(?:/|$)`)
	gvPlaceholderBaseUrlRe  = regexp.MustCompile(`^https://\{(?:location|region)\}-aiplatform\.googleapis\.com(?:/v\d+(?:[a-zA-Z]+\d*)?)?/?$`)
	gvResolveADCBearerToken = gvDefaultResolveADCBearerToken
)

// StreamGoogleVertex streams a response from Google Vertex AI using either an API key
// (x-goog-api-key) or Application Default Credentials via golang.org/x/oauth2/google
// (Authorization: Bearer), sent over raw net/http + internal/sse.
func StreamGoogleVertex(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options ai.ProviderStreamOptions,
) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()

	go func() {
		if ctx == nil {
			ctx = context.Background()
		}
		if model == nil {
			msg := ggaInitOutput(nil)
			errMsg := "Google Vertex stream called with nil model"
			msg.StopReason = ai.StopReasonError
			msg.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonError, Error: msg})
			stream.End(msg)
			return
		}

		opts := gvExtractOptions(options)
		normCtx := c
		if normCtx == nil {
			normCtx = &ai.TranscriptContext{}
		}
		output := ggaInitOutput(model)

		defer func() {
			if r := recover(); r != nil {
				errMsg := ai.FormatThrownValue(r)
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
			}
		}()

		apiKey := gvResolveApiKey(&opts)
		var endpoint string
		var authHeaderName, authHeaderValue string

		if apiKey != "" {
			endpoint = gvBuildEndpointUrl(model, true, "", "")
			authHeaderName = "x-goog-api-key"
			authHeaderValue = apiKey
		} else {
			project := gvResolveProject(&opts)
			location := gvResolveLocation(&opts)
			if project == "" || location == "" {
				errMsg := "Vertex AI requires project and location. Set GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT and GOOGLE_CLOUD_LOCATION, pass them in options, or provide an API key."
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
				return
			}
			endpoint = gvBuildEndpointUrl(model, false, project, location)

			if !gvHasExplicitHeader(opts.Headers, "authorization") && !gvHasExplicitHeader(clshToProviderHeaders(model.Headers), "authorization") {
				token, err := gvResolveADCBearerToken(ctx, opts.Env)
				if err != nil {
					errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
					output.StopReason = ggaAbortOrError(ctx)
					if ctx.Err() != nil {
						output.StopReason = ai.StopReasonAborted
						errMsg = "Request was aborted"
					}
					output.ErrorMessage = &errMsg
					stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
					stream.End(output)
					return
				}
				authHeaderName = "Authorization"
				authHeaderValue = "Bearer " + token
			}
		}

		gOpts := GoogleOptions{
			StreamOptions: opts.StreamOptions,
			ToolChoice:    opts.ToolChoice,
			Thinking:      opts.Thinking,
		}
		params, err := ggaBuildParams(model, normCtx, &gOpts)
		if err != nil {
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			output.StopReason = ggaAbortOrError(ctx)
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		if opts.OnPayload != nil {
			updated, err := opts.OnPayload(params, model)
			if err != nil {
				errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
				return
			}
			if updated != nil {
				if obj, ok := updated.(*jsonx.Object); ok && obj != nil {
					params = obj
				}
			}
		}

		restBody := ggaParamsToRESTBody(params)
		bodyBytes, err := jsonx.Marshal(restBody)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to marshal request body: %v", err)
			output.StopReason = ggaAbortOrError(ctx)
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		headers := ggaBuildHeaders(model, &gOpts, authHeaderName, authHeaderValue)

		httpClient := clshResolveHTTPClient(opts.HTTPClient, opts.Env)
		var hdrTimeout int64
		if opts.TimeoutMs != nil {
			hdrTimeout = *opts.TimeoutMs
		}
		maxRetries := 0
		if opts.MaxRetries != nil {
			maxRetries = *opts.MaxRetries
		}

		resp, cancelHdrWait, err := ggaDoStreamRequestWithRetry(ctx, httpClient, endpoint, headers, bodyBytes, hdrTimeout, &GsGoogleRetryConfig{
			MaxRetries:      maxRetries,
			MaxRetryDelayMs: opts.MaxRetryDelayMs,
		})
		if cancelHdrWait != nil {
			defer cancelHdrWait()
		}
		if err != nil {
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			output.StopReason = ggaAbortOrError(ctx)
			if ctx.Err() != nil {
				output.StopReason = ai.StopReasonAborted
				errMsg = "Request was aborted"
			}
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}
		defer resp.Body.Close()

		if opts.OnResponse != nil {
			if cbErr := opts.OnResponse(&ai.ProviderResponse{
				Status:  resp.StatusCode,
				Headers: ai.HeadersToRecord(resp.Header),
			}, model); cbErr != nil {
				errMsg := ai.FormatProviderError(ai.NormalizeProviderError(cbErr))
				output.StopReason = ggaAbortOrError(ctx)
				output.ErrorMessage = &errMsg
				stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
				stream.End(output)
				return
			}
		}

		if err := ggaConsumeSSEStream(ctx, resp.Body, model, stream, output, opts.OnProviderStreamEvent); err != nil {
			errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
			output.StopReason = ggaAbortOrError(ctx)
			if ctx.Err() != nil {
				output.StopReason = ai.StopReasonAborted
				errMsg = "Request was aborted"
			}
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		if ctx.Err() != nil {
			errMsg := "Request was aborted"
			output.StopReason = ai.StopReasonAborted
			output.ErrorMessage = &errMsg
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: output})
			stream.End(output)
			return
		}

		if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
			stream.Push(&ai.AssistantMessageEventError{Type: "error", Reason: output.StopReason, Error: output})
			stream.End(output)
			return
		}

		stream.Push(&ai.AssistantMessageEventDone{
			Type:    "done",
			Reason:  output.StopReason,
			Message: output,
		})
		stream.End(output)
	}()

	return stream
}

// StreamSimpleGoogleVertex maps uniform SimpleStreamOptions onto GoogleVertexOptions
// and invokes StreamGoogleVertex.
func StreamSimpleGoogleVertex(
	ctx context.Context,
	model *ai.Model,
	c *ai.TranscriptContext,
	options *ai.SimpleStreamOptions,
) *ai.AssistantMessageEventStream {
	var env ai.ProviderEnv
	var explicitApiKey *string
	if options != nil {
		env = options.Env
		explicitApiKey = options.ApiKey
	}
	var resolvedKey *string
	if explicitApiKey != nil && *explicitApiKey != "" {
		resolvedKey = explicitApiKey
	} else if envKey := ai.GetEnvApiKey(string(ai.KnownProviderGoogleVertex), env); envKey != nil {
		resolvedKey = envKey
	}

	base := BuildBaseOptions(model, c, options, resolvedKey)
	vOpts := &GoogleVertexOptions{
		StreamOptions: base,
	}
	if options != nil && options.ToolChoice != nil {
		tc := string(*options.ToolChoice)
		vOpts.ToolChoice = &tc
	}

	var customBudgets *ai.ThinkingBudgets
	if options != nil {
		customBudgets = options.ThinkingBudgets
	}

	if options == nil || options.Reasoning == nil {
		if model != nil && model.Reasoning {
			vOpts.Thinking = &GoogleThinkingOption{Enabled: false}
		}
		return StreamGoogleVertex(ctx, model, c, vOpts)
	}

	resolved := ResolveGoogleThinkingLevel(model, *options.Reasoning)
	if strings.EqualFold(resolved, "off") {
		if model != nil && model.Reasoning {
			vOpts.Thinking = &GoogleThinkingOption{Enabled: false}
		}
		return StreamGoogleVertex(ctx, model, c, vOpts)
	}

	if UsesGoogleThinkingLevel(model) {
		lvl := ToGoogleThinkingLevel(resolved)
		vOpts.Thinking = &GoogleThinkingOption{
			Enabled: true,
			Level:   &lvl,
		}
		return StreamGoogleVertex(ctx, model, c, vOpts)
	}

	budget := gvGetVertexBudget(model, resolved, customBudgets)
	vOpts.Thinking = &GoogleThinkingOption{
		Enabled:      true,
		BudgetTokens: &budget,
	}
	return StreamGoogleVertex(ctx, model, c, vOpts)
}

func gvGetVertexBudget(model *ai.Model, level string, customBudgets *ai.ThinkingBudgets) int {
	return ggaGetGoogleBudget(model, level, customBudgets)
}

func gvExtractOptions(options ai.ProviderStreamOptions) GoogleVertexOptions {
	if options == nil {
		return GoogleVertexOptions{}
	}
	switch o := options.(type) {
	case *GoogleVertexOptions:
		if o != nil {
			return *o
		}
	case *GoogleOptions:
		if o != nil {
			return GoogleVertexOptions{
				StreamOptions: o.StreamOptions,
				ToolChoice:    o.ToolChoice,
				Thinking:      o.Thinking,
			}
		}
	case *ai.SimpleStreamOptions:
		if o != nil {
			out := GoogleVertexOptions{StreamOptions: o.StreamOptions}
			if o.ToolChoice != nil {
				s := string(*o.ToolChoice)
				out.ToolChoice = &s
			}
			return out
		}
	case *ai.StreamOptions:
		if o != nil {
			return GoogleVertexOptions{StreamOptions: *o}
		}
	default:
		if base := options.BaseStreamOptions(); base != nil {
			return GoogleVertexOptions{StreamOptions: *base}
		}
	}
	return GoogleVertexOptions{}
}

var gvPlaceholderApiKeyRe = regexp.MustCompile(`^<[^>]+>$`)

func gvIsPlaceholderApiKey(k string) bool {
	trimmed := strings.TrimSpace(k)
	if trimmed == "" || trimmed == "gcp-vertex-credentials" {
		return true
	}
	return gvPlaceholderApiKeyRe.MatchString(trimmed)
}

func gvResolveApiKey(opts *GoogleVertexOptions) string {
	if opts != nil && opts.ApiKey != nil && !gvIsPlaceholderApiKey(*opts.ApiKey) {
		return strings.TrimSpace(*opts.ApiKey)
	}
	var env ai.ProviderEnv
	if opts != nil {
		env = opts.Env
	}
	if envKey := ai.GetProviderEnvValue("GOOGLE_CLOUD_API_KEY", env); !gvIsPlaceholderApiKey(envKey) {
		return strings.TrimSpace(envKey)
	}
	if keyPtr := ai.GetEnvApiKey(string(ai.KnownProviderGoogleVertex), env); keyPtr != nil && !gvIsPlaceholderApiKey(*keyPtr) {
		return strings.TrimSpace(*keyPtr)
	}
	return ""
}

func gvResolveProject(opts *GoogleVertexOptions) string {
	if opts != nil && opts.Project != nil && strings.TrimSpace(*opts.Project) != "" {
		return strings.TrimSpace(*opts.Project)
	}
	var env ai.ProviderEnv
	if opts != nil {
		env = opts.Env
	}
	if p := ai.GetProviderEnvValue("GOOGLE_CLOUD_PROJECT", env); strings.TrimSpace(p) != "" {
		return strings.TrimSpace(p)
	}
	if p := ai.GetProviderEnvValue("GCLOUD_PROJECT", env); strings.TrimSpace(p) != "" {
		return strings.TrimSpace(p)
	}
	return ""
}

func gvResolveLocation(opts *GoogleVertexOptions) string {
	if opts != nil && opts.Location != nil && strings.TrimSpace(*opts.Location) != "" {
		return strings.TrimSpace(*opts.Location)
	}
	var env ai.ProviderEnv
	if opts != nil {
		env = opts.Env
	}
	return strings.TrimSpace(ai.GetProviderEnvValue("GOOGLE_CLOUD_LOCATION", env))
}

func gvIsCustomBaseUrl(baseUrl string) bool {
	trimmed := strings.TrimSpace(baseUrl)
	if trimmed == "" || trimmed == gvGeneratedBaseUrlPattern {
		return false
	}
	return !gvPlaceholderBaseUrlRe.MatchString(trimmed)
}

func gvBuildEndpointUrl(model *ai.Model, useApiKey bool, project, location string) string {
	modelPath := gvFormatVertexModelPath(model.Id)
	customBase := ""
	if model != nil && gvIsCustomBaseUrl(model.BaseUrl) {
		customBase = strings.TrimRight(strings.TrimSpace(model.BaseUrl), "/")
	}

	if customBase != "" {
		basePrefix := customBase
		if !gvApiVersionInPathRe.MatchString(customBase) {
			basePrefix += "/v1"
		}
		if !useApiKey && project != "" && location != "" && !strings.Contains(customBase, "/projects/") {
			return fmt.Sprintf("%s/projects/%s/locations/%s/%s:streamGenerateContent?alt=sse",
				basePrefix, project, location, modelPath)
		}
		return basePrefix + "/" + modelPath + ":streamGenerateContent?alt=sse"
	}

	if useApiKey {
		return "https://aiplatform.googleapis.com/v1/" + modelPath + ":streamGenerateContent?alt=sse"
	}

	var origin string
	switch location {
	case "global":
		origin = "https://aiplatform.googleapis.com"
	case "us", "eu":
		origin = fmt.Sprintf("https://aiplatform.%s.rep.googleapis.com", location)
	default:
		origin = fmt.Sprintf("https://%s-aiplatform.googleapis.com", location)
	}
	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/%s:streamGenerateContent?alt=sse",
		origin, project, location, modelPath)
}

func gvFormatVertexModelPath(modelId string) string {
	trimmed := strings.TrimLeft(strings.TrimSpace(modelId), "/")
	if strings.HasPrefix(trimmed, "publishers/") || strings.HasPrefix(trimmed, "projects/") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "models/") {
		return "publishers/google/" + trimmed
	}
	if idx := strings.IndexByte(trimmed, '/'); idx > 0 {
		publisher := trimmed[:idx]
		modelName := trimmed[idx+1:]
		if !strings.HasPrefix(modelName, "models/") {
			modelName = "models/" + modelName
		}
		return "publishers/" + publisher + "/" + modelName
	}
	return "publishers/google/models/" + trimmed
}

func gvHasExplicitHeader(headers ai.ProviderHeaders, name string) bool {
	if headers == nil {
		return false
	}
	target := strings.ToLower(name)
	for k, v := range headers.All() {
		if strings.ToLower(k) == target && v != nil && *v != "" {
			return true
		}
	}
	return false
}

func gvDefaultResolveADCBearerToken(ctx context.Context, env ai.ProviderEnv) (string, error) {
	if credsPath := strings.TrimSpace(ai.GetProviderEnvValue("GOOGLE_APPLICATION_CREDENTIALS", env)); credsPath != "" {
		raw, err := os.ReadFile(credsPath)
		if err != nil {
			return "", fmt.Errorf("failed to read GOOGLE_APPLICATION_CREDENTIALS (%s): %w", credsPath, err)
		}
		credsType, err := gvInferGoogleCredentialType(raw)
		if err != nil {
			return "", fmt.Errorf("invalid GOOGLE_APPLICATION_CREDENTIALS JSON (%s): %w", credsPath, err)
		}
		creds, err := google.CredentialsFromJSONWithType(ctx, raw, credsType, gvVertexCloudPlatformScope)
		if err != nil {
			return "", fmt.Errorf("failed to load Google credentials from %s: %w", credsPath, err)
		}
		tok, err := creds.TokenSource.Token()
		if err != nil {
			return "", fmt.Errorf("failed to obtain Google ADC access token: %w", err)
		}
		return tok.AccessToken, nil
	}

	creds, err := google.FindDefaultCredentials(ctx, gvVertexCloudPlatformScope)
	if err != nil {
		return "", fmt.Errorf("failed to find Google Application Default Credentials: %w", err)
	}
	tok, err := creds.TokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("failed to obtain Google ADC access token: %w", err)
	}
	return tok.AccessToken, nil
}

func gvInferGoogleCredentialType(raw []byte) (google.CredentialsType, error) {
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return "", err
	}
	var metaType string
	if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
		if t, ok := obj.GetString("type"); ok {
			metaType = t
		}
	}
	switch metaType {
	case "service_account", "":
		return google.ServiceAccount, nil
	case "authorized_user":
		return google.AuthorizedUser, nil
	case "external_account":
		return google.ExternalAccount, nil
	case "external_account_authorized_user":
		return google.ExternalAccountAuthorizedUser, nil
	case "impersonated_service_account":
		return google.ImpersonatedServiceAccount, nil
	case "gdch_service_account":
		return google.GDCHServiceAccount, nil
	default:
		return google.CredentialsType(metaType), nil
	}
}
