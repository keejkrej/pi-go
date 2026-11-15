// Ported from packages/coding-agent/src/cli/experimental/commands/client.ts (pi v1.0.0).

package experimentalcli

import "strings"

// ClientCommand holds parsed flags and arguments for `experimental client`.
type ClientCommand struct {
	Command        string            `json:"command"`
	Auth           *AuthInput        `json:"auth,omitzero"`
	Connect        *TransportAddress `json:"connect,omitzero"`
	SessionId      *string           `json:"sessionId,omitzero"`
	Continue       bool              `json:"continue,omitzero"`
	Resume         bool              `json:"resume,omitzero"`
	Provider       *string           `json:"provider,omitzero"`
	Model          *string           `json:"model,omitzero"`
	PluginPackages []string          `json:"pluginPackages,omitzero"`
	Prompt         *string           `json:"prompt,omitzero"`
}

// CommandName implements NamedCommandInvocation.
func (c ClientCommand) CommandName() string {
	return c.Command
}

// ClientCommandContext runs a parsed ClientCommand.
type ClientCommandContext interface {
	RunClient(command ClientCommand) error
}

var (
	ccSessionIdOption     = StringOption("--session-id")
	ccContinueOption      = FlagOption("--continue")
	ccContinueShortOption = FlagOption("-c")
	ccResumeOption        = FlagOption("--resume")
	ccResumeShortOption   = FlagOption("-r")
	ccProviderOption      = StringOption("--provider")
	ccModelOption         = StringOption("--model")
	ccPluginPackageOption = StringOption("-e", &CommandOptionConfig{Repeatable: true})
)

// ClientCommandDef defines the `client` experimental CLI command.
var ClientCommandDef = NewCommand[ClientCommand, ClientCommandContext, ClientCommand]("client").
	Option(ConnectOption).
	Option(ccSessionIdOption).
	Option(ccContinueOption).
	Option(ccContinueShortOption).
	Option(ccResumeOption).
	Option(ccResumeShortOption).
	Option(ccProviderOption).
	Option(ccModelOption).
	Option(ccPluginPackageOption).
	Option(AuthTokenOption).
	Option(AuthTokenFileOption).
	Build(func(input *ParsedCommandInput) CommandBuildResult[ClientCommand] {
		auth, authErrors := ParseAuth(input)
		connectVal, hasConnect := OptionValue(input, ConnectOption)
		sessionId, hasSessionId := OptionValue(input, ccSessionIdOption)
		contLong, _ := OptionValue(input, ccContinueOption)
		contShort, _ := OptionValue(input, ccContinueShortOption)
		shouldContinue := contLong || contShort
		resLong, _ := OptionValue(input, ccResumeOption)
		resShort, _ := OptionValue(input, ccResumeShortOption)
		shouldResume := resLong || resShort
		provider, hasProvider := OptionValue(input, ccProviderOption)
		model, hasModel := OptionValue(input, ccModelOption)
		pluginPackages := OptionValues(input, ccPluginPackageOption)

		promptArgs := input.RemainingArgs
		if len(input.RemainingArgs) > 0 && input.RemainingArgs[0] == "--" {
			promptArgs = input.RemainingArgs[1:]
		}
		var prompt *string
		if len(promptArgs) == 1 &&
			(input.RemainingArgs[0] == "--" || !strings.HasPrefix(promptArgs[0], "-")) &&
			len(promptArgs[0]) > 0 {
			p := promptArgs[0]
			prompt = &p
		}

		var modelErrors []string
		if hasProvider && !hasModel {
			modelErrors = append(modelErrors, "--provider requires --model")
		}
		selectionCount := 0
		if hasSessionId {
			selectionCount++
		}
		if shouldContinue {
			selectionCount++
		}
		if shouldResume {
			selectionCount++
		}
		var sessionSelectionErrors []string
		if selectionCount > 1 {
			sessionSelectionErrors = append(sessionSelectionErrors, "--session-id, --continue, and --resume are mutually exclusive")
		}
		var unsupportedErrors []string
		if len(input.RemainingArgs) > 0 && prompt == nil {
			unsupportedErrors = UnsupportedOptions("client", input)
		}

		errors := make([]string, 0, len(authErrors)+len(modelErrors)+len(sessionSelectionErrors)+len(unsupportedErrors))
		errors = append(errors, authErrors...)
		errors = append(errors, modelErrors...)
		errors = append(errors, sessionSelectionErrors...)
		errors = append(errors, unsupportedErrors...)
		if len(errors) > 0 {
			return CommandBuildResult[ClientCommand]{Ok: false, Errors: errors}
		}

		cmd := ClientCommand{
			Command:  "client",
			Auth:     auth,
			Continue: shouldContinue,
			Resume:   shouldResume,
			Prompt:   prompt,
		}
		if hasConnect {
			c := connectVal
			cmd.Connect = &c
		}
		if hasSessionId {
			id := sessionId
			cmd.SessionId = &id
		}
		if hasProvider {
			p := provider
			cmd.Provider = &p
		}
		if hasModel {
			m := model
			cmd.Model = &m
		}
		if len(pluginPackages) > 0 {
			cmd.PluginPackages = pluginPackages
		}
		return CommandBuildResult[ClientCommand]{Ok: true, Command: cmd}
	}).
	Action(func(command ClientCommand, context ClientCommandContext) error {
		return context.RunClient(command)
	})
