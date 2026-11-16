// Ported from packages/coding-agent/src/cli/experimental/command-options.ts (pi v1.0.0).

package experimentalcli

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/keejkrej/pi-go/protocol"
)

// AuthInputType selects how authentication credentials are supplied.
type AuthInputType string

const (
	AuthInputTypeToken AuthInputType = "token"
	AuthInputTypeFile  AuthInputType = "file"
)

// AuthInput describes an inline bearer token or token file path.
type AuthInput struct {
	Type  AuthInputType `json:"type"`
	Token string        `json:"token,omitzero"`
	Path  string        `json:"path,omitzero"`
}

// TransportType identifies the remote client transport scheme.
type TransportType string

const (
	TransportTypeUnix   TransportType = "unix"
	TransportTypeRadius TransportType = "radius"
)

// TransportAddress describes a parsed unix or radius connection target.
type TransportAddress struct {
	Transport TransportType     `json:"transport"`
	Path      string            `json:"path,omitzero"`
	ServerId  protocol.ServerId `json:"serverId,omitzero"`
}

var (
	// AuthTokenOption parses --auth-token <token>.
	AuthTokenOption = StringOption("--auth-token")
	// AuthTokenFileOption parses --auth-token-file <path>.
	AuthTokenFileOption = StringOption("--auth-token-file")
	// ConnectOption parses --connect <unix:///...|radius://<uuid>>.
	ConnectOption = ValueOption("--connect", func(value string) CommandOptionParseResult[TransportAddress] {
		address, errMsg := coParseTransportAddress(value)
		if errMsg != "" {
			return CommandOptionParseResult[TransportAddress]{Ok: false, Error: errMsg}
		}
		return CommandOptionParseResult[TransportAddress]{Ok: true, Value: address}
	})
)

func coParseAuthInput(authToken string, hasAuthToken bool, authTokenFile string, hasAuthTokenFile bool) (*AuthInput, []string) {
	if hasAuthToken && hasAuthTokenFile {
		return nil, []string{"--auth-token and --auth-token-file are mutually exclusive"}
	}
	if hasAuthToken {
		return &AuthInput{Type: AuthInputTypeToken, Token: authToken}, nil
	}
	if hasAuthTokenFile {
		return &AuthInput{Type: AuthInputTypeFile, Path: authTokenFile}, nil
	}
	return nil, nil
}

func coParseTransportAddress(value string) (TransportAddress, string) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" {
		return TransportAddress{}, fmt.Sprintf(`Invalid --connect address "%s"`, value)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "radius" {
		if u.User != nil ||
			u.Host != u.Hostname() ||
			(u.Path != "" && u.Path != "/") ||
			u.RawQuery != "" ||
			u.Fragment != "" ||
			strings.ContainsAny(value, "?#") ||
			value != "radius://"+u.Hostname()+u.Path {
			return TransportAddress{}, fmt.Sprintf(`Invalid --connect address "%s"`, value)
		}
		serverId := u.Hostname()
		if !protocol.IsServerId(serverId) {
			return TransportAddress{}, "Radius transport address requires a lowercase UUIDv4 server ID"
		}
		return TransportAddress{
			Transport: TransportTypeRadius,
			ServerId:  protocol.ServerId(serverId),
		}, ""
	}
	if scheme != "unix" {
		return TransportAddress{}, fmt.Sprintf(`Unsupported --connect transport "%s:"`, scheme)
	}
	if u.Host != "" || u.User != nil {
		return TransportAddress{}, "Unix transport address must not include an authority"
	}
	if !strings.HasPrefix(value, "unix:///") ||
		strings.HasPrefix(value, "unix:////") ||
		strings.Contains(value, "?") ||
		strings.Contains(value, "#") ||
		strings.Contains(value, "\\") ||
		strings.Contains(u.Path, "//") ||
		u.String() != value {
		return TransportAddress{}, fmt.Sprintf(`Invalid --connect address "%s"`, value)
	}
	if strings.Contains(u.Path, "\x00") {
		return TransportAddress{}, fmt.Sprintf(`Invalid --connect address "%s"`, value)
	}
	if !path.IsAbs(u.Path) {
		return TransportAddress{}, "Unix transport address requires an absolute path"
	}
	return TransportAddress{
		Transport: TransportTypeUnix,
		Path:      u.Path,
	}, ""
}

// ParseAuth extracts and validates authentication options from parsed CLI input.
func ParseAuth(input *ParsedCommandInput) (*AuthInput, []string) {
	token, hasToken := OptionValue(input, AuthTokenOption)
	tokenFile, hasTokenFile := OptionValue(input, AuthTokenFileOption)
	return coParseAuthInput(token, hasToken, tokenFile, hasTokenFile)
}

// UnsupportedOptions reports an error when unrecognized CLI arguments remain.
func UnsupportedOptions(command string, input *ParsedCommandInput) []string {
	if input == nil || len(input.RemainingArgs) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("The experimental %s command does not support existing CLI options yet", command)}
}
