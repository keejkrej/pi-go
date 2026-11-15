// Ported from packages/durable/src/env/node.ts (pi v1.0.0).

package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/xspawn"
)

const (
	nodeMaxTimeoutMs        = 2_147_483_647
	nodeMaxTimeoutSeconds   = float64(nodeMaxTimeoutMs) / 1000.0
	nodeExitStdioGraceMs    = 100 * time.Millisecond
	nodeSpillHighWaterMark  = 1024 * 1024
	nodeSafeMaxInteger      = int64(1)<<53 - 1
	nodeLineReaderChunkSize = 64 * 1024
)

var nodeLegacyWslBashPattern = regexp.MustCompile(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

type nodeSpillWriter interface {
	io.Writer
	io.Closer
}

// nodeEnvTestHooks supplies deterministic seams for package-internal tests.
type nodeEnvTestHooks struct {
	createTempFileFn func(options *CreateTempFileOptions, ctx chord.Context) Result[string, *FileError]
	wrapSpillWriter  func(path string, w *os.File) (nodeSpillWriter, int)
	readAtFn         func(f *os.File, b []byte, off int64) (int, error)
	syncFileFn       func(f *os.File) error
	platformFn       func() string
	killFn           func(pid int)
}

// NodeExecutionEnvOptions configures a NodeExecutionEnv instance.
type NodeExecutionEnvOptions struct {
	Cwd       string
	ShellPath string
	ShellEnv  map[string]string
}

type nodeShellConfig struct {
	shell            string
	args             []string
	commandTransport string
}

type nodeStreamChunk struct {
	isStderr bool
	data     []byte
}

type nodeTextLineReader struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	decoder    *js.UTF8StreamDecoder
	chunk      []byte
	byteOffset int64
	buffered   string
	ended      bool
	closed     bool
	readAtFn   func(f *os.File, b []byte, off int64) (int, error)
}

var (
	_ ExecutionEnv   = (*NodeExecutionEnv)(nil)
	_ TextLineReader = (*nodeTextLineReader)(nil)
)

// NodeExecutionEnv implements ExecutionEnv against the local OS filesystem and shell.
type NodeExecutionEnv struct {
	mu              sync.Mutex
	cwd             string
	shellPath       string
	shellEnv        map[string]string
	activeChildPids map[int]struct{}
	hooks           nodeEnvTestHooks
}

// NewNodeExecutionEnv creates a local execution environment configured by options.
func NewNodeExecutionEnv(options NodeExecutionEnvOptions) *NodeExecutionEnv {
	var shellEnv map[string]string
	if len(options.ShellEnv) > 0 {
		shellEnv = make(map[string]string, len(options.ShellEnv))
		for k, v := range options.ShellEnv {
			shellEnv[k] = v
		}
	}
	return &NodeExecutionEnv{
		cwd:             options.Cwd,
		shellPath:       options.ShellPath,
		shellEnv:        shellEnv,
		activeChildPids: make(map[int]struct{}),
	}
}

func (e *NodeExecutionEnv) Id() string { return "node:local" }

func (e *NodeExecutionEnv) Cwd() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cwd
}

func (e *NodeExecutionEnv) SetCwd(cwd string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cwd = cwd
}

func (e *NodeExecutionEnv) getPlatform() string {
	if e.hooks.platformFn != nil {
		return e.hooks.platformFn()
	}
	return js.Platform()
}

func (e *NodeExecutionEnv) killPID(pid int) {
	if e.hooks.killFn != nil {
		e.hooks.killFn(pid)
		return
	}
	nodeKillProcessTree(pid)
}

func nodeResolveTimeoutDuration(timeout *float64) Result[time.Duration, *ExecutionError] {
	if timeout == nil {
		return Ok[time.Duration, *ExecutionError](0)
	}
	v := *timeout
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return Err[time.Duration, *ExecutionError](NewExecutionError("timeout", "Invalid timeout: must be a finite number of seconds", nil))
	}
	ms := v * 1000
	if ms > nodeMaxTimeoutMs {
		return Err[time.Duration, *ExecutionError](NewExecutionError(
			"timeout",
			fmt.Sprintf("Invalid timeout: maximum is %s seconds", strconv.FormatFloat(nodeMaxTimeoutSeconds, 'g', -1, 64)),
			nil,
		))
	}
	return Ok[time.Duration, *ExecutionError](time.Duration(ms * float64(time.Millisecond)))
}

func nodeFileURLToPath(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	if runtime.GOOS == "windows" {
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			return `\\` + u.Host + filepath.FromSlash(u.Path), true
		}
		p := u.Path
		if len(p) >= 3 && p[0] == '/' && ((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) && p[2] == ':' {
			return filepath.FromSlash(p[1:]), true
		}
		return filepath.FromSlash(p), true
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", false
	}
	return u.Path, true
}

