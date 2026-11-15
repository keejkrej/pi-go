// Ported from packages/ai/src/env-api-keys.ts (pi v1.5.2).

package ai

import (
	"os"
	"path/filepath"
	"sync"
)

const (
	AnthropicAuthTokenEnv         = "ANTHROPIC_AUTH_TOKEN"
	AnthropicOAuthTokenEnv        = "ANTHROPIC_OAUTH_TOKEN"
	AnthropicApiKeyEnv            = "ANTHROPIC_API_KEY"
	AnthropicFederationRuleIdEnv  = "ANTHROPIC_FEDERATION_RULE_ID"
	AnthropicOrganizationIdEnv    = "ANTHROPIC_ORGANIZATION_ID"
	AnthropicServiceAccountIdEnv  = "ANTHROPIC_SERVICE_ACCOUNT_ID"
	AnthropicIdentityTokenFileEnv = "ANTHROPIC_IDENTITY_TOKEN_FILE"
	AnthropicWorkspaceIdEnv       = "ANTHROPIC_WORKSPACE_ID"
)

var (
	eakVertexAdcMu     sync.Mutex
	eakVertexAdcKnown  bool
	eakVertexAdcExists bool
	eakStat            = os.Stat
	eakUserHomeDir     = os.UserHomeDir
)

