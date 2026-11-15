// Ported from packages/ai/src/providers/radius-config.ts (pi v1.5.2).

package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefaultRadiusGateway is the default Radius gateway base URL.
const DefaultRadiusGateway = "https://radius.pi.dev"

var rcHttpsSchemeRe = regexp.MustCompile(`(?i)^https?://`)

// RadiusGatewayModel describes one model advertised by a Radius gateway's /v1/config endpoint.
type RadiusGatewayModel struct {
	Id               string               `json:"id"`
	Name             string               `json:"name"`
	Reasoning        bool                 `json:"reasoning"`
	ThinkingLevelMap *ai.ThinkingLevelMap `json:"thinkingLevelMap,omitzero"`
	Input            []ai.ModelModality   `json:"input"`
	Cost             ai.ModelCost         `json:"cost"`
	ContextWindow    int                  `json:"contextWindow"`
	MaxTokens        int                  `json:"maxTokens"`
}

// RadiusGatewayConfig is the configuration returned by a Radius gateway's /v1/config endpoint.
type RadiusGatewayConfig struct {
	BaseUrl string               `json:"baseUrl"`
	Models  []RadiusGatewayModel `json:"models"`
}

// RadiusOAuthCredential is an OAuthCredential carrying optional Radius gateway configuration.
type RadiusOAuthCredential struct {
	ai.OAuthCredential
	GatewayConfig *RadiusGatewayConfig `json:"gatewayConfig,omitzero"`
}

// MarshalJSON encodes RadiusOAuthCredential preserving canonical OAuthCredential fields and gatewayConfig.
func (c RadiusOAuthCredential) MarshalJSON() ([]byte, error) {
	cred := c.OAuthCredential
	if c.GatewayConfig != nil {
		var extra *jsonx.Object
		if cred.Extra != nil {
			extra = cred.Extra.Clone()
		} else {
			extra = jsonx.NewObject()
		}
		if encoded, err := jsonx.Encode(c.GatewayConfig); err == nil {
			extra.Set("gatewayConfig", encoded)
		}
		cred.Extra = extra
	}
	return cred.MarshalJSON()
}

// UnmarshalJSON decodes RadiusOAuthCredential including gatewayConfig if present.
func (c *RadiusOAuthCredential) UnmarshalJSON(data []byte) error {
	if err := c.OAuthCredential.UnmarshalJSON(data); err != nil {
		return err
	}
	c.GatewayConfig = GetRadiusCredentialConfig(&c.OAuthCredential)
	return nil
}

func rcIsRadiusGatewayModel(value any) bool {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return false
	}
	if _, ok := obj.GetString("id"); !ok {
		return false
	}
	if _, ok := obj.GetString("name"); !ok {
		return false
	}
	if _, ok := obj.GetBool("reasoning"); !ok {
		return false
	}
	if arr, ok := obj.GetArray("input"); !ok || arr == nil {
		return false
	}
	if cost, ok := obj.GetObject("cost"); !ok || cost == nil {
		return false
	}
	if _, ok := obj.GetNumber("contextWindow"); !ok {
		return false
	}
	if _, ok := obj.GetNumber("maxTokens"); !ok {
		return false
	}
	return true
}

func rcSanitizeRadiusGatewayConfig(value any) *RadiusGatewayConfig {
	obj, ok := value.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	baseUrl, ok := obj.GetString("baseUrl")
	if !ok {
		return nil
	}
	rawModels, ok := obj.GetArray("models")
	if !ok || rawModels == nil {
		return nil
	}
	models := make([]RadiusGatewayModel, 0, len(rawModels))
	for _, entry := range rawModels {
		if !rcIsRadiusGatewayModel(entry) {
			continue
		}
		var m RadiusGatewayModel
		if err := jsonx.Decode(entry, &m); err != nil {
			continue
		}
		models = append(models, m)
	}
	return &RadiusGatewayConfig{
		BaseUrl: baseUrl,
		Models:  models,
	}
}

