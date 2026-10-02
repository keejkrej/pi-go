//go:build windows

package xspawn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandEchoAndENOENT(t *testing.T) {
	ctx := context.Background()
	cmd := Command(ctx, "cmd", []string{"/c", "echo", "hi"})
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hi\r\n" {
		t.Fatalf("echo %q", out)
	}
	if err := VerifyENOENT(cmd, nil); err != nil {
		t.Fatal(err)
	}

	args := []string{"/c", "echo", "hi"}
	cmd = Command(ctx, "cmd", args)
	args[2] = "mutated"
	out, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hi\r\n" {
		t.Fatalf("aliased args %q", out)
	}

	cmd = Command(ctx, "cmd", []string{"/c", "exit", "1"})
	err = cmd.Run()
	err = VerifyENOENT(cmd, err)
	var missing *NotFoundError
	if errors.As(err, &missing) {
		t.Fatalf("exit 1 became ENOENT: %v", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("exit 1: %v", err)
	}

	const missingName = "somecommandthatwillneverexist-pi-go"
	cmd = Command(ctx, missingName, []string{"foo"})
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("missing command succeeded")
	} else {
		err = VerifyENOENT(cmd, err)
		if !errors.As(err, &missing) {
			t.Fatalf("missing: %v", err)
		}
		if missing.Error() != "spawn "+missingName+" ENOENT" {
			t.Fatalf("message %q", missing.Error())
		}
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("not ErrNotFound: %v", err)
		}
		if missing.Path != missingName || len(missing.Args) != 1 || missing.Args[0] != "foo" {
			t.Fatalf("meta %+v", missing)
		}
	}
	cmd = Command(ctx, missingName, []string{"foo"})
	runErr := cmd.Run()
	syncErr := VerifyENOENTSync(cmd, runErr)
	if syncErr == nil || syncErr.Error() != "spawnSync "+missingName+" ENOENT" {
		t.Fatalf("sync %v", syncErr)
	}
}

