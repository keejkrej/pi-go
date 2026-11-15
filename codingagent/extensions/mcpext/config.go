// Ported from packages/coding-agent/src/extensions/mcp/config.ts (pi v1.5.2).

package mcpext

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp"
)

// McpExposure controls how an MCP server's tools are exposed to the model.
type McpExposure = core.McpExposure

// McpStdioServerConfig re-exports pi-mcp's stdio server configuration.
type McpStdioServerConfig = mcp.McpStdioServerConfig

// McpOAuthConfig re-exports pi-mcp's OAuth client configuration.
type McpOAuthConfig = mcp.McpOAuthConfig

// McpHttpServerConfig re-exports pi-mcp's streamable HTTP server configuration.
type McpHttpServerConfig = mcp.McpHttpServerConfig

// McpAuthConfig sends a configured pi provider's credential instead of using OAuth.
type McpAuthConfig struct {
	Provider string `json:"provider"`
}

// McpServerConfig is a validated stdio or streamable HTTP MCP server entry.
type McpServerConfig struct {
	Type         string                         `json:"type,omitzero"`
	Command      *string                        `json:"command,omitzero"`
	Args         []string                       `json:"args,omitzero"`
	Env          *omap.Map[string, string]      `json:"env,omitzero"`
	Cwd          *string                        `json:"cwd,omitzero"`
	Url          *string                        `json:"url,omitzero"`
	Headers      *omap.Map[string, string]      `json:"headers,omitzero"`
	OAuth        *McpOAuthConfig                `json:"oauth,omitzero"`
	Auth         *McpAuthConfig                 `json:"auth,omitzero"`
	Exposure     *McpExposure                   `json:"exposure,omitzero"`
	Description  *string                        `json:"description,omitzero"`
	ToolExposure *omap.Map[string, McpExposure] `json:"toolExposure,omitzero"`
	Enabled      *bool                          `json:"enabled,omitzero"`
	Timeout      *float64                       `json:"timeout,omitzero"`
}

// IsHttp reports whether this server uses Streamable HTTP transport.
func (c McpServerConfig) IsHttp() bool {
	return c.Url != nil
}

// IsStdio reports whether this server uses stdio subprocess transport.
func (c McpServerConfig) IsStdio() bool {
	return c.Command != nil
}

// McpServerEntry is a configured or extension-registered MCP server with provenance metadata.
type McpServerEntry struct {
	Name     string          `json:"name"`
	Config   McpServerConfig `json:"config"`
	Source   string          `json:"source"`
	Scope    string          `json:"scope,omitzero"` // "global" | "project" | "extension"
	Override *string         `json:"override,omitzero"`
}

// LoadedMcpConfig holds merged global and trusted-project MCP server entries.
type LoadedMcpConfig struct {
	Servers            []McpServerEntry
	AutoEnableCodemode *bool
	Errors             []string
	ProjectConfig      *string
}

// LoadMcpConfigOptions configures LoadMcpConfig.
type LoadMcpConfigOptions struct {
	AgentDir       string
	Cwd            string
	ProjectTrusted bool
}

// McpServerConfigPatch captures changes made through /mcp.
type McpServerConfigPatch struct {
	Enabled  *bool
	Exposure *McpExposure
}

// UpdateMcpConfigOptions configures UpdateMcpServerConfig.
type UpdateMcpConfigOptions struct {
	Override bool
}

var (
	configOverrideKeys = []string{"enabled", "exposure", "toolExposure"}
	configExposures    = []McpExposure{
		core.McpExposureCodemode,
		core.McpExposureDeferred,
		core.McpExposureDirect,
		core.McpExposureHidden,
	}
	configExposureAliases = map[string]McpExposure{
		"codemode-deferred": core.McpExposureCodemode,
	}
	configLoopbackHosts = []string{"localhost", "127.0.0.1", "::1", "[::1]"}
	configIndentRe      = regexp.MustCompile(`(?m)^([ \t]+)\S`)
)

// IsClientMetadataDocumentCallback reports whether pi's Client ID Metadata Documents
// list a redirect URI for the given loopback callback URL.
func IsClientMetadataDocumentCallback(callbackUrl *string) bool {
	if callbackUrl == nil {
		return true
	}
	parsed, err := url.Parse(*callbackUrl)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host != "::1" && host != "[::1]" && parsed.Path == "/callback"
}

// GetMcpToolExposure resolves a single tool's exposure for config.
func GetMcpToolExposure(config McpServerConfig, toolName string) McpExposure {
	return core.GetMcpToolExposure(core.McpServerConfig{
		Exposure:     config.Exposure,
		ToolExposure: config.ToolExposure,
	}, toolName)
}

