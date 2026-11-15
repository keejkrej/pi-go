// Ported from packages/durable/test/env-node.test.ts (pi v1.0.0).

package env

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/chord"
)

func init() {
	switch os.Getenv("PI_ENV_TEST_HELPER") {
	case "split-emoji":
		b := []byte("😀")
		_, _ = os.Stdout.Write(b[:2])
		time.Sleep(50 * time.Millisecond)
		_, _ = os.Stdout.Write(b[2:])
		os.Exit(0)
	case "raw-bytes":
		_, _ = os.Stdout.Write([]byte{0x66, 0x80, 0x00, 0x6f})
		os.Exit(0)
	case "large-x":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("x", 500_000)))
		os.Exit(0)
	}
}

func tenMust[T any](t *testing.T, res Result[T, *FileError]) T {
	t.Helper()
	v, err := GetOrThrow(res)
	if err != nil {
		t.Fatalf("unexpected FileError: %+v", err)
	}
	return v
}

func tenMustExec(t *testing.T, res Result[ShellExecResult, *ExecutionError]) ShellExecResult {
	t.Helper()
	v, err := GetOrThrow(res)
	if err != nil {
		t.Fatalf("unexpected ExecutionError: %+v", err)
	}
	return v
}

func tenAbortedContext() chord.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return chord.WithAbortSignal(ctx, chord.BackgroundContext)
}

func tenCollectShellOutput(
	e *NodeExecutionEnv,
	command string,
	options *ShellExecOptions,
	ctx chord.Context,
) (Result[ShellExecResult, *ExecutionError], string) {
	var b strings.Builder
	opts := &ShellExecOptions{}
	if options != nil {
		*opts = *options
	}
	prev := opts.OnOutput
	opts.OnOutput = func(text string, c chord.Context) error {
		b.WriteString(text)
		if prev != nil {
			return prev(text, c)
		}
		return nil
	}
	res := e.Exec(command, opts, ctx)
	return res, b.String()
}

func tenShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func tenHelperCommand(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return fmt.Sprintf("PI_ENV_TEST_HELPER=%s %s -test.run='^$'", mode, tenShellQuote(exe))
}

func tenWaitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNodeExecutionEnvFilesystem(t *testing.T) {
	t.Run("reads, writes, lists, and removes files and directories", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext

		if got := tenMust(t, e.AbsolutePath("nested/child", bg)); got != filepath.Join(root, "nested", "child") {
			t.Fatalf("AbsolutePath got %q, want %q", got, filepath.Join(root, "nested", "child"))
		}
		if got := tenMust(t, e.JoinPath([]string{root, "nested", "child"}, bg)); got != filepath.Join(root, "nested", "child") {
			t.Fatalf("JoinPath got %q", got)
		}

		tenMust(t, e.CreateDir("nested/child", nil, bg))
		tenMust(t, e.WriteFile("nested/child/file.txt", []byte("hel"), bg))
		tenMust(t, e.AppendFile("nested/child/file.txt", []byte("lo"), bg))
		if got := tenMust(t, e.ReadTextFile("nested/child/file.txt", bg)); got != "hello" {
			t.Fatalf("ReadTextFile got %q", got)
		}
		one := 1
		if got := tenMust(t, e.ReadTextLines("nested/child/file.txt", &ReadTextLinesOptions{MaxLines: &one}, bg)); !reflect.DeepEqual(got, []string{"hello"}) {
			t.Fatalf("ReadTextLines got %#v", got)
		}
		if got := string(tenMust(t, e.ReadBinaryFile("nested/child/file.txt", bg))); got != "hello" {
			t.Fatalf("ReadBinaryFile got %q", got)
		}

		entries := tenMust(t, e.ListDir("nested/child", bg))
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].Name != "file.txt" || entries[0].Path != filepath.Join(root, "nested", "child", "file.txt") || entries[0].Kind != FileKindFile || entries[0].Size != 5 || entries[0].MtimeMs <= 0 {
			t.Fatalf("unexpected entry: %+v", entries[0])
		}

		if !tenMust(t, e.Exists("nested/child/file.txt", bg)) {
			t.Fatalf("expected file to exist")
		}
		tenMust(t, e.Remove("nested/child/file.txt", nil, bg))
		if tenMust(t, e.Exists("nested/child/file.txt", bg)) {
			t.Fatalf("expected file to be removed")
		}
	})

	t.Run("expands home-relative paths and file URLs", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		if got := tenMust(t, e.AbsolutePath("~/pi-node-env-test", bg)); got != filepath.Join(home, "pi-node-env-test") {
			t.Fatalf("got %q", got)
		}
		filePath := filepath.Join(root, "file with spaces.txt")
		fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filePath)}).String()
		if got := tenMust(t, e.AbsolutePath(fileURL, bg)); got != filePath {
			t.Fatalf("got %q, want %q", got, filePath)
		}
	})

	t.Run("returns fileInfo for files, directories, and symlinks without following symlinks", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		rec := true
		tenMust(t, e.CreateDir("dir", &CreateDirOptions{Recursive: &rec}, bg))
		tenMust(t, e.WriteFile("dir/file.txt", []byte("hello"), bg))
		if err := os.Symlink(filepath.Join(root, "dir/file.txt"), filepath.Join(root, "file-link")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if err := os.Symlink(filepath.Join(root, "dir"), filepath.Join(root, "dir-link")); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		dirInfo := tenMust(t, e.FileInfo("dir", bg))
		if dirInfo.Name != "dir" || dirInfo.Kind != FileKindDirectory {
			t.Fatalf("unexpected dir info: %+v", dirInfo)
		}
		fileInfo := tenMust(t, e.FileInfo("dir/file.txt", bg))
		if fileInfo.Name != "file.txt" || fileInfo.Kind != FileKindFile || fileInfo.Size != 5 {
			t.Fatalf("unexpected file info: %+v", fileInfo)
		}
		linkInfo := tenMust(t, e.FileInfo("file-link", bg))
		if linkInfo.Name != "file-link" || linkInfo.Kind != FileKindSymlink {
			t.Fatalf("unexpected file-link info: %+v", linkInfo)
		}
		dirLinkInfo := tenMust(t, e.FileInfo("dir-link", bg))
		if dirLinkInfo.Name != "dir-link" || dirLinkInfo.Kind != FileKindSymlink {
			t.Fatalf("unexpected dir-link info: %+v", dirLinkInfo)
		}
		realTarget, err := filepath.EvalSymlinks(filepath.Join(root, "dir/file.txt"))
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if got := tenMust(t, e.CanonicalPath("file-link", bg)); got != realTarget {
			t.Fatalf("CanonicalPath got %q, want %q", got, realTarget)
		}
	})

	t.Run("lists symlinks as symlinks", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("target.txt", []byte("hello"), bg))
		if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "link.txt")); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		entries := tenMust(t, e.ListDir(".", bg))
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		if len(entries) != 2 || entries[0].Name != "link.txt" || entries[0].Kind != FileKindSymlink || entries[1].Name != "target.txt" || entries[1].Kind != FileKindFile {
			t.Fatalf("unexpected entries: %+v", entries)
		}
	})

	t.Run("stops reading text lines at the requested limit", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("file.txt", []byte("one\ntwo\nthree"), bg))
		one := 1
		got := tenMust(t, e.ReadTextLines("file.txt", &ReadTextLinesOptions{MaxLines: &one}, bg))
		if !reflect.DeepEqual(got, []string{"one"}) {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("returns FileError for missing paths and keeps exists false for missing paths", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		info := e.FileInfo("missing.txt", bg)
		if info.Ok || info.Error == nil || info.Error.Name() != "FileError" || info.Error.Code != FileErrorNotFound || info.Error.Path != filepath.Join(root, "missing.txt") {
			t.Fatalf("unexpected info result: %+v", info)
		}
		if tenMust(t, e.Exists("missing.txt", bg)) {
			t.Fatalf("expected Exists=false")
		}
	})

	t.Run("returns FileError for listing non-directories", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("file.txt", []byte("hello"), bg))
		res := e.ListDir("file.txt", bg)
		if res.Ok || res.Error == nil || res.Error.Code != FileErrorNotDirectory {
			t.Fatalf("expected not_directory error, got %+v", res)
		}
	})

	t.Run("appends to new files and creates parent directories", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.AppendFile("new/nested/file.txt", []byte("a"), bg))
		tenMust(t, e.AppendFile("new/nested/file.txt", []byte("b"), bg))
		if got := tenMust(t, e.ReadTextFile("new/nested/file.txt", bg)); got != "ab" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("atomically renames a file and replaces the destination", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("source.txt", []byte("new"), bg))
		tenMust(t, e.WriteFile("destination.txt", []byte("old"), bg))
		tenMust(t, e.RenameFile("source.txt", "destination.txt", bg))
		if tenMust(t, e.Exists("source.txt", bg)) {
			t.Fatalf("source.txt should not exist")
		}
		if got := tenMust(t, e.ReadTextFile("destination.txt", bg)); got != "new" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("reports the source path when rename fails because the source is missing", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("destination.txt", []byte("unchanged"), bg))
		res := e.RenameFile("missing-source.txt", "destination.txt", bg)
		if res.Ok || res.Error == nil || res.Error.Code != FileErrorNotFound || res.Error.Path != filepath.Join(root, "missing-source.txt") {
			t.Fatalf("unexpected error: %+v", res)
		}
		if got := tenMust(t, e.ReadTextFile("destination.txt", bg)); got != "unchanged" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("creates temporary directories and files", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tempDir := tenMust(t, e.CreateTempDir("node-env-test-", bg))
		defer os.RemoveAll(tempDir)
		if _, err := os.Stat(tempDir); err != nil {
			t.Fatalf("stat tempDir: %v", err)
		}
		tempFile := tenMust(t, e.CreateTempFile(&CreateTempFileOptions{Prefix: "prefix-", Suffix: ".txt"}, bg))
		defer os.RemoveAll(filepath.Dir(tempFile))
		if !strings.HasSuffix(tempFile, ".txt") {
			t.Fatalf("expected .txt suffix: %q", tempFile)
		}
		if got := tenMust(t, e.ReadTextFile(tempFile, bg)); got != "" {
			t.Fatalf("expected empty file, got %q", got)
		}
	})

	t.Run("honors createDir recursive false and remove recursive/force options", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		noRec := false
		createRes := e.CreateDir("missing/child", &CreateDirOptions{Recursive: &noRec}, bg)
		if createRes.Ok || createRes.Error == nil || createRes.Error.Code != FileErrorNotFound {
			t.Fatalf("expected not_found error, got %+v", createRes)
		}

		tenMust(t, e.WriteFile("dir/child/file.txt", []byte("hello"), bg))
		remDir := e.Remove("dir", &RemoveOptions{Recursive: false}, bg)
		if remDir.Ok {
			t.Fatalf("expected error removing directory non-recursively")
		}
		tenMust(t, e.Remove("dir", &RemoveOptions{Recursive: true}, bg))
		if tenMust(t, e.Exists("dir", bg)) {
			t.Fatalf("dir should have been removed")
		}
		remMissing := e.Remove("missing", &RemoveOptions{Force: false}, bg)
		if remMissing.Ok {
			t.Fatalf("expected error removing missing path without force")
		}
		tenMust(t, e.Remove("missing", &RemoveOptions{Force: true}, bg))
	})

	t.Run("returns aborted results without side effects for pre-aborted file operations", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("file.txt", []byte("hello"), bg))
		aborted := tenAbortedContext()

		checkAborted := func(name string, ok bool, fe *FileError) {
			t.Helper()
			if ok || fe == nil || fe.Code != FileErrorAborted {
				t.Fatalf("%s: expected aborted FileError, got ok=%v err=%+v", name, ok, fe)
			}
		}

		r1 := e.ReadTextFile("file.txt", aborted)
		checkAborted("ReadTextFile", r1.Ok, r1.Error)
		r2 := e.ReadTextLines("file.txt", nil, aborted)
		checkAborted("ReadTextLines", r2.Ok, r2.Error)
		r3 := e.ReadBinaryFile("file.txt", aborted)
		checkAborted("ReadBinaryFile", r3.Ok, r3.Error)
		r4 := e.OpenTextLineReader("file.txt", aborted)
		checkAborted("OpenTextLineReader", r4.Ok, r4.Error)
		r5 := e.WriteFile("other.txt", []byte("hello"), aborted)
		checkAborted("WriteFile", r5.Ok, r5.Error)
		r6 := e.AppendFile("file.txt", []byte(" world"), aborted)
		checkAborted("AppendFile", r6.Ok, r6.Error)
		r7 := e.TruncateFile("file.txt", 1, aborted)
		checkAborted("TruncateFile", r7.Ok, r7.Error)
		r8 := e.FlushFile("file.txt", aborted)
		checkAborted("FlushFile", r8.Ok, r8.Error)
		r9 := e.RenameFile("file.txt", "renamed.txt", aborted)
		checkAborted("RenameFile", r9.Ok, r9.Error)
		r10 := e.FileInfo("file.txt", aborted)
		checkAborted("FileInfo", r10.Ok, r10.Error)
		r11 := e.ListDir(".", aborted)
		checkAborted("ListDir", r11.Ok, r11.Error)
		r12 := e.CanonicalPath("file.txt", aborted)
		checkAborted("CanonicalPath", r12.Ok, r12.Error)
		r13 := e.Exists("file.txt", aborted)
		checkAborted("Exists", r13.Ok, r13.Error)
		r14 := e.CreateDir("dir", nil, aborted)
		checkAborted("CreateDir", r14.Ok, r14.Error)
		r15 := e.Remove("file.txt", nil, aborted)
		checkAborted("Remove", r15.Ok, r15.Error)
		r16 := e.CreateTempDir("", aborted)
		checkAborted("CreateTempDir", r16.Ok, r16.Error)
		r17 := e.CreateTempFile(nil, aborted)
		checkAborted("CreateTempFile", r17.Ok, r17.Error)

		if got := tenMust(t, e.ReadTextFile("file.txt", bg)); got != "hello" {
			t.Fatalf("file.txt mutated: %q", got)
		}
		entries := tenMust(t, e.ListDir(".", bg))
		if len(entries) != 1 || entries[0].Name != "file.txt" {
			t.Fatalf("unexpected directory contents: %+v", entries)
		}
	})

	t.Run("truncates and extends files to exact byte sizes", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		raw := []byte{0x61, 0xc3, 0xa9, 0x0a, 0x62, 0x0a}
		tenMust(t, e.WriteFile("file.bin", raw, bg))

		tenMust(t, e.TruncateFile("file.bin", 2, bg))
		if got := tenMust(t, e.ReadBinaryFile("file.bin", bg)); !reflect.DeepEqual(got, []byte{0x61, 0xc3}) {
			t.Fatalf("got %v", got)
		}
		tenMust(t, e.TruncateFile("file.bin", 4, bg))
		if got := tenMust(t, e.ReadBinaryFile("file.bin", bg)); !reflect.DeepEqual(got, []byte{0x61, 0xc3, 0, 0}) {
			t.Fatalf("got %v", got)
		}
		tenMust(t, e.TruncateFile("file.bin", 0, bg))
		if info := tenMust(t, e.FileInfo("file.bin", bg)); info.Size != 0 {
			t.Fatalf("expected size 0, got %d", info.Size)
		}
	})

	t.Run("rejects invalid truncation sizes and never creates missing files", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("file.txt", []byte("hello"), bg))
		for _, size := range []int64{-1, nodeSafeMaxInteger + 1} {
			res := e.TruncateFile("file.txt", size, bg)
			if res.Ok || res.Error == nil || res.Error.Code != FileErrorInvalid || res.Error.Path != filepath.Join(root, "file.txt") {
				t.Fatalf("size %d: expected invalid error, got %+v", size, res)
			}
		}
		if got := tenMust(t, e.ReadTextFile("file.txt", bg)); got != "hello" {
			t.Fatalf("got %q", got)
		}

		res := e.TruncateFile("missing.txt", 0, bg)
		if res.Ok || res.Error == nil || res.Error.Code != FileErrorNotFound || res.Error.Path != filepath.Join(root, "missing.txt") {
			t.Fatalf("unexpected error: %+v", res)
		}
		if tenMust(t, e.Exists("missing.txt", bg)) {
			t.Fatalf("missing.txt should not exist")
		}
	})

	t.Run("flushes existing files without changing content and reports missing paths", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("file.txt", []byte("durable"), bg))
		tenMust(t, e.FlushFile("file.txt", bg))
		if got := tenMust(t, e.ReadTextFile("file.txt", bg)); got != "durable" {
			t.Fatalf("got %q", got)
		}

		missing := e.FlushFile("missing.txt", bg)
		if missing.Ok || missing.Error == nil || missing.Error.Code != FileErrorNotFound || missing.Error.Path != filepath.Join(root, "missing.txt") {
			t.Fatalf("unexpected missing error: %+v", missing)
		}
		if tenMust(t, e.Exists("missing.txt", bg)) {
			t.Fatalf("missing.txt should not exist")
		}

		tenMust(t, e.CreateDir("dir", nil, bg))
		dirRes := e.FlushFile("dir", bg)
		if dirRes.Ok || dirRes.Error == nil || dirRes.Error.Code != FileErrorIsDirectory {
			t.Fatalf("expected is_directory, got %+v", dirRes)
		}
	})

	t.Run("syncs through an opened handle, returns sync failures, and always closes the handle", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("file.txt", []byte("durable"), bg))

		var synced []*os.File
		var failNext bool
		e.hooks.syncFileFn = func(f *os.File) error {
			synced = append(synced, f)
			if failNext {
				return errors.New("EIO: i/o error, fsync")
			}
			return f.Sync()
		}

		tenMust(t, e.FlushFile("file.txt", bg))
		if len(synced) != 1 {
			t.Fatalf("expected 1 sync call, got %d", len(synced))
		}
		if err := synced[0].Close(); err == nil || !errors.Is(err, os.ErrClosed) {
			t.Fatalf("expected opened handle to already be closed, got err=%v", err)
		}

		failNext = true
		res := e.FlushFile("file.txt", bg)
		if res.Ok || res.Error == nil || res.Error.Code != FileErrorUnknown || res.Error.Path != filepath.Join(root, "file.txt") || res.Error.Message != "EIO: i/o error, fsync" {
			t.Fatalf("unexpected sync failure result: %+v", res)
		}
		if len(synced) != 2 {
			t.Fatalf("expected 2 sync calls, got %d", len(synced))
		}
		if err := synced[1].Close(); err == nil || !errors.Is(err, os.ErrClosed) {
			t.Fatalf("expected second opened handle to already be closed, got err=%v", err)
		}
	})

	t.Run("cleanup is best-effort", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		e.Cleanup(chord.BackgroundContext)
	})
}

