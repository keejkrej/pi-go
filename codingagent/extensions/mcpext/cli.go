// Ported from packages/coding-agent/src/extensions/mcp/cli.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/ansi"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
)

func cliBuildHelp() string {
	return ansi.Bold("Usage:") + `
  ` + utils.AppName + ` mcp add <server> [options] -- <command> [args...]
  ` + utils.AppName + ` mcp add <server> [options] --url <url>
  ` + utils.AppName + ` mcp remove <server> [-l]
  ` + utils.AppName + ` mcp list [--json]
  ` + utils.AppName + ` mcp login <server> [-l] [--timeout <seconds>]
  ` + utils.AppName + ` mcp logout <server> [-l]

Configure and check MCP servers and sign in to OAuth servers without starting a session.
Reads ~/` + utils.ConfigDirName + `/agent/mcp.json and, in trusted projects, ` + utils.ConfigDirName + `/mcp.json.

Commands:
  add <server>            Add or replace a server in mcp.json
  remove <server>         Remove a server from mcp.json
  list                    Show state, tools, and errors (exits 1 on failure)
  login <server>          Sign in through the browser
  logout <server>         Delete the stored OAuth credentials

Options for add, remove, login, and logout:
  -l, --local             Use ` + utils.ConfigDirName + `/mcp.json in the current project instead of the global file

Options for add:
  --url <url>             Streamable HTTP server URL (instead of a command)
  --env <KEY=VALUE>       Environment variable for a stdio server (repeatable)
  --cwd <dir>             Working directory for a stdio server
  --header <KEY=VALUE>    HTTP header (repeatable)
  --bearer-token-env-var <NAME>
                          Send "Authorization: Bearer ${NAME}"
  --oauth-client-id <id>  Pre-registered OAuth client id
  --oauth-client-secret <secret>
                          OAuth client secret (may be ${NAME} or !command)
  --oauth-callback-port <port>
                          Fixed OAuth callback port
  --oauth-client-name <name>
                          Client name sent when registering with the OAuth server
  --oauth-client-registration <mode>
                          auto (default), cimd, or dcr
  --exposure <mode>       codemode (default), deferred, direct, or hidden
  --description <text>    What the server offers, shown in the system prompt

Other options:
  --json                  Print the list as JSON
  --timeout <seconds>     Time limit for the whole sign-in (default: 300)`
}

func cliHelpHint() string {
	return ansi.Dim(fmt.Sprintf(`Use "%s mcp --help" for usage.`, utils.AppName))
}

const cliDefaultLoginTimeoutSeconds = 300

// McpCommandOptions configures RunMcpCommand.
type McpCommandOptions struct {
	Cwd           string
	AgentDir      string
	Credentials   *McpOAuthCredentialStore
	ProviderToken func(ctx context.Context, provider string) (*string, error)
	OpenUrl       func(url string)
	Log           func(line string)
	Error         func(line string)
	HTTPClient    *http.Client
	Interactive   *bool
	Input         io.Reader
	Context       context.Context
}

type cliServerReport struct {
	Name         string                    `json:"name"`
	Scope        string                    `json:"scope"`
	Source       string                    `json:"source"`
	Override     *string                   `json:"override,omitzero"`
	Enabled      bool                      `json:"enabled"`
	Exposure     McpExposure               `json:"exposure"`
	Transport    string                    `json:"transport"`
	AuthProvider *string                   `json:"authProvider,omitzero"`
	State        string                    `json:"state"`
	Tools        []string                  `json:"tools"`
	ToolExposure *omap.Map[string, string] `json:"toolExposure,omitzero"`
	Resources    bool                      `json:"resources,omitzero"`
	Error        *string                   `json:"error,omitzero"`
}

func cliFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cliIsStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func cliDescribeTransport(entry McpServerEntry) string {
	cfg := entry.Config
	if cfg.IsHttp() && cfg.Url != nil {
		return *cfg.Url
	}
	var parts []string
	if cfg.Command != nil {
		parts = append(parts, *cfg.Command)
	}
	parts = append(parts, cfg.Args...)
	return strings.Join(parts, " ")
}

