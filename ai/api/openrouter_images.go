// Ported from packages/ai/src/api/openrouter-images.ts (pi v1.5.2).

package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

var oiDataUrlPattern = regexp.MustCompile(`^data:([^;]+);base64,(.+)$`)

type oiMessageContentPart struct {
	Type     string            `json:"type"`
	Text     *string           `json:"text,omitzero"`
	ImageUrl *oiImageUrlStruct `json:"image_url,omitzero"`
}

type oiImageUrlStruct struct {
	Url string `json:"url"`
}

type oiRequestMessage struct {
	Role    string                 `json:"role"`
	Content []oiMessageContentPart `json:"content"`
}

type oiRequestPayload struct {
	Model      string             `json:"model"`
	Messages   []oiRequestMessage `json:"messages"`
	Stream     bool               `json:"stream"`
	Modalities []string           `json:"modalities"`
}

func oiBuildParams(model *ai.ImageModel, c *ai.ImagesContext) oiRequestPayload {
	var parts []oiMessageContentPart
	if c != nil {
		parts = make([]oiMessageContentPart, 0, len(c.Input))
		for _, item := range c.Input {
			switch blk := item.(type) {
			case *ai.TextContent:
				if blk != nil {
					text := ai.SanitizeSurrogates(blk.Text)
					parts = append(parts, oiMessageContentPart{
						Type: "text",
						Text: &text,
					})
				}
			case *ai.ImageContent:
				if blk != nil {
					parts = append(parts, oiMessageContentPart{
						Type:     "image_url",
						ImageUrl: &oiImageUrlStruct{Url: fmt.Sprintf("data:%s;base64,%s", blk.MimeType, blk.Data)},
					})
				}
			}
		}
	}
	if parts == nil {
		parts = []oiMessageContentPart{}
	}

	hasText := false
	for _, outMod := range model.Output {
		if outMod == ai.ModelModalityText {
			hasText = true
			break
		}
	}
	modalities := []string{"image"}
	if hasText {
		modalities = []string{"image", "text"}
	}

	return oiRequestPayload{
		Model: model.Id,
		Messages: []oiRequestMessage{
			{
				Role:    "user",
				Content: parts,
			},
		},
		Stream:     false,
		Modalities: modalities,
	}
}

func oiParseUsage(rawUsage *jsonx.Object, model *ai.ImageModel) *ai.Usage {
	if rawUsage == nil {
		return nil
	}
	var promptTokens, completionTokens, cachedTokens, cacheWriteTokens int
	if v, ok := rawUsage.Get("prompt_tokens"); ok {
		if f, ok := clshToFloat64(v); ok {
			promptTokens = int(f)
		}
	}
	if v, ok := rawUsage.Get("completion_tokens"); ok {
		if f, ok := clshToFloat64(v); ok {
			completionTokens = int(f)
		}
	}
	if rawDetails, ok := rawUsage.Get("prompt_tokens_details"); ok {
		if details, ok := rawDetails.(*jsonx.Object); ok && details != nil {
			if v, ok := details.Get("cached_tokens"); ok {
				if f, ok := clshToFloat64(v); ok {
					cachedTokens = int(f)
				}
			}
			if v, ok := details.Get("cache_write_tokens"); ok {
				if f, ok := clshToFloat64(v); ok {
					cacheWriteTokens = int(f)
				}
			}
		}
	}

	cacheReadTokens := cachedTokens
	if cacheWriteTokens > 0 {
		cacheReadTokens = max(0, cachedTokens-cacheWriteTokens)
	}
	input := max(0, promptTokens-cacheReadTokens-cacheWriteTokens)
	output := completionTokens
	totalFromProvider := false
	usage := &ai.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheReadTokens,
		CacheWrite:  cacheWriteTokens,
		TotalTokens: input + output + cacheReadTokens + cacheWriteTokens,
		Cost: ai.UsageCost{
			Input:             (model.Cost.Input / 1000000) * float64(input),
			Output:            (model.Cost.Output / 1000000) * float64(output),
			CacheRead:         (model.Cost.CacheRead / 1000000) * float64(cacheReadTokens),
			CacheWrite:        (model.Cost.CacheWrite / 1000000) * float64(cacheWriteTokens),
			TotalFromProvider: &totalFromProvider,
		},
	}
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return usage
}

func oiExtractHttpError(resp *http.Response, bodyBytes []byte) error {
	status := resp.StatusCode
	bodyText := string(bodyBytes)
	var parsedErr any
	if parsed, err := jsonx.Parse(bodyBytes); err == nil && parsed != nil {
		if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
			if inner, ok := obj.Get("error"); ok && inner != nil {
				parsedErr = inner
			} else {
				parsedErr = obj
			}
		}
	}
	msg := fmt.Sprintf("%d status code (no body)", status)
	if obj, ok := parsedErr.(*jsonx.Object); ok && obj != nil {
		if m, ok := obj.GetString("message"); ok && m != "" {
			msg = fmt.Sprintf("%d %s", status, m)
		}
	}
	return &oiHttpError{
		ProviderSdkError: ai.ProviderSdkError{
			Message:     msg,
			StatusCode:  &status,
			Body:        bodyText,
			ParsedError: parsedErr,
		},
		headers: resp.Header.Clone(),
	}
}

type oiHttpError struct {
	ai.ProviderSdkError
	headers http.Header
}

func (e *oiHttpError) Unwrap() error { return &e.ProviderSdkError }

func (e *oiHttpError) ProviderHeaders() http.Header { return e.headers }

