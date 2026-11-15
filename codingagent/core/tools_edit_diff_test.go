package core

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsdiff"
)

func ttedApplyTestReplacements(content string, replacements []TextReplacement) string {
	var b strings.Builder
	pos := 0
	for _, r := range replacements {
		b.WriteString(content[pos:r.MatchIndex])
		b.WriteString(r.NewText)
		pos = r.MatchIndex + r.MatchLength
	}
	b.WriteString(content[pos:])
	return b.String()
}

func ttedRandomCase(rng *rand.Rand, distinct bool) (content string, replacements []TextReplacement, newContent string) {
	lineCount := rng.Intn(300)
	lines := make([]string, lineCount)
	vocab := []string{"a", "b", "", "}"}
	for i := 0; i < lineCount; i++ {
		if distinct {
			lines[i] = strings.Repeat("line ", 1) + string(rune('0'+i%10)) + "_" + strings.Repeat("x", i%5)
			lines[i] = "line " + string(rune('A'+i/26)) + string(rune('a'+i%26))
			if i >= 26*26 {
				lines[i] += string(rune('0' + i%10))
			}
		} else {
			lines[i] = vocab[rng.Intn(len(vocab))]
		}
	}
	content = strings.Join(lines, "\n")
	if rng.Intn(2) == 0 {
		content += "\n"
	}

	pos := 0
	count := 1 + rng.Intn(4)
	for i := 0; i < count && pos < len(content); i++ {
		maxGap := len(content) - pos
		if maxGap > 300 {
			maxGap = 300
		}
		matchIndex := pos + rng.Intn(maxGap)
		if matchIndex >= len(content) {
			break
		}
		maxLen := len(content) - matchIndex
		if maxLen > 120 {
			maxLen = 120
		}
		matchLength := 1 + rng.Intn(maxLen)
		var newText string
		if rng.Intn(3) != 0 {
			nLines := make([]string, rng.Intn(4))
			for j := range nLines {
				nLines[j] = "new " + string(rune('0'+i)) + " " + string(rune('0'+j))
			}
			newText = strings.Join(nLines, "\n")
			if rng.Intn(2) == 0 {
				newText += "\n"
			}
		}
		replacements = append(replacements, TextReplacement{
			MatchIndex:  matchIndex,
			MatchLength: matchLength,
			NewText:     newText,
		})
		pos = matchIndex + matchLength
	}
	newContent = ttedApplyTestReplacements(content, replacements)
	return content, replacements, newContent
}

func TestGenerateEditDiff_WindowParityAndApplyPatch(t *testing.T) {
	t.Run("matches whole-file diff and patch when lines are distinct", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1))
		for run := 0; run < 200; run++ {
			content, replacements, newContent := ttedRandomCase(rng, true)
			if content == newContent {
				continue
			}
			windowed := GenerateEditDiff("f.txt", content, newContent, replacements)
			whole := GenerateDiffString(content, newContent)
			wantPatch := GenerateUnifiedPatch("f.txt", content, newContent)
			if windowed.Diff != whole.Diff {
				t.Fatalf("run %d: diff mismatch\ngot:\n%s\nwant:\n%s", run, windowed.Diff, whole.Diff)
			}
			if windowed.Patch != wantPatch {
				t.Fatalf("run %d: patch mismatch", run)
			}
			if (windowed.FirstChangedLine == nil) != (whole.FirstChangedLine == nil) ||
				(windowed.FirstChangedLine != nil && *windowed.FirstChangedLine != *whole.FirstChangedLine) {
				t.Fatalf("run %d: firstChangedLine mismatch: got %v want %v", run, windowed.FirstChangedLine, whole.FirstChangedLine)
			}
		}
	})

	t.Run("produces applicable unified patch when lines repeat", func(t *testing.T) {
		rng := rand.New(rand.NewSource(2))
		for run := 0; run < 200; run++ {
			content, replacements, newContent := ttedRandomCase(rng, false)
			if content == newContent {
				continue
			}
			ed := GenerateEditDiff("f.txt", content, newContent, replacements)
			patched, ok, err := jsdiff.ApplyPatch(content, ed.Patch, nil)
			if err != nil || !ok || patched != newContent {
				t.Fatalf("run %d: patch failed ok=%v err=%v", run, ok, err)
			}
		}
	})

	t.Run("handles large file replacement efficiently", func(t *testing.T) {
		filler := strings.Repeat("x", 99) + "\n"
		half := strings.Repeat(filler, 20000)
		base := half + "TARGET = 1\n" + half
		changed := half + "TARGET = 2\n" + half
		ed := GenerateEditDiff("big.txt", base, changed, []TextReplacement{{
			MatchIndex:  len(half),
			MatchLength: len("TARGET = 1"),
			NewText:     "TARGET = 2",
		}})
		if !strings.Contains(ed.Diff, "TARGET = 1") || !strings.Contains(ed.Diff, "TARGET = 2") {
			t.Fatalf("expected target change in diff:\n%s", ed.Diff)
		}
	})
}