func cliGetAuthProvider(entry McpServerEntry) *string {
	if entry.Config.IsHttp() && entry.Config.Auth != nil && entry.Config.Auth.Provider != "" {
		p := entry.Config.Auth.Provider
		return &p
	}
	return nil
}

func cliCreateConnection(
	entry McpServerEntry,
	options McpCommandOptions,
	credentials *McpOAuthCredentialStore,
	providerToken func(ctx context.Context, provider string) (*string, error),
) *McpServerConnection {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = ai.NodeHttpProxyDefaultClient()
	}
	return NewMcpServerConnection(McpServerConnectionOptions{
		Entry:      entry,
		Cwd:        options.Cwd,
		HTTPClient: httpClient,
		CreateTransport: func(e McpServerEntry, cwd string, authProvider mcp.AuthProvider) (mcp.McpTransport, error) {
			return CreateDefaultTransport(e, cwd, authProvider, httpClient)
		},
		Credentials:   credentials,
		ProviderToken: providerToken,
		Log:           NewMcpServerLog(filepath.Join(options.AgentDir, "mcp.log")),
	})
}

func cliCreateProviderTokenReader(agentDir string) func(ctx context.Context, provider string) (*string, error) {
	var once sync.Once
	var rt *core.ModelRuntime
	var initErr error
	return func(ctx context.Context, provider string) (*string, error) {
		once.Do(func() {
			authPath := filepath.Join(agentDir, "auth.json")
			modelsPath := filepath.Join(agentDir, "models.json")
			refreshFalse := false
			rt, initErr = core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
				AuthPath:        &authPath,
				ModelsPath:      &modelsPath,
				RefreshOnCreate: &refreshFalse,
			})
		})
		if initErr != nil || rt == nil {
			return nil, nil
		}
		res, err := rt.GetAuth(ctx, provider, nil)
		if err != nil || res == nil {
			return nil, nil
		}
		return res.Auth.ApiKey, nil
	}
}

var cliOptionAliases = map[string]string{
	"-l": "--local",
}

type cliParsedOptions struct {
	positional []string
	flags      map[string]bool
	values     map[string]string
	lists      map[string][]string
}

