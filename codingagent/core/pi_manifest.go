// Ported from packages/coding-agent/src/core/pi-manifest.ts (pi v1.5.2).

package core

import (
	"os"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// PiManifest holds resource declarations extracted from a package.json "pi" field.
type PiManifest struct {
	Extensions    []string `json:"extensions,omitzero"`
	Skills        []string `json:"skills,omitzero"`
	Prompts       []string `json:"prompts,omitzero"`
	Themes        []string `json:"themes,omitzero"`
	ChangelogPath *string  `json:"changelogPath,omitzero"`
}

var pmaResourceFields = []string{"extensions", "skills", "prompts", "themes"}

func pmaReadStringSlice(obj *jsonx.Object, key string) ([]string, bool) {
	val, ok := obj.Get(key)
	if !ok {
		return nil, false
	}
	arr, ok := val.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(arr))
	for i, elem := range arr {
		s, ok := elem.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// ReadPiManifest reads and parses the "pi" manifest section from packageJsonPath.
// Returns nil if the file is unreadable, invalid JSON, or lacks an object "pi" field.
func ReadPiManifest(packageJsonPath string) *PiManifest {
	raw, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return nil
	}
	parsed, err := jsonx.ParseString(utils.StripBom(string(raw)))
	if err != nil {
		return nil
	}
	pkgObj, ok := parsed.(*jsonx.Object)
	if !ok || pkgObj == nil {
		return nil
	}
	piRaw, ok := pkgObj.Get("pi")
	if !ok {
		return nil
	}
	piObj, ok := piRaw.(*jsonx.Object)
	if !ok || piObj == nil {
		return nil
	}

	manifest := &PiManifest{}
	for _, field := range pmaResourceFields {
		entries, ok := pmaReadStringSlice(piObj, field)
		if !ok {
			continue
		}
		switch field {
		case "extensions":
			manifest.Extensions = entries
		case "skills":
			manifest.Skills = entries
		case "prompts":
			manifest.Prompts = entries
		case "themes":
			manifest.Themes = entries
		}
	}

	if cpRaw, ok := piObj.Get("changelogPath"); ok {
		if cp, ok := cpRaw.(string); ok {
			manifest.ChangelogPath = &cp
		}
	}

	return manifest
}