func TestCommandBatShimShebangAndPath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	bat := filepath.Join(dir, "echo.bat")
	if err := os.WriteFile(bat, []byte("@echo off\r\necho [%~1]\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// %~1 is expanded by cmd before it parses pipes, so a value with "|" is not
	// a safe probe here. Spaces still prove the argument arrived as one token.
	cmd := Command(ctx, bat, []string{"hello world"})
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[hello world]\r\n" {
		t.Fatalf("bat %q", out)
	}

	sub := filepath.Join(dir, "sub dir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	spaced := filepath.Join(sub, "echo.bat")
	if err := os.WriteFile(spaced, []byte("@echo off\r\necho [%~1]\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = Command(ctx, spaced, []string{"x y"})
	out, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[x y]\r\n" {
		t.Fatalf("spaced bat %q", out)
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	echoJS := filepath.Join(dir, "echo.js")
	if err := os.WriteFile(echoJS, []byte("process.stdout.write(JSON.stringify(process.argv.slice(2)))"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(bin, "echo-cmd-shim.cmd")
	shimBody := "@IF EXIST \"%~dp0\\node.exe\" (\r\n" +
		"  \"%~dp0\\node.exe\"  \"%~dp0\\..\\..\\echo.js\" %*\r\n" +
		") ELSE (\r\n" +
		"  @SETLOCAL\r\n" +
		"  @SET PATHEXT=%PATHEXT:;.JS;=;%\r\n" +
		"  node  \"%~dp0\\..\\..\\echo.js\" %*\r\n" +
		")\r\n"
	if err := os.WriteFile(shim, []byte(shimBody), 0o644); err != nil {
		t.Fatal(err)
	}
	shimArgs := []string{`"(foo|bar>baz|foz)"`, "a|b", "hello world"}
	cmd = Command(ctx, shim, shimArgs)
	out, err = cmd.Output()
	if err != nil {
		ee, _ := err.(*exec.ExitError)
		stderr := ""
		if ee != nil {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("shim: %v %s %q", err, stderr, out)
	}
	if !jsonEqual(out, shimArgs) {
		t.Fatalf("shim stdout %s", out)
	}

	// node -e does not put the evaluated script into process.argv.
	directArgs := []string{"-e", "process.stdout.write(JSON.stringify(process.argv.slice(1)))", "a|b", "hello world", `"(foo|bar>baz|foz)"`, ""}
	cmd = Command(ctx, node, directArgs)
	out, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(out, directArgs[2:]) {
		t.Fatalf("direct node %s", out)
	}

	script := filepath.Join(dir, "runjs")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env node\nprocess.stdout.write('shebang-ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = Command(ctx, script, nil)
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("shebang: %v %q", err, out)
	}
	if string(out) != "shebang-ok" {
		t.Fatalf("shebang stdout %q", out)
	}

	batDir := filepath.Join(dir, "bindir")
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(batDir, 0o755); err != nil || os.Mkdir(empty, 0o755) != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batDir, "pi-go-hello-bat.bat"), []byte("@echo off\r\necho from-bat\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// cmd.exe does not resolve a bare name in cwd (cross-spawn has the same
	// limit). A relative path is resolved against Dir.
	cmd = CommandIn(ctx, dir, nil, filepath.Join("bindir", "pi-go-hello-bat.bat"), nil)
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("relative bat: %v %q", err, out)
	}
	if string(out) != "from-bat\r\n" {
		t.Fatalf("relative bat %q", out)
	}
	env := append([]string{}, os.Environ()...)
	replaced := false
	for i, e := range env {
		if len(e) >= 5 && strings.EqualFold(e[:5], "PATH=") {
			env[i] = "PATH=" + batDir
			replaced = true
		}
	}
	if !replaced {
		env = append(env, "PATH="+batDir)
	}
	cmd = CommandIn(ctx, empty, env, "pi-go-hello-bat", nil)
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("PATH bat: %v %q", err, out)
	}
	if string(out) != "from-bat\r\n" {
		t.Fatalf("PATH bat %q", out)
	}
}

func TestPlanMatchesNodeParse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	parseJS := `C:\Users\ctyja\workspace\pi\node_modules\cross-spawn\lib\parse.js`
	if _, err := os.Stat(parseJS); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	bat := filepath.Join(dir, "echo.bat")
	if err := os.WriteFile(bat, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(bin, "echo-cmd-shim.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "runjs")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args []string
		cwd  string
	}{
		{bat, []string{"hello world", "a|b", `"(foo|bar>baz|foz)"`}, ""},
		{shim, []string{`"(foo|bar>baz|foz)"`, "a|b", "hello world"}, ""},
		{node, []string{"-e", "1", "a|b"}, ""},
		{"somecommandthatwillneverexist-pi-go", []string{"foo"}, ""},
		{script, []string{"arg"}, ""},
		{"pi-go-hello-bat", nil, dir},
	}
	for _, tc := range cases {
		gotCmd, gotArgs, gotFile := nodeParsed(t, node, parseJS, tc.name, tc.args, tc.cwd)
		cmd := startCommand(context.Background(), tc.cwd, nil, tc.name, cloneArgs(tc.args))
		if !strings.EqualFold(cmd.Args[0], gotCmd) {
			t.Errorf("command %q Args0 %q node %q", tc.name, cmd.Args[0], gotCmd)
		}
		if strings.Join(cmd.Args[1:], "\n") != strings.Join(gotArgs, "\n") {
			t.Errorf("command %q\nargs %#v\nnode %#v", tc.name, cmd.Args[1:], gotArgs)
		}
		m, ok := lookupMeta(cmd)
		if !ok {
			t.Fatalf("no meta for %s", tc.name)
		}
		if !samePath(m.file, gotFile) {
			t.Errorf("command %q file %q node %q", tc.name, m.file, gotFile)
		}
		if cmd.SysProcAttr != nil && cmd.SysProcAttr.CmdLine != "" {
			wantLine := gotCmd + " " + strings.Join(gotArgs, " ")
			if cmd.SysProcAttr.CmdLine != wantLine {
				t.Errorf("cmdline\n%s\nnode\n%s", cmd.SysProcAttr.CmdLine, wantLine)
			}
		}
	}
}

func jsonEqual(out []byte, want []string) bool {
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		return false
	}
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	return strings.EqualFold(strings.ReplaceAll(a, "/", `\`), strings.ReplaceAll(b, "/", `\`))
}

func nodeParsed(t *testing.T, node, parseJS, command string, args []string, cwd string) (string, []string, string) {
	t.Helper()
	if args == nil {
		args = []string{}
	}
	body, err := json.Marshal(map[string]any{
		"command": command,
		"args":    args,
		"options": map[string]any{"cwd": cwdOrNil(cwd)},
	})
	if err != nil {
		t.Fatal(err)
	}
	script := `
const parse = require(process.argv[1]);
const fs = require("fs");
const input = JSON.parse(fs.readFileSync(0, "utf8"));
const options = {};
if (input.options && input.options.cwd) options.cwd = input.options.cwd;
const r = parse(input.command, input.args, options);
process.stdout.write(JSON.stringify({ command: r.command, args: r.args, file: r.file || null }));
`
	cmd := exec.Command(node, "-e", script, parseJS)
	cmd.Stdin = strings.NewReader(string(body))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node parse: %v\n%s", err, ee.Stderr)
		}
		t.Fatal(err)
	}
	var parsed struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		File    *string  `json:"file"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	file := ""
	if parsed.File != nil {
		file = *parsed.File
	}
	return parsed.Command, parsed.Args, file
}

func cwdOrNil(cwd string) any {
	if cwd == "" {
		return nil
	}
	return cwd
}
