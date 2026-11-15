// Ported from packages/tui/src/utils.ts (pi v1.5.2).

package tui

import (
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/eastasian"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/rivo/uniseg"
)

type SegmentData struct {
	Segment    string `json:"segment"`
	Index      int    `json:"index"`
	Input      string `json:"input"`
	IsWordLike *bool  `json:"isWordLike,omitzero"`
}

type Segmenter interface {
	Segment(input string) []SegmentData
}

type utilsGraphemeSegmenter struct{}
type utilsWordSegmenter struct{}

var (
	utilsDefaultGraphemeSegmenter Segmenter = utilsGraphemeSegmenter{}
	utilsDefaultWordSegmenter     Segmenter = utilsWordSegmenter{}
)

func GetGraphemeSegmenter() Segmenter {
	return utilsDefaultGraphemeSegmenter
}

func GetWordSegmenter() Segmenter {
	return utilsDefaultWordSegmenter
}

// Segment implements Segmenter for Unicode grapheme clusters (UAX #29 plus GB9c Indic conjuncts).
func (utilsGraphemeSegmenter) Segment(input string) []SegmentData {
	clusters := utilsSplitGraphemes(input)
	out := make([]SegmentData, len(clusters))
	u16Offset := 0
	for i, c := range clusters {
		out[i] = SegmentData{
			Segment: c,
			Index:   u16Offset,
			Input:   input,
		}
		u16Offset += js.Len(c)
	}
	return out
}

// Segment implements Segmenter for Unicode word boundaries (UAX #29).
func (utilsWordSegmenter) Segment(input string) []SegmentData {
	if input == "" {
		return nil
	}
	var out []SegmentData
	rest := input
	state := -1
	u16Offset := 0
	for rest != "" {
		var word string
		word, rest, state = uniseg.FirstWordInString(rest, state)
		if word == "" {
			break
		}
		wordLike := utilsIsWordLikeSegment(word)
		out = append(out, SegmentData{
			Segment:    word,
			Index:      u16Offset,
			Input:      input,
			IsWordLike: &wordLike,
		})
		u16Offset += js.Len(word)
	}
	return out
}

func utilsIsWordLikeSegment(seg string) bool {
	for _, r := range seg {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// utilsSplitGraphemes splits s using uniseg and merges GB9c Indic conjunct sequences
// (InCB=Consonant (InCB=Extend|InCB=Linker)* InCB=Linker (InCB=Extend|InCB=Linker)* x InCB=Consonant)
// which uniseg@v0.4.7 (Unicode 15.0) splits prior to Unicode 15.1.
func utilsSplitGraphemes(s string) []string {
	if s == "" {
		return nil
	}
	var raw []string
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		raw = append(raw, g.Str())
	}
	if len(raw) <= 1 {
		return raw
	}
	merged := make([]string, 0, len(raw))
	for _, cluster := range raw {
		if len(merged) > 0 && utilsEndsWithConjunctPrefix(merged[len(merged)-1]) && utilsStartsWithInCBConsonant(cluster) {
			merged[len(merged)-1] += cluster
			continue
		}
		merged = append(merged, cluster)
	}
	return merged
}

func utilsStartsWithInCBConsonant(cluster string) bool {
	r, _ := utf8.DecodeRuneInString(cluster)
	return utilsInRanges(r, utilsInCBConsonantRanges)
}

func utilsEndsWithConjunctPrefix(cluster string) bool {
	const (
		stNone = iota
		stConsonant
		stLinker
	)
	state := stNone
	for _, r := range cluster {
		if utilsInRanges(r, utilsInCBConsonantRanges) {
			state = stConsonant
		} else if utilsIsInCBLinker(r) {
			if state == stConsonant || state == stLinker {
				state = stLinker
			} else {
				state = stNone
			}
		} else if utilsIsInCBExtend(r) {
			// Preserve current state (Consonant or Linker) across Extend marks.
		} else {
			state = stNone
		}
	}
	return state == stLinker
}

func utilsIsInCBLinker(r rune) bool {
	return slices.Contains(utilsInCBLinkerRunes, r)
}

func utilsIsInCBExtend(r rune) bool {
	if r == 0x200C {
		return false
	}
	if r == 0x200D {
		return true
	}
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r)
}

func utilsInRanges(r rune, ranges [][2]rune) bool {
	lo, hi := 0, len(ranges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		pair := ranges[mid]
		if r < pair[0] {
			hi = mid - 1
		} else if r > pair[1] {
			lo = mid + 1
		} else {
			return true
		}
	}
	return false
}

var utilsInCBLinkerRunes = []rune{
	0x094d, 0x09cd, 0x0acd, 0x0b4d, 0x0c4d, 0x0d4d, 0x1039, 0x17d2,
	0x1a60, 0x1b44, 0x1bab, 0xa9c0, 0xaaf6, 0x10a3f, 0x11133, 0x113d0,
	0x1193e, 0x11a47, 0x11a99, 0x11f42,
}

var utilsInCBConsonantRanges = [][2]rune{
	{0x0915, 0x0939}, {0x0958, 0x095f}, {0x0978, 0x097f}, {0x0995, 0x09a8},
	{0x09aa, 0x09b0}, {0x09b2, 0x09b2}, {0x09b6, 0x09b9}, {0x09dc, 0x09dd},
	{0x09df, 0x09df}, {0x09f0, 0x09f1}, {0x0a95, 0x0aa8}, {0x0aaa, 0x0ab0},
	{0x0ab2, 0x0ab3}, {0x0ab5, 0x0ab9}, {0x0af9, 0x0af9}, {0x0b15, 0x0b28},
	{0x0b2a, 0x0b30}, {0x0b32, 0x0b33}, {0x0b35, 0x0b39}, {0x0b5c, 0x0b5d},
	{0x0b5f, 0x0b5f}, {0x0b71, 0x0b71}, {0x0c15, 0x0c28}, {0x0c2a, 0x0c39},
	{0x0c58, 0x0c5a}, {0x0d15, 0x0d3a}, {0x1000, 0x102a}, {0x103f, 0x103f},
	{0x1050, 0x1055}, {0x105a, 0x105d}, {0x1061, 0x1061}, {0x1065, 0x1066},
	{0x106e, 0x1070}, {0x1075, 0x1081}, {0x108e, 0x108e}, {0x1780, 0x17b3},
	{0x1a20, 0x1a54}, {0x1b0b, 0x1b0c}, {0x1b13, 0x1b33}, {0x1b45, 0x1b4c},
	{0x1b83, 0x1ba0}, {0x1bae, 0x1baf}, {0x1bbb, 0x1bbd}, {0xa989, 0xa98b},
	{0xa98f, 0xa9b2}, {0xa9e0, 0xa9e4}, {0xa9e7, 0xa9ef}, {0xa9fa, 0xa9fe},
	{0xaa60, 0xaa6f}, {0xaa71, 0xaa73}, {0xaa7a, 0xaa7a}, {0xaa7e, 0xaa7f},
	{0xaae0, 0xaaea}, {0xabc0, 0xabda}, {0x10a00, 0x10a00}, {0x10a10, 0x10a13},
	{0x10a15, 0x10a17}, {0x10a19, 0x10a35}, {0x11103, 0x11126}, {0x11144, 0x11144},
	{0x11147, 0x11147}, {0x11380, 0x11389}, {0x1138b, 0x1138b}, {0x1138e, 0x1138e},
	{0x11390, 0x113b5}, {0x11900, 0x11906}, {0x11909, 0x11909}, {0x1190c, 0x11913},
	{0x11915, 0x11916}, {0x11918, 0x1192f}, {0x11a00, 0x11a00}, {0x11a0b, 0x11a32},
	{0x11a50, 0x11a50}, {0x11a5c, 0x11a83}, {0x11f04, 0x11f10}, {0x11f12, 0x11f33},
}

var utilsDefaultIgnorableRanges = [][2]rune{
	{0xad, 0xad},
	{0x34f, 0x34f},
	{0x61c, 0x61c},
	{0x115f, 0x1160},
	{0x17b4, 0x17b5},
	{0x180b, 0x180f},
	{0x200b, 0x200f},
	{0x202a, 0x202e},
	{0x2060, 0x206f},
	{0x3164, 0x3164},
	{0xfe00, 0xfe0f},
	{0xfeff, 0xfeff},
	{0xffa0, 0xffa0},
	{0xfff0, 0xfff8},
	{0x1bca0, 0x1bca3},
	{0x1d173, 0x1d17a},
	{0xe0000, 0xe0fff},
}

