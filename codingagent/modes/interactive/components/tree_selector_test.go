// Ported from packages/coding-agent/test/tree-selector.test.ts (pi v1.5.2).

package components

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func ttselSetupEnv(t *testing.T) {
	t.Helper()
	prevKB := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	_, _ = theme.InitTheme("dark", false)
	t.Cleanup(func() {
		tui.SetKeybindings(prevKB)
	})
}

func ttselStrPtr(s string) *string { return &s }

func ttselUserNode(id string, parentId *string, text string, label *string, children ...core.SessionTreeNode) core.SessionTreeNode {
	return core.SessionTreeNode{
		Entry: &core.SessionMessageEntry{
			SessionEntryBase: core.SessionEntryBase{
				Type:      "message",
				Id:        id,
				ParentId:  parentId,
				Timestamp: "2026-01-15T10:00:00.000Z",
			},
			Message: &ai.UserMessage{
				Role:    "user",
				Content: ai.UserMessageContent{Text: &text},
			},
		},
		Children: children,
		Label:    label,
	}
}

func ttselAssistantNode(id string, parentId *string, blocks ai.ContentList, stopReason ai.StopReason, errMsg *string, children ...core.SessionTreeNode) core.SessionTreeNode {
	return core.SessionTreeNode{
		Entry: &core.SessionMessageEntry{
			SessionEntryBase: core.SessionEntryBase{
				Type:      "message",
				Id:        id,
				ParentId:  parentId,
				Timestamp: "2026-01-15T10:01:00.000Z",
			},
			Message: &ai.AssistantMessage{
				Role:         "assistant",
				Content:      blocks,
				StopReason:   stopReason,
				ErrorMessage: errMsg,
			},
		},
		Children: children,
	}
}

func ttselToolResultNode(id string, parentId *string, toolCallId, toolName, output string, children ...core.SessionTreeNode) core.SessionTreeNode {
	return core.SessionTreeNode{
		Entry: &core.SessionMessageEntry{
			SessionEntryBase: core.SessionEntryBase{
				Type:      "message",
				Id:        id,
				ParentId:  parentId,
				Timestamp: "2026-01-15T10:02:00.000Z",
			},
			Message: &ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallId: toolCallId,
				ToolName:   toolName,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: output}},
			},
		},
		Children: children,
	}
}

func ttselStripAll(lines []string) string {
	stripped := make([]string, len(lines))
	for i, l := range lines {
		stripped[i] = utils.StripAnsi(l)
	}
	return strings.Join(stripped, "\n")
}