func cliParseOptions(
	args []string,
	known map[string]string,
	errorFn func(line string),
	maxPositionals int,
) *cliParsedOptions {
	parsed := &cliParsedOptions{
		flags:  make(map[string]bool),
		values: make(map[string]string),
		lists:  make(map[string][]string),
	}
	for index := 0; index < len(args); index++ {
		typed := args[index]
		arg := typed
		if alias, ok := cliOptionAliases[typed]; ok {
			arg = alias
		}
		if arg == "--" || (maxPositionals >= 0 && len(parsed.positional) >= maxPositionals) {
			start := index
			if arg == "--" {
				start = index + 1
			}
			parsed.positional = append(parsed.positional, args[start:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			parsed.positional = append(parsed.positional, arg)
			continue
		}
		name := arg[2:]
		kind, ok := known[name]
		if !ok {
			errorFn(fmt.Sprintf("Unknown option %s.\n%s", typed, cliHelpHint()))
			return nil
		}
		if kind == "flag" {
			parsed.flags[name] = true
			continue
		}
		index++
		if index >= len(args) {
			errorFn(fmt.Sprintf("%s needs a value.", arg))
			return nil
		}
		val := args[index]
		if kind == "list" {
			parsed.lists[name] = append(parsed.lists[name], val)
		} else {
			parsed.values[name] = val
		}
	}
	return parsed
}

// RunMcpCommand runs `pi mcp <args>` and returns the exit code.
func RunMcpCommand(args []string, options McpCommandOptions) int {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Cwd == "" {
		if cwd, err := os.Getwd(); err == nil {
			options.Cwd = cwd
		}
	}
	if options.AgentDir == "" {
		options.AgentDir = utils.GetAgentDir()
	}
	logFn := options.Log
	if logFn == nil {
		logFn = func(line string) { fmt.Println(line) }
	}
	errFn := options.Error
	if errFn == nil {
		errFn = func(line string) { fmt.Fprintln(os.Stderr, line) }
	}
	if options.HTTPClient == nil {
		options.HTTPClient = ai.NodeHttpProxyDefaultClient()
	}

	if len(args) == 0 || args[0] == "help" || slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		logFn(cliBuildHelp())
		return 0
	}
	command := args[0]
	rest := args[1:]

	projectConfig := filepath.Join(options.Cwd, utils.ConfigDirName, "mcp.json")
	if command == "add" || command == "remove" {
		if command == "add" {
			return cliAdd(rest, projectConfig, options, logFn, errFn)
		}
		return cliRemove(rest, projectConfig, options, logFn, errFn)
	}

	trustDec, trustErr := core.NewProjectTrustStore(options.AgentDir).Get(options.Cwd)
	projectTrusted := trustErr == nil && trustDec != nil && *trustDec
	loaded := LoadMcpConfig(LoadMcpConfigOptions{
		AgentDir:       options.AgentDir,
		Cwd:            options.Cwd,
		ProjectTrusted: projectTrusted,
	})
	var untrustedNote *string
	if !projectTrusted && cliFileExists(projectConfig) {
		note := fmt.Sprintf("%s is ignored because the project is not trusted. Start %s in the project to trust it.", projectConfig, utils.AppName)
		untrustedNote = &note
	}

	credentials := options.Credentials
	if credentials == nil {
		credentials = NewMcpOAuthCredentialStore()
	}
	providerToken := options.ProviderToken
	if providerToken == nil {
		providerToken = cliCreateProviderTokenReader(options.AgentDir)
	}

	switch command {
	case "list":
		parsed := cliParseOptions(rest, map[string]string{"json": "flag"}, errFn, -1)
		if parsed == nil {
			return 1
		}
		if len(parsed.positional) > 0 {
			errFn(fmt.Sprintf("Usage: %s mcp list [--json]\n%s", utils.AppName, cliHelpHint()))
			return 1
		}
		return cliList(ctx, loaded, parsed.flags["json"], untrustedNote, options, credentials, providerToken, logFn)

	case "login", "logout":
		known := map[string]string{"local": "flag"}
		if command == "login" {
			known["timeout"] = "value"
		}
		parsed := cliParseOptions(rest, known, errFn, -1)
		if parsed == nil {
			return 1
		}
		if len(parsed.positional) != 1 {
			errFn(fmt.Sprintf("Usage: %s mcp %s <server> [-l]\n%s", utils.AppName, command, cliHelpHint()))
			return 1
		}
		name := parsed.positional[0]
		var entry *McpServerEntry
		for i := range loaded.Servers {
			if loaded.Servers[i].Name == name {
				entry = &loaded.Servers[i]
				break
			}
		}
		if entry == nil {
			names := make([]string, len(loaded.Servers))
			for i, s := range loaded.Servers {
				names[i] = s.Name
			}
			var suggestion string
			for _, candidate := range names {
				if cliEditDistance(candidate, name) <= 2 {
					suggestion = candidate
					break
				}
			}
			suggText := ""
			if suggestion != "" {
				suggText = fmt.Sprintf(` Did you mean %q?`, suggestion)
			}
			noteText := ""
			if untrustedNote != nil {
				noteText = " " + *untrustedNote
			}
			configured := strings.Join(names, ", ")
			if configured == "" {
				configured = "none"
			}
			errFn(fmt.Sprintf(`No MCP server named %q.%s%s Configured: %s.`, name, suggText, noteText, configured))
			return 1
		}
		if parsed.flags["local"] && entry.Scope != "project" {
			errFn(fmt.Sprintf(`MCP server %q is defined in %s, not in the project; omit --local.`, name, entry.Source))
			return 1
		}
		if authProv := cliGetAuthProvider(*entry); authProv != nil {
			errFn(fmt.Sprintf(`MCP server %q uses the credentials of provider %q. Run "%s auth %s %s".`, name, *authProv, utils.AppName, command, *authProv))
			return 1
		}
		conn := cliCreateConnection(*entry, options, credentials, providerToken)
		defer func() {
			_ = conn.Close()
		}()
		urlPtr := conn.OAuthUrl()
		if urlPtr == nil {
			errFn(fmt.Sprintf(`MCP server %q does not use OAuth. Only HTTP servers without an Authorization header do.`, name))
			return 1
		}
		if command == "logout" {
			removed := credentials.Remove(name, *urlPtr)
			if removed {
				logFn(fmt.Sprintf(`Signed out of MCP server %q.`, name))
			} else {
				logFn(fmt.Sprintf(`No stored credentials for MCP server %q.`, name))
			}
			return 0
		}
		timeoutSec := float64(cliDefaultLoginTimeoutSeconds)
		if rawTimeout, hasTimeout := parsed.values["timeout"]; hasTimeout {
			parsedTimeout, err := strconv.ParseFloat(rawTimeout, 64)
			if err != nil || math.IsNaN(parsedTimeout) || math.IsInf(parsedTimeout, 0) || parsedTimeout <= 0 {
				errFn("--timeout must be a positive number of seconds.")
				return 1
			}
			timeoutSec = parsedTimeout
		}
		timeoutMs := time.Duration(timeoutSec * float64(time.Second))
		return cliLogin(ctx, *entry, conn, *urlPtr, timeoutMs, options, credentials, logFn, errFn)

	default:
		errFn(fmt.Sprintf("Unknown mcp command %q.\n%s", command, cliHelpHint()))
		return 1
	}
}

func cliEditDistance(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	previous := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current := make([]int, len(rb)+1)
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			best := previous[j] + 1
			if current[j-1]+1 < best {
				best = current[j-1] + 1
			}
			if previous[j-1]+cost < best {
				best = previous[j-1] + cost
			}
			current[j] = best
		}
		previous = current
	}
	return previous[len(rb)]
}

