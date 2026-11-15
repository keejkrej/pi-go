// Ported from packages/durable/src/env/index.ts (pi v1.0.0).

package env

import (
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Result is the outcome of a fallible operation. Expected failures are returned instead of panicked.
type Result[TValue, TError any] struct {
	Ok    bool
	Value TValue
	Error TError
}

// Ok constructs a successful Result carrying value.
func Ok[TValue, TError any](value TValue) Result[TValue, TError] {
	return Result[TValue, TError]{
		Ok:    true,
		Value: value,
	}
}

// Err constructs a failed Result carrying errValue.
func Err[TValue, TError any](errValue TError) Result[TValue, TError] {
	return Result[TValue, TError]{
		Ok:    false,
		Error: errValue,
	}
}

// GetOrThrow unwraps a Result into Go's (value, error) tuple.
func GetOrThrow[TValue, TError any](result Result[TValue, TError]) (TValue, error) {
	if !result.Ok {
		var zero TValue
		if err, ok := any(result.Error).(error); ok && err != nil {
			return zero, err
		}
		return zero, ToError(result.Error)
	}
	return result.Value, nil
}

// GetOrUndefined returns the Result value and true if Ok, or the zero value and false otherwise.
func GetOrUndefined[TValue, TError any](result Result[TValue, TError]) (TValue, bool) {
	if !result.Ok {
		var zero TValue
		return zero, false
	}
	return result.Value, true
}

// ToError converts an arbitrary failure value into an error.
func ToError(value any) error {
	if err, ok := value.(error); ok && err != nil {
		return err
	}
	if s, ok := value.(string); ok {
		return errors.New(s)
	}
	if s, err := jsonx.Stringify(value); err == nil {
		return errors.New(s)
	}
	return errors.New(fmt.Sprint(value))
}

// FileKind identifies the type of a filesystem entry.
type FileKind string

const (
	FileKindFile      FileKind = "file"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
)

// FileErrorCode classifies expected filesystem operation failures.
type FileErrorCode string

const (
	FileErrorAborted          FileErrorCode = "aborted"
	FileErrorNotFound         FileErrorCode = "not_found"
	FileErrorPermissionDenied FileErrorCode = "permission_denied"
	FileErrorNotDirectory     FileErrorCode = "not_directory"
	FileErrorIsDirectory      FileErrorCode = "is_directory"
	FileErrorInvalid          FileErrorCode = "invalid"
	FileErrorNotSupported     FileErrorCode = "not_supported"
	FileErrorUnknown          FileErrorCode = "unknown"
)

// FileError is a structured filesystem failure returned inside Result.
type FileError struct {
	Code    FileErrorCode `json:"code"`
	Message string        `json:"message"`
	Path    string        `json:"path,omitzero"`
	Cause   error         `json:"-"`
}

// NewFileError constructs a FileError.
func NewFileError(code FileErrorCode, message string, path string, cause error) *FileError {
	return &FileError{
		Code:    code,
		Message: message,
		Path:    path,
		Cause:   cause,
	}
}

func (e *FileError) Name() string  { return "FileError" }
func (e *FileError) Error() string { return e.Message }
func (e *FileError) Unwrap() error { return e.Cause }

// ExecutionErrorCode classifies shell execution failures.
type ExecutionErrorCode string

const (
	ExecutionErrorAborted          ExecutionErrorCode = "aborted"
	ExecutionErrorTimeout          ExecutionErrorCode = "timeout"
	ExecutionErrorShellUnavailable ExecutionErrorCode = "shell_unavailable"
	ExecutionErrorSpawnError       ExecutionErrorCode = "spawn_error"
	ExecutionErrorCallbackError    ExecutionErrorCode = "callback_error"
	ExecutionErrorUnknown          ExecutionErrorCode = "unknown"
)

// ExecutionError is a structured shell execution failure returned inside Result.
type ExecutionError struct {
	Code    ExecutionErrorCode `json:"code"`
	Message string             `json:"message"`
	// SpillPath holds the temporary spill file if a command timed out or aborted after crossing spill thresholds.
	SpillPath string `json:"spillPath,omitzero"`
	Cause     error  `json:"-"`
}

// NewExecutionError constructs an ExecutionError.
func NewExecutionError(code ExecutionErrorCode, message string, cause error) *ExecutionError {
	return &ExecutionError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

func (e *ExecutionError) Name() string  { return "ExecutionError" }
func (e *ExecutionError) Error() string { return e.Message }
func (e *ExecutionError) Unwrap() error { return e.Cause }

// FileInfo describes a file, directory, or symbolic link.
type FileInfo struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Kind    FileKind `json:"kind"`
	Size    int64    `json:"size"`
	MtimeMs float64  `json:"mtimeMs"`
}

// TextLine represents one LF-delimited line returned by TextLineReader.
type TextLine struct {
	Text       string `json:"text"`
	Terminated bool   `json:"terminated"`
}

// TextLineReader incrementally reads LF-terminated lines from a file.
// ReadLine returns Ok(nil) when EOF is reached.
type TextLineReader interface {
	ReadLine(ctx chord.Context) Result[*TextLine, *FileError]
	Close(ctx chord.Context)
}

// ReadTextLinesOptions configures FileSystem.ReadTextLines.
type ReadTextLinesOptions struct {
	MaxLines *int `json:"maxLines,omitzero"`
}

// CreateDirOptions configures FileSystem.CreateDir.
type CreateDirOptions struct {
	Recursive *bool `json:"recursive,omitzero"`
}

// RemoveOptions configures FileSystem.Remove.
type RemoveOptions struct {
	Recursive bool `json:"recursive,omitzero"`
	Force     bool `json:"force,omitzero"`
}

// CreateTempFileOptions configures FileSystem.CreateTempFile.
type CreateTempFileOptions struct {
	Prefix string `json:"prefix,omitzero"`
	Suffix string `json:"suffix,omitzero"`
}

// FileSystem provides portable filesystem capabilities. Operations return failures rather than panicking.
type FileSystem interface {
	// Id identifies the file namespace: equal ids see the same files at the same paths, whatever their cwd.
	Id() string
	Cwd() string
	SetCwd(cwd string)
	AbsolutePath(path string, ctx chord.Context) Result[string, *FileError]
	JoinPath(parts []string, ctx chord.Context) Result[string, *FileError]
	ReadTextFile(path string, ctx chord.Context) Result[string, *FileError]
	OpenTextLineReader(path string, ctx chord.Context) Result[TextLineReader, *FileError]
	ReadTextLines(path string, options *ReadTextLinesOptions, ctx chord.Context) Result[[]string, *FileError]
	ReadBinaryFile(path string, ctx chord.Context) Result[[]byte, *FileError]
	WriteFile(path string, content []byte, ctx chord.Context) Result[struct{}, *FileError]
	AppendFile(path string, content []byte, ctx chord.Context) Result[struct{}, *FileError]
	// TruncateFile truncates or extends a file to exactly size bytes.
	TruncateFile(path string, size int64, ctx chord.Context) Result[struct{}, *FileError]
	// FlushFile flushes file contents and metadata needed to retrieve them from an open file handle.
	FlushFile(path string, ctx chord.Context) Result[struct{}, *FileError]
	RenameFile(sourcePath string, destinationPath string, ctx chord.Context) Result[struct{}, *FileError]
	FileInfo(path string, ctx chord.Context) Result[FileInfo, *FileError]
	ListDir(path string, ctx chord.Context) Result[[]FileInfo, *FileError]
	CanonicalPath(path string, ctx chord.Context) Result[string, *FileError]
	Exists(path string, ctx chord.Context) Result[bool, *FileError]
	CreateDir(path string, options *CreateDirOptions, ctx chord.Context) Result[struct{}, *FileError]
	Remove(path string, options *RemoveOptions, ctx chord.Context) Result[struct{}, *FileError]
	CreateTempDir(prefix string, ctx chord.Context) Result[string, *FileError]
	CreateTempFile(options *CreateTempFileOptions, ctx chord.Context) Result[string, *FileError]
	Cleanup(ctx chord.Context)
}

// ShellSpillOptions instructs Shell.Exec to spill complete raw output to a temporary file
// once either threshold is exceeded.
type ShellSpillOptions struct {
	AfterBytes int `json:"afterBytes"`
	// AfterLines counts complete or partial lines.
	AfterLines int `json:"afterLines"`
}

// ShellExecResult contains the exit code and optional spill file path from a completed shell command.
type ShellExecResult struct {
	ExitCode int `json:"exitCode"`
	// SpillPath holds the temporary file path containing complete raw output when spill thresholds were exceeded.
	SpillPath string `json:"spillPath,omitzero"`
}

// ShellExecOptions configures an invocation of Shell.Exec.
type ShellExecOptions struct {
	Cwd        string                                     `json:"cwd,omitzero"`
	Env        map[string]string                          `json:"env,omitzero"`
	InheritEnv *bool                                      `json:"inheritEnv,omitzero"`
	Timeout    *float64                                   `json:"timeout,omitzero"`
	OnOutput   func(text string, ctx chord.Context) error `json:"-"`
	Spill      *ShellSpillOptions                         `json:"spill,omitzero"`
}

// Shell provides command execution capabilities.
type Shell interface {
	Exec(command string, options *ShellExecOptions, ctx chord.Context) Result[ShellExecResult, *ExecutionError]
	Cleanup(ctx chord.Context)
}

// ExecutionEnv combines FileSystem and Shell capabilities.
type ExecutionEnv interface {
	FileSystem
	Shell
}