var utilsEmojiPresentationRanges = [][2]rune{
	{0x231a, 0x231b},
	{0x23e9, 0x23ec},
	{0x23f0, 0x23f0},
	{0x23f3, 0x23f3},
	{0x25fd, 0x25fe},
	{0x2614, 0x2615},
	{0x2648, 0x2653},
	{0x267f, 0x267f},
	{0x2693, 0x2693},
	{0x26a1, 0x26a1},
	{0x26aa, 0x26ab},
	{0x26bd, 0x26be},
	{0x26c4, 0x26c5},
	{0x26ce, 0x26ce},
	{0x26d4, 0x26d4},
	{0x26ea, 0x26ea},
	{0x26f2, 0x26f3},
	{0x26f5, 0x26f5},
	{0x26fa, 0x26fa},
	{0x26fd, 0x26fd},
	{0x2705, 0x2705},
	{0x270a, 0x270b},
	{0x2728, 0x2728},
	{0x274c, 0x274c},
	{0x274e, 0x274e},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2795, 0x2797},
	{0x27b0, 0x27b0},
	{0x27bf, 0x27bf},
	{0x2b1b, 0x2b1c},
	{0x2b50, 0x2b50},
	{0x2b55, 0x2b55},
	{0x1f004, 0x1f004},
	{0x1f0cf, 0x1f0cf},
	{0x1f18e, 0x1f18e},
	{0x1f191, 0x1f19a},
	{0x1f1e6, 0x1f1ff},
	{0x1f201, 0x1f201},
	{0x1f21a, 0x1f21a},
	{0x1f22f, 0x1f22f},
	{0x1f232, 0x1f236},
	{0x1f238, 0x1f23a},
	{0x1f250, 0x1f251},
	{0x1f300, 0x1f320},
	{0x1f32d, 0x1f335},
	{0x1f337, 0x1f37c},
	{0x1f37e, 0x1f393},
	{0x1f3a0, 0x1f3ca},
	{0x1f3cf, 0x1f3d3},
	{0x1f3e0, 0x1f3f0},
	{0x1f3f4, 0x1f3f4},
	{0x1f3f8, 0x1f43e},
	{0x1f440, 0x1f440},
	{0x1f442, 0x1f4fc},
	{0x1f4ff, 0x1f53d},
	{0x1f54b, 0x1f54e},
	{0x1f550, 0x1f567},
	{0x1f57a, 0x1f57a},
	{0x1f595, 0x1f596},
	{0x1f5a4, 0x1f5a4},
	{0x1f5fb, 0x1f64f},
	{0x1f680, 0x1f6c5},
	{0x1f6cc, 0x1f6cc},
	{0x1f6d0, 0x1f6d2},
	{0x1f6d5, 0x1f6d8},
	{0x1f6dc, 0x1f6df},
	{0x1f6eb, 0x1f6ec},
	{0x1f6f4, 0x1f6fc},
	{0x1f7e0, 0x1f7eb},
	{0x1f7f0, 0x1f7f0},
	{0x1f90c, 0x1f93a},
	{0x1f93c, 0x1f945},
	{0x1f947, 0x1f9ff},
	{0x1fa70, 0x1fa7c},
	{0x1fa80, 0x1fa8a},
	{0x1fa8e, 0x1fac6},
	{0x1fac8, 0x1fac8},
	{0x1facd, 0x1fadc},
	{0x1fadf, 0x1faea},
	{0x1faef, 0x1faf8},
}

var utilsEmojiRanges = [][2]rune{
	{0x23, 0x23},
	{0x2a, 0x2a},
	{0x30, 0x39},
	{0xa9, 0xa9},
	{0xae, 0xae},
	{0x203c, 0x203c},
	{0x2049, 0x2049},
	{0x2122, 0x2122},
	{0x2139, 0x2139},
	{0x2194, 0x2199},
	{0x21a9, 0x21aa},
	{0x231a, 0x231b},
	{0x2328, 0x2328},
	{0x23cf, 0x23cf},
	{0x23e9, 0x23f3},
	{0x23f8, 0x23fa},
	{0x24c2, 0x24c2},
	{0x25aa, 0x25ab},
	{0x25b6, 0x25b6},
	{0x25c0, 0x25c0},
	{0x25fb, 0x25fe},
	{0x2600, 0x2604},
	{0x260e, 0x260e},
	{0x2611, 0x2611},
	{0x2614, 0x2615},
	{0x2618, 0x2618},
	{0x261d, 0x261d},
	{0x2620, 0x2620},
	{0x2622, 0x2623},
	{0x2626, 0x2626},
	{0x262a, 0x262a},
	{0x262e, 0x262f},
	{0x2638, 0x263a},
	{0x2640, 0x2640},
	{0x2642, 0x2642},
	{0x2648, 0x2653},
	{0x265f, 0x2660},
	{0x2663, 0x2663},
	{0x2665, 0x2666},
	{0x2668, 0x2668},
	{0x267b, 0x267b},
	{0x267e, 0x267f},
	{0x2692, 0x2697},
	{0x2699, 0x2699},
	{0x269b, 0x269c},
	{0x26a0, 0x26a1},
	{0x26a7, 0x26a7},
	{0x26aa, 0x26ab},
	{0x26b0, 0x26b1},
	{0x26bd, 0x26be},
	{0x26c4, 0x26c5},
	{0x26c8, 0x26c8},
	{0x26ce, 0x26cf},
	{0x26d1, 0x26d1},
	{0x26d3, 0x26d4},
	{0x26e9, 0x26ea},
	{0x26f0, 0x26f5},
	{0x26f7, 0x26fa},
	{0x26fd, 0x26fd},
	{0x2702, 0x2702},
	{0x2705, 0x2705},
	{0x2708, 0x270d},
	{0x270f, 0x270f},
	{0x2712, 0x2712},
	{0x2714, 0x2714},
	{0x2716, 0x2716},
	{0x271d, 0x271d},
	{0x2721, 0x2721},
	{0x2728, 0x2728},
	{0x2733, 0x2734},
	{0x2744, 0x2744},
	{0x2747, 0x2747},
	{0x274c, 0x274c},
	{0x274e, 0x274e},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2763, 0x2764},
	{0x2795, 0x2797},
	{0x27a1, 0x27a1},
	{0x27b0, 0x27b0},
	{0x27bf, 0x27bf},
	{0x2934, 0x2935},
	{0x2b05, 0x2b07},
	{0x2b1b, 0x2b1c},
	{0x2b50, 0x2b50},
	{0x2b55, 0x2b55},
	{0x3030, 0x3030},
	{0x303d, 0x303d},
	{0x3297, 0x3297},
	{0x3299, 0x3299},
	{0x1f004, 0x1f004},
	{0x1f0cf, 0x1f0cf},
	{0x1f170, 0x1f171},
	{0x1f17e, 0x1f17f},
	{0x1f18e, 0x1f18e},
	{0x1f191, 0x1f19a},
	{0x1f1e6, 0x1f1ff},
	{0x1f201, 0x1f202},
	{0x1f21a, 0x1f21a},
	{0x1f22f, 0x1f22f},
	{0x1f232, 0x1f23a},
	{0x1f250, 0x1f251},
	{0x1f300, 0x1f321},
	{0x1f324, 0x1f393},
	{0x1f396, 0x1f397},
	{0x1f399, 0x1f39b},
	{0x1f39e, 0x1f3f0},
	{0x1f3f3, 0x1f3f5},
	{0x1f3f7, 0x1f4fd},
	{0x1f4ff, 0x1f53d},
	{0x1f549, 0x1f54e},
	{0x1f550, 0x1f567},
	{0x1f56f, 0x1f570},
	{0x1f573, 0x1f57a},
	{0x1f587, 0x1f587},
	{0x1f58a, 0x1f58d},
	{0x1f590, 0x1f590},
	{0x1f595, 0x1f596},
	{0x1f5a4, 0x1f5a5},
	{0x1f5a8, 0x1f5a8},
	{0x1f5b1, 0x1f5b2},
	{0x1f5bc, 0x1f5bc},
	{0x1f5c2, 0x1f5c4},
	{0x1f5d1, 0x1f5d3},
	{0x1f5dc, 0x1f5de},
	{0x1f5e1, 0x1f5e1},
	{0x1f5e3, 0x1f5e3},
	{0x1f5e8, 0x1f5e8},
	{0x1f5ef, 0x1f5ef},
	{0x1f5f3, 0x1f5f3},
	{0x1f5fa, 0x1f64f},
	{0x1f680, 0x1f6c5},
	{0x1f6cb, 0x1f6d2},
	{0x1f6d5, 0x1f6d8},
	{0x1f6dc, 0x1f6e5},
	{0x1f6e9, 0x1f6e9},
	{0x1f6eb, 0x1f6ec},
	{0x1f6f0, 0x1f6f0},
	{0x1f6f3, 0x1f6fc},
	{0x1f7e0, 0x1f7eb},
	{0x1f7f0, 0x1f7f0},
	{0x1f90c, 0x1f93a},
	{0x1f93c, 0x1f945},
	{0x1f947, 0x1f9ff},
	{0x1fa70, 0x1fa7c},
	{0x1fa80, 0x1fa8a},
	{0x1fa8e, 0x1fac6},
	{0x1fac8, 0x1fac8},
	{0x1facd, 0x1fadc},
	{0x1fadf, 0x1faea},
	{0x1faef, 0x1faf8},
}

