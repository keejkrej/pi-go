package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

// SessionImportFileNotFoundError reports that an imported session JSONL file
// does not exist at the supplied path.
type SessionImportFileNotFoundError struct {
	Path string
}

// Error implements error.
func (e *SessionImportFileNotFoundError) Error() string {
	return fmt.Sprintf("Import file not found: %s", e.Path)
}

// Unwrap allows errors.Is(err, os.ErrNotExist) checks.
func (e *SessionImportFileNotFoundError) Unwrap() error {
	return os.ErrNotExist
}

// CreateAgentSessionRuntimeResult is returned by CreateAgentSessionRuntimeFactory
// when initializing or replacing the active session in AgentSessionRuntime.
type CreateAgentSessionRuntimeResult struct {
	CreateAgentSessionResult
	Services    *AgentSessionServices
	Diagnostics []AgentSessionRuntimeDiagnostic
}

// AgentSessionRuntimeCreateArgs passes session initialization parameters to a
// CreateAgentSessionRuntimeFactory invocation.
type AgentSessionRuntimeCreateArgs struct {
	Cwd               string
	AgentDir          string
	SessionManager    *SessionManager
	SessionStartEvent *SessionStartEvent
}

// CreateAgentSessionRuntimeFactory builds an initialized session + services pair
// for startup and every subsequent session replacement (new, resume, fork, import).
type CreateAgentSessionRuntimeFactory func(ctx context.Context, args AgentSessionRuntimeCreateArgs) (*CreateAgentSessionRuntimeResult, error)

// CreateAgentSessionRuntimeOptions specifies startup session location and initial session manager.
type CreateAgentSessionRuntimeOptions struct {
	Cwd            string
	AgentDir       string
	SessionManager *SessionManager
}

// NewSessionRuntimeOptions configures runtime.NewSession.
type NewSessionRuntimeOptions struct {
	ParentSession *string
	Setup         func(sm *SessionManager) error
}

// NewSessionResult describes whether runtime.NewSession completed or was cancelled by an extension.
type NewSessionResult struct {
	Cancelled bool
}

// SwitchSessionResult describes whether runtime.SwitchSession or ImportFromJsonl completed or was cancelled.
type SwitchSessionResult struct {
	Cancelled bool
}

// ForkSessionOptions configures runtime.Fork.
type ForkSessionOptions struct {
	Position string // "before" (default) or "at"
}

// ForkSessionResult returns fork outcome and any extracted user prompt text for "before" forks.
type ForkSessionResult struct {
	Cancelled    bool
	SelectedText *string
}

// AgentSessionRuntime owns the active AgentSession and shared services across
// session lifecycle transitions (new session, switch, fork, JSONL import, and dispose).
type AgentSessionRuntime struct {
	mu                   sync.Mutex
	createRuntime        CreateAgentSessionRuntimeFactory
	cwd                  string
	agentDir             string
	Session              *AgentSession
	Services             *AgentSessionServices
	Diagnostics          []AgentSessionRuntimeDiagnostic
	ModelFallbackMessage *string
	disposed             bool
}

// Cwd returns the runtime's working directory.
func (r *AgentSessionRuntime) Cwd() string {
	r.mu.Lock()
	cwd := r.cwd
	r.mu.Unlock()
	return cwd
}

// CurrentSession returns the active *AgentSession under r.mu so concurrent UI readers
// do not race with replaceSession during NewSession, SwitchSession, Fork, or ImportFromJsonl.
func (r *AgentSessionRuntime) CurrentSession() *AgentSession {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	sess := r.Session
	r.mu.Unlock()
	return sess
}