func cliParsePairs(option string, pairs []string, errorFn func(line string)) (*omap.Map[string, string], bool) {
	out := omap.NewMap[string, string]()
	for _, pair := range pairs {
		sep := strings.IndexByte(pair, '=')
		if sep <= 0 {
			errorFn(fmt.Sprintf("--%s expects KEY=VALUE, got %q.", option, pair))
			return nil, false
		}
		out.Set(pair[:sep], pair[sep+1:])
	}
	return out, true
}

func cliAdd(
	args []string,
	projectConfig string,
	options McpCommandOptions,
	logFn func(line string),
	errFn func(line string),
) int {
	usage := fmt.Sprintf("Usage: %s mcp add <server> [options] (--url <url> | -- <command> [args...])\n%s", utils.AppName, cliHelpHint())
	known := map[string]string{
		"local":                     "flag",
		"url":                       "value",
		"env":                       "list",
		"cwd":                       "value",
		"header":                    "list",
		"bearer-token-env-var":      "value",
		"oauth-client-id":           "value",
		"oauth-client-secret":       "value",
		"oauth-callback-port":       "value",
		"oauth-client-name":         "value",
		"oauth-client-registration": "value",
		"exposure":                  "value",
		"description":               "value",
	}
	parsed := cliParseOptions(args, known, errFn, 2)
	if parsed == nil {
		return 1
	}
	if len(parsed.positional) == 0 {
		errFn(usage)
		return 1
	}
	name := parsed.positional[0]
	command := parsed.positional[1:]
	rawUrl, hasUrl := parsed.values["url"]
	if name == "" || (!hasUrl) == (len(command) == 0) {
		errFn(usage)
		return 1
	}

	httpOnly := []string{
		"header",
		"bearer-token-env-var",
		"oauth-client-id",
		"oauth-client-secret",
		"oauth-callback-port",
		"oauth-client-name",
		"oauth-client-registration",
	}
	stdioOnly := []string{"env", "cwd"}
	checkList := httpOnly
	targetDesc := "HTTP servers (--url)"
	if hasUrl {
		checkList = stdioOnly
		targetDesc = "stdio servers"
	}
	for _, opt := range checkList {
		_, inVals := parsed.values[opt]
		_, inLists := parsed.lists[opt]
		if inVals || inLists {
			errFn(fmt.Sprintf("--%s only applies to %s.", opt, targetDesc))
			return 1
		}
	}

	configObj := jsonx.NewObject()
	if hasUrl {
		headers, ok := cliParsePairs("header", parsed.lists["header"], errFn)
		if !ok {
			return 1
		}
		if bearer, ok := parsed.values["bearer-token-env-var"]; ok {
			headers.Set("Authorization", fmt.Sprintf("Bearer ${%s}", bearer))
		}
		oauthObj := jsonx.NewObject()
		if v, ok := parsed.values["oauth-client-id"]; ok {
			oauthObj.Set("clientId", v)
		}
		if v, ok := parsed.values["oauth-client-secret"]; ok {
			oauthObj.Set("clientSecret", v)
		}
		if rawPort, ok := parsed.values["oauth-callback-port"]; ok {
			if n, err := strconv.ParseFloat(rawPort, 64); err == nil && !math.IsNaN(n) {
				oauthObj.Set("callbackPort", n)
			} else {
				oauthObj.Set("callbackPort", rawPort)
			}
		}
		if v, ok := parsed.values["oauth-client-name"]; ok {
			oauthObj.Set("clientName", v)
		}
		if v, ok := parsed.values["oauth-client-registration"]; ok {
			oauthObj.Set("clientRegistration", v)
		}
		configObj.Set("url", rawUrl)
		if headers.Len() > 0 {
			headersObj := jsonx.NewObject()
			for k, v := range headers.All() {
				headersObj.Set(k, v)
			}
			configObj.Set("headers", headersObj)
		}
		if oauthObj.Len() > 0 {
			configObj.Set("oauth", oauthObj)
		}
	} else {
		envMap, ok := cliParsePairs("env", parsed.lists["env"], errFn)
		if !ok {
			return 1
		}
		configObj.Set("command", command[0])
		if len(command) > 1 {
			cmdArgs := make([]any, len(command)-1)
			for i, a := range command[1:] {
				cmdArgs[i] = a
			}
			configObj.Set("args", cmdArgs)
		}
		if envMap.Len() > 0 {
			envObj := jsonx.NewObject()
			for k, v := range envMap.All() {
				envObj.Set(k, v)
			}
			configObj.Set("env", envObj)
		}
		if v, ok := parsed.values["cwd"]; ok {
			configObj.Set("cwd", v)
		}
	}
	if v, ok := parsed.values["exposure"]; ok {
		configObj.Set("exposure", v)
	}
	if v, ok := parsed.values["description"]; ok {
		configObj.Set("description", v)
	}

	validated, valErr := ValidateMcpServerConfig(name, configObj)
	if valErr != nil {
		errFn(valErr.Error())
		return 1
	}

	project := parsed.flags["local"]
	path := filepath.Join(options.AgentDir, "mcp.json")
	scope := "global"
	if project {
		path = projectConfig
		scope = "project"
	}
	replaced, addErr := AddMcpServerConfig(path, name, *validated)
	if addErr != nil {
		errFn(fmt.Sprintf("Could not update %s: %s", path, addErr.Error()))
		return 1
	}
	action := "Added"
	if replaced {
		action = "Replaced"
	}
	logFn(fmt.Sprintf(`%s %s MCP server %q in %s.`, action, scope, name, path))
	if project {
		trustDec, trustErr := core.NewProjectTrustStore(options.AgentDir).Get(options.Cwd)
		if trustErr != nil || trustDec == nil || !*trustDec {
			logFn(fmt.Sprintf("The project is not trusted, so %s is ignored until you start %s in the project and trust it.", path, utils.AppName))
		}
	}
	mayNeedSignIn := validated.IsHttp()
	if mayNeedSignIn && validated.Headers != nil {
		for k := range validated.Headers.All() {
			if strings.EqualFold(k, "authorization") {
				mayNeedSignIn = false
				break
			}
		}
	}
	hint := fmt.Sprintf("Check it with: %s mcp list", utils.AppName)
	if mayNeedSignIn {
		hint += fmt.Sprintf(". If it requires sign-in: %s mcp login %s", utils.AppName, name)
	}
	logFn(hint)
	return 0
}

