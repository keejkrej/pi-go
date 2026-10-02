//go:build windows

package xspawn

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode/utf8"
	"weak"
)

// cmdShimPattern is cross-spawn's isCmdShimRegExp. The "." before "bin" is
// any character, matching the JavaScript regular expression.
var cmdShimPattern = regexp.MustCompile(`(?i)node_modules[\\/].bin[\\/][^\\/]+\.cmd$`)

const defaultPathExt = `.EXE;.CMD;.BAT;.COM`

type cmdMeta struct {
	file    string
	command string
	args    []string
}

type metaEntry struct {
	w weak.Pointer[exec.Cmd]
	m *cmdMeta
}

var (
	metaSeq   atomic.Uint64
	metaStore sync.Map // uint64 -> metaEntry
)

// remember keeps ENOENT metadata without pinning cmd. The cleanup drops the
// entry once cmd is unreachable.
func remember(cmd *exec.Cmd, m *cmdMeta) {
	id := metaSeq.Add(1)
	metaStore.Store(id, metaEntry{w: weak.Make(cmd), m: m})
	runtime.AddCleanup(cmd, func(id uint64) {
		metaStore.Delete(id)
	}, id)
}

func lookupMeta(cmd *exec.Cmd) (*cmdMeta, bool) {
	var found *cmdMeta
	metaStore.Range(func(_, value any) bool {
		e := value.(metaEntry)
		if e.w.Value() == cmd {
			found = e.m
			return false
		}
		return true
	})
	if found == nil {
		return nil, false
	}
	return found, true
}

func verifyMissing(cmd *exec.Cmd, err error, syscallName string) error {
	if err == nil || cmd == nil {
		return err
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return err
	}
	m, ok := lookupMeta(cmd)
	if !ok || m.file != "" {
		return err
	}
	return &NotFoundError{
		Command: m.command,
		Args:    append([]string(nil), m.args...),
		Syscall: syscallName + " " + m.command,
		Path:    m.command,
	}
}

func startCommand(ctx context.Context, dir string, env []string, name string, args []string) *exec.Cmd {
	original := append([]string(nil), args...)
	file, commandFile, command, args := detectShebang(name, args, dir, env)
	var cmd *exec.Cmd
	if isExecutableExt(commandFile) {
		cmd = directCommand(ctx, command, commandFile, args)
	} else {
		cmd = shellCommand(ctx, command, args, commandFile)
	}
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = append([]string(nil), env...)
	}
	remember(cmd, &cmdMeta{file: file, command: name, args: original})
	return cmd
}

func isExecutableExt(file string) bool {
	if len(file) < 4 {
		return false
	}
	ext := file[len(file)-4:]
	return strings.EqualFold(ext, ".com") || strings.EqualFold(ext, ".exe")
}

// detectShebang mirrors cross-spawn's detectShebang. file stays the first
// resolved path (the script, when a shebang was found). commandFile is the
// interpreter when a shebang was found, otherwise file.
func detectShebang(command string, args []string, dir string, env []string) (file, commandFile, commandOut string, argsOut []string) {
	if resolved, ok := resolveCommand(command, dir, env); ok {
		file = resolved
	}
	commandFile = file
	if file != "" {
		if sh := readShebang(file); sh != "" {
			args = append([]string{file}, args...)
			command = sh
			commandFile = ""
			if resolved, ok := resolveCommand(command, dir, env); ok {
				commandFile = resolved
			}
		}
	}
	return file, commandFile, command, args
}

func directCommand(ctx context.Context, command, commandFile string, args []string) *exec.Cmd {
	// Launch the file which resolved. cross-spawn spawns the original name
	// and lets Windows search again; Go resolves PATH when Command is called,
	// using the process environment rather than cmd.Env, so the absolute
	// path is what makes a custom PATH and the cwd-first search stick.
	// argv0 stays the original command.
	exe := commandFile
	if exe == "" {
		exe = command
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	if len(cmd.Args) == 0 {
		cmd.Args = []string{command}
	} else {
		cmd.Args[0] = command
	}
	return cmd
}

func shellCommand(ctx context.Context, command string, args []string, commandFile string) *exec.Cmd {
	comspec, argv, line := shellArgs(command, args, commandFile)
	cmd := exec.CommandContext(ctx, comspec, argv[1:]...)
	cmd.Args = argv
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	return cmd
}

func shellArgs(command string, args []string, commandFile string) (comspec string, argv []string, cmdLine string) {
	double := commandFile != "" && cmdShimPattern.MatchString(commandFile)
	command = escapeCommand(winNormalize(command))
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = escapeArgument(a, double)
	}
	shellCommand := command
	if len(escaped) > 0 {
		shellCommand += " " + strings.Join(escaped, " ")
	}
	quoted := `"` + shellCommand + `"`
	comspec = comspecPath()
	argv = []string{comspec, "/d", "/s", "/c", quoted}
	cmdLine = comspec + " /d /s /c " + quoted
	return comspec, argv, cmdLine
}

// comspecPath is process.env.comspec. Windows environment lookup is
// case-insensitive; an empty value falls back to "cmd.exe".
func comspecPath() string {
	if v, ok := lastEnv(os.Environ(), "comspec"); ok && v != "" {
		return v
	}
	return "cmd.exe"
}