var utilsEmojiModifierBaseRanges = [][2]rune{
	{0x261d, 0x261d},
	{0x26f9, 0x26f9},
	{0x270a, 0x270d},
	{0x1f385, 0x1f385},
	{0x1f3c2, 0x1f3c4},
	{0x1f3c7, 0x1f3c7},
	{0x1f3ca, 0x1f3cc},
	{0x1f442, 0x1f443},
	{0x1f446, 0x1f450},
	{0x1f466, 0x1f478},
	{0x1f47c, 0x1f47c},
	{0x1f481, 0x1f483},
	{0x1f485, 0x1f487},
	{0x1f48f, 0x1f48f},
	{0x1f491, 0x1f491},
	{0x1f4aa, 0x1f4aa},
	{0x1f574, 0x1f575},
	{0x1f57a, 0x1f57a},
	{0x1f590, 0x1f590},
	{0x1f595, 0x1f596},
	{0x1f645, 0x1f647},
	{0x1f64b, 0x1f64f},
	{0x1f6a3, 0x1f6a3},
	{0x1f6b4, 0x1f6b6},
	{0x1f6c0, 0x1f6c0},
	{0x1f6cc, 0x1f6cc},
	{0x1f90c, 0x1f90c},
	{0x1f90f, 0x1f90f},
	{0x1f918, 0x1f91f},
	{0x1f926, 0x1f926},
	{0x1f930, 0x1f939},
	{0x1f93c, 0x1f93e},
	{0x1f977, 0x1f977},
	{0x1f9b5, 0x1f9b6},
	{0x1f9b8, 0x1f9b9},
	{0x1f9bb, 0x1f9bb},
	{0x1f9cd, 0x1f9cf},
	{0x1f9d1, 0x1f9dd},
	{0x1fac3, 0x1fac5},
	{0x1faf0, 0x1faf8},
}

var utilsSpacingMarkRanges = [][2]rune{
	{0x65f, 0x65f},
	{0x903, 0x903},
	{0x93b, 0x93b},
	{0x93e, 0x940},
	{0x949, 0x94c},
	{0x94e, 0x94f},
	{0x982, 0x983},
	{0x9be, 0x9c0},
	{0x9c7, 0x9c8},
	{0x9cb, 0x9cc},
	{0x9d7, 0x9d7},
	{0xa03, 0xa03},
	{0xa3e, 0xa40},
	{0xa83, 0xa83},
	{0xabe, 0xac0},
	{0xac9, 0xac9},
	{0xacb, 0xacc},
	{0xb02, 0xb03},
	{0xb3e, 0xb3e},
	{0xb40, 0xb40},
	{0xb47, 0xb48},
	{0xb4b, 0xb4c},
	{0xb57, 0xb57},
	{0xbbe, 0xbbf},
	{0xbc1, 0xbc2},
	{0xbc6, 0xbc8},
	{0xbca, 0xbcc},
	{0xbd7, 0xbd7},
	{0xc01, 0xc03},
	{0xc41, 0xc44},
	{0xc82, 0xc83},
	{0xcbe, 0xcbe},
	{0xcc0, 0xcc4},
	{0xcc7, 0xcc8},
	{0xcca, 0xccb},
	{0xcd5, 0xcd6},
	{0xcf3, 0xcf3},
	{0xd02, 0xd03},
	{0xd3e, 0xd40},
	{0xd46, 0xd48},
	{0xd4a, 0xd4c},
	{0xd57, 0xd57},
	{0xd82, 0xd83},
	{0xdcf, 0xdd1},
	{0xdd8, 0xddf},
	{0xdf2, 0xdf3},
	{0xf3e, 0xf3f},
	{0xf7f, 0xf7f},
	{0x102b, 0x102c},
	{0x1031, 0x1031},
	{0x1033, 0x1035},
	{0x1038, 0x1038},
	{0x103a, 0x103e},
	{0x1056, 0x1057},
	{0x1062, 0x1064},
	{0x1067, 0x106d},
	{0x1083, 0x1084},
	{0x1087, 0x108c},
	{0x108f, 0x108f},
	{0x109a, 0x109c},
	{0x1715, 0x1715},
	{0x17b6, 0x17b6},
	{0x17be, 0x17c5},
	{0x17c7, 0x17c8},
	{0x1923, 0x1926},
	{0x1929, 0x192b},
	{0x1930, 0x1931},
	{0x1933, 0x1938},
	{0x1a19, 0x1a1a},
	{0x1a55, 0x1a55},
	{0x1a57, 0x1a57},
	{0x1a61, 0x1a61},
	{0x1a63, 0x1a64},
	{0x1a6d, 0x1a72},
	{0x1b04, 0x1b04},
	{0x1b35, 0x1b35},
	{0x1b3b, 0x1b3b},
	{0x1b3d, 0x1b41},
	{0x1b43, 0x1b44},
	{0x1b82, 0x1b82},
	{0x1ba1, 0x1ba1},
	{0x1ba6, 0x1ba7},
	{0x1baa, 0x1baa},
	{0x1be7, 0x1be7},
	{0x1bea, 0x1bec},
	{0x1bee, 0x1bee},
	{0x1bf2, 0x1bf3},
	{0x1c24, 0x1c2b},
	{0x1c34, 0x1c35},
	{0x1ce1, 0x1ce1},
	{0x1cf7, 0x1cf7},
	{0xa823, 0xa824},
	{0xa827, 0xa827},
	{0xa880, 0xa881},
	{0xa8b4, 0xa8c3},
	{0xa952, 0xa953},
	{0xa983, 0xa983},
	{0xa9b4, 0xa9b5},
	{0xa9ba, 0xa9bb},
	{0xa9be, 0xa9c0},
	{0xaa2f, 0xaa30},
	{0xaa33, 0xaa34},
	{0xaa4d, 0xaa4d},
	{0xaa7b, 0xaa7b},
	{0xaa7d, 0xaa7d},
	{0xaaeb, 0xaaeb},
	{0xaaee, 0xaaef},
	{0xaaf5, 0xaaf5},
	{0xabe3, 0xabe4},
	{0xabe6, 0xabe7},
	{0xabe9, 0xabea},
	{0xabec, 0xabec},
	{0x11000, 0x11000},
	{0x11002, 0x11002},
	{0x11082, 0x11082},
	{0x110b0, 0x110b2},
	{0x110b7, 0x110b8},
	{0x1112c, 0x1112c},
	{0x11145, 0x11146},
	{0x11182, 0x11182},
	{0x111b3, 0x111b5},
	{0x111bf, 0x111c0},
	{0x111ce, 0x111ce},
	{0x1122c, 0x1122e},
	{0x11232, 0x11233},
	{0x11235, 0x11235},
	{0x112e0, 0x112e2},
	{0x11302, 0x11303},
	{0x1133e, 0x1133f},
	{0x11341, 0x11344},
	{0x11347, 0x11348},
	{0x1134b, 0x1134d},
	{0x11357, 0x11357},
	{0x11362, 0x11363},
	{0x113b8, 0x113ba},
	{0x113c2, 0x113c2},
	{0x113c5, 0x113c5},
	{0x113c7, 0x113ca},
	{0x113cc, 0x113cd},
	{0x113cf, 0x113cf},
	{0x11435, 0x11437},
	{0x11440, 0x11441},
	{0x11445, 0x11445},
	{0x114b0, 0x114b2},
	{0x114b9, 0x114b9},
	{0x114bb, 0x114be},
	{0x114c1, 0x114c1},
	{0x115af, 0x115b1},
	{0x115b8, 0x115bb},
	{0x115be, 0x115be},
	{0x11630, 0x11632},
	{0x1163b, 0x1163c},
	{0x1163e, 0x1163e},
	{0x116ac, 0x116ac},
	{0x116ae, 0x116af},
	{0x116b6, 0x116b6},
	{0x1171e, 0x1171e},
	{0x11720, 0x11721},
	{0x11726, 0x11726},
	{0x1182c, 0x1182e},
	{0x11838, 0x11838},
	{0x11930, 0x11935},
	{0x11937, 0x11938},
	{0x1193d, 0x1193d},
	{0x11940, 0x11940},
	{0x11942, 0x11942},
	{0x119d1, 0x119d3},
	{0x119dc, 0x119df},
	{0x119e4, 0x119e4},
	{0x11a39, 0x11a39},
	{0x11a57, 0x11a58},
	{0x11a97, 0x11a97},
	{0x11b61, 0x11b61},
	{0x11b65, 0x11b65},
	{0x11b67, 0x11b67},
	{0x11c2f, 0x11c2f},
	{0x11c3e, 0x11c3e},
	{0x11ca9, 0x11ca9},
	{0x11cb1, 0x11cb1},
	{0x11cb4, 0x11cb4},
	{0x11d8a, 0x11d8e},
	{0x11d93, 0x11d94},
	{0x11d96, 0x11d96},
	{0x11ef5, 0x11ef6},
	{0x11f03, 0x11f03},
	{0x11f34, 0x11f35},
	{0x11f3e, 0x11f3f},
	{0x11f41, 0x11f41},
	{0x1612a, 0x1612c},
	{0x16f51, 0x16f87},
	{0x16ff0, 0x16ff1},
	{0x1d165, 0x1d166},
	{0x1d16d, 0x1d172},
}

