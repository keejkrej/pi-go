// Shared test harness for interactive mode unit and headless virtual-terminal tests.

package interactive

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func tithStrPtr(v string) *string { return &v }

func tithBoolPtr(v bool) *bool { return &v }

func tithIntPtr(v int) *int { return &v }

func tithInitTestTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = theme.InitTheme("dark", false)
	})
	if _, err := theme.InitTheme("dark", false); err != nil {
		t.Fatalf("InitTheme(dark): %v", err)
	}
}

// tithVirtualTerminal emulates a full terminal over github.com/charmbracelet/x/vt.
type tithVirtualTerminal struct {
	mu              sync.Mutex
	emulator        *vt.Emulator
	closed          bool
	inputHandler    func(data string)
	resizeHandler   func()
	columns         int
	rows            int
	kitty           bool
	writes          []string
	titles          []string
	progress        []bool
	programStatuses []tui.ProgramStatus
}

func tithNewVirtualTerminal(columns, rows int) *tithVirtualTerminal {
	if columns <= 0 {
		columns = 100
	}
	if rows <= 0 {
		rows = 30
	}
	emu := vt.NewEmulator(columns, rows)
	emu.RegisterApcHandler(func([]byte) bool { return true })
	// Drain outbound terminal responses (OSC 10/11 color queries, DSR, DA) so unbuffered
	// pipe writes inside vt.Emulator never block WriteString.
	go func() {
		_, _ = io.Copy(io.Discard, emu)
	}()
	return &tithVirtualTerminal{
		emulator: emu,
		columns:  columns,
		rows:     rows,
		kitty:    true,
	}
}

func (v *tithVirtualTerminal) tithClose() {
	if v == nil {
		return
	}
	v.mu.Lock()
	// Mark closed without calling v.emulator.Close() because charmbracelet/x/vt.Emulator
	// unsynchronized-mutates internal reader state against the background io.Copy drainer.
	v.closed = true
	v.inputHandler = nil
	v.resizeHandler = nil
	v.mu.Unlock()
}

func (v *tithVirtualTerminal) Start(onInput func(data string), onResize func()) {
	v.mu.Lock()
	if !v.closed && v.emulator != nil {
		v.inputHandler = onInput
		v.resizeHandler = onResize
		_, _ = v.emulator.WriteString("\x1b[?2004h")
	}
	v.mu.Unlock()
}

func (v *tithVirtualTerminal) DrainInput(_ *int64, _ *int64) error {
	return nil
}

func (v *tithVirtualTerminal) Stop() {
	v.mu.Lock()
	if !v.closed && v.emulator != nil {
		_, _ = v.emulator.WriteString("\x1b[?2004l")
	}
	v.inputHandler = nil
	v.resizeHandler = nil
	v.mu.Unlock()
}

func (v *tithVirtualTerminal) Write(data string) {
	v.mu.Lock()
	v.writes = append(v.writes, data)
	if !v.closed && v.emulator != nil {
		_, _ = v.emulator.WriteString(data)
	}
	v.mu.Unlock()
}

func (v *tithVirtualTerminal) Columns() int {
	v.mu.Lock()
	cols := v.columns
	v.mu.Unlock()
	return cols
}

func (v *tithVirtualTerminal) Rows() int {
	v.mu.Lock()
	rows := v.rows
	v.mu.Unlock()
	return rows
}

func (v *tithVirtualTerminal) KittyProtocolActive() bool {
	v.mu.Lock()
	k := v.kitty
	v.mu.Unlock()
	return k
}

func (v *tithVirtualTerminal) MoveBy(lines int) {
	if lines == 0 {
		return
	}
	v.mu.Lock()
	if !v.closed && v.emulator != nil {
		if lines > 0 {
			_, _ = v.emulator.WriteString(fmt.Sprintf("\x1b[%dB", lines))
		} else {
			_, _ = v.emulator.WriteString(fmt.Sprintf("\x1b[%dA", -lines))
		}
	}
	v.mu.Unlock()
}

func (v *tithVirtualTerminal) HideCursor() {
	v.Write("\x1b[?25l")
}

func (v *tithVirtualTerminal) ShowCursor() {
	v.Write("\x1b[?25h")
}

