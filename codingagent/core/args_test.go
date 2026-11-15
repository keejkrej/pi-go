package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/tui"
)

func targsStrPtr(s string) *string { return &s }

func TestParseArgsFlagsAndAliases(t *testing.T) {
	parsed := ParseArgs([]string{
		"--provider", "anthropic",
		"--model", "claude-sonnet-4-5:high",
		"--api-key", "sk-test",
		"--system-prompt", "sys",
		"--append-system-prompt", "extra1",
		"--append-system-prompt", "extra2",
		"--thinking", "xhigh",
		"-c",
		"-r",
		"-h",
		"-v",
		"--gui",
		"--mode", "gui",
		"-n", "  my session  ",
		"--no-session",
		"--session", "sess-123",
		"--session-id", "fixed-id",
		"--fork", "fork-1",
		"--session-dir", "/tmp/sessions",
		"--models", "anthropic/*, *sonnet*",
		"-t", "read, bash, edit",
		"-xt", "grep, ls",
		"-e", "ext1.ts",
		"--extension", "ext2.ts",
		"-ne",
		"--no-mcp",
		"-p", "hello prompt",
		"--export", "in.jsonl",
		"-ns",
		"--skill", "skill.md",
		"--prompt-template", "tmpl.md",
		"-np",
		"--theme", "custom.json",
		"--use-theme", "dark",
		"--no-themes",
		"-nc",
		"--list-models", "opus",
		"--offline",
		"--tui-mode", "regular",
		"--verbose",
		"-a",
		"@README.md",
		"positional message",
	})

	if len(parsed.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", parsed.Diagnostics)
	}
	if parsed.Provider == nil || *parsed.Provider != "anthropic" {
		t.Fatalf("Provider = %v", parsed.Provider)
	}
	if parsed.Model == nil || *parsed.Model != "claude-sonnet-4-5:high" {
		t.Fatalf("Model = %v", parsed.Model)
	}
	if parsed.ApiKey == nil || *parsed.ApiKey != "sk-test" {
		t.Fatalf("ApiKey = %v", parsed.ApiKey)
	}
	if parsed.SystemPrompt == nil || *parsed.SystemPrompt != "sys" {
		t.Fatalf("SystemPrompt = %v", parsed.SystemPrompt)
	}
	if !slices.Equal(parsed.AppendSystemPrompt, []string{"extra1", "extra2"}) {
		t.Fatalf("AppendSystemPrompt = %v", parsed.AppendSystemPrompt)
	}
	if parsed.Thinking == nil || *parsed.Thinking != agent.ThinkingLevel("xhigh") {
		t.Fatalf("Thinking = %v", parsed.Thinking)
	}
	if !parsed.Continue || !parsed.Resume || !parsed.Help || !parsed.Version || !parsed.Gui {
		t.Fatalf("expected boolean flags set, got %+v", parsed)
	}
	if parsed.Mode == nil || *parsed.Mode != ModeGui {
		t.Fatalf("Mode = %v, want %q", parsed.Mode, ModeGui)
	}
	if parsed.Name == nil || *parsed.Name != "  my session  " {
		t.Fatalf("Name = %v", parsed.Name)
	}
	if !parsed.NoSession || !parsed.NoExtensions || !parsed.NoMcp || !parsed.Print ||
		!parsed.NoSkills || !parsed.NoPromptTemplates || !parsed.NoThemes || !parsed.NoContextFiles ||
		!parsed.Offline || !parsed.Verbose {
		t.Fatalf("expected bool flags to be true: %+v", parsed)
	}
	if parsed.Session == nil || *parsed.Session != "sess-123" ||
		parsed.SessionId == nil || *parsed.SessionId != "fixed-id" ||
		parsed.Fork == nil || *parsed.Fork != "fork-1" ||
		parsed.SessionDir == nil || *parsed.SessionDir != "/tmp/sessions" {
		t.Fatalf("session flags mismatch: %+v", parsed)
	}
	if !slices.Equal(parsed.Models, []string{"anthropic/*", "*sonnet*"}) {
		t.Fatalf("Models = %v", parsed.Models)
	}
	if !slices.Equal(parsed.Tools, []string{"read", "bash", "edit"}) {
		t.Fatalf("Tools = %v", parsed.Tools)
	}
	if !slices.Equal(parsed.ExcludeTools, []string{"grep", "ls"}) {
		t.Fatalf("ExcludeTools = %v", parsed.ExcludeTools)
	}
	if !slices.Equal(parsed.Extensions, []string{"ext1.ts", "ext2.ts"}) {
		t.Fatalf("Extensions = %v", parsed.Extensions)
	}
	if !slices.Equal(parsed.Skills, []string{"skill.md"}) {
		t.Fatalf("Skills = %v", parsed.Skills)
	}
	if !slices.Equal(parsed.PromptTemplates, []string{"tmpl.md"}) {
		t.Fatalf("PromptTemplates = %v", parsed.PromptTemplates)
	}
	if !slices.Equal(parsed.Themes, []string{"custom.json"}) {
		t.Fatalf("Themes = %v", parsed.Themes)
	}
	if parsed.UseTheme == nil || *parsed.UseTheme != "dark" {
		t.Fatalf("UseTheme = %v", parsed.UseTheme)
	}
	if parsed.Export == nil || *parsed.Export != "in.jsonl" {
		t.Fatalf("Export = %v", parsed.Export)
	}
	if parsed.ListModels == nil || parsed.ListModels.All || parsed.ListModels.Search != "opus" {
		t.Fatalf("ListModels = %+v", parsed.ListModels)
	}
	if parsed.TuiMode == nil || *parsed.TuiMode != tui.TuiModeRegular {
		t.Fatalf("TuiMode = %v", parsed.TuiMode)
	}
	if parsed.ProjectTrustOverride == nil || !*parsed.ProjectTrustOverride {
		t.Fatalf("ProjectTrustOverride = %v", parsed.ProjectTrustOverride)
	}
	if !slices.Equal(parsed.FileArgs, []string{"README.md"}) {
		t.Fatalf("FileArgs = %v", parsed.FileArgs)
	}
	if !slices.Equal(parsed.Messages, []string{"hello prompt", "positional message"}) {
		t.Fatalf("Messages = %v", parsed.Messages)
	}
}