func cliRemove(
	args []string,
	projectConfig string,
	options McpCommandOptions,
	logFn func(line string),
	errFn func(line string),
) int {
	parsed := cliParseOptions(args, map[string]string{"local": "flag"}, errFn, -1)
	if parsed == nil {
		return 1
	}
	if len(parsed.positional) != 1 || parsed.positional[0] == "" {
		errFn(fmt.Sprintf("Usage: %s mcp remove <server> [-l]\n%s", utils.AppName, cliHelpHint()))
		return 1
	}
	name := parsed.positional[0]
	project := parsed.flags["local"]
	globalConfig := filepath.Join(options.AgentDir, "mcp.json")
	path := globalConfig
	scope := "global"
	if project {
		path = projectConfig
		scope = "project"
	}
	removed, rmErr := RemoveMcpServerConfig(path, name)
	if rmErr != nil {
		errFn(fmt.Sprintf("Could not update %s: %s", path, rmErr.Error()))
		return 1
	}
	if removed {
		logFn(fmt.Sprintf(`Removed %s MCP server %q from %s.`, scope, name, path))
		return 0
	}
	all := LoadMcpConfig(LoadMcpConfigOptions{
		AgentDir:       options.AgentDir,
		Cwd:            options.Cwd,
		ProjectTrusted: true,
	})
	var other *McpServerEntry
	for i := range all.Servers {
		if all.Servers[i].Name == name && all.Servers[i].Scope != scope {
			other = &all.Servers[i]
			break
		}
	}
	otherHint := ""
	if other != nil {
		localFlag := "omit --local"
		if other.Scope == "project" {
			localFlag = "use --local"
		}
		otherHint = fmt.Sprintf(" It is defined in %s; %s.", other.Source, localFlag)
	}
	errFn(fmt.Sprintf(`No %s MCP server named %q in %s.%s`, scope, name, path, otherHint))
	return 1
}

