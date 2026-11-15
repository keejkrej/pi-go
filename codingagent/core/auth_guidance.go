// Ported from packages/coding-agent/src/core/auth-guidance.ts (pi v1.5.2).

package core

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

const agUnknownProvider = "unknown"

// GetProviderLoginHelp returns instructions for logging into a provider and links to provider/model docs.
func GetProviderLoginHelp() string {
	docsPath := utils.GetDocsPath()
	return strings.Join([]string{
		"Use /login to log into a provider via OAuth or API key. See:",
		"  " + filepath.Join(docsPath, "providers.md"),
		"  " + filepath.Join(docsPath, "models.md"),
	}, "\n")
}

// FormatNoModelsAvailableMessage formats the guidance message when no models are available.
func FormatNoModelsAvailableMessage() string {
	return "No models available. " + GetProviderLoginHelp()
}

// FormatNoModelSelectedMessage formats the guidance message when no model is selected.
func FormatNoModelSelectedMessage() string {
	return fmt.Sprintf("No model selected.\n\n%s\n\nThen use /model to select a model.", GetProviderLoginHelp())
}

// FormatNoApiKeyFoundMessage formats the guidance message when no API key is configured for provider.
func FormatNoApiKeyFoundMessage(provider string) string {
	providerDisplay := provider
	if provider == agUnknownProvider {
		providerDisplay = "the selected model"
	}
	return fmt.Sprintf("No API key found for %s.\n\n%s", providerDisplay, GetProviderLoginHelp())
}
