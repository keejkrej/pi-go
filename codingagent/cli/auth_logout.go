// Ported from packages/coding-agent/src/cli/auth-logout.ts (pi v1.5.2).

package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// AuthLogoutOptions supplies dependencies to RunAuthLogout.
type AuthLogoutOptions struct {
	ModelRuntime *core.ModelRuntime
	Terminal     AuthOutput
}

const aloAuthLogoutUsage = utils.AppName + " auth logout <provider>"

// RunAuthLogout removes stored credentials for the specified provider while leaving
// environment variables and models.json configuration intact.
func RunAuthLogout(ctx context.Context, args []string, options AuthLogoutOptions) int {
	if ctx == nil {
		ctx = context.Background()
	}
	modelRuntime := options.ModelRuntime
	terminal := options.Terminal

	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		terminal.Error("Usage: " + aloAuthLogoutUsage)
		return 1
	}
	providerRef := args[0]

	stored, err := modelRuntime.ListCredentials(ctx, nil)
	if err != nil {
		terminal.Error(fmt.Sprintf("Could not read stored credentials: %v", err))
		return 1
	}

	var provider *ai.Provider
	if matches := core.FindMatchingProviders(modelRuntime.GetProviders(), providerRef); len(matches) > 0 {
		provider = matches[0]
	}

	targetId := providerRef
	if provider != nil {
		targetId = provider.Id
	}

	var credential *ai.CredentialInfo
	for i := range stored {
		if stored[i].ProviderId == targetId {
			credential = &stored[i]
			break
		}
	}

	if provider == nil && credential == nil {
		terminal.Error(fmt.Sprintf(`Unknown provider "%s".`, tui.ReplaceControlCharacters(providerRef)))
		storedIds := make([]string, len(stored))
		for i, c := range stored {
			storedIds[i] = c.ProviderId
		}
		sort.Strings(storedIds)
		listStr := "none"
		if len(storedIds) > 0 {
			listStr = strings.Join(storedIds, ", ")
		}
		terminal.Error("Stored credentials: " + listStr)
		return 1
	}

	providerName := providerRef
	if provider != nil {
		providerName = provider.Name
	}

	if credential == nil {
		terminal.Write(fmt.Sprintf("No stored credentials for %s. Environment variables and models.json config are unchanged.", providerName))
		return 0
	}

	if err := modelRuntime.Logout(ctx, credential.ProviderId, nil); err != nil {
		var syncErr *core.CredentialSynchronizationError
		if errors.As(err, &syncErr) {
			terminal.Error(fmt.Sprintf("Credentials removed for %s, but local model state could not be synchronized: %v", providerName, err))
		} else {
			terminal.Error(fmt.Sprintf("Logout failed: %v", err))
		}
		return 1
	}

	if credential.Type == ai.AuthTypeOAuth {
		terminal.Write(fmt.Sprintf("Logged out of %s.", providerName))
	} else {
		terminal.Write(fmt.Sprintf("Removed stored API key for %s. Environment variables and models.json config are unchanged.", providerName))
	}
	return 0
}