func cliList(
	ctx context.Context,
	loaded LoadedMcpConfig,
	asJson bool,
	untrustedNote *string,
	options McpCommandOptions,
	credentials *McpOAuthCredentialStore,
	providerToken func(ctx context.Context, provider string) (*string, error),
	logFn func(line string),
) int {
	reports := make([]cliServerReport, len(loaded.Servers))
	var wg sync.WaitGroup
	for i, entry := range loaded.Servers {
		wg.Add(1)
		go func(idx int, ent McpServerEntry) {
			defer wg.Done()
			scope := ent.Scope
			if scope == "" {
				scope = "global"
			}
			exposure := core.McpExposureCodemode
			if ent.Config.Exposure != nil {
				exposure = *ent.Config.Exposure
			}
			enabled := ent.Config.Enabled == nil || *ent.Config.Enabled
			report := cliServerReport{
				Name:         ent.Name,
				Scope:        scope,
				Source:       ent.Source,
				Override:     ent.Override,
				Enabled:      enabled,
				Exposure:     exposure,
				Transport:    cliDescribeTransport(ent),
				AuthProvider: cliGetAuthProvider(ent),
				State:        "disabled",
				Tools:        []string{},
			}
			if !enabled {
				reports[idx] = report
				return
			}
			conn := cliCreateConnection(ent, options, credentials, providerToken)
			_, _ = conn.GetClient(ctx)
			report.State = string(conn.State())
			tools := conn.Tools()
			report.Tools = make([]string, len(tools))
			var overrides *omap.Map[string, string]
			for j, t := range tools {
				report.Tools[j] = t.Name
				toolExp := GetMcpToolExposure(ent.Config, t.Name)
				if toolExp != exposure {
					if overrides == nil {
						overrides = omap.NewMap[string, string]()
					}
					overrides.Set(t.Name, string(toolExp))
				}
			}
			report.ToolExposure = overrides
			if conn.HasResources() {
				report.Resources = true
			}
			if conn.State() != ServerStateConnected {
				report.Error = conn.Error()
			}
			_ = conn.Close()
			reports[idx] = report
		}(i, entry)
	}
	wg.Wait()

	failed := len(loaded.Errors) > 0
	if !failed {
		for _, r := range reports {
			if r.Enabled && r.State != string(ServerStateConnected) {
				failed = true
				break
			}
		}
	}

	if asJson {
		payload := jsonx.NewObject()
		payload.Set("servers", reports)
		errsOut := loaded.Errors
		if errsOut == nil {
			errsOut = []string{}
		}
		payload.Set("errors", errsOut)
		if untrustedNote != nil {
			payload.Set("note", *untrustedNote)
		}
		encoded, err := jsonx.MarshalIndent(payload, "  ")
		if err != nil {
			logFn("{}")
		} else {
			logFn(string(encoded))
		}
		if failed {
			return 1
		}
		return 0
	}

	if len(reports) == 0 && len(loaded.Errors) == 0 {
		logFn(fmt.Sprintf("No MCP servers configured. Add them to %s or .pi/mcp.json.", filepath.Join(options.AgentDir, "mcp.json")))
	}
	for _, r := range reports {
		var state string
		switch ServerState(r.State) {
		case ServerStateConnected:
			plural := "s"
			if len(r.Tools) == 1 {
				plural = ""
			}
			state = fmt.Sprintf("connected, %d tool%s", len(r.Tools), plural)
		case ServerStateNeedsAuth:
			state = "needs sign-in"
		default:
			state = r.State
		}
		logFn(fmt.Sprintf("%s: %s (%s, %s)", r.Name, state, r.Exposure, r.Scope))
		logFn("  " + r.Transport)
		if r.Override != nil {
			logFn("  project override: " + *r.Override)
		}
		if ServerState(r.State) == ServerStateNeedsAuth {
			if r.AuthProvider != nil {
				logFn(fmt.Sprintf("  sign in with: /login %s in %s", *r.AuthProvider, utils.AppName))
			} else {
				logFn(fmt.Sprintf("  sign in with: %s mcp login %s", utils.AppName, r.Name))
			}
		}
		if len(r.Tools) > 0 {
			formattedTools := make([]string, len(r.Tools))
			for i, tName := range r.Tools {
				if r.ToolExposure != nil {
					if exp, ok := r.ToolExposure.Get(tName); ok {
						formattedTools[i] = fmt.Sprintf("%s [%s]", tName, exp)
						continue
					}
				}
				formattedTools[i] = tName
			}
			logFn("  tools: " + strings.Join(formattedTools, ", "))
		}
		if r.Resources {
			logFn("  offers resources")
		}
		if r.Error != nil && *r.Error != "" {
			logFn("  " + strings.ReplaceAll(*r.Error, "\n", "\n  "))
		}
	}
	for _, cfgErr := range loaded.Errors {
		logFn("config error: " + cfgErr)
	}
	if untrustedNote != nil {
		logFn(*untrustedNote)
	}
	if failed {
		return 1
	}
	return 0
}

