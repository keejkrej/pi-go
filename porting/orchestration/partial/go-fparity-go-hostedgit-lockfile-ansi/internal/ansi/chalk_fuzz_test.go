package ansi

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/chalk_fuzz.json is a differential corpus generated with chalk@6.0.0 under node v24.21.0
// (generator script: chalkfuzz.mjs, not kept). Each record is [level, chain, input, output]: a `new Chalk({level})`
// instance, a random chain of 1-4 steps (named styles, `visible`, and hex/bgHex/rgb/underlineRgb/ansi256/
// bgAnsi256 calls with odd hex strings such as "#12345" and "zz12ab34cd"), and an input built from text,
// line breaks, lone escapes and close codes of the chained styles.
type tcfRecord struct {
	Level  int
	Chain  [][]any
	Input  string
	Output string
}

func (r *tcfRecord) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	fields := []any{&r.Level, &r.Chain, &r.Input, &r.Output}
	for i, f := range fields {
		if err := json.Unmarshal(raw[i], f); err != nil {
			return err
		}
	}
	return nil
}

func TestChalkFuzz_MatchesChalk(t *testing.T) {
	data, err := os.ReadFile("testdata/chalk_fuzz.json")
	if err != nil {
		t.Fatal(err)
	}
	var records []tcfRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("no records")
	}
	for _, r := range records {
		var recv any = NewChalk(&Options{Level: &r.Level})
		for _, step := range r.Chain {
			recv = applyChainStep(t, recv, step)
		}
		if got := recv.(*Builder).Apply(r.Input); got != r.Output {
			t.Errorf("level %d chain %v input %q: got %q, want %q", r.Level, r.Chain, r.Input, got, r.Output)
		}
	}
}
