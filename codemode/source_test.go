// Ported from packages/codemode/test/source.test.ts (pi v1.0.0).

package codemode_test

import (
	"errors"
	"regexp"
	"testing"

	"github.com/keejkrej/pi-go/codemode"
)

func TestParseCodemodeSource(t *testing.T) {
	t.Run("returns plain code unchanged", func(t *testing.T) {
		got, err := codemode.ParseCodemodeSource("text('hi')")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Code != "text('hi')" || got.Options.MaxOutputTokens != nil || got.Options.TimeoutMs != nil {
			t.Fatalf("got %+v", got)
		}

		got2, err := codemode.ParseCodemodeSource("// just a comment\nreturn 1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got2.Code != "// just a comment\nreturn 1" || got2.Options.MaxOutputTokens != nil || got2.Options.TimeoutMs != nil {
			t.Fatalf("got %+v", got2)
		}
	})

	t.Run("parses the options line and keeps line numbers", func(t *testing.T) {
		got, err := codemode.ParseCodemodeSource("// @options: {\"timeout_ms\": 10}\nconst a = 1;\ntext(a)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Code != "\nconst a = 1;\ntext(a)" {
			t.Fatalf("code = %q, want %q", got.Code, "\nconst a = 1;\ntext(a)")
		}
		if got.Options.TimeoutMs == nil || *got.Options.TimeoutMs != 10 || got.Options.MaxOutputTokens != nil {
			t.Fatalf("options = %+v", got.Options)
		}

		got2, err := codemode.ParseCodemodeSource("  // @options:{\"max_output_tokens\":0,\"timeout_ms\":1500}\r\ntext(1)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got2.Options.MaxOutputTokens == nil || *got2.Options.MaxOutputTokens != 0 {
			t.Fatalf("maxOutputTokens = %v, want 0", got2.Options.MaxOutputTokens)
		}
		if got2.Options.TimeoutMs == nil || *got2.Options.TimeoutMs != 1500 {
			t.Fatalf("timeoutMs = %v, want 1500", got2.Options.TimeoutMs)
		}

		got3, err := codemode.ParseCodemodeSource("// @options: {}\ntext(1)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got3.Code != "\ntext(1)" || got3.Options.MaxOutputTokens != nil || got3.Options.TimeoutMs != nil {
			t.Fatalf("got %+v", got3)
		}
	})

	t.Run("only treats the first line as an options line", func(t *testing.T) {
		input := "text(1)\n// @options: {\"timeout_ms\": 1}"
		got, err := codemode.ParseCodemodeSource(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Code != input || got.Options.TimeoutMs != nil || got.Options.MaxOutputTokens != nil {
			t.Fatalf("got %+v", got)
		}

		got2, err := codemode.ParseCodemodeSource("// @optionsx {}\ntext(1)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got2.Options.TimeoutMs != nil || got2.Options.MaxOutputTokens != nil {
			t.Fatalf("got options %+v", got2.Options)
		}
	})

	t.Run("rejects empty input and invalid options", func(t *testing.T) {
		cases := []struct {
			input   string
			pattern *regexp.Regexp
		}{
			{"", regexp.MustCompile(`Expected JavaScript source text \(non-empty\)`)},
			{"  \n", regexp.MustCompile(`Expected JavaScript source text \(non-empty\)`)},
			{"// @options:\ntext(1)", regexp.MustCompile(`@options must be a JSON object with supported fields`)},
			{"// @options: {timeout_ms: 1}\ntext(1)", regexp.MustCompile(`@options must be valid JSON with supported fields`)},
			{"// @options: [1]\ntext(1)", regexp.MustCompile(`@options must be a JSON object with supported fields`)},
			{
				`// @options: {"yield": 1}` + "\ntext(1)",
				regexp.MustCompile(`^@options only supports ` + "`max_output_tokens` and `timeout_ms`; got `yield`$"),
			},
			{
				`// @options: {"max_output_tokens": 1.5}` + "\ntext(1)",
				regexp.MustCompile(`^@options field ` + "`max_output_tokens` must be a non-negative safe integer$"),
			},
			{
				`// @options: {"max_output_tokens": null}` + "\ntext(1)",
				regexp.MustCompile(`^@options field ` + "`max_output_tokens` must be a non-negative safe integer$"),
			},
			{
				`// @options: {"timeout_ms": 0}` + "\ntext(1)",
				regexp.MustCompile(`@options field ` + "`timeout_ms` must be a positive integer"),
			},
			{
				`// @options: {"timeout_ms": 2147483648}` + "\ntext(1)",
				regexp.MustCompile(`@options field ` + "`timeout_ms` must be a positive integer"),
			},
			{
				`// @options: {"timeout_ms": 1}`,
				regexp.MustCompile(`^The @options line must be followed by JavaScript source on subsequent lines$`),
			},
			{
				"// @options: {\"timeout_ms\": 1}\n  \n",
				regexp.MustCompile(`^The @options line must be followed by JavaScript source on subsequent lines$`),
			},
		}

		for _, tc := range cases {
			_, err := codemode.ParseCodemodeSource(tc.input)
			if err == nil {
				t.Fatalf("input %q: expected error, got nil", tc.input)
			}
			var srcErr *codemode.CodemodeSourceError
			if !errors.As(err, &srcErr) {
				t.Fatalf("input %q: expected *CodemodeSourceError, got %T (%v)", tc.input, err, err)
			}
			if srcErr.Name() != "CodemodeSourceError" {
				t.Fatalf("input %q: Name() = %q", tc.input, srcErr.Name())
			}
			if !tc.pattern.MatchString(err.Error()) {
				t.Fatalf("input %q: error %q did not match %s", tc.input, err.Error(), tc.pattern)
			}
		}
	})
}
