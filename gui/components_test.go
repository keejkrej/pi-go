package gui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestMarkdownViewStreamingAndStatic(t *testing.T) {
	mv := NewMarkdownView("## Heading\n", MarkdownConfig{
		Done: false,
	})
	var copiedCode, clickedLink string
	mv.SetOnCopyCode(func(lang, code string) { copiedCode = lang + ":" + code })
	mv.SetOnLink(func(url string) { clickedLink = url })
	_ = clickedLink

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(16).Children(func() {
			mv.View(c)
		})
	}, 600, 400)

	mv.AppendText("\nSome text and a [link](https://example.com).\n\n```go\nfmt.Println(\"hi\")\n```\n")
	tt.Frame()
	tt.Advance(400 * time.Millisecond)

	mv.SetDone(true)
	tt.Frame()

	if !tt.HasText("Heading") || !tt.HasText("Copy") {
		t.Fatalf("expected rendered Heading and Copy button, texts=%v", tt.Texts())
	}
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("click Copy failed: %v", err)
	}
	if !strings.HasPrefix(copiedCode, "go:") || !strings.Contains(copiedCode, `fmt.Println("hi")`) {
		t.Fatalf("unexpected OnCopyCode value: %q", copiedCode)
	}
	if gotClip := tt.Clipboard(); !strings.Contains(gotClip, `fmt.Println("hi")`) {
		t.Fatalf("expected copied code in clipboard, got %q", gotClip)
	}
}

func TestDiffViewerAndCodeViewer(t *testing.T) {
	dv := NewDiffViewer("line1\nold2\nline3\n", "line1\nnew2\nadded\nline3\n")
	dv.SetLanguageForFile("main.go")

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(10).Children(func() {
			dv.View(c)
		})
	}, 800, 500)

	added, deleted := dv.Stats()
	if added == 0 || deleted == 0 {
		t.Fatalf("expected non-zero diff stats, got +%d -%d", added, deleted)
	}
	if dv.Layout() != editor.DiffSplit {
		t.Fatalf("expected initial DiffSplit, got %v", dv.Layout())
	}
	if !tt.HasText("main.go") || !tt.HasText("Unified") {
		t.Fatalf("expected DiffViewer toolbar controls, texts=%v", tt.Texts())
	}
	if err := tt.Click("Unified"); err != nil {
		t.Fatalf("click Unified failed: %v", err)
	}
	if dv.Layout() != editor.DiffUnified {
		t.Fatalf("expected DiffUnified after toggle, got %v", dv.Layout())
	}

	patch := `--- a/foo.txt
+++ b/foo.txt
@@ -1,2 +1,2 @@
-old line
+new line
 same
`
	dvPatch, err := NewDiffViewerFromPatch(patch)
	if err != nil {
		t.Fatalf("NewDiffViewerFromPatch err: %v", err)
	}
	if len(dvPatch.Hunks()) != 1 || dvPatch.Filename() != "foo.txt" {
		t.Fatalf("unexpected patch state: hunks=%v name=%q", dvPatch.Hunks(), dvPatch.Filename())
	}

	cv := NewCodeViewer("hello.go", "package main\nfunc main() {}\n")
	ttCode := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(10).Children(func() {
			cv.View(c)
		})
	}, 600, 300)
	if !ttCode.HasText("hello.go") || !ttCode.HasText("Go") {
		t.Fatalf("expected CodeViewer header with hello.go and Go language, texts=%v", ttCode.Texts())
	}
	if err := ttCode.Click("Copy Code"); err != nil {
		t.Fatalf("Copy Code err: %v", err)
	}
	if !strings.Contains(ttCode.Clipboard(), "package main") {
		t.Fatalf("expected code copied to clipboard, got %q", ttCode.Clipboard())
	}
}

func TestSelectListAndSettingsList(t *testing.T) {
	sl := NewSelectList([]SelectItem{
		{Value: "sonnet", Label: "Claude Sonnet", Description: "Balanced"},
		{Value: "opus", Label: "Claude Opus", Description: "Deep reasoning"},
		{Value: "disabled", Label: "Disabled Model", Disabled: true},
	})
	sl.SetSearchable(true)
	var chosen SelectItem
	sl.SetOnSelect(func(it SelectItem) { chosen = it })

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() {
			sl.View(c)
		})
	}, 450, 360)

	if err := tt.Click("Filter..."); err != nil {
		t.Fatal(err)
	}
	tt.Type("opus")
	if len(sl.FilteredItems()) != 1 || sl.FilteredItems()[0].Value != "opus" {
		t.Fatalf("unexpected filtered items: %+v", sl.FilteredItems())
	}
	tt.Key(0, ui.KeyEnter)
	if chosen.Value != "opus" {
		t.Fatalf("expected opus selected via Enter, got %+v", chosen)
	}

	// Pointer click selection on full list
	sl.SetFilter("")
	tt.Frame()
	if err := tt.Click("Claude Sonnet"); err != nil {
		t.Fatal(err)
	}
	if chosen.Value != "sonnet" {
		t.Fatalf("expected sonnet on click, got %+v", chosen)
	}

	// SettingsList toggle & enum & search
	var changes []string
	var openedSub string
	settings := NewSettingsList([]SettingItem{
		{ID: "stream", Label: "Stream Responses", Kind: SettingToggle, CurrentValue: "true"},
		{ID: "thinking", Label: "Thinking Level", Kind: SettingEnum, CurrentValue: "low", Values: []string{"off", "low", "high"}},
		{ID: "keys", Label: "Keybindings", Kind: SettingSubmenu},
	})
	settings.SetEnableSearch(true)
	settings.SetOnChange(func(id, val string) { changes = append(changes, id+"="+val) })
	settings.SetOnOpenSubmenu(func(it SettingItem) { openedSub = it.ID })

	var cancelledSettings bool
	settings.SetOnCancel(func() { cancelledSettings = true })

	ttSet := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() {
			settings.View(c)
		})
	}, 500, 360)

	if err := ttSet.Click("Toggle Stream Responses"); err != nil {
		t.Fatalf("toggle switch click err: %v", err)
	}
	if err := ttSet.Click("Thinking Level: low"); err != nil {
		t.Fatalf("enum click err: %v", err)
	}
	if err := ttSet.Click("Open Keybindings"); err != nil {
		t.Fatalf("submenu click err: %v", err)
	}
	if len(changes) != 2 || changes[0] != "stream=false" || changes[1] != "thinking=high" || openedSub != "keys" {
		t.Fatalf("unexpected settings events changes=%v submenu=%q", changes, openedSub)
	}
	if err := ttSet.Click("Search settings"); err != nil {
		t.Fatalf("click settings search err: %v", err)
	}
	ttSet.Type("stream")
	if len(settings.FilteredItems()) != 1 || settings.FilteredItems()[0].ID != "stream" {
		t.Fatalf("expected filtered settings [stream], got %+v", settings.FilteredItems())
	}
	ttSet.Key(0, ui.KeyEscape)
	if !cancelledSettings {
		t.Fatal("expected Escape in settings search to trigger OnCancel")
	}
}

