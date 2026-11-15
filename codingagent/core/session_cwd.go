// Ported from packages/coding-agent/src/core/session-cwd.ts (pi v1.5.2).

package core

import (
	"fmt"
	"os"
)

// SessionCwdIssue describes a stored session working directory that no longer exists on disk.
type SessionCwdIssue struct {
	SessionFile *string
	SessionCwd  string
	FallbackCwd string
}

// SessionCwdSource provides the cwd and session file needed by GetMissingSessionCwdIssue.
type SessionCwdSource interface {
	GetCwd() string
	GetSessionFile() *string
}

// MissingSessionCwdChoice identifies how to resolve a missing session working directory.
type MissingSessionCwdChoice string

const (
	MissingSessionCwdChoiceContinue MissingSessionCwdChoice = "continue"
	MissingSessionCwdChoiceFork     MissingSessionCwdChoice = "fork"
)

// MissingSessionCwdOption represents one interactive choice for resolving a missing session cwd.
type MissingSessionCwdOption struct {
	Label string
	Value *MissingSessionCwdChoice
}

// MissingSessionCwdError is returned when a persisted session's stored cwd does not exist.
type MissingSessionCwdError struct {
	Issue SessionCwdIssue
}

func (e *MissingSessionCwdError) Error() string {
	return FormatMissingSessionCwdError(e.Issue)
}

// GetMissingSessionCwdIssue returns an issue if sessionManager points to a persisted session whose cwd is missing.
func GetMissingSessionCwdIssue(sessionManager SessionCwdSource, fallbackCwd string) *SessionCwdIssue {
	sessionFile := sessionManager.GetSessionFile()
	if sessionFile == nil || *sessionFile == "" {
		return nil
	}

	sessionCwd := sessionManager.GetCwd()
	if sessionCwd == "" {
		return nil
	}
	if _, err := os.Stat(sessionCwd); err == nil {
		return nil
	}

	return &SessionCwdIssue{
		SessionFile: sessionFile,
		SessionCwd:  sessionCwd,
		FallbackCwd: fallbackCwd,
	}
}

// FormatMissingSessionCwdError formats the error message for a missing session working directory.
func FormatMissingSessionCwdError(issue SessionCwdIssue) string {
	sessionFile := ""
	if issue.SessionFile != nil && *issue.SessionFile != "" {
		sessionFile = "\nSession file: " + *issue.SessionFile
	}
	return fmt.Sprintf(
		"Stored session working directory does not exist: %s%s\nCurrent working directory: %s",
		issue.SessionCwd,
		sessionFile,
		issue.FallbackCwd,
	)
}

// FormatMissingSessionCwdPrompt formats the interactive prompt title for a missing session cwd.
func FormatMissingSessionCwdPrompt(issue SessionCwdIssue) string {
	return fmt.Sprintf("Session cwd does not exist: %s", issue.SessionCwd)
}

// GetMissingSessionCwdOptions returns prompt options for handling a missing session cwd; a nil Value cancels.
func GetMissingSessionCwdOptions(issue SessionCwdIssue) []MissingSessionCwdOption {
	cont := MissingSessionCwdChoiceContinue
	fork := MissingSessionCwdChoiceFork
	return []MissingSessionCwdOption{
		{Label: fmt.Sprintf("Continue in %s (asks again next time)", issue.FallbackCwd), Value: &cont},
		{Label: fmt.Sprintf("Fork into %s (new session ID, original kept)", issue.FallbackCwd), Value: &fork},
		{Label: "Cancel", Value: nil},
	}
}

// AssertSessionCwdExists returns *MissingSessionCwdError if the persisted session's cwd does not exist.
func AssertSessionCwdExists(sessionManager SessionCwdSource, fallbackCwd string) error {
	if issue := GetMissingSessionCwdIssue(sessionManager, fallbackCwd); issue != nil {
		return &MissingSessionCwdError{Issue: *issue}
	}
	return nil
}
