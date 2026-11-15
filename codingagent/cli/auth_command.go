// Ported from packages/coding-agent/src/cli/auth-command.ts (pi v1.5.2).

package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

// AuthCommandKind identifies non-interactive auth inspection/credential subcommands.
type AuthCommandKind string

const (
	AuthCommandKindCheck       AuthCommandKind = "check"
	AuthCommandKindApiKey      AuthCommandKind = "api_key"
	AuthCommandKindBearerToken AuthCommandKind = "bearer_token"
)

// AuthCommand holds the parsed `pi auth <check|print-api-key|print-bearer-token>` options.
type AuthCommand struct {
	Kind        AuthCommandKind
	Args        []string
	Json        bool
	Credentials bool
	NoRefresh   bool
	MinExpiryMs *int64
}

// AuthCommandError reports an invalid auth CLI invocation or resolution failure.
type AuthCommandError struct {
	Message string
}

func (e *AuthCommandError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

var (
	acCommandUsage = map[AuthCommandKind]string{
		AuthCommandKindCheck:       utils.AppName + " auth check --provider <provider> [--json] [--credentials] [--no-refresh]",
		AuthCommandKindApiKey:      utils.AppName + " auth print-api-key --provider <provider> [--model <model>]",
		AuthCommandKindBearerToken: utils.AppName + " auth print-bearer-token --provider <provider> [--model <model>] [--min-expiry <duration>]",
	}
	acMinExpiryRe = regexp.MustCompile(`(?i)^(\d+)(ms|s|m|h)$`)
	acBearerRe    = regexp.MustCompile(`(?i)^Bearer\s+(.+)$`)
)

// GetAuthCommandName returns the CLI command display string for kind.
func GetAuthCommandName(kind AuthCommandKind) string {
	switch kind {
	case AuthCommandKindCheck:
		return "auth check"
	case AuthCommandKindApiKey:
		return "auth print-api-key"
	default:
		return "auth print-bearer-token"
	}
}

// GetAuthCommandUsage returns the CLI usage string for kind.
func GetAuthCommandUsage(kind AuthCommandKind) string {
	return acCommandUsage[kind]
}

// IsAuthCommandHelp reports whether args invoke `auth` help.
func IsAuthCommandHelp(args []string) bool {
	if len(args) == 0 || args[0] != "auth" {
		return false
	}
	return len(args) == 1 || args[1] == "help" || slices.Contains(args, "--help") || slices.Contains(args, "-h")
}

// PrintAuthCommandHelp writes `pi auth` usage to stdout.
func PrintAuthCommandHelp() {
	PrintAuthCommandHelpTo(os.Stdout)
}

// PrintAuthCommandHelpTo writes `pi auth` usage to w.
func PrintAuthCommandHelpTo(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	acPrintAuthCommandHelpTo(w)
}

func acPrintAuthCommandHelpTo(w io.Writer) {
	_, _ = fmt.Fprintln(w, `Usage:
  pi auth login <provider> [--api-key] [--no-browser]
  pi auth logout <provider>
  pi auth print-api-key [--provider <provider>] [--model <model>]
  pi auth print-bearer-token [--provider <provider>] [--model <model>] [--min-expiry <duration>]
  pi auth check [--provider <provider>] [--model <model>] [--json] [--credentials] [--no-refresh]

Log in to and out of model providers, print their credentials for external clients, or check that they are ready.
Credentials are stored in ~/.pi/agent/auth.json.

Commands:
  login <provider>        Sign in with an account in the browser, or save an API key, then exit
  logout <provider>       Remove the stored credentials; environment variables and models.json stay
  print-api-key           Print the resolved API key
  print-bearer-token      Print a resolved OAuth bearer token (refreshes if expired)
  check                   Print ready, not_ready, or invalid; exit with 0, 1, or 2

Options for print-api-key, print-bearer-token, and check (at least one is required):
  --provider <provider>   Resolve credentials for a provider
  --model <model>         Resolve credentials from a model

Options for login:
  --api-key               Save an API key instead of signing in with an account
  --no-browser            Do not open a browser; use another sign-in method, e.g. device code

Other options:
  --min-expiry <duration> Minimum remaining token lifetime for print-bearer-token, e.g. 30m or 1h
  --json                  Print the check result as JSON
  --credentials           Emit the credential from check, or include it in JSON output
  --no-refresh            Do not refresh expired OAuth credentials during check`)
}

// ParseAuthCommand parses `auth check|print-api-key|print-bearer-token` subcommands.
// Returns (nil, nil) when args do not start with "auth".
func ParseAuthCommand(args []string) (*AuthCommand, error) {
	if len(args) == 0 || args[0] != "auth" {
		return nil, nil
	}

	sub := ""
	if len(args) > 1 {
		sub = args[1]
	}

	var kind AuthCommandKind
	switch sub {
	case "check":
		kind = AuthCommandKindCheck
	case "print-api-key":
		kind = AuthCommandKindApiKey
	case "print-bearer-token":
		kind = AuthCommandKindBearerToken
	default:
		return nil, &AuthCommandError{
			Message: fmt.Sprintf(
				`Unknown auth command "%s". Use "%s auth login", "%s auth logout", "%s auth print-api-key", "%s auth print-bearer-token", or "%s auth check".`,
				sub, utils.AppName, utils.AppName, utils.AppName, utils.AppName, utils.AppName,
			),
		}
	}

	commandArgs := make([]string, 0)
	jsonOut := false
	credentials := false
	noRefresh := false
	var minExpiryMs *int64

	for index := 2; index < len(args); index++ {
		arg := args[index]
		if arg == "--min-expiry" {
			if kind != AuthCommandKindBearerToken {
				return nil, &AuthCommandError{Message: "--min-expiry is only supported by print-bearer-token"}
			}
			index++
			var value string
			if index < len(args) {
				value = args[index]
			}
			ms, ok := acParseDurationMs(value)
			if !ok {
				return nil, &AuthCommandError{Message: "--min-expiry must use a duration such as 30m or 1h"}
			}
			minExpiryMs = &ms
			continue
		}
		if arg == "--json" || arg == "--credentials" || arg == "--no-refresh" {
			if kind != AuthCommandKindCheck {
				return nil, &AuthCommandError{Message: fmt.Sprintf("%s is only supported by auth check", arg)}
			}
			switch arg {
			case "--json":
				jsonOut = true
			case "--credentials":
				credentials = true
			case "--no-refresh":
				noRefresh = true
			}
			continue
		}
		commandArgs = append(commandArgs, arg)
	}

	return &AuthCommand{
		Kind:        kind,
		Args:        commandArgs,
		Json:        jsonOut,
		Credentials: credentials,
		NoRefresh:   noRefresh,
		MinExpiryMs: minExpiryMs,
	}, nil
}

func acParseDurationMs(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	match := acMinExpiryRe.FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}
	amount, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(match[2]) {
	case "ms":
		return amount, true
	case "s":
		return amount * 1_000, true
	case "m":
		return amount * 60_000, true
	default:
		return amount * 3_600_000, true
	}
}

