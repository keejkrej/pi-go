// Ported from packages/coding-agent/src/utils/changelog.ts (pi v1.5.2).

package utils

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
)

// ChangelogEntry represents a single release section parsed from CHANGELOG.md.
type ChangelogEntry struct {
	Major   int
	Minor   int
	Patch   int
	Content string
}

const chanhCanonicalRepo = "https://github.com/earendil-works/pi"

var (
	chanhHeadingRe       = regexp.MustCompile(`^## \[?(\d+)\.(\d+)\.(\d+)\]?`)
	chanhSemverPrefixRe  = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)
	chanhMdLinkRe        = regexp.MustCompile(`(!?\[[^\]\n]+\]\()([^\s)]+)((?:\s+[^)]*)?\))`)
	chanhLegacyRepoUrlRe = regexp.MustCompile(`^https://github\.com/(?:badlogic|earendil-works)/pi-mono(/|$)`)
	chanhSchemeRe        = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// ParseChangelog reads and parses version sections from changelogPath in file order.
func ParseChangelog(changelogPath string) ([]ChangelogEntry, error) {
	raw, err := os.ReadFile(changelogPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var entries []ChangelogEntry
	var current *ChangelogEntry
	var buf []string

	flush := func() {
		if current == nil {
			return
		}
		current.Content = strings.TrimSpace(strings.Join(buf, "\n"))
		entries = append(entries, *current)
	}

	for _, line := range lines {
		if m := chanhHeadingRe.FindStringSubmatch(line); len(m) == 4 {
			flush()
			maj, _ := strconv.Atoi(m[1])
			min, _ := strconv.Atoi(m[2])
			pat, _ := strconv.Atoi(m[3])
			current = &ChangelogEntry{Major: maj, Minor: min, Patch: pat}
			buf = []string{line}
			continue
		}
		if current != nil {
			if strings.HasPrefix(line, "## ") {
				flush()
				current = nil
				buf = nil
				continue
			}
			buf = append(buf, line)
		}
	}
	flush()
	return entries, nil
}

func chanhParseVersionTuple(v string) (major, minor, patch int, ok bool) {
	m := chanhSemverPrefixRe.FindStringSubmatch(strings.TrimSpace(v))
	if len(m) != 4 {
		return 0, 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	patch, _ = strconv.Atoi(m[3])
	return major, minor, patch, true
}

func chanhCompareTuples(aMaj, aMin, aPat, bMaj, bMin, bPat int) int {
	if aMaj != bMaj {
		if aMaj > bMaj {
			return 1
		}
		return -1
	}
	if aMin != bMin {
		if aMin > bMin {
			return 1
		}
		return -1
	}
	if aPat != bPat {
		if aPat > bPat {
			return 1
		}
		return -1
	}
	return 0
}

// GetNewEntries returns entries newer than lastVersion. Returns (nil, false) when
// lastVersion does not contain a valid major.minor.patch core version.
func GetNewEntries(entries []ChangelogEntry, lastVersion string) ([]ChangelogEntry, bool) {
	lMaj, lMin, lPat, ok := chanhParseVersionTuple(lastVersion)
	if !ok {
		return nil, false
	}
	result := make([]ChangelogEntry, 0)
	for _, entry := range entries {
		if chanhCompareTuples(entry.Major, entry.Minor, entry.Patch, lMaj, lMin, lPat) > 0 {
			result = append(result, entry)
		}
	}
	return result, true
}

func chanhNormalizeLinkTarget(target string, tag string) string {
	canonicalTarget := chanhLegacyRepoUrlRe.ReplaceAllString(target, chanhCanonicalRepo+"$1")
	for _, route := range []string{"blob", "tree"} {
		for _, branch := range []string{"main", "master"} {
			prefix := chanhCanonicalRepo + "/" + route + "/" + branch + "/"
			if strings.HasPrefix(canonicalTarget, prefix) {
				return chanhCanonicalRepo + "/" + route + "/" + tag + "/" + canonicalTarget[len(prefix):]
			}
		}
	}

	if strings.HasPrefix(canonicalTarget, "#") || strings.HasPrefix(canonicalTarget, "//") || chanhSchemeRe.MatchString(canonicalTarget) {
		return canonicalTarget
	}

	hashIdx := strings.IndexByte(canonicalTarget, '#')
	beforeHash := canonicalTarget
	fragment := ""
	if hashIdx >= 0 {
		beforeHash = canonicalTarget[:hashIdx]
		fragment = canonicalTarget[hashIdx:]
	}

	queryIdx := strings.IndexByte(beforeHash, '?')
	pathPart := beforeHash
	query := ""
	if queryIdx >= 0 {
		pathPart = beforeHash[:queryIdx]
		query = beforeHash[queryIdx:]
	}

	if pathPart == "" {
		return canonicalTarget
	}

	normPath := strings.ReplaceAll(pathPart, "\\", "/")
	trailingSlash := strings.HasSuffix(normPath, "/")
	var repoPath string
	if strings.HasPrefix(normPath, "/") {
		repoPath = path.Clean(strings.TrimLeft(normPath, "/"))
	} else {
		repoPath = path.Join("packages/coding-agent", normPath)
	}
	if repoPath == "" || repoPath == "." || repoPath == ".." || strings.HasPrefix(repoPath, "../") {
		return canonicalTarget
	}
	if trailingSlash && !strings.HasSuffix(repoPath, "/") {
		repoPath += "/"
	}

	route := "blob"
	if strings.HasSuffix(pathPart, "/") || !strings.Contains(path.Base(repoPath), ".") {
		route = "tree"
	}
	return chanhCanonicalRepo + "/" + route + "/" + tag + "/" + js.EncodeURI(repoPath) + query + fragment
}

// NormalizeChangelogLinks canonicalizes legacy repository URLs and rewrites
// package-relative Markdown links to tag-pinned GitHub links for version.
func NormalizeChangelogLinks(markdown string, version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return markdown
	}
	tag := trimmed
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return chanhMdLinkRe.ReplaceAllStringFunc(markdown, func(m string) string {
		parts := chanhMdLinkRe.FindStringSubmatch(m)
		if len(parts) != 4 {
			return m
		}
		return parts[1] + chanhNormalizeLinkTarget(parts[2], tag) + parts[3]
	})
}

// NormalizeChangelogLinksForEntry rewrites Markdown links in markdown using entry's version tag.
func NormalizeChangelogLinksForEntry(markdown string, entry ChangelogEntry) string {
	return NormalizeChangelogLinks(markdown, fmt.Sprintf("%d.%d.%d", entry.Major, entry.Minor, entry.Patch))
}