func (v *tithVirtualTerminal) ClearLine() {
	v.Write("\x1b[K")
}

func (v *tithVirtualTerminal) ClearFromCursor() {
	v.Write("\x1b[J")
}

func (v *tithVirtualTerminal) ClearScreen() {
	v.Write("\x1b[2J\x1b[H")
}

func (v *tithVirtualTerminal) SetTitle(title string) {
	v.mu.Lock()
	v.titles = append(v.titles, title)
	v.mu.Unlock()
	v.Write("\x1b]0;" + title + "\x07")
}

func (v *tithVirtualTerminal) SetProgress(active bool) {
	v.mu.Lock()
	v.progress = append(v.progress, active)
	v.mu.Unlock()
}

func (v *tithVirtualTerminal) SetProgramStatus(status tui.ProgramStatus) {
	v.mu.Lock()
	v.programStatuses = append(v.programStatuses, status)
	v.mu.Unlock()
}

func (v *tithVirtualTerminal) tithSendInput(data string) {
	v.mu.Lock()
	handler := v.inputHandler
	v.mu.Unlock()
	if handler != nil {
		handler(data)
	}
}

func (v *tithVirtualTerminal) tithTypeAndSubmit(text string) {
	if text != "" {
		v.tithSendInput(text)
	}
	v.tithSendInput("\r")
}

func (v *tithVirtualTerminal) tithResize(columns, rows int) {
	if columns <= 0 {
		columns = 1
	}
	if rows <= 0 {
		rows = 1
	}
	v.mu.Lock()
	v.columns = columns
	v.rows = rows
	if !v.closed && v.emulator != nil {
		v.emulator.Resize(columns, rows)
	}
	handler := v.resizeHandler
	v.mu.Unlock()
	if handler != nil {
		handler()
	}
}

func tithCellsPlainText(cells []uv.Cell) string {
	var b strings.Builder
	for _, c := range cells {
		if c.IsZero() {
			continue
		}
		if c.Content == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func (v *tithVirtualTerminal) tithViewportLinesLocked() []string {
	lines := make([]string, v.rows)
	for y := 0; y < v.rows; y++ {
		var b strings.Builder
		for x := 0; x < v.columns; x++ {
			c := v.emulator.CellAt(x, y)
			if c == nil || c.IsZero() {
				continue
			}
			if c.Content == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Content)
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return lines
}

func (v *tithVirtualTerminal) tithViewportText() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return strings.Join(v.tithViewportLinesLocked(), "\n")
}

func (v *tithVirtualTerminal) tithScrollBufferText() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var lines []string
	if !v.emulator.IsAltScreen() {
		if sb := v.emulator.Scrollback(); sb != nil {
			for _, sbLine := range sb.Lines() {
				lines = append(lines, tithCellsPlainText(sbLine))
			}
		}
	}
	lines = append(lines, v.tithViewportLinesLocked()...)
	return strings.Join(lines, "\n")
}

func (v *tithVirtualTerminal) tithTitles() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]string, len(v.titles))
	copy(out, v.titles)
	return out
}

func (v *tithVirtualTerminal) tithProgramStatuses() []tui.ProgramStatus {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]tui.ProgramStatus, len(v.programStatuses))
	copy(out, v.programStatuses)
	return out
}

var _ tui.Terminal = (*tithVirtualTerminal)(nil)

type tithRuntimeOptions struct {
	Tools                 []*agent.AgentTool
	InMemory              bool
	Settings              map[string]any
	ResourceLoader        core.ResourceLoader
	ResourceLoaderOptions *core.DefaultResourceLoaderOptions
	ExtraAuthKeys         map[string]ai.Credential
}

type tithRuntimeFixture struct {
	Runtime    *core.AgentSessionRuntime
	Faux       *providers.FauxProviderRegistration
	Provider   string
	Cwd        string
	AgentDir   string
	SessionDir string
	Models     []*ai.Model
}