func TestNodeExecutionEnvTextLineReader(t *testing.T) {
	t.Run("reports whether each line was newline-terminated and preserves carriage returns", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("lines.txt", []byte("one\r\n\ntwo\npartial"), bg))
		reader := tenMust(t, e.OpenTextLineReader("lines.txt", bg))
		defer reader.Close(bg)

		var lines []TextLine
		for {
			line := tenMust(t, reader.ReadLine(bg))
			if line == nil {
				break
			}
			lines = append(lines, *line)
		}
		want := []TextLine{
			{Text: "one\r", Terminated: true},
			{Text: "", Terminated: true},
			{Text: "two", Terminated: true},
			{Text: "partial", Terminated: false},
		}
		if !reflect.DeepEqual(lines, want) {
			t.Fatalf("got %+v, want %+v", lines, want)
		}
		if line := tenMust(t, reader.ReadLine(bg)); line != nil {
			t.Fatalf("expected nil at EOF, got %+v", line)
		}
	})

	t.Run("returns no lines for an empty file and one terminated empty line for a lone newline", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("empty.txt", []byte(""), bg))
		tenMust(t, e.WriteFile("newline.txt", []byte("\n"), bg))

		empty := tenMust(t, e.OpenTextLineReader("empty.txt", bg))
		if line := tenMust(t, empty.ReadLine(bg)); line != nil {
			t.Fatalf("expected nil for empty file, got %+v", line)
		}
		empty.Close(bg)

		nl := tenMust(t, e.OpenTextLineReader("newline.txt", bg))
		line := tenMust(t, nl.ReadLine(bg))
		if line == nil || line.Text != "" || !line.Terminated {
			t.Fatalf("unexpected lone newline result: %+v", line)
		}
		if eof := tenMust(t, nl.ReadLine(bg)); eof != nil {
			t.Fatalf("expected nil after lone newline, got %+v", eof)
		}
		nl.Close(bg)
	})

	t.Run("decodes multi-byte characters split across read chunks and lines longer than one chunk", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		first := strings.Repeat("a", 64*1024-2) + "😀tail"
		second := strings.Repeat("é", 100_000)
		tenMust(t, e.WriteFile("large.txt", []byte(first+"\n"+second), bg))
		reader := tenMust(t, e.OpenTextLineReader("large.txt", bg))
		defer reader.Close(bg)

		l1 := tenMust(t, reader.ReadLine(bg))
		if l1 == nil || l1.Text != first || !l1.Terminated {
			t.Fatalf("first line mismatch")
		}
		l2 := tenMust(t, reader.ReadLine(bg))
		if l2 == nil || l2.Text != second || l2.Terminated {
			t.Fatalf("second line mismatch")
		}
		if l3 := tenMust(t, reader.ReadLine(bg)); l3 != nil {
			t.Fatalf("expected nil at EOF")
		}
	})

	t.Run("rejects a read cancelled while pending without consuming its bytes", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("lines.txt", []byte("one\ntwo\n"), bg))

		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		e.hooks.readAtFn = func(f *os.File, b []byte, off int64) (int, error) {
			firstCall := false
			once.Do(func() { firstCall = true })
			if firstCall {
				close(started)
				<-release
			}
			return f.ReadAt(b, off)
		}

		reader := tenMust(t, e.OpenTextLineReader("lines.txt", bg))
		defer reader.Close(bg)

		cancelCtx, cancel := context.WithCancel(context.Background())
		resCh := make(chan Result[*TextLine, *FileError], 1)
		go func() {
			resCh <- reader.ReadLine(chord.WithAbortSignal(cancelCtx, bg))
		}()

		<-started
		cancel()
		close(release)

		aborted := <-resCh
		if aborted.Ok || aborted.Error == nil || aborted.Error.Code != FileErrorAborted {
			t.Fatalf("expected aborted, got %+v", aborted)
		}
		if l1 := tenMust(t, reader.ReadLine(bg)); l1 == nil || l1.Text != "one" || !l1.Terminated {
			t.Fatalf("unexpected first line after retry: %+v", l1)
		}
		if l2 := tenMust(t, reader.ReadLine(bg)); l2 == nil || l2.Text != "two" || !l2.Terminated {
			t.Fatalf("unexpected second line after retry: %+v", l2)
		}
		if l3 := tenMust(t, reader.ReadLine(bg)); l3 != nil {
			t.Fatalf("expected nil at EOF, got %+v", l3)
		}
	})

	t.Run("rejects reads after close and closes idempotently", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		bg := chord.BackgroundContext
		tenMust(t, e.WriteFile("lines.txt", []byte("one\n"), bg))
		reader := tenMust(t, e.OpenTextLineReader("lines.txt", bg))
		reader.Close(bg)
		reader.Close(bg)
		res := reader.ReadLine(bg)
		if res.Ok || res.Error == nil || res.Error.Code != FileErrorInvalid || res.Error.Path != filepath.Join(root, "lines.txt") {
			t.Fatalf("expected invalid after close, got %+v", res)
		}
	})

	t.Run("reports missing files when opening a reader", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		res := e.OpenTextLineReader("missing.txt", chord.BackgroundContext)
		if res.Ok || res.Error == nil || res.Error.Code != FileErrorNotFound || res.Error.Path != filepath.Join(root, "missing.txt") {
			t.Fatalf("unexpected error: %+v", res)
		}
	})
}

