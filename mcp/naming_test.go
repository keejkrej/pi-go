// Ported from packages/mcp/test/naming.test.ts (pi v1.5.2).

package mcp

import (
	"regexp"
	"strings"
	"testing"
)

func TestNaming_CreateMcpToolName(t *testing.T) {
	if got := CreateMcpToolName("docs", "search", nil); got != "mcp__docs__search" {
		t.Fatalf("got %q", got)
	}
	if got := CreateMcpToolName("my-server", "get.item/v2", nil); got != "mcp__my_server__get_item_v2" {
		t.Fatalf("got %q", got)
	}

	long := CreateMcpToolName("server", strings.Repeat("x", 100), nil)
	if len(long) != 64 {
		t.Fatalf("len = %d, want 64", len(long))
	}
	if !regexp.MustCompile(`^mcp__server__x+_[0-9a-f]{8}$`).MatchString(long) {
		t.Fatalf("long = %q did not match expected pattern", long)
	}
	if other := CreateMcpToolName("server", strings.Repeat("x", 100)+"y", nil); other == long {
		t.Fatalf("expected different hash for longer different tool")
	}
	if again := CreateMcpToolName("server", strings.Repeat("x", 100), nil); again != long {
		t.Fatalf("expected deterministic output: %q != %q", again, long)
	}
	a := CreateMcpToolName("ser-ver", strings.Repeat("x", 100), nil)
	b := CreateMcpToolName("ser_ver", strings.Repeat("x", 100), nil)
	if a == b {
		t.Fatalf("expected raw server name in hash: %q == %q", a, b)
	}

	taken := CreateMcpToolName("s", "a_b", nil)
	if taken != "mcp__s__a_b" {
		t.Fatalf("taken = %q", taken)
	}
	second := CreateMcpToolName("s", "a-b", func(name string) bool { return name == taken })
	if !regexp.MustCompile(`^mcp__s__a_b_[0-9a-f]{8}$`).MatchString(second) {
		t.Fatalf("second = %q", second)
	}
	if first := CreateMcpToolName("s", "a_b", func(name string) bool { return name == taken }); first == second {
		t.Fatalf("expected distinct hashes on collision: %q", first)
	}
}

func TestNaming_McpNamespace(t *testing.T) {
	if got := McpNamespace("docs"); got != "mcp__docs" {
		t.Fatalf("got %q", got)
	}
	if got := McpNamespace("my-server"); got != "mcp__my_server" {
		t.Fatalf("got %q", got)
	}
	if McpNamespace("my-server") != McpNamespace("my_server") {
		t.Fatal("expected normalized namespace parity")
	}
	if !strings.HasPrefix(CreateMcpToolName("my-server", "search", nil), McpNamespace("my-server")+"__") {
		t.Fatal("expected tool name to start with server namespace")
	}
}
