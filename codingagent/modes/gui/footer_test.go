package gui

import (
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
)

type tftMockProvider struct {
	mu       sync.Mutex
	branch   *string
	statuses map[string]string
	avail    int
	cb       func()
}

func (p *tftMockProvider) GetGitBranch() *string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.branch == nil {
		return nil
	}
	s := *p.branch
	return &s
}

func (p *tftMockProvider) GetExtensionStatuses() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]string, len(p.statuses))
	for k, v := range p.statuses {
		out[k] = v
	}
	return out
}

func (p *tftMockProvider) GetAvailableProviderCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.avail
}

func (p *tftMockProvider) OnBranchChange(cb func()) func() {
	p.mu.Lock()
	p.cb = cb
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		p.cb = nil
		p.mu.Unlock()
	}
}

func (p *tftMockProvider) setBranch(branch string) {
	p.mu.Lock()
	p.branch = &branch
	cb := p.cb
	p.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func TestFooterBarRenderingProviderUpdatesAndClicks(t *testing.T) {
	mainBranch := "main"
	prov := &tftMockProvider{
		branch:   &mainBranch,
		statuses: map[string]string{"ext": "\x1b[32m✓ ready\x1b[0m"},
		avail:    2,
	}

	var modelClicked, thinkingClicked bool
	fb := NewFooterBar(
		WithFooterDataProvider(prov),
		WithFooterOnModelClick(func() { modelClicked = true }),
		WithFooterOnThinkingClick(func() { thinkingClicked = true }),
	)
	defer fb.Dispose()

	fb.SetState(FooterState{
		Cwd:                "/tmp/repo",
		GitBranch:          &mainBranch,
		SessionName:        "refactor",
		Provider:           "anthropic",
		ModelId:            "claude-sonnet-4-5",
		RoutedModelId:      "claude-sonnet-4-5-20250929",
		ThinkingLevel:      agent.ThinkingLevel("high"),
		AvailableProviders: 2,
		InputTokens:        2400,
		OutputTokens:       600,
		TotalCost:          0.032,
		ContextTokens:      84000,
		HasContextTokens:   true,
		ContextWindow:      200000,
		ContextPercent:     42.0,
		HasContextPercent:  true,
		AutoCompactEnabled: true,
		SteeringCount:      1,
		FollowUpCount:      2,
		IsStreaming:        true,
		WorkingMessage:     "Drafting patch",
		ExtensionStatuses:  prov.GetExtensionStatuses(),
	})

	tt := ui.NewTester(func(c *ui.Context) {
		fb.View(c)
	}, 1100, 120)

	allText := strings.Join(tt.Texts(), " | ")
	for _, want := range []string{
		"/tmp/repo", "⎇ main", "#refactor", "✓ ready",
		"Drafting patch", "1 steering", "2 follow-up",
		"↑2.4k ↓600 $0.032", "42.0%/200k (auto)",
		"thinking: high", "(anthropic) claude-sonnet-4-5 → claude-sonnet-4-5-20250929",
	} {
		if !strings.Contains(allText, want) {
			t.Fatalf("missing %q in footer text:\n%s", want, allText)
		}
	}

	// Branch change notification updates footer automatically
	prov.setBranch("feat/gui")
	tt.Frame()
	if !tt.HasText("⎇ feat/gui") {
		t.Fatalf("expected updated git branch in footer, got %v", tt.Texts())
	}

	// Click pills
	tt.Click("thinking: high")
	if !thinkingClicked {
		t.Fatal("expected thinking pill click callback")
	}
	tt.Click("(anthropic) claude-sonnet-4-5 → claude-sonnet-4-5-20250929")
	if !modelClicked {
		t.Fatal("expected model pill click callback")
	}
}