func TestFuzzyMatchAndComputeEditsDiff(t *testing.T) {
	t.Run("fuzzy matching normalizes smart quotes, dashes, NBSP, trailing whitespace", func(t *testing.T) {
		content := "function foo() {  \n  return \u201Chello\u201D \u2014 world\u00A0!;\n}"
		oldText := "function foo() {\n  return \"hello\" - world !;\n}"
		m := FuzzyFindText(content, oldText)
		if !m.Found || !m.UsedFuzzyMatch {
			t.Fatalf("expected fuzzy match, got %+v", m)
		}
	})

	t.Run("prefers exact match over fuzzy match", func(t *testing.T) {
		content := "foo \u201Cbar\u201D\nfoo \"bar\"\n"
		m := FuzzyFindText(content, `foo "bar"`)
		if !m.Found || m.UsedFuzzyMatch || m.Index != strings.Index(content, `foo "bar"`) {
			t.Fatalf("expected exact match at second line, got %+v", m)
		}
	})

	t.Run("detects duplicates after fuzzy normalization", func(t *testing.T) {
		content := "let a = \u201Cx\u201D\nlet b = 2\nlet a = \"x\"\n"
		_, err := ApplyEditsToNormalizedContent(content, []EditReplacement{{OldText: `let a = "x"`, NewText: `let a = "y"`}}, "dup.ts")
		if err == nil || !strings.Contains(err.Error(), "2 occurrences") {
			t.Fatalf("expected duplicate occurrence error, got %v", err)
		}
	})

	t.Run("ComputeEditsDiff reports ENOENT and computes preview for valid edits", func(t *testing.T) {
		dir := t.TempDir()
		missing := ComputeEditsDiff(context.Background(), "missing.txt", []EditReplacement{{OldText: "a", NewText: "b"}}, dir)
		if !strings.Contains(missing.Error, "ENOENT") {
			t.Fatalf("expected ENOENT error, got %+v", missing)
		}

		p := filepath.Join(dir, "sample.txt")
		if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res := ComputeEditsDiff(context.Background(), "sample.txt", []EditReplacement{{OldText: "two", NewText: "TWO"}}, dir)
		if res.Error != "" || !strings.Contains(res.Diff, "-2 two") || !strings.Contains(res.Diff, "+2 TWO") {
			t.Fatalf("unexpected ComputeEditsDiff result: %+v", res)
		}
		if res.FirstChangedLine == nil || *res.FirstChangedLine != 2 {
			t.Fatalf("FirstChangedLine=%v, want 2", res.FirstChangedLine)
		}
	})

	t.Run("preserves CRLF endings and overlapping multi-edit errors", func(t *testing.T) {
		if got := DetectLineEnding("a\r\nb\r\n"); got != "\r\n" {
			t.Fatalf("DetectLineEnding=%q", got)
		}
		if got := RestoreLineEndings("a\nb\n", "\r\n"); got != "a\r\nb\r\n" {
			t.Fatalf("RestoreLineEndings=%q", got)
		}
		_, err := ApplyEditsToNormalizedContent("alpha beta gamma", []EditReplacement{
			{OldText: "alpha beta", NewText: "X"},
			{OldText: "beta gamma", NewText: "Y"},
		}, "overlap.txt")
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "overlap") {
			t.Fatalf("expected overlap error, got %v", err)
		}
	})
}
