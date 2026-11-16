// Ported from packages/mcp/src/config.ts (pi v1.5.2).

package mcp

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

var cfgServerNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// McpStdioServerConfig configures an MCP server started as a local program that
// speaks MCP over stdin and stdout.
type McpStdioServerConfig struct {
	Type        string            `json:"type,omitzero"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitzero"`
	Env         map[string]string `json:"env,omitzero"`
	Cwd         string            `json:"cwd,omitzero"`
	Description *string           `json:"description,omitzero"`
	Enabled     *bool             `json:"enabled,omitzero"`
	Timeout     *float64          `json:"timeout,omitzero"`
}

// McpOAuthConfig configures OAuth client settings for HTTP MCP servers.
type McpOAuthConfig struct {
	ClientId              string `json:"clientId,omitzero"`
	ClientSecret          string `json:"clientSecret,omitzero"`
	CallbackPort          *int   `json:"callbackPort,omitzero"`
	CallbackUrl           string `json:"callbackUrl,omitzero"`
	Scope                 string `json:"scope,omitzero"`
	ClientName            string `json:"clientName,omitzero"`
	ClientRegistration    string `json:"clientRegistration,omitzero"`
	AuthServerMetadataUrl string `json:"authServerMetadataUrl,omitzero"`
}

// McpHttpServerConfig configures an MCP server reached over Streamable HTTP.
type McpHttpServerConfig struct {
	Type        string            `json:"type,omitzero"`
	Url         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitzero"`
	OAuth       *McpOAuthConfig   `json:"oauth,omitzero"`
	Description *string           `json:"description,omitzero"`
	Enabled     *bool             `json:"enabled,omitzero"`
	Timeout     *float64          `json:"timeout,omitzero"`
}

// McpServerConfigKind tags the result of ValidateMcpServerConfig.
type McpServerConfigKind string

const (
	McpServerConfigKindStdio   McpServerConfigKind = "stdio"
	McpServerConfigKindHttp    McpServerConfigKind = "http"
	McpServerConfigKindInvalid McpServerConfigKind = "invalid"
)

// McpServerConfigResult is a validated mcpServers entry tagged with its
// transport kind, or why it is invalid. Unknown fields are preserved on Raw.
type McpServerConfigResult struct {
	Kind  McpServerConfigKind   `json:"kind"`
	Stdio *McpStdioServerConfig `json:"-"`
	Http  *McpHttpServerConfig  `json:"-"`
	Raw   *jsonx.Object         `json:"-"`
	Error string                `json:"error,omitzero"`
}

// McpServerConfigOptions supplies application-level checks when validating a
// server entry.
type McpServerConfigOptions struct {
	CheckMetadataDocumentCallback func(callbackUrl *string) string
}

// ValidateMcpServerConfig validates one entry of the mcpServers configuration
// shape, keeping unknown fields on result.Raw for caller-specific checks.
func ValidateMcpServerConfig(name string, raw any, options *McpServerConfigOptions) McpServerConfigResult {
	var opts McpServerConfigOptions
	if options != nil {
		opts = *options
	}
	invalid := func(err string) McpServerConfigResult {
		return McpServerConfigResult{Kind: McpServerConfigKindInvalid, Error: err}
	}
	if !cfgServerNameRe.MatchString(name) {
		return invalid(fmt.Sprintf(`invalid server name "%s" (use letters, digits, "_" and "-")`, name))
	}
	obj, ok := cfgToObject(raw)
	if !ok {
		return invalid(fmt.Sprintf(`server "%s" must be an object`, name))
	}
	if errMsg := cfgValidateBaseFields(obj); errMsg != "" {
		return invalid(fmt.Sprintf(`server "%s": %s`, name, errMsg))
	}

	typVal, hasType := obj.Get("type")
	urlVal, hasUrl := obj.Get("url")
	cmdVal, hasCmd := obj.Get("command")

	if hasType && typVal == "sse" {
		return invalid(fmt.Sprintf(`server "%s": legacy SSE transport is not supported; use the streamable HTTP URL`, name))
	}

	typStr, typIsStr := typVal.(string)
	if hasUrl {
		if urlStr, ok := urlVal.(string); ok && (!hasType || (typIsStr && (typStr == "http" || typStr == "streamable-http"))) {
			if errMsg := cfgValidateHttpFields(obj, urlStr, opts); errMsg != "" {
				return invalid(fmt.Sprintf(`server "%s": %s`, name, errMsg))
			}
			var decoded McpHttpServerConfig
			if err := jsonx.Decode(obj, &decoded); err != nil {
				return invalid(fmt.Sprintf(`server "%s": %s`, name, err.Error()))
			}
			return McpServerConfigResult{
				Kind: McpServerConfigKindHttp,
				Http: &decoded,
				Raw:  obj,
			}
		}
	}

	if hasCmd {
		if _, ok := cmdVal.(string); ok && (!hasType || (typIsStr && typStr == "stdio")) {
			if errMsg := cfgValidateStdioFields(obj); errMsg != "" {
				return invalid(fmt.Sprintf(`server "%s": %s`, name, errMsg))
			}
			var decoded McpStdioServerConfig
			if err := jsonx.Decode(obj, &decoded); err != nil {
				return invalid(fmt.Sprintf(`server "%s": %s`, name, err.Error()))
			}
			return McpServerConfigResult{
				Kind:  McpServerConfigKindStdio,
				Stdio: &decoded,
				Raw:   obj,
			}
		}
	}

	return invalid(fmt.Sprintf(`server "%s" needs either "command" (stdio) or "url" (streamable HTTP)`, name))
}

