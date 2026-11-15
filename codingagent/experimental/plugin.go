// Ported from packages/coding-agent/src/experimental/plugin.ts (pi v1.0.0).

package experimental

import "github.com/keejkrej/pi-go/codingagent/experimental/services"

// Service descriptor re-exports for experimental plugins.
var (
	AgentControllerService = services.AgentControllerService
	PresentationUIService  = services.PresentationUIService
	SlashCommandsService   = services.SlashCommandsService
)

// Type aliases re-exported for experimental plugin authors.
type (
	AgentCompactionRequest   = services.AgentCompactionRequest
	AgentController          = services.AgentController
	AgentOperationError      = services.AgentOperationError
	AgentOperationResponse   = services.AgentOperationResponse
	AgentPromptRequest       = services.AgentPromptRequest
	AgentPromptResult        = services.AgentPromptResult
	AgentQueueResponse       = services.AgentQueueResponse
	PresentationSelectItem   = services.PresentationSelectItem
	PresentationUI           = services.PresentationUI
	SlashCommandCompletion   = services.SlashCommandCompletion
	SlashCommandContribution = services.SlashCommandContribution
	SlashCommandRunResult    = services.SlashCommandRunResult
	SlashCommands            = services.SlashCommands
)