func TestTreeSelectorComponent(t *testing.T) {
	ttselSetupEnv(t)

	t.Run("hides tool-call-only assistant and usage entries by default, prioritizes active branch", func(t *testing.T) {
		argsObj := jsonx.NewObject()
		argsObj.Set("path", "/tmp/demo.go")
		argsObj.Set("offset", 10)
		argsObj.Set("limit", 20)

		leafId := "u2"
		tree := []core.SessionTreeNode{
			ttselUserNode("u1", nil, "first prompt", nil,
				ttselAssistantNode("a_tool", ttselStrPtr("u1"),
					ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "read", Arguments: argsObj}},
					ai.StopReasonToolUse, nil,
					ttselToolResultNode("tr1", ttselStrPtr("a_tool"), "tc1", "read", "ok",
						ttselAssistantNode("a1", ttselStrPtr("tr1"),
							ai.ContentList{&ai.TextContent{Type: "text", Text: "non-active answer"}},
							ai.StopReasonStop, nil,
						),
					),
				),
				core.SessionTreeNode{
					Entry: &core.UsageEntry{
						SessionEntryBase: core.SessionEntryBase{Type: "usage", Id: "use1", ParentId: ttselStrPtr("u1")},
					},
				},
				ttselUserNode("u2", ttselStrPtr("u1"), "active question", nil),
			),
		}

		comp := NewTreeSelectorComponent(tree, &leafId, 40, nil, nil, nil, nil)
		defer comp.Dispose()

		rendered := ttselStripAll(comp.GetTreeList().Render(120))
		if strings.Contains(rendered, "use1") {
			t.Fatalf("expected usage entry hidden, got:\n%s", rendered)
		}
		if !strings.Contains(rendered, "[read: /tmp/demo.go:10-29]") {
			t.Fatalf("expected formatted tool call line range, got:\n%s", rendered)
		}
		if !strings.Contains(rendered, "› ") || comp.GetTreeList().GetSelectedNode().Entry.GetId() != "u2" {
			t.Fatalf("expected active leaf u2 selected, got %v", comp.GetTreeList().GetSelectedNode())
		}
		// Active child branch (u2) appears before non-active child branch (tr1)
		idxU2 := strings.Index(rendered, "active question")
		idxA1 := strings.Index(rendered, "non-active answer")
		if idxU2 < 0 || idxA1 < 0 || idxU2 > idxA1 {
			t.Fatalf("expected active branch u2 before non-active a1, got:\n%s", rendered)
		}
	})

	t.Run("shows tool-call-only assistant when it is the current leaf, and displays aborted/error assistants", func(t *testing.T) {
		leafId := "a_leaf"
		tree := []core.SessionTreeNode{
			ttselUserNode("u1", nil, "start", nil,
				ttselAssistantNode("a_leaf", ttselStrPtr("u1"), ai.ContentList{}, ai.StopReasonToolUse, nil),
				ttselAssistantNode("a_aborted", ttselStrPtr("u1"), ai.ContentList{}, ai.StopReasonAborted, nil),
				ttselAssistantNode("a_error", ttselStrPtr("u1"), ai.ContentList{}, ai.StopReasonError, ttselStrPtr("boom failed")),
			),
		}
		tl := NewTreeList(tree, &leafId, 20, nil)
		out := ttselStripAll(tl.Render(120))
		if !strings.Contains(out, "(no content)") {
			t.Fatalf("expected leaf empty assistant shown as (no content), got:\n%s", out)
		}
		if !strings.Contains(out, "(aborted)") {
			t.Fatalf("expected aborted assistant shown, got:\n%s", out)
		}
		if !strings.Contains(out, "boom failed") {
			t.Fatalf("expected error assistant shown, got:\n%s", out)
		}
	})

	t.Run("cycles and toggles filter modes via keybindings", func(t *testing.T) {
		tree := []core.SessionTreeNode{
			ttselUserNode("u1", nil, "hello user", ttselStrPtr("bookmark"),
				ttselToolResultNode("tr1", ttselStrPtr("u1"), "tc1", "ls", "files",
					core.SessionTreeNode{
						Entry: &core.ModelChangeEntry{
							SessionEntryBase: core.SessionEntryBase{Type: "model_change", Id: "mc1", ParentId: ttselStrPtr("tr1")},
							Provider:         "anthropic",
							ModelId:          "claude-sonnet",
						},
					},
				),
			),
		}

		tl := NewTreeList(tree, ttselStrPtr("u1"), 20, nil)
		if tl.GetFilterMode() != core.TreeFilterModeDefault {
			t.Fatalf("expected default mode, got %q", tl.GetFilterMode())
		}
		// Ctrl+O cycles default -> no-tools -> user-only -> labeled-only -> all -> default
		tl.HandleInput("\x0f") // ctrl+o
		if tl.GetFilterMode() != core.TreeFilterModeNoTools {
			t.Fatalf("expected no-tools, got %q", tl.GetFilterMode())
		}
		if out := ttselStripAll(tl.Render(100)); strings.Contains(out, "[ls]") {
			t.Fatalf("expected no-tools to hide tool result, got:\n%s", out)
		}

		tl.HandleInput("\x0f") // user-only
		if tl.GetFilterMode() != core.TreeFilterModeUserOnly {
			t.Fatalf("expected user-only, got %q", tl.GetFilterMode())
		}
		tl.HandleInput("\x0f") // labeled-only
		if tl.GetFilterMode() != core.TreeFilterModeLabeledOnly {
			t.Fatalf("expected labeled-only, got %q", tl.GetFilterMode())
		}
		if out := ttselStripAll(tl.Render(100)); !strings.Contains(out, "[bookmark]") {
			t.Fatalf("expected labeled entry shown in labeled-only, got:\n%s", out)
		}

		tl.HandleInput("\x0f") // all
		if tl.GetFilterMode() != core.TreeFilterModeAll {
			t.Fatalf("expected all, got %q", tl.GetFilterMode())
		}
		if out := ttselStripAll(tl.Render(100)); !strings.Contains(out, "[model: claude-sonnet]") {
			t.Fatalf("expected settings entries shown in all mode, got:\n%s", out)
		}

		// Toggle ctrl+t toggles between no-tools and default
		tl.HandleInput("\x14") // ctrl+t
		if tl.GetFilterMode() != core.TreeFilterModeNoTools {
			t.Fatalf("expected ctrl+t -> no-tools, got %q", tl.GetFilterMode())
		}
		tl.HandleInput("\x14") // ctrl+t again -> default
		if tl.GetFilterMode() != core.TreeFilterModeDefault {
			t.Fatalf("expected second ctrl+t -> default, got %q", tl.GetFilterMode())
		}
	})

	t.Run("folds and unfolds branch subtrees and tracks markers", func(t *testing.T) {
		tree := []core.SessionTreeNode{
			ttselUserNode("root", nil, "root msg", nil,
				ttselUserNode("b1", ttselStrPtr("root"), "branch one", nil,
					ttselUserNode("b1c", ttselStrPtr("b1"), "branch one child", nil),
				),
				ttselUserNode("b2", ttselStrPtr("root"), "branch two", nil),
			),
		}
		tl := NewTreeList(tree, ttselStrPtr("b2"), 20, ttselStrPtr("b1"))
		if tl.GetSelectedNode().Entry.GetId() != "b1" {
			t.Fatalf("expected b1 selected, got %s", tl.GetSelectedNode().Entry.GetId())
		}
		// Fold b1 via Alt+Left (app.tree.foldOrUp)
		tl.HandleInput("\x1b[1;3D")
		outFolded := ttselStripAll(tl.Render(100))
		if strings.Contains(outFolded, "branch one child") {
			t.Fatalf("expected b1c hidden when b1 folded, got:\n%s", outFolded)
		}
		if !strings.Contains(outFolded, "⊞") {
			t.Fatalf("expected ⊞ fold marker, got:\n%s", outFolded)
		}

		// Unfold b1 via Alt+Right (app.tree.unfoldOrDown)
		tl.HandleInput("\x1b[1;3C")
		outUnfolded := ttselStripAll(tl.Render(100))
		if !strings.Contains(outUnfolded, "branch one child") {
			t.Fatalf("expected b1c restored after unfold, got:\n%s", outUnfolded)
		}
	})

	t.Run("handles inline label editing, timestamps, search clear vs cancel, and copy", func(t *testing.T) {
		var labelChanges []string
		var canceled int
		var selected string
		var copied []*string

		tree := []core.SessionTreeNode{
			ttselUserNode("u1", nil, "first item alpha", nil,
				ttselAssistantNode("a1", ttselStrPtr("u1"), ai.ContentList{&ai.TextContent{Type: "text", Text: "second item beta"}}, ai.StopReasonStop, nil),
			),
		}

		comp := NewTreeSelectorComponent(
			tree,
			ttselStrPtr("u1"),
			30,
			func(id string) { selected = id },
			func() { canceled++ },
			func(id string, label *string) {
				val := "<nil>"
				if label != nil {
					val = *label
				}
				labelChanges = append(labelChanges, id+"="+val)
			},
			ttselStrPtr("a1"),
		)
		defer comp.Dispose()
		comp.OnCopy = func(text *string) {
			copied = append(copied, text)
		}
		comp.SetFocused(true)

		if !comp.CapturesViewportInput() {
			t.Fatalf("expected CapturesViewportInput true initially")
		}

		// Copy selected assistant message (Ctrl+X)
		comp.HandleInput("\x18")
		if len(copied) != 1 || copied[0] == nil || *copied[0] != "second item beta" {
			t.Fatalf("expected copied text, got %v", copied)
		}

		// Edit label via Shift+L ("L")
		comp.HandleInput("L")
		if comp.CapturesViewportInput() {
			t.Fatalf("expected CapturesViewportInput false while label input active")
		}
		for _, r := range "important" {
			comp.HandleInput(string(r))
		}
		comp.HandleInput("\r")
		if !comp.CapturesViewportInput() {
			t.Fatalf("expected CapturesViewportInput true after saving label")
		}
		if len(labelChanges) != 1 || labelChanges[0] != "a1=important" {
			t.Fatalf("expected label change callback, got %v", labelChanges)
		}
		// Toggle label timestamps via Shift+T ("T")
		comp.HandleInput("T")
		rendered := ttselStripAll(comp.Render(100))
		if !strings.Contains(rendered, "[important]") || !strings.Contains(rendered, "[timestamps]") {
			t.Fatalf("expected label and [timestamps] indicator, got:\n%s", rendered)
		}

		// Search filters entries; Esc clears query first without cancelling selector
		for _, r := range "alpha" {
			comp.HandleInput(string(r))
		}
		if sel := comp.GetTreeList().GetSelectedNode(); sel == nil || sel.Entry.GetId() != "u1" {
			t.Fatalf("expected search for alpha to narrow to u1, got %v", sel)
		}
		comp.HandleInput("\x1b") // Esc clears search query
		if canceled != 0 {
			t.Fatalf("expected Esc with active query not to cancel selector")
		}
		if comp.GetTreeList().GetSearchQuery() != "" {
			t.Fatalf("expected search query cleared")
		}

		// Confirm selection with Enter
		comp.HandleInput("\r")
		if selected != "u1" {
			t.Fatalf("expected u1 selected on Enter, got %q", selected)
		}

		// Second Esc when query is empty cancels
		comp.HandleInput("\x1b")
		if canceled != 1 {
			t.Fatalf("expected cancel callback on Esc with empty query")
		}
	})

	t.Run("renders all entry types, formatToolCall variants, and clips narrow horizontal viewports", func(t *testing.T) {
		deepArgs := jsonx.NewObject()
		deepArgs.Set("command", strings.Repeat("x", 80))

		// Build deeply nested tree (16 levels of branches)
		leaf := ttselUserNode("leaf", ttselStrPtr("b15"), "deep user content beyond horizontal pan", nil)
		cur := leaf
		for i := 15; i >= 1; i-- {
			id := fmt.Sprintf("b%d", i)
			parent := "root"
			if i > 1 {
				parent = fmt.Sprintf("b%d", i-1)
			}
			sibling := ttselUserNode(fmt.Sprintf("sib%d", i), &parent, "sib", nil)
			branch := ttselUserNode(id, &parent, fmt.Sprintf("node %d", i), nil, cur)
			if i == 1 {
				cur = ttselUserNode("root", nil, "root", nil, branch, sibling)
			} else {
				// Attach sibling at each level via parent on previous iteration
				cur = core.SessionTreeNode{
					Entry:    branch.Entry,
					Children: []core.SessionTreeNode{cur, sibling},
				}
			}
		}

		tl := NewTreeList([]core.SessionTreeNode{cur}, ttselStrPtr("leaf"), 20, ttselStrPtr("leaf"))
		lines := tl.Render(36)
		for _, line := range lines {
			if w := tui.VisibleWidth(line); w > 36 {
				t.Fatalf("rendered line width %d exceeded viewport 36: %q", w, utils.StripAnsi(line))
			}
		}
		out := ttselStripAll(lines)
		if !strings.Contains(out, "user:") && !strings.Contains(out, "deep") {
			t.Fatalf("expected horizontal viewport pan to preserve anchor content, got:\n%s", out)
		}

		// Verify custom/compaction/branch/context entries in "all" mode
		various := []core.SessionTreeNode{
			{
				Entry: &core.SessionMessageEntry{
					SessionEntryBase: core.SessionEntryBase{Type: "message", Id: "m_bash"},
					Message:          &core.BashExecutionMessage{Role: "bashExecution", Command: "ls -la"},
				},
			},
			{
				Entry: &core.CustomMessageEntry{
					SessionEntryBase: core.SessionEntryBase{Type: "custom_message", Id: "cm1"},
					CustomType:       "ext",
					Content:          ai.UserMessageContent{Text: ttselStrPtr("hello ext")},
				},
			},
			{
				Entry: &core.CompactionEntry{
					SessionEntryBase: core.SessionEntryBase{Type: "compaction", Id: "cmp1"},
					Summary:          "compacted summary",
					TokensBefore:     45200,
				},
			},
			{
				Entry: &core.BranchSummaryEntry{
					SessionEntryBase: core.SessionEntryBase{Type: "branch_summary", Id: "bs1"},
					Summary:          "branch recapped",
				},
			},
			{
				Entry: &core.ContextEditEntry{
					SessionEntryBase: core.SessionEntryBase{Type: "context_edit", Id: "ce1"},
					TargetId:         "u1",
				},
			},
			{
				Entry: &core.SessionInfoEntry{
					SessionEntryBase: core.SessionEntryBase{Type: "session_info", Id: "si1"},
					Name:             nil,
				},
			},
		}
		tlAll := NewTreeList(various, nil, 20, nil, core.TreeFilterModeAll)
		outAll := ttselStripAll(tlAll.Render(120))
		for _, want := range []string{"[bash]: ls -la", "[ext]: hello ext", "[compaction: 45k tokens]", "[branch summary]: branch recapped", "[context omit: u1]", "[title: empty]"} {
			if !strings.Contains(outAll, want) {
				t.Fatalf("expected %q in rendered all-mode output, got:\n%s", want, outAll)
			}
		}

		if got := tselFormatToolCall("bash", deepArgs); !strings.HasSuffix(got, "...]") {
			t.Fatalf("expected truncated bash tool call, got %q", got)
		}
	})

	t.Run("cancels empty tree after delay unless disposed", func(t *testing.T) {
		var fired atomic.Int32
		comp := NewTreeSelectorComponent(nil, nil, 20, nil, func() { fired.Add(1) }, nil, nil)
		comp.Dispose()
		time.Sleep(150 * time.Millisecond)
		if fired.Load() != 0 {
			t.Fatalf("expected disposed empty TreeSelectorComponent not to fire onCancel")
		}

		comp2 := NewTreeSelectorComponent(nil, nil, 20, nil, func() { fired.Add(1) }, nil, nil)
		defer comp2.Dispose()
		time.Sleep(160 * time.Millisecond)
		if fired.Load() != 1 {
			t.Fatalf("expected non-disposed empty TreeSelectorComponent to auto-cancel once, got %d", fired.Load())
		}
	})
}
