// Ported from packages/coding-agent/test/experimental-cli-resolution.test.ts (pi v1.0.0).

package experimentalcli_test

import (
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/protocol"
)

type tecrMockCliContext struct {
	serverCalls []experimentalcli.ServerCommand
	clientCalls []experimentalcli.ClientCommand
}

func (m *tecrMockCliContext) RunServer(command experimentalcli.ServerCommand) error {
	m.serverCalls = append(m.serverCalls, command)
	return nil
}

func (m *tecrMockCliContext) RunClient(command experimentalcli.ClientCommand) error {
	m.clientCalls = append(m.clientCalls, command)
	return nil
}

func TestExperimentalCliResolution(t *testing.T) {
	t.Run("requires an experimental subcommand", func(t *testing.T) {
		got := experimentalcli.Cli.Parse(nil)
		want := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok:     false,
			Errors: []string{"Expected experimental command: server or client"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("passes server options to the command action", func(t *testing.T) {
		ctx := &tecrMockCliContext{}
		serverId := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		sessionDir := "./sessions"
		provider := "anthropic"
		model := "claude-sonnet-4-5"
		result, err := experimentalcli.Cli.Execute([]string{
			"server",
			"--server-id",
			string(serverId),
			"--session-dir",
			sessionDir,
			"--provider",
			provider,
			"--model",
			model,
		}, ctx)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		wantCommand := experimentalcli.ServerCommand{
			Command:    "server",
			ServerId:   &serverId,
			SessionDir: &sessionDir,
			Provider:   &provider,
			Model:      &model,
		}
		want := experimentalcli.CommandExecutionResult[experimentalcli.NamedCommandInvocation]{
			Ok:      true,
			Command: wantCommand,
		}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("got %#v, want %#v", result, want)
		}
		if len(ctx.serverCalls) != 1 || !reflect.DeepEqual(ctx.serverCalls[0], wantCommand) {
			t.Fatalf("unexpected server calls: %#v", ctx.serverCalls)
		}
	})

	for _, name := range []string{"server", "client"} {
		t.Run("executes the parsed "+name+" command", func(t *testing.T) {
			ctx := &tecrMockCliContext{}
			result, err := experimentalcli.Cli.Execute([]string{name}, ctx)
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if !result.Ok || result.Command.CommandName() != name {
				t.Fatalf("got %#v", result)
			}
			wantServer := 0
			wantClient := 0
			if name == "server" {
				wantServer = 1
			} else {
				wantClient = 1
			}
			if len(ctx.serverCalls) != wantServer || len(ctx.clientCalls) != wantClient {
				t.Fatalf("call count mismatch: server=%d (want %d), client=%d (want %d)",
					len(ctx.serverCalls), wantServer, len(ctx.clientCalls), wantClient)
			}
		})
	}
}