// IsMcpServerOverride reports whether entry overrides a server defined
// elsewhere instead of defining one (has no "command", "url", or "type").
func IsMcpServerOverride(entry any) bool {
	obj, ok := cfgToObject(entry)
	if !ok {
		return false
	}
	return !obj.Has("command") && !obj.Has("url") && !obj.Has("type")
}

func cfgToObject(value any) (*jsonx.Object, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case *jsonx.Object:
		if v == nil {
			return nil, false
		}
		return v, true
	case map[string]any:
		if v == nil {
			return nil, false
		}
		obj := jsonx.NewObject()
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			obj.Set(k, v[k])
		}
		return obj, true
	default:
		return nil, false
	}
}

func cfgToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	default:
		return 0, false
	}
}

func cfgValidateBaseFields(entry *jsonx.Object) string {
	if enabled, ok := entry.Get("enabled"); ok {
		if _, isBool := enabled.(bool); !isBool {
			return "enabled must be a boolean"
		}
	}
	if desc, ok := entry.Get("description"); ok {
		if _, isStr := desc.(string); !isStr {
			return "description must be a string"
		}
	}
	if timeout, ok := entry.Get("timeout"); ok {
		f, isNum := cfgToFloat(timeout)
		if !isNum || math.IsNaN(f) || math.IsInf(f, 0) || !(f > 0) {
			return "timeout must be a positive number of seconds"
		}
	}
	return ""
}

func cfgValidateHttpFields(entry *jsonx.Object, rawUrl string, options McpServerConfigOptions) string {
	u, err := cfgParseUrl(rawUrl)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "url must be an http or https URL"
	}
	if headers, ok := entry.Get("headers"); ok {
		if !cfgIsStringRecord(headers) {
			return "headers must map names to strings"
		}
	}
	oauthVal, hasOAuth := entry.Get("oauth")
	if !hasOAuth {
		return ""
	}
	return cfgValidateOAuth(oauthVal, options)
}

func cfgValidateStdioFields(entry *jsonx.Object) string {
	if args, ok := entry.Get("args"); ok {
		if !cfgIsStringSlice(args) {
			return "args must be an array of strings"
		}
	}
	if env, ok := entry.Get("env"); ok {
		if !cfgIsStringRecord(env) {
			return "env must map names to strings"
		}
	}
	if cwd, ok := entry.Get("cwd"); ok {
		if _, isStr := cwd.(string); !isStr {
			return "cwd must be a string"
		}
	}
	return ""
}

