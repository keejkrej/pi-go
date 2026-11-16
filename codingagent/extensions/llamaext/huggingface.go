// Ported from packages/coding-agent/src/extensions/llama/huggingface.ts (pi v1.5.2).

package llamaext

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/omap"
)

// DefaultHuggingFaceBaseUrl is the default Hugging Face Hub API origin.
const DefaultHuggingFaceBaseUrl = "https://huggingface.co"

// HuggingFaceModel summarizes a GGUF repository returned by the Hugging Face models API.
type HuggingFaceModel struct {
	Id        string `json:"id"`
	Downloads int    `json:"downloads"`
	Likes     int    `json:"likes,omitzero"`
}

// HuggingFaceQuantization describes an available GGUF quantization and its summed byte size.
type HuggingFaceQuantization struct {
	Name string `json:"name"`
	Size *int64 `json:"size,omitzero"`
}

// HuggingFaceModelDetails holds gated status and GGUF quantizations for a Hugging Face repository.
type HuggingFaceModelDetails struct {
	Id            string                    `json:"id"`
	Gated         any                       `json:"gated,omitzero"`
	Quantizations []HuggingFaceQuantization `json:"quantizations"`
}

// HuggingFaceClientOptions configures NewHuggingFaceClient.
type HuggingFaceClientOptions struct {
	Token      string
	BaseUrl    string
	HTTPClient *http.Client
	Timeout    time.Duration
}

// HuggingFaceClient queries Hugging Face Hub for GGUF models and quantization files.
type HuggingFaceClient struct {
	token      string
	baseUrl    string
	httpClient *http.Client
	timeout    time.Duration
}

type huggSibling struct {
	Rfilename string `json:"rfilename"`
	Size      *int64 `json:"size,omitzero"`
	Lfs       *struct {
		Size *int64 `json:"size,omitzero"`
	} `json:"lfs,omitzero"`
}

type huggRawModelDetails struct {
	Id       string        `json:"id"`
	Gated    any           `json:"gated,omitzero"`
	Siblings []huggSibling `json:"siblings,omitzero"`
}

var (
	huggShardSuffixRe = jsre.MustCompile(`-\d+-of-\d+$`, "")
	huggQuantTokenRe  = jsre.MustCompile(`(?:^|[-_.])([iI]?[qQ]\d+(?:_[A-Za-z0-9_]+)*|[bB][fF]16|[fF](?:16|32)|[fF][pP](?:16|32)|[mM]?[xX][fF][pP]\d+(?:_[A-Za-z0-9_]+)*|[tT][qQ]\d+_[A-Za-z0-9_]+)$`, "")
	huggRateResetRe   = jsre.MustCompile(`reset=(\d+)`, "")
)

func huggGetEnv(key string, overrides map[string]string) string {
	if overrides != nil {
		return overrides[key]
	}
	return os.Getenv(key)
}

func huggExpandHome(p string, home string) string {
	if p == "" || !strings.HasPrefix(p, "~") || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return p
}

func huggReadTokenFile(filePath string) string {
	if filePath == "" {
		return ""
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// FindHuggingFaceToken locates a Hugging Face access token from env vars or standard token files.
// Passing an envOverrides map queries that map instead of process environment variables.
func FindHuggingFaceToken(envOverrides ...map[string]string) string {
	var overrides map[string]string
	if len(envOverrides) > 0 {
		overrides = envOverrides[0]
	}
	if tok := strings.TrimSpace(huggGetEnv("HF_TOKEN", overrides)); tok != "" {
		return tok
	}
	home := ""
	if overrides != nil {
		home = huggGetEnv("HOME", overrides)
		if home == "" {
			home = huggGetEnv("USERPROFILE", overrides)
		}
	} else {
		home, _ = os.UserHomeDir()
	}
	var candidates []string
	if tokPath := strings.TrimSpace(huggGetEnv("HF_TOKEN_PATH", overrides)); tokPath != "" {
		candidates = append(candidates, huggExpandHome(tokPath, home))
	}
	if hfHome := strings.TrimSpace(huggGetEnv("HF_HOME", overrides)); hfHome != "" {
		candidates = append(candidates, filepath.Join(huggExpandHome(hfHome, home), "token"))
	}
	if xdgCache := strings.TrimSpace(huggGetEnv("XDG_CACHE_HOME", overrides)); xdgCache != "" {
		candidates = append(candidates, filepath.Join(huggExpandHome(xdgCache, home), "huggingface", "token"))
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".cache", "huggingface", "token"))
	}
	for _, candidate := range candidates {
		if tok := huggReadTokenFile(candidate); tok != "" {
			return tok
		}
	}
	return ""
}

// NewHuggingFaceClient creates a HuggingFaceClient. Nil HTTPClient defaults to ai.NodeHttpProxyDefaultClient().
func NewHuggingFaceClient(opts ...*HuggingFaceClientOptions) *HuggingFaceClient {
	baseUrl := DefaultHuggingFaceBaseUrl
	httpClient := ai.NodeHttpProxyDefaultClient()
	timeout := DefaultLlamaTimeout
	token := ""
	if len(opts) > 0 && opts[0] != nil {
		o := opts[0]
		token = strings.TrimSpace(o.Token)
		if strings.TrimSpace(o.BaseUrl) != "" {
			baseUrl = strings.TrimRight(strings.TrimSpace(o.BaseUrl), "/")
		}
		if o.HTTPClient != nil {
			httpClient = o.HTTPClient
		}
		if o.Timeout > 0 {
			timeout = o.Timeout
		}
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HuggingFaceClient{
		token:      token,
		baseUrl:    baseUrl,
		httpClient: httpClient,
		timeout:    timeout,
	}
}

// SetToken updates the bearer token used for subsequent requests.
func (c *HuggingFaceClient) SetToken(token string) {
	c.token = strings.TrimSpace(token)
}

func huggRetryDelay(headers http.Header) (time.Duration, bool) {
	if retryAfter := strings.TrimSpace(headers.Get("Retry-After")); retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second, true
		}
		if t, err := http.ParseTime(retryAfter); err == nil {
			d := time.Until(t)
			if d > 0 {
				return d, true
			}
		}
	}
	if rateLimit := headers.Get("RateLimit"); rateLimit != "" {
		if m := huggRateResetRe.Exec(rateLimit, 0); m != nil {
			if secs, err := strconv.Atoi(m.Group(1)); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second, true
			}
		}
	}
	return 0, false
}

