package highlight

import (
	"strings"
	"testing"
)

// themeScopes is buildCliHighlightTheme's key set, plus "" for unstyled text.
var themeScopes = map[string]struct{}{
	"":            {},
	"keyword":     {},
	"built_in":    {},
	"literal":     {},
	"number":      {},
	"regexp":      {},
	"string":      {},
	"comment":     {},
	"doctag":      {},
	"meta":        {},
	"function":    {},
	"title":       {},
	"class":       {},
	"type":        {},
	"tag":         {},
	"name":        {},
	"attr":        {},
	"variable":    {},
	"params":      {},
	"operator":    {},
	"punctuation": {},
	"emphasis":    {},
	"strong":      {},
	"link":        {},
	"addition":    {},
	"deletion":    {},
}

func TestSupportsLanguage(t *testing.T) {
	if len(UnsupportedLanguages) != 0 {
		t.Fatalf("chroma cannot lex registered languages: %v", UnsupportedLanguages)
	}
	canonical := []string{
		"python", "java", "go", "javascript", "json", "cpp", "typescript",
		"php", "ruby", "c", "csharp", "nix", "bash", "rust", "scala",
		"kotlin", "swift", "dart", "groovy", "perl", "lua",
	}
	for _, name := range canonical {
		if !SupportsLanguage(name) {
			t.Errorf("SupportsLanguage(%q) = false", name)
		}
		if !SupportsLanguage(strings.ToUpper(name)) {
			t.Errorf("SupportsLanguage(%q) = false", strings.ToUpper(name))
		}
	}
	aliases := []string{
		"py", "gyp", "ipython", "jsp", "golang", "js", "jsx", "mjs", "cjs",
		"cc", "c++", "C++", "h++", "hpp", "hh", "hxx", "cxx",
		"ts", "tsx", "php3", "php4", "php5", "php6", "php7", "php8",
		"rb", "gemspec", "podspec", "thor", "irb", "h", "cs", "c#", "C#",
		"nixos", "sh", "zsh", "rs", "kt", "kts", "pl", "pm",
	}
	for _, name := range aliases {
		if !SupportsLanguage(name) {
			t.Errorf("SupportsLanguage(%q) = false", name)
		}
	}
	for _, name := range []string{"", "sql", "html", "yaml", "plaintext", "shell", "markdown"} {
		if SupportsLanguage(name) {
			t.Errorf("SupportsLanguage(%q) = true", name)
		}
	}
}

func TestHighlightLanguages(t *testing.T) {
	snippets := map[string]string{
		"python":     "def greet(name):\n    # hi\n    return f\"hi {name}\"\n",
		"java":       "class Main { public static void main(String[] args) { System.out.println(\"hi\"); } }\n",
		"go":         "package main\n\nfunc main() { println(\"hi\") }\n",
		"javascript": "const x = 1;\nfunction f(a) { return /a+/; }\n",
		"json":       "{\"a\": 1, \"b\": true}\n",
		"cpp":        "#include <iostream>\nint main() { return 0; }\n",
		"typescript": "interface User { name: string }\nconst u: User = { name: \"a\" };\n",
		"php":        "<?php\necho \"hi\";\n",
		"ruby":       "def greet(name)\n  puts name\nend\n",
		"c":          "#include <stdio.h>\nint main(void) { return 0; }\n",
		"csharp":     "class Program { static void Main() { System.Console.WriteLine(\"hi\"); } }\n",
		"nix":        "{ pkgs }:\n  pkgs.hello\n",
		"bash":       "echo \"hi\"\nfor x in a b; do echo \"$x\"; done\n",
		"rust":       "fn main() { let x = 1; println!(\"{x}\"); }\n",
		"scala":      "object Main { def main(args: Array[String]): Unit = println(\"hi\") }\n",
		"kotlin":     "fun main() { println(\"hi\") }\n",
		"swift":      "func greet(name: String) -> String { return name }\n",
		"dart":       "void main() { print('hi'); }\n",
		"groovy":     "def greet(name) { return \"hi ${name}\" }\n",
		"perl":       "sub greet { my $name = shift; print \"$name\\n\"; }\n",
		"lua":        "function greet(name) print(name) end\n",
	}
	if len(snippets) != 21 {
		t.Fatalf("snippets = %d, want 21 eager languages", len(snippets))
	}
	for lang, code := range snippets {
		t.Run(lang, func(t *testing.T) {
			toks := Highlight(code, lang)
			assertTokens(t, code, toks)
		})
	}
}

func TestHighlightEdges(t *testing.T) {
	if Highlight("", "go") != nil {
		t.Fatal("empty code")
	}
	code := "not really highlighted"
	toks := Highlight(code, "nope")
	if len(toks) != 1 || toks[0].Text != code || toks[0].Scope != "" {
		t.Fatalf("unknown lang: %#v", toks)
	}
	if Highlight(code, "")[0].Scope != "" || Highlight(code, "")[0].Text != code {
		t.Fatal("empty lang")
	}

	// No trailing newline: EnsureNL must not append one.
	bare := "package main"
	toks = Highlight(bare, "go")
	assertTokens(t, bare, toks)

	crlf := "let x = 1\r\n"
	toks = Highlight(crlf, "javascript")
	assertTokens(t, crlf, toks)

	aliased := Highlight("fn main() {}\n", "RS")
	assertTokens(t, "fn main() {}\n", aliased)

	cxx := Highlight("#include <stdio.h>\nint main(){return 0;}\n", "C++")
	assertTokens(t, "#include <stdio.h>\nint main(){return 0;}\n", cxx)
}

func assertTokens(t *testing.T, code string, toks []Token) {
	t.Helper()
	var b strings.Builder
	scoped := false
	for _, tok := range toks {
		if _, ok := themeScopes[tok.Scope]; !ok {
			t.Errorf("scope %q is not a theme key", tok.Scope)
		}
		if tok.Text == "" {
			t.Error("empty token")
		}
		if tok.Scope != "" {
			scoped = true
		}
		b.WriteString(tok.Text)
	}
	if b.String() != code {
		t.Fatalf("joined text %q != input %q", b.String(), code)
	}
	if code != "" && !scoped {
		t.Fatal("no scoped token")
	}
}