func tithNewRuntimeFixture(t *testing.T, opts *tithRuntimeOptions) *tithRuntimeFixture {
	t.Helper()
	tithInitTestTheme(t)
	t.Setenv("PI_OFFLINE", "1")

	ctx := context.Background()
	tempRoot := t.TempDir()
	cwd := filepath.Join(tempRoot, "cwd")
	agentDir := filepath.Join(tempRoot, "agent")
	sessionDir := filepath.Join(tempRoot, "sessions")
	for _, d := range []string{cwd, agentDir, sessionDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}

	provName := fmt.Sprintf("faux-int-%d", time.Now().UnixNano())
	fauxReg := compat.RegisterFauxProvider(&providers.RegisterFauxProviderOptions{
		Provider: tithStrPtr(provName),
		Models: []providers.FauxModelDefinition{
			{
				Id:            "faux-1",
				Name:          tithStrPtr("Faux 1"),
				Reasoning:     tithBoolPtr(false),
				Input:         []string{"text", "image"},
				ContextWindow: tithIntPtr(128_000),
				MaxTokens:     tithIntPtr(4096),
			},
			{
				Id:            "faux-2",
				Name:          tithStrPtr("Faux 2"),
				Reasoning:     tithBoolPtr(true),
				Input:         []string{"text", "image"},
				ContextWindow: tithIntPtr(200_000),
				MaxTokens:     tithIntPtr(8192),
			},
		},
	})
	t.Cleanup(func() {
		if fauxReg != nil && fauxReg.Unregister != nil {
			fauxReg.Unregister()
		}
	})

	settings := map[string]any{
		"defaultProvider": provName,
		"defaultModel":    "faux-1",
	}
	if opts != nil {
		for k, v := range opts.Settings {
			settings[k] = v
		}
	}
	settingsBytes, err := jsonx.Marshal(settings)
	if err != nil {
		t.Fatalf("Marshal settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settingsBytes, 0o644); err != nil {
		t.Fatalf("WriteFile settings.json: %v", err)
	}

	authStorage := core.AuthStorageCreate(filepath.Join(agentDir, "auth.json"))
	fauxKey := "faux-key"
	if _, err := authStorage.Modify(ctx, provName, func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &fauxKey}, nil
	}, nil); err != nil {
		t.Fatalf("AuthStorage.Modify: %v", err)
	}
	if opts != nil {
		for extraProv, cred := range opts.ExtraAuthKeys {
			c := cred
			if _, err := authStorage.Modify(ctx, extraProv, func(ai.Credential) (ai.Credential, error) {
				return c, nil
			}, nil); err != nil {
				t.Fatalf("AuthStorage.Modify(%s): %v", extraProv, err)
			}
		}
	}

	providerModels := make([]core.ProviderModelConfig, 0, len(fauxReg.Models))
	for _, m := range fauxReg.Models {
		if m == nil {
			continue
		}
		modelApi := string(m.Api)
		modelBaseUrl := m.BaseUrl
		providerModels = append(providerModels, &core.ProviderChatModelConfig{
			ProviderModelConfigBase: core.ProviderModelConfigBase{
				Id:      m.Id,
				Name:    m.Name,
				Api:     &modelApi,
				BaseUrl: &modelBaseUrl,
				Input:   m.Input,
				Cost:    m.Cost,
			},
			Reasoning:     m.Reasoning,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
		})
	}
	apiStr := fauxReg.Api
	var baseUrl string
	if len(fauxReg.Models) > 0 && fauxReg.Models[0] != nil {
		baseUrl = fauxReg.Models[0].BaseUrl
	}

	var extraTools []*agent.AgentTool
	inMemory := false
	rlOpts := &core.DefaultResourceLoaderOptions{
		NoExtensions:      true,
		NoSkills:          true,
		NoPromptTemplates: true,
		NoThemes:          true,
		NoContextFiles:    true,
	}
	var customRL core.ResourceLoader
	if opts != nil {
		extraTools = opts.Tools
		inMemory = opts.InMemory
		customRL = opts.ResourceLoader
		if opts.ResourceLoaderOptions != nil {
			rlOpts = opts.ResourceLoaderOptions
		}
	}

	createRuntime := func(c context.Context, args core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
		services, err := core.CreateAgentSessionServices(c, &core.CreateAgentSessionServicesOptions{
			Cwd:                   args.Cwd,
			AgentDir:              args.AgentDir,
			AuthStorage:           authStorage,
			ResourceLoader:        customRL,
			ResourceLoaderOptions: rlOpts,
		})
		if err != nil {
			return nil, err
		}
		if err := services.ModelRegistry.RegisterProvider(provName, core.ProviderConfigInput{
			BaseUrl: &baseUrl,
			ApiKey:  &fauxKey,
			Api:     &apiStr,
			Models:  providerModels,
		}); err != nil {
			return nil, err
		}
		res, err := core.CreateAgentSessionFromServices(c, core.CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    args.SessionManager,
			SessionStartEvent: args.SessionStartEvent,
			Tools:             extraTools,
		})
		if err != nil {
			return nil, err
		}
		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *res,
			Services:                 services,
			Diagnostics:              services.Diagnostics,
		}, nil
	}

	var sm *core.SessionManager
	if inMemory {
		sm = core.SessionManagerInMemory(cwd)
	} else {
		sm, err = core.SessionManagerCreate(cwd, sessionDir)
		if err != nil {
			t.Fatalf("SessionManagerCreate: %v", err)
		}
	}

	runtime, err := core.CreateAgentSessionRuntime(ctx, createRuntime, core.CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: sm,
	})
	if err != nil {
		t.Fatalf("CreateAgentSessionRuntime: %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Dispose(context.Background())
	})

	return &tithRuntimeFixture{
		Runtime:    runtime,
		Faux:       fauxReg,
		Provider:   provName,
		Cwd:        cwd,
		AgentDir:   agentDir,
		SessionDir: sessionDir,
		Models:     fauxReg.Models,
	}
}

