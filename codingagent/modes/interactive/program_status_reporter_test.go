// Ported from packages/coding-agent/test/program-status-reporter.test.ts (pi v1.5.2).

package interactive

import (
	"slices"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tpsrSpyTerminal struct {
	mu       sync.Mutex
	statuses []tui.ProgramStatus
}

func (s *tpsrSpyTerminal) Start(_ func(string), _ func())      {}
func (s *tpsrSpyTerminal) Stop()                               {}
func (s *tpsrSpyTerminal) DrainInput(_ *int64, _ *int64) error { return nil }
func (s *tpsrSpyTerminal) Write(_ string)                      {}
func (s *tpsrSpyTerminal) Columns() int                        { return 80 }
func (s *tpsrSpyTerminal) Rows() int                           { return 24 }
func (s *tpsrSpyTerminal) KittyProtocolActive() bool           { return false }
func (s *tpsrSpyTerminal) MoveBy(_ int)                        {}
func (s *tpsrSpyTerminal) HideCursor()                         {}
func (s *tpsrSpyTerminal) ShowCursor()                         {}
func (s *tpsrSpyTerminal) ClearLine()                          {}
func (s *tpsrSpyTerminal) ClearFromCursor()                    {}
func (s *tpsrSpyTerminal) ClearScreen()                        {}
func (s *tpsrSpyTerminal) SetTitle(_ string)                   {}
func (s *tpsrSpyTerminal) SetProgress(_ bool)                  {}
func (s *tpsrSpyTerminal) SetProgramStatus(status tui.ProgramStatus) {
	s.mu.Lock()
	s.statuses = append(s.statuses, status)
	s.mu.Unlock()
}

func (s *tpsrSpyTerminal) tpsrSnapshot() []tui.ProgramStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.statuses)
}

func TestProgramStatusReporterLifecycleAndBlockedStack(t *testing.T) {
	term := &tpsrSpyTerminal{}
	sessionName := "alpha"
	reporter := NewProgramStatusReporter(
		func() tui.Terminal { return term },
		func() *string { return &sessionName },
	)

	reporter.Report()
	reporter.Report() // identical status is deduplicated
	if got := len(term.tpsrSnapshot()); got != 1 {
		t.Fatalf("expected 1 initial idle report, got %d", got)
	}
	if first := term.tpsrSnapshot()[0]; first.State != tui.ProgramStatusStateIdle || first.App != utils.AppName {
		t.Fatalf("unexpected initial status: %+v", first)
	}

	// Start agent run -> working with session name
	reporter.HandleEvent(&core.AgentSessionEventAgentStart{})
	got := term.tpsrSnapshot()
	last := got[len(got)-1]
	if last.State != tui.ProgramStatusStateWorking || last.Message != "alpha" {
		t.Fatalf("expected working(alpha), got %+v", last)
	}

	// Blocked overlay supersedes working status cleanly
	reporter.SetBlocked("confirm-1", &BlockedStatus{
		Kind:    tui.ProgramStatusKindPermission,
		Message: "Allow tool execution?",
	})
	reporter.SetBlocked("input-2", &BlockedStatus{
		Kind:    tui.ProgramStatusKindQuestion,
		Message: "Enter branch name",
	})
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateBlocked || last.Kind != tui.ProgramStatusKindQuestion || last.Message != "Enter branch name" {
		t.Fatalf("expected top blocked dialog, got %+v", last)
	}

	// Clear top blocked -> restores confirm-1
	reporter.SetBlocked("input-2", nil)
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateBlocked || last.Kind != tui.ProgramStatusKindPermission {
		t.Fatalf("expected confirm-1 restored, got %+v", last)
	}

	// Clear confirm-1 -> back to working
	reporter.SetBlocked("confirm-1", nil)
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateWorking || last.Message != "alpha" {
		t.Fatalf("expected working after clearing blocked, got %+v", last)
	}

	// Error assistant response followed by retry success
	errMsg := "rate limit hit\nsecond line"
	reporter.HandleEvent(&core.AgentSessionEventMessageEnd{
		Message: &ai.AssistantMessage{
			StopReason:   ai.StopReasonError,
			ErrorMessage: &errMsg,
		},
	})
	reporter.HandleEvent(&core.AgentSessionEventAgentSettled{})
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateError || last.Message != "rate limit hit" {
		t.Fatalf("expected first-line error report, got %+v", last)
	}

	// Successful run emits done with session name
	reporter.HandleEvent(&core.AgentSessionEventAgentStart{})
	reporter.HandleEvent(&core.AgentSessionEventMessageEnd{
		Message: &ai.AssistantMessage{StopReason: ai.StopReasonStop},
	})
	reporter.HandleEvent(&core.AgentSessionEventAgentSettled{})
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateDone || last.Message != "alpha" {
		t.Fatalf("expected done(alpha), got %+v", last)
	}

	// Compaction transitions
	reporter.HandleEvent(&core.AgentSessionEventCompactionStart{})
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateWorking || last.Message != "Compacting context" {
		t.Fatalf("expected Compacting context, got %+v", last)
	}
	reporter.HandleEvent(&core.AgentSessionEventCompactionEnd{Reason: "manual"})
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateDone {
		t.Fatalf("expected done after manual compaction, got %+v", last)
	}

	// Reset clears back to idle
	reporter.Reset()
	got = term.tpsrSnapshot()
	last = got[len(got)-1]
	if last.State != tui.ProgramStatusStateIdle {
		t.Fatalf("expected idle after Reset, got %+v", last)
	}
}
