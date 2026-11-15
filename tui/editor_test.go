package tui

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
)

func teditorDefaultTheme() EditorTheme {
	return EditorTheme{
		BorderColor: func(s string) string { return "\x1b[2m" + s + "\x1b[22m" },
		SelectList:  tslTheme(),
	}
}

func teditorIntPtr(v int) *int { return &v }

type teditorCustomOverrides struct {
	*Editor
	topBorderCalls int
	inputCalls     []string
}

func (o *teditorCustomOverrides) HandleInput(data string) {
	o.inputCalls = append(o.inputCalls, data)
	o.Editor.HandleInput(data)
}

func (o *teditorCustomOverrides) RenderTopBorder(width int, hiddenLineCount int) string {
	o.topBorderCalls++
	return strings.Repeat("=", width)
}

func (o *teditorCustomOverrides) RenderBottomBorder(width int, hiddenLineCount int) string {
	return strings.Repeat("=", width)
}

type teditorMockAutocompleteProvider struct {
	triggerChars []string
	getFn        func(ctx context.Context, lines []string, cursorLine int, cursorCol int, options *AutocompleteSuggestionsOptions) (*AutocompleteSuggestions, error)
}

func (p *teditorMockAutocompleteProvider) TriggerCharacters() []string { return p.triggerChars }
func (p *teditorMockAutocompleteProvider) SetTriggerCharacters(chars []string) {
	p.triggerChars = chars
}
func (p *teditorMockAutocompleteProvider) GetSuggestions(ctx context.Context, lines []string, cursorLine int, cursorCol int, options *AutocompleteSuggestionsOptions) (*AutocompleteSuggestions, error) {
	return p.getFn(ctx, lines, cursorLine, cursorCol, options)
}
func (p *teditorMockAutocompleteProvider) ApplyCompletion(lines []string, cursorLine int, cursorCol int, item AutocompleteItem, prefix string) AutocompleteApplyResult {
	var line string
	if cursorLine >= 0 && cursorLine < len(lines) {
		line = lines[cursorLine]
	}
	prefixStart := cursorCol - js.Len(prefix)
	if prefixStart < 0 {
		prefixStart = 0
	}
	before := js.Slice(line, 0, prefixStart)
	after := js.SliceFrom(line, cursorCol)
	newLines := slices.Clone(lines)
	if cursorLine >= len(newLines) && cursorLine >= 0 {
		for len(newLines) <= cursorLine {
			newLines = append(newLines, "")
		}
	}
	if cursorLine >= 0 {
		newLines[cursorLine] = before + item.Value + after
	}
	return AutocompleteApplyResult{
		Lines:      newLines,
		CursorLine: cursorLine,
		CursorCol:  prefixStart + js.Len(item.Value),
	}
}
func (p *teditorMockAutocompleteProvider) ShouldTriggerFileCompletion(lines []string, cursorLine int, cursorCol int) bool {
	return true
}