// CreateAgentSessionRuntime initializes the first session using createRuntime.
func CreateAgentSessionRuntime(
	ctx context.Context,
	createRuntime CreateAgentSessionRuntimeFactory,
	options CreateAgentSessionRuntimeOptions,
) (*AgentSessionRuntime, error) {
	if createRuntime == nil {
		return nil, fmt.Errorf("createRuntime factory is required")
	}

	cwd := options.Cwd
	if strings.TrimSpace(cwd) == "" {
		if options.SessionManager != nil {
			cwd = options.SessionManager.GetCwd()
		}
		if strings.TrimSpace(cwd) == "" {
			wd, err := os.Getwd()
			if err != nil {
				return nil, err
			}
			cwd = wd
		}
	} else {
		cwd = assResolvePath(cwd)
	}

	agentDir := options.AgentDir
	if strings.TrimSpace(agentDir) == "" {
		agentDir = utils.GetAgentDir()
	} else {
		agentDir = assResolvePath(agentDir)
	}

	sm := options.SessionManager
	if sm == nil {
		sessionDir := GetDefaultSessionDir(cwd, agentDir)
		created, err := SessionManagerCreate(cwd, sessionDir)
		if err != nil {
			return nil, err
		}
		sm = created
	}

	result, err := createRuntime(ctx, AgentSessionRuntimeCreateArgs{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: sm,
		SessionStartEvent: &SessionStartEvent{
			Type:   "session_start",
			Reason: "startup",
		},
	})
	if err != nil {
		return nil, err
	}

	diagnostics := result.Diagnostics
	if len(diagnostics) == 0 && result.Services != nil {
		diagnostics = result.Services.Diagnostics
	}

	return &AgentSessionRuntime{
		createRuntime:        createRuntime,
		cwd:                  cwd,
		agentDir:             agentDir,
		Session:              result.Session,
		Services:             result.Services,
		Diagnostics:          diagnostics,
		ModelFallbackMessage: result.ModelFallbackMessage,
	}, nil
}

// NewSession starts a fresh session (persisted if the current session is persisted)
// after running session_before_switch and settling the outgoing session.
func (r *AgentSessionRuntime) NewSession(ctx context.Context, options ...*NewSessionRuntimeOptions) (*NewSessionResult, error) {
	var opts NewSessionRuntimeOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	r.mu.Lock()
	if r.disposed || r.Session == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("AgentSessionRuntime has been disposed")
	}
	outgoingSession := r.Session
	outgoingServices := r.Services
	r.mu.Unlock()

	if cancelled, err := outgoingSession.emitBeforeSwitch(ctx, "new", nil); err != nil {
		return nil, err
	} else if cancelled {
		return &NewSessionResult{Cancelled: true}, nil
	}

	previousSessionFile := outgoingSession.SessionFile()
	outgoingSession.settleForReplacement(ctx)

	var nextSM *SessionManager
	var err error
	if outgoingSession.SessionManager.IsPersisted() {
		sessionDir := outgoingSession.SessionManager.GetSessionDir()
		var newOpts *NewSessionOptions
		if opts.ParentSession != nil {
			newOpts = &NewSessionOptions{ParentSession: opts.ParentSession}
		}
		nextSM, err = SessionManagerCreate(r.cwd, sessionDir, newOpts)
		if err != nil {
			return nil, err
		}
	} else {
		var newOpts *NewSessionOptions
		if opts.ParentSession != nil {
			newOpts = &NewSessionOptions{ParentSession: opts.ParentSession}
		}
		var inMemErr error
		nextSM, inMemErr = SessionManagerInMemoryWith(r.cwd, newOpts, nil)
		if inMemErr != nil {
			return nil, inMemErr
		}
	}

	if opts.Setup != nil {
		if err := opts.Setup(nextSM); err != nil {
			return nil, err
		}
	}

	targetFile := nextSM.GetSessionFile()
	if err := r.replaceSession(ctx, outgoingSession, outgoingServices, nextSM, "new", targetFile, previousSessionFile); err != nil {
		return nil, err
	}
	return &NewSessionResult{Cancelled: false}, nil
}

// SwitchSession switches the runtime to an existing session JSONL file.
func (r *AgentSessionRuntime) SwitchSession(ctx context.Context, sessionPath string, cwdOverride ...string) (*SwitchSessionResult, error) {
	r.mu.Lock()
	if r.disposed || r.Session == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("AgentSessionRuntime has been disposed")
	}
	outgoingSession := r.Session
	outgoingServices := r.Services
	r.mu.Unlock()

	resolvedTarget := assResolvePath(sessionPath)
	if cancelled, err := outgoingSession.emitBeforeSwitch(ctx, "resume", &resolvedTarget); err != nil {
		return nil, err
	} else if cancelled {
		return &SwitchSessionResult{Cancelled: true}, nil
	}

	previousSessionFile := outgoingSession.SessionFile()
	outgoingSession.settleForReplacement(ctx)

	sessionDir := outgoingSession.SessionManager.GetSessionDir()
	openOpts := SessionOpenOptions{SessionDir: sessionDir}
	if len(cwdOverride) > 0 && cwdOverride[0] != "" {
		c := cwdOverride[0]
		openOpts.CwdOverride = &c
	}
	nextSM, err := SessionManagerOpen(resolvedTarget, openOpts)
	if err != nil {
		return nil, err
	}

	if err := r.replaceSession(ctx, outgoingSession, outgoingServices, nextSM, "resume", &resolvedTarget, previousSessionFile); err != nil {
		return nil, err
	}
	return &SwitchSessionResult{Cancelled: false}, nil
}

