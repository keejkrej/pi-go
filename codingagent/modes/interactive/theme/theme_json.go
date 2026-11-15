// Ported from packages/coding-agent/src/modes/interactive/theme/theme-json.ts (pi v1.5.2).

package theme

import (
	"fmt"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ParseAndValidateThemeJson decodes raw JSON bytes and validates them as a ThemeJson.
func ParseAndValidateThemeJson(themeName string, data []byte) (ThemeJson, error) {
	clean := utils.StripBom(string(data))
	var raw any
	if err := jsonx.Unmarshal([]byte(clean), &raw); err != nil {
		return ThemeJson{}, fmt.Errorf("Invalid theme %q: %w", themeName, err)
	}
	return ValidateThemeJson(themeName, raw)
}
