package mermaid

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type goldenCase struct {
	Name     string   `json:"name"`
	Fn       string   `json:"fn"`
	Src      string   `json:"src"`
	Kind     string   `json:"kind"`
	Err      bool     `json:"err"`
	Plain    []string `json:"plain"`
	Width    int      `json:"width"`
	Warnings []string `json:"warnings"`
	Styled   [][]Span `json:"styled"`
	MaxWidth *int     `json:"maxWidth"`
	Lines    []string `json:"lines"`
}

func TestGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			switch c.Fn {
			case "render":
				if got := DiagramKind(c.Src); got != c.Kind {
					t.Fatalf("kind %q want %q", got, c.Kind)
				}
				art, err := Render(c.Src, Options{})
				if c.Err {
					if err == nil {
						t.Fatalf("want error, got width %d\n%s", art.Width, strings.Join(art.Plain, "\n"))
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				compareArt(t, art, c)
			case "sourceBox":
				art := SourceBox(c.Src, c.MaxWidth)
				compareArt(t, art, c)
			case "ansi":
				art, err := Render(c.Src, Options{})
				if err != nil {
					t.Fatal(err)
				}
				got := ToAnsi(art, nil)
				if strings.Join(got, "\n") != strings.Join(c.Lines, "\n") {
					t.Fatalf("ansi\n got %q\nwant %q", got, c.Lines)
				}
			default:
				t.Fatalf("unknown fn %s", c.Fn)
			}
		})
	}
}

func compareArt(t *testing.T, art MermaidArt, c goldenCase) {
	t.Helper()
	if art.Width != c.Width {
		t.Errorf("width %d want %d", art.Width, c.Width)
	}
	if strings.Join(art.Plain, "\n") != strings.Join(c.Plain, "\n") {
		t.Errorf("plain\n got:\n%s\nwant:\n%s", strings.Join(art.Plain, "\n"), strings.Join(c.Plain, "\n"))
	}
	if strings.Join(art.Warnings, "\n") != strings.Join(c.Warnings, "\n") {
		t.Errorf("warnings\n got %q\nwant %q", art.Warnings, c.Warnings)
	}
	if len(art.Styled) != len(c.Styled) {
		t.Fatalf("styled rows %d want %d", len(art.Styled), len(c.Styled))
	}
	for i := range c.Styled {
		var got, want strings.Builder
		for _, sp := range art.Styled[i] {
			got.WriteString(sp.Text)
			if sp.Cls == "" {
				t.Errorf("row %d empty cls", i)
			}
		}
		for _, sp := range c.Styled[i] {
			want.WriteString(sp.Text)
		}
		if got.String() != want.String() {
			t.Errorf("row %d styled text %q want %q", i, got.String(), want.String())
		}
		if len(art.Styled[i]) != len(c.Styled[i]) {
			t.Errorf("row %d spans %d want %d\n got %#v\nwant %#v", i, len(art.Styled[i]), len(c.Styled[i]), art.Styled[i], c.Styled[i])
			continue
		}
		for j := range c.Styled[i] {
			if art.Styled[i][j] != c.Styled[i][j] {
				t.Errorf("row %d span %d got %#v want %#v", i, j, art.Styled[i][j], c.Styled[i][j])
			}
		}
	}
	for i, row := range art.Plain {
		var b strings.Builder
		for _, sp := range art.Styled[i] {
			b.WriteString(sp.Text)
		}
		if b.String() != row {
			t.Errorf("row %d plain/styled diverge %q vs %q", i, row, b.String())
		}
	}
}
