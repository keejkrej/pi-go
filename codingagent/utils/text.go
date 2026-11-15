// Ported from packages/coding-agent/src/utils/text.ts (pi v1.5.2).

package utils

import "strings"

const textUTF8BOM = "\uFEFF"

// BomSplit holds the optional leading UTF-8 BOM and remaining text.
type BomSplit struct {
	Bom  string
	Text string
}

// SplitBom splits a leading UTF-8 byte order mark from decoded text.
func SplitBom(content string) BomSplit {
	if strings.HasPrefix(content, textUTF8BOM) {
		return BomSplit{
			Bom:  textUTF8BOM,
			Text: content[len(textUTF8BOM):],
		}
	}
	return BomSplit{Bom: "", Text: content}
}

// StripBom removes a leading UTF-8 byte order mark from decoded text.
func StripBom(content string) string {
	return SplitBom(content).Text
}
