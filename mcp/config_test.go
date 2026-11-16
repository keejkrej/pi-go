// Ported from packages/mcp/test/config.test.ts (pi v1.5.2).

package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tcfgErrOf(r McpServerConfigResult) string {
	if r.Kind == McpServerConfigKindInvalid {
		return r.Error
	}
	return ""
}

func TestConfig_AcceptsStdioAndHttpAndKeepsUnknownFields(t *testing.T) {
	stdio := jsonx.ObjectOf(
		"command", "npx",
		"args", []any{"-y", "server"},
		"env", jsonx.ObjectOf("A", "1"),
		"cwd", "sub",
		"enabled", false,
	)
	resStdio := ValidateMcpServerConfig("files", stdio, nil)
	if resStdio.Kind != McpServerConfigKindStdio || resStdio.Raw != stdio {
		t.Fatalf("unexpected stdio result: %#v", resStdio)
	}
	if ValidateMcpServerConfig("files", jsonx.ObjectOf("type", "stdio", "command", "x"), nil).Kind != McpServerConfigKindStdio {
		t.Fatal("expected explicit type stdio to pass")
	}

	httpCfg := jsonx.ObjectOf(
		"url", "https://example.com/mcp",
		"headers", jsonx.ObjectOf("Authorization", "Bearer token"),
		"oauth", jsonx.ObjectOf("clientId", "id", "callbackPort", 8080, "scope", "read"),
		"timeout", 30,
		"description", "Docs",
	)
	resHttp := ValidateMcpServerConfig("docs", httpCfg, nil)
	if resHttp.Kind != McpServerConfigKindHttp || resHttp.Raw != httpCfg {
		t.Fatalf("unexpected http result: %#v", resHttp)
	}
	for _, typ := range []string{"http", "streamable-http"} {
		if ValidateMcpServerConfig("docs", jsonx.ObjectOf("type", typ, "url", "http://localhost:3000/mcp"), nil).Kind != McpServerConfigKindHttp {
			t.Fatalf("expected type %q to validate as http", typ)
		}
	}
}

func TestConfig_PicksTransportFromTypeUrlAndCommand(t *testing.T) {
	if got := ValidateMcpServerConfig("s", jsonx.ObjectOf("url", "https://a.example", "command", "x"), nil).Kind; got != McpServerConfigKindHttp {
		t.Fatalf("got %q, want http", got)
	}
	if got := ValidateMcpServerConfig("s", jsonx.ObjectOf("type", "stdio", "url", "https://a.example", "command", "x"), nil).Kind; got != McpServerConfigKindStdio {
		t.Fatalf("got %q, want stdio", got)
	}
	if got := tcfgErrOf(ValidateMcpServerConfig("s", jsonx.ObjectOf("type", "http", "command", "x"), nil)); got != `server "s" needs either "command" (stdio) or "url" (streamable HTTP)` {
		t.Fatalf("got %q", got)
	}
	if got := tcfgErrOf(ValidateMcpServerConfig("s", jsonx.ObjectOf("type", "sse", "url", "https://a.example/sse"), nil)); got != `server "s": legacy SSE transport is not supported; use the streamable HTTP URL` {
		t.Fatalf("got %q", got)
	}
}