var eakEnvMap = map[string][]string{
	"github-copilot":                {"COPILOT_GITHUB_TOKEN"},
	"anthropic":                     {AnthropicAuthTokenEnv, AnthropicOAuthTokenEnv, AnthropicApiKeyEnv},
	"ant-ling":                      {"ANT_LING_API_KEY"},
	"qwen-token-plan":               {"QWEN_TOKEN_PLAN_TEAM_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
	"qwen-token-plan-cn":            {"QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
	"qwen-token-plan-individual":    {"QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
	"qwen-token-plan-individual-cn": {"QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
	"openai":                        {"OPENAI_API_KEY"},
	"azure":                         {"AZURE_OPENAI_API_KEY"},
	"nvidia":                        {"NVIDIA_API_KEY"},
	"deepseek":                      {"DEEPSEEK_API_KEY"},
	"google":                        {"GEMINI_API_KEY"},
	"google-vertex":                 {"GOOGLE_CLOUD_API_KEY"},
	"groq":                          {"GROQ_API_KEY"},
	"cerebras":                      {"CEREBRAS_API_KEY"},
	"xai":                           {"XAI_API_KEY"},
	"typesafe":                      {"TYPESAFE_API_KEY"},
	"radius":                        {"RADIUS_API_KEY"},
	"openrouter":                    {"OPENROUTER_API_KEY"},
	"vercel-ai-gateway":             {"AI_GATEWAY_API_KEY"},
	"zai":                           {"ZAI_API_KEY"},
	"zai-coding-cn":                 {"ZAI_CODING_CN_API_KEY"},
	"mistral":                       {"MISTRAL_API_KEY"},
	"minimax":                       {"MINIMAX_API_KEY"},
	"minimax-cn":                    {"MINIMAX_CN_API_KEY"},
	"moonshotai":                    {"MOONSHOT_API_KEY"},
	"moonshotai-cn":                 {"MOONSHOT_API_KEY"},
	"huggingface":                   {"HF_TOKEN"},
	"fireworks":                     {"FIREWORKS_API_KEY"},
	"together":                      {"TOGETHER_API_KEY"},
	"baseten":                       {"BASETEN_API_KEY"},
	"opencode":                      {"OPENCODE_API_KEY"},
	"opencode-go":                   {"OPENCODE_API_KEY"},
	"kimi-coding":                   {"KIMI_API_KEY"},
	"meta":                          {"META_API_KEY"},
	"cloudflare-workers-ai":         {"CLOUDFLARE_API_KEY"},
	"cloudflare-ai-gateway":         {"CLOUDFLARE_API_KEY"},
	"xiaomi":                        {"XIAOMI_API_KEY"},
	"xiaomi-token-plan-cn":          {"XIAOMI_TOKEN_PLAN_CN_API_KEY"},
	"xiaomi-token-plan-ams":         {"XIAOMI_TOKEN_PLAN_AMS_API_KEY"},
	"xiaomi-token-plan-sgp":         {"XIAOMI_TOKEN_PLAN_SGP_API_KEY"},
}

func eakHasVertexAdcCredentials(env ProviderEnv) bool {
	if env != nil {
		if explicit := env["GOOGLE_APPLICATION_CREDENTIALS"]; explicit != "" {
			_, err := eakStat(explicit)
			return err == nil
		}
	}

	eakVertexAdcMu.Lock()
	defer eakVertexAdcMu.Unlock()
	if eakVertexAdcKnown {
		return eakVertexAdcExists
	}

	var exists bool
	if gacPath := GetProviderEnvValue("GOOGLE_APPLICATION_CREDENTIALS", env); gacPath != "" {
		_, err := eakStat(gacPath)
		exists = err == nil
	} else if home, err := eakUserHomeDir(); err == nil {
		adcPath := filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
		_, statErr := eakStat(adcPath)
		exists = statErr == nil
	}
	eakVertexAdcKnown = true
	eakVertexAdcExists = exists
	return exists
}

// FindEnvKeys returns configured environment variable names that can provide an API key
// for provider, excluding ambient credential sources. Returns nil when none are set.
func FindEnvKeys(provider string, env ProviderEnv) []string {
	envVars, ok := eakEnvMap[provider]
	if !ok {
		return nil
	}
	var found []string
	for _, envVar := range envVars {
		if GetProviderEnvValue(envVar, env) != "" {
			found = append(found, envVar)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

// GetEnvApiKey resolves the effective API key (or "<authenticated>" sentinel for ambient
// Google Vertex ADC and Amazon Bedrock credentials) for provider from environment variables.
func GetEnvApiKey(provider string, env ProviderEnv) *string {
	envKeys := FindEnvKeys(provider, env)
	if len(envKeys) > 0 {
		var apiKeyEnv string
		if provider == "anthropic" {
			for _, key := range envKeys {
				if key != AnthropicAuthTokenEnv {
					apiKeyEnv = key
					break
				}
			}
		} else {
			apiKeyEnv = envKeys[0]
		}
		if apiKeyEnv != "" {
			if val := GetProviderEnvValue(apiKeyEnv, env); val != "" {
				return &val
			}
		}
	}

	if provider == string(KnownProviderGoogleVertex) {
		hasCredentials := eakHasVertexAdcCredentials(env)
		hasProject := GetProviderEnvValue("GOOGLE_CLOUD_PROJECT", env) != "" || GetProviderEnvValue("GCLOUD_PROJECT", env) != ""
		hasLocation := GetProviderEnvValue("GOOGLE_CLOUD_LOCATION", env) != ""
		if hasCredentials && hasProject && hasLocation {
			s := "<authenticated>"
			return &s
		}
	}

	if provider == string(KnownProviderAmazonBedrock) {
		if GetProviderEnvValue("AWS_PROFILE", env) != "" ||
			(GetProviderEnvValue("AWS_ACCESS_KEY_ID", env) != "" && GetProviderEnvValue("AWS_SECRET_ACCESS_KEY", env) != "") ||
			GetProviderEnvValue("AWS_BEARER_TOKEN_BEDROCK", env) != "" ||
			GetProviderEnvValue("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", env) != "" ||
			GetProviderEnvValue("AWS_CONTAINER_CREDENTIALS_FULL_URI", env) != "" ||
			GetProviderEnvValue("AWS_WEB_IDENTITY_TOKEN_FILE", env) != "" {
			s := "<authenticated>"
			return &s
		}
	}

	return nil
}