type cliLoginPrompt struct {
	name        string
	interactive bool
	input       io.Reader
	openUrl     func(url string)
	logFn       func(line string)
}

func (p *cliLoginPrompt) ShowAuthorizationUrl(u *url.URL) {
	urlStr := ""
	if u != nil {
		urlStr = u.String()
	}
	p.logFn(fmt.Sprintf("Sign in to MCP server %q in your browser:\n%s", p.name, urlStr))
	if p.openUrl != nil {
		p.openUrl(urlStr)
	}
}

func (p *cliLoginPrompt) PromptForRedirectUrl(ctx context.Context) (*string, error) {
	res := cliWaitForRedirectUrl(ctx, p.interactive, p.input)
	if res == "" {
		return nil, nil
	}
	return &res, nil
}

func cliLogin(
	ctx context.Context,
	entry McpServerEntry,
	conn *McpServerConnection,
	serverUrl string,
	timeout time.Duration,
	options McpCommandOptions,
	credentials *McpOAuthCredentialStore,
	logFn func(line string),
	errFn func(line string),
) int {
	name := entry.Name
	if _, err := conn.GetClient(ctx); err == nil {
		logFn(fmt.Sprintf(`Already signed in to MCP server %q (%d tools).`, name, len(conn.Tools())))
		return 0
	}
	if conn.State() != ServerStateNeedsAuth {
		errText := "unknown error"
		if e := conn.Error(); e != nil {
			errText = *e
		}
		errFn(fmt.Sprintf(`MCP server %q failed to connect: %s`, name, errText))
		return 1
	}

	openUrl := options.OpenUrl
	if openUrl == nil {
		openUrl = utils.OpenBrowser
	}
	interactive := false
	if options.Interactive != nil {
		interactive = *options.Interactive
	} else if options.OpenUrl == nil {
		interactive = cliIsStdinTTY()
	}

	settings, setErr := conn.OAuthSettings()
	if setErr != nil {
		errFn(fmt.Sprintf("Sign-in to MCP server %q failed: %s", name, setErr.Error()))
		return 1
	}

	signInCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	signInErr := SignInMcpServer(SignInMcpServerOptions{
		Context:    signInCtx,
		ServerUrl:  serverUrl,
		Store:      credentials.ForServer(name, serverUrl),
		Settings:   settings,
		Challenge:  conn.Challenge(),
		HTTPClient: options.HTTPClient,
		Prompt: &cliLoginPrompt{
			name:        name,
			interactive: interactive,
			input:       options.Input,
			openUrl:     openUrl,
			logFn:       logFn,
		},
	})
	if signInErr != nil {
		var cancelErr *mcpoauth.McpSignInCancelledError
		if errors.As(signInErr, &cancelErr) {
			secs := math.Round(timeout.Seconds())
			errFn(fmt.Sprintf(`Sign-in to MCP server %q was cancelled or not completed within %.0f seconds.`, name, secs))
		} else {
			errFn(fmt.Sprintf(`Sign-in to MCP server %q failed: %s`, name, signInErr.Error()))
		}
		return 1
	}

	conn.ClearChallenge()
	if err := conn.Reconnect(ctx); err != nil {
		errFn("Signed in, but " + err.Error())
		return 1
	}
	logFn(fmt.Sprintf(`Signed in to MCP server %q (%d tools).`, name, len(conn.Tools())))
	return 0
}

func cliWaitForRedirectUrl(ctx context.Context, interactive bool, input io.Reader) string {
	if !interactive {
		<-ctx.Done()
		return ""
	}
	answer, err := utils.QuestionTerminal(
		ctx,
		"If the browser cannot reach this machine, paste the URL it was redirected to: ",
		utils.QuestionTerminalOptions{
			Input:  input,
			Output: os.Stderr,
		},
	)
	if err != nil {
		return ""
	}
	return answer
}