// Fork forks the current session at entryId ("before" excludes entryId and returns
// its prompt text; "at" includes entryId) into a new session file.
func (r *AgentSessionRuntime) Fork(ctx context.Context, entryId string, options ...*ForkSessionOptions) (*ForkSessionResult, error) {
	position := "before"
	if len(options) > 0 && options[0] != nil && options[0].Position != "" {
		position = options[0].Position
	}

	r.mu.Lock()
	if r.disposed || r.Session == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("AgentSessionRuntime has been disposed")
	}
	outgoingSession := r.Session
	outgoingServices := r.Services
	cwd := r.cwd
	r.mu.Unlock()

	if cancelled, err := outgoingSession.emitBeforeFork(ctx, entryId, position); err != nil {
		return nil, err
	} else if cancelled {
		return &ForkSessionResult{Cancelled: true}, nil
	}

	sourceEntry := outgoingSession.SessionManager.GetEntry(entryId)
	if sourceEntry == nil {
		return nil, errors.New("Invalid entry ID for forking")
	}

	var leafTargetId string
	var selectedText *string
	if position == "at" {
		leafTargetId = sourceEntry.GetId()
	} else {
		msgEntry, ok := sourceEntry.(*SessionMessageEntry)
		if !ok || msgEntry.Message == nil || msgEntry.Message.GetRole() != "user" {
			return nil, errors.New("Invalid entry ID for forking")
		}
		if parentId := sourceEntry.GetParentId(); parentId != nil {
			leafTargetId = *parentId
		}
		if txt, ok := as1ExtractUserMessageText(msgEntry.Message); ok {
			selectedText = &txt
		}
	}

	previousSessionFile := outgoingSession.SessionFile()
	var nextSM *SessionManager
	if outgoingSession.SessionManager.IsPersisted() {
		if previousSessionFile == nil || *previousSessionFile == "" {
			return nil, errors.New("Persisted session is missing a session file")
		}
		sessionDir := outgoingSession.SessionManager.GetSessionDir()
		if leafTargetId == "" {
			var err error
			nextSM, err = SessionManagerCreate(cwd, sessionDir, &NewSessionOptions{
				ParentSession: previousSessionFile,
			})
			if err != nil {
				return nil, err
			}
		} else {
			if _, err := os.Stat(*previousSessionFile); err != nil {
				return nil, errors.New("This session has not been saved yet. Send a message before cloning or forking it.")
			}
			outgoingSession.settleForReplacement(ctx)
			var err error
			nextSM, err = SessionManagerOpen(*previousSessionFile, SessionOpenOptions{SessionDir: sessionDir})
			if err != nil {
				return nil, err
			}
			branchedPath, err := nextSM.CreateBranchedSession(leafTargetId)
			if err != nil {
				return nil, err
			}
			if branchedPath == nil {
				return nil, errors.New("Failed to create forked session")
			}
			goto replace
		}
		outgoingSession.settleForReplacement(ctx)
	} else {
		outgoingSession.settleForReplacement(ctx)
		nextSM = outgoingSession.SessionManager
		if leafTargetId == "" {
			nextSM.NewSession(&NewSessionOptions{ParentSession: previousSessionFile})
		} else {
			if _, err := nextSM.CreateBranchedSession(leafTargetId); err != nil {
				return nil, err
			}
		}
	}

replace:
	targetFile := nextSM.GetSessionFile()
	if err := r.replaceSession(ctx, outgoingSession, outgoingServices, nextSM, "fork", targetFile, previousSessionFile); err != nil {
		return nil, err
	}
	return &ForkSessionResult{
		Cancelled:    false,
		SelectedText: selectedText,
	}, nil
}