var CjkBreakRegex = jsre.MustCompile(`[\p{Script_Extensions=Han}\p{Script_Extensions=Hiragana}\p{Script_Extensions=Katakana}\p{Script_Extensions=Hangul}\p{Script_Extensions=Bopomofo}]`, "u")
var CjkPunctuationRegex = jsre.MustCompile(`(?:(?=\p{Punctuation})`+CjkBreakRegex.Source()+`|[，．：；！？（）［］｛｝“”‘’…—])`, "u")
var AutocompleteSeparatorRegex = jsre.MustCompile(`(?:\s|`+CjkPunctuationRegex.Source()+`)`, "u")
var AutocompleteBoundaryRegex = jsre.MustCompile(`(?:^|`+AutocompleteSeparatorRegex.Source()+`)`, "u")
var PunctuationRegex = jsre.MustCompile(`[(){}[\]<>.,;:'"!?+\-=*/\\|&%^$#@~`+"`"+`]`, "")

func utilsCouldBeEmoji(segment string) bool {
	for _, r := range segment {
		if (r >= 0x1f000 && r <= 0x1fbff) ||
			(r >= 0x2000 && r <= 0x3fff) ||
			r == 0xfe0f || r == 0x20e3 ||
			r == 0x00a9 || r == 0x00ae {
			return true
		}
	}
	return false
}

func utilsIsRegionalIndicator(r rune) bool {
	return r >= 0x1f1e6 && r <= 0x1f1ff
}

func utilsIsSkinTone(r rune) bool {
	return r >= 0x1f3fb && r <= 0x1f3ff
}

func utilsIsKeycapBase(r rune) bool {
	return (r >= '0' && r <= '9') || r == '#' || r == '*'
}

func utilsIsEmojiCore(runes []rune) bool {
	switch len(runes) {
	case 1:
		return utilsInRanges(runes[0], utilsEmojiPresentationRanges)
	case 2:
		if utilsInRanges(runes[0], utilsEmojiRanges) && runes[1] == 0xfe0f {
			return true
		}
		if utilsIsRegionalIndicator(runes[0]) && utilsIsRegionalIndicator(runes[1]) {
			return true
		}
		if utilsInRanges(runes[0], utilsEmojiModifierBaseRanges) && utilsIsSkinTone(runes[1]) {
			return true
		}
	case 3:
		if utilsIsKeycapBase(runes[0]) && runes[1] == 0xfe0f && runes[2] == 0x20e3 {
			return true
		}
	}
	return false
}

func utilsIsRgiEmoji(segment string) bool {
	if segment == "" {
		return false
	}
	if strings.ContainsRune(segment, 0x200d) {
		parts := strings.Split(segment, string(rune(0x200d)))
		if len(parts) < 2 {
			return false
		}
		for _, p := range parts {
			runes := []rune(p)
			if len(runes) == 0 {
				return false
			}
			// Allow any core emoji component or single emoji rune joined with ZWJ.
			if !utilsIsEmojiCore(runes) && !(len(runes) == 1 && utilsInRanges(runes[0], utilsEmojiRanges)) {
				return false
			}
		}
		return true
	}
	runes := []rune(segment)
	if utilsIsEmojiCore(runes) {
		return true
	}
	// RGI tag sequences (subdivision flags): U+1F3F4 [tag char]+ U+E007F
	if len(runes) >= 3 && runes[0] == 0x1f3f4 && runes[len(runes)-1] == 0xe007f {
		for _, t := range runes[1 : len(runes)-1] {
			if !((t >= 0xe0030 && t <= 0xe0039) || (t >= 0xe0061 && t <= 0xe007a)) {
				return false
			}
		}
		return true
	}
	return false
}

func utilsIsTerminalSpacingMark(r rune) bool {
	return utilsInRanges(r, utilsSpacingMarkRanges)
}

func utilsIsZeroWidthChar(r rune) bool {
	if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
		return true
	}
	if unicode.IsMark(r) {
		return true
	}
	if r >= 0xd800 && r <= 0xdfff {
		return true
	}
	return utilsInRanges(r, utilsDefaultIgnorableRanges)
}

func utilsIsNonPrintingChar(r rune) bool {
	return unicode.Is(unicode.Cf, r) || utilsIsZeroWidthChar(r)
}

func utilsGraphemeWidth(segment string) int {
	if segment == "" {
		return 0
	}
	if segment == "\t" {
		return 3
	}
	if len(segment) == 1 {
		b := segment[0]
		if b >= 0x20 && b <= 0x7e {
			return 1
		}
		return 0
	}

	// Some marks occupy cells even without a base character.
	allTerminalSpacing := true
	allZeroWidth := true
	runeCount := 0
	for _, r := range segment {
		runeCount++
		if !utilsIsTerminalSpacingMark(r) {
			allTerminalSpacing = false
		}
		if !utilsIsZeroWidthChar(r) {
			allZeroWidth = false
		}
	}
	if allTerminalSpacing {
		return runeCount
	}
	if allZeroWidth {
		return 0
	}

	if utilsCouldBeEmoji(segment) && utilsIsRgiEmoji(segment) {
		return 2
	}

	// Get base visible codepoints (stripping leading non-printing characters).
	runes := []rune(segment)
	start := 0
	for start < len(runes) && utilsIsNonPrintingChar(runes[start]) {
		start++
	}
	if start >= len(runes) {
		return 0
	}
	base := runes[start:]
	cp := base[0]

	// Regional indicator symbols (U+1F1E6..U+1F1FF) are often rendered as
	// full-width emoji in terminals, even when isolated during streaming.
	if cp >= 0x1f1e6 && cp <= 0x1f1ff {
		return 2
	}

	width := eastasian.Width(cp, false)

	// Count trailing visible code points within one grapheme cluster that
	// terminals may allocate cells for: Indic consonants after marks,
	// halfwidth/fullwidth forms, and Thai/Lao AM vowels.
	followsMark := false
	for _, c := range base[1:] {
		if utilsIsTerminalSpacingMark(c) {
			width++
			followsMark = false
		} else if unicode.IsMark(c) {
			followsMark = true
		} else if !utilsIsNonPrintingChar(c) {
			if followsMark || (c >= 0xff00 && c <= 0xffef) {
				width += eastasian.Width(c, false)
			} else if c == 0x0e33 || c == 0x0eb3 {
				width++
			}
			followsMark = false
		}
	}

	return width
}

const utilsWidthCacheLimit = 512

var (
	utilsWidthCacheMu    sync.Mutex
	utilsWidthCache      = make(map[string]int, utilsWidthCacheLimit)
	utilsWidthCacheOrder = make([]string, 0, utilsWidthCacheLimit)
)

func utilsCacheWidth(key string, width int) {
	if len(key) > 1024 {
		return
	}
	utilsWidthCacheMu.Lock()
	defer utilsWidthCacheMu.Unlock()
	if _, exists := utilsWidthCache[key]; exists {
		utilsWidthCache[key] = width
		return
	}
	if len(utilsWidthCacheOrder) >= utilsWidthCacheLimit {
		evict := utilsWidthCacheOrder[0]
		copy(utilsWidthCacheOrder, utilsWidthCacheOrder[1:])
		utilsWidthCacheOrder = utilsWidthCacheOrder[:len(utilsWidthCacheOrder)-1]
		delete(utilsWidthCache, evict)
	}
	utilsWidthCache[key] = width
	utilsWidthCacheOrder = append(utilsWidthCacheOrder, key)
}

