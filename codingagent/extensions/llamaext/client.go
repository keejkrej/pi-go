// Ported from packages/coding-agent/src/extensions/llama/client.ts (pi v1.5.2).

package llamaext

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefaultLlamaTimeout is the default request timeout for llama.cpp management API calls.
const DefaultLlamaTimeout = 15 * time.Second

// LlamaModelStatus describes the runtime status and launch arguments of a llama.cpp router model.
type LlamaModelStatus struct {
	Value  string   `json:"value"`
	Args   []string `json:"args,omitzero"`
	Failed *bool    `json:"failed,omitzero"`
}

// LlamaModelArchitecture reports supported input and output modalities for a llama.cpp model.
type LlamaModelArchitecture struct {
	InputModalities  []string `json:"input_modalities,omitzero"`
	OutputModalities []string `json:"output_modalities,omitzero"`
}

// LlamaModelMeta holds model context window metadata reported by llama-server.
type LlamaModelMeta struct {
	NCtx      *int `json:"n_ctx,omitzero"`
	NCtxTrain *int `json:"n_ctx_train,omitzero"`
}

// LlamaModelInfo represents one entry returned by GET /models on a llama.cpp router server.
type LlamaModelInfo struct {
	Id           string                  `json:"id"`
	Status       LlamaModelStatus        `json:"status"`
	Source       *string                 `json:"source,omitzero"`
	Architecture *LlamaModelArchitecture `json:"architecture,omitzero"`
	Meta         *LlamaModelMeta         `json:"meta,omitzero"`
}

// LlamaModelsResponse is the response envelope from GET /models.
type LlamaModelsResponse struct {
	Data []LlamaModelInfo `json:"data"`
}

// LlamaServerProps holds server or per-model properties returned by GET /props.
type LlamaServerProps struct {
	Role           string `json:"role,omitzero"`
	ModelsAutoload *bool  `json:"models_autoload,omitzero"`
	ChatTemplate   string `json:"chat_template,omitzero"`
}

// LlamaProgress represents normalized load or download progress for UI and callers.
type LlamaProgress struct {
	Message string   `json:"message"`
	Ratio   *float64 `json:"ratio,omitzero"`
	Detail  *string  `json:"detail,omitzero"`
}

// LlamaLoadProgress reports stage progress nested in SSE status_change payloads.
type LlamaLoadProgress struct {
	Stages  []string `json:"stages,omitzero"`
	Current string   `json:"current,omitzero"`
	Value   *float64 `json:"value,omitzero"`
}

// LlamaDownloadFileProgress tracks byte counts for one downloading artifact.
type LlamaDownloadFileProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// LlamaModelEvent represents one parsed SSE event from /models/sse.
type LlamaModelEvent struct {
	Model string        `json:"model"`
	Event string        `json:"event"`
	Data  *jsonx.Object `json:"data,omitzero"`
}

