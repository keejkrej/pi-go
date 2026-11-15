// Ported from packages/coding-agent/src/cli/experimental/commands/server.ts (pi v1.0.0).

package experimentalcli

import (
	"fmt"

	"github.com/keejkrej/pi-go/protocol"
)

// ServerCommand holds parsed flags and arguments for `experimental server`.
type ServerCommand struct {
	Command        string             `json:"command"`
	Auth           *AuthInput         `json:"auth,omitzero"`
	Provider       *string            `json:"provider,omitzero"`
	Model          *string            `json:"model,omitzero"`
	PluginPackages []string           `json:"pluginPackages,omitzero"`
	ServerId       *protocol.ServerId `json:"serverId,omitzero"`
	SessionDir     *string            `json:"sessionDir,omitzero"`
}

// CommandName implements NamedCommandInvocation.
func (c ServerCommand) CommandName() string {
	return c.Command
}

// ServerCommandContext runs a parsed ServerCommand.
type ServerCommandContext interface {
	RunServer(command ServerCommand) error
}

var (
	csServerIdOption = ValueOption("--server-id", func(value string) CommandOptionParseResult[protocol.ServerId] {
		if protocol.IsServerId(value) {
			return CommandOptionParseResult[protocol.ServerId]{Ok: true, Value: protocol.ServerId(value)}
		}
		return CommandOptionParseResult[protocol.ServerId]{
			Ok:    false,
			Error: fmt.Sprintf(`Invalid --server-id "%s"; expected a lowercase UUIDv4`, value),
		}
	})
	csSessionDirOption    = StringOption("--session-dir")
	csProviderOption      = StringOption("--provider")
	csModelOption         = StringOption("--model")
	csPluginPackageOption = StringOption("-e", &CommandOptionConfig{Repeatable: true})
)

// ServerCommandDef defines the `server` experimental CLI command.
var ServerCommandDef = NewCommand[ServerCommand, ServerCommandContext, ServerCommand]("server").
	Option(csServerIdOption).
	Option(csSessionDirOption).
	Option(csProviderOption).
	Option(csModelOption).
	Option(csPluginPackageOption).
	Option(AuthTokenOption).
	Option(AuthTokenFileOption).
	Build(func(input *ParsedCommandInput) CommandBuildResult[ServerCommand] {
		auth, authErrors := ParseAuth(input)
		serverId, hasServerId := OptionValue(input, csServerIdOption)
		sessionDir, hasSessionDir := OptionValue(input, csSessionDirOption)
		provider, hasProvider := OptionValue(input, csProviderOption)
		model, hasModel := OptionValue(input, csModelOption)
		pluginPackages := OptionValues(input, csPluginPackageOption)
		var modelErrors []string
		if hasProvider && !hasModel {
			modelErrors = append(modelErrors, "--provider requires --model")
		}
		unsupportedErrors := UnsupportedOptions("server", input)
		errors := make([]string, 0, len(authErrors)+len(modelErrors)+len(unsupportedErrors))
		errors = append(errors, authErrors...)
		errors = append(errors, modelErrors...)
		errors = append(errors, unsupportedErrors...)
		if len(errors) > 0 {
			return CommandBuildResult[ServerCommand]{Ok: false, Errors: errors}
		}

		cmd := ServerCommand{
			Command: "server",
			Auth:    auth,
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
		if hasServerId {
			id := serverId
			cmd.ServerId = &id
		}
		if hasSessionDir {
			dir := sessionDir
			cmd.SessionDir = &dir
		}
		return CommandBuildResult[ServerCommand]{Ok: true, Command: cmd}
	}).
	Action(func(command ServerCommand, context ServerCommandContext) error {
		return context.RunServer(command)
	})