func nodeResolvePathWithPlatform(cwd, p, platform string) string {
	normalized := p
	if normalized == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			normalized = home
		}
	} else if strings.HasPrefix(normalized, "~/") || (platform == "win32" && strings.HasPrefix(normalized, `~\`)) {
		if home, err := os.UserHomeDir(); err == nil {
			normalized = filepath.Join(home, normalized[2:])
		}
	} else if strings.HasPrefix(normalized, "file://") {
		if converted, ok := nodeFileURLToPath(normalized); ok {
			normalized = converted
		}
	}
	if filepath.IsAbs(normalized) {
		return filepath.Clean(normalized)
	}
	joined := filepath.Join(cwd, normalized)
	if abs, err := filepath.Abs(joined); err == nil {
		return abs
	}
	return filepath.Clean(joined)
}

func nodeFileKindFromInfo(info os.FileInfo) (FileKind, bool) {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return FileKindSymlink, true
	}
	if info.IsDir() {
		return FileKindDirectory, true
	}
	if mode.IsRegular() {
		return FileKindFile, true
	}
	return "", false
}

func nodeFileInfoFromStats(p string, info os.FileInfo) Result[FileInfo, *FileError] {
	kind, ok := nodeFileKindFromInfo(info)
	if !ok {
		return Err[FileInfo, *FileError](NewFileError(FileErrorInvalid, "Unsupported file type", p, nil))
	}
	return Ok[FileInfo, *FileError](FileInfo{
		Name:    filepath.Base(p),
		Path:    p,
		Kind:    kind,
		Size:    info.Size(),
		MtimeMs: float64(info.ModTime().UnixNano()) / 1e6,
	})
}

func nodeToFileError(err error, fallbackPath string) *FileError {
	if fe, ok := err.(*FileError); ok && fe != nil {
		return fe
	}
	if err == nil {
		return NewFileError(FileErrorUnknown, "unknown error", fallbackPath, nil)
	}
	errPath := fallbackPath
	var pe *os.PathError
	if errors.As(err, &pe) && pe.Path != "" {
		errPath = pe.Path
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return NewFileError(FileErrorAborted, msg, errPath, err)
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOENT):
		return NewFileError(FileErrorNotFound, msg, errPath, err)
	case errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM):
		return NewFileError(FileErrorPermissionDenied, msg, errPath, err)
	case errors.Is(err, syscall.ENOTDIR):
		return NewFileError(FileErrorNotDirectory, msg, errPath, err)
	case errors.Is(err, syscall.EISDIR):
		return NewFileError(FileErrorIsDirectory, msg, errPath, err)
	case errors.Is(err, fs.ErrInvalid) || errors.Is(err, syscall.EINVAL):
		return NewFileError(FileErrorInvalid, msg, errPath, err)
	default:
		return NewFileError(FileErrorUnknown, msg, errPath, err)
	}
}

func nodeAbortError(ctx chord.Context, path string) *FileError {
	if ctx == nil {
		return nil
	}
	sig := ctx.AbortSignal()
	if sig == nil {
		return nil
	}
	if sigErr := sig.Err(); sigErr != nil {
		return NewFileError(FileErrorAborted, "aborted", path, sigErr)
	}
	return nil
}

func nodePathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func nodeFindBashOnPath(platform string) string {
	name := "bash"
	if platform == "win32" {
		name = "bash.exe"
	}
	found, err := exec.LookPath(name)
	if err != nil || found == "" {
		return ""
	}
	if nodePathExists(found) {
		return found
	}
	return ""
}

func nodeIsLegacyWslBashPath(p string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(p, "/", `\`))
	return nodeLegacyWslBashPattern.MatchString(normalized)
}

func nodeGetBashShellConfig(shell string) nodeShellConfig {
	if nodeIsLegacyWslBashPath(shell) {
		return nodeShellConfig{shell: shell, args: []string{"-s"}, commandTransport: "stdin"}
	}
	return nodeShellConfig{shell: shell, args: []string{"-c"}, commandTransport: "argv"}
}

func nodeGetShellConfig(customShellPath string, platform string) Result[nodeShellConfig, *ExecutionError] {
	if customShellPath != "" {
		if nodePathExists(customShellPath) {
			return Ok[nodeShellConfig, *ExecutionError](nodeGetBashShellConfig(customShellPath))
		}
		return Err[nodeShellConfig, *ExecutionError](NewExecutionError(
			ExecutionErrorShellUnavailable,
			"Custom shell path not found: "+customShellPath,
			nil,
		))
	}
	if platform == "win32" {
		var candidates []string
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			candidates = append(candidates, pf+`\Git\bin\bash.exe`)
		}
		if pfx86 := os.Getenv("ProgramFiles(x86)"); pfx86 != "" {
			candidates = append(candidates, pfx86+`\Git\bin\bash.exe`)
		}
		for _, candidate := range candidates {
			if nodePathExists(candidate) {
				return Ok[nodeShellConfig, *ExecutionError](nodeGetBashShellConfig(candidate))
			}
		}
		if bashOnPath := nodeFindBashOnPath(platform); bashOnPath != "" {
			return Ok[nodeShellConfig, *ExecutionError](nodeGetBashShellConfig(bashOnPath))
		}
		searched := make([]string, len(candidates))
		for i, p := range candidates {
			searched[i] = "  " + p
		}
		return Err[nodeShellConfig, *ExecutionError](NewExecutionError(
			ExecutionErrorShellUnavailable,
			"No bash shell found. Options:\n"+
				"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
				"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
				"  3. Configure an explicit shellPath\n\n"+
				"Searched Git Bash in:\n"+strings.Join(searched, "\n"),
			nil,
		))
	}
	if nodePathExists("/bin/bash") {
		return Ok[nodeShellConfig, *ExecutionError](nodeGetBashShellConfig("/bin/bash"))
	}
	if bashOnPath := nodeFindBashOnPath(platform); bashOnPath != "" {
		return Ok[nodeShellConfig, *ExecutionError](nodeGetBashShellConfig(bashOnPath))
	}
	return Ok[nodeShellConfig, *ExecutionError](nodeShellConfig{shell: "sh", args: []string{"-c"}, commandTransport: "argv"})
}

func nodeGetShellEnv(baseEnv, extraEnv map[string]string, inheritEnv bool) []string {
	merged := make(map[string]string)
	if inheritEnv {
		for _, entry := range os.Environ() {
			if k, v, ok := strings.Cut(entry, "="); ok {
				merged[k] = v
			}
		}
		for k, v := range baseEnv {
			merged[k] = v
		}
	}
	for k, v := range extraEnv {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func (r *nodeTextLineReader) ReadLine(ctx chord.Context) Result[*TextLine, *FileError] {
	if fe := nodeAbortError(ctx, r.path); fe != nil {
		return Err[*TextLine, *FileError](fe)
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Err[*TextLine, *FileError](NewFileError(FileErrorInvalid, "Text line reader is closed", r.path, nil))
	}
	r.mu.Unlock()

	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return Err[*TextLine, *FileError](NewFileError(FileErrorInvalid, "Text line reader is closed", r.path, nil))
		}
		if idx := strings.IndexByte(r.buffered, '\n'); idx != -1 {
			text := r.buffered[:idx]
			r.buffered = r.buffered[idx+1:]
			r.mu.Unlock()
			return Ok[*TextLine, *FileError](&TextLine{Text: text, Terminated: true})
		}
		if r.ended {
			if len(r.buffered) == 0 {
				r.mu.Unlock()
				return Ok[*TextLine, *FileError](nil)
			}
			text := r.buffered
			r.buffered = ""
			r.mu.Unlock()
			return Ok[*TextLine, *FileError](&TextLine{Text: text, Terminated: false})
		}
		file := r.file
		offset := r.byteOffset
		buf := r.chunk
		readAtFn := r.readAtFn
		r.mu.Unlock()

		var n int
		var readErr error
		if readAtFn != nil {
			n, readErr = readAtFn(file, buf, offset)
		} else {
			n, readErr = file.ReadAt(buf, offset)
		}

		if fe := nodeAbortError(ctx, r.path); fe != nil {
			return Err[*TextLine, *FileError](fe)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Err[*TextLine, *FileError](nodeToFileError(readErr, r.path))
		}

		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return Err[*TextLine, *FileError](NewFileError(FileErrorInvalid, "Text line reader is closed", r.path, nil))
		}
		r.byteOffset += int64(n)
		if n > 0 {
			r.buffered += r.decoder.Decode(buf[:n])
		}
		if n == 0 || errors.Is(readErr, io.EOF) {
			r.buffered += r.decoder.Flush()
			r.ended = true
		}
		r.mu.Unlock()
	}
}

func (r *nodeTextLineReader) Close(_ chord.Context) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.buffered = ""
	file := r.file
	r.mu.Unlock()
	if file != nil {
		_ = file.Close()
	}
}

func (e *NodeExecutionEnv) AbsolutePath(p string, _ chord.Context) Result[string, *FileError] {
	return Ok[string, *FileError](nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform()))
}

func (e *NodeExecutionEnv) JoinPath(parts []string, _ chord.Context) Result[string, *FileError] {
	return Ok[string, *FileError](filepath.Join(parts...))
}

func (e *NodeExecutionEnv) OpenTextLineReader(p string, ctx chord.Context) Result[TextLineReader, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[TextLineReader, *FileError](fe)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return Err[TextLineReader, *FileError](nodeToFileError(err, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		_ = file.Close()
		return Err[TextLineReader, *FileError](fe)
	}
	return Ok[TextLineReader, *FileError](&nodeTextLineReader{
		file:     file,
		path:     resolved,
		decoder:  js.NewUTF8StreamDecoder(),
		chunk:    make([]byte, nodeLineReaderChunkSize),
		readAtFn: e.hooks.readAtFn,
	})
}

func (e *NodeExecutionEnv) ReadTextFile(p string, ctx chord.Context) Result[string, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[string, *FileError](fe)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return Err[string, *FileError](nodeToFileError(err, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[string, *FileError](fe)
	}
	return Ok[string, *FileError](string(data))
}

func (e *NodeExecutionEnv) ReadTextLines(p string, options *ReadTextLinesOptions, ctx chord.Context) Result[[]string, *FileError] {
	if options != nil && options.MaxLines != nil && *options.MaxLines <= 0 {
		return Ok[[]string, *FileError]([]string{})
	}
	opened := e.OpenTextLineReader(p, ctx)
	if !opened.Ok {
		return Err[[]string, *FileError](opened.Error)
	}
	reader := opened.Value
	defer reader.Close(ctx)

	lines := make([]string, 0)
	for options == nil || options.MaxLines == nil || len(lines) < *options.MaxLines {
		line := reader.ReadLine(ctx)
		if !line.Ok {
			return Err[[]string, *FileError](line.Error)
		}
		if line.Value == nil {
			break
		}
		lines = append(lines, line.Value.Text)
	}
	return Ok[[]string, *FileError](lines)
}

func (e *NodeExecutionEnv) ReadBinaryFile(p string, ctx chord.Context) Result[[]byte, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[[]byte, *FileError](fe)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return Err[[]byte, *FileError](nodeToFileError(err, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[[]byte, *FileError](fe)
	}
	return Ok[[]byte, *FileError](data)
}

func (e *NodeExecutionEnv) WriteFile(p string, content []byte, ctx chord.Context) Result[struct{}, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o777); err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	if err := os.WriteFile(resolved, content, 0o666); err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	return Ok[struct{}, *FileError](struct{}{})
}

func (e *NodeExecutionEnv) AppendFile(p string, content []byte, ctx chord.Context) Result[struct{}, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o777); err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	f, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if writeErr != nil {
		return Err[struct{}, *FileError](nodeToFileError(writeErr, resolved))
	}
	if closeErr != nil {
		return Err[struct{}, *FileError](nodeToFileError(closeErr, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	return Ok[struct{}, *FileError](struct{}{})
}

func (e *NodeExecutionEnv) TruncateFile(p string, size int64, ctx chord.Context) Result[struct{}, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	if size < 0 || size > nodeSafeMaxInteger {
		return Err[struct{}, *FileError](NewFileError(FileErrorInvalid, "File size must be a non-negative safe integer", resolved, nil))
	}
	f, err := os.OpenFile(resolved, os.O_RDWR, 0)
	if err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	truncErr := f.Truncate(size)
	_ = f.Close()
	if truncErr != nil {
		return Err[struct{}, *FileError](nodeToFileError(truncErr, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	return Ok[struct{}, *FileError](struct{}{})
}

func (e *NodeExecutionEnv) FlushFile(p string, ctx chord.Context) Result[struct{}, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	f, err := os.OpenFile(resolved, os.O_RDWR, 0)
	if err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	var syncErr error
	if e.hooks.syncFileFn != nil {
		syncErr = e.hooks.syncFileFn(f)
	} else {
		syncErr = f.Sync()
	}
	_ = f.Close()
	if syncErr != nil {
		return Err[struct{}, *FileError](nodeToFileError(syncErr, resolved))
	}
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	return Ok[struct{}, *FileError](struct{}{})
}

func (e *NodeExecutionEnv) RenameFile(sourcePath, destinationPath string, ctx chord.Context) Result[struct{}, *FileError] {
	source := nodeResolvePathWithPlatform(e.Cwd(), sourcePath, e.getPlatform())
	destination := nodeResolvePathWithPlatform(e.Cwd(), destinationPath, e.getPlatform())
	if fe := nodeAbortError(ctx, destination); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	if err := os.Rename(source, destination); err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, source))
	}
	return Ok[struct{}, *FileError](struct{}{})
}

func (e *NodeExecutionEnv) FileInfo(p string, ctx chord.Context) Result[FileInfo, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[FileInfo, *FileError](fe)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return Err[FileInfo, *FileError](nodeToFileError(err, resolved))
	}
	return nodeFileInfoFromStats(resolved, info)
}

func (e *NodeExecutionEnv) ListDir(p string, ctx chord.Context) Result[[]FileInfo, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[[]FileInfo, *FileError](fe)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return Err[[]FileInfo, *FileError](nodeToFileError(err, resolved))
	}
	infos := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		if fe := nodeAbortError(ctx, resolved); fe != nil {
			return Err[[]FileInfo, *FileError](fe)
		}
		entryPath := filepath.Join(resolved, entry.Name())
		stat, statErr := os.Lstat(entryPath)
		if statErr != nil {
			return Err[[]FileInfo, *FileError](nodeToFileError(statErr, entryPath))
		}
		infoRes := nodeFileInfoFromStats(entryPath, stat)
		if infoRes.Ok {
			infos = append(infos, infoRes.Value)
		}
	}
	return Ok[[]FileInfo, *FileError](infos)
}

func (e *NodeExecutionEnv) CanonicalPath(p string, ctx chord.Context) Result[string, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[string, *FileError](fe)
	}
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return Err[string, *FileError](nodeToFileError(err, resolved))
	}
	return Ok[string, *FileError](real)
}

func (e *NodeExecutionEnv) Exists(p string, ctx chord.Context) Result[bool, *FileError] {
	res := e.FileInfo(p, ctx)
	if res.Ok {
		return Ok[bool, *FileError](true)
	}
	if res.Error != nil && res.Error.Code == FileErrorNotFound {
		return Ok[bool, *FileError](false)
	}
	return Err[bool, *FileError](res.Error)
}

func (e *NodeExecutionEnv) CreateDir(p string, options *CreateDirOptions, ctx chord.Context) Result[struct{}, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	recursive := true
	if options != nil && options.Recursive != nil {
		recursive = *options.Recursive
	}
	var err error
	if recursive {
		err = os.MkdirAll(resolved, 0o777)
	} else {
		err = os.Mkdir(resolved, 0o777)
	}
	if err != nil {
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	return Ok[struct{}, *FileError](struct{}{})
}

func (e *NodeExecutionEnv) Remove(p string, options *RemoveOptions, ctx chord.Context) Result[struct{}, *FileError] {
	resolved := nodeResolvePathWithPlatform(e.Cwd(), p, e.getPlatform())
	if fe := nodeAbortError(ctx, resolved); fe != nil {
		return Err[struct{}, *FileError](fe)
	}
	recursive := false
	force := false
	if options != nil {
		recursive = options.Recursive
		force = options.Force
	}

	info, lstatErr := os.Lstat(resolved)
	if lstatErr != nil {
		if force && errors.Is(lstatErr, fs.ErrNotExist) {
			return Ok[struct{}, *FileError](struct{}{})
		}
		return Err[struct{}, *FileError](nodeToFileError(lstatErr, resolved))
	}

	if recursive {
		if err := os.RemoveAll(resolved); err != nil {
			if force && errors.Is(err, fs.ErrNotExist) {
				return Ok[struct{}, *FileError](struct{}{})
			}
			return Err[struct{}, *FileError](nodeToFileError(err, resolved))
		}
		return Ok[struct{}, *FileError](struct{}{})
	}

	if info.IsDir() {
		return Err[struct{}, *FileError](NewFileError(FileErrorIsDirectory, "Path is a directory: "+resolved, resolved, syscall.EISDIR))
	}
	if err := os.Remove(resolved); err != nil {
		if force && errors.Is(err, fs.ErrNotExist) {
			return Ok[struct{}, *FileError](struct{}{})
		}
		return Err[struct{}, *FileError](nodeToFileError(err, resolved))
	}
	return Ok[struct{}, *FileError](struct{}{})
}

func (e *NodeExecutionEnv) CreateTempDir(prefix string, ctx chord.Context) Result[string, *FileError] {
	if fe := nodeAbortError(ctx, ""); fe != nil {
		return Err[string, *FileError](fe)
	}
	if prefix == "" {
		prefix = "tmp-"
	}
	dir, err := os.MkdirTemp("", prefix+"*")
	if err != nil {
		return Err[string, *FileError](nodeToFileError(err, ""))
	}
	return Ok[string, *FileError](dir)
}

func (e *NodeExecutionEnv) CreateTempFile(options *CreateTempFileOptions, ctx chord.Context) Result[string, *FileError] {
	dir := e.CreateTempDir("tmp-", ctx)
	if !dir.Ok {
		return dir
	}
	var prefix, suffix string
	if options != nil {
		prefix = options.Prefix
		suffix = options.Suffix
	}
	filePath := filepath.Join(dir.Value, prefix+uuid.NewString()+suffix)
	if err := os.WriteFile(filePath, nil, 0o666); err != nil {
		return Err[string, *FileError](nodeToFileError(err, filePath))
	}
	return Ok[string, *FileError](filePath)
}

func (e *NodeExecutionEnv) createSpillTempFile(ctx chord.Context) Result[string, *FileError] {
	opts := &CreateTempFileOptions{Prefix: "pi-output-", Suffix: ".log"}
	if e.hooks.createTempFileFn != nil {
		return e.hooks.createTempFileFn(opts, ctx)
	}
	return e.CreateTempFile(opts, ctx)
}

func (e *NodeExecutionEnv) Cleanup(_ chord.Context) {
	e.mu.Lock()
	pids := make([]int, 0, len(e.activeChildPids))
	for pid := range e.activeChildPids {
		pids = append(pids, pid)
	}
	clear(e.activeChildPids)
	e.mu.Unlock()

	for _, pid := range pids {
		e.killPID(pid)
	}
}

type nodeSpillManager struct {
	mu            sync.Mutex
	env           *NodeExecutionEnv
	ctx           chord.Context
	highWaterMark int
	started       bool
	opened        bool
	inFlightWrite bool
	backpressured bool
	queuedBytes   int
	queue         [][]byte
	closedInput   bool
	spillPath     string
	spillErr      *ExecutionError
	wakeCh        chan struct{}
	notifyCh      chan struct{}
	doneCh        chan struct{}
	onErrorKill   func()
}

func nodeNewSpillManager(env *NodeExecutionEnv, ctx chord.Context, notifyCh chan struct{}, onErrorKill func()) *nodeSpillManager {
	return &nodeSpillManager{
		env:           env,
		ctx:           ctx,
		highWaterMark: nodeSpillHighWaterMark,
		wakeCh:        make(chan struct{}, 1),
		notifyCh:      notifyCh,
		doneCh:        make(chan struct{}),
		onErrorKill:   onErrorKill,
	}
}

func (s *nodeSpillManager) notify() {
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}
}

func (s *nodeSpillManager) enqueue(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	copied := make([]byte, len(chunk))
	copy(copied, chunk)

	var startWorker bool
	s.mu.Lock()
	if s.spillErr != nil || s.closedInput {
		s.mu.Unlock()
		return
	}
	if !s.started {
		s.started = true
		startWorker = true
	}
	s.queue = append(s.queue, copied)
	s.queuedBytes += len(copied)
	if s.queuedBytes >= s.highWaterMark {
		s.backpressured = true
	}
	s.mu.Unlock()

	if startWorker {
		go s.run()
	} else {
		select {
		case s.wakeCh <- struct{}{}:
		default:
		}
	}
}

func (s *nodeSpillManager) isBackpressured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spillErr == nil && s.backpressured
}

func (s *nodeSpillManager) isDraining() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spillErr != nil || !s.started {
		return false
	}
	return !s.opened || s.inFlightWrite || s.backpressured || s.queuedBytes > 0
}

func (s *nodeSpillManager) hasStarted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func (s *nodeSpillManager) finish() (string, *ExecutionError) {
	s.mu.Lock()
	started := s.started
	if started {
		s.closedInput = true
	}
	s.mu.Unlock()
	if !started {
		return "", nil
	}
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
	<-s.doneCh
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spillPath, s.spillErr
}

func (s *nodeSpillManager) fail(err error) {
	s.mu.Lock()
	if s.spillErr == nil {
		s.spillErr = NewExecutionError(
			ExecutionErrorUnknown,
			"Failed to preserve complete shell output: "+err.Error(),
			err,
		)
		s.backpressured = false
		s.inFlightWrite = false
	}
	s.mu.Unlock()
	s.onErrorKill()
	s.notify()
}

func (s *nodeSpillManager) run() {
	defer close(s.doneCh)

	created := s.env.createSpillTempFile(s.ctx)
	if !created.Ok {
		s.fail(created.Error)
		return
	}
	filePath := created.Value
	rawFile, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		s.fail(err)
		return
	}

	var writer nodeSpillWriter = rawFile
	hwm := nodeSpillHighWaterMark
	if s.env.hooks.wrapSpillWriter != nil {
		var customHWM int
		writer, customHWM = s.env.hooks.wrapSpillWriter(filePath, rawFile)
		if customHWM > 0 {
			hwm = customHWM
		}
	}

	s.mu.Lock()
	s.spillPath = filePath
	s.highWaterMark = hwm
	s.opened = true
	s.backpressured = s.queuedBytes >= s.highWaterMark
	s.mu.Unlock()
	s.notify()

	for {
		s.mu.Lock()
		if s.spillErr != nil {
			s.mu.Unlock()
			_ = writer.Close()
			return
		}
		if len(s.queue) == 0 {
			if s.closedInput {
				s.inFlightWrite = false
				s.backpressured = false
				s.mu.Unlock()
				if err := writer.Close(); err != nil {
					s.fail(err)
				}
				return
			}
			s.inFlightWrite = false
			s.backpressured = false
			s.mu.Unlock()
			s.notify()
			<-s.wakeCh
			continue
		}
		chunk := s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		if len(s.queue) == 0 {
			s.queue = nil
		}
		s.queuedBytes -= len(chunk)
		s.inFlightWrite = true
		s.backpressured = s.queuedBytes >= s.highWaterMark
		s.mu.Unlock()

		if _, err := writer.Write(chunk); err != nil {
			_ = writer.Close()
			s.fail(err)
			return
		}
		s.mu.Lock()
		s.inFlightWrite = false
		s.backpressured = s.queuedBytes >= s.highWaterMark
		s.mu.Unlock()
		s.notify()
	}
}

func (e *NodeExecutionEnv) Exec(command string, options *ShellExecOptions, ctx chord.Context) Result[ShellExecResult, *ExecutionError] {
	var sig context.Context
	if ctx != nil {
		sig = ctx.AbortSignal()
	}
	if sig != nil && sig.Err() != nil {
		return Err[ShellExecResult, *ExecutionError](NewExecutionError(ExecutionErrorAborted, "aborted", sig.Err()))
	}

	var timeoutPtr *float64
	if options != nil {
		timeoutPtr = options.Timeout
	}
	timeoutRes := nodeResolveTimeoutDuration(timeoutPtr)
	if !timeoutRes.Ok {
		return Err[ShellExecResult, *ExecutionError](timeoutRes.Error)
	}
	timeoutDur := timeoutRes.Value

	platform := e.getPlatform()
	cwd := e.Cwd()
	if options != nil && options.Cwd != "" {
		cwd = nodeResolvePathWithPlatform(cwd, options.Cwd, platform)
	}
	shellCfgRes := nodeGetShellConfig(e.shellPath, platform)
	if !shellCfgRes.Ok {
		return Err[ShellExecResult, *ExecutionError](shellCfgRes.Error)
	}
	shellCfg := shellCfgRes.Value

	if _, err := os.Stat(cwd); err != nil {
		return Err[ShellExecResult, *ExecutionError](NewExecutionError(
			ExecutionErrorSpawnError,
			fmt.Sprintf("Working directory does not exist: %s\nCannot execute bash commands.", cwd),
			err,
		))
	}

	inheritEnv := true
	var extraEnv map[string]string
	if options != nil {
		extraEnv = options.Env
		if options.InheritEnv != nil {
			inheritEnv = *options.InheritEnv
		}
	}
	cmdEnv := nodeGetShellEnv(e.shellEnv, extraEnv, inheritEnv)

	args := append([]string(nil), shellCfg.args...)
	if shellCfg.commandTransport != "stdin" {
		args = append(args, command)
	}

	cmd := xspawn.CommandIn(context.Background(), cwd, cmdEnv, shellCfg.shell, args)
	nodeSetProcessGroup(cmd)

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return Err[ShellExecResult, *ExecutionError](NewExecutionError(ExecutionErrorSpawnError, err.Error(), err))
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return Err[ShellExecResult, *ExecutionError](NewExecutionError(ExecutionErrorSpawnError, err.Error(), err))
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	var stdinW *os.File
	if shellCfg.commandTransport == "stdin" {
		stdinR, w, pipeErr := os.Pipe()
		if pipeErr != nil {
			_ = stdoutR.Close()
			_ = stdoutW.Close()
			_ = stderrR.Close()
			_ = stderrW.Close()
			return Err[ShellExecResult, *ExecutionError](NewExecutionError(ExecutionErrorSpawnError, pipeErr.Error(), pipeErr))
		}
		cmd.Stdin = stdinR
		stdinW = w
		defer stdinR.Close()
	}

	if err := cmd.Start(); err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		if stdinW != nil {
			_ = stdinW.Close()
		}
		err = xspawn.VerifyENOENT(cmd, err)
		return Err[ShellExecResult, *ExecutionError](NewExecutionError(ExecutionErrorSpawnError, err.Error(), err))
	}

	_ = stdoutW.Close()
	_ = stderrW.Close()
	if stdinW != nil {
		go func(w *os.File, body string) {
			_, _ = io.WriteString(w, body)
			_ = w.Close()
		}(stdinW, command)
	}

	pid := cmd.Process.Pid
	e.mu.Lock()
	e.activeChildPids[pid] = struct{}{}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.activeChildPids, pid)
		e.mu.Unlock()
	}()

	var killOnce sync.Once
	killProcess := func() {
		killOnce.Do(func() {
			e.killPID(pid)
		})
	}

	var timedOut atomic.Bool
	var timeoutTimer *time.Timer
	if timeoutDur > 0 {
		timeoutTimer = time.AfterFunc(timeoutDur, func() {
			timedOut.Store(true)
			killProcess()
		})
	}
	if timeoutTimer != nil {
		defer timeoutTimer.Stop()
	}

	doneCh := make(chan struct{})
	defer close(doneCh)

	if sig != nil {
		if sig.Err() != nil {
			killProcess()
		} else {
			go func() {
				select {
				case <-sig.Done():
					killProcess()
				case <-doneCh:
				}
			}()
		}
	}

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- xspawn.VerifyENOENT(cmd, cmd.Wait())
	}()

	chunkCh := make(chan nodeStreamChunk)
	stdoutDoneCh := make(chan struct{})
	stderrDoneCh := make(chan struct{})
	stopReadCh := make(chan struct{})

	readPipe := func(r *os.File, isStderr bool, endedCh chan struct{}) {
		defer close(endedCh)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case chunkCh <- nodeStreamChunk{isStderr: isStderr, data: data}:
				case <-stopReadCh:
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}

	go readPipe(stdoutR, false, stdoutDoneCh)
	go readPipe(stderrR, true, stderrDoneCh)

	spillNotifyCh := make(chan struct{}, 1)
	spillMgr := nodeNewSpillManager(e, ctx, spillNotifyCh, killProcess)

	var callbackErr *ExecutionError
	emit := func(text string) {
		if text == "" || options == nil || options.OnOutput == nil || callbackErr != nil {
			return
		}
		func() {
			defer func() {
				if r := recover(); r != nil && callbackErr == nil {
					cause := ToError(r)
					callbackErr = NewExecutionError(ExecutionErrorCallbackError, cause.Error(), cause)
					killProcess()
				}
			}()
			if err := options.OnOutput(text, ctx); err != nil {
				callbackErr = NewExecutionError(ExecutionErrorCallbackError, err.Error(), err)
				killProcess()
			}
		}()
	}

	stdoutDecoder := js.NewUTF8StreamDecoder()
	stderrDecoder := js.NewUTF8StreamDecoder()
	var spillPrefix [][]byte
	var seenBytes int
	var seenNewlines int
	var spillOpts *ShellSpillOptions
	if options != nil {
		spillOpts = options.Spill
	}

	handleChunk := func(chunk nodeStreamChunk) {
		if chunk.isStderr {
			emit(stderrDecoder.Decode(chunk.data))
		} else {
			emit(stdoutDecoder.Decode(chunk.data))
		}
		if spillOpts == nil || len(chunk.data) == 0 {
			return
		}
		if spillMgr.hasStarted() {
			spillMgr.enqueue(chunk.data)
			return
		}
		seenBytes += len(chunk.data)
		for _, b := range chunk.data {
			if b == '\n' {
				seenNewlines++
			}
		}
		lines := seenNewlines
		if chunk.data[len(chunk.data)-1] != '\n' {
			lines++
		}
		if seenBytes <= spillOpts.AfterBytes && lines <= spillOpts.AfterLines {
			spillPrefix = append(spillPrefix, chunk.data)
			return
		}
		for _, prefix := range spillPrefix {
			spillMgr.enqueue(prefix)
		}
		spillPrefix = nil
		spillMgr.enqueue(chunk.data)
	}

	var exited bool
	var waitErr error
	stdoutEnded := false
	stderrEnded := false
	graceTimer := time.NewTimer(time.Hour)
	graceTimer.Stop()
	defer graceTimer.Stop()

	resetGraceTimer := func() {
		if !graceTimer.Stop() {
			select {
			case <-graceTimer.C:
			default:
			}
		}
		graceTimer.Reset(nodeExitStdioGraceMs)
	}

	for {
		if exited && stdoutEnded && stderrEnded {
			break
		}
		var activeChunkCh <-chan nodeStreamChunk
		if !spillMgr.isBackpressured() {
			activeChunkCh = chunkCh
		}
		var activeStdoutDone <-chan struct{}
		if !stdoutEnded {
			activeStdoutDone = stdoutDoneCh
		}
		var activeStderrDone <-chan struct{}
		if !stderrEnded {
			activeStderrDone = stderrDoneCh
		}
		var activeExitCh <-chan error
		if !exited {
			activeExitCh = exitCh
		}

		select {
		case chunk := <-activeChunkCh:
			handleChunk(chunk)
			if exited {
				resetGraceTimer()
			}
		case <-activeStdoutDone:
			stdoutEnded = true
		case <-activeStderrDone:
			stderrEnded = true
		case err := <-activeExitCh:
			exited = true
			waitErr = err
			if !(stdoutEnded && stderrEnded) {
				resetGraceTimer()
			}
		case <-spillNotifyCh:
			// Re-evaluate spill backpressure on next iteration.
		case <-graceTimer.C:
			if exited {
				if spillMgr.isDraining() {
					resetGraceTimer()
				} else {
					goto forceCloseStdio
				}
			}
		}
	}

forceCloseStdio:
	close(stopReadCh)
	_ = stdoutR.Close()
	_ = stderrR.Close()
	<-stdoutDoneCh
	<-stderrDoneCh

	spillPath, spillErr := spillMgr.finish()

	emit(stdoutDecoder.Flush())
	emit(stderrDecoder.Flush())

	if callbackErr != nil {
		return Err[ShellExecResult, *ExecutionError](callbackErr)
	}
	if timedOut.Load() {
		errMsg := "timeout:undefined"
		if options != nil && options.Timeout != nil {
			errMsg = "timeout:" + strconv.FormatFloat(*options.Timeout, 'g', -1, 64)
		}
		execErr := NewExecutionError(ExecutionErrorTimeout, errMsg, nil)
		if spillPath != "" {
			execErr.SpillPath = spillPath
		}
		return Err[ShellExecResult, *ExecutionError](execErr)
	}
	if sig != nil && sig.Err() != nil {
		execErr := NewExecutionError(ExecutionErrorAborted, "aborted", sig.Err())
		if spillPath != "" {
			execErr.SpillPath = spillPath
		}
		return Err[ShellExecResult, *ExecutionError](execErr)
	}
	if spillErr != nil {
		return Err[ShellExecResult, *ExecutionError](spillErr)
	}

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if sigCode, ok := nodeSignalExitCode(exitErr.ProcessState); ok {
				exitCode = sigCode
			} else {
				exitCode = exitErr.ExitCode()
				if exitCode < 0 {
					exitCode = 1
				}
			}
		} else {
			return Err[ShellExecResult, *ExecutionError](NewExecutionError(ExecutionErrorSpawnError, waitErr.Error(), waitErr))
		}
	}

	return Ok[ShellExecResult, *ExecutionError](ShellExecResult{
		ExitCode:  exitCode,
		SpillPath: spillPath,
	})
}
