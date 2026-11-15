// Ported from packages/coding-agent/src/core/mcp-servers.ts (pi v1.5.2, barebone stub).

package core

import (
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	ListMcpResourcesTool         = "list_mcp_resources"
	ListMcpResourceTemplatesTool = "list_mcp_resource_templates"
	ReadMcpResourceTool          = "read_mcp_resource"
)

var msMcpResourceTools = map[string]struct{}{
	ListMcpResourcesTool:         {},
	ListMcpResourceTemplatesTool: {},
	ReadMcpResourceTool:          {},
}

// McpExposure controls how an MCP server's tools are exposed to the model.
type McpExposure string

const (
	McpExposureCodemode McpExposure = "codemode"
	McpExposureDeferred McpExposure = "deferred"
	McpExposureDirect   McpExposure = "direct"
	McpExposureHidden   McpExposure = "hidden"
)

// McpServerConfig is the minimal barebone MCP server configuration shape.
type McpServerConfig struct {
	Exposure     *McpExposure                   `json:"exposure,omitzero"`
	Description  *string                        `json:"description,omitzero"`
	ToolExposure *omap.Map[string, McpExposure] `json:"toolExposure,omitzero"`
	Timeout      *int                           `json:"timeout,omitzero"`
	Command      *string                        `json:"command,omitzero"`
	Args         []string                       `json:"args,omitzero"`
	Env          *omap.Map[string, string]      `json:"env,omitzero"`
	Cwd          *string                        `json:"cwd,omitzero"`
	Url          *string                        `json:"url,omitzero"`
	Headers      *omap.Map[string, string]      `json:"headers,omitzero"`
	Enabled      *bool                          `json:"enabled,omitzero"`
}

// RegisteredMcpServer describes an MCP server registration stored in McpServerRegistry.
type RegisteredMcpServer struct {
	Name          string          `json:"name"`
	Config        McpServerConfig `json:"config"`
	ExtensionPath string          `json:"extensionPath"`
}

func msToolPatternRegExp(pattern string) *jsre.Regexp {
	parts := strings.Split(pattern, "*")
	escaped := make([]string, len(parts))
	for i, part := range parts {
		var b strings.Builder
		for _, r := range part {
			switch r {
			case '.', '+', '*', '?', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		escaped[i] = b.String()
	}
	src := "^" + strings.Join(escaped, ".*") + "$"
	re, err := jsre.Compile(src, "")
	if err != nil {
		return nil
	}
	return re
}

// CreateToolNameMatcher builds a matcher for exact tool names or `*` wildcard patterns.
func CreateToolNameMatcher(entries []string) func(name string) bool {
	names := make(map[string]struct{})
	var patterns []*jsre.Regexp
	for _, entry := range entries {
		if !strings.Contains(entry, "*") {
			names[entry] = struct{}{}
			continue
		}
		if re := msToolPatternRegExp(entry); re != nil {
			patterns = append(patterns, re)
		}
	}
	return func(name string) bool {
		if _, ok := names[name]; ok {
			return true
		}
		for _, p := range patterns {
			if p.Test(name) {
				return true
			}
		}
		return false
	}
}

// IsMcpToolName reports whether a tool name belongs to an MCP server or MCP resource tool.
func IsMcpToolName(name string) bool {
	if strings.HasPrefix(name, "mcp__") {
		return true
	}
	_, ok := msMcpResourceTools[name]
	return ok
}

// GetMcpToolExposure returns the effective exposure for a tool on an MCP server.
func GetMcpToolExposure(config McpServerConfig, toolName string) McpExposure {
	if config.ToolExposure != nil {
		if exact, ok := config.ToolExposure.Get(toolName); ok {
			return exact
		}
		for pattern, exposure := range config.ToolExposure.All() {
			if strings.Contains(pattern, "*") {
				if re := msToolPatternRegExp(pattern); re != nil && re.Test(toolName) {
					return exposure
				}
			}
		}
	}
	if config.Exposure != nil {
		return *config.Exposure
	}
	return McpExposureCodemode
}

// McpServerRegistry is a barebone in-memory MCP server registry stub.
// Barebone builds do not load or connect to external MCP servers.
type McpServerRegistry struct {
	mu             sync.RWMutex
	servers        *omap.Map[string, RegisteredMcpServer]
	changeListener func()
}

// NewMcpServerRegistry creates an empty barebone McpServerRegistry.
func NewMcpServerRegistry() *McpServerRegistry {
	return &McpServerRegistry{
		servers: omap.NewMap[string, RegisteredMcpServer](),
	}
}

// Register registers or replaces a server entry in memory.
func (r *McpServerRegistry) Register(server RegisteredMcpServer) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.servers == nil {
		r.servers = omap.NewMap[string, RegisteredMcpServer]()
	}
	r.servers.Set(server.Name, server)
	cb := r.changeListener
	r.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// Unregister removes a server registered by extensionPath.
func (r *McpServerRegistry) Unregister(name, extensionPath string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.servers == nil {
		r.mu.Unlock()
		return
	}
	existing, ok := r.servers.Get(name)
	if !ok || existing.ExtensionPath != extensionPath {
		r.mu.Unlock()
		return
	}
	r.servers.Delete(name)
	cb := r.changeListener
	r.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// Get returns the server registered under name, if any.
func (r *McpServerRegistry) Get(name string) *RegisteredMcpServer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.servers == nil {
		return nil
	}
	s, ok := r.servers.Get(name)
	if !ok {
		return nil
	}
	copyS := s
	return &copyS
}

// List returns registered servers in registration order.
func (r *McpServerRegistry) List() []RegisteredMcpServer {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.servers == nil {
		return nil
	}
	return r.servers.Values()
}

// SetChangeListener sets a callback invoked when registrations change.
func (r *McpServerRegistry) SetChangeListener(listener func()) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.changeListener = listener
	r.mu.Unlock()
}

// Clear clears any in-memory server registrations.
func (r *McpServerRegistry) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.servers != nil {
		r.servers.Clear()
	}
	r.mu.Unlock()
}