// LlamaClientOptions configures NewLlamaClient.
type LlamaClientOptions struct {
	ApiKey     *string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// LlamaClient communicates with a llama-server router management API.
type LlamaClient struct {
	baseUrl    string
	apiKey     string
	timeout    time.Duration
	httpClient *http.Client
}

var clientByteUnits = []string{"B", "KiB", "MiB", "GiB", "TiB"}

// FormatBytes formats a byte count using IEC units (B, KiB, MiB, GiB, TiB).
func FormatBytes(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	val := float64(bytes)
	unitIdx := 0
	for val >= 1024 && unitIdx < len(clientByteUnits)-1 {
		val /= 1024
		unitIdx++
	}
	return fmt.Sprintf("%.2f %s", val, clientByteUnits[unitIdx])
}

// NormalizeLlamaServerUrl validates an http(s) URL and returns the llama-server management base URL without /v1 or trailing slash.
func NormalizeLlamaServerUrl(rawUrl string) (string, error) {
	trimmed := strings.TrimSpace(rawUrl)
	if trimmed == "" {
		return "", errors.New("llama.cpp server URL must not be empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid llama.cpp server URL %q: %w", trimmed, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("llama.cpp server URL must use http or https (got %q)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("llama.cpp server URL must include a host: %q", trimmed)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "/v1" {
		path = ""
	} else if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
		path = strings.TrimRight(path, "/")
	}
	parsed.Scheme = scheme
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// LlamaInferenceUrl appends /v1 to a normalized management URL for OpenAI-compatible inference endpoints.
func LlamaInferenceUrl(managementUrl string) string {
	base := strings.TrimRight(managementUrl, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// NewLlamaClient creates a LlamaClient targeting baseUrl. Nil HTTPClient defaults to ai.NodeHttpProxyDefaultClient().
func NewLlamaClient(baseUrl string, opts ...*LlamaClientOptions) (*LlamaClient, error) {
	normalized, err := NormalizeLlamaServerUrl(baseUrl)
	if err != nil {
		return nil, err
	}
	timeout := DefaultLlamaTimeout
	httpClient := ai.NodeHttpProxyDefaultClient()
	apiKey := ""
	if len(opts) > 0 && opts[0] != nil {
		o := opts[0]
		if o.Timeout > 0 {
			timeout = o.Timeout
		}
		if o.HTTPClient != nil {
			httpClient = o.HTTPClient
		}
		if o.ApiKey != nil {
			apiKey = strings.TrimSpace(*o.ApiKey)
		}
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &LlamaClient{
		baseUrl:    normalized,
		apiKey:     apiKey,
		timeout:    timeout,
		httpClient: httpClient,
	}, nil
}

// BaseUrl returns the normalized management base URL.
func (c *LlamaClient) BaseUrl() string {
	return c.baseUrl
}

func (c *LlamaClient) applyHeaders(req *http.Request, hasBody bool) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
}

func (c *LlamaClient) formatReachError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	detail := err.Error()
	if errors.Is(err, syscall.ECONNREFUSED) {
		hostPort := c.baseUrl
		if u, parseErr := url.Parse(c.baseUrl); parseErr == nil && u.Host != "" {
			hostPort = u.Host
		}
		detail = fmt.Sprintf("connect ECONNREFUSED %s (%s)", hostPort, err.Error())
	}
	return fmt.Errorf("Could not reach llama.cpp server at %s: %s", c.baseUrl, detail)
}

func (c *LlamaClient) requestJSON(ctx context.Context, method, path string, body any, out any) error {
	reqCtx := ctx
	var cancel context.CancelFunc
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	if c.timeout > 0 {
		if _, hasDeadline := reqCtx.Deadline(); !hasDeadline {
			reqCtx, cancel = context.WithTimeout(reqCtx, c.timeout)
			defer cancel()
		}
	}

	var bodyReader io.Reader
	hasBody := body != nil
	if hasBody {
		encoded, err := jsonx.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, c.baseUrl+path, bodyReader)
	if err != nil {
		return err
	}
	c.applyHeaders(req, hasBody)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.formatReachError(err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.formatReachError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return clientFormatHTTPError(resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return jsonx.Unmarshal(raw, out)
}

func clientFormatHTTPError(status int, body []byte) error {
	trimmed := strings.TrimSpace(string(body))
	if trimmed != "" {
		if parsed, err := jsonx.ParseString(trimmed); err == nil {
			if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
				if errObj, ok := obj.GetObject("error"); ok && errObj != nil {
					if msg, ok := errObj.GetString("message"); ok && msg != "" {
						return fmt.Errorf("llama.cpp server error (%d): %s", status, msg)
					}
				}
				if msg, ok := obj.GetString("error"); ok && msg != "" {
					return fmt.Errorf("llama.cpp server error (%d): %s", status, msg)
				}
				if msg, ok := obj.GetString("message"); ok && msg != "" {
					return fmt.Errorf("llama.cpp server error (%d): %s", status, msg)
				}
			}
		}
		return fmt.Errorf("llama.cpp server error (%d): %s", status, trimmed)
	}
	return fmt.Errorf("llama.cpp server error (%d)", status)
}

// List fetches the model catalog from GET /models.
func (c *LlamaClient) List(ctx context.Context) ([]LlamaModelInfo, error) {
	var resp LlamaModelsResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/models", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// Reload requests a catalog refresh and returns the updated model list.
func (c *LlamaClient) Reload(ctx context.Context) ([]LlamaModelInfo, error) {
	var resp LlamaModelsResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/models?reload=true", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// Props fetches server properties from GET /props, optionally scoped to one model with autoload=false.
func (c *LlamaClient) Props(ctx context.Context, modelId *string) (*LlamaServerProps, error) {
	path := "/props"
	if modelId != nil && *modelId != "" {
		q := url.Values{}
		q.Set("model", *modelId)
		q.Set("autoload", "false")
		path += "?" + q.Encode()
	}
	var props LlamaServerProps
	if err := c.requestJSON(ctx, http.MethodGet, path, nil, &props); err != nil {
		return nil, err
	}
	return &props, nil
}

// Load initiates loading a model via POST /models/load.
func (c *LlamaClient) Load(ctx context.Context, modelId string, extraArgs ...string) error {
	payload := jsonx.NewObject()
	payload.Set("model", modelId)
	if len(extraArgs) > 0 {
		payload.Set("extra_args", extraArgs)
	}
	return c.requestJSON(ctx, http.MethodPost, "/models/load", payload, nil)
}

// Unload requests unloading a model via POST /models/unload.
func (c *LlamaClient) Unload(ctx context.Context, modelId string) error {
	payload := jsonx.NewObject()
	payload.Set("model", modelId)
	return c.requestJSON(ctx, http.MethodPost, "/models/unload", payload, nil)
}

// UnloadAndWait unloads a model and polls GET /models until its status is no longer loaded/sleeping/loading.
func (c *LlamaClient) UnloadAndWait(ctx context.Context, modelId string) error {
	if err := c.Unload(ctx, modelId); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		models, err := c.List(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, m := range models {
			if m.Id == modelId {
				found = true
				if m.Status.Value == "unloaded" {
					return nil
				}
				break
			}
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Download initiates downloading a model spec (for example owner/repo:quant) via POST /models.
func (c *LlamaClient) Download(ctx context.Context, modelSpec string, hfToken ...string) error {
	payload := jsonx.NewObject()
	payload.Set("model", modelSpec)
	if len(hfToken) > 0 && hfToken[0] != "" {
		payload.Set("hf_token", hfToken[0])
	}
	return c.requestJSON(ctx, http.MethodPost, "/models", payload, nil)
}

func (c *LlamaClient) openSSE(ctx context.Context) (io.ReadCloser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseUrl+"/models/sse", nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req, false)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, c.formatReachError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, clientFormatHTTPError(resp.StatusCode, raw)
	}
	return resp.Body, nil
}

func clientConsumeSSE(body io.Reader, onEvent func(LlamaModelEvent) (stop bool, err error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var dataLines []string
	flush := func() (bool, error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		var ev LlamaModelEvent
		if err := jsonx.Unmarshal([]byte(raw), &ev); err != nil {
			return false, nil
		}
		return onEvent(ev)
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			stop, err := flush()
			if err != nil || stop {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			val := strings.TrimPrefix(line, "data:")
			val = strings.TrimPrefix(val, " ")
			dataLines = append(dataLines, val)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err := flush()
	return err
}

// Watch subscribes to GET /models/sse and invokes onEvent until canceled or onEvent returns an error.
func (c *LlamaClient) Watch(ctx context.Context, onEvent func(LlamaModelEvent) error) error {
	body, err := c.openSSE(ctx)
	if err != nil {
		return err
	}
	defer body.Close()
	return clientConsumeSSE(body, func(ev LlamaModelEvent) (bool, error) {
		if onEvent != nil {
			if cbErr := onEvent(ev); cbErr != nil {
				return true, cbErr
			}
		}
		return false, nil
	})
}

func clientExtractEventError(data *jsonx.Object) string {
	if data == nil {
		return ""
	}
	if msg, ok := data.GetString("error"); ok && msg != "" {
		return msg
	}
	if errObj, ok := data.GetObject("error"); ok && errObj != nil {
		if msg, ok := errObj.GetString("message"); ok && msg != "" {
			return msg
		}
	}
	if msg, ok := data.GetString("message"); ok && msg != "" {
		return msg
	}
	return ""
}

func clientFormatLoadStage(current string) string {
	if current == "" {
		return "Loading model"
	}
	return "Loading " + strings.ReplaceAll(current, "_", " ")
}

func clientParseLoadProgress(data *jsonx.Object) *LlamaProgress {
	if data == nil {
		return nil
	}
	progObj, ok := data.GetObject("progress")
	if !ok || progObj == nil {
		return nil
	}
	var p LlamaLoadProgress
	if err := jsonx.Decode(progObj, &p); err != nil {
		return nil
	}
	out := &LlamaProgress{
		Message: clientFormatLoadStage(p.Current),
		Ratio:   p.Value,
	}
	if len(p.Stages) > 0 && p.Current != "" {
		if idx := slices.Index(p.Stages, p.Current); idx >= 0 {
			d := fmt.Sprintf("step %d/%d", idx+1, len(p.Stages))
			out.Detail = &d
		}
	}
	return out
}

func clientParseDownloadProgress(data *jsonx.Object) *LlamaProgress {
	if data == nil {
		return nil
	}
	progObj, ok := data.GetObject("progress")
	if !ok || progObj == nil {
		return nil
	}
	var doneTotal int64
	var sizeTotal int64
	sawFile := false
	for _, val := range progObj.All() {
		fileObj, ok := val.(*jsonx.Object)
		if !ok || fileObj == nil {
			continue
		}
		sawFile = true
		if d, ok := fileObj.GetNumber("done"); ok {
			doneTotal += int64(d)
		}
		if t, ok := fileObj.GetNumber("total"); ok {
			sizeTotal += int64(t)
		}
	}
	if !sawFile {
		return nil
	}
	out := &LlamaProgress{
		Message: "Downloading model",
	}
	if sizeTotal > 0 {
		ratio := float64(doneTotal) / float64(sizeTotal)
		if ratio < 0 {
			ratio = 0
		} else if ratio > 1 {
			ratio = 1
		}
		detail := fmt.Sprintf("%s / %s", FormatBytes(doneTotal), FormatBytes(sizeTotal))
		out.Ratio = &ratio
		out.Detail = &detail
	} else {
		detail := FormatBytes(doneTotal)
		out.Detail = &detail
	}
	return out
}

// LoadAndWait loads modelId, streams SSE progress updates to onProgress, and resolves once the catalog shows loaded.
func (c *LlamaClient) LoadAndWait(ctx context.Context, modelId string, onProgress func(LlamaProgress), extraArgs ...string) (*LlamaModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	body, sseErr := c.openSSE(watchCtx)
	if sseErr == nil {
		defer body.Close()
	}

	if err := c.Load(ctx, modelId, extraArgs...); err != nil {
		return nil, err
	}

	if body != nil {
		var sawLoading bool
		var watchFailure error
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = clientConsumeSSE(body, func(ev LlamaModelEvent) (bool, error) {
				if ev.Model != "" && ev.Model != modelId {
					return false, nil
				}
				switch ev.Event {
				case "status_change":
					status, _ := ev.Data.GetString("status")
					if errMsg := clientExtractEventError(ev.Data); errMsg != "" {
						watchFailure = fmt.Errorf("failed to load model %q: %s", modelId, errMsg)
						return true, nil
					}
					if status == "loading" {
						sawLoading = true
						if prog := clientParseLoadProgress(ev.Data); prog != nil && onProgress != nil {
							onProgress(*prog)
						}
					} else if status == "loaded" {
						return true, nil
					} else if status == "unloaded" && sawLoading {
						watchFailure = fmt.Errorf("failed to load model %q: server reported unloaded", modelId)
						return true, nil
					}
				case "load_error":
					errMsg := clientExtractEventError(ev.Data)
					if errMsg == "" {
						errMsg = "load failed"
					}
					watchFailure = fmt.Errorf("failed to load model %q: %s", modelId, errMsg)
					return true, nil
				}
				return false, nil
			})
		}()

		select {
		case <-ctx.Done():
			cancelWatch()
			<-done
			return nil, ctx.Err()
		case <-done:
			if watchFailure != nil {
				return nil, watchFailure
			}
		}
	}

	// Confirm loaded state via GET /models (and handle servers whose SSE stream closes early).
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		models, err := c.List(ctx)
		if err != nil {
			return nil, err
		}
		for i := range models {
			if models[i].Id == modelId {
				switch models[i].Status.Value {
				case "loaded":
					m := models[i]
					return &m, nil
				case "unloaded":
					if models[i].Status.Failed != nil && *models[i].Status.Failed {
						return nil, fmt.Errorf("failed to load model %q", modelId)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// DownloadAndWait starts downloading modelSpec, streams SSE byte progress to onProgress, and returns the refreshed catalog.
func (c *LlamaClient) DownloadAndWait(ctx context.Context, modelSpec string, onProgress func(LlamaProgress), hfToken ...string) ([]LlamaModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	body, sseErr := c.openSSE(watchCtx)
	if sseErr == nil {
		defer body.Close()
	}

	if err := c.Download(ctx, modelSpec, hfToken...); err != nil {
		return nil, err
	}

	if body != nil {
		var watchFailure error
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = clientConsumeSSE(body, func(ev LlamaModelEvent) (bool, error) {
				if ev.Model != "" && ev.Model != modelSpec {
					return false, nil
				}
				switch ev.Event {
				case "download_progress":
					if prog := clientParseDownloadProgress(ev.Data); prog != nil && onProgress != nil {
						onProgress(*prog)
					}
				case "download_finished":
					return true, nil
				case "download_error":
					errMsg := clientExtractEventError(ev.Data)
					if errMsg == "" {
						errMsg = "download failed"
					}
					watchFailure = fmt.Errorf("failed to download model %q: %s", modelSpec, errMsg)
					return true, nil
				case "status_change":
					status, _ := ev.Data.GetString("status")
					if errMsg := clientExtractEventError(ev.Data); errMsg != "" {
						watchFailure = fmt.Errorf("failed to download model %q: %s", modelSpec, errMsg)
						return true, nil
					}
					if status == "unloaded" || status == "loaded" {
						return true, nil
					}
				}
				return false, nil
			})
		}()

		select {
		case <-ctx.Done():
			cancelWatch()
			<-done
			return nil, ctx.Err()
		case <-done:
			if watchFailure != nil {
				return nil, watchFailure
			}
		}
	}

	models, err := c.Reload(ctx)
	if err != nil {
		return nil, err
	}
	return models, nil
}
