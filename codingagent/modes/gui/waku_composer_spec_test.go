package gui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type wakuLayoutPlacement string

const (
	wakuPlaceFlowColumn        wakuLayoutPlacement = "flow-col"
	wakuPlaceFlowRow           wakuLayoutPlacement = "flow-row"
	wakuPlaceViewportCenter    wakuLayoutPlacement = "viewport-center"
	wakuPlacePopoverAboveLeft  wakuLayoutPlacement = "popover-above-left"
	wakuPlacePopoverAboveRight wakuLayoutPlacement = "popover-above-right"
)

type wakuIntentionalDiff struct {
	WakuElement string
	PiElement   string
	Rationale   string
}

type wakuLayoutNodeSpec struct {
	ID               string
	AccessName       string
	Role             ui.Role
	Placement        wakuLayoutPlacement
	ExactW           float32
	ExactH           float32
	MatchXWith       string
	MatchWidthWith   string
	AboveOf          string
	LeftOf           string
	BgHex            string
	NoOrangeFill     bool
	Children         []wakuLayoutNodeSpec
	IntentionalDiffs []wakuIntentionalDiff
}

// wakuReferenceComposerSpec encodes the canonical Waku composer & workspace geometry
// derived from Waku (src/app/composer.rs, sidebar.rs, render.rs).
func wakuReferenceComposerSpec() wakuLayoutNodeSpec {
	return wakuLayoutNodeSpec{
		ID:        "center-workspace",
		Placement: wakuPlaceFlowColumn,
		Children: []wakuLayoutNodeSpec{
			{
				ID:         "empty-state",
				AccessName: "Empty State",
				Role:       ui.RoleRegion,
				Placement:  wakuPlaceViewportCenter,
				Children: []wakuLayoutNodeSpec{
					{
						ID:         "empty-sparkle",
						AccessName: "Empty State Sparkle",
						Role:       ui.RoleImage,
						Placement:  wakuPlaceFlowColumn,
						ExactW:     20,
						ExactH:     20,
						AboveOf:    "What should we build in cwd?",
					},
					{
						ID:        "empty-heading",
						Placement: wakuPlaceFlowColumn,
					},
				},
			},
			{
				ID:               "composer-card",
				AccessName:       "Composer Card",
				Role:             ui.RoleRegion,
				Placement:        wakuPlaceFlowColumn,
				ExactW:           wakuContentMaxWidth,
				BgHex:            "#212121",
				MatchXWith:       "Workspace Footer",
				MatchWidthWith:   "Workspace Footer",
				AboveOf:          "Workspace Footer",
				IntentionalDiffs: wakuComposerIntentionalDiffs(),
				Children: []wakuLayoutNodeSpec{
					{
						ID:         "prompt-editor",
						AccessName: "Prompt editor",
						Placement:  wakuPlaceFlowColumn,
					},
					{
						ID:        "composer-toolbar",
						Placement: wakuPlaceFlowRow,
						Children: []wakuLayoutNodeSpec{
							{
								ID:         "model-chip",
								AccessName: "Select Model Chip",
								Role:       ui.RoleButton,
								Placement:  wakuPlaceFlowRow,
								ExactH:     24,
								LeftOf:     "Select Thinking Chip",
							},
							{
								ID:         "thinking-chip",
								AccessName: "Select Thinking Chip",
								Role:       ui.RoleButton,
								Placement:  wakuPlaceFlowRow,
								ExactH:     24,
							},
							{
								ID:         "attach-btn",
								AccessName: "Attach clipboard image",
								Role:       ui.RoleButton,
								Placement:  wakuPlaceFlowRow,
								ExactW:     24,
								ExactH:     24,
							},
							{
								ID:         "send-btn",
								AccessName: "Send",
								Role:       ui.RoleButton,
								Placement:  wakuPlaceFlowRow,
								ExactW:     26,
								ExactH:     26,
							},
						},
					},
				},
			},
			{
				ID:         "workspace-footer",
				AccessName: "Workspace Footer",
				Role:       ui.RoleRegion,
				Placement:  wakuPlaceFlowRow,
				ExactW:     wakuContentMaxWidth,
				ExactH:     wakuWorkspaceFooterHeight,
				Children: []wakuLayoutNodeSpec{
					{
						ID:         "project-chip",
						AccessName: "Select Project",
						Role:       ui.RoleButton,
						Placement:  wakuPlaceFlowRow,
						ExactH:     26,
						LeftOf:     "Select Workspace",
					},
					{
						ID:         "workspace-chip",
						AccessName: "Select Workspace",
						Role:       ui.RoleButton,
						Placement:  wakuPlaceFlowRow,
						ExactH:     26,
						LeftOf:     "Switch Git Branch",
					},
					{
						ID:         "git-branch-chip",
						AccessName: "Switch Git Branch",
						Role:       ui.RoleButton,
						Placement:  wakuPlaceFlowRow,
						ExactH:     26,
					},
					{
						ID:         "context-gauge-chip",
						AccessName: "Context Window Usage",
						Role:       ui.RoleButton,
						Placement:  wakuPlaceFlowRow,
						ExactH:     24,
					},
				},
			},
		},
	}
}