// ImportFromJsonl imports an external session JSONL file into the runtime's session
// directory without overwriting existing files, validates cwd existence, and switches to it.
func (r *AgentSessionRuntime) ImportFromJsonl(ctx context.Context, inputPath string, cwdOverride ...string) (*SwitchSessionResult, error) {
	resolvedInput := assResolvePath(inputPath)
	info, err := os.Stat(resolvedInput)
	if err != nil || info.IsDir() {
		return nil, &SessionImportFileNotFoundError{Path: resolvedInput}
	}

	r.mu.Lock()
	if r.disposed || r.Session == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("AgentSessionRuntime has been disposed")
	}
	outgoingSession := r.Session
	outgoingServices := r.Services
	r.mu.Unlock()

	sessionDir := outgoingSession.SessionManager.GetSessionDir()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, err
	}

	destPath := asrUniqueImportPath(sessionDir, filepath.Base(resolvedInput))
	if err := asrCopyFile(resolvedInput, destPath); err != nil {
		return nil, err
	}

	openOpts := SessionOpenOptions{SessionDir: sessionDir}
	if len(cwdOverride) > 0 && cwdOverride[0] != "" {
		c := cwdOverride[0]
		openOpts.CwdOverride = &c
	}
	nextSM, err := SessionManagerOpen(destPath, openOpts)
	if err != nil {
		_ = os.Remove(destPath)
		return nil, err
	}
	if err := AssertSessionCwdExists(nextSM, r.Cwd()); err != nil {
		_ = os.Remove(destPath)
		return nil, err
	}

	previousSessionFile := outgoingSession.SessionFile()
	outgoingSession.settleForReplacement(ctx)

	if err := r.replaceSession(ctx, outgoingSession, outgoingServices, nextSM, "resume", &destPath, previousSessionFile); err != nil {
		return nil, err
	}
	return &SwitchSessionResult{Cancelled: false}, nil
}

// Dispose emits session_shutdown, disposes the current session, and settles services.
func (r *AgentSessionRuntime) Dispose(ctx context.Context) error {
	r.mu.Lock()
	if r.disposed {
		r.mu.Unlock()
		return nil
	}
	r.disposed = true
	outgoingSession := r.Session
	outgoingServices := r.Services
	r.mu.Unlock()

	if outgoingSession != nil {
		outgoingSession.emitShutdown("quit", nil)
		outgoingSession.Dispose()
	}
	if outgoingServices != nil {
		err := outgoingServices.Settle(ctx)
		if outgoingServices.ModelRuntime != nil {
			outgoingServices.ModelRuntime.Close()
		}
		return err
	}
	return nil
}

func (r *AgentSessionRuntime) replaceSession(
	ctx context.Context,
	outgoingSession *AgentSession,
	outgoingServices *AgentSessionServices,
	nextSM *SessionManager,
	reason string,
	targetSessionFile *string,
	previousSessionFile *string,
) error {
	outgoingSession.emitShutdown(reason, targetSessionFile)
	outgoingSession.Dispose()
	if outgoingServices != nil {
		err := outgoingServices.Settle(ctx)
		if outgoingServices.ModelRuntime != nil {
			outgoingServices.ModelRuntime.Close()
		}
		if err != nil {
			return err
		}
	}

	result, err := r.createRuntime(ctx, AgentSessionRuntimeCreateArgs{
		Cwd:            nextSM.GetCwd(),
		AgentDir:       r.agentDir,
		SessionManager: nextSM,
		SessionStartEvent: &SessionStartEvent{
			Type:                "session_start",
			Reason:              reason,
			PreviousSessionFile: previousSessionFile,
		},
	})
	if err != nil {
		return err
	}

	diagnostics := result.Diagnostics
	if len(diagnostics) == 0 && result.Services != nil {
		diagnostics = result.Services.Diagnostics
	}

	r.mu.Lock()
	r.cwd = nextSM.GetCwd()
	r.Session = result.Session
	r.Services = result.Services
	r.Diagnostics = diagnostics
	r.ModelFallbackMessage = result.ModelFallbackMessage
	r.mu.Unlock()
	return nil
}

func asrUniqueImportPath(dir, filename string) string {
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	candidate := filepath.Join(dir, filename)
	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate
	}
	for i := 1; ; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func asrCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