type tithModeFixture struct {
	*tithRuntimeFixture
	Term *tithVirtualTerminal
	Mode *InteractiveMode
}

func tithNewModeFixture(t *testing.T, rtOpts *tithRuntimeOptions, modeOpts *InteractiveModeOptions) *tithModeFixture {
	t.Helper()
	fx := tithNewRuntimeFixture(t, rtOpts)
	term := tithNewVirtualTerminal(100, 30)

	var opts InteractiveModeOptions
	if modeOpts != nil {
		opts = *modeOpts
	}
	if opts.Terminal == nil {
		opts.Terminal = term
	}
	opts.DisableVersionCheck = true
	opts.DisableTmuxCheck = true
	if opts.IsInteractiveTerminal == nil {
		opts.IsInteractiveTerminal = func() bool { return true }
	}

	mode := NewInteractiveMode(fx.Runtime, &opts)
	if err := mode.Init(context.Background()); err != nil {
		t.Fatalf("InteractiveMode.Init: %v", err)
	}
	t.Cleanup(func() {
		mode.Stop()
		term.tithClose()
	})
	tithDrainUI(t, mode)
	return &tithModeFixture{
		tithRuntimeFixture: fx,
		Term:               term,
		Mode:               mode,
	}
}

func tithWaitUntil(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for: %s", timeout, desc)
}

func tithDrainUI(t *testing.T, mode *InteractiveMode) {
	t.Helper()
	ui := mode.Ui()
	if ui == nil {
		return
	}
	// Two sequential Post roundtrips ensure nested m.imPostUI(...) dispatches settle too.
	for range 2 {
		done := make(chan struct{})
		ui.Post(func() {
			ui.RenderNow(nil)
			close(done)
		})
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatalf("timed out draining UI post queue")
		}
	}
}

func tithRenderAllLines(comp tui.Component, width int) []string {
	if comp == nil {
		return nil
	}
	return comp.Render(width)
}

func tithRenderJoined(comp tui.Component, width int) string {
	return strings.Join(tithRenderAllLines(comp, width), "\n")
}

func tithRenderOnUI(t *testing.T, ui tui.TUI, comp tui.Component, width int) string {
	t.Helper()
	if ui == nil {
		return tithRenderJoined(comp, width)
	}
	var out string
	done := make(chan struct{})
	ui.Post(func() {
		out = tithRenderJoined(comp, width)
		close(done)
	})
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out rendering component on UI thread")
	}
	return out
}

type tithStubComponent struct {
	lines []string
}

func (s *tithStubComponent) Render(width int) []string {
	return s.lines
}

func (s *tithStubComponent) Invalidate() {}