func wakuReferenceModelPickerPopoverSpec() wakuLayoutNodeSpec {
	return wakuLayoutNodeSpec{
		ID:           "model-picker-popover",
		AccessName:   "Model Picker Popover",
		Role:         ui.RoleDialog,
		Placement:    wakuPlacePopoverAboveLeft,
		ExactW:       460,
		ExactH:       390,
		MatchXWith:   "Select Model Chip",
		AboveOf:      "Select Model Chip",
		BgHex:        "#1A1A1A",
		NoOrangeFill: true,
		Children: []wakuLayoutNodeSpec{
			{
				ID:         "provider-rail",
				AccessName: "Model Provider Rail",
				Role:       ui.RoleToolbar,
				Placement:  wakuPlaceFlowRow,
				ExactW:     50,
				ExactH:     388, // 390px outer popover height minus 2x 1px border
				MatchXWith: "Model Picker Popover",
				Children: []wakuLayoutNodeSpec{
					{
						ID:         "rail-favorites",
						AccessName: "Filter Favorite Models",
						Role:       ui.RoleButton,
						ExactW:     38,
						ExactH:     38,
					},
				},
			},
			{
				ID:         "model-search",
				AccessName: "Search Models",
				Role:       ui.RoleTextField,
				Placement:  wakuPlaceFlowRow,
			},
		},
	}
}

func wakuComposerIntentionalDiffs() []wakuIntentionalDiff {
	return []wakuIntentionalDiff{
		{
			WakuElement: "MenuChip 'Full access' / SandboxMode selector",
			PiElement:   "omitted",
			Rationale:   "pi runs an in-process coding agent with configurable native tools rather than spawning sandboxed external provider CLIs via waku-daemon",
		},
		{
			WakuElement: "MenuChip AgentPreset selector",
			PiElement:   "omitted",
			Rationale:   "pi exposes custom agents/subagents and skills through tools, skills, and slash commands rather than daemon agent presets",
		},
		{
			WakuElement: "GoalChip / Goal verification control",
			PiElement:   "omitted",
			Rationale:   "Daemon goal-loop orchestration belongs to waku-daemon; pi uses native codingagent turns and extensions",
		},
		{
			WakuElement: "omitted",
			PiElement:   "'+' Attach clipboard image button + '! bash' / '!! bash' indicator badge",
			Rationale:   "pi natively supports inline interactive bash execution (! / !!) and one-click clipboard PNG staging",
		},
		{
			WakuElement: "omitted",
			PiElement:   "Extension statuses in Workspace Footer",
			Rationale:   "pi extensions can publish live status strings into FooterDataProvider without crowding the transcript",
		},
	}
}

func formatWakuSpecPseudocode(node wakuLayoutNodeSpec, depth int) string {
	var b strings.Builder
	pad := strings.Repeat("  ", depth)
	fmt.Fprintf(&b, "%s- %s [%s]", pad, node.ID, node.Placement)
	if node.AccessName != "" {
		fmt.Fprintf(&b, " label=%q", node.AccessName)
	}
	if node.ExactW > 0 || node.ExactH > 0 {
		fmt.Fprintf(&b, " size=%.0fx%.0f", node.ExactW, node.ExactH)
	}
	if node.MatchXWith != "" {
		fmt.Fprintf(&b, " alignX=%q", node.MatchXWith)
	}
	if node.MatchWidthWith != "" {
		fmt.Fprintf(&b, " matchW=%q", node.MatchWidthWith)
	}
	if node.AboveOf != "" {
		fmt.Fprintf(&b, " above=%q", node.AboveOf)
	}
	if node.LeftOf != "" {
		fmt.Fprintf(&b, " leftOf=%q", node.LeftOf)
	}
	if node.BgHex != "" {
		fmt.Fprintf(&b, " bg=%s", node.BgHex)
	}
	b.WriteByte('\n')
	for _, d := range node.IntentionalDiffs {
		fmt.Fprintf(&b, "%s  [NON-PARITY waku=%q pi=%q] rationale: %s\n", pad, d.WakuElement, d.PiElement, d.Rationale)
	}
	for _, ch := range node.Children {
		b.WriteString(formatWakuSpecPseudocode(ch, depth+1))
	}
	return b.String()
}

func formatLiveLayoutPseudocode(tt *ui.Tester, title string, names []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== %s ===\n", title)
	byName := make(map[string]ui.AccessInfo)
	for _, info := range tt.AccessAll() {
		if _, exists := byName[info.Name]; !exists && info.Name != "" {
			byName[info.Name] = info
		}
	}
	for _, name := range names {
		if info, ok := byName[name]; ok {
			fmt.Fprintf(&b, "  - %-28q role=%v rect=(x=%.1f, y=%.1f, w=%.1f, h=%.1f)\n",
				name, info.Role, info.Bounds.X, info.Bounds.Y, info.Bounds.W, info.Bounds.H)
		} else if r, ok := tt.Find(name); ok {
			fmt.Fprintf(&b, "  - %-28q find rect=(x=%.1f, y=%.1f, w=%.1f, h=%.1f)\n",
				name, r.X, r.Y, r.W, r.H)
		} else {
			fmt.Fprintf(&b, "  - %-28q [ABSENT]\n", name)
		}
	}
	return b.String()
}

func assertNoUnexplainedSpecDiffs(t *testing.T, node wakuLayoutNodeSpec) {
	t.Helper()
	for _, d := range node.IntentionalDiffs {
		if strings.TrimSpace(d.WakuElement) == "" || strings.TrimSpace(d.PiElement) == "" {
			t.Fatalf("spec node %q has incomplete IntentionalDiff: %+v", node.ID, d)
		}
		if len(strings.TrimSpace(d.Rationale)) < 16 {
			t.Fatalf("spec node %q has missing/trivial Rationale: %+v", node.ID, d)
		}
	}
	for _, ch := range node.Children {
		assertNoUnexplainedSpecDiffs(t, ch)
	}
}