func cfgIsStringSlice(value any) bool {
	switch s := value.(type) {
	case []string:
		return s != nil
	case []any:
		if s == nil {
			return false
		}
		for _, item := range s {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func cfgIsStringRecord(value any) bool {
	switch m := value.(type) {
	case map[string]string:
		return m != nil
	case *jsonx.Object:
		if m == nil {
			return false
		}
		for _, v := range m.All() {
			if _, ok := v.(string); !ok {
				return false
			}
		}
		return true
	case map[string]any:
		if m == nil {
			return false
		}
		for _, v := range m {
			if _, ok := v.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func cfgIsLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func cfgParseUrl(raw string) (*url.URL, error) {
	if raw == "" || strings.ContainsAny(raw, "\r\n\t ") {
		return nil, fmt.Errorf("invalid url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("missing scheme or host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	return u, nil
}

func cfgValidateOAuth(value any, options McpServerConfigOptions) string {
	obj, ok := cfgToObject(value)
	if !ok {
		return "oauth must be an object"
	}
	var clientIdVal any
	var hasClientId bool
	if clientIdVal, hasClientId = obj.Get("clientId"); hasClientId {
		if _, isStr := clientIdVal.(string); !isStr {
			return "oauth.clientId must be a string"
		}
	}
	if clientSecretVal, hasClientSecret := obj.Get("clientSecret"); hasClientSecret {
		if _, isStr := clientSecretVal.(string); !isStr {
			return "oauth.clientSecret must be a string"
		}
	}
	var callbackPort *int
	if portVal, hasPort := obj.Get("callbackPort"); hasPort {
		f, isNum := cfgToFloat(portVal)
		if !isNum || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f < 1 || f > 65535 {
			return "oauth.callbackPort must be a port number"
		}
		p := int(f)
		callbackPort = &p
	}
	var callbackUrlPtr *string
	if cbVal, hasCb := obj.Get("callbackUrl"); hasCb {
		cbStr, isStr := cbVal.(string)
		cbUrl, valid := cfgParseLoopbackRedirectUri(cbStr)
		if !isStr || !valid {
			return "oauth.callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment"
		}
		callbackUrlPtr = &cbStr
		urlPort := cbUrl.Port()
		if urlPort == "80" {
			urlPort = ""
		}
		if urlPort != "" && callbackPort != nil {
			p, err := strconv.Atoi(urlPort)
			if err == nil && p != *callbackPort {
				return "oauth.callbackUrl and oauth.callbackPort name different ports"
			}
		}
	}
	if scopeVal, hasScope := obj.Get("scope"); hasScope {
		if _, isStr := scopeVal.(string); !isStr {
			return "oauth.scope must be a string"
		}
	}
	var clientNameVal any
	var hasClientName bool
	if clientNameVal, hasClientName = obj.Get("clientName"); hasClientName {
		cnStr, isStr := clientNameVal.(string)
		if !isStr || strings.TrimSpace(cnStr) == "" {
			return "oauth.clientName must be a non-empty string"
		}
	}
	if regVal, hasReg := obj.Get("clientRegistration"); hasReg {
		if regVal != "auto" && regVal != "dcr" {
			if regVal != "cimd" {
				return `oauth.clientRegistration must be "auto", "dcr", or "cimd"`
			}
			if hasClientId || hasClientName {
				return `oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName`
			}
			if options.CheckMetadataDocumentCallback == nil {
				return `oauth.clientRegistration "cimd" is not supported: there are no Client ID Metadata Documents`
			}
			if callbackError := options.CheckMetadataDocumentCallback(callbackUrlPtr); callbackError != "" {
				return callbackError
			}
		}
	}
	if metadataVal, hasMetadata := obj.Get("authServerMetadataUrl"); hasMetadata {
		metaStr, isStr := metadataVal.(string)
		var u *url.URL
		if isStr {
			u, _ = cfgParseUrl(metaStr)
		}
		if u == nil || !(u.Scheme == "https" || (u.Scheme == "http" && cfgIsLoopbackHost(u.Hostname()))) {
			return "oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]"
		}
	}
	return ""
}

func cfgParseLoopbackRedirectUri(value string) (*url.URL, bool) {
	u, err := cfgParseUrl(value)
	if err != nil {
		return nil, false
	}
	if u.Scheme != "http" || !cfgIsLoopbackHost(u.Hostname()) || u.RawQuery != "" || u.Fragment != "" {
		return nil, false
	}
	return u, true
}
