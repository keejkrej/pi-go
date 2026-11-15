// Ported from packages/coding-agent/src/modes/interactive/theme/theme-schema.ts (pi v1.5.2).

package theme

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ThemeExportJson holds optional raw HTML export colors from a theme JSON file.
type ThemeExportJson struct {
	PageBg any `json:"pageBg,omitzero"`
	CardBg any `json:"cardBg,omitzero"`
	InfoBg any `json:"infoBg,omitzero"`
}

// ThemeJson represents a parsed and validated theme JSON document.
type ThemeJson struct {
	Schema     string           `json:"$schema,omitzero"`
	Name       string           `json:"name"`
	Appearance string           `json:"appearance,omitzero"`
	Vars       map[string]any   `json:"vars,omitzero"`
	Colors     map[string]any   `json:"colors"`
	Export     *ThemeExportJson `json:"export,omitzero"`
}

func tschemaObj(kv ...any) *jsonx.Object {
	o := jsonx.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			o.Set(k, kv[i+1])
		}
	}
	return o
}

func tschemaColorValue(description string) *typebox.Schema {
	strSchema := typebox.String(tschemaObj(
		"description", "Hex color (#RGB or #RRGGBB), OKLCH or OKHSL color, variable reference, or empty string for terminal default",
	))
	intSchema := typebox.Integer(tschemaObj(
		"minimum", float64(0),
		"maximum", float64(255),
		"description", "256-color palette index (0-255)",
	))
	if description != "" {
		return typebox.Union([]*typebox.Schema{strSchema, intSchema}, tschemaObj("description", description))
	}
	return typebox.Union([]*typebox.Schema{strSchema, intSchema})
}

func tschemaBuildThemeJsonSchema() *typebox.Schema {
	colorProps := omap.NewMap[string, *typebox.Schema]()
	allTokens := make([]string, 0, len(ThemeColorFgTokens)+len(ThemeColorBgTokens))
	allTokens = append(allTokens, ThemeColorFgTokens...)
	allTokens = append(allTokens, ThemeColorBgTokens...)

	for _, name := range allTokens {
		schema := tschemaColorValue("")
		if _, isOpt := ThemeOptionalFallbacks[name]; isOpt {
			colorProps.Set(name, typebox.Optional(schema))
		} else {
			colorProps.Set(name, schema)
		}
	}

	colorsSchema := typebox.Object(colorProps, tschemaObj(
		"description", "Theme color definitions (scrollbar, thinkingMax, and search highlight colors are optional and use compatible fallbacks)",
		"additionalProperties", false,
	))

	exportProps := omap.NewMap[string, *typebox.Schema]()
	exportProps.Set("pageBg", typebox.Optional(tschemaColorValue("Page background color")))
	exportProps.Set("cardBg", typebox.Optional(tschemaColorValue("Card/container background color")))
	exportProps.Set("infoBg", typebox.Optional(tschemaColorValue("Info sections background (system prompt, notices)")))
	exportSchema := typebox.Object(exportProps, tschemaObj(
		"description", "Optional colors for HTML export (defaults derived from userMessageBg if not specified)",
		"additionalProperties", false,
	))

	rootProps := omap.NewMap[string, *typebox.Schema]()
	rootProps.Set("$schema", typebox.Optional(typebox.String(tschemaObj("description", "JSON schema reference"))))
	rootProps.Set("name", typebox.String(tschemaObj(
		"pattern", "^[^/]+$",
		"description", "Theme name. Must not contain '/' because it is reserved for automatic light/dark theme settings.",
	)))
	rootProps.Set("appearance", typebox.Optional(typebox.Union(
		[]*typebox.Schema{typebox.Literal("dark"), typebox.Literal("light")},
		tschemaObj("description", "Background the theme is designed for. Detected from the theme colors when omitted."),
	)))
	rootProps.Set("vars", typebox.Optional(typebox.Record(
		typebox.String(),
		ColorValueSchema,
		tschemaObj("description", "Reusable color variables"),
	)))
	rootProps.Set("colors", colorsSchema)
	rootProps.Set("export", typebox.Optional(exportSchema))

	return typebox.Object(rootProps, tschemaObj(
		"title", "Pi Coding Agent Theme",
		"description", "Theme schema for Pi coding agent",
		"additionalProperties", false,
	))
}

var (
	// ColorValueSchema validates one theme color value (hex/OKLCH/OKHSL/var string or 0-255 integer).
	ColorValueSchema = tschemaColorValue("")
	// ThemeJsonSchema is the compiled TypeBox schema matching theme-schema.ts.
	ThemeJsonSchema = tschemaBuildThemeJsonSchema()

	tschemaOnce     sync.Once
	tschemaCompiled *typebox.Validator
	tschemaInitErr  error
)

