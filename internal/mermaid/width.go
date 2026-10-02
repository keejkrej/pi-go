package mermaid

import "github.com/rivo/uniseg"

// codePointWidth is the width of one code point from the unicode-width table.
func codePointWidth(cp rune) int {
	lo, hi := 0, len(widths)-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		run := widths[mid]
		switch {
		case cp < run[0]:
			hi = mid - 1
		case cp > run[1]:
			lo = mid + 1
		default:
			return int(run[2])
		}
	}
	return 1
}

func isAlnumRune(r rune) bool {
	lo, hi := 0, len(alnumRanges)-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		rg := alnumRanges[mid]
		switch {
		case r < rg[0]:
			hi = mid - 1
		case r > rg[1]:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

const vs16 rune = 0xfe0f

func isRegionalIndicator(cp rune) bool { return cp >= 0x1f1e6 && cp <= 0x1f1ff }

// clusterWidth is the display columns of one grapheme cluster.
func clusterWidth(cluster string) int {
	w := 0
	vs := false
	regional := 0
	for _, cp := range cluster {
		if cp == vs16 {
			vs = true
		}
		if isRegionalIndicator(cp) {
			regional++
		}
		if cw := codePointWidth(cp); cw > w {
			w = cw
		}
	}
	if vs || regional >= 2 {
		return 2
	}
	return w
}

type measuredCluster struct {
	s string
	w int
}

func measured(s string) []measuredCluster {
	var out []measuredCluster
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		seg := g.Str()
		out = append(out, measuredCluster{seg, clusterWidth(seg)})
	}
	return out
}

func stringWidth(s string) int {
	w := 0
	for _, c := range measured(s) {
		w += c.w
	}
	return w
}
