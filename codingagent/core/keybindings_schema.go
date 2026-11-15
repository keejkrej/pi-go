// Ported from packages/coding-agent/src/core/keybindings-schema.ts (pi v1.5.2).

package core

import (
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// KeybindingsSchema is the TypeBox JSON Schema for keybindings.json.
var KeybindingsSchema = func() *typebox.Schema {
	properties := omap.NewMap[string, *typebox.Schema]()
	properties.Set("$schema", typebox.Optional(typebox.String()))

	for _, id := range Keybindings.Keys() {
		def, _ := Keybindings.Get(id)
		var opts []*jsonx.Object
		if def != nil && def.Description != nil {
			opts = append(opts, jsonx.ObjectOf("description", *def.Description))
		}
		properties.Set(id, typebox.Optional(NewKeybindingValueSchema(opts...)))
	}

	return typebox.Object(properties, jsonx.ObjectOf(
		"additionalProperties", KeybindingValueSchema,
	))
}()
