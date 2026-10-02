// Package tools provides agentloop.AgentTool implementations over local file
// IO and search: read, write, edit, grep, and find.
//
// Each constructor takes a base/working directory (cwd); relative path
// arguments are resolved against it. Tools honor context cancellation and
// return Go errors for IO failures so the agent loop converts them into error
// tool results, rather than baking error strings into normal content.
package tools

import "path/filepath"

// ObjectSchema builds a JSON-Schema object with the given properties and
// required keys.
func ObjectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		schema["required"] = req
	}
	return schema
}

// StringProp builds a JSON-Schema string property with the given description.
func StringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// NumberProp builds a JSON-Schema number property with the given description.
func NumberProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

// BoolProp builds a JSON-Schema boolean property with the given description.
func BoolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// ignoreDirs are directory names skipped during recursive walks.
var ignoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// resolvePath resolves p against cwd when p is relative.
func resolvePath(cwd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

// stringArg reads a string-typed argument from validated params.
func stringArg(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

// numberArg reads a numeric argument (decoded as float64) from validated
// params. It returns (value, true) only when the key is present and numeric.
func numberArg(params map[string]any, key string) (float64, bool) {
	switch v := params[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// boolArg reads a boolean argument from validated params.
func boolArg(params map[string]any, key string) bool {
	if v, ok := params[key].(bool); ok {
		return v
	}
	return false
}