// FromCoreMcpServerConfig converts a core.McpServerConfig registered by another extension
// into an mcpext.McpServerConfig.
func FromCoreMcpServerConfig(c core.McpServerConfig) McpServerConfig {
	var timeout *float64
	if c.Timeout != nil {
		v := float64(*c.Timeout)
		timeout = &v
	}
	var env *omap.Map[string, string]
	if c.Env != nil {
		env = c.Env.Clone()
	}
	var headers *omap.Map[string, string]
	if c.Headers != nil {
		headers = c.Headers.Clone()
	}
	var toolExp *omap.Map[string, McpExposure]
	if c.ToolExposure != nil {
		toolExp = c.ToolExposure.Clone()
	}
	var args []string
	if c.Args != nil {
		args = slices.Clone(c.Args)
	}
	return McpServerConfig{
		Command:      c.Command,
		Args:         args,
		Env:          env,
		Cwd:          c.Cwd,
		Url:          c.Url,
		Headers:      headers,
		Exposure:     c.Exposure,
		Description:  c.Description,
		ToolExposure: toolExp,
		Enabled:      c.Enabled,
		Timeout:      timeout,
	}
}

func configToObject(raw any) (*jsonx.Object, bool) {
	if raw == nil {
		return nil, false
	}
	if obj, ok := raw.(*jsonx.Object); ok {
		return obj, obj != nil
	}
	encoded, err := jsonx.Encode(raw)
	if err != nil {
		return nil, false
	}
	obj, ok := encoded.(*jsonx.Object)
	return obj, ok && obj != nil
}

func configResolveExposureAlias(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	if mapped, found := configExposureAliases[s]; found {
		return string(mapped)
	}
	return value
}

func configResolveExposureAliases(obj *jsonx.Object) *jsonx.Object {
	resolved := obj.Clone()
	if exp, hasExp := obj.Get("exposure"); hasExp {
		resolved.Set("exposure", configResolveExposureAlias(exp))
	}
	if teRaw, hasTe := obj.Get("toolExposure"); hasTe {
		if teObj, ok := configToObject(teRaw); ok {
			nextTe := jsonx.NewObject()
			for k, v := range teObj.All() {
				nextTe.Set(k, configResolveExposureAlias(v))
			}
			resolved.Set("toolExposure", nextTe)
		}
	}
	return resolved
}

func configIsExposure(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	return slices.Contains(configExposures, McpExposure(s))
}

func configCheckExposures(obj *jsonx.Object) string {
	exposuresList := `"codemode", "deferred", "direct", "hidden"`
	if exp, hasExp := obj.Get("exposure"); hasExp {
		if !configIsExposure(exp) {
			return fmt.Sprintf("exposure must be one of %s", exposuresList)
		}
	}
	if teRaw, hasTe := obj.Get("toolExposure"); hasTe {
		teObj, ok := configToObject(teRaw)
		if !ok {
			return "toolExposure must map tool names to exposures"
		}
		for k, v := range teObj.All() {
			if !configIsExposure(v) {
				return fmt.Sprintf(`toolExposure "%s" must be one of %s`, k, exposuresList)
			}
		}
	}
	return ""
}

func configCheckAuth(obj *jsonx.Object, rawUrl string) string {
	authRaw, hasAuth := obj.Get("auth")
	if !hasAuth {
		return ""
	}
	authObj, ok := configToObject(authRaw)
	if !ok {
		return "auth.provider must be a provider name"
	}
	provider, ok := authObj.GetString("provider")
	if !ok || provider == "" {
		return "auth.provider must be a provider name"
	}
	parsed, err := url.Parse(rawUrl)
	if err != nil {
		return "auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]"
	}
	if parsed.Scheme != "https" && !slices.Contains(configLoopbackHosts, parsed.Hostname()) {
		return "auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]"
	}
	return ""
}

// ValidateMcpServerConfig validates one entry of the mcpServers shape: pi-mcp's shared fields
// and pi's own fields, returning a copy with exposure aliases resolved or an error message.
func ValidateMcpServerConfig(name string, raw any) (*McpServerConfig, error) {
	validationInput := raw
	if obj, ok := configToObject(raw); ok {
		validationInput = configResolveExposureAliases(obj)
	}
	res := mcp.ValidateMcpServerConfig(name, validationInput, &mcp.McpServerConfigOptions{
		CheckMetadataDocumentCallback: func(callbackUrl *string) string {
			if IsClientMetadataDocumentCallback(callbackUrl) {
				return ""
			}
			return `oauth.clientRegistration "cimd" requires oauth.callbackUrl on localhost or 127.0.0.1 with path /callback`
		},
	})
	if res.Kind == mcp.McpServerConfigKindInvalid {
		return nil, fmt.Errorf("%s", res.Error)
	}
	errMsg := configCheckExposures(res.Raw)
	if errMsg == "" && res.Kind == mcp.McpServerConfigKindHttp && res.Http != nil {
		errMsg = configCheckAuth(res.Raw, res.Http.Url)
	}
	if errMsg != "" {
		return nil, fmt.Errorf(`server "%s": %s`, name, errMsg)
	}
	var cfg McpServerConfig
	if err := jsonx.Decode(res.Raw, &cfg); err != nil {
		return nil, fmt.Errorf(`server "%s": %s`, name, err.Error())
	}
	return &cfg, nil
}