func TestEditorUnit_ConstructConfigFocusHistoryOverridesSnapshot(t *testing.T) {
	t.Run("options, padding, autocompleteMaxVisible, focus, viewport capture, and history", func(t *testing.T) {
		ed := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), &EditorOptions{
			PaddingX:               teditorIntPtr(2),
			AutocompleteMaxVisible: teditorIntPtr(8),
		})
		if ed.GetPaddingX() != 2 {
			t.Errorf("GetPaddingX() = %d, want 2", ed.GetPaddingX())
		}
		ed.SetPaddingX(1)
		if ed.GetPaddingX() != 1 {
			t.Errorf("GetPaddingX() after SetPaddingX(1) = %d, want 1", ed.GetPaddingX())
		}

		if ed.GetAutocompleteMaxVisible() != 8 {
			t.Errorf("GetAutocompleteMaxVisible() = %d, want 8", ed.GetAutocompleteMaxVisible())
		}
		ed.SetAutocompleteMaxVisible(12)
		if ed.GetAutocompleteMaxVisible() != 12 {
			t.Errorf("GetAutocompleteMaxVisible() after Set = %d, want 12", ed.GetAutocompleteMaxVisible())
		}

		if ed.Focused() {
			t.Error("new Editor should start unfocused")
		}
		ed.SetFocused(true)
		if !ed.Focused() {
			t.Error("Focused() = false after SetFocused(true)")
		}
		if ed.IsShowingAutocomplete() {
			t.Error("new Editor should not show autocomplete")
		}
		if ed.CapturesViewportInput() {
			t.Error("Editor without active autocomplete should not capture viewport input")
		}
		var _ Component = ed
		var _ Focusable = ed

		ed.AddToHistory("first command")
		ed.AddToHistory("first command") // duplicate ignored
		ed.AddToHistory("   ")           // whitespace-only ignored
		ed.AddToHistory("second command")
		if len(ed.history) != 2 || ed.history[0] != "second command" || ed.history[1] != "first command" {
			t.Fatalf("unexpected history: %#v", ed.history)
		}

		if got := ed.GetText(); got != "" {
			t.Errorf("initial GetText() = %q, want empty", got)
		}
		if got := ed.GetExpandedText(); got != "" {
			t.Errorf("initial GetExpandedText() = %q, want empty", got)
		}
		lines := ed.GetLines()
		if !reflect.DeepEqual(lines, []string{""}) {
			t.Errorf("initial GetLines() = %#v, want [\"\"]", lines)
		}
		lines[0] = "mutated"
		if ed.GetLines()[0] != "" {
			t.Fatal("GetLines() did not return a defensive copy")
		}
	})

	t.Run("RenderTopBorder and RenderBottomBorder without hidden lines and SetOverrides", func(t *testing.T) {
		ed := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		top := tmarkStripAnsi(ed.RenderTopBorder(10, 0))
		if top != strings.Repeat("─", 10) {
			t.Errorf("RenderTopBorder(10, 0) = %q", top)
		}
		bot := tmarkStripAnsi(ed.RenderBottomBorder(10, 0))
		if bot != strings.Repeat("─", 10) {
			t.Errorf("RenderBottomBorder(10, 0) = %q", bot)
		}

		custom := &teditorCustomOverrides{Editor: ed}
		ed.SetOverrides(custom)
		if ed.overrides != custom {
			t.Fatal("SetOverrides did not store custom overrides")
		}
	})

	t.Run("ceEditorSnapshot.Clone deep-copies lines and pastes", func(t *testing.T) {
		pastes := omap.NewMap[int, string]()
		pastes.Set(1, "pasted")
		snap := ceEditorSnapshot{
			state: ceEditorState{
				lines:      []string{"a", "b"},
				cursorLine: 1,
				cursorCol:  1,
			},
			pastes:       pastes,
			pasteCounter: 1,
		}
		cloned := snap.Clone()
		cloned.state.lines[0] = "changed"
		cloned.pastes.Set(1, "mutated")
		if snap.state.lines[0] != "a" {
			t.Fatal("Clone shared state.lines slice")
		}
		if v, _ := snap.pastes.Get(1); v != "pasted" {
			t.Fatal("Clone shared pastes map")
		}
	})
}