// GenerateImages generates images over OpenRouter's chat completions endpoint.
func GenerateImages(
	ctx context.Context,
	model *ai.ImageModel,
	c *ai.ImagesContext,
	options *ai.ImagesOptions,
) (*ai.AssistantImages, error) {
	var apiName ai.ImageApi
	var provider ai.ProviderId
	var modelId string
	if model != nil {
		apiName = ai.ImageApi(model.Api)
		provider = model.Provider
		modelId = model.Id
	}

	output := &ai.AssistantImages{
		Api:        apiName,
		Provider:   provider,
		Model:      modelId,
		Output:     ai.ContentList{},
		StopReason: ai.ImagesStopReasonStop,
		Timestamp:  js.DateNow(),
	}

	fail := func(err error) (*ai.AssistantImages, error) {
		reason := ai.ImagesStopReasonError
		if ctx != nil && ctx.Err() != nil {
			reason = ai.ImagesStopReasonAborted
		}
		output.StopReason = reason
		errMsg := ai.FormatProviderError(ai.NormalizeProviderError(err))
		output.ErrorMessage = &errMsg
		return output, nil
	}

	if model == nil {
		return fail(fmt.Errorf("No API key for provider: "))
	}
	if options == nil || options.ApiKey == nil || *options.ApiKey == "" {
		return fail(fmt.Errorf("No API key for provider: %s", model.Provider))
	}

	var payload any = oiBuildParams(model, c)
	if options.OnPayload != nil {
		nextPayload, err := options.OnPayload(payload, model)
		if err != nil {
			return fail(err)
		}
		if nextPayload != nil {
			payload = nextPayload
		}
	}

	endpoint := clshTrimTrailingSlashes(model.BaseUrl) + "/chat/completions"
	idHeaders := ai.GetIdentityHeaders(ai.ClientIdentityTarget{
		Provider: string(model.Provider),
		BaseUrl:  model.BaseUrl,
	}, options.Identity)
	mergedHeaders := ai.ProviderHeadersToOrderedRecord(
		clshToProviderHeaders(idHeaders),
		clshStaticProviderHeaders(
			"accept", "application/json",
			"content-type", "application/json",
			"authorization", "Bearer "+*options.ApiKey,
		),
		clshToProviderHeaders(model.Headers),
		options.Headers,
	)

	httpClient := clshResolveHTTPClient(options.HTTPClient, options.Env)

	type oiAttemptResult struct {
		response *http.Response
		body     *jsonx.Object
	}

	res, err := ai.RetryProviderRequest(ctx, func(retryCtx context.Context) (oiAttemptResult, error) {
		bodyBytes, err := jsonx.Marshal(payload)
		if err != nil {
			return oiAttemptResult{}, err
		}
		attemptCtx, cancel, timeoutCtx := clshCombineAttemptContext(retryCtx, options.TimeoutMs)
		defer cancel()

		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return oiAttemptResult{}, err
		}
		if mergedHeaders != nil {
			for k, v := range mergedHeaders.All() {
				req.Header.Set(k, v)
			}
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return oiAttemptResult{}, clshCheckTimeout(err, retryCtx, timeoutCtx, options.TimeoutMs)
		}
		defer resp.Body.Close()
		respBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return oiAttemptResult{}, clshCheckTimeout(readErr, retryCtx, timeoutCtx, options.TimeoutMs)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return oiAttemptResult{}, oiExtractHttpError(resp, respBytes)
		}
		parsed, parseErr := jsonx.Parse(respBytes)
		if parseErr != nil {
			return oiAttemptResult{}, parseErr
		}
		obj, _ := parsed.(*jsonx.Object)
		return oiAttemptResult{response: resp, body: obj}, nil
	}, &ai.ProviderRetryOptions{
		MaxRetries:      options.MaxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
	})
	if err != nil {
		return fail(err)
	}

	if options.OnResponse != nil {
		if err := options.OnResponse(&ai.ProviderResponse{
			Status:  res.response.StatusCode,
			Headers: ai.HeadersToRecord(res.response.Header),
		}, model); err != nil {
			return fail(err)
		}
	}

	if res.body != nil {
		if id, ok := res.body.GetString("id"); ok && id != "" {
			output.ResponseId = &id
		}
		if rawUsage, ok := res.body.Get("usage"); ok {
			if usageObj, ok := rawUsage.(*jsonx.Object); ok && usageObj != nil {
				output.Usage = oiParseUsage(usageObj, model)
			}
		}
		if rawChoices, ok := res.body.Get("choices"); ok {
			if choices, ok := rawChoices.([]any); ok && len(choices) > 0 {
				if firstChoice, ok := choices[0].(*jsonx.Object); ok && firstChoice != nil {
					if rawMsg, ok := firstChoice.Get("message"); ok {
						if msgObj, ok := rawMsg.(*jsonx.Object); ok && msgObj != nil {
							if content, ok := msgObj.GetString("content"); ok && len(content) > 0 {
								output.Output = append(output.Output, &ai.TextContent{
									Type: "text",
									Text: content,
								})
							}
							if rawImgs, ok := msgObj.Get("images"); ok {
								if imgs, ok := rawImgs.([]any); ok {
									for _, item := range imgs {
										imgObj, ok := item.(*jsonx.Object)
										if !ok || imgObj == nil {
											continue
										}
										rawUrl, _ := imgObj.Get("image_url")
										var imageUrl string
										switch u := rawUrl.(type) {
										case string:
											imageUrl = u
										case *jsonx.Object:
											if u != nil {
												imageUrl, _ = u.GetString("url")
											}
										}
										if imageUrl == "" || !oiDataUrlPattern.MatchString(imageUrl) {
											continue
										}
										matches := oiDataUrlPattern.FindStringSubmatch(imageUrl)
										if len(matches) == 3 {
											output.Output = append(output.Output, &ai.ImageContent{
												Type:     "image",
												MimeType: matches[1],
												Data:     matches[2],
											})
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return output, nil
}
