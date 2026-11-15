// Ported from packages/coding-agent/src/core/tools/path-utils.ts (pi v1.0.0).

package core

import (
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"golang.org/x/text/unicode/norm"
)

const tpuNarrowNoBreakSpace = "\u202F"

var tpuMacOSScreenshotRe = regexp.MustCompile(`(?i) (AM|PM)\.`)

func tpuTryMacOSScreenshotPath(filePath string) string {
	return tpuMacOSScreenshotRe.ReplaceAllString(filePath, tpuNarrowNoBreakSpace+"$1.")
}

func tpuTryNFDVariant(filePath string) string {
	return norm.NFD.String(filePath)
}

func tpuTryCurlyQuoteVariant(filePath string) string {
	return strings.ReplaceAll(filePath, "'", "\u2019")
}

func tpuFileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

// PathExists checks whether filePath exists synchronously (respecting ctx cancellation).
func PathExists(ctx context.Context, filePath string) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	return tpuFileExists(filePath)
}

// ExpandPath normalizes Unicode spaces, strips a leading '@', and expands '~' / file URLs.
func ExpandPath(filePath string) string {
	resolved, err := utils.NormalizePath(filePath, &utils.PathInputOptions{
		NormalizeUnicodeSpaces: true,
		StripAtPrefix:          true,
	})
	if err != nil {
		return filePath
	}
	return resolved
}

// ResolveToCwd resolves filePath relative to cwd, handling '~' expansion and Unicode space normalization.
func ResolveToCwd(filePath, cwd string) string {
	resolved, err := utils.ResolvePath(filePath, cwd, &utils.PathInputOptions{
		NormalizeUnicodeSpaces: true,
		StripAtPrefix:          true,
	})
	if err != nil {
		return ExpandPath(filePath)
	}
	return resolved
}

// ResolveReadPath resolves a path for reading, falling back through macOS AM/PM, NFD, and curly-quote variants.
func ResolveReadPath(filePath, cwd string) string {
	resolved := ResolveToCwd(filePath, cwd)

	if tpuFileExists(resolved) {
		return resolved
	}

	amPmVariant := tpuTryMacOSScreenshotPath(resolved)
	if amPmVariant != resolved && tpuFileExists(amPmVariant) {
		return amPmVariant
	}

	nfdVariant := tpuTryNFDVariant(resolved)
	if nfdVariant != resolved && tpuFileExists(nfdVariant) {
		return nfdVariant
	}

	curlyVariant := tpuTryCurlyQuoteVariant(resolved)
	if curlyVariant != resolved && tpuFileExists(curlyVariant) {
		return curlyVariant
	}

	nfdCurlyVariant := tpuTryCurlyQuoteVariant(nfdVariant)
	if nfdCurlyVariant != resolved && tpuFileExists(nfdCurlyVariant) {
		return nfdCurlyVariant
	}

	return resolved
}

// ResolveReadPathAsync resolves a path for reading with context awareness.
func ResolveReadPathAsync(ctx context.Context, filePath, cwd string) string {
	if ctx != nil && ctx.Err() != nil {
		return ResolveToCwd(filePath, cwd)
	}
	return ResolveReadPath(filePath, cwd)
}
