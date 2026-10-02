package xspawn

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWinPathVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/escape.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Normalize map[string]string `json:"normalize"`
		Join      []struct {
			A   string `json:"a"`
			B   string `json:"b"`
			Out string `json:"out"`
		} `json:"join"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Normalize) == 0 || len(file.Join) == 0 {
		t.Fatal("path vectors missing")
	}
	for in, want := range file.Normalize {
		if got := winNormalize(in); got != want {
			t.Errorf("normalize %q = %q, want %q", in, got, want)
		}
	}
	for _, row := range file.Join {
		if got := winJoin(row.A, row.B); got != row.Out {
			t.Errorf("join %q %q = %q, want %q", row.A, row.B, got, row.Out)
		}
	}
}

// TestWinPathMatchesNode checks path.win32 against Node for cases the saved
// vectors do not cover (UNC, reserved devices, resolve).
func TestWinPathMatchesNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	input := map[string]any{
		"normalize": []string{
			"", ".", "..", "foo/../../bar", "/foo/../../bar", "C:foo", "C:/foo", "C:/foo/",
			"C:", "C:\\", "C:/", "//server/share", "//server/share/foo",
			"//server/share/foo/../bar", `\\server\share\foo`, "//?/C:/foo",
			"//./PHYSICALDRIVE0", "CON:", "aux:", "foo:bar", "foo:", "C:.",
			"foo/bar/.", "foo/../bar/../baz", "././foo", `foo\..\bar`, "C:/foo/../bar",
			"foo//bar//", "/", "\\", "CON", "nul.txt", `\\?\COM1:`,
			"foo/bar/../../../../baz", `C:\foo\..\bar\`,
		},
		"join": [][]string{
			{"", `.\foo`},
			{"", "./foo"},
			{"", "foo"},
			{`C:\dir`, "foo"},
			{"//server", "share"},
			{"//server", "share", "a"},
			{"foo", "bar", "..", "baz"},
			{`C:\a`, `C:\b`},
			{"", ""},
			{".", "foo"},
			{"foo/", "bar"},
			{`\\server\share`, "a"},
		},
		"resolve": [][]string{
			{},
			{""},
			{"."},
			{"foo"},
			{`C:\a`, "b"},
			{`C:\a`, `C:\b`},
			{`C:\a\b`, `..\c`},
			{"", `.\foo`},
			{"subdir", `.\foo`},
			{`\\server\share`, "a"},
			{"/foo", "bar"},
			{"C:foo"},
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	script := `
const fs = require("fs");
const path = require("path");
const input = JSON.parse(fs.readFileSync(0, "utf8"));
const norm = (input.normalize || []).map((p) => path.win32.normalize(p));
const join = (input.join || []).map((xs) => path.win32.join(...xs));
const resolve = (input.resolve || []).map((xs) => path.win32.resolve(...xs));
const isAbs = (input.normalize || []).map((p) => path.win32.isAbsolute(p));
process.stdout.write(JSON.stringify({ cwd: process.cwd(), norm, join, resolve, isAbs }));
`
	cmd := exec.Command(node, "-e", script)
	cmd.Stdin = strings.NewReader(string(raw))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node: %v\n%s", err, ee.Stderr)
		}
		t.Fatal(err)
	}
	var got struct {
		Cwd     string   `json:"cwd"`
		Norm    []string `json:"norm"`
		Join    []string `json:"join"`
		Resolve []string `json:"resolve"`
		IsAbs   []bool   `json:"isAbs"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	norms := input["normalize"].([]string)
	if len(got.Norm) != len(norms) || len(got.IsAbs) != len(norms) {
		t.Fatalf("normalize count %d", len(got.Norm))
	}
	for i, in := range norms {
		if g := winNormalize(in); g != got.Norm[i] {
			t.Errorf("normalize %q = %q, node %q", in, g, got.Norm[i])
		}
		if g := winIsAbsolute(in); g != got.IsAbs[i] {
			t.Errorf("isAbsolute %q = %v, node %v", in, g, got.IsAbs[i])
		}
	}
	joins := input["join"].([][]string)
	for i, xs := range joins {
		if g := winJoin(xs...); g != got.Join[i] {
			t.Errorf("join %q = %q, node %q", xs, g, got.Join[i])
		}
	}
	resolves := input["resolve"].([][]string)
	for i, xs := range resolves {
		if g := winResolve(got.Cwd, xs...); g != got.Resolve[i] {
			t.Errorf("resolve %q = %q, node %q (cwd %q)", xs, g, got.Resolve[i], got.Cwd)
		}
	}
}