func tschemaGetValidator() (*typebox.Validator, error) {
	tschemaOnce.Do(func() {
		v, err := typebox.Compile(ThemeJsonSchema)
		if err != nil {
			tschemaInitErr = err
			return
		}
		tschemaCompiled = v
	})
	return tschemaCompiled, tschemaInitErr
}

func tschemaToObjectMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		if m == nil {
			return nil, false
		}
		return m, true
	case *jsonx.Object:
		if m == nil {
			return nil, false
		}
		out := make(map[string]any, m.Len())
		for k, val := range m.All() {
			out[k] = val
		}
		return out, true
	default:
		return nil, false
	}
}

func tschemaToJsonxValue(v any) any {
	switch x := v.(type) {
	case *jsonx.Object:
		o := jsonx.NewObject()
		for k, val := range x.All() {
			o.Set(k, tschemaToJsonxValue(val))
		}
		return o
	case map[string]any:
		if x == nil {
			return nil
		}
		o := jsonx.NewObject()
		for k, val := range x {
			o.Set(k, tschemaToJsonxValue(val))
		}
		return o
	case map[string]string:
		if x == nil {
			return nil
		}
		o := jsonx.NewObject()
		for k, val := range x {
			o.Set(k, val)
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = tschemaToJsonxValue(el)
		}
		return out
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	default:
		return x
	}
}

// ValidateThemeJson validates a decoded theme JSON document against ThemeJsonSchema
// and converts it into a strongly-typed ThemeJson value.
func ValidateThemeJson(themeName string, raw any) (ThemeJson, error) {
	obj, isObj := tschemaToObjectMap(raw)
	if isObj {
		if nameVal, ok := obj["name"].(string); ok && strings.ContainsRune(nameVal, '/') {
			return ThemeJson{}, fmt.Errorf(
				`Invalid theme name "%s": theme names cannot contain "/" because it is reserved for automatic light/dark theme settings.`,
				nameVal,
			)
		}
	}

	validator, err := tschemaGetValidator()
	if err != nil {
		return ThemeJson{}, err
	}

	jsonxDoc := tschemaToJsonxValue(raw)
	if !validator.Check(jsonxDoc) {
		errs := validator.Errors(jsonxDoc)
		missingColors := make(map[string]struct{})
		var otherErrors []string

		for _, e := range errs {
			if e.Keyword == "required" && e.InstancePath == "/colors" && e.Params != nil {
				if reqAny, ok := e.Params.Get("requiredProperties"); ok {
					if arr, ok := reqAny.([]any); ok {
						for _, item := range arr {
							if s, ok := item.(string); ok {
								missingColors[s] = struct{}{}
							}
						}
					}
				}
				continue
			}

			path := e.InstancePath
			if path == "" {
				path = "/"
			}
			otherErrors = append(otherErrors, fmt.Sprintf("  - %s: %s", path, e.Message))
		}

		msg := fmt.Sprintf("Invalid theme %q:\n", themeName)
		if len(missingColors) > 0 {
			missingList := make([]string, 0, len(missingColors))
			for c := range missingColors {
				missingList = append(missingList, c)
			}
			sort.Strings(missingList)
			msg += "\nMissing required color tokens:\n"
			for i, c := range missingList {
				if i > 0 {
					msg += "\n"
				}
				msg += "  - " + c
			}
			msg += "\n\nPlease add these colors to your theme's \"colors\" object."
			msg += "\nSee the built-in themes (dark.json, light.json) for reference values."
		}
		if len(otherErrors) > 0 {
			msg += "\n\nOther errors:\n" + strings.Join(otherErrors, "\n")
		}
		return ThemeJson{}, fmt.Errorf("%s", msg)
	}

	var out ThemeJson
	if !isObj {
		return out, fmt.Errorf("Invalid theme %q:\n\nOther errors:\n  - /: must be object", themeName)
	}
	if s, ok := obj["$schema"].(string); ok {
		out.Schema = s
	}
	if n, ok := obj["name"].(string); ok {
		out.Name = n
	}
	if a, ok := obj["appearance"].(string); ok {
		out.Appearance = a
	}
	if varsMap, ok := tschemaToObjectMap(obj["vars"]); ok {
		out.Vars = varsMap
	}
	if colorsMap, ok := tschemaToObjectMap(obj["colors"]); ok {
		out.Colors = colorsMap
	}
	if expMap, ok := tschemaToObjectMap(obj["export"]); ok {
		out.Export = &ThemeExportJson{
			PageBg: expMap["pageBg"],
			CardBg: expMap["cardBg"],
			InfoBg: expMap["infoBg"],
		}
	}
	return out, nil
}
