// Ported from packages/durable/src/tools/path-utils.ts (pi v1.0.0).

package tools

import (
	"regexp"
	"strings"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
	"golang.org/x/text/unicode/norm"
)

const tpthNarrowNoBreakSpace = "\u202F"

var (
	tpthUnicodeSpacesRe = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")
	tpthAmPmRe          = regexp.MustCompile(`(?i) (AM|PM)\.`)
)

func tpthNormalizeToolPath(path string) string {
	normalized := tpthUnicodeSpacesRe.ReplaceAllString(path, " ")
	return strings.TrimPrefix(normalized, "@")
}

// ResolveToolPath normalizes and resolves a tool path to an absolute path in executionEnv.
func ResolveToolPath(executionEnv env.ExecutionEnv, path string, ctx chord.Context) (string, error) {
	return env.GetOrThrow(executionEnv.AbsolutePath(tpthNormalizeToolPath(path), ctx))
}

// ResolveReadToolPath resolves a tool path and checks macOS filename normalization variants if needed.
func ResolveReadToolPath(executionEnv env.ExecutionEnv, path string, ctx chord.Context) (string, error) {
	resolved, err := ResolveToolPath(executionEnv, path, ctx)
	if err != nil {
		return "", err
	}

	nfd := norm.NFD.String(resolved)
	variants := []string{
		resolved,
		tpthAmPmRe.ReplaceAllString(resolved, tpthNarrowNoBreakSpace+"$1."),
		nfd,
		strings.ReplaceAll(resolved, "'", "\u2019"),
		strings.ReplaceAll(nfd, "'", "\u2019"),
	}

	seen := make(map[string]struct{}, len(variants))
	for _, variant := range variants {
		if _, ok := seen[variant]; ok {
			continue
		}
		seen[variant] = struct{}{}
		exists, err := env.GetOrThrow(executionEnv.Exists(variant, ctx))
		if err != nil {
			return "", err
		}
		if exists {
			return variant, nil
		}
	}
	return resolved, nil
}