type configState struct {
	servers            *omap.Map[string, McpServerEntry]
	autoEnableCodemode *bool
	errors             []string
}

func configReadFile(path string, scope string, state *configState) {
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, err.Error()))
		return
	}
	parsedAny, err := jsonx.ParseString(utils.StripBom(string(rawBytes)))
	if err != nil {
		state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, err.Error()))
		return
	}
	parsedObj, ok := configToObject(parsedAny)
	if !ok {
		state.errors = append(state.errors, fmt.Sprintf(`%s: expected an object with an "mcpServers" object`, path))
		return
	}
	var serversObj *jsonx.Object
	if rawServers, hasServers := parsedObj.Get("mcpServers"); hasServers {
		serversObj, ok = configToObject(rawServers)
		if !ok {
			state.errors = append(state.errors, fmt.Sprintf(`%s: expected an object with an "mcpServers" object`, path))
			return
		}
	}
	if rawAuto, hasAuto := parsedObj.Get("autoEnableCodemode"); hasAuto {
		if b, isBool := rawAuto.(bool); isBool {
			state.autoEnableCodemode = &b
		} else {
			state.errors = append(state.errors, fmt.Sprintf("%s: autoEnableCodemode must be a boolean", path))
		}
	}
	if serversObj == nil {
		return
	}
	for name, value := range serversObj.All() {
		if scope == "project" && mcp.IsMcpServerOverride(value) {
			valObj, _ := configToObject(value)
			base, hasBase := state.servers.Get(name)
			var extra []string
			for k := range valObj.All() {
				if !slices.Contains(configOverrideKeys, k) {
					extra = append(extra, k)
				}
			}
			if !hasBase {
				state.errors = append(state.errors, fmt.Sprintf(`%s: server "%s" needs "command" or "url", or a global server to override`, path, name))
			} else if len(extra) > 0 {
				state.errors = append(state.errors, fmt.Sprintf(`%s: server "%s": an override can only set %s`, path, name, strings.Join(configOverrideKeys, ", ")))
			} else {
				mergedAny, encErr := jsonx.Encode(base.Config)
				if encErr != nil {
					state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, encErr.Error()))
					continue
				}
				mergedObj, _ := mergedAny.(*jsonx.Object)
				mergedObj.Assign(valObj)
				cfg, valErr := ValidateMcpServerConfig(name, mergedObj)
				if valErr != nil {
					state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, valErr.Error()))
				} else {
					nextEntry := base
					nextEntry.Config = *cfg
					p := path
					nextEntry.Override = &p
					state.servers.Set(name, nextEntry)
				}
			}
			continue
		}

		cfg, valErr := ValidateMcpServerConfig(name, value)
		if valErr != nil {
			state.errors = append(state.errors, fmt.Sprintf("%s: %s", path, valErr.Error()))
			continue
		}
		var clash string
		for existingName := range state.servers.All() {
			if existingName != name && mcp.McpNamespace(existingName) == mcp.McpNamespace(name) {
				clash = existingName
				break
			}
		}
		if clash != "" {
			state.errors = append(state.errors, fmt.Sprintf(`%s: server "%s" conflicts with "%s"`, path, name, clash))
			continue
		}
		if scope == "project" && cfg.IsHttp() && cfg.Auth != nil {
			state.errors = append(state.errors, fmt.Sprintf(`%s: server "%s": auth is only allowed in the global mcp.json`, path, name))
			continue
		}
		state.servers.Set(name, McpServerEntry{
			Name:   name,
			Config: *cfg,
			Source: path,
			Scope:  scope,
		})
	}
}

