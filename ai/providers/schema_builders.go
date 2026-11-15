// Ported from packages/ai/src/providers/schema-builders.ts (pi v1.5.2).

package providers

import (
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// sbFilterOpenObjectOpts strips additionalProperties unless true, matching schema-builders.ts openObject.
func sbFilterOpenObjectOpts(options []*jsonx.Object) []*jsonx.Object {
	if len(options) == 0 {
		return nil
	}
	out := make([]*jsonx.Object, 0, len(options))
	for _, opt := range options {
		if opt == nil {
			continue
		}
		if v, ok := opt.Get("additionalProperties"); ok {
			if b, isBool := v.(bool); !isBool || !b {
				cp := opt.Clone()
				cp.Delete("additionalProperties")
				out = append(out, cp)
				continue
			}
		}
		out = append(out, opt)
	}
	return out
}

// OpenObject builds an open JSON object schema with optional annotations and additionalProperties: true.
func OpenObject(properties *omap.Map[string, *typebox.Schema], options ...*jsonx.Object) *typebox.Schema {
	return typebox.Object(properties, sbFilterOpenObjectOpts(options)...)
}

// StringLiteral builds { type: "string", const: value, ...options }.
func StringLiteral(value string, options ...*jsonx.Object) *typebox.Schema {
	return typebox.Literal(value, options...)
}

// StringRecord builds { type: "object", patternProperties: { "^.*$": value }, ...options }.
func StringRecord(value *typebox.Schema, options ...*jsonx.Object) *typebox.Schema {
	return typebox.Record(typebox.Any(), value, options...)
}
