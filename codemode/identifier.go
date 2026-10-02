// Ported from packages/codemode/src/identifier.ts (pi v1.0.0).

package codemode

// ToCodemodeIdentifier is the identifier a script uses for a tool: characters that
// are not valid in a JavaScript identifier become `_`. `mcp__docs__search` stays
// as is, `my-tool` becomes `my_tool`. An empty name becomes `_`.
func ToCodemodeIdentifier(name string) string {
	panic("unported: ToCodemodeIdentifier")
}
