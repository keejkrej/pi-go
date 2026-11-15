// Ported from packages/ai/test/models-entry.test.ts (pi v1.5.2).

package providers_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
)

func TestModelsEntry_LightweightAiPackageDeps(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{join .Deps "\n"}}`, "github.com/keejkrej/pi-go/ai")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, string(out))
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	forbidden := []string{
		"github.com/keejkrej/pi-go/ai/catalog",
		"github.com/keejkrej/pi-go/ai/api",
		"github.com/keejkrej/pi-go/ai/oauth",
		"github.com/keejkrej/pi-go/ai/providers",
		"github.com/keejkrej/pi-go/ai/compat",
		"github.com/anthropics/",
		"github.com/openai/",
		"google.golang.org/genai",
		"github.com/aws/aws-sdk-go-v2",
	}
	for _, dep := range deps {
		for _, prefix := range forbidden {
			if strings.HasPrefix(dep, prefix) || dep == prefix {
				t.Fatalf("heavy dependency loaded by ai package: %s", dep)
			}
		}
	}
}

func TestModelsEntry_RunsFauxCompletionThroughModels(t *testing.T) {
	models := ai.CreateModels(nil)
	if len(models.GetModels(nil)) != 0 {
		t.Fatalf("expected empty models, got %d", len(models.GetModels(nil)))
	}
	if len(models.GetProviders()) != 0 {
		t.Fatalf("expected empty providers, got %d", len(models.GetProviders()))
	}
	faux, err := providers.FauxProvider(nil)
	if err != nil {
		t.Fatalf("FauxProvider failed: %v", err)
	}
	models.SetProvider(faux.Provider)
	faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("OK"), nil),
		},
	})

	resp, err := models.CompleteSimple(context.Background(), faux.GetModel(), &ai.Context{Messages: []ai.Message{}}, nil)
	if err != nil {
		t.Fatalf("CompleteSimple failed: %v", err)
	}
	if resp.StopReason != ai.StopReasonStop {
		t.Fatalf("StopReason = %q, want %q", resp.StopReason, ai.StopReasonStop)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Content len = %d, want 1", len(resp.Content))
	}
	tc, ok := resp.Content[0].(*ai.TextContent)
	if !ok || tc.Text != "OK" {
		t.Fatalf("Content[0] = %#v, want text \"OK\"", resp.Content[0])
	}
}