func VisibleWidth(str string) int {
	if str == "" {
		return 0
	}
	pureASCII := true
	for i := 0; i < len(str); i++ {
		if str[i] < 0x20 || str[i] > 0x7e {
			pureASCII = false
			break
		}
	}
	if pureASCII {
		return len(str)
	}

	if len(str) <= 1024 {
		utilsWidthCacheMu.Lock()
		cached, ok := utilsWidthCache[str]
		utilsWidthCacheMu.Unlock()
		if ok {
			return cached
		}
	}

	clean := StripTerminalSequences(str)
	width := 0
	for _, seg := range utilsSplitGraphemes(clean) {
		width += utilsGraphemeWidth(seg)
	}
	utilsCacheWidth(str, width)
	return width
}

func StripTerminalSequences(str string) string {
	if str == "" || (!strings.ContainsRune(str, '\x1b') && !strings.ContainsRune(str, '\x9b')) {
		return str
	}
	var b strings.Builder
	b.Grow(len(str))
	i := 0
	for i < len(str) {
		if str[i] == '\x1b' {
			if _, n := utilsExtractAnsiCodeBytes(str, i); n > 0 {
				i += n
				continue
			}
		}
		// Also handle 8-bit C1 CSI if present directly in UTF-8.
		if r, size := utf8.DecodeRuneInString(str[i:]); r == '\u009b' {
			j := i + size
			for j < len(str) && str[j] >= 0x30 && str[j] <= 0x3f {
				j++
			}
			for j < len(str) && str[j] >= 0x20 && str[j] <= 0x2f {
				j++
			}
			if j < len(str) && str[j] >= 0x40 && str[j] <= 0x7e {
				i = j + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(str[i:])
		b.WriteString(str[i : i+size])
		i += size
	}
	return b.String()
}

// ReplaceControlCharacters collapses each run of C0 and C1 control characters with one space.
func ReplaceControlCharacters(str string) string {
	if str == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(str))
	inControl := false
	changed := false
	for i := 0; i < len(str); {
		r, size := utf8.DecodeRuneInString(str[i:])
		if (r >= 0x0000 && r <= 0x001f) || (r >= 0x007f && r <= 0x009f) {
			if !inControl {
				b.WriteByte(' ')
				inControl = true
			}
			changed = true
		} else {
			inControl = false
			b.WriteString(str[i : i+size])
		}
		i += size
	}
	if !changed {
		return str
	}
	return b.String()
}

type GraphemeCellRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func GetGraphemeCellRange(line string, column int) *GraphemeCellRange {
	if column < 0 || line == "" {
		return nil
	}
	clean := StripTerminalSequences(line)
	currentCol := 0
	for _, seg := range utilsSplitGraphemes(clean) {
		w := utilsGraphemeWidth(seg)
		if w <= 0 {
			continue
		}
		if column >= currentCol && column < currentCol+w {
			return &GraphemeCellRange{Start: currentCol, End: currentCol + w}
		}
		currentCol += w
		if currentCol > column {
			break
		}
	}
	return nil
}

func GetOsc8LinkAtColumn(line string, column int) *string {
	if column < 0 || line == "" {
		return nil
	}
	var tracker utilsAnsiTracker
	currentCol := 0
	for _, tok := range utilsTokenizeAnsi(line) {
		if tok.isAnsi {
			tracker.process(tok.text)
			continue
		}
		for _, seg := range utilsSplitGraphemes(tok.text) {
			w := utilsGraphemeWidth(seg)
			if w > 0 && column >= currentCol && column < currentCol+w {
				if tracker.osc8Url != "" {
					url := tracker.osc8Url
					return &url
				}
				return nil
			}
			currentCol += w
		}
	}
	return nil
}

func NormalizeTerminalOutput(str string) string {
	normalized := str
	if strings.ContainsRune(normalized, 0x0e33) || strings.ContainsRune(normalized, 0x0eb3) {
		normalized = strings.ReplaceAll(normalized, "\u0e33", "\u0e4d\u0e32")
		normalized = strings.ReplaceAll(normalized, "\u0eb3", "\u0ecd\u0eb2")
	}
	if !strings.ContainsRune(normalized, '\t') {
		return normalized
	}
	var result strings.Builder
	result.Grow(len(normalized) + 8)
	i := 0
	for i < len(normalized) {
		if normalized[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(normalized, i); n > 0 {
				result.WriteString(code)
				i += n
				continue
			}
		}
		if normalized[i] == '\t' {
			result.WriteString("   ")
		} else {
			result.WriteByte(normalized[i])
		}
		i++
	}
	return result.String()
}

type ExtractedAnsiCode struct {
	Code   string `json:"code"`
	Length int    `json:"length"`
}

func ExtractAnsiCode(str string, pos int) *ExtractedAnsiCode {
	if pos < 0 {
		return nil
	}
	bytePos := js.U16ToByte(str, pos)
	if bytePos < 0 || bytePos >= len(str) || str[bytePos] != '\x1b' {
		return nil
	}
	code, byteLen := utilsExtractAnsiCodeBytes(str, bytePos)
	if byteLen == 0 {
		return nil
	}
	return &ExtractedAnsiCode{
		Code:   code,
		Length: js.Len(code),
	}
}

func utilsExtractAnsiCodeBytes(str string, pos int) (string, int) {
	if pos < 0 || pos >= len(str) || str[pos] != '\x1b' || pos+1 >= len(str) {
		return "", 0
	}
	next := str[pos+1]
	switch next {
	case '[':
		j := pos + 2
		for j < len(str) && str[j] >= 0x30 && str[j] <= 0x3f {
			j++
		}
		for j < len(str) && str[j] >= 0x20 && str[j] <= 0x2f {
			j++
		}
		if j < len(str) && str[j] >= 0x40 && str[j] <= 0x7e {
			return str[pos : j+1], j + 1 - pos
		}
		return "", 0
	case ']', '_', 'P', '^', 'X':
		j := pos + 2
		for j < len(str) {
			if str[j] == '\x07' {
				return str[pos : j+1], j + 1 - pos
			}
			if str[j] == '\x1b' && j+1 < len(str) && str[j+1] == '\\' {
				return str[pos : j+2], j + 2 - pos
			}
			j++
		}
		return "", 0
	default:
		if next >= 0x20 && next <= 0x2f {
			j := pos + 2
			for j < len(str) && str[j] >= 0x20 && str[j] <= 0x2f {
				j++
			}
			if j < len(str) && str[j] >= 0x30 && str[j] <= 0x7e {
				return str[pos : j+1], j + 1 - pos
			}
			return "", 0
		}
		if (next >= 0x30 && next <= 0x5f) ||
			next == 'c' || next == 'l' || next == 'm' || next == 'n' ||
			next == 'o' || next == '|' || next == '}' || next == '~' {
			return str[pos : pos+2], 2
		}
		return "", 0
	}
}

type utilsToken struct {
	text   string
	isAnsi bool
}

func utilsTokenizeAnsi(str string) []utilsToken {
	if str == "" {
		return nil
	}
	if !strings.ContainsRune(str, '\x1b') {
		return []utilsToken{{text: str, isAnsi: false}}
	}
	var out []utilsToken
	textStart := 0
	i := 0
	for i < len(str) {
		if str[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(str, i); n > 0 {
				if i > textStart {
					out = append(out, utilsToken{text: str[textStart:i], isAnsi: false})
				}
				out = append(out, utilsToken{text: code, isAnsi: true})
				i += n
				textStart = i
				continue
			}
		}
		i++
	}
	if textStart < len(str) {
		out = append(out, utilsToken{text: str[textStart:], isAnsi: false})
	}
	return out
}

type utilsAnsiTracker struct {
	bold           bool
	dim            bool
	italic         bool
	underline      string
	blink          bool
	inverse        bool
	hidden         bool
	strikethrough  bool
	fgColor        string
	bgColor        string
	osc8Code       string
	osc8Url        string
	osc8Terminator string
}

func utilsParseOsc8(code string) (url string, terminator string, ok bool) {
	if !strings.HasPrefix(code, "\x1b]8;") {
		return "", "", false
	}
	body := code[len("\x1b]8;"):]
	if strings.HasSuffix(body, "\x1b\\") {
		terminator = "\x1b\\"
		body = body[:len(body)-2]
	} else if strings.HasSuffix(body, "\x07") {
		terminator = "\x07"
		body = body[:len(body)-1]
	} else {
		return "", "", false
	}
	semi := strings.IndexByte(body, ';')
	if semi < 0 {
		return "", "", false
	}
	return body[semi+1:], terminator, true
}

func utilsGetActiveOsc8Close(prefix string) string {
	if !strings.Contains(prefix, "\x1b]8;") {
		return ""
	}
	activeTerm := ""
	i := 0
	for i < len(prefix) {
		if prefix[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(prefix, i); n > 0 {
				if url, term, ok := utilsParseOsc8(code); ok {
					if url == "" {
						activeTerm = ""
					} else {
						activeTerm = term
					}
				}
				i += n
				continue
			}
		}
		i++
	}
	if activeTerm == "" {
		return ""
	}
	return "\x1b]8;;" + activeTerm
}

func (t *utilsAnsiTracker) resetSgr() {
	t.bold = false
	t.dim = false
	t.italic = false
	t.underline = ""
	t.blink = false
	t.inverse = false
	t.hidden = false
	t.strikethrough = false
	t.fgColor = ""
	t.bgColor = ""
}

func (t *utilsAnsiTracker) process(code string) {
	if strings.HasPrefix(code, "\x1b]8;") {
		t.processOsc8(code)
		return
	}
	if len(code) >= 3 && code[0] == '\x1b' && code[1] == '[' && code[len(code)-1] == 'm' {
		t.processSgr(code[2 : len(code)-1])
	}
}

func (t *utilsAnsiTracker) processOsc8(code string) {
	url, term, ok := utilsParseOsc8(code)
	if !ok {
		return
	}
	if url == "" {
		t.osc8Code = ""
		t.osc8Url = ""
		t.osc8Terminator = ""
	} else {
		t.osc8Code = code
		t.osc8Url = url
		t.osc8Terminator = term
	}
}

func (t *utilsAnsiTracker) processSgr(paramsStr string) {
	if paramsStr == "" {
		t.resetSgr()
		return
	}
	params := strings.Split(paramsStr, ";")
	for i := 0; i < len(params); i++ {
		p := params[i]
		if strings.HasPrefix(p, "38:") {
			t.fgColor = p
			continue
		}
		if strings.HasPrefix(p, "48:") {
			t.bgColor = p
			continue
		}
		if strings.HasPrefix(p, "4:") {
			if p == "4:0" {
				t.underline = ""
			} else {
				t.underline = p
			}
			continue
		}
		switch p {
		case "", "0":
			t.resetSgr()
		case "1":
			t.bold = true
		case "2":
			t.dim = true
		case "3":
			t.italic = true
		case "4":
			t.underline = "4"
		case "5", "6":
			t.blink = true
		case "7":
			t.inverse = true
		case "8":
			t.hidden = true
		case "9":
			t.strikethrough = true
		case "21":
			t.bold = false
		case "22":
			t.bold = false
			t.dim = false
		case "23":
			t.italic = false
		case "24":
			t.underline = ""
		case "25":
			t.blink = false
		case "27":
			t.inverse = false
		case "28":
			t.hidden = false
		case "29":
			t.strikethrough = false
		case "39":
			t.fgColor = ""
		case "49":
			t.bgColor = ""
		case "38":
			if i+2 < len(params) && params[i+1] == "5" {
				t.fgColor = "38;5;" + params[i+2]
				i += 2
			} else if i+4 < len(params) && params[i+1] == "2" {
				t.fgColor = "38;2;" + params[i+2] + ";" + params[i+3] + ";" + params[i+4]
				i += 4
			}
		case "48":
			if i+2 < len(params) && params[i+1] == "5" {
				t.bgColor = "48;5;" + params[i+2]
				i += 2
			} else if i+4 < len(params) && params[i+1] == "2" {
				t.bgColor = "48;2;" + params[i+2] + ";" + params[i+3] + ";" + params[i+4]
				i += 4
			}
		default:
			if utilsIsFgCode(p) {
				t.fgColor = p
			} else if utilsIsBgCode(p) {
				t.bgColor = p
			}
		}
	}
}

func utilsIsFgCode(p string) bool {
	if len(p) != 2 {
		return false
	}
	return (p[0] == '3' && p[1] >= '0' && p[1] <= '7') || (p[0] == '9' && p[1] >= '0' && p[1] <= '7')
}

func utilsIsBgCode(p string) bool {
	if len(p) == 2 {
		return p[0] == '4' && p[1] >= '0' && p[1] <= '7'
	}
	if len(p) == 3 {
		return p[0] == '1' && p[1] == '0' && p[2] >= '0' && p[2] <= '7'
	}
	return false
}

func (t *utilsAnsiTracker) hasActive() bool {
	return t.bold || t.dim || t.italic || t.underline != "" || t.blink ||
		t.inverse || t.hidden || t.strikethrough || t.fgColor != "" ||
		t.bgColor != "" || t.osc8Code != ""
}

func (t *utilsAnsiTracker) getActiveAnsiCodes() string {
	var codes []string
	if t.bold {
		codes = append(codes, "1")
	}
	if t.dim {
		codes = append(codes, "2")
	}
	if t.italic {
		codes = append(codes, "3")
	}
	if t.underline != "" {
		codes = append(codes, t.underline)
	}
	if t.blink {
		codes = append(codes, "5")
	}
	if t.inverse {
		codes = append(codes, "7")
	}
	if t.hidden {
		codes = append(codes, "8")
	}
	if t.strikethrough {
		codes = append(codes, "9")
	}
	if t.fgColor != "" {
		codes = append(codes, t.fgColor)
	}
	if t.bgColor != "" {
		codes = append(codes, t.bgColor)
	}
	var b strings.Builder
	if len(codes) > 0 {
		b.WriteString("\x1b[")
		b.WriteString(strings.Join(codes, ";"))
		b.WriteByte('m')
	}
	if t.osc8Code != "" {
		b.WriteString(t.osc8Code)
	}
	return b.String()
}

func (t *utilsAnsiTracker) getLineEndReset() string {
	var b strings.Builder
	if t.underline != "" {
		b.WriteString("\x1b[24m")
	}
	if t.strikethrough {
		b.WriteString("\x1b[29m")
	}
	if t.osc8Code != "" {
		term := t.osc8Terminator
		if term == "" {
			term = "\x1b\\"
		}
		b.WriteString("\x1b]8;;")
		b.WriteString(term)
	}
	return b.String()
}

func GetActiveBackgroundAnsi(text string) string {
	var tracker utilsAnsiTracker
	for _, tok := range utilsTokenizeAnsi(text) {
		if tok.isAnsi {
			tracker.process(tok.text)
		}
	}
	if tracker.bgColor == "" {
		return ""
	}
	return "\x1b[" + tracker.bgColor + "m"
}

func FlattenLines(lines []string) {}

type utilsWordToken struct {
	text         string
	isWhitespace bool
}

func utilsIsPrintableAscii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func utilsExtractAnsiCodes(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(s, i); n > 0 {
				b.WriteString(code)
				i += n
				continue
			}
		}
		i++
	}
	return b.String()
}

