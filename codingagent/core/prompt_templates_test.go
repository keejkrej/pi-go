package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func tpteBoolPtr(v bool) *bool { return &v }

func TestParseCommandArgs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "   ", want: nil},
		{name: "basic", input: "foo bar baz", want: []string{"foo", "bar", "baz"}},
		{name: "quoted spaces", input: `"hello world" 'single arg'`, want: []string{"hello world", "single arg"}},
		{name: "escaped matching quote", input: `"say \"hi\"" 'it\'s ok'`, want: []string{`say "hi"`, `it's ok`}},
		{name: "empty quotes", input: `"" '' a`, want: []string{"", "", "a"}},
		{name: "apostrophe inside word", input: `don't stop`, want: []string{"don't", "stop"}},
		{name: "unclosed quote literal", input: `"unclosed arg`, want: []string{`"unclosed`, "arg"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCommandArgs(tc.input)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ParseCommandArgs(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSubstituteArgs(t *testing.T) {
	args := []string{"first", "second", "third"}
	if got := SubstituteArgs("1=$1 2=$2 4=$4", args); got != "1=first 2=second 4=" {
		t.Fatalf("positional substitution failed: %q", got)
	}
	if got := SubstituteArgs("all=$@ / $ARGUMENTS", args); got != "all=first second third / first second third" {
		t.Fatalf("$@/$ARGUMENTS failed: %q", got)
	}
	if got := SubstituteArgs("tail=${@:2} slice=${@:2:1} zero=${@:0:2} neg=${@:2:0}", args); got != "tail=second third slice=second zero=first second neg=" {
		t.Fatalf("slice substitution failed: %q", got)
	}
	// Do not recursively expand $ARGUMENTS replaced inside an earlier arg.
	if got := SubstituteArgs("$1 $2", []string{"literal $ARGUMENTS", "second"}); got != "literal $ARGUMENTS second" {
		t.Fatalf("recursive substitution occurred: %q", got)
	}
}

func TestExpandAndLoadPromptTemplates(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "proj")
	agentDir := filepath.Join(base, "agent")
	projPrompts := filepath.Join(cwd, ".pi", "prompts")
	userPrompts := filepath.Join(agentDir, "prompts")
	for _, d := range []string{projPrompts, userPrompts} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projPrompts, "review.md"), []byte("---\ndescription: Review code\nargument-hint: <file>\n---\nReview $1 carefully.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userPrompts, "review.md"), []byte("---\ndescription: User review\n---\nUser version.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userPrompts, "useronly.md"), []byte("User prompt body $@"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := LoadPromptTemplates(&LoadPromptTemplatesOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		IncludeDefaults: tpteBoolPtr(true),
	})
	if len(res.Templates) != 2 {
		t.Fatalf("expected 2 templates, got %+v", res.Templates)
	}
	var sawCollision bool
	for _, d := range res.Diagnostics {
		if d.Type == ResourceDiagnosticTypeCollision && d.Collision != nil && d.Collision.Name == "review" {
			sawCollision = true
		}
	}
	if !sawCollision {
		t.Fatalf("expected collision diagnostic for review, got %+v", res.Diagnostics)
	}

	expanded := ExpandPromptTemplate(`/review "main.go"`, res.Templates)
	if expanded != "Review main.go carefully." {
		t.Fatalf("ExpandPromptTemplate got %q", expanded)
	}
	if untouched := ExpandPromptTemplate("/unknown arg", res.Templates); untouched != "/unknown arg" {
		t.Fatalf("unknown command should stay unchanged, got %q", untouched)
	}
}
