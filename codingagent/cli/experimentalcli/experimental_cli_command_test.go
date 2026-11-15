// Ported from packages/coding-agent/test/experimental-cli-command.test.ts (pi v1.0.0).

package experimentalcli_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/protocol"
)

const (
	teccUnsupportedServerOptions = "The experimental server command does not support existing CLI options yet"
	teccUnsupportedClientOptions = "The experimental client command does not support existing CLI options yet"
)

func teccPtr[T any](v T) *T {
	return &v
}

func TestExperimentalCliCommands(t *testing.T) {
	t.Run("parses server configuration", func(t *testing.T) {
		got := experimentalcli.Cli.Parse([]string{
			"server",
			"--server-id",
			"00000000-0000-4000-8000-000000000001",
			"--session-dir",
			"~/pi-sessions",
			"--provider",
			"anthropic",
			"--model",
			"claude-sonnet-4-5",
			"-e",
			"./first-plugin",
			"-e=./second-plugin",
		})
		want := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok: true,
			Command: experimentalcli.ServerCommand{
				Command:        "server",
				ServerId:       teccPtr(protocol.ServerId("00000000-0000-4000-8000-000000000001")),
				SessionDir:     teccPtr("~/pi-sessions"),
				Provider:       teccPtr("anthropic"),
				Model:          teccPtr("claude-sonnet-4-5"),
				PluginPackages: []string{"./first-plugin", "./second-plugin"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("parses provider-qualified server models", func(t *testing.T) {
		got := experimentalcli.Cli.Parse([]string{"server", "--model", "anthropic/claude-sonnet-4-5:high"})
		want := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok: true,
			Command: experimentalcli.ServerCommand{
				Command: "server",
				Model:   teccPtr("anthropic/claude-sonnet-4-5:high"),
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("parses client transport addresses", func(t *testing.T) {
		gotUnix := experimentalcli.Cli.Parse([]string{"client", "--connect", "unix:///tmp/pi.sock"})
		wantUnix := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok: true,
			Command: experimentalcli.ClientCommand{
				Command: "client",
				Connect: &experimentalcli.TransportAddress{
					Transport: experimentalcli.TransportTypeUnix,
					Path:      "/tmp/pi.sock",
				},
			},
		}
		if !reflect.DeepEqual(gotUnix, wantUnix) {
			t.Fatalf("unix: got %#v, want %#v", gotUnix, wantUnix)
		}

		gotRadius := experimentalcli.Cli.Parse([]string{
			"client",
			"--connect",
			"radius://00000000-0000-4000-8000-000000000001",
			"--session-id",
			"demo-1",
		})
		wantRadius := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok: true,
			Command: experimentalcli.ClientCommand{
				Command: "client",
				Connect: &experimentalcli.TransportAddress{
					Transport: experimentalcli.TransportTypeRadius,
					ServerId:  "00000000-0000-4000-8000-000000000001",
				},
				SessionId: teccPtr("demo-1"),
			},
		}
		if !reflect.DeepEqual(gotRadius, wantRadius) {
			t.Fatalf("radius: got %#v, want %#v", gotRadius, wantRadius)
		}
	})

	for _, tc := range []struct {
		option string
		want   experimentalcli.ClientCommand
	}{
		{option: "-c", want: experimentalcli.ClientCommand{Command: "client", Continue: true}},
		{option: "--continue", want: experimentalcli.ClientCommand{Command: "client", Continue: true}},
		{option: "-r", want: experimentalcli.ClientCommand{Command: "client", Resume: true}},
		{option: "--resume", want: experimentalcli.ClientCommand{Command: "client", Resume: true}},
	} {
		t.Run("parses client Session selection "+tc.option, func(t *testing.T) {
			got := experimentalcli.Cli.Parse([]string{"client", tc.option})
			want := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
				Ok:      true,
				Command: tc.want,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}

	t.Run("parses a Session selection followed by a one-shot prompt", func(t *testing.T) {
		got := experimentalcli.Cli.Parse([]string{"client", "-r", "Explain this project"})
		want := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok: true,
			Command: experimentalcli.ClientCommand{
				Command: "client",
				Resume:  true,
				Prompt:  teccPtr("Explain this project"),
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("parses client model and plugin configuration", func(t *testing.T) {
		got := experimentalcli.Cli.Parse([]string{
			"client",
			"--model",
			"anthropic/claude-sonnet-4-5:high",
			"-e",
			"./first-plugin",
			"-e",
			"./second-plugin",
		})
		want := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok: true,
			Command: experimentalcli.ClientCommand{
				Command:        "client",
				Model:          teccPtr("anthropic/claude-sonnet-4-5:high"),
				PluginPackages: []string{"./first-plugin", "./second-plugin"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	for _, tc := range []struct {
		name string
		argv []string
		auth *experimentalcli.AuthInput
	}{
		{
			name: "token",
			argv: []string{"--auth-token", "secret"},
			auth: &experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeToken, Token: "secret"},
		},
		{
			name: "file",
			argv: []string{"--auth-token-file", "/tmp/token"},
			auth: &experimentalcli.AuthInput{Type: experimentalcli.AuthInputTypeFile, Path: "/tmp/token"},
		},
	} {
		t.Run("parses authentication source "+tc.name, func(t *testing.T) {
			for _, command := range []string{"server", "client"} {
				argv := append([]string{command}, tc.argv...)
				got := experimentalcli.Cli.Parse(argv)
				if !got.Ok {
					t.Fatalf("expected ok for %v, got errors %v", argv, got.Errors)
				}
				switch cmd := got.Command.(type) {
				case experimentalcli.ServerCommand:
					if !reflect.DeepEqual(cmd.Auth, tc.auth) {
						t.Fatalf("server auth: got %#v, want %#v", cmd.Auth, tc.auth)
					}
				case experimentalcli.ClientCommand:
					if !reflect.DeepEqual(cmd.Auth, tc.auth) {
						t.Fatalf("client auth: got %#v, want %#v", cmd.Auth, tc.auth)
					}
				default:
					t.Fatalf("unexpected command type %T", got.Command)
				}
			}
		})
	}

	for _, command := range []string{"server", "client"} {
		t.Run("permits omitted authentication for "+command, func(t *testing.T) {
			got := experimentalcli.Cli.Parse([]string{command})
			if !got.Ok || got.Command.CommandName() != command {
				t.Fatalf("got %#v", got)
			}
		})
	}

	invalidCases := []struct {
		argv []string
		err  string
	}{
		{[]string{"client", "--listen", "unix:///tmp/pi.sock"}, teccUnsupportedClientOptions},
		{[]string{"server", "--listen", "unix:///tmp/pi.sock"}, teccUnsupportedServerOptions},
		{[]string{"server", "--connect", "unix:///tmp/pi.sock"}, teccUnsupportedServerOptions},
		{[]string{"client", "--connect", "ws://localhost:8080"}, `Unsupported --connect transport "ws:"`},
		{[]string{"client", "--connect", "radius://not-a-server"}, "Radius transport address requires"},
		{[]string{"client", "--connect", "unix://relative.sock"}, "Unix transport address must not include an authority"},
		{[]string{"client", "--connect", "unix:///tmp/pi.sock?wrong=value"}, "Invalid --connect address"},
		{[]string{"client", "--provider", "anthropic"}, "--provider requires --model"},
		{[]string{"client", "-c", "-r"}, "--session-id, --continue, and --resume are mutually exclusive"},
		{[]string{"client", "--continue=true"}, "--continue does not take a value"},
		{[]string{"server", "--provider", "anthropic"}, "--provider requires --model"},
		{[]string{"server", "--server-id", "not-a-uuid"}, "Invalid --server-id"},
		{[]string{"server", "--server-id"}, "--server-id requires a value"},
		{[]string{"server", "--session-dir"}, "--session-dir requires a value"},
		{[]string{"client", "-e"}, "-e requires a value"},
		{[]string{"server", "--session-dir", "/tmp/first", "--session-dir=/tmp/second"}, "--session-dir may only be specified once"},
		{[]string{"client", "--connect="}, "--connect requires a value"},
	}

	for _, tc := range invalidCases {
		t.Run("rejects invalid experimental input "+strings.Join(tc.argv, " "), func(t *testing.T) {
			result := experimentalcli.Cli.Parse(tc.argv)
			if result.Ok {
				t.Fatalf("expected parse failure for %v, got %#v", tc.argv, result)
			}
			matched := false
			for _, msg := range result.Errors {
				if strings.Contains(msg, tc.err) {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("expected error containing %q, got %v", tc.err, result.Errors)
			}
		})
	}

	t.Run("rejects unsupported options without parsing them through the stable CLI", func(t *testing.T) {
		got := experimentalcli.Cli.Parse([]string{"client", "--tui-mode", "wrong", "--model", "claude-sonnet"})
		want := experimentalcli.CommandParseResult[experimentalcli.NamedCommandInvocation]{
			Ok:     false,
			Errors: []string{teccUnsupportedClientOptions},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})
}