func (c *HuggingFaceClient) getJSON(ctx context.Context, reqPath string, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	const maxRateRetries = 2
	for attempt := 0; attempt <= maxRateRetries; attempt++ {
		reqCtx := ctx
		var cancel context.CancelFunc
		if c.timeout > 0 {
			if _, hasDeadline := reqCtx.Deadline(); !hasDeadline {
				reqCtx, cancel = context.WithTimeout(reqCtx, c.timeout)
			}
		}
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseUrl+reqPath, nil)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return err
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return err
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateRetries {
			delay, ok := huggRetryDelay(resp.Header)
			if !ok {
				delay = time.Second
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			msg := strings.TrimSpace(string(raw))
			if msg != "" {
				if parsed, pErr := jsonx.ParseString(msg); pErr == nil {
					if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
						if e, ok := obj.GetString("error"); ok && e != "" {
							msg = e
						} else if m, ok := obj.GetString("message"); ok && m != "" {
							msg = m
						}
					}
				}
				return fmt.Errorf("Hugging Face API error (%d): %s", resp.StatusCode, msg)
			}
			return fmt.Errorf("Hugging Face API error (%d)", resp.StatusCode)
		}
		return jsonx.Unmarshal(raw, out)
	}
	return fmt.Errorf("Hugging Face API error (429): rate limit exceeded")
}

// Search searches Hugging Face Hub for GGUF repositories matching query sorted by downloads.
func (c *HuggingFaceClient) Search(ctx context.Context, query string) ([]HuggingFaceModel, error) {
	q := url.Values{}
	q.Set("search", query)
	q.Set("filter", "gguf")
	q.Set("sort", "downloads")
	q.Set("direction", "-1")
	q.Set("limit", "20")
	var models []HuggingFaceModel
	if err := c.getJSON(ctx, "/api/models?"+q.Encode(), &models); err != nil {
		return nil, err
	}
	return models, nil
}

func huggEscapeRepoPath(repoId string) string {
	parts := strings.Split(repoId, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func huggExtractQuantization(filename string) (string, bool) {
	base := path.Base(filename)
	lowerBase := strings.ToLower(base)
	if !strings.HasSuffix(lowerBase, ".gguf") || strings.Contains(lowerBase, "mmproj") {
		return "", false
	}
	stem := base[:len(base)-len(".gguf")]
	stem = huggShardSuffixRe.Replace(stem, "")
	m := huggQuantTokenRe.Exec(stem, 0)
	if m == nil {
		return "", false
	}
	return strings.ToUpper(m.Group(1)), true
}

// Details fetches repository metadata, gated state, and grouped GGUF quantizations.
func (c *HuggingFaceClient) Details(ctx context.Context, repoId string) (*HuggingFaceModelDetails, error) {
	var raw huggRawModelDetails
	if err := c.getJSON(ctx, "/api/models/"+huggEscapeRepoPath(repoId)+"?blobs=true", &raw); err != nil {
		return nil, err
	}

	type quantAcc struct {
		size  int64
		known bool
	}
	grouped := omap.NewMap[string, *quantAcc]()
	for _, s := range raw.Siblings {
		quant, ok := huggExtractQuantization(s.Rfilename)
		if !ok {
			continue
		}
		acc, exists := grouped.Get(quant)
		if !exists {
			acc = &quantAcc{}
			grouped.Set(quant, acc)
		}
		var fileSize *int64
		if s.Size != nil {
			fileSize = s.Size
		} else if s.Lfs != nil && s.Lfs.Size != nil {
			fileSize = s.Lfs.Size
		}
		if fileSize != nil {
			acc.size += *fileSize
			acc.known = true
		}
	}

	quants := make([]HuggingFaceQuantization, 0, grouped.Len())
	for name, acc := range grouped.All() {
		var sz *int64
		if acc.known {
			v := acc.size
			sz = &v
		}
		quants = append(quants, HuggingFaceQuantization{Name: name, Size: sz})
	}
	slices.SortStableFunc(quants, func(a, b HuggingFaceQuantization) int {
		aDef := a.Name == "Q4_K_M"
		bDef := b.Name == "Q4_K_M"
		if aDef != bDef {
			if aDef {
				return -1
			}
			return 1
		}
		switch {
		case a.Size != nil && b.Size != nil:
			if c := cmp.Compare(*a.Size, *b.Size); c != 0 {
				return c
			}
		case a.Size != nil:
			return -1
		case b.Size != nil:
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})

	id := raw.Id
	if id == "" {
		id = repoId
	}
	return &HuggingFaceModelDetails{
		Id:            id,
		Gated:         raw.Gated,
		Quantizations: quants,
	}, nil
}