func TestSearchBarLoaderAndCollapsibleCard(t *testing.T) {
	sb := NewSearchBar()
	sb.Open()
	var lastQuery string
	var closed bool
	sb.SetOnQueryChange(func(q string) { lastQuery = q })
	sb.SetOnClose(func() { closed = true })
	sb.SetMatches(5)
	sb.Next()
	if sb.Current() != 1 {
		t.Fatalf("expected Current=1 after Next, got %d", sb.Current())
	}

	loader := NewCancellableLoader("Running tool...")
	var loaderCancelled bool
	loader.SetOnCancel(func() { loaderCancelled = true })

	card := NewCollapsibleCard("Read file", CardToolSuccess, false)
	card.Badge = "done"

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Gap(10).Children(func() {
			sb.View(c)
			loader.View(c)
			card.View(c, func(cc *ui.Context) {
				ui.Text(cc, "file contents body")
			})
		})
	}, 500, 340)

	if !tt.HasText("Running tool...") {
		t.Fatalf("expected loader text, texts=%v", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("click loader Cancel failed: %v", err)
	}
	if !loaderCancelled || !loader.Cancelled() || loader.Running() || loader.Context().Err() == nil {
		t.Fatal("expected CancellableLoader cancel state and context cancellation")
	}

	if tt.HasText("file contents body") {
		t.Fatal("expected collapsed card body hidden initially")
	}
	if err := tt.Click("Read file"); err != nil {
		t.Fatalf("click card trigger failed: %v", err)
	}
	tt.Advance(250 * time.Millisecond)
	if !card.IsOpen() || !tt.HasText("file contents body") {
		t.Fatalf("expected card opened and body visible, open=%v texts=%v", card.IsOpen(), tt.Texts())
	}

	sb.SetQuery("needle")
	if lastQuery != "needle" {
		t.Fatalf("expected OnQueryChange(needle), got %q", lastQuery)
	}
	sb.Close()
	if !closed || sb.IsOpen() {
		t.Fatal("expected SearchBar Close to fire OnClose")
	}
}

func TestScrollViewFollowEndScrollLockAndJumpToBottom(t *testing.T) {
	sv := NewScrollView()
	sv.SetKeyFunc(func(i int) any { return i })
	count := 60

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			sv.View(c, count, func(rc *ui.Context, idx int) {
				ui.Box(rc).FillWidth().Padding(8, 12).Children(func() {
					ui.Text(rc, fmt.Sprintf("Message #%02d", idx))
				})
			})
		})
	}, 480, 320)

	tt.Frame()
	if !sv.AtEnd() {
		t.Fatal("expected ScrollView with FollowEnd to start at end")
	}
	if tt.HasText("Scroll to bottom") {
		t.Fatal("did not expect scroll-to-bottom button while at bottom")
	}

	// Scroll upward away from bottom -> pauses follow-end & reveals centered 32x32 icon button
	tt.Move(240, 160)
	tt.Wheel(240, 160, 0, -400)
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if sv.AtEnd() || !sv.Locked() {
		t.Fatalf("expected scroll-up lock (atEnd=%v locked=%v)", sv.AtEnd(), sv.Locked())
	}
	if tt.HasText("↓") {
		t.Fatalf("did not expect duplicate arrow text on scroll-to-bottom button, texts=%v", tt.Texts())
	}
	rect, ok := tt.Find("Scroll to bottom")
	if !ok {
		t.Fatalf("expected floating Scroll to bottom button when scrolled up, texts=%v", tt.Texts())
	}
	if rect.W != 32 || rect.H != 32 || rect.X != 224 {
		t.Fatalf("Scroll to bottom rect = %+v, want centered 32x32 at X=224", rect)
	}

	// Clicking Scroll to bottom returns to end & hides button
	if err := tt.Click("Scroll to bottom"); err != nil {
		t.Fatalf("click Scroll to bottom failed: %v", err)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !sv.AtEnd() {
		t.Fatal("expected AtEnd() after clicking Jump to bottom")
	}
}
