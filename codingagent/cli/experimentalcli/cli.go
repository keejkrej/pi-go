// Ported from packages/coding-agent/src/cli/experimental/cli.ts (pi v1.0.0).

package experimentalcli

// ExperimentalCommandGroup is the placeholder invocation for the bare experimental command.
type ExperimentalCommandGroup struct {
	Command string `json:"command"`
}

// CommandName implements NamedCommandInvocation.
func (g ExperimentalCommandGroup) CommandName() string {
	return g.Command
}

// CliContext combines ServerCommandContext and ClientCommandContext.
type CliContext interface {
	ServerCommandContext
	ClientCommandContext
}

var cliExperimentalCommand = NewCommand[ExperimentalCommandGroup, CliContext, NamedCommandInvocation]("experimental").
	Build(func(_ *ParsedCommandInput) CommandBuildResult[ExperimentalCommandGroup] {
		return CommandBuildResult[ExperimentalCommandGroup]{
			Ok:     false,
			Errors: []string{"Expected experimental command: server or client"},
		}
	})

// Cli is the root experimental CLI command parser and executor.
var Cli = cliExperimentalCommand.
	AddSubcommand(ServerCommandDef).
	AddSubcommand(ClientCommandDef)