func lookupSpecBox(tt *ui.Tester, name string) (ui.AccessInfo, bool) {
	if info, ok := tt.Access(name); ok {
		return info, true
	}
	if r, ok := tt.Find(name); ok {
		return ui.AccessInfo{Name: name, Bounds: r}, true
	}
	return ui.AccessInfo{}, false
}

func verifySpecAuditAndGeometry(t *testing.T, tt *ui.Tester, spec wakuLayoutNodeSpec) {
	t.Helper()
	assertNoUnexplainedSpecDiffs(t, spec)

	img := tt.Image()
	var walk func(n wakuLayoutNodeSpec)
	walk = func(n wakuLayoutNodeSpec) {
		if n.AccessName != "" {
			info, ok := lookupSpecBox(tt, n.AccessName)
			if !ok {
				t.Fatalf("spec %q: element %q not found; texts=%v", n.ID, n.AccessName, tt.Texts())
			}
			if n.Role != 0 && info.Role != 0 && info.Role != n.Role {
				t.Fatalf("spec %q (%q): role = %v, want %v", n.ID, n.AccessName, info.Role, n.Role)
			}
			r := info.Bounds
			if n.ExactW > 0 && math.Abs(float64(r.W-n.ExactW)) > 1.0 {
				t.Fatalf("spec %q (%q): width = %.1f, want %.1f (rect=%+v)", n.ID, n.AccessName, r.W, n.ExactW, r)
			}
			if n.ExactH > 0 && math.Abs(float64(r.H-n.ExactH)) > 1.0 {
				t.Fatalf("spec %q (%q): height = %.1f, want %.1f (rect=%+v)", n.ID, n.AccessName, r.H, n.ExactH, r)
			}
			if n.MatchXWith != "" {
				ref, ok := lookupSpecBox(tt, n.MatchXWith)
				if !ok {
					t.Fatalf("spec %q: MatchXWith %q missing", n.ID, n.MatchXWith)
				}
				if math.Abs(float64(r.X-ref.Bounds.X)) > 2.0 {
					t.Fatalf("spec %q (%q).X = %.1f, want aligned with %q.X = %.1f", n.ID, n.AccessName, r.X, n.MatchXWith, ref.Bounds.X)
				}
			}
			if n.MatchWidthWith != "" {
				ref, ok := lookupSpecBox(tt, n.MatchWidthWith)
				if !ok {
					t.Fatalf("spec %q: MatchWidthWith %q missing", n.ID, n.MatchWidthWith)
				}
				if math.Abs(float64(r.W-ref.Bounds.W)) > 2.0 {
					t.Fatalf("spec %q (%q).W = %.1f, want equal to %q.W = %.1f", n.ID, n.AccessName, r.W, n.MatchWidthWith, ref.Bounds.W)
				}
			}
			if n.AboveOf != "" {
				ref, ok := lookupSpecBox(tt, n.AboveOf)
				if !ok {
					t.Fatalf("spec %q: AboveOf %q missing", n.ID, n.AboveOf)
				}
				if r.Y+r.H > ref.Bounds.Y+2.0 {
					t.Fatalf("spec %q (%q) bottom %.1f should sit above %q top %.1f", n.ID, n.AccessName, r.Y+r.H, n.AboveOf, ref.Bounds.Y)
				}
			}
			if n.LeftOf != "" {
				ref, ok := lookupSpecBox(tt, n.LeftOf)
				if !ok {
					t.Fatalf("spec %q: LeftOf %q missing", n.ID, n.LeftOf)
				}
				if r.X+r.W > ref.Bounds.X+2.0 {
					t.Fatalf("spec %q (%q) right %.1f should sit left of %q left %.1f", n.ID, n.AccessName, r.X+r.W, n.LeftOf, ref.Bounds.X)
				}
			}
			if n.NoOrangeFill {
				// Reject any wide (>=24px) & tall (>=24px) orange-filled background block,
				// while permitting thin 1px accent focus border rings and 18px coral icons.
				isOrange := func(c color.RGBA) bool {
					return c.R > 180 && int(c.R)-int(c.B) > 50
				}
				b := img.Bounds()
				x0 := max(b.Min.X, int(r.X)+6)
				x1 := min(b.Max.X, int(r.X+r.W)-30)
				y0 := max(b.Min.Y, int(r.Y)+6)
				y1 := min(b.Max.Y, int(r.Y+r.H)-6)
				for px := x0; px < x1; px += 4 {
					colRun := 0
					for py := y0; py < y1; py++ {
						col := img.RGBAAt(px, py)
						if isOrange(col) && isOrange(img.RGBAAt(px+24, py)) {
							colRun++
							if colRun >= 24 {
								t.Fatalf("spec %q (%q): unreadable orange background fill region around (%d,%d): %+v",
									n.ID, n.AccessName, px, py, col)
							}
						} else {
							colRun = 0
						}
					}
				}
			}
			if n.BgHex != "" {
				want := wpHexRGBA(t, n.BgHex)
				cx, cy := int(r.X+r.W/2), int(r.Y+r.H/2)
				if image.Pt(cx, cy).In(img.Bounds()) {
					got := img.RGBAAt(cx, cy)
					if !wpRGBAEqual(got, want) && colorDiffLarge(got, want) {
						// Note center point might hit child text; sample corner inset as fallback before failing.
						ix, iy := int(r.X)+8, int(r.Y)+4
						gotCorner := img.RGBAAt(ix, iy)
						if !wpRGBAEqual(gotCorner, want) {
							t.Fatalf("spec %q (%q) bg at (%d,%d) = %+v (center=%+v), want %s (%+v)",
								n.ID, n.AccessName, ix, iy, gotCorner, got, n.BgHex, want)
						}
					}
				}
			}
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(spec)
}

func colorDiffLarge(a, b color.RGBA) bool {
	return !wpRGBAEqual(a, b)
}

func saveWakuSnapshotPNG(t *testing.T, tt *ui.Tester, filename string) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("PI_WAKU_SNAPSHOT_DIR"))
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	outPath := filepath.Join(dir, filename)
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("Create(%s): %v", outPath, err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatalf("png.Encode(%s): %v", outPath, err)
	}
}