// NormalizeRadiusGatewayUrl adds an https:// scheme if missing and strips trailing slashes.
func NormalizeRadiusGatewayUrl(value string) string {
	withScheme := value
	if !rcHttpsSchemeRe.MatchString(value) {
		withScheme = "https://" + value
	}
	return strings.TrimRight(withScheme, "/")
}

// GetRadiusCredentialConfig extracts sanitized RadiusGatewayConfig metadata from an OAuth credential.
func GetRadiusCredentialConfig(credential *ai.OAuthCredential) *RadiusGatewayConfig {
	if credential == nil || credential.Extra == nil {
		return nil
	}
	raw, ok := credential.Extra.Get("gatewayConfig")
	if !ok || raw == nil {
		return nil
	}
	if _, isObj := raw.(*jsonx.Object); !isObj {
		encoded, err := jsonx.Encode(raw)
		if err != nil {
			return nil
		}
		raw = encoded
	}
	return rcSanitizeRadiusGatewayConfig(raw)
}

// GetRadiusModelsFromConfig materializes pi-messages Model definitions from a RadiusGatewayConfig.
func GetRadiusModelsFromConfig(providerId string, config *RadiusGatewayConfig) []*ai.Model {
	if config == nil {
		return nil
	}
	out := make([]*ai.Model, len(config.Models))
	for i, m := range config.Models {
		var tlm *ai.ThinkingLevelMap
		if m.ThinkingLevelMap != nil {
			cp := *m.ThinkingLevelMap
			tlm = &cp
		}
		cost := m.Cost
		if m.Cost.Tiers != nil {
			cost.Tiers = slices.Clone(m.Cost.Tiers)
		}
		out[i] = &ai.Model{
			BaseModel: ai.BaseModel{
				Id:       m.Id,
				Name:     m.Name,
				Api:      string(ai.KnownApiPiMessages),
				Provider: ai.ProviderId(providerId),
				BaseUrl:  config.BaseUrl,
				Input:    slices.Clone(m.Input),
				Cost:     cost,
			},
			Reasoning:        m.Reasoning,
			ThinkingLevelMap: tlm,
			ContextWindow:    m.ContextWindow,
			MaxTokens:        m.MaxTokens,
		}
	}
	return out
}

// GetRadiusModels extracts Radius models from a stored OAuth credential's gatewayConfig metadata.
func GetRadiusModels(providerId string, credential *ai.OAuthCredential) []*ai.Model {
	config := GetRadiusCredentialConfig(credential)
	if config == nil {
		return []*ai.Model{}
	}
	return GetRadiusModelsFromConfig(providerId, config)
}

func rcTruncateHttpBody(body string) string {
	trimmed := js.Trim(body)
	if js.Len(trimmed) > 512 {
		return ai.OwnedSlice(trimmed, 0, 512) + "…"
	}
	return trimmed
}

// LoadRadiusGatewayConfig fetches and validates gateway configuration from `<gateway>/v1/config`.
func LoadRadiusGatewayConfig(ctx context.Context, gateway string, apiKey *string) (*RadiusGatewayConfig, error) {
	base, err := url.Parse(gateway)
	if err != nil {
		return nil, err
	}
	target := base.ResolveReference(&url.URL{Path: "/v1/config"}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	if apiKey != nil && *apiKey != "" {
		req.Header.Set("authorization", "Bearer "+*apiKey)
	}

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Could not load Radius config from %s: %d: %s", gateway, resp.StatusCode, rcTruncateHttpBody(string(body)))
	}

	parsed, err := jsonx.Parse(body)
	if err != nil {
		return nil, err
	}
	config := rcSanitizeRadiusGatewayConfig(parsed)
	if config == nil {
		return nil, fmt.Errorf("Invalid Radius config from %s", gateway)
	}
	return config, nil
}
