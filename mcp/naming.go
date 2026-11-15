// Ported from packages/mcp/src/naming.ts (pi v1.5.2).

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const namMaxToolNameLength = 64

// CreateMcpToolName formats a sanitized qualified tool name for an MCP tool on
// server, truncating with a deterministic SHA-256 suffix when exceeding 64 bytes
// or colliding according to isTaken.
func CreateMcpToolName(server, tool string, isTaken func(name string) bool) string {
	sanitized := namSanitize("mcp__" + server + "__" + tool)
	taken := isTaken != nil && isTaken(sanitized)
	if len(sanitized) <= namMaxToolNameLength && !taken {
		return sanitized
	}
	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	hash := hex.EncodeToString(sum[:])[:8]
	prefixLen := namMaxToolNameLength - len(hash) - 1
	if len(sanitized) > prefixLen {
		sanitized = sanitized[:prefixLen]
	}
	return sanitized + "_" + hash
}

// McpNamespace returns the tool-name namespace prefix for an MCP server.
func McpNamespace(server string) string {
	return "mcp__" + strings.ReplaceAll(server, "-", "_")
}

func namSanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
