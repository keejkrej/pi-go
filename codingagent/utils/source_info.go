// Ported from packages/coding-agent/src/core/source-info.ts (pi v1.5.2).

package utils

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// SourceScope classifies where a resource comes from.
type SourceScope string

const (
	SourceScopeUser      SourceScope = "user"
	SourceScopeProject   SourceScope = "project"
	SourceScopeTemporary SourceScope = "temporary"
)

// SourceOrigin classifies whether a resource came from a package or top-level configuration.
type SourceOrigin string

const (
	SourceOriginPackage  SourceOrigin = "package"
	SourceOriginTopLevel SourceOrigin = "top-level"
)

// PathMetadata records package/source attribution for a discovered resource path.
// Owned by codingagent/utils/source_info.go per PORTING.md §2.2.
type PathMetadata struct {
	Source        string       `json:"source"`
	Scope         SourceScope  `json:"scope"`
	Origin        SourceOrigin `json:"origin"`
	BaseDir       string       `json:"baseDir,omitzero"`
	ChangelogPath string       `json:"changelogPath,omitzero"`
	PackageRoot   string       `json:"packageRoot,omitzero"`
}

// SourceInfo describes the provenance of a loaded resource.
type SourceInfo struct {
	Path          string       `json:"path"`
	Source        string       `json:"source"`
	Scope         SourceScope  `json:"scope"`
	Origin        SourceOrigin `json:"origin"`
	BaseDir       string       `json:"baseDir,omitzero"`
	ChangelogPath string       `json:"changelogPath,omitzero"`
}

// SourcePackageIdentity identifies a package source within a scope.
type SourcePackageIdentity struct {
	Source string      `json:"source"`
	Scope  SourceScope `json:"scope"`
}

// SyntheticSourceOptions supplies metadata for synthetic resources.
type SyntheticSourceOptions struct {
	Source        string
	Scope         SourceScope
	Origin        SourceOrigin
	BaseDir       string
	ChangelogPath string
}

// BuiltinPathPrefix is the prefix of built-in tool and extension paths, such as `builtin:read` or `builtin:mcp`.
const BuiltinPathPrefix = "builtin:"

// GetSourcePackageKey serializes identity to its canonical JSON key string.
func GetSourcePackageKey(identity SourcePackageIdentity) string {
	s, err := jsonx.Stringify(identity)
	if err != nil {
		return ""
	}
	return s
}

// GetSyntheticPathSource returns the source tag of a synthetic path (`builtin` for `builtin:*`
// or the prefix of `<prefix:name>`), or nil for filesystem paths.
func GetSyntheticPathSource(p string) *string {
	if strings.HasPrefix(p, BuiltinPathPrefix) {
		s := "builtin"
		return &s
	}
	if strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">") && len(p) >= 2 {
		inner := p[1 : len(p)-1]
		prefix := strings.SplitN(inner, ":", 2)[0]
		if prefix == "" {
			prefix = "temporary"
		}
		return &prefix
	}
	return nil
}

// IsSyntheticPath reports whether p is a builtin: or angle-bracket synthetic resource identifier.
func IsSyntheticPath(p string) bool {
	return strings.HasPrefix(p, BuiltinPathPrefix) || strings.HasPrefix(p, "<")
}

// GetResourcePathKey returns the deduplication key for p: synthetic paths unchanged,
// file paths via canonicalization and platform comparison key.
func GetResourcePathKey(p string) string {
	if IsSyntheticPath(p) {
		return p
	}
	return GetPathComparisonKey(CanonicalizePath(p))
}

// CreateSourceInfo constructs a SourceInfo from p and metadata.
func CreateSourceInfo(p string, metadata PathMetadata) SourceInfo {
	return SourceInfo{
		Path:          p,
		Source:        metadata.Source,
		Scope:         metadata.Scope,
		Origin:        metadata.Origin,
		BaseDir:       metadata.BaseDir,
		ChangelogPath: metadata.ChangelogPath,
	}
}

// CreateSyntheticSourceInfo constructs a SourceInfo for a non-file resource.
func CreateSyntheticSourceInfo(p string, options SyntheticSourceOptions) SourceInfo {
	scope := options.Scope
	if scope == "" {
		scope = SourceScopeTemporary
	}
	origin := options.Origin
	if origin == "" {
		origin = SourceOriginTopLevel
	}
	return SourceInfo{
		Path:          p,
		Source:        options.Source,
		Scope:         scope,
		Origin:        origin,
		BaseDir:       options.BaseDir,
		ChangelogPath: options.ChangelogPath,
	}
}