func lastEnv(env []string, key string) (string, bool) {
	found := false
	val := ""
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || !strings.EqualFold(k, key) {
			continue
		}
		found = true
		val = v
	}
	return val, found
}

// resolveCommand is cross-spawn's resolveCommand: which.sync, then a second
// attempt with pathExt ";" so an extensionless file is accepted. It does not
// change the process working directory; a custom dir is searched by stating
// paths under that directory, which is what which does after chdir.
func resolveCommand(command, dir string, env []string) (string, bool) {
	if resolved, ok := resolveAttempt(command, dir, env, false); ok {
		return resolved, true
	}
	return resolveAttempt(command, dir, env, true)
}

func resolveAttempt(command, dir string, env []string, withoutPathExt bool) (string, bool) {
	root := statRoot(dir)
	pathVal := pathForWhich(env)
	var pathEnv []string
	if strings.ContainsAny(command, `/\`) {
		pathEnv = []string{""}
	} else {
		pathEnv = append([]string{root}, strings.Split(pathVal, ";")...)
	}
	extExe := os.Getenv("PATHEXT")
	if extExe == "" {
		extExe = defaultPathExt
	}
	if withoutPathExt {
		extExe = ";"
	}
	exts := strings.Split(extExe, ";")
	if strings.Contains(command, ".") && len(exts) > 0 && exts[0] != "" {
		exts = append([]string{""}, exts...)
	}
	for _, ppRaw := range pathEnv {
		pathPart := ppRaw
		if len(ppRaw) >= 2 && ppRaw[0] == '"' && ppRaw[len(ppRaw)-1] == '"' {
			pathPart = ppRaw[1 : len(ppRaw)-1]
		}
		pCmd := winJoin(pathPart, command)
		p := pCmd
		// path.win32.join("", ".\\foo") collapses to "foo". which puts the
		// "./" or ".\" prefix back on.
		if pathPart == "" && dotSlashPrefix(command) {
			p = command[:2] + pCmd
		}
		for _, ext := range exts {
			cur := p + ext
			if !isExeFile(cur, root, extExe) {
				continue
			}
			return absResolved(dir, cur), true
		}
	}
	return "", false
}

func dotSlashPrefix(command string) bool {
	return strings.HasPrefix(command, `.\`) || strings.HasPrefix(command, "./")
}

// statRoot is the directory which would have chdir'd into. A dir that cannot
// be entered leaves the process working directory in place.
func statRoot(dir string) string {
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				return abs
			}
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func pathForWhich(env []string) string {
	src := env
	if src == nil {
		src = os.Environ()
	}
	v, ok := lastEnv(src, "PATH")
	if !ok || v == "" {
		return os.Getenv("PATH")
	}
	return v
}

// absResolved is path.resolve(customCwd || "", found). An absolute found path
// does not depend on the cwd argument. The original dir is used even when
// chdir would have failed, matching resolveCommand.
func absResolved(dir, found string) string {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	if dir != "" {
		return winResolve(wd, dir, found)
	}
	return winResolve(wd, found)
}

// isExeFile is isexe's Windows check: the path is a regular file (symlinks
// are followed, as fs.stat does) and its extension is in pathExt. An empty
// extension in pathExt accepts any file. Relative paths are stated from root
// because which stats after chdir.
func isExeFile(path, root, pathExt string) bool {
	statAt := path
	if root != "" && !winIsAbsolute(path) {
		statAt = winJoin(root, path)
	}
	st, err := os.Stat(statAt)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	return checkPathExt(path, pathExt)
}

func checkPathExt(path, pathext string) bool {
	if pathext == "" {
		return true
	}
	parts := strings.Split(pathext, ";")
	for _, p := range parts {
		if p == "" {
			return true
		}
	}
	low := strings.ToLower(path)
	for _, p := range parts {
		p = strings.ToLower(p)
		if p != "" && strings.HasSuffix(low, p) {
			return true
		}
	}
	return false
}

// readShebang reads 150 bytes, zero-padded, and parses them like
// shebang-command. A read error yields no shebang.
func readShebang(path string) string {
	buf := make([]byte, 150)
	f, err := os.Open(path)
	if err == nil {
		_, _ = f.Read(buf)
		_ = f.Close()
	}
	return shebangCommand(string(buf))
}

// shebangCommand is the shebang-command package. The returned string is empty
// when there is no usable interpreter. Trailing NULs in the 150-byte buffer
// are part of the match when the shebang line has no newline, matching
// Buffer.toString plus /^#!(.*)/.
func shebangCommand(buf string) string {
	if !strings.HasPrefix(buf, "#!") {
		return ""
	}
	rest := buf[2:]
	end := len(rest)
	for i := 0; i < len(rest); {
		r, size := utf8.DecodeRuneInString(rest[i:])
		if r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' {
			end = i
			break
		}
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		i += size
	}
	body := rest[:end]
	if strings.HasPrefix(body, " ") {
		body = body[1:]
	}
	if body == "" {
		return ""
	}
	parts := strings.Split(body, " ")
	pathPart := parts[0]
	argument := ""
	if len(parts) > 1 {
		argument = parts[1]
	}
	binary := pathPart
	if i := strings.LastIndex(pathPart, "/"); i >= 0 {
		binary = pathPart[i+1:]
	}
	if binary == "env" {
		return argument
	}
	if argument != "" {
		return binary + " " + argument
	}
	return binary
}
