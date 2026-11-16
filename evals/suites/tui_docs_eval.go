// Ported from packages/evals/evals/tui.docs.eval.ts (pi v1.0.0).

package suites

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

const tuidocsContextWindow = 272_000

var tuidocsBuiltInContextPattern = regexp.MustCompile(`\d+(?:\.\d+)?%/272k(?: \(auto\))?`)

var tuidocsWhitespacePattern = regexp.MustCompile(`\s+`)

var tuidocsLineSplitPattern = regexp.MustCompile(`[\r\n]+`)

type tuidocsContextFixture struct {
	percent     float64
	expectedBar string
}

var tuidocsContextFixtures = []tuidocsContextFixture{
	{percent: 42.2, expectedBar: "████░░░░░░ 42%"},
	{percent: 65, expectedBar: "███████░░░ 65%"},
	{percent: 120, expectedBar: "██████████ 100%"},
}

// TuiFooterContextUsage captures context window usage during one footer render observation.
type TuiFooterContextUsage struct {
	Tokens        *int     `json:"tokens"`
	ContextWindow int      `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

// TuiFooterObservation captures one percentage fixture render and reported context usage.
type TuiFooterObservation struct {
	Percent        float64                `json:"percent"`
	RenderedOutput string                 `json:"renderedOutput"`
	ContextUsage   *TuiFooterContextUsage `json:"contextUsage"`
}

// TuiFooterOutput holds the captured footer renders and extension diagnostics for TUI docs evaluation.
type TuiFooterOutput struct {
	BuiltInStatusLine *string                `json:"builtInStatusLine"`
	Observations      []TuiFooterObservation `json:"observations"`
	ExtensionErrors   []string               `json:"extensionErrors"`
}

func tuidocsLevenshteinSimilarity(a, b string) float64 {
	ra := []rune(a)
	rb := []rune(b)
	maxLength := len(ra)
	if len(rb) > maxLength {
		maxLength = len(rb)
	}
	if maxLength == 0 {
		return 1
	}
	previous := make([]int, len(rb)+1)
	for j := 0; j <= len(rb); j++ {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current := make([]int, len(rb)+1)
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			best := previous[j] + 1
			if current[j-1]+1 < best {
				best = current[j-1] + 1
			}
			if previous[j-1]+cost < best {
				best = previous[j-1] + cost
			}
			current[j] = best
		}
		previous = current
	}
	return 1.0 - float64(previous[len(rb)])/float64(maxLength)
}

type tuidocsRecordingTerminal struct {
	mu            sync.Mutex
	columns       int
	rows          int
	kitty         bool
	writes        strings.Builder
	resizeHandler func()
}

func tuidocsNewRecordingTerminal() *tuidocsRecordingTerminal {
	return &tuidocsRecordingTerminal{
		columns: 100,
		rows:    30,
		kitty:   true,
	}
}

func (t *tuidocsRecordingTerminal) Start(_ func(string), onResize func()) {
	t.mu.Lock()
	t.resizeHandler = onResize
	t.mu.Unlock()
}

func (t *tuidocsRecordingTerminal) Stop() {
	t.mu.Lock()
	t.resizeHandler = nil
	t.mu.Unlock()
}

func (t *tuidocsRecordingTerminal) DrainInput(_ *int64, _ *int64) error { return nil }

func (t *tuidocsRecordingTerminal) Write(data string) {
	t.mu.Lock()
	t.writes.WriteString(data)
	t.mu.Unlock()
}

func (t *tuidocsRecordingTerminal) Columns() int              { return t.columns }
func (t *tuidocsRecordingTerminal) Rows() int                 { return t.rows }
func (t *tuidocsRecordingTerminal) KittyProtocolActive() bool { return t.kitty }
func (t *tuidocsRecordingTerminal) MoveBy(int)                {}
func (t *tuidocsRecordingTerminal) HideCursor()               {}
func (t *tuidocsRecordingTerminal) ShowCursor()               {}
func (t *tuidocsRecordingTerminal) ClearLine()                {}
func (t *tuidocsRecordingTerminal) ClearFromCursor()          {}
func (t *tuidocsRecordingTerminal) ClearScreen()              {}
func (t *tuidocsRecordingTerminal) SetTitle(string)           {}
func (t *tuidocsRecordingTerminal) SetProgress(bool)          {}
func (t *tuidocsRecordingTerminal) SetProgramStatus(tui.ProgramStatus) {
}

func (t *tuidocsRecordingTerminal) requestResize() {
	t.mu.Lock()
	handler := t.resizeHandler
	t.mu.Unlock()
	if handler != nil {
		handler()
	}
}

func (t *tuidocsRecordingTerminal) takeRenders(ui tui.TUI) ([]string, error) {
	time.Sleep(35 * time.Millisecond)
	t.mu.Lock()
	output := t.writes.String()
	t.mu.Unlock()
	if !strings.Contains(output, "\x1b[?2026h") && ui != nil {
		force := false
		ui.RenderNow(&force)
	}
	t.mu.Lock()
	output = t.writes.String()
	t.writes.Reset()
	t.mu.Unlock()

	const startMarker = "\x1b[?2026h"
	const endMarker = "\x1b[?2026l"
	var renders []string
	offset := 0
	for {
		startRel := strings.Index(output[offset:], startMarker)
		if startRel == -1 {
			return renders, nil
		}
		contentStart := offset + startRel + len(startMarker)
		endRel := strings.Index(output[contentStart:], endMarker)
		if endRel == -1 {
			return nil, fmt.Errorf("TUI emitted an incomplete synchronized render.")
		}
		renders = append(renders, utils.StripAnsi(output[contentStart:contentStart+endRel]))
		offset = contentStart + endRel + len(endMarker)
	}
}

var _ tui.Terminal = (*tuidocsRecordingTerminal)(nil)

func tuidocsLines(output string) []string {
	raw := tuidocsLineSplitPattern.Split(output, -1)
	var out []string
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func tuidocsCreateContextMessage(model ai.Model, percent float64) *ai.AssistantMessage {
	tokens := int(math.Round(float64(model.ContextWindow) * percent / 100.0))
	return &ai.AssistantMessage{
		Role:     "assistant",
		Content:  ai.ContentList{&ai.TextContent{Type: "text", Text: "Context usage fixture"}},
		Api:      ai.Api(model.Api),
		Provider: model.Provider,
		Model:    model.Id,
		Usage: ai.Usage{
			Input:       tokens,
			Output:      0,
			CacheRead:   0,
			CacheWrite:  0,
			TotalTokens: tokens,
			Cost:        ai.UsageCost{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0, Total: 0},
		},
		StopReason: ai.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

func tuidocsInspectContextFooter(ctx context.Context, session *core.AgentSession, agentDir string) (TuiFooterOutput, error) {
	activeModel := session.Model()
	if activeModel == nil {
		return TuiFooterOutput{}, fmt.Errorf("TUI footer oracle requires an active model.")
	}
	model := *activeModel
	model.ContextWindow = tuidocsContextWindow

	services := &core.AgentSessionServices{
		Cwd:             session.SessionManager.GetCwd(),
		AgentDir:        agentDir,
		ModelRuntime:    session.ModelRuntime(),
		ModelRegistry:   session.ModelRegistry(),
		SettingsManager: session.SettingsManager,
		ResourceLoader:  session.ResourceLoader(),
	}
	oracleSM := core.SessionManagerInMemory(services.Cwd)
	oracleRuntime, err := core.CreateAgentSessionRuntime(
		ctx,
		func(fCtx context.Context, args core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
			created, err := core.CreateAgentSessionFromServices(fCtx, core.CreateAgentSessionFromServicesOptions{
				Services:       services,
				SessionManager: args.SessionManager,
				Model:          &model,
				ThinkingLevel:  agent.ThinkingLevelHigh,
				NoTools:        core.NoToolsAll,
			})
			if err != nil {
				return nil, err
			}
			return &core.CreateAgentSessionRuntimeResult{
				CreateAgentSessionResult: *created,
				Services:                 services,
			}, nil
		},
		core.CreateAgentSessionRuntimeOptions{
			Cwd:            services.Cwd,
			AgentDir:       services.AgentDir,
			SessionManager: oracleSM,
		},
	)
	if err != nil {
		return TuiFooterOutput{}, err
	}
	oracleSession := oracleRuntime.Session

	terminal := tuidocsNewRecordingTerminal()
	tuiMode := tui.TuiModeRegular
	mode := interactive.NewInteractiveMode(oracleRuntime, &interactive.InteractiveModeOptions{
		TuiMode:             &tuiMode,
		Terminal:            terminal,
		DisableVersionCheck: true,
		DisableTmuxCheck:    true,
	})

	prevOffline, hadOffline := os.LookupEnv("PI_OFFLINE")
	_ = os.Setenv("PI_OFFLINE", "1")
	defer func() {
		mode.Stop()
		oracleSession.Dispose()
		_ = services.Settle(context.Background())
		if hadOffline {
			_ = os.Setenv("PI_OFFLINE", prevOffline)
		} else {
			_ = os.Unsetenv("PI_OFFLINE")
		}
	}()

	seed := tuidocsCreateContextMessage(model, 0)
	seed.Usage = ai.Usage{
		Input:       106_000,
		Output:      5_800,
		CacheRead:   606_000,
		CacheWrite:  0,
		TotalTokens: 717_800,
		Cost:        ai.UsageCost{Input: 1.008, Output: 0, CacheRead: 0, CacheWrite: 0, Total: 1.008},
	}
	if _, err := oracleSession.SessionManager.AppendMessage(seed); err != nil {
		return TuiFooterOutput{}, err
	}

	var builtInStatusLine *string
	observations := make([]TuiFooterObservation, 0, len(tuidocsContextFixtures))
	for index, fixture := range tuidocsContextFixtures {
		msg := tuidocsCreateContextMessage(model, fixture.percent)
		oracleSession.Agent.ReplaceMessages([]agent.AgentMessage{msg})
		if _, err := oracleSession.SessionManager.AppendMessage(msg); err != nil {
			return TuiFooterOutput{}, err
		}
		if index == 0 {
			if err := mode.Init(ctx); err != nil {
				return TuiFooterOutput{}, err
			}
		} else {
			terminal.requestResize()
		}
		renders, err := terminal.takeRenders(mode.Ui())
		if err != nil {
			return TuiFooterOutput{}, err
		}
		if index == 0 {
			for _, r := range renders {
				for _, line := range tuidocsLines(r) {
					if tuidocsBuiltInContextPattern.MatchString(line) {
						found := line
						builtInStatusLine = &found
						break
					}
				}
				if builtInStatusLine != nil {
					break
				}
			}
		}
		lastRender := ""
		if len(renders) > 0 {
			lastRender = renders[len(renders)-1]
		}
		var ctxUsage *TuiFooterContextUsage
		if cu := oracleSession.GetContextUsage(); cu != nil {
			converted := TuiFooterContextUsage{ContextWindow: cu.ContextWindow}
			if tok, ok := cu.Tokens.Get(); ok {
				tokCopy := tok
				converted.Tokens = &tokCopy
			}
			if pct, ok := cu.Percent.Get(); ok {
				pctCopy := pct
				converted.Percent = &pctCopy
			}
			ctxUsage = &converted
		}
		observations = append(observations, TuiFooterObservation{
			Percent:        fixture.percent,
			RenderedOutput: lastRender,
			ContextUsage:   ctxUsage,
		})
	}

	loadErrs := session.ResourceLoader().GetExtensions().Errors
	extErrStrings := make([]string, 0, len(loadErrs))
	for _, e := range loadErrs {
		extErrStrings = append(extErrStrings, e.Error)
	}
	return TuiFooterOutput{
		BuiltInStatusLine: builtInStatusLine,
		Observations:      observations,
		ExtensionErrors:   extErrStrings,
	}, nil
}

// TuiDocsEvalSuite returns the TUI footer documentation-lift eval suite ("Customize the footer context display").
func TuiDocsEvalSuite() *evals.EvalSuite {
	judge := evals.CreateJudge[evals.PiCodingAgentInput, TuiFooterOutput](
		"ContextFooterJudge",
		func(_ context.Context, args evals.JudgeInput[evals.PiCodingAgentInput, TuiFooterOutput]) (evals.JudgeOutcome, error) {
			output := args.Output
			valid := len(output.ExtensionErrors) == 0
			var similarities []float64
			const contextMarker = "{context}"

			reference := ""
			hasReference := false
			if output.BuiltInStatusLine != nil {
				normalized := strings.TrimSpace(*output.BuiltInStatusLine)
				normalized = tuidocsWhitespacePattern.ReplaceAllString(normalized, " ")
				reference = tuidocsBuiltInContextPattern.ReplaceAllString(normalized, contextMarker)
				hasReference = strings.Contains(reference, contextMarker)
			}
			if !hasReference {
				valid = false
			}

			for index, fixture := range tuidocsContextFixtures {
				if index >= len(output.Observations) {
					valid = false
					continue
				}
				obs := output.Observations[index]
				if obs.Percent != fixture.percent {
					valid = false
					continue
				}
				obsPercent := -1.0
				if obs.ContextUsage != nil && obs.ContextUsage.Percent != nil {
					obsPercent = *obs.ContextUsage.Percent
				}
				if obs.ContextUsage == nil || obs.ContextUsage.ContextWindow != tuidocsContextWindow || math.Round(obsPercent*10)/10 != fixture.percent {
					valid = false
				}

				actualStatusLine := ""
				foundStatusLine := false
				for _, line := range tuidocsLines(obs.RenderedOutput) {
					if strings.Contains(line, fixture.expectedBar) || tuidocsBuiltInContextPattern.MatchString(line) {
						actualStatusLine = line
						foundStatusLine = true
						break
					}
				}
				if !foundStatusLine {
					valid = false
					continue
				}
				normalizedStatusLine := tuidocsWhitespacePattern.ReplaceAllString(strings.TrimSpace(actualStatusLine), " ")
				if !strings.Contains(normalizedStatusLine, fixture.expectedBar) || tuidocsBuiltInContextPattern.MatchString(normalizedStatusLine) {
					valid = false
					continue
				}
				for _, other := range tuidocsContextFixtures {
					if other.expectedBar != fixture.expectedBar && strings.Contains(normalizedStatusLine, other.expectedBar) {
						valid = false
					}
				}
				if hasReference {
					substituted := strings.ReplaceAll(normalizedStatusLine, fixture.expectedBar, contextMarker)
					similarities = append(similarities, tuidocsLevenshteinSimilarity(substituted, reference))
				}
			}

			retained := 0.0
			if len(similarities) == len(tuidocsContextFixtures) {
				retained = similarities[0]
				for _, s := range similarities[1:] {
					if s < retained {
						retained = s
					}
				}
			}
			retainedPercent := math.Round(retained * 100)
			score := 0.0
			rationale := "Context progress behavior was incorrect."
			if valid {
				score = 1.0
				rationale = fmt.Sprintf("Footer retained %.0f%% of the built-in status text.", retainedPercent)
			}
			return evals.JudgeOutcome{
				Score:    &score,
				Metadata: jsonx.ObjectOf("rationale", rationale, "retainedPercent", retainedPercent),
			}, nil
		},
	)

	return evals.DescribeEval(
		"Customize the footer context display",
		evals.DescribeEvalOptions[evals.PiCodingAgentInput, TuiFooterOutput]{
			File:    "evals/suites/tui_docs_eval.go",
			Project: "docs",
			HarnessFactory: func(runOpts evals.SuiteRunOptions) (evals.Harness[evals.PiCodingAgentInput, TuiFooterOutput], error) {
				opts := evals.PiCodingAgentHarnessOptions[TuiFooterOutput]{
					Model:             runOpts.Model,
					SetupModelRuntime: runOpts.SetupModelRuntime,
					Output: func(args evals.PiOutputArgs) (TuiFooterOutput, error) {
						return tuidocsInspectContextFooter(args.Ctx, args.Session, args.AgentDir)
					},
				}
				if runOpts.Variant != "" {
					return evals.CreatePiDocumentationEvalHarnessForTest(runOpts.Variant, opts)
				}
				return evals.CreatePiDocumentationEvalHarness(opts)
			},
			Judges:         []evals.Judge[evals.PiCodingAgentInput, TuiFooterOutput]{judge},
			JudgeThreshold: nil,
		},
		func(it *evals.EvalCaseBuilder[evals.PiCodingAgentInput, TuiFooterOutput]) {
			it.It("replaces only the context window percentage with a 10-cell progress bar in the built-in footer", func(rc evals.EvalRunContext[evals.PiCodingAgentInput, TuiFooterOutput]) error {
				_, err := rc.Run(evals.StepsInput(
					evals.PiInputStep{
						Type:    evals.PiInputStepPrompt,
						Content: "Configure this running Pi installation with an extension that keeps the built-in footer and replaces only the context usage readout (`<percent>/<window>`) with a 10-cell progress bar made of `█` for filled cells and `░` for empty cells, rounded to the nearest integer cell and capped at 100%, followed by ` <percent>%` with the percentage rounded to the nearest integer (for example `████░░░░░░ 42%`). Keep every other footer item intact, including token counts, cost, model name, branch, and working directory.",
					},
					evals.PiInputStep{Type: evals.PiInputStepReload},
				))
				return err
			})
		},
	)
}
