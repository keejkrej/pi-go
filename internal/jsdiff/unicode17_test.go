package jsdiff

import "testing"

// Expectations are String.prototype.toLowerCase and diffChars({ignoreCase:true})
// from Node 24 (ICU Unicode 17), which is ahead of Go 1.26's Unicode 15 tables.
func TestUnicode17_ToLowerAndIgnoreCase(t *testing.T) {
	lowers := []struct{ in, want string }{
		{"\u1C89", "\u1C8A"},
		{"\uA7CB", "\u0264"},
		{"\uA7CC", "\uA7CD"},
		{"\uA7CE", "\uA7CF"},
		{"\uA7D2", "\uA7D3"},
		{"\uA7D4", "\uA7D5"},
		{"\uA7DA", "\uA7DB"},
		{"\uA7DC", "\u019B"},
		{"\U00010D50", "\U00010D70"},
		{"\U00010D65", "\U00010D85"},
		{"\U00016EA0", "\U00016EBB"},
		{"\U00016EB8", "\U00016ED3"},
		{"İ", "i\u0307"},
		{"Σ", "σ"},
		{"AΣ", "aς"},
		{"A\u0295Σ", "a\u0295σ"},         // U+0295 is not cased in Unicode 17
		{"A\u0897Σ", "a\u0897ς"},         // U+0897 is case-ignorable
		{"A\U0001171EΣ", "a\U0001171Eσ"}, // U+1171E is not case-ignorable
		{"Σ\uA7F1", "σ\uA7F1"},           // nothing cased before sigma, so it stays σ
	}
	for _, tt := range lowers {
		if got := jsToLower(tt.in); got != tt.want {
			t.Errorf("jsToLower(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	ignore := &Options{IgnoreCase: true}
	equal := []struct{ old, neu string }{
		{"\u1C89", "\u1C8A"},
		{"\uA7CB", "\u0264"},
		{"\uA7DC", "\u019B"},
		{"\U00010D50", "\U00010D70"},
		{"\U00016EA0", "\U00016EBB"},
		{"A\u0295Σ", "a\u0295σ"},
		{"A\U0001171EΣ", "a\U0001171Eσ"},
		{"Σ", "σ"},
	}
	for _, tt := range equal {
		got := DiffChars(tt.old, tt.neu, ignore)
		if len(got) != 1 || got[0].Added || got[0].Removed || got[0].Value != tt.neu {
			t.Errorf("DiffChars(%q, %q, ignoreCase) = %+v, want one keep of %q", tt.old, tt.neu, got, tt.neu)
		}
	}
	// A lone sigma lowercases to σ, not ς, so these are a real change even with ignoreCase.
	// U+0897 is skipped as case-ignorable inside the full string, but equality is per token.
	split := DiffChars("A\u0897Σ", "a\u0897ς", ignore)
	if len(split) != 3 || split[0].Value != "a\u0897" || split[0].Added || split[0].Removed ||
		split[1].Value != "Σ" || !split[1].Removed || split[2].Value != "ς" || !split[2].Added {
		t.Errorf("DiffChars sigma token = %+v", split)
	}
}