func TestConfig_ReportsInvalidNamesAndFields(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"bad name", jsonx.ObjectOf("command", "x"), `invalid server name "bad name" (use letters, digits, "_" and "-")`},
		{"s", "npx", `server "s" must be an object`},
		{"s", []any{}, `server "s" must be an object`},
		{"s", jsonx.ObjectOf("command", "x", "enabled", "yes"), `server "s": enabled must be a boolean`},
		{"s", jsonx.ObjectOf("command", "x", "description", 1), `server "s": description must be a string`},
		{"s", jsonx.ObjectOf("command", "x", "timeout", 0), `server "s": timeout must be a positive number of seconds`},
		{"s", jsonx.ObjectOf("command", "x", "args", "a b"), `server "s": args must be an array of strings`},
		{"s", jsonx.ObjectOf("command", "x", "env", jsonx.ObjectOf("A", 1)), `server "s": env must map names to strings`},
		{"s", jsonx.ObjectOf("command", "x", "cwd", 1), `server "s": cwd must be a string`},
		{"s", jsonx.ObjectOf("url", "file:///x"), `server "s": url must be an http or https URL`},
		{"s", jsonx.ObjectOf("url", "not a url"), `server "s": url must be an http or https URL`},
		{"s", jsonx.ObjectOf("url", "https://a.example", "headers", jsonx.ObjectOf("A", 1)), `server "s": headers must map names to strings`},
		{"s", jsonx.NewObject(), `server "s" needs either "command" (stdio) or "url" (streamable HTTP)`},
	}
	for _, tc := range cases {
		if got := tcfgErrOf(ValidateMcpServerConfig(tc.name, tc.raw, nil)); got != tc.want {
			t.Fatalf("ValidateMcpServerConfig(%q, %v) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

func TestConfig_ChecksOAuthSettings(t *testing.T) {
	oauth := func(settings any) string {
		return tcfgErrOf(ValidateMcpServerConfig("s", jsonx.ObjectOf("url", "https://a.example", "oauth", settings), nil))
	}
	if got := oauth("x"); got != `server "s": oauth must be an object` {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("clientId", 1)); got != `server "s": oauth.clientId must be a string` {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("clientSecret", 1)); got != `server "s": oauth.clientSecret must be a string` {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("callbackPort", 70000)); got != `server "s": oauth.callbackPort must be a port number` {
		t.Fatalf("got %q", got)
	}
	for _, cb := range []string{"https://localhost/callback", "http://example.com/callback", "http://localhost/callback?x=1"} {
		if got := oauth(jsonx.ObjectOf("callbackUrl", cb)); !strings.Contains(got, "oauth.callbackUrl must be an http URI") {
			t.Fatalf("cb=%q got %q", cb, got)
		}
	}
	if got := oauth(jsonx.ObjectOf("callbackUrl", "http://[::1]:8080/cb", "callbackPort", 8080)); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("callbackUrl", "http://localhost:8080/cb", "callbackPort", 9090)); got != `server "s": oauth.callbackUrl and oauth.callbackPort name different ports` {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("scope", 1)); got != `server "s": oauth.scope must be a string` {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("clientName", " ")); got != `server "s": oauth.clientName must be a non-empty string` {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("clientRegistration", "x")); got != `server "s": oauth.clientRegistration must be "auto", "dcr", or "cimd"` {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("clientRegistration", "dcr", "clientName", "app")); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("authServerMetadataUrl", "http://auth.example/.well-known/x")); !strings.Contains(got, "oauth.authServerMetadataUrl must be an https URL") {
		t.Fatalf("got %q", got)
	}
	if got := oauth(jsonx.ObjectOf("authServerMetadataUrl", "http://127.0.0.1/.well-known/x")); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestConfig_AllowsCimdOnlyWithAppCheck(t *testing.T) {
	entry := func(oauth *jsonx.Object) *jsonx.Object {
		return jsonx.ObjectOf("url", "https://a.example", "oauth", oauth)
	}
	if got := tcfgErrOf(ValidateMcpServerConfig("s", entry(jsonx.ObjectOf("clientRegistration", "cimd")), nil)); got !=
		`server "s": oauth.clientRegistration "cimd" is not supported: there are no Client ID Metadata Documents` {
		t.Fatalf("got %q", got)
	}
	var checked []*string
	opts := &McpServerConfigOptions{
		CheckMetadataDocumentCallback: func(cb *string) string {
			checked = append(checked, cb)
			if cb != nil && strings.HasSuffix(*cb, "/other") {
				return "the documents do not list it"
			}
			return ""
		},
	}
	if res := ValidateMcpServerConfig("s", entry(jsonx.ObjectOf("clientRegistration", "cimd")), opts); res.Kind != McpServerConfigKindHttp {
		t.Fatalf("got %#v", res)
	}
	if got := tcfgErrOf(ValidateMcpServerConfig("s", entry(jsonx.ObjectOf("clientRegistration", "cimd", "callbackUrl", "http://localhost/other")), opts)); got !=
		`server "s": the documents do not list it` {
		t.Fatalf("got %q", got)
	}
	if len(checked) != 2 || checked[0] != nil || checked[1] == nil || *checked[1] != "http://localhost/other" {
		t.Fatalf("unexpected checked: %#v", checked)
	}
	if got := tcfgErrOf(ValidateMcpServerConfig("s", entry(jsonx.ObjectOf("clientRegistration", "cimd", "clientId", "id")), opts)); got !=
		`server "s": oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName` {
		t.Fatalf("got %q", got)
	}
}

func TestConfig_ExtendingValidateMcpServerConfigAndOverride(t *testing.T) {
	validateApp := func(name string, raw any) string {
		res := ValidateMcpServerConfig(name, raw, nil)
		if res.Kind == McpServerConfigKindInvalid {
			return res.Error
		}
		shared, hasShared := res.Raw.Get("shared")
		if hasShared {
			if _, ok := shared.(bool); !ok {
				return fmt.Sprintf(`server "%s": shared must be a boolean`, name)
			}
		}
		runOn, hasRunOn := res.Raw.Get("runOn")
		if res.Kind == McpServerConfigKindHttp && hasRunOn && runOn != nil {
			return fmt.Sprintf(`server "%s": runOn is for stdio`, name)
		}
		sharedStr := "undefined"
		if hasShared {
			sharedStr = fmt.Sprint(shared)
		}
		return fmt.Sprintf("%s shared=%s", res.Kind, sharedStr)
	}

	if got := validateApp("s", jsonx.ObjectOf("command", "x", "shared", true, "runOn", "host")); got != "stdio shared=true" {
		t.Fatalf("got %q", got)
	}
	if got := validateApp("s", jsonx.ObjectOf("url", "https://a.example", "runOn", "host")); got != `server "s": runOn is for stdio` {
		t.Fatalf("got %q", got)
	}
	if got := validateApp("s", jsonx.ObjectOf("command", "x", "shared", 1)); got != `server "s": shared must be a boolean` {
		t.Fatalf("got %q", got)
	}
	if got := validateApp("s", jsonx.ObjectOf("command", "x", "shared", 1, "enabled", "no")); got != `server "s": enabled must be a boolean` {
		t.Fatalf("got %q", got)
	}

	if !IsMcpServerOverride(jsonx.ObjectOf("enabled", false)) {
		t.Fatal("expected override for {enabled: false}")
	}
	if !IsMcpServerOverride(jsonx.NewObject()) {
		t.Fatal("expected override for {}")
	}
	if IsMcpServerOverride(jsonx.ObjectOf("command", "x")) {
		t.Fatal("did not expect override with command")
	}
	if IsMcpServerOverride(jsonx.ObjectOf("url", "https://a.example")) {
		t.Fatal("did not expect override with url")
	}
	if IsMcpServerOverride(jsonx.ObjectOf("type", "stdio", "enabled", false)) {
		t.Fatal("did not expect override with type")
	}
}
