// Ported from packages/coding-agent/src/modes/interactive/program-status-reporter.ts (pi v1.5.2).

package interactive

import (
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

// BlockedStatus describes an interactive modal dialog waiting on user action.
type BlockedStatus struct {
	Kind    tui.ProgramStatusKind
	Message string
}

type psrBlockedEntry struct {
	source string
	status BlockedStatus
}

func psrFirstLine(text *string) string {
	if text == nil {
		return "Error"
	}
	s := *text
	if idx := strings.IndexAny(s, "\r\n"); idx != -1 {
		s = s[:idx]
	}
	trimmed := js.Trim(s)
	if trimmed == "" {
		return "Error"
	}
	return trimmed
}

// ProgramStatusReporter reports interactive-mode state to the terminal (OSC 7501):
// `working` during agent runs and compaction, `blocked` while a dialog waits for
// the user, then `done`, `error`, or `idle` once the run settles.
// Messages are limited to the session name, dialog titles, and the first line of
// errors; prompts and assistant output are never reported.
type ProgramStatusReporter struct {
	mu             sync.Mutex
	getTerminal    func() tui.Terminal
	getSessionName func() *string
	runActive      bool
	compacting     bool
	runResult      tui.ProgramStatus
	restingStatus  tui.ProgramStatus
	blocked        []psrBlockedEntry
	lastReport     string
	hasReported    bool
}

// NewProgramStatusReporter creates a ProgramStatusReporter reading terminal and session name dynamically.
func NewProgramStatusReporter(getTerminal func() tui.Terminal, getSessionName func() *string) *ProgramStatusReporter {
	return &ProgramStatusReporter{
		getTerminal:    getTerminal,
		getSessionName: getSessionName,
		runResult:      tui.ProgramStatus{State: tui.ProgramStatusStateDone},
		restingStatus:  tui.ProgramStatus{State: tui.ProgramStatusStateIdle},
	}
}

// HandleEvent updates OSC 7501 status state from a coding-agent session event.
func (p *ProgramStatusReporter) HandleEvent(event core.AgentSessionEvent) {
	if event == nil {
		return
	}
	p.mu.Lock()
	switch ev := event.(type) {
	case *core.AgentSessionEventAgentStart:
		p.runActive = true
		p.runResult = tui.ProgramStatus{State: tui.ProgramStatusStateDone}
	case *core.AgentSessionEventMessageEnd:
		// The latest response decides the outcome, so a retried error is replaced by its successful retry.
		msg, ok := ev.Message.(*ai.AssistantMessage)
		if !ok || msg == nil {
			p.mu.Unlock()
			return
		}
		if msg.StopReason == ai.StopReasonError {
			p.runResult = tui.ProgramStatus{
				State:   tui.ProgramStatusStateError,
				Message: psrFirstLine(msg.ErrorMessage),
			}
		} else {
			p.runResult = tui.ProgramStatus{State: tui.ProgramStatusStateDone}
		}
	case *core.AgentSessionEventCompactionStart:
		p.compacting = true
	case *core.AgentSessionEventCompactionEnd:
		p.compacting = false
		if p.runActive {
			// A failed recovery compaction ends the run unless a later response succeeds.
			if ev.Aborted {
				p.runResult = tui.ProgramStatus{State: tui.ProgramStatusStateIdle}
			} else if ev.ErrorMessage != nil && *ev.ErrorMessage != "" {
				p.runResult = tui.ProgramStatus{
					State:   tui.ProgramStatusStateError,
					Message: psrFirstLine(ev.ErrorMessage),
				}
			}
		} else if ev.Aborted {
			p.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusStateIdle}
		} else if ev.Reason == "manual" {
			if ev.ErrorMessage != nil && *ev.ErrorMessage != "" {
				p.restingStatus = tui.ProgramStatus{
					State:   tui.ProgramStatusStateError,
					Message: psrFirstLine(ev.ErrorMessage),
				}
			} else {
				p.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusStateDone}
			}
		}
	case *core.AgentSessionEventAgentSettled:
		p.runActive = false
		if ev.Aborted {
			p.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusStateIdle}
		} else {
			p.restingStatus = p.runResult
		}
	case *core.AgentSessionEventSessionInfoChanged:
		// The session name is part of working and done reports.
	default:
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	p.Report()
}

// SetBlocked reports `blocked` for source until cleared with nil. Reopening a source moves it to the end.
func (p *ProgramStatusReporter) SetBlocked(source string, status *BlockedStatus) {
	p.mu.Lock()
	for i := range p.blocked {
		if p.blocked[i].source == source {
			p.blocked = append(p.blocked[:i], p.blocked[i+1:]...)
			break
		}
	}
	if status != nil {
		p.blocked = append(p.blocked, psrBlockedEntry{
			source: source,
			status: *status,
		})
	}
	p.mu.Unlock()
	p.Report()
}

// Reset forgets the previous session's run state (e.g. after switching sessions).
func (p *ProgramStatusReporter) Reset() {
	p.mu.Lock()
	p.runActive = false
	p.compacting = false
	p.runResult = tui.ProgramStatus{State: tui.ProgramStatusStateDone}
	p.restingStatus = tui.ProgramStatus{State: tui.ProgramStatusStateIdle}
	p.mu.Unlock()
	p.Report()
}

// Report emits the current OSC 7501 status to the active terminal when it has changed.
func (p *ProgramStatusReporter) Report() {
	var sessionName *string
	if p.getSessionName != nil {
		sessionName = p.getSessionName()
	}

	p.mu.Lock()
	status := p.psrCurrentStatus(sessionName)
	status.App = utils.AppName
	key, err := jsonx.Stringify(status)
	if err != nil {
		p.mu.Unlock()
		return
	}
	if p.hasReported && key == p.lastReport {
		p.mu.Unlock()
		return
	}
	p.lastReport = key
	p.hasReported = true
	getTerminal := p.getTerminal
	p.mu.Unlock()

	if getTerminal == nil {
		return
	}
	term := getTerminal()
	if term == nil {
		return
	}
	term.SetProgramStatus(status)
}

func (p *ProgramStatusReporter) psrCurrentStatus(sessionName *string) tui.ProgramStatus {
	if len(p.blocked) > 0 {
		last := p.blocked[len(p.blocked)-1].status
		return tui.ProgramStatus{
			State:   tui.ProgramStatusStateBlocked,
			Kind:    last.Kind,
			Message: last.Message,
		}
	}
	if p.compacting {
		return tui.ProgramStatus{
			State:   tui.ProgramStatusStateWorking,
			Message: "Compacting context",
		}
	}
	status := p.restingStatus
	if p.runActive {
		status = tui.ProgramStatus{State: tui.ProgramStatusStateWorking}
	}
	if status.State == tui.ProgramStatusStateWorking || status.State == tui.ProgramStatusStateDone {
		if sessionName != nil {
			status.Message = *sessionName
		} else {
			status.Message = ""
		}
	}
	return status
}
