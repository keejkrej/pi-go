package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tfwFooterData struct {
	branch        *string
	statuses      map[string]string
	providerCount int
	onChange      func()
}

func (d *tfwFooterData) GetGitBranch() *string                   { return d.branch }
func (d *tfwFooterData) GetExtensionStatuses() map[string]string { return d.statuses }
func (d *tfwFooterData) GetAvailableProviderCount() int          { return d.providerCount }
func (d *tfwFooterData) OnBranchChange(cb func()) func() {
	d.onChange = cb
	return func() { d.onChange = nil }
}

func TestFooterFormatHelpers(t *testing.T) {
	if got := FormatTokens(950); got != "950" {
		t.Fatalf("FormatTokens(950) = %q", got)
	}
	if got := FormatTokens(1250); got != "1.2k" && got != "1.3k" {
		t.Fatalf("FormatTokens(1250) = %q", got)
	}
	if got := FormatTokens(50400); got != "50k" {
		t.Fatalf("FormatTokens(50400) = %q", got)
	}
	if got := FormatTokens(1500000); got != "1.5M" {
		t.Fatalf("FormatTokens(1500000) = %q", got)
	}

	home := "/Users/alice"
	if got := FormatCwdForFooter("/Users/alice/projects/app", &home); got != "~/projects/app" {
		t.Fatalf("FormatCwdForFooter = %q", got)
	}
	if got := FormatCwdForFooter("/Users/alice", &home); got != "~" {
		t.Fatalf("FormatCwdForFooter exact home = %q", got)
	}
}

func TestFooterComponentRenderStatsExtensionsAndWidths(t *testing.T) {
	sm := core.SessionManagerInMemory("/Users/alice/work")
	sm.AppendSessionInfo("refactor")
	_, err := sm.AppendMessage(&ai.AssistantMessage{
		Role:       "assistant",
		Model:      "claude-sonnet-4",
		Provider:   "anthropic",
		StopReason: ai.StopReasonStop,
		Usage: ai.Usage{
			Input:       1200,
			Output:      300,
			CacheRead:   5000,
			CacheWrite:  200,
			TotalTokens: 6700,
			Cost:        ai.UsageCost{Total: 0.042},
		},
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	model := &ai.Model{
		BaseModel:     ai.BaseModel{Id: "claude-sonnet-4", Provider: "anthropic"},
		Reasoning:     true,
		ContextWindow: 200000,
	}
	ag := agent.NewAgent(&agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			Model:         model,
			ThinkingLevel: agent.ThinkingLevelMedium,
		},
	})
	sess := core.NewAgentSession(core.AgentSessionConfig{
		Agent:           ag,
		SessionManager:  sm,
		SettingsManager: core.SettingsManagerInMemory(),
		Cwd:             "/Users/alice/work",
	})
	defer sess.Dispose()

	branch := "feat/go-port"
	fd := &tfwFooterData{
		branch:        &branch,
		providerCount: 2,
		statuses: map[string]string{
			"z-last":  "second\nline",
			"a-first": "first",
		},
	}

	footer := NewFooterComponent(sess, fd)
	defer footer.Dispose()

	// Wide render verifies pwd, branch, session name, usage, model+provider+thinking, and sorted statuses.
	wide := footer.Render(120)
	if len(wide) < 3 {
		t.Fatalf("expected 3 footer lines with extension statuses, got %d", len(wide))
	}
	plainWide := utils.StripAnsi(strings.Join(wide, "\n"))
	for _, want := range []string{"(feat/go-port)", "refactor", "↑1.2k", "↓300", "$0.042", "(anthropic)", "claude-sonnet-4", "medium", "(auto)", "first second line"} {
		if !strings.Contains(plainWide, want) {
			t.Fatalf("expected %q in wide footer:\n%s", want, plainWide)
		}
	}

	// Narrow widths never overflow.
	for _, w := range []int{20, 40, 60, 100} {
		lines := footer.Render(w)
		for idx, l := range lines {
			if vw := tui.VisibleWidth(l); vw > w {
				t.Fatalf("width %d line %d overflowed (%d > %d): %q", w, idx, vw, w, utils.StripAnsi(l))
			}
		}
	}

	// Branch change callback invalidates cleanly, and Dispose unsubscribes.
	if fd.onChange == nil {
		t.Fatalf("expected branch change callback registered")
	}
	fd.onChange()
	footer.SetAutoCompactEnabled(false)
	noAuto := utils.StripAnsi(strings.Join(footer.Render(120), "\n"))
	if strings.Contains(noAuto, "(auto)") {
		t.Fatalf("expected (auto) omitted after SetAutoCompactEnabled(false), got %s", noAuto)
	}

	footer.SetSession(nil)
	if lines := footer.Render(80); len(lines) != 0 {
		t.Fatalf("expected empty lines with nil session, got %v", lines)
	}
	footer.Dispose()
	if fd.onChange != nil {
		t.Fatalf("expected Dispose to unsubscribe branch change listener")
	}

	_ = fmt.Sprintf("")
}