// LoadMcpConfig loads global and (when trusted) project MCP configuration. Disabled servers are
// included with enabled: false so they can be enabled again.
func LoadMcpConfig(options LoadMcpConfigOptions) LoadedMcpConfig {
	state := &configState{
		servers: omap.NewMap[string, McpServerEntry](),
	}
	if options.AgentDir != "" {
		configReadFile(filepath.Join(options.AgentDir, "mcp.json"), "global", state)
	}
	var projectConfig *string
	if options.ProjectTrusted && options.Cwd != "" {
		p := filepath.Join(options.Cwd, utils.ConfigDirName, "mcp.json")
		projectConfig = &p
		configReadFile(p, "project", state)
	}
	return LoadedMcpConfig{
		Servers:            state.servers.Values(),
		AutoEnableCodemode: state.autoEnableCodemode,
		Errors:             state.errors,
		ProjectConfig:      projectConfig,
	}
}

// UpdateMcpServerConfig changes one server's settings in the mcp.json that defines or overrides it.
func UpdateMcpServerConfig(path, name string, patch McpServerConfigPatch, options ...UpdateMcpConfigOptions) error {
	var opts UpdateMcpConfigOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return configEditMcpServers(path, func(servers, parsed *jsonx.Object) (bool, error) {
		var rawServer any
		var exists bool
		if servers != nil {
			rawServer, exists = servers.Get(name)
		}
		var serverObj *jsonx.Object
		if !exists && opts.Override {
			serverObj = jsonx.NewObject()
			nextServers := jsonx.NewObject()
			if servers != nil {
				nextServers.Assign(servers)
			}
			nextServers.Set(name, serverObj)
			parsed.Set("mcpServers", nextServers)
		} else if exists {
			var ok bool
			serverObj, ok = rawServer.(*jsonx.Object)
			if !ok || serverObj == nil {
				return false, fmt.Errorf(`%s does not define MCP server "%s"`, path, name)
			}
		} else {
			return false, fmt.Errorf(`%s does not define MCP server "%s"`, path, name)
		}

		keepDefaults := mcp.IsMcpServerOverride(serverObj)
		if patch.Enabled != nil {
			if *patch.Enabled && !keepDefaults {
				serverObj.Delete("enabled")
			} else {
				serverObj.Set("enabled", *patch.Enabled)
			}
		}
		if patch.Exposure != nil {
			if *patch.Exposure == core.McpExposureCodemode && !keepDefaults {
				serverObj.Delete("exposure")
			} else {
				serverObj.Set("exposure", string(*patch.Exposure))
			}
		}
		return true, nil
	})
}

// AddMcpServerConfig adds or replaces a server in mcp.json, creating the file if missing.
// It returns true when an existing entry was replaced.
func AddMcpServerConfig(path, name string, config McpServerConfig) (bool, error) {
	replaced := false
	err := configEditMcpServers(path, func(servers, parsed *jsonx.Object) (bool, error) {
		target := servers
		if target == nil {
			target = jsonx.NewObject()
		}
		replaced = target.Has(name)
		encoded, err := jsonx.Encode(config)
		if err != nil {
			return false, err
		}
		target.Set(name, encoded)
		parsed.Set("mcpServers", target)
		return true, nil
	})
	return replaced, err
}

// RemoveMcpServerConfig removes a server from mcp.json, returning false when the file or entry does not exist.
func RemoveMcpServerConfig(path, name string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	removed := false
	err := configEditMcpServers(path, func(servers, _ *jsonx.Object) (bool, error) {
		if servers == nil || !servers.Has(name) {
			return false, nil
		}
		servers.Delete(name)
		removed = true
		return true, nil
	})
	return removed, err
}

func configEditMcpServers(path string, edit func(servers, parsed *jsonx.Object) (bool, error)) error {
	var text *string
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		s := utils.StripBom(string(rawBytes))
		text = &s
	}

	var parsedObj *jsonx.Object
	if text == nil {
		parsedObj = jsonx.NewObject()
	} else {
		parsedAny, err := jsonx.ParseString(*text)
		if err != nil {
			return err
		}
		var ok bool
		parsedObj, ok = parsedAny.(*jsonx.Object)
		if !ok || parsedObj == nil {
			return fmt.Errorf(`%s: expected an object with an "mcpServers" object`, path)
		}
	}

	var serversObj *jsonx.Object
	if rawServers, hasServers := parsedObj.Get("mcpServers"); hasServers {
		var ok bool
		serversObj, ok = rawServers.(*jsonx.Object)
		if !ok || serversObj == nil {
			return fmt.Errorf(`%s: expected an object with an "mcpServers" object`, path)
		}
	}

	changed, err := edit(serversObj, parsedObj)
	if err != nil || !changed {
		return err
	}

	indent := "  "
	if text != nil {
		if m := configIndentRe.FindStringSubmatch(*text); len(m) == 2 {
			indent = m[1]
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := jsonx.MarshalIndent(parsedObj, indent)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
