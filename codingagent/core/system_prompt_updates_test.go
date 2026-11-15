package core

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tspuPtrMap(m SystemPromptSections) *omap.Map[string, *string] {
	out := omap.NewMap[string, *string]()
	if m == nil {
		return out
	}
	for k, v := range m.All() {
		cp := v
		out.Set(k, &cp)
	}
	return out
}

func TestDiffSystemPromptSectionsAndStateValidation(t *testing.T) {
	prev, err := BuildSystemPromptSections(BuildSystemPromptOptions{Cwd: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if diff := DiffSystemPromptSections(tspuPtrMap(prev), prev); diff != nil {
		t.Fatalf("expected nil diff for identical sections, got %+v", diff)
	}

	secMap := omap.NewMap[string, string]()
	secMap.Set("plan_mode", "Implementation allowed.")
	curr, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:      "/tmp",
		Sections: secMap,
	})
	if err != nil {
		t.Fatal(err)
	}
	addedPatch := DiffSystemPromptSections(tspuPtrMap(prev), curr)
	if addedPatch == nil || addedPatch.Len() != 1 {
		t.Fatalf("expected 1-key patch adding plan_mode, got %+v", addedPatch)
	}
	if v, ok := addedPatch.Get("plan_mode"); !ok || v == nil || *v != "<plan_mode>\nImplementation allowed.\n</plan_mode>" {
		t.Fatalf("unexpected plan_mode patch entry: %v (ok=%v)", v, ok)
	}

	removedPatch := DiffSystemPromptSections(tspuPtrMap(curr), prev)
	if removedPatch == nil || removedPatch.Len() != 1 {
		t.Fatalf("expected 1-key patch removing plan_mode, got %+v", removedPatch)
	}
	if v, ok := removedPatch.Get("plan_mode"); !ok || v != nil {
		t.Fatalf("expected nil deletion sentinel for plan_mode, got %v (ok=%v)", v, ok)
	}

	customA := "You are A."
	customB := "You are B."
	prevCustom, err := BuildSystemPromptSections(BuildSystemPromptOptions{CustomPrompt: &customA, Cwd: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	currCustom, err := BuildSystemPromptSections(BuildSystemPromptOptions{CustomPrompt: &customB, Cwd: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if pre, _ := prevCustom.Get("preamble"); pre != "You are A." {
		t.Fatalf("expected untagged preamble, got %q", pre)
	}
	preambleDiff := DiffSystemPromptSections(tspuPtrMap(prevCustom), currCustom)
	if v, ok := preambleDiff.Get("preamble"); !ok || v == nil || *v != "You are B." || preambleDiff.Len() != 1 {
		t.Fatalf("unexpected preamble diff: %+v", preambleDiff)
	}

	forced := "Exact prompt."
	forcedState, err := BuildSystemPromptState(BuildSystemPromptOptions{ForceSystemPrompt: &forced, Cwd: "/tmp"})
	if err != nil || forcedState.Content != "Exact prompt." || forcedState.Sections != nil {
		t.Fatalf("unexpected forced system prompt state: %+v err=%v", forcedState, err)
	}

	badSections := omap.NewMap[string, string]()
	badSections.Set("preamble", "x")
	if _, err := BuildSystemPromptSections(BuildSystemPromptOptions{Cwd: "/tmp", Sections: badSections}); err == nil || !strings.Contains(err.Error(), "Invalid system prompt section name") {
		t.Fatalf("expected Invalid system prompt section name error, got %v", err)
	}
}

func TestSystemPromptToolUpdateDiffAndJsonRoundTrip(t *testing.T) {
	initial, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		SelectedTools:  []string{"first"},
		ToolSnippets:   map[string]string{"first": "first prompt snippet"},
		ToolGuidelines: map[string][]string{"first": {"Use first carefully."}},
		Cwd:            "/tmp",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := BuildSystemPromptSections(BuildSystemPromptOptions{
		SelectedTools:  []string{"second"},
		ToolSnippets:   map[string]string{"second": "second prompt snippet"},
		ToolGuidelines: map[string][]string{"second": {"Use second carefully."}},
		Cwd:            "/tmp",
	})
	if err != nil {
		t.Fatal(err)
	}
	diff := DiffSystemPromptSections(tspuPtrMap(initial), updated)
	if diff == nil || diff.Len() != 2 {
		t.Fatalf("expected diff on tools & rules only, got %+v", diff)
	}
	toolsSec, _ := diff.Get("tools")
	rulesSec, _ := diff.Get("rules")
	if toolsSec == nil || !strings.Contains(*toolsSec, "second prompt snippet") || strings.Contains(*toolsSec, "first prompt snippet") {
		t.Fatalf("unexpected tools section diff: %v", toolsSec)
	}
	if rulesSec == nil || strings.Contains(*rulesSec, "Use first carefully.") || !strings.Contains(*rulesSec, "Use second carefully.") {
		t.Fatalf("unexpected rules section diff: %v", rulesSec)
	}

	// JSON round-trip of ai.SystemMessage with sections preserves ai.GetSystemMessageText.
	empty := ""
	msg := &ai.SystemMessage{
		Role:     "system",
		Content:  ai.SystemMessageContent{Text: &empty},
		Sections: tspuPtrMap(updated),
	}
	raw, err := jsonx.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped ai.SystemMessage
	if err := jsonx.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if ai.GetSystemMessageText(&roundTripped) != ai.GetSystemMessageText(msg) {
		t.Fatalf("system message text mismatch after JSON round-trip:\nwant: %s\ngot:  %s", ai.GetSystemMessageText(msg), ai.GetSystemMessageText(&roundTripped))
	}
}