func TestParseArgsModesAndDiagnostics(t *testing.T) {
	for _, m := range []Mode{ModeText, ModeJson, ModeRpc, ModeGui} {
		parsed := ParseArgs([]string{"--mode", string(m)})
		if parsed.Mode == nil || *parsed.Mode != m || len(parsed.Diagnostics) != 0 {
			t.Fatalf("mode %s -> mode=%v diags=%+v", m, parsed.Mode, parsed.Diagnostics)
		}
	}

	missingMode := ParseArgs([]string{"--mode"})
	if missingMode.Mode != nil || len(missingMode.Diagnostics) != 1 ||
		missingMode.Diagnostics[0] != (ArgsDiagnostic{Type: "error", Message: "--mode requires text, json, rpc, or gui"}) {
		t.Fatalf("missingMode = %+v", missingMode.Diagnostics)
	}

	missingModeFollowedByFlag := ParseArgs([]string{"--mode", "--gui"})
	if !missingModeFollowedByFlag.Gui || missingModeFollowedByFlag.Mode != nil || len(missingModeFollowedByFlag.Diagnostics) != 1 {
		t.Fatalf("expected --gui preserved after missing --mode: %+v", missingModeFollowedByFlag)
	}

	badMode := ParseArgs([]string{"--mode", "invalid"})
	if badMode.Mode != nil || len(badMode.Diagnostics) != 1 ||
		badMode.Diagnostics[0] != (ArgsDiagnostic{Type: "error", Message: `Invalid mode "invalid". Valid values: text, json, rpc, gui`}) {
		t.Fatalf("badMode = %+v", badMode.Diagnostics)
	}

	badThinking := ParseArgs([]string{"--thinking", "ultra"})
	if badThinking.Thinking != nil || len(badThinking.Diagnostics) != 1 ||
		badThinking.Diagnostics[0].Type != "warning" ||
		!strings.Contains(badThinking.Diagnostics[0].Message, `Invalid thinking level "ultra"`) {
		t.Fatalf("badThinking = %+v", badThinking.Diagnostics)
	}

	badTools := ParseArgs([]string{"--tools", "read,+bash"})
	if len(badTools.Tools) != 0 || len(badTools.Diagnostics) != 1 ||
		badTools.Diagnostics[0].Type != "error" || !strings.Contains(badTools.Diagnostics[0].Message, "--tools:") {
		t.Fatalf("badTools = %+v", badTools.Diagnostics)
	}

	goodModifiers := ParseArgs([]string{"-t", "+grep, -bash"})
	if len(goodModifiers.Diagnostics) != 0 || !slices.Equal(goodModifiers.Tools, []string{"+grep", "-bash"}) {
		t.Fatalf("goodModifiers = %+v diags=%+v", goodModifiers.Tools, goodModifiers.Diagnostics)
	}

	noThemeVal := ParseArgs([]string{"--use-theme", "--verbose"})
	if noThemeVal.UseTheme != nil || !noThemeVal.Verbose || len(noThemeVal.Diagnostics) != 1 ||
		noThemeVal.Diagnostics[0].Message != "--use-theme requires a theme name" {
		t.Fatalf("noThemeVal = %+v", noThemeVal)
	}

	noNameVal := ParseArgs([]string{"-n"})
	if noNameVal.Name != nil || len(noNameVal.Diagnostics) != 1 ||
		noNameVal.Diagnostics[0].Message != "--name requires a value" {
		t.Fatalf("noNameVal = %+v", noNameVal)
	}

	noTuiVal := ParseArgs([]string{"--tui-mode"})
	if len(noTuiVal.Diagnostics) != 1 || noTuiVal.Diagnostics[0].Message != "--tui-mode requires regular or fullscreen" {
		t.Fatalf("noTuiVal = %+v", noTuiVal.Diagnostics)
	}

	badTuiVal := ParseArgs([]string{"--tui-mode", "split"})
	if len(badTuiVal.Diagnostics) != 1 || badTuiVal.Diagnostics[0].Message != `Invalid TUI mode "split". Valid values: regular, fullscreen` {
		t.Fatalf("badTuiVal = %+v", badTuiVal.Diagnostics)
	}

	fsTui := ParseArgs([]string{"--tui-mode", "fullscreen", "-nt", "-nbt", "-na"})
	if fsTui.TuiMode == nil || *fsTui.TuiMode != tui.TuiModeFullscreen ||
		!fsTui.NoTools || !fsTui.NoBuiltinTools ||
		fsTui.ProjectTrustOverride == nil || *fsTui.ProjectTrustOverride {
		t.Fatalf("fsTui = %+v", fsTui)
	}
}