func saveWakuSnapshotReport(t *testing.T, filename, body string) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("PI_WAKU_SNAPSHOT_DIR"))
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", filename, err)
	}
}

func TestWakuComposerLayoutSpecPseudocodeParity(t *testing.T) {
	ctx := context.Background()
	t.Setenv(utils.EnvSessionDir, t.TempDir())

	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	holdTool := &agent.AgentTool{
		Name:        "hold_composer",
		Label:       "hold_composer",
		Description: "blocks briefly so busy composer and docked queue tray can be audited",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, _ string, _ any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			select {
			case <-toolStarted:
			default:
				close(toolStarted)
			}
			select {
			case <-releaseTool:
			case <-ctx.Done():
				return agent.AgentToolResult{}, ctx.Err()
			}
			return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}}}, nil
		},
	}

	fx := tgmNewFixture(t, &tgmFixtureOpts{tools: []*agent.AgentTool{holdTool}})

	// Seed a real git repository on branch "main" (with a second branch "feature/ui") so Footer branch switcher renders.
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", fx.cwd}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(fx.cwd, "README.md"), []byte("waku parity probe\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README.md: %v", err)
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Parity Tester")
	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")
	runGit("branch", "feature/ui")

	// Register multiple providers (anthropic, openai, xai) so Model Picker has a multi-provider icon rail.
	reg := fx.runtime.CurrentSession().ModelRegistry()
	apiKey := "test-key"
	fauxModel := fx.runtime.CurrentSession().Model()
	if fauxModel == nil {
		t.Fatal("expected default faux model")
	}
	baseURL := fauxModel.BaseUrl
	apiStr := fauxModel.Api
	registerCatalogProvider := func(provName string, defs []struct {
		id, name  string
		reasoning bool
		ctx       int
	}) {
		cfgModels := make([]core.ProviderModelConfig, 0, len(defs))
		for _, d := range defs {
			a := apiStr
			u := baseURL
			cfgModels = append(cfgModels, &core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:      d.id,
					Name:    d.name,
					Api:     &a,
					BaseUrl: &u,
					Input:   []ai.ModelModality{ai.ModelModalityText, ai.ModelModalityImage},
				},
				Reasoning:     d.reasoning,
				ContextWindow: d.ctx,
				MaxTokens:     8192,
			})
		}
		if err := reg.RegisterProvider(provName, core.ProviderConfigInput{
			BaseUrl: &baseURL,
			ApiKey:  &apiKey,
			Api:     &apiStr,
			Models:  cfgModels,
		}); err != nil {
			t.Fatalf("RegisterProvider(%s): %v", provName, err)
		}
	}

	registerCatalogProvider("anthropic", []struct {
		id, name  string
		reasoning bool
		ctx       int
	}{
		{"claude-opus-4-5", "Claude Opus 4.5", true, 200_000},
		{"claude-sonnet-4-5", "Claude Sonnet 4.5", true, 200_000},
	})
	registerCatalogProvider("openai", []struct {
		id, name  string
		reasoning bool
		ctx       int
	}{
		{"gpt-5", "GPT-5", true, 400_000},
		{"gpt-5-mini", "GPT-5 Mini", true, 400_000},
	})
	registerCatalogProvider("xai", []struct {
		id, name  string
		reasoning bool
		ctx       int
	}{
		{"grok-4", "Grok 4", true, 256_000},
	})

	stopTool := ai.StopReasonToolUse
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("hold_composer", nil, nil),
			}, &providers.FauxAssistantMessageOptions{
				StopReason: &stopTool,
			}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantText("final busy turn response"), nil),
		},
	})

	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	mode.refreshSidebarSessionsSync(ctx)

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.SetVibrancy(false)
	tt.SetDark(true)
	tt.Frame()

	var report strings.Builder
	idleSpec := wakuReferenceComposerSpec()
	report.WriteString("=== WAKU COMPOSER REFERENCE PSEUDOCODE SPEC ===\n")
	report.WriteString(formatWakuSpecPseudocode(idleSpec, 0))
	report.WriteByte('\n')

	// 1. Verify Idle Workspace + Stacked Empty State + 720px Composer Card + 720x28 Workspace Footer.
	verifySpecAuditAndGeometry(t, tt, idleSpec)

	headingR, ok := tt.Find("What should we build in cwd?")
	if !ok {
		t.Fatalf("expected Empty State heading, texts=%v", tt.Texts())
	}
	sparkleInfo, ok := tt.Access("Empty State Sparkle")
	if !ok {
		t.Fatalf("expected Empty State Sparkle, texts=%v", tt.Texts())
	}
	if sparkleInfo.Bounds.Y+sparkleInfo.Bounds.H > headingR.Y-10 {
		t.Fatalf("Empty State Sparkle bottom %.1f should be stacked >=10px above heading Y=%.1f", sparkleInfo.Bounds.Y+sparkleInfo.Bounds.H, headingR.Y)
	}
	sparkleCenterX := sparkleInfo.Bounds.X + sparkleInfo.Bounds.W/2
	headingCenterX := headingR.X + headingR.W/2
	if math.Abs(float64(sparkleCenterX-headingCenterX)) > 2.0 {
		t.Fatalf("Empty State Sparkle centerX %.1f not aligned with heading centerX %.1f", sparkleCenterX, headingCenterX)
	}
	emptyRegion, _ := tt.Access("Empty State")
	if emptyRegion.Bounds.H < 400 {
		t.Fatalf("expected Empty State container to fill viewport height (>=400), got %+v", emptyRegion.Bounds)
	}

	// Verify Send button shows muted ghost icon (#575757) on OverlayStrong when empty,
	// and folder icon X matches model chip icon X vertically.
	modelChipR, _ := tt.Find("Select Model Chip")
	footerR, _ := lookupSpecBox(tt, "Workspace Footer")
	cardR, _ := lookupSpecBox(tt, "Composer Card")
	if math.Abs(float64(cardR.Bounds.W-720)) > 1.0 || math.Abs(float64(footerR.Bounds.W-720)) > 1.0 {
		t.Fatalf("expected Composer Card (%.1f) and Workspace Footer (%.1f) both 720px wide", cardR.Bounds.W, footerR.Bounds.W)
	}
	if math.Abs(float64(modelChipR.X-cardR.Bounds.X-11)) > 2.0 {
		t.Fatalf("expected model chip inset ~11px from card left edge: chip.X=%.1f card.X=%.1f", modelChipR.X, cardR.Bounds.X)
	}

	report.WriteString(formatLiveLayoutPseudocode(tt, "PI GUI IDLE COMPOSER & EMPTY STATE", []string{
		"Empty State", "Empty State Sparkle", "What should we build in cwd?",
		"Composer Card", "Prompt editor", "Select Model Chip", "Select Thinking Chip",
		"Attach clipboard image", "Send", "Workspace Footer",
		"Select Project", "Select Workspace", "Switch Git Branch", "Context Window Usage",
	}))
	saveWakuSnapshotPNG(t, tt, "01_composer_idle.png")

	// 2. Open anchored Waku 460x390 Model Picker Popover (NOT centered modal!).
	if err := tt.Click("Select Model Chip"); err != nil {
		t.Fatalf("Click Select Model Chip: %v", err)
	}
	tt.Frame()
	if mode.GUI().HasOverlay() {
		t.Fatal("Model Picker must open as an anchored popover, not a centered modal overlay")
	}

	saveWakuSnapshotPNG(t, tt, "02_composer_model_picker.png")
	pickerSpec := wakuReferenceModelPickerPopoverSpec()
	verifySpecAuditAndGeometry(t, tt, pickerSpec)

	for _, want := range []string{"Filter Favorite Models", "Provider anthropic", "Provider openai", "Provider xai"} {
		if _, ok := tt.Access(want); !ok {
			t.Fatalf("expected Model Picker provider rail control %q, texts=%v", want, tt.Texts())
		}
	}
	report.WriteString(formatWakuSpecPseudocode(pickerSpec, 0))
	report.WriteString(formatLiveLayoutPseudocode(tt, "PI GUI ANCHORED MODEL PICKER POPOVER", []string{
		"Model Picker Popover", "Model Provider Rail", "Filter Favorite Models",
		"Provider anthropic", "Provider openai", "Provider xai", "Search Models",
	}))

	// Switch to OpenAI tab on left rail, favorite GPT-5, then switch to Favorites tab to verify star filter.
	if err := tt.Click("Provider openai"); err != nil {
		t.Fatalf("Click Provider openai: %v", err)
	}
	tt.Frame()
	if !tt.HasText("GPT-5") || !tt.HasText("GPT-5 Mini") {
		t.Fatalf("expected OpenAI models after clicking OpenAI rail tab, texts=%v", tt.Texts())
	}
	if err := tt.Click("Favorite Model openai/gpt-5"); err != nil {
		t.Fatalf("Click Favorite Model openai/gpt-5: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Filter Favorite Models"); err != nil {
		t.Fatalf("Click Filter Favorite Models: %v", err)
	}
	tt.Frame()
	if !tt.HasText("GPT-5") {
		t.Fatalf("expected favorited GPT-5 under Favorites tab, texts=%v", tt.Texts())
	}

	// Cross-provider fuzzy search while Favorites tab is selected.
	if err := tt.Click("Search Models"); err != nil {
		t.Fatalf("Click Search Models: %v", err)
	}
	tt.Type("grok")
	tt.Frame()
	if !tt.HasText("Grok 4") {
		t.Fatalf("expected cross-provider search to find Grok 4, texts=%v", tt.Texts())
	}

	// First Escape clears query without closing popover.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !tt.HasText("Model Picker Popover") && !tt.HasText("GPT-5") {
		t.Fatalf("expected first Escape to clear search query and keep Model Picker open, texts=%v", tt.Texts())
	}

	// Select Anthropic tab -> Down -> Enter chooses Claude Sonnet 4.5 (second Anthropic model).
	if err := tt.Click("Provider anthropic"); err != nil {
		t.Fatalf("Click Provider anthropic: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Search Models"); err != nil {
		t.Fatalf("Focus Search Models: %v", err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tt.HasText("GPT-5") || tt.HasText("Claude Sonnet 4.5") && mode.Session().Model().Id != "claude-sonnet-4-5" {
		// Note "Claude Sonnet 4.5" appears in composer chip once selected.
		t.Fatalf("unexpected model picker state after Enter, model=%+v, texts=%v", mode.Session().Model(), tt.Texts())
	}
	if mode.Session().Model() == nil || mode.Session().Model().Id != "claude-sonnet-4-5" {
		t.Fatalf("expected active model claude-sonnet-4-5 after Down+Enter, got %+v", mode.Session().Model())
	}

	// Test Ctrl+L and /model open the anchored Model Picker Popover (not modal overlay).
	tt.Key(ui.Ctrl, ui.KeyL)
	tt.Frame()
	if mode.GUI().HasOverlay() {
		t.Fatal("expected Ctrl+L to open anchored Model Picker Popover, not modal overlay")
	}
	if _, ok := tt.Access("Model Picker Popover"); !ok {
		t.Fatalf("expected Ctrl+L to open Model Picker Popover, texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Switch back to the faux provider model so the tool call turn below runs against our Faux provider.
	if err := fx.runtime.CurrentSession().SetModel(ctx, fauxModel); err != nil {
		t.Fatalf("restore fauxModel: %v", err)
	}
	tt.Frame()

	// 3. Busy composer + Stop left of Queue Follow-up + Inset 692px Docked Queue Tray.
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor: %v", err)
	}
	tt.Type("run hold tool")
	tt.Key(0, ui.KeyEnter)

	select {
	case <-toolStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hold_composer")
	}
	tgmWaitUntil(t, mode, tt, 3*time.Second, "busy Stop button shown", func() bool {
		return mode.Session().IsStreaming() && tt.HasText("Stop")
	})

	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor while busy: %v", err)
	}
	tt.Type("draft follow up")
	tt.Frame()

	stopR, ok := tt.Find("Stop")
	if !ok {
		t.Fatal("expected Stop button when busy")
	}
	qBtnR, ok := tt.Find("Queue Follow-up")
	if !ok {
		t.Fatal("expected Queue Follow-up button when busy with draft")
	}
	if stopR.W != 26 || stopR.H != 26 || qBtnR.W != 26 || qBtnR.H != 26 {
		t.Fatalf("expected Stop (%.0fx%.0f) and Queue Follow-up (%.0fx%.0f) both 26x26", stopR.W, stopR.H, qBtnR.W, qBtnR.H)
	}
	if stopR.X+stopR.W > qBtnR.X {
		t.Fatalf("Waku order requires Stop (x=%.1f,w=%.1f) LEFT of Queue Follow-up (x=%.1f)", stopR.X, stopR.W, qBtnR.X)
	}

	// Queue follow-up + another follow-up so the inset docked queue tray renders above Composer Card.
	if err := tt.Click("Queue Follow-up"); err != nil {
		t.Fatalf("Click Queue Follow-up: %v", err)
	}
	tgmWaitUntil(t, mode, tt, 3*time.Second, "queued item 1 shown", func() bool {
		return mode.Session().PendingMessageCount() == 1 && tt.HasText("draft follow up")
	})

	trayInfo, ok := tt.Access("Queued Messages Tray")
	if !ok {
		t.Fatalf("expected Queued Messages Tray region, texts=%v", tt.Texts())
	}
	cardBusyR, _ := lookupSpecBox(tt, "Composer Card")
	if math.Abs(float64(trayInfo.Bounds.W-(cardBusyR.Bounds.W-28))) > 2.0 || math.Abs(float64(trayInfo.Bounds.X-(cardBusyR.Bounds.X+14))) > 2.0 {
		t.Fatalf("expected Queue Tray inset 14px inside 720px card (W=692, X=%.1f); got tray=%+v card=%+v",
			cardBusyR.Bounds.X+14, trayInfo.Bounds, cardBusyR.Bounds)
	}
	if math.Abs(float64((trayInfo.Bounds.Y+trayInfo.Bounds.H)-cardBusyR.Bounds.Y)) > 2.0 {
		t.Fatalf("expected Queue Tray bottom %.1f flush with Composer Card top %.1f", trayInfo.Bounds.Y+trayInfo.Bounds.H, cardBusyR.Bounds.Y)
	}

	saveWakuSnapshotPNG(t, tt, "03_composer_busy_queue.png")

	// Promote follow-up to steer, then remove it cleanly.
	if err := tt.Click("Steer queued message 1"); err != nil {
		t.Fatalf("Click Steer queued message 1: %v", err)
	}
	tt.Frame()
	if len(mode.Session().GetSteeringMessages()) != 1 || len(mode.Session().GetFollowUpMessages()) != 0 {
		t.Fatalf("expected message promoted to steering, steer=%v follow=%v",
			mode.Session().GetSteeringMessages(), mode.Session().GetFollowUpMessages())
	}
	if err := tt.Click("Remove queued message 1"); err != nil {
		t.Fatalf("Click Remove queued message 1: %v", err)
	}
	tt.Frame()
	if mode.Session().PendingMessageCount() != 0 {
		t.Fatalf("expected queue empty after remove, count=%d", mode.Session().PendingMessageCount())
	}

	close(releaseTool)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "turn finished cleanly", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("final busy turn response")
	})

	// 4. Verify Workspace Footer Git Branch & Context Window popovers open ABOVE the footer.
	footerNow, _ := lookupSpecBox(tt, "Workspace Footer")
	if err := tt.Click("Switch Git Branch"); err != nil {
		t.Fatalf("Click Switch Git Branch: %v", err)
	}
	tt.Frame()
	brPopR, ok := tt.Find("Branches")
	if !ok {
		t.Fatalf("expected Branches popover, texts=%v", tt.Texts())
	}
	if brPopR.Y >= footerNow.Bounds.Y {
		t.Fatalf("expected Branches popover Y=%.1f above Workspace Footer Y=%.1f", brPopR.Y, footerNow.Bounds.Y)
	}

	if err := tt.Click("Context Window Usage"); err != nil {
		t.Fatalf("Click Context Window Usage: %v", err)
	}
	tt.Frame()
	ctxPopR, ok := tt.Find("Context Window")
	if !ok {
		t.Fatalf("expected Context Window popover, texts=%v", tt.Texts())
	}
	if ctxPopR.Y >= footerNow.Bounds.Y {
		t.Fatalf("expected Context Window popover Y=%.1f above Workspace Footer Y=%.1f", ctxPopR.Y, footerNow.Bounds.Y)
	}
	saveWakuSnapshotPNG(t, tt, "04_composer_footer_popovers.png")
	saveWakuSnapshotReport(t, "layout_pseudocode_report.txt", report.String())
}

