package jsdiff

import "strings"

// Ported from patch/line-endings.js and patch/reverse.js.

func copyPatchWithLines(patch *StructuredPatch, mapLines func(lines []string) []string) *StructuredPatch {
	out := *patch
	out.Hunks = make([]StructuredPatchHunk, len(patch.Hunks))
	for i, hunk := range patch.Hunks {
		hunk.Lines = mapLines(hunk.Lines)
		out.Hunks[i] = hunk
	}
	return &out
}

// unixToWin converts a patch with Unix line endings to Windows ones.
func unixToWin(patch *StructuredPatch) *StructuredPatch {
	return copyPatchWithLines(patch, func(lines []string) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			if strings.HasPrefix(line, `\`) || strings.HasSuffix(line, "\r") ||
				(i+1 < len(lines) && strings.HasPrefix(lines[i+1], `\`)) {
				out[i] = line
			} else {
				out[i] = line + "\r"
			}
		}
		return out
	})
}

// winToUnix converts a patch with Windows line endings to Unix ones.
func winToUnix(patch *StructuredPatch) *StructuredPatch {
	return copyPatchWithLines(patch, func(lines []string) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = strings.TrimSuffix(line, "\r")
		}
		return out
	})
}

// isUnix returns true if the patch consistently uses Unix line endings (or only involves
// one line and has no line endings).
func isUnix(patch *StructuredPatch) bool {
	for _, hunk := range patch.Hunks {
		for _, line := range hunk.Lines {
			if !strings.HasPrefix(line, `\`) && strings.HasSuffix(line, "\r") {
				return false
			}
		}
	}
	return true
}

// isWin returns true if the patch uses Windows line endings and only Windows line
// endings.
func isWin(patch *StructuredPatch) bool {
	some := false
	for _, hunk := range patch.Hunks {
		for _, line := range hunk.Lines {
			if strings.HasSuffix(line, "\r") {
				some = true
			}
		}
	}
	if !some {
		return false
	}
	for _, hunk := range patch.Hunks {
		for i, line := range hunk.Lines {
			if !(strings.HasPrefix(line, `\`) || strings.HasSuffix(line, "\r") ||
				(i+1 < len(hunk.Lines) && strings.HasPrefix(hunk.Lines[i+1], `\`))) {
				return false
			}
		}
	}
	return true
}

// ReversePatch returns a patch that undoes patch: old and new file names, headers and
// ranges are swapped and '+'/'-' lines are exchanged.
func ReversePatch(structuredPatch *StructuredPatch) *StructuredPatch {
	out := *structuredPatch
	out.OldFileName, out.NewFileName = structuredPatch.NewFileName, structuredPatch.OldFileName
	out.OldHeader, out.NewHeader = structuredPatch.NewHeader, structuredPatch.OldHeader
	out.Hunks = make([]StructuredPatchHunk, len(structuredPatch.Hunks))
	for i, hunk := range structuredPatch.Hunks {
		lines := make([]string, len(hunk.Lines))
		for k, l := range hunk.Lines {
			switch {
			case strings.HasPrefix(l, "-"):
				lines[k] = "+" + l[1:]
			case strings.HasPrefix(l, "+"):
				lines[k] = "-" + l[1:]
			default:
				lines[k] = l
			}
		}
		out.Hunks[i] = StructuredPatchHunk{
			OldLines: hunk.NewLines,
			OldStart: hunk.NewStart,
			NewLines: hunk.OldLines,
			NewStart: hunk.OldStart,
			Lines:    lines,
		}
	}
	return &out
}

// ReversePatchList reverses each patch of a multi-file patch and the order of the
// patches.
func ReversePatchList(structuredPatches []*StructuredPatch) []*StructuredPatch {
	out := make([]*StructuredPatch, len(structuredPatches))
	for i, p := range structuredPatches {
		out[len(out)-1-i] = ReversePatch(p)
	}
	return out
}