func TestNodeExecutionEnvShell(t *testing.T) {
	t.Run("executes commands in cwd with env overrides", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		res, out := tenCollectShellOutput(
			e,
			`printf '%s:%s' "$PWD" "$NODE_ENV_TEST"`,
			&ShellExecOptions{Env: map[string]string{"NODE_ENV_TEST": "ok"}},
			chord.BackgroundContext,
		)
		got := tenMustExec(t, res)
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		if out != realRoot+":ok" || got.ExitCode != 0 {
			t.Fatalf("got out=%q res=%+v (realRoot=%q)", out, got, realRoot)
		}
	})

	t.Run("applies string shell environment overrides", func(t *testing.T) {
		cases := []struct {
			name      string
			overrides map[string]string
			expected  string
		}{
			{"missing override preserves base value", nil, "x:/stale/parent.jsonl"},
			{"empty override shadows base value", map[string]string{"PI_SESSION_FILE": ""}, "x:"},
			{"string override replaces base value", map[string]string{"PI_SESSION_FILE": "/sessions/current.jsonl"}, "x:/sessions/current.jsonl"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				root := t.TempDir()
				e := NewNodeExecutionEnv(NodeExecutionEnvOptions{
					Cwd: root,
					ShellEnv: map[string]string{
						"PI_SESSION_FILE":            "/stale/parent.jsonl",
						"PI_CODING_AGENT":            "true",
						"PI_NODE_ENV_PRESERVED_TEST": "preserved",
					},
				})
				res, out := tenCollectShellOutput(
					e,
					`printf '%s:%s|%s|%s' "${PI_SESSION_FILE+x}" "${PI_SESSION_FILE-}" "$PI_CODING_AGENT" "$PI_NODE_ENV_PRESERVED_TEST"`,
					&ShellExecOptions{Env: tc.overrides},
					chord.BackgroundContext,
				)
				tenMustExec(t, res)
				want := tc.expected + "|true|preserved"
				if out != want {
					t.Fatalf("got %q, want %q", out, want)
				}
			})
		}
	})

	t.Run("can replace rather than inherit the default shell environment", func(t *testing.T) {
		root := t.TempDir()
		const inheritedKey = "PI_NODE_ENV_INHERITED_TEST"
		const configuredKey = "PI_NODE_ENV_CONFIGURED_TEST"
		const explicitKey = "PI_NODE_ENV_EXPLICIT_TEST"
		t.Setenv(inheritedKey, "host")
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{
			Cwd:      root,
			ShellEnv: map[string]string{configuredKey: "configured"},
		})
		inherit := false
		res, out := tenCollectShellOutput(
			e,
			fmt.Sprintf(`printf '%%s:%%s:%%s' "${%s-}" "${%s-}" "${%s-}"`, inheritedKey, configuredKey, explicitKey),
			&ShellExecOptions{InheritEnv: &inherit, Env: map[string]string{explicitKey: "explicit"}},
			chord.BackgroundContext,
		)
		tenMustExec(t, res)
		if out != "::explicit" {
			t.Fatalf("got %q, want '::explicit'", out)
		}
	})

	t.Run("uses stdin command transport for legacy WSL bash paths", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix-only fake WSL shell test")
		}
		root := t.TempDir()
		shellPath := `C:\Windows\System32\bash.exe`
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		tenMust(t, e.WriteFile(shellPath, []byte("#!/bin/sh\nprintf 'args:%s\\n' \"$*\" >&2\nexec /bin/bash \"$@\"\n"), chord.BackgroundContext))
		if err := os.Chmod(filepath.Join(root, shellPath), 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}

		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		if err := os.Chdir(root); err != nil {
			t.Fatalf("Chdir: %v", err)
		}
		defer func() { _ = os.Chdir(origWd) }()
		t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

		wslEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath})
		wslEnv.hooks.platformFn = func() string { return "win32" }

		res, out := tenCollectShellOutput(
			wslEnv,
			`name='World'; echo "Hello, ${name}!"`,
			nil,
			chord.BackgroundContext,
		)
		got := tenMustExec(t, res)
		if !strings.Contains(out, "Hello, World!") || !strings.Contains(out, "args:-s") || got.ExitCode != 0 {
			t.Fatalf("unexpected WSL output=%q res=%+v", out, got)
		}
	})

	t.Run("settles after the shell exits when a detached descendant retains inherited stdio", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix subshell test")
		}
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		done := make(chan Result[ShellExecResult, *ExecutionError], 1)
		var out string
		go func() {
			r, o := tenCollectShellOutput(e, "(sleep 30 &) && echo child-exiting", nil, chord.BackgroundContext)
			out = o
			done <- r
		}()
		select {
		case r := <-done:
			tenMustExec(t, r)
			if !strings.Contains(out, "child-exiting") {
				t.Fatalf("expected child-exiting in output, got %q", out)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for settlement with detached descendant holding stdio")
		}
	})

	t.Run("cleanup terminates active shell processes", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		resCh := make(chan Result[ShellExecResult, *ExecutionError], 1)
		go func() {
			resCh <- e.Exec("touch started; sleep 60", nil, chord.BackgroundContext)
		}()
		tenWaitFor(t, 10*time.Second, func() bool {
			r := e.Exists("started", chord.BackgroundContext)
			return r.Ok && r.Value
		})
		e.Cleanup(chord.BackgroundContext)
		select {
		case r := <-resCh:
			if !r.Ok {
				t.Fatalf("expected Ok after cleanup kill, got %+v", r.Error)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for killed command to settle")
		}
	})

	t.Run("streams combined stdout and stderr", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		res, out := tenCollectShellOutput(e, "printf out; printf err >&2", nil, chord.BackgroundContext)
		got := tenMustExec(t, res)
		if got.ExitCode != 0 || got.SpillPath != "" || !strings.Contains(out, "out") || !strings.Contains(out, "err") {
			t.Fatalf("unexpected got=%+v out=%q", got, out)
		}
	})

	t.Run("decodes UTF-8 split across raw process chunks", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		res, out := tenCollectShellOutput(e, tenHelperCommand(t, "split-emoji"), nil, chord.BackgroundContext)
		tenMustExec(t, res)
		if out != "😀" {
			t.Fatalf("got %q, want 😀", out)
		}
	})

	t.Run("reports a missing working directory before spawning", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: filepath.Join(root, "missing")})
		res := e.Exec("printf ok", nil, chord.BackgroundContext)
		if res.Ok || res.Error == nil || res.Error.Code != ExecutionErrorSpawnError || !strings.Contains(res.Error.Message, "Working directory does not exist") {
			t.Fatalf("unexpected result: %+v", res)
		}
	})

	t.Run("returns non-zero command exit codes as successful execution results", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		got := tenMustExec(t, e.Exec("exit 7", nil, chord.BackgroundContext))
		if got.ExitCode != 7 {
			t.Fatalf("got exitCode=%d, want 7", got.ExitCode)
		}
	})

	t.Run("maps signal-killed processes to a non-zero exit code", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix signal test")
		}
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		got := tenMustExec(t, e.Exec("kill -9 $$", nil, chord.BackgroundContext))
		if got.ExitCode != 128+int(syscall.SIGKILL) {
			t.Fatalf("got exitCode=%d, want %d", got.ExitCode, 128+int(syscall.SIGKILL))
		}
	})

	t.Run("returns timeout errors for commands exceeding the timeout", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		to := 0.01
		res := e.Exec("sleep 5", &ShellExecOptions{Timeout: &to}, chord.BackgroundContext)
		if res.Ok || res.Error == nil || res.Error.Code != ExecutionErrorTimeout {
			t.Fatalf("expected timeout error, got %+v", res)
		}
	})

	t.Run("rejects invalid timeouts before spawning", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		for _, to := range []float64{0, -1, math.NaN(), math.Inf(1), 2_147_484} {
			v := to
			res := e.Exec("touch spawned", &ShellExecOptions{Timeout: &v}, chord.BackgroundContext)
			if res.Ok || res.Error == nil || res.Error.Code != ExecutionErrorTimeout || !strings.Contains(res.Error.Message, "Invalid timeout") {
				t.Fatalf("timeout %v: got %+v", to, res)
			}
		}
		if tenMust(t, e.Exists("spawned", chord.BackgroundContext)) {
			t.Fatalf("spawned should not have been created")
		}
	})

	t.Run("returns callback errors from exec stream handlers", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		res := e.Exec("printf out", &ShellExecOptions{
			OnOutput: func(string, chord.Context) error {
				return errors.New("callback failed")
			},
		}, chord.BackgroundContext)
		if res.Ok || res.Error == nil || res.Error.Code != ExecutionErrorCallbackError || res.Error.Message != "callback failed" {
			t.Fatalf("expected callback_error, got %+v", res)
		}
	})

	t.Run("returns shell unavailable and spawn errors", func(t *testing.T) {
		root := t.TempDir()
		missingEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: filepath.Join(root, "missing-shell")})
		missingRes := missingEnv.Exec("printf ok", nil, chord.BackgroundContext)
		if missingRes.Ok || missingRes.Error == nil || missingRes.Error.Code != ExecutionErrorShellUnavailable {
			t.Fatalf("expected shell_unavailable, got %+v", missingRes)
		}

		shellPath := filepath.Join(root, "not-executable-shell")
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		tenMust(t, e.WriteFile(shellPath, []byte("not executable"), chord.BackgroundContext))
		spawnEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath})
		spawnRes := spawnEnv.Exec("printf ok", nil, chord.BackgroundContext)
		if spawnRes.Ok || spawnRes.Error == nil || spawnRes.Error.Code != ExecutionErrorSpawnError {
			t.Fatalf("expected spawn_error, got %+v", spawnRes)
		}
	})

	t.Run("returns an aborted result for pre-aborted and aborted commands", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		pre := e.Exec("touch spawned", nil, tenAbortedContext())
		if pre.Ok || pre.Error == nil || pre.Error.Code != ExecutionErrorAborted {
			t.Fatalf("expected pre-aborted error, got %+v", pre)
		}
		if tenMust(t, e.Exists("spawned", chord.BackgroundContext)) {
			t.Fatalf("spawned should not exist")
		}

		cancelCtx, cancel := context.WithCancel(context.Background())
		resCh := make(chan Result[ShellExecResult, *ExecutionError], 1)
		go func() {
			resCh <- e.Exec("sleep 5", nil, chord.WithAbortSignal(cancelCtx, chord.BackgroundContext))
		}()
		cancel()
		aborted := <-resCh
		if aborted.Ok || aborted.Error == nil || aborted.Error.Code != ExecutionErrorAborted {
			t.Fatalf("expected aborted, got %+v", aborted)
		}
	})

	t.Run("does not create a spill before output crosses its thresholds", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		got := tenMustExec(t, e.Exec("printf short", &ShellExecOptions{
			Spill: &ShellSpillOptions{AfterBytes: 100, AfterLines: 10},
		}, chord.BackgroundContext))
		if got.SpillPath != "" {
			defer os.RemoveAll(filepath.Dir(got.SpillPath))
			t.Fatalf("expected empty SpillPath, got %q", got.SpillPath)
		}
	})

	t.Run("preserves exact raw bytes in the spill while streaming decoded text", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		got := tenMustExec(t, e.Exec(tenHelperCommand(t, "raw-bytes"), &ShellExecOptions{
			Spill: &ShellSpillOptions{AfterBytes: 1, AfterLines: 10},
		}, chord.BackgroundContext))
		if got.SpillPath == "" {
			t.Fatalf("expected SpillPath")
		}
		defer os.RemoveAll(filepath.Dir(got.SpillPath))
		raw := tenMust(t, e.ReadBinaryFile(got.SpillPath, chord.BackgroundContext))
		if !reflect.DeepEqual(raw, []byte{0x66, 0x80, 0x00, 0x6f}) {
			t.Fatalf("got raw=%v", raw)
		}
	})

	t.Run("reports the spill of a command that times out", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		to := 0.3
		res := e.Exec("printf 12345678901234567890; sleep 5", &ShellExecOptions{
			Timeout: &to,
			Spill:   &ShellSpillOptions{AfterBytes: 10, AfterLines: 10},
		}, chord.BackgroundContext)
		if res.Ok || res.Error == nil || res.Error.Code != ExecutionErrorTimeout || res.Error.SpillPath == "" {
			t.Fatalf("expected timeout with SpillPath, got %+v", res)
		}
		defer os.RemoveAll(filepath.Dir(res.Error.SpillPath))
		if got := tenMust(t, e.ReadTextFile(res.Error.SpillPath, chord.BackgroundContext)); got != "12345678901234567890" {
			t.Fatalf("got spilled %q", got)
		}
	})

	t.Run("fails rather than silently losing a requested spill", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		e.hooks.createTempFileFn = func(opts *CreateTempFileOptions, ctx chord.Context) Result[string, *FileError] {
			if opts != nil && opts.Prefix == "pi-output-" {
				return Ok[string, *FileError](filepath.Join(root, "missing", "spill.log"))
			}
			return e.CreateTempFile(opts, ctx)
		}
		res := e.Exec("printf 12345678901234567890", &ShellExecOptions{
			Spill: &ShellSpillOptions{AfterBytes: 10, AfterLines: 10},
		}, chord.BackgroundContext)
		if res.Ok || res.Error == nil || res.Error.Code != ExecutionErrorUnknown || !strings.Contains(res.Error.Message, "Failed to preserve complete shell output") {
			t.Fatalf("unexpected error: %+v", res)
		}
	})

	t.Run("preserves complete large output in the spill", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		got := tenMustExec(t, e.Exec(tenHelperCommand(t, "large-x"), &ShellExecOptions{
			Spill: &ShellSpillOptions{AfterBytes: 10, AfterLines: 10},
		}, chord.BackgroundContext))
		if got.SpillPath == "" {
			t.Fatalf("expected SpillPath")
		}
		defer os.RemoveAll(filepath.Dir(got.SpillPath))
		content := tenMust(t, e.ReadTextFile(got.SpillPath, chord.BackgroundContext))
		if len(content) != 500_000 {
			t.Fatalf("got len=%d, want 500000", len(content))
		}
	})

	t.Run("streams every line and spills them all once output crosses its line threshold", func(t *testing.T) {
		root := t.TempDir()
		e := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
		res, out := tenCollectShellOutput(
			e,
			"i=1; while [ $i -le 15000 ]; do echo line-$i; i=$((i+1)); done",
			&ShellExecOptions{Spill: &ShellSpillOptions{AfterBytes: 1024 * 1024, AfterLines: 100}},
			chord.BackgroundContext,
		)
		got := tenMustExec(t, res)
		if got.SpillPath == "" {
			t.Fatalf("expected SpillPath")
		}
		defer os.RemoveAll(filepath.Dir(got.SpillPath))
		var want strings.Builder
		for i := 1; i <= 15000; i++ {
			fmt.Fprintf(&want, "line-%d\n", i)
		}
		if out != want.String() {
			t.Fatalf("streamed output mismatch (len %d vs %d)", len(out), want.Len())
		}
		spilled := tenMust(t, e.ReadTextLines(got.SpillPath, nil, chord.BackgroundContext))
		if len(spilled) != 15000 || spilled[len(spilled)-1] != "line-15000" {
			t.Fatalf("unexpected spilled lines len=%d", len(spilled))
		}
	})
}