func TestWakuFooterPickersInteractiveParity(t *testing.T) {
	ctx := context.Background()
	t.Setenv(utils.EnvSessionDir, t.TempDir())
	t.Setenv(utils.EnvAgentDir, t.TempDir())

	initGitRepo := func(dir, initialBranch string, extraBranches ...string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
		runGit := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git -C %s %v failed: %v\n%s", dir, args, err, string(out))
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("repo "+filepath.Base(dir)+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile README.md: %v", err)
		}
		runGit("init", "-b", initialBranch)
		runGit("config", "user.email", "test@example.com")
		runGit("config", "user.name", "Footer Picker Tester")
		runGit("add", "README.md")
		runGit("commit", "-m", "initial commit")
		for _, br := range extraBranches {
			runGit("branch", br)
		}
	}

	fx := tgmNewFixture(t, nil)
	longBranch := "fix/very-long-branch-name-that-must-truncate-inside-210px-chip"
	initGitRepo(fx.cwd, longBranch, "main", "feature/search-me")

	projTwo := filepath.Join(t.TempDir(), "beta-service")
	initGitRepo(projTwo, "main", "beta-feature")

	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	mode.refreshSidebarSessionsSync(ctx)
	mode.refreshGitBranchesSync(fx.cwd)

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.SetVibrancy(false)
	tt.SetDark(true)
	tt.Frame()

	// 1. Verify all 3 footer MenuChips (26px height, max width bounds enforced).
	for _, spec := range []struct {
		name string
		maxW float32
	}{
		{"Select Project", 190},
		{"Select Workspace", 180},
		{"Switch Git Branch", 210},
	} {
		info, ok := tt.Access(spec.name)
		if !ok {
			t.Fatalf("expected footer chip %q, texts=%v", spec.name, tt.Texts())
		}
		if info.Role != ui.RoleButton {
			t.Fatalf("footer chip %q role = %v, want RoleButton", spec.name, info.Role)
		}
		if math.Abs(float64(info.Bounds.H-26)) > 1.0 {
			t.Fatalf("footer chip %q height = %.1f, want 26", spec.name, info.Bounds.H)
		}
		if info.Bounds.W > spec.maxW+1.0 {
			t.Fatalf("footer chip %q width = %.1f, exceeds MaxWidth %.1f", spec.name, info.Bounds.W, spec.maxW)
		}
	}

	// Preserve unsent draft across project switching.
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor: %v", err)
	}
	tt.Type("keep unsent draft")
	tt.Frame()

	// 2. Project Picker: "New project..." via pickDirectoryHook -> beta-service.
	footerInfo, _ := lookupSpecBox(tt, "Workspace Footer")
	if err := tt.Click("Select Project"); err != nil {
		t.Fatalf("Click Select Project: %v", err)
	}
	tt.Frame()
	projPop, ok := tt.Access("Project Picker Popover")
	if !ok {
		t.Fatalf("expected Project Picker Popover, texts=%v", tt.Texts())
	}
	if math.Abs(float64(projPop.Bounds.W-300)) > 1.0 || projPop.Bounds.Y >= footerInfo.Bounds.Y {
		t.Fatalf("Project Picker Popover bounds = %+v, want 300px wide above footer Y=%.1f", projPop.Bounds, footerInfo.Bounds.Y)
	}

	mode.mu.Lock()
	mode.pickDirectoryHook = func() (string, error) { return projTwo, nil }
	mode.mu.Unlock()

	if err := tt.Click("New Project…"); err != nil {
		t.Fatalf("Click New Project…: %v", err)
	}
	tgmWaitUntil(t, mode, tt, 5*time.Second, "switched project to beta-service", func() bool {
		return wakuResolvePath(mode.Session().Cwd()) == wakuResolvePath(projTwo) &&
			tt.HasText("What should we build in beta-service?")
	})
	if gotDraft := mode.editor.Text(); gotDraft != "keep unsent draft" {
		t.Fatalf("expected unsent composer draft preserved after project switch, got %q", gotDraft)
	}

	// Clear editor so empty state & subsequent snapshots stay clean; inspect open Project Picker (shows both projects).
	mode.editor.SetText("")
	tt.Frame()
	if err := tt.Click("Select Project"); err != nil {
		t.Fatalf("Re-open Select Project: %v", err)
	}
	tt.Frame()
	if !tt.HasText("cwd") || !tt.HasText("beta-service") {
		t.Fatalf("expected both cwd and beta-service in Project Picker, texts=%v", tt.Texts())
	}
	saveWakuSnapshotPNG(t, tt, "05_footer_project_picker.png")

	// Search & switch back to original cwd via keyboard Enter.
	if err := tt.Click("Search Projects"); err != nil {
		t.Fatalf("Click Search Projects: %v", err)
	}
	tt.Type("cwd")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "switched project back to cwd", func() bool {
		return wakuResolvePath(mode.Session().Cwd()) == wakuResolvePath(fx.cwd) &&
			tt.HasText("What should we build in cwd?")
	})

	// 3. Workspace Picker: "New worktree" keeps project name "cwd" & switches workspace chip, then "Local" switches back.
	if err := tt.Click("Select Workspace"); err != nil {
		t.Fatalf("Click Select Workspace: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Access("Workspace Picker Popover"); !ok {
		t.Fatalf("expected Workspace Picker Popover, texts=%v", tt.Texts())
	}
	if err := tt.Click("New worktree"); err != nil {
		t.Fatalf("Click New worktree: %v", err)
	}
	tgmWaitUntil(t, mode, tt, 5*time.Second, "switched into linked git worktree", func() bool {
		curCwd := mode.Session().Cwd()
		return wakuResolvePath(curCwd) != wakuResolvePath(fx.cwd) &&
			strings.HasPrefix(filepath.Base(curCwd), "wt-")
	})
	wtCwd := mode.Session().Cwd()
	wtName := filepath.Base(wtCwd)
	mode.refreshGitBranchesSync(wtCwd)
	tt.Frame()
	if !tt.HasText("What should we build in cwd?") {
		t.Fatalf("expected empty state heading to retain main project 'cwd' while in linked worktree, texts=%v", tt.Texts())
	}
	if !tt.HasText(wtName) {
		t.Fatalf("expected Select Workspace chip to display worktree %q, texts=%v", wtName, tt.Texts())
	}

	if err := tt.Click("Select Workspace"); err != nil {
		t.Fatalf("Open Workspace Picker in worktree: %v", err)
	}
	tt.Frame()
	saveWakuSnapshotPNG(t, tt, "06_footer_workspace_picker.png")

	if err := tt.Click("Local workspace"); err != nil {
		t.Fatalf("Click Local workspace: %v", err)
	}
	tgmWaitUntil(t, mode, tt, 5*time.Second, "switched back to Local workspace", func() bool {
		return wakuResolvePath(mode.Session().Cwd()) == wakuResolvePath(fx.cwd) &&
			tt.HasText("Local")
	})

	// 4. Branch Picker (300px wide): search + checkout existing branch + create & checkout new branch.
	mode.refreshGitBranchesSync(fx.cwd)
	tt.Frame()
	if err := tt.Click("Switch Git Branch"); err != nil {
		t.Fatalf("Click Switch Git Branch: %v", err)
	}
	tt.Frame()
	brPop, ok := tt.Access("Branch Picker Popover")
	if !ok {
		t.Fatalf("expected Branch Picker Popover, texts=%v", tt.Texts())
	}
	footerNow, _ := lookupSpecBox(tt, "Workspace Footer")
	if math.Abs(float64(brPop.Bounds.W-300)) > 1.0 || brPop.Bounds.Y >= footerNow.Bounds.Y {
		t.Fatalf("Branch Picker Popover bounds = %+v, want 300px wide above footer Y=%.1f", brPop.Bounds, footerNow.Bounds.Y)
	}
	saveWakuSnapshotPNG(t, tt, "07_footer_branch_picker.png")

	if err := tt.Click("Search Git Branches"); err != nil {
		t.Fatalf("Click Search Git Branches: %v", err)
	}
	tt.Type("search-me")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "checked out feature/search-me", func() bool {
		return tt.HasText("feature/search-me")
	})

	mode.refreshGitBranchesSync(fx.cwd)
	tt.Frame()
	if err := tt.Click("Switch Git Branch"); err != nil {
		t.Fatalf("Re-open Switch Git Branch: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Create and checkout new branch…"); err != nil {
		t.Fatalf("Click Create and checkout new branch…: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Create and checkout new branch") || !tt.HasText("Press Return to create · Esc to cancel") {
		t.Fatalf("expected Waku branch create prompt and hint, texts=%v", tt.Texts())
	}
	// Escape exits create mode cleanly back to branch list.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !tt.HasText("Create and checkout new branch…") {
		t.Fatalf("expected Escape to exit branch create mode back to browse list, texts=%v", tt.Texts())
	}
	if err := tt.Click("Create and checkout new branch…"); err != nil {
		t.Fatalf("Re-open Create and checkout new branch…: %v", err)
	}
	tt.Frame()
	if err := tt.Click("New Branch Name"); err != nil {
		t.Fatalf("Click New Branch Name: %v", err)
	}
	tt.Type("feat/agent-created")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "created and checked out feat/agent-created", func() bool {
		return tt.HasText("feat/agent-created")
	})
}