func TestEditor_EditingHistoryUndoAutocompleteAndPasteMarkers(t *testing.T) {
	t.Run("public state accessors and defensive copy", func(t *testing.T) {
		ed := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		if cur := ed.GetCursor(); cur != (EditorCursor{Line: 0, Col: 0}) {
			t.Fatalf("initial cursor = %#v", cur)
		}
		ed.HandleInput("a")
		ed.HandleInput("b")
		ed.HandleInput("c")
		if cur := ed.GetCursor(); cur != (EditorCursor{Line: 0, Col: 3}) {
			t.Fatalf("cursor after abc = %#v", cur)
		}
		ed.HandleInput("\x1b[D")
		if cur := ed.GetCursor(); cur != (EditorCursor{Line: 0, Col: 2}) {
			t.Fatalf("cursor after Left = %#v", cur)
		}

		ed.SetText("a\nb")
		lines := ed.GetLines()
		if !reflect.DeepEqual(lines, []string{"a", "b"}) {
			t.Fatalf("GetLines = %#v", lines)
		}
		lines[0] = "mutated"
		if !reflect.DeepEqual(ed.GetLines(), []string{"a", "b"}) {
			t.Fatal("GetLines shared internal slice")
		}
	})

	t.Run("backslash+enter newline workaround and Kitty CSI-u handling", func(t *testing.T) {
		ed := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		ed.HandleInput(`\`)
		if ed.GetText() != `\` {
			t.Fatalf("got %q, want backslash immediately", ed.GetText())
		}
		ed.HandleInput("\r")
		if ed.GetText() != "\n" {
			t.Fatalf("got %q, want newline after \\+Enter", ed.GetText())
		}

		ed2 := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		submitted := false
		ed2.OnSubmit = func(string) { submitted = true }
		ed2.HandleInput(`\`)
		ed2.HandleInput("x")
		ed2.HandleInput("\r")
		if !submitted {
			t.Fatal("expected Enter after \\x to submit")
		}

		ed3 := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		ed3.HandleInput("\x1b[99;9u") // unsupported modifier ignored
		if ed3.GetText() != "" {
			t.Fatalf("expected empty after unsupported CSI-u, got %q", ed3.GetText())
		}
		ed3.HandleInput("\x1b[69;2u") // shifted 'E'
		if ed3.GetText() != "E" {
			t.Fatalf("expected 'E', got %q", ed3.GetText())
		}
		ed3.HandleInput("\x1b[27;2;69~") // modifyOtherKeys shifted 'E'
		if ed3.GetText() != "EE" {
			t.Fatalf("expected 'EE', got %q", ed3.GetText())
		}
	})

	t.Run("undo coalesces words and restores spaces/newlines/backspace", func(t *testing.T) {
		ed := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		for _, ch := range "hello world" {
			ed.HandleInput(string(ch))
		}
		if ed.GetText() != "hello world" {
			t.Fatalf("text = %q", ed.GetText())
		}
		ed.HandleInput("\x1b[45;5u") // Ctrl+- undo
		if ed.GetText() != "hello" {
			t.Fatalf("after 1st undo = %q, want hello", ed.GetText())
		}
		ed.HandleInput("\x1b[45;5u")
		if ed.GetText() != "" {
			t.Fatalf("after 2nd undo = %q, want empty", ed.GetText())
		}
	})

	t.Run("autocomplete page navigation (#7629) and viewport capture", func(t *testing.T) {
		ed := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		var submitted string
		ed.OnSubmit = func(text string) { submitted = text }

		items := make([]AutocompleteItem, 12)
		for i := range items {
			items[i] = AutocompleteItem{
				Value: fmt.Sprintf("/command-%d", i),
				Label: fmt.Sprintf("command-%d", i),
			}
		}
		ed.SetAutocompleteProvider(&teditorMockAutocompleteProvider{
			getFn: func(context.Context, []string, int, int, *AutocompleteSuggestionsOptions) (*AutocompleteSuggestions, error) {
				return &AutocompleteSuggestions{Items: items, Prefix: "/"}, nil
			},
		})

		if ed.CapturesViewportInput() {
			t.Fatal("CapturesViewportInput before autocomplete = true")
		}
		ed.HandleInput("/")
		time.Sleep(50 * time.Millisecond)
		if !ed.CapturesViewportInput() {
			t.Fatal("CapturesViewportInput while showing autocomplete = false")
		}
		ed.HandleInput("\x1b[6~") // PageDown
		ed.HandleInput("\x1b[6~") // PageDown
		ed.HandleInput("\x1b[5~") // PageUp
		ed.HandleInput("\r")
		if submitted != "/command-5" {
			t.Fatalf("submitted = %q, want /command-5", submitted)
		}
	})

	t.Run("paste marker atomic navigation, deletion, and expansion", func(t *testing.T) {
		ed := NewEditor(&tcloMockTUI{}, teditorDefaultTheme(), nil)
		big := strings.TrimSuffix(strings.Repeat("line\n", 20), "\n")
		ed.HandleInput("A")
		ed.HandleInput("\x1b[200~" + big + "\x1b[201~")
		ed.HandleInput("B")

		markerRe := regexp.MustCompile(`\[paste #\d+ \+\d+ lines\]`)
		marker := markerRe.FindString(ed.GetText())
		if marker == "" {
			t.Fatalf("expected paste marker in %q", ed.GetText())
		}

		ed.HandleInput("\x01") // Ctrl+A
		ed.HandleInput("\x1b[C")
		if cur := ed.GetCursor(); cur != (EditorCursor{Line: 0, Col: 1}) {
			t.Fatalf("cursor after 1st Right = %#v, want {0 1}", cur)
		}
		ed.HandleInput("\x1b[C")
		if cur := ed.GetCursor(); cur != (EditorCursor{Line: 0, Col: 1 + len(marker)}) {
			t.Fatalf("cursor after jumping marker = %#v, want col %d", cur, 1+len(marker))
		}
		ed.HandleInput("\x7f") // Backspace deletes whole marker
		if ed.GetText() != "AB" {
			t.Fatalf("after atomic backspace = %q, want AB", ed.GetText())
		}
	})
}
