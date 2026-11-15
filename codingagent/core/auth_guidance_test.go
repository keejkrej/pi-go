package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestAuthGuidance(t *testing.T) {
	docsPath := utils.GetDocsPath()
	help := GetProviderLoginHelp()
	if !strings.Contains(help, "Use /login to log into a provider via OAuth or API key.") {
		t.Fatalf("unexpected help: %q", help)
	}
	if !strings.Contains(help, filepath.Join(docsPath, "providers.md")) {
		t.Fatalf("help missing providers.md: %q", help)
	}
	if !strings.Contains(help, filepath.Join(docsPath, "models.md")) {
		t.Fatalf("help missing models.md: %q", help)
	}

	if got := FormatNoModelsAvailableMessage(); got != "No models available. "+help {
		t.Fatalf("FormatNoModelsAvailableMessage = %q", got)
	}
	if got := FormatNoModelSelectedMessage(); !strings.Contains(got, "No model selected.") || !strings.Contains(got, "/model") {
		t.Fatalf("FormatNoModelSelectedMessage = %q", got)
	}
	if got := FormatNoApiKeyFoundMessage("anthropic"); !strings.Contains(got, "No API key found for anthropic.") {
		t.Fatalf("FormatNoApiKeyFoundMessage(anthropic) = %q", got)
	}
	if got := FormatNoApiKeyFoundMessage("unknown"); !strings.Contains(got, "No API key found for the selected model.") {
		t.Fatalf("FormatNoApiKeyFoundMessage(unknown) = %q", got)
	}
}
