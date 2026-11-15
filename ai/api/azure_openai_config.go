// Ported from packages/ai/src/api/azure-openai-config.ts (pi v1.5.2).

package api

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
)

const aocDefaultAzureApiVersion = "v1"

// AzureEndpointOptions configures Azure OpenAI endpoint and deployment resolution.
// Azure models ship without a baseUrl: one resource per user, resolved per request.
type AzureEndpointOptions struct {
	ai.StreamOptions
	AzureApiVersion     *string `json:"azureApiVersion,omitzero"`
	AzureResourceName   *string `json:"azureResourceName,omitzero"`
	AzureBaseUrl        *string `json:"azureBaseUrl,omitzero"`
	AzureDeploymentName *string `json:"azureDeploymentName,omitzero"`
}

// ResolvedAzureConfig holds resolved Azure OpenAI baseUrl and apiVersion values.
type ResolvedAzureConfig struct {
	BaseUrl    string `json:"baseUrl"`
	ApiVersion string `json:"apiVersion"`
}

func aocParseDeploymentNameMap(value string) map[string]string {
	m := make(map[string]string)
	if value == "" {
		return m
	}
	for _, entry := range strings.Split(value, ",") {
		trimmed := js.Trim(entry)
		if trimmed == "" {
			continue
		}
		modelId, deploymentName, found := strings.Cut(trimmed, "=")
		if !found || modelId == "" || deploymentName == "" {
			continue
		}
		m[js.Trim(modelId)] = js.Trim(deploymentName)
	}
	return m
}

func aocEnv(options *AzureEndpointOptions) ai.ProviderEnv {
	if options == nil {
		return nil
	}
	return options.Env
}

// ResolveDeploymentName resolves the Azure deployment name from options,
// AZURE_OPENAI_DEPLOYMENT_NAME_MAP, or model.Id.
func ResolveDeploymentName(model *ai.Model, options *AzureEndpointOptions) string {
	if options != nil && options.AzureDeploymentName != nil && *options.AzureDeploymentName != "" {
		return *options.AzureDeploymentName
	}
	mappedDeployment := aocParseDeploymentNameMap(
		ai.GetProviderEnvValue("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", aocEnv(options)),
	)[model.Id]
	if mappedDeployment != "" {
		return mappedDeployment
	}
	return model.Id
}

func aocNormalizeAzureBaseUrl(baseUrl string) (string, error) {
	trimmed := strings.TrimRight(js.Trim(baseUrl), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("Invalid Azure OpenAI base URL: %s", baseUrl)
	}

	host := strings.ToLower(u.Hostname())
	isAzureHost := strings.HasSuffix(host, ".openai.azure.com") ||
		strings.HasSuffix(host, ".cognitiveservices.azure.com") ||
		strings.HasSuffix(host, ".ai.azure.com")
	normalizedPath := strings.TrimRight(u.Path, "/")

	// Ensure Azure hosts have /openai/v1 as base path so requests resolve against
	// the Azure v1 API path and ?api-version=v1 cleanly.
	if isAzureHost && (normalizedPath == "" ||
		normalizedPath == "/openai" ||
		normalizedPath == "/openai/v1/responses") {
		u.Path = "/openai/v1"
		u.RawPath = ""
		u.RawQuery = ""
	}

	return strings.TrimRight(u.String(), "/"), nil
}

func aocBuildDefaultBaseUrl(resourceName string) string {
	return fmt.Sprintf("https://%s.openai.azure.com/openai/v1", resourceName)
}

// ResolveAzureBaseUrl resolves and normalizes the Azure OpenAI base URL.
func ResolveAzureBaseUrl(model *ai.Model, options *AzureEndpointOptions) (string, error) {
	var baseUrl string
	if options != nil && options.AzureBaseUrl != nil {
		baseUrl = js.Trim(*options.AzureBaseUrl)
	}
	if baseUrl == "" {
		baseUrl = js.Trim(ai.GetProviderEnvValue("AZURE_OPENAI_BASE_URL", aocEnv(options)))
	}

	var resourceName string
	if options != nil && options.AzureResourceName != nil {
		resourceName = *options.AzureResourceName
	}
	if resourceName == "" {
		resourceName = ai.GetProviderEnvValue("AZURE_OPENAI_RESOURCE_NAME", aocEnv(options))
	}

	resolvedBaseUrl := baseUrl
	if resolvedBaseUrl == "" && resourceName != "" {
		resolvedBaseUrl = aocBuildDefaultBaseUrl(resourceName)
	}
	if resolvedBaseUrl == "" && model != nil && model.BaseUrl != "" {
		resolvedBaseUrl = model.BaseUrl
	}
	if resolvedBaseUrl == "" {
		return "", fmt.Errorf(
			"Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME, or pass azureBaseUrl, azureResourceName, or model.baseUrl.",
		)
	}

	return aocNormalizeAzureBaseUrl(resolvedBaseUrl)
}

// ResolveAzureConfig resolves both Azure base URL and API version.
func ResolveAzureConfig(model *ai.Model, options *AzureEndpointOptions) (ResolvedAzureConfig, error) {
	baseUrl, err := ResolveAzureBaseUrl(model, options)
	if err != nil {
		return ResolvedAzureConfig{}, err
	}
	var apiVersion string
	if options != nil && options.AzureApiVersion != nil && *options.AzureApiVersion != "" {
		apiVersion = *options.AzureApiVersion
	}
	if apiVersion == "" {
		apiVersion = ai.GetProviderEnvValue("AZURE_OPENAI_API_VERSION", aocEnv(options))
	}
	if apiVersion == "" {
		apiVersion = aocDefaultAzureApiVersion
	}
	return ResolvedAzureConfig{
		BaseUrl:    baseUrl,
		ApiVersion: apiVersion,
	}, nil
}
