package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

type tscStubCwdSource struct {
	sessionFile *string
	cwd         string
}

func (s tscStubCwdSource) GetCwd() string          { return s.cwd }
func (s tscStubCwdSource) GetSessionFile() *string { return s.sessionFile }

func tscStrPtr(s string) *string { return &s }

func TestSessionCwdHelpers(t *testing.T) {
	tempRoot := t.TempDir()
	existingDir := filepath.Join(tempRoot, "existing")
	missingDir := filepath.Join(tempRoot, "missing")
	fallbackDir := filepath.Join(tempRoot, "fallback")
	for _, d := range []string{existingDir, fallbackDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// In-memory / unpersisted session with non-existent cwd returns nil issue.
	ephemeral := SessionManagerInMemory(missingDir)
	if issue := GetMissingSessionCwdIssue(ephemeral, fallbackDir); issue != nil {
		t.Fatalf("expected nil issue for unpersisted session, got %+v", issue)
	}

	// Persisted session whose stored cwd still exists returns nil issue.
	persistedOK := tscStubCwdSource{
		sessionFile: tscStrPtr(filepath.Join(tempRoot, "ok.jsonl")),
		cwd:         existingDir,
	}
	if issue := GetMissingSessionCwdIssue(persistedOK, fallbackDir); issue != nil {
		t.Fatalf("expected nil issue when cwd exists, got %+v", issue)
	}

	// Persisted session whose cwd was deleted reports issue, error, prompt, and options.
	sessionFile := filepath.Join(tempRoot, "session.jsonl")
	persistedMissing := tscStubCwdSource{
		sessionFile: &sessionFile,
		cwd:         missingDir,
	}
	issue := GetMissingSessionCwdIssue(persistedMissing, fallbackDir)
	if issue == nil {
		t.Fatal("expected non-nil issue when stored cwd does not exist")
	}
	if issue.SessionCwd != missingDir || issue.FallbackCwd != fallbackDir ||
		issue.SessionFile == nil || *issue.SessionFile != sessionFile {
		t.Fatalf("unexpected issue: %+v", issue)
	}

	// Verify integration with real SessionManager persisted session whose cwd is removed.
	sessionDir := filepath.Join(tempRoot, "sessions")
	goneDir := filepath.Join(tempRoot, "gone")
	if err := os.MkdirAll(goneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sm, err := SessionManagerCreate(goneDir, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(&ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: tscStrPtr("hello")},
		Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(goneDir); err != nil {
		t.Fatal(err)
	}
	realIssue := GetMissingSessionCwdIssue(sm, fallbackDir)
	if realIssue == nil || realIssue.SessionFile == nil || *realIssue.SessionFile != *sm.GetSessionFile() {
		t.Fatalf("unexpected real SessionManager issue: %+v", realIssue)
	}

	err = AssertSessionCwdExists(persistedMissing, fallbackDir)
	if err == nil {
		t.Fatal("expected MissingSessionCwdError")
	}
	var missingErr *MissingSessionCwdError
	if ok := func() bool {
		m, isMissing := err.(*MissingSessionCwdError)
		missingErr = m
		return isMissing
	}(); !ok || missingErr == nil {
		t.Fatalf("expected *MissingSessionCwdError, got %T", err)
	}
	formattedErr := missingErr.Error()
	for _, want := range []string{missingDir, sessionFile, fallbackDir} {
		if !strings.Contains(formattedErr, want) {
			t.Fatalf("error %q missing %q", formattedErr, want)
		}
	}

	if err := AssertSessionCwdExists(persistedOK, fallbackDir); err != nil {
		t.Fatalf("expected nil error when cwd exists, got %v", err)
	}

	if prompt := FormatMissingSessionCwdPrompt(*issue); prompt != "Session cwd does not exist: "+missingDir {
		t.Fatalf("prompt = %q", prompt)
	}

	opts := GetMissingSessionCwdOptions(*issue)
	cont := MissingSessionCwdChoiceContinue
	fork := MissingSessionCwdChoiceFork
	wantOpts := []MissingSessionCwdOption{
		{Label: "Continue in " + fallbackDir + " (asks again next time)", Value: &cont},
		{Label: "Fork into " + fallbackDir + " (new session ID, original kept)", Value: &fork},
		{Label: "Cancel", Value: nil},
	}
	if !reflect.DeepEqual(opts, wantOpts) {
		t.Fatalf("options = %+v, want %+v", opts, wantOpts)
	}
}
