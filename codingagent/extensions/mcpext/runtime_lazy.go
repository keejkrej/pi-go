// Ported from packages/coding-agent/src/extensions/mcp/runtime.lazy.ts (pi v1.5.2).

package mcpext

import (
	"net/http"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/mcp"
)

// McpRuntimeModule bundles the MCP runtime constructors loaded on first use.
type McpRuntimeModule struct {
	NewMcpOAuthCredentialStore func(backend ...core.AuthStorageBackend) *McpOAuthCredentialStore
	CreateDefaultTransport     func(entry McpServerEntry, cwd string, authProvider mcp.AuthProvider, httpClient ...*http.Client) (mcp.McpTransport, error)
	NewMcpServerConnection     func(options McpServerConnectionOptions) *McpServerConnection
	NewMcpServerLog            func(path string) *McpServerLog
	SignInMcpServer            func(options SignInMcpServerOptions) error
}

// LoadMcpRuntime returns the runtime helpers needed once at least one MCP server is configured.
func LoadMcpRuntime() McpRuntimeModule {
	return McpRuntimeModule{
		NewMcpOAuthCredentialStore: NewMcpOAuthCredentialStore,
		CreateDefaultTransport:     CreateDefaultTransport,
		NewMcpServerConnection:     NewMcpServerConnection,
		NewMcpServerLog:            NewMcpServerLog,
		SignInMcpServer:            SignInMcpServer,
	}
}