func utilsUpdateTrackerFromText(s string, tracker *utilsAnsiTracker) {
	i := 0
	for i < len(s) {
		idx := strings.IndexByte(s[i:], '\x1b')
		if idx < 0 {
			return
		}
		i += idx
		if code, n := utilsExtractAnsiCodeBytes(s, i); n > 0 {
			tracker.process(code)
			i += n
		} else {
			i++
		}
	}
}

func utilsSplitNewlines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\r' {
			lines = append(lines, text[start:i])
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			start = i + 1
		} else if text[i] == '\n' {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	lines = append(lines, text[start:])
	return lines
}

func utilsSplitIntoTokensWithAnsi(text string) []utilsWordToken {
	if text == "" {
		return nil
	}
	var tokens []utilsWordToken
	var cur strings.Builder
	var curHasContent bool
	var curWS bool
	pendingAnsi := ""

	flushCurrent := func() {
		if !curHasContent {
			return
		}
		tokens = append(tokens, utilsWordToken{
			text:         cur.String(),
			isWhitespace: curWS,
		})
		cur.Reset()
		curHasContent = false
	}

	i := 0
	for i < len(text) {
		if text[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(text, i); n > 0 {
				pendingAnsi += code
				i += n
				continue
			}
		}

		// Visible text runs up to the next valid escape sequence.
		end := i + 1
		for end < len(text) {
			if text[end] == '\x1b' {
				if _, n := utilsExtractAnsiCodeBytes(text, end); n > 0 {
					break
				}
			}
			end++
		}

		chunk := text[i:end]
		if utilsIsPrintableAscii(chunk) {
			for k := 0; k < len(chunk); k++ {
				seg := chunk[k : k+1]
				segIsSpace := chunk[k] == ' '
				if curHasContent && curWS != segIsSpace {
					flushCurrent()
				}
				if pendingAnsi != "" {
					cur.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				curHasContent = true
				curWS = segIsSpace
				cur.WriteString(seg)
			}
		} else {
			for _, seg := range utilsSplitGraphemes(chunk) {
				segIsSpace := seg == " "
				if !segIsSpace && CjkBreakRegex.Test(seg) {
					flushCurrent()
					tokens = append(tokens, utilsWordToken{
						text:         pendingAnsi + seg,
						isWhitespace: false,
					})
					pendingAnsi = ""
					continue
				}

				if curHasContent && curWS != segIsSpace {
					flushCurrent()
				}
				if pendingAnsi != "" {
					cur.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				curHasContent = true
				curWS = segIsSpace
				cur.WriteString(seg)
			}
		}

		i = end
	}

	// Handle any remaining pending ANSI codes (attach to last token).
	if pendingAnsi != "" {
		if curHasContent {
			cur.WriteString(pendingAnsi)
		} else if len(tokens) > 0 {
			tokens[len(tokens)-1].text += pendingAnsi
		} else {
			cur.WriteString(pendingAnsi)
			curHasContent = true
			curWS = false
		}
	}
	flushCurrent()
	return tokens
}

type utilsAnsiOrGrapheme struct {
	isAnsi bool
	value  string
}

func utilsBreakLongWord(word string, width int, tracker *utilsAnsiTracker) []string {
	var lines []string
	var cur strings.Builder
	if active := tracker.getActiveAnsiCodes(); active != "" {
		cur.WriteString(active)
	}
	curWidth := 0

	var segments []utilsAnsiOrGrapheme
	i := 0
	for i < len(word) {
		if word[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(word, i); n > 0 {
				segments = append(segments, utilsAnsiOrGrapheme{isAnsi: true, value: code})
				i += n
				continue
			}
		}
		end := i + 1
		for end < len(word) {
			if word[end] == '\x1b' {
				if _, n := utilsExtractAnsiCodeBytes(word, end); n > 0 {
					break
				}
			}
			end++
		}
		for _, seg := range utilsSplitGraphemes(word[i:end]) {
			segments = append(segments, utilsAnsiOrGrapheme{isAnsi: false, value: seg})
		}
		i = end
	}

	for _, seg := range segments {
		if seg.isAnsi {
			cur.WriteString(seg.value)
			tracker.process(seg.value)
			continue
		}
		if seg.value == "" {
			continue
		}
		gw := utilsGraphemeWidth(seg.value)
		if curWidth > 0 && curWidth+gw > width {
			if endReset := tracker.getLineEndReset(); endReset != "" {
				cur.WriteString(endReset)
			}
			lines = append(lines, cur.String())
			cur.Reset()
			if active := tracker.getActiveAnsiCodes(); active != "" {
				cur.WriteString(active)
			}
			curWidth = 0
		}
		cur.WriteString(seg.value)
		curWidth += gw
	}

	if cur.Len() > 0 {
		// No reset at end of final segment - caller handles continuation.
		lines = append(lines, cur.String())
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func utilsTrimTrailingSpaces(line string) string {
	return strings.TrimRightFunc(line, js.IsJSWhitespace)
}

func WrapTextWithAnsi(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	if width <= 0 {
		width = 1
	}
	inputLines := utilsSplitNewlines(text)
	var result []string
	var tracker utilsAnsiTracker
	for _, inputLine := range inputLines {
		prefix := ""
		if len(result) > 0 {
			prefix = tracker.getActiveAnsiCodes()
		}
		wrapped := utilsWrapSingleLine(prefix+inputLine, width)
		result = append(result, wrapped...)
		utilsUpdateTrackerFromText(inputLine, &tracker)
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func utilsWrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if VisibleWidth(line) <= width {
		return []string{line}
	}

	var wrapped []string
	var tracker utilsAnsiTracker
	tokens := utilsSplitIntoTokensWithAnsi(line)

	var curLine strings.Builder
	curWidth := 0

	for _, tok := range tokens {
		tokWidth := VisibleWidth(tok.text)

		if tokWidth > width && !tok.isWhitespace {
			if curLine.Len() > 0 {
				lineToWrap := utilsTrimTrailingSpaces(curLine.String())
				if endReset := tracker.getLineEndReset(); endReset != "" {
					lineToWrap += endReset
				}
				wrapped = append(wrapped, lineToWrap)
				curLine.Reset()
				curWidth = 0
			}

			broken := utilsBreakLongWord(tok.text, width, &tracker)
			for i := 0; i < len(broken)-1; i++ {
				wrapped = append(wrapped, broken[i])
			}
			curLine.Reset()
			last := broken[len(broken)-1]
			curLine.WriteString(last)
			curWidth = VisibleWidth(last)
			continue
		}

		totalNeeded := curWidth + tokWidth

		if totalNeeded > width && curWidth > 0 {
			lineToWrap := utilsTrimTrailingSpaces(curLine.String())
			if tok.isWhitespace {
				// Keep closing codes carried by the dropped space on the wrapped line.
				lineToWrap += utilsExtractAnsiCodes(tok.text)
				utilsUpdateTrackerFromText(tok.text, &tracker)
			}
			if endReset := tracker.getLineEndReset(); endReset != "" {
				lineToWrap += endReset
			}
			wrapped = append(wrapped, lineToWrap)
			curLine.Reset()
			curLine.WriteString(tracker.getActiveAnsiCodes())
			if tok.isWhitespace {
				curWidth = 0
				continue
			}
			curLine.WriteString(tok.text)
			curWidth = tokWidth
		} else if tok.isWhitespace && totalNeeded > width && strings.ContainsRune(tok.text, '\x1b') {
			// Whitespace wider than an empty line: keep only embedded ANSI codes.
			curLine.WriteString(utilsExtractAnsiCodes(tok.text))
		} else {
			curLine.WriteString(tok.text)
			curWidth += tokWidth
		}

		utilsUpdateTrackerFromText(tok.text, &tracker)
	}

	if curLine.Len() > 0 {
		// No reset at end of final line - let caller handle it.
		wrapped = append(wrapped, curLine.String())
	}

	if len(wrapped) == 0 {
		return []string{""}
	}
	for i := range wrapped {
		wrapped[i] = utilsTrimTrailingSpaces(wrapped[i])
	}
	return wrapped
}

func IsWhitespaceChar(char string) bool {
	if char == "" {
		return false
	}
	for _, r := range char {
		if !js.IsJSWhitespace(r) {
			return false
		}
	}
	return true
}

func IsPunctuationChar(char string) bool {
	return PunctuationRegex.Test(char)
}

func ApplyBackgroundToLine(line string, width int, bgFn func(text string) string) string {
	visibleLen := VisibleWidth(line)
	paddingNeeded := width - visibleLen
	if paddingNeeded < 0 {
		paddingNeeded = 0
	}
	padded := line
	if paddingNeeded > 0 {
		padded += strings.Repeat(" ", paddingNeeded)
	}
	return bgFn(padded)
}

func utilsTruncateFragmentToWidth(text string, maxWidth int) (string, int) {
	if maxWidth <= 0 || text == "" {
		return "", 0
	}
	if utilsIsPrintableAscii(text) {
		n := maxWidth
		if n > len(text) {
			n = len(text)
		}
		clipped := text[:n]
		return clipped, len(clipped)
	}

	hasAnsi := strings.ContainsRune(text, '\x1b')
	hasTabs := strings.ContainsRune(text, '\t')
	if !hasAnsi && !hasTabs {
		var result strings.Builder
		width := 0
		for _, seg := range utilsSplitGraphemes(text) {
			w := utilsGraphemeWidth(seg)
			if width+w > maxWidth {
				break
			}
			result.WriteString(seg)
			width += w
		}
		return result.String(), width
	}

	var result strings.Builder
	width := 0
	i := 0
	pendingAnsi := ""

	for i < len(text) {
		if text[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(text, i); n > 0 {
				pendingAnsi += code
				i += n
				continue
			}
		}

		if text[i] == '\t' {
			if width+3 > maxWidth {
				break
			}
			if pendingAnsi != "" {
				result.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			result.WriteByte('\t')
			width += 3
			i++
			continue
		}

		end := i + 1
		for end < len(text) && text[end] != '\t' {
			if text[end] == '\x1b' {
				if _, n := utilsExtractAnsiCodeBytes(text, end); n > 0 {
					break
				}
			}
			end++
		}

		for _, seg := range utilsSplitGraphemes(text[i:end]) {
			w := utilsGraphemeWidth(seg)
			if width+w > maxWidth {
				return result.String(), width
			}
			if pendingAnsi != "" {
				result.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			result.WriteString(seg)
			width += w
		}
		i = end
	}

	return result.String(), width
}

func utilsFinalizeTruncatedResult(prefix string, prefixWidth int, ellipsis string, ellipsisWidth int, maxWidth int, pad bool) string {
	const reset = "\x1b[0m"
	hyperlinkClose := utilsGetActiveOsc8Close(prefix)
	visibleWidth := prefixWidth + ellipsisWidth
	var result string
	if ellipsis != "" {
		result = prefix + hyperlinkClose + reset + ellipsis + reset
	} else {
		result = prefix + hyperlinkClose + reset
	}
	if pad && visibleWidth < maxWidth {
		result += strings.Repeat(" ", maxWidth-visibleWidth)
	}
	return result
}

func TruncateToWidth(text string, maxWidth int, ellipsis string, pad bool) string {
	if maxWidth <= 0 {
		return ""
	}
	if text == "" {
		if pad {
			return strings.Repeat(" ", maxWidth)
		}
		return ""
	}

	ellipsisWidth := VisibleWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		textWidth := VisibleWidth(text)
		if textWidth <= maxWidth {
			if pad && textWidth < maxWidth {
				return text + strings.Repeat(" ", maxWidth-textWidth)
			}
			return text
		}
		clippedText, clippedWidth := utilsTruncateFragmentToWidth(ellipsis, maxWidth)
		if clippedWidth == 0 {
			if pad {
				return strings.Repeat(" ", maxWidth)
			}
			return ""
		}
		return utilsFinalizeTruncatedResult("", 0, clippedText, clippedWidth, maxWidth, pad)
	}

	if utilsIsPrintableAscii(text) {
		if len(text) <= maxWidth {
			if pad && len(text) < maxWidth {
				return text + strings.Repeat(" ", maxWidth-len(text))
			}
			return text
		}
		targetWidth := maxWidth - ellipsisWidth
		return utilsFinalizeTruncatedResult(text[:targetWidth], targetWidth, ellipsis, ellipsisWidth, maxWidth, pad)
	}

	targetWidth := maxWidth - ellipsisWidth
	var result strings.Builder
	pendingAnsi := ""
	visibleSoFar := 0
	keptWidth := 0
	keepContiguousPrefix := true
	overflowed := false
	exhaustedInput := false
	hasAnsi := strings.ContainsRune(text, '\x1b')
	hasTabs := strings.ContainsRune(text, '\t')

	if !hasAnsi && !hasTabs {
		for _, seg := range utilsSplitGraphemes(text) {
			w := utilsGraphemeWidth(seg)
			if keepContiguousPrefix && keptWidth+w <= targetWidth {
				result.WriteString(seg)
				keptWidth += w
			} else {
				keepContiguousPrefix = false
			}
			visibleSoFar += w
			if visibleSoFar > maxWidth {
				overflowed = true
				break
			}
		}
		exhaustedInput = !overflowed
	} else {
		i := 0
		for i < len(text) {
			if text[i] == '\x1b' {
				if code, n := utilsExtractAnsiCodeBytes(text, i); n > 0 {
					pendingAnsi += code
					i += n
					continue
				}
			}

			if text[i] == '\t' {
				if keepContiguousPrefix && keptWidth+3 <= targetWidth {
					if pendingAnsi != "" {
						result.WriteString(pendingAnsi)
						pendingAnsi = ""
					}
					result.WriteByte('\t')
					keptWidth += 3
				} else {
					keepContiguousPrefix = false
					pendingAnsi = ""
				}
				visibleSoFar += 3
				if visibleSoFar > maxWidth {
					overflowed = true
					break
				}
				i++
				continue
			}

			end := i + 1
			for end < len(text) && text[end] != '\t' {
				if text[end] == '\x1b' {
					if _, n := utilsExtractAnsiCodeBytes(text, end); n > 0 {
						break
					}
				}
				end++
			}

			for _, seg := range utilsSplitGraphemes(text[i:end]) {
				w := utilsGraphemeWidth(seg)
				if keepContiguousPrefix && keptWidth+w <= targetWidth {
					if pendingAnsi != "" {
						result.WriteString(pendingAnsi)
						pendingAnsi = ""
					}
					result.WriteString(seg)
					keptWidth += w
				} else {
					keepContiguousPrefix = false
					pendingAnsi = ""
				}

				visibleSoFar += w
				if visibleSoFar > maxWidth {
					overflowed = true
					break
				}
			}
			if overflowed {
				break
			}
			i = end
		}
		exhaustedInput = i >= len(text)
	}

	if !overflowed && exhaustedInput {
		if pad && visibleSoFar < maxWidth {
			return text + strings.Repeat(" ", maxWidth-visibleSoFar)
		}
		return text
	}

	return utilsFinalizeTruncatedResult(result.String(), keptWidth, ellipsis, ellipsisWidth, maxWidth, pad)
}

func SliceByColumn(line string, startCol int, length int, strict bool) string {
	return SliceWithWidth(line, startCol, length, strict).Text
}

type SliceWithWidthResult struct {
	Text  string `json:"text"`
	Width int    `json:"width"`
}

func SliceWithWidth(line string, startCol int, length int, strict bool) SliceWithWidthResult {
	if line == "" || length <= 0 {
		return SliceWithWidthResult{}
	}
	if startCol < 0 {
		length += startCol
		startCol = 0
		if length <= 0 {
			return SliceWithWidthResult{}
		}
	}

	endCol := startCol + length
	var result strings.Builder
	resultWidth := 0
	currentCol := 0
	i := 0
	pendingAnsi := ""

	for i < len(line) {
		if line[i] == '\x1b' {
			if code, n := utilsExtractAnsiCodeBytes(line, i); n > 0 {
				if currentCol >= startCol && currentCol < endCol {
					result.WriteString(pendingAnsi)
					result.WriteString(code)
					pendingAnsi = ""
				} else if currentCol < startCol && !strings.HasPrefix(code, "\x1b_") {
					pendingAnsi += code
				}
				i += n
				continue
			}
		}

		textEnd := i + 1
		for textEnd < len(line) {
			if line[textEnd] == '\x1b' {
				if _, n := utilsExtractAnsiCodeBytes(line, textEnd); n > 0 {
					break
				}
			}
			textEnd++
		}

		for _, seg := range utilsSplitGraphemes(line[i:textEnd]) {
			w := utilsGraphemeWidth(seg)
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strict || currentCol+w <= endCol
			if inRange && fits {
				if pendingAnsi != "" {
					result.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				result.WriteString(seg)
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				break
			}
		}
		i = textEnd
		if currentCol >= endCol {
			break
		}
	}

	return SliceWithWidthResult{Text: result.String(), Width: resultWidth}
}

type ExtractSegmentsResult struct {
	Before      string `json:"before"`
	BeforeWidth int    `json:"beforeWidth"`
	After       string `json:"after"`
	AfterWidth  int    `json:"afterWidth"`
}

func ExtractSegments(line string, beforeEnd int, afterStart int, afterLen int, strictAfter bool) ExtractSegmentsResult {
	var tracker utilsAnsiTracker
	var before, after strings.Builder
	beforeWidth := 0
	afterWidth := 0
	beforeDone := beforeEnd <= 0
	afterStarted := false
	afterEnd := afterStart + afterLen
	currentCol := 0

	if beforeEnd > 0 && line != "" {
		// Emit up to beforeEnd (strict: wide grapheme crossing beforeEnd is excluded).
		beforeDone = false
	}

	for _, tok := range utilsTokenizeAnsi(line) {
		if tok.isAnsi {
			tracker.process(tok.text)
			if !beforeDone && currentCol < beforeEnd {
				before.WriteString(tok.text)
			} else if currentCol >= afterStart && currentCol < afterEnd && (afterStarted || strings.HasPrefix(tok.text, "\x1b_")) {
				after.WriteString(tok.text)
			}
			continue
		}
		for _, seg := range utilsSplitGraphemes(tok.text) {
			w := utilsGraphemeWidth(seg)
			if w == 0 {
				if !beforeDone && currentCol < beforeEnd {
					before.WriteString(seg)
				} else if afterStarted {
					after.WriteString(seg)
				}
				continue
			}
			segEnd := currentCol + w

			if !beforeDone {
				if segEnd <= beforeEnd {
					before.WriteString(seg)
					beforeWidth += w
				} else {
					beforeDone = true
				}
			}

			if afterLen > 0 && currentCol >= afterStart {
				fitsAfter := (!strictAfter && currentCol < afterEnd) || (strictAfter && segEnd <= afterEnd)
				if fitsAfter {
					if !afterStarted {
						if active := tracker.getActiveAnsiCodes(); active != "" {
							after.WriteString(active)
						}
						afterStarted = true
					}
					after.WriteString(seg)
					afterWidth += w
				} else if currentCol >= afterEnd || (strictAfter && segEnd > afterEnd) {
					currentCol = segEnd
					goto done
				}
			}

			currentCol = segEnd
		}
	}

done:
	return ExtractSegmentsResult{
		Before:      before.String(),
		BeforeWidth: beforeWidth,
		After:       after.String(),
		AfterWidth:  afterWidth,
	}
}