func TestParseArgsPrintDoubleDashListModelsAndUnknownFlags(t *testing.T) {
	// --print consumes message starting with --- (not a short/long flag) and skips @files
	p1 := ParseArgs([]string{"-p", "---", "--verbose"})
	if !p1.Print || !p1.Verbose || !slices.Equal(p1.Messages, []string{"---"}) {
		t.Fatalf("p1 = %+v", p1)
	}

	p2 := ParseArgs([]string{"--print", "@foo.txt", "--other-bool", "--key=val", "--pair", "value", "--trailing-flag"})
	if !p2.Print || len(p2.Messages) != 0 || !slices.Equal(p2.FileArgs, []string{"foo.txt"}) {
		t.Fatalf("p2 = %+v", p2)
	}
	if v, ok := p2.UnknownFlags.Get("other-bool"); !ok || v.IsString || !v.Bool {
		t.Fatalf("other-bool = %+v, %v", v, ok)
	}
	if v, ok := p2.UnknownFlags.Get("key"); !ok || !v.IsString || v.String != "val" {
		t.Fatalf("key = %+v, %v", v, ok)
	}
	if v, ok := p2.UnknownFlags.Get("pair"); !ok || !v.IsString || v.String != "value" {
		t.Fatalf("pair = %+v, %v", v, ok)
	}
	if v, ok := p2.UnknownFlags.Get("trailing-flag"); !ok || v.IsString || !v.Bool {
		t.Fatalf("trailing-flag = %+v, %v", v, ok)
	}

	// "--" stops flag parsing and treats remaining items strictly as files/messages
	afterDash := ParseArgs([]string{"--gui", "--", "--not-a-flag", "@file.go", "-x"})
	if !afterDash.Gui || !slices.Equal(afterDash.Messages, []string{"--not-a-flag", "-x"}) ||
		!slices.Equal(afterDash.FileArgs, []string{"file.go"}) || afterDash.UnknownFlags.Len() != 0 {
		t.Fatalf("afterDash = %+v", afterDash)
	}

	// --list-models without search yields All: true
	lm := ParseArgs([]string{"--list-models", "--verbose"})
	if lm.ListModels == nil || !lm.ListModels.All || lm.ListModels.Search != "" || !lm.Verbose {
		t.Fatalf("lm = %+v", lm)
	}
}

func TestArgsHelpersAndFormatHelp(t *testing.T) {
	for _, lvl := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if !IsValidThinkingLevel(lvl) {
			t.Fatalf("expected valid thinking level %q", lvl)
		}
	}
	if IsValidThinkingLevel("turbo") {
		t.Fatal("expected invalid thinking level turbo")
	}

	if NormalizeSessionName("   \t\n  ") != nil {
		t.Fatal("expected nil for whitespace session name")
	}
	if n := NormalizeSessionName("  alpha  "); n == nil || *n != "alpha" {
		t.Fatalf("NormalizeSessionName = %v", n)
	}

	help := FormatHelp(
		ExtensionFlag{Name: "custom-flag", Type: "boolean", Description: targsStrPtr("Enable custom flag"), ExtensionPath: "/ext/a.ts"},
		ExtensionFlag{Name: "custom-opt", Type: "string", ExtensionPath: "/ext/b.ts"},
	)
	for _, want := range []string{
		"--mode <mode>",
		"Output mode: text (default), json, rpc, or gui",
		"--gui",
		"Launch the native desktop GUI",
		"Extension CLI Flags:",
		"--custom-flag",
		"Enable custom flag",
		"--custom-opt <value>",
		"Registered by /ext/b.ts",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output missing %q", want)
		}
	}
}