// ValidateAuthCommandArgs ensures an auth command specifies --provider or --model cleanly.
func ValidateAuthCommandArgs(args core.Args, kind AuthCommandKind) (provider *string, model *string, err error) {
	if args.Provider != nil {
		trimmed := strings.TrimSpace(*args.Provider)
		if trimmed != "" {
			provider = &trimmed
		}
	}
	if args.Model != nil {
		trimmed := strings.TrimSpace(*args.Model)
		if trimmed != "" {
			model = &trimmed
		}
	}
	if args.UnknownFlags != nil && args.UnknownFlags.Len() > 0 {
		for _, option := range args.UnknownFlags.Keys() {
			return nil, nil, &AuthCommandError{
				Message: fmt.Sprintf(`Unknown option --%s for "%s".`, option, GetAuthCommandName(kind)),
			}
		}
	}
	if args.ApiKey != nil || len(args.Messages) > 0 || len(args.FileArgs) > 0 {
		return nil, nil, &AuthCommandError{Message: "Auth commands only accept --provider and --model"}
	}
	if provider == nil && model == nil {
		if kind == AuthCommandKindCheck {
			return nil, nil, &AuthCommandError{Message: "Auth checks require --provider <provider> or --model <model>"}
		}
		return nil, nil, &AuthCommandError{Message: "Credential printing requires --provider <provider> or --model <model>"}
	}
	return provider, model, nil
}

// GetAuthCredential extracts the API key or Authorization Bearer token from auth.
func GetAuthCredential(auth *ai.AuthResult) *string {
	if auth == nil {
		return nil
	}
	if auth.Auth.ApiKey != nil && *auth.Auth.ApiKey != "" {
		val := *auth.Auth.ApiKey
		return &val
	}
	if auth.Auth.Headers != nil {
		for name, value := range auth.Auth.Headers.All() {
			if strings.EqualFold(name, "authorization") && value != nil {
				if match := acBearerRe.FindStringSubmatch(*value); match != nil {
					token := match[1]
					return &token
				}
			}
		}
	}
	return nil
}
