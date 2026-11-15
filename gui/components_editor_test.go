package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEditorTypingSubmitNewlineFollowUpCancel(t *testing.T) {
	ed := NewEditor(EditorOptions{Label: "Prompt editor"})

	var submitted []EditorSubmitEvent
	var followUps []EditorSubmitEvent
	cancels := 0

	ed.SetOnSubmit(func(ev EditorSubmitEvent) { submitted = append(submitted, ev) })
	ed.SetOnFollowUp(func(ev EditorSubmitEvent) { followUps = append(followUps, ev) })
	ed.SetOnCancel(func() { cancels++ })

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(16).Children(func() {
			ed.View(c)
		})
	}, 600, 320)

	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click editor err: %v", err)
	}

	// Typing + Shift+Enter newline + Enter submit
	tt.Type("hello")
	tt.Key(ui.Shift, ui.KeyEnter)
	tt.Type("world")
	if got := ed.Text(); got != "hello\nworld" {
		t.Fatalf("after Shift+Enter text = %q, want hello\\nworld", got)
	}

	tt.Key(0, ui.KeyEnter)
	if len(submitted) != 1 || submitted[0].Text != "hello\nworld" {
		t.Fatalf("unexpected submitted events: %+v", submitted)
	}
	if ed.Text() != "" {
		t.Fatalf("expected cleared text after submit, got %q", ed.Text())
	}

	// Alt+Enter follow-up
	tt.Type("queued follow up")
	tt.Key(ui.Alt, ui.KeyEnter)
	if len(followUps) != 1 || followUps[0].Text != "queued follow up" {
		t.Fatalf("unexpected followUps: %+v", followUps)
	}

	// Escape cancel when autocomplete is closed
	tt.Key(0, ui.KeyEscape)
	if cancels != 1 {
		t.Fatalf("expected 1 Escape cancel, got %d", cancels)
	}
}

func TestEditorHistoryUndoKillRingAndBashMode(t *testing.T) {
	ed := NewEditor(EditorOptions{Label: "Prompt editor"})
	ed.AddToHistory("first prompt")
	ed.AddToHistory("second prompt")

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(16).Children(func() {
			ed.View(c)
		})
	}, 600, 320)

	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatal(err)
	}

	// Empty editor + Up browses history newest first
	tt.Key(0, ui.KeyUp)
	if ed.Text() != "second prompt" {
		t.Fatalf("expected second prompt, got %q", ed.Text())
	}
	tt.Key(0, ui.KeyUp)
	if ed.Text() != "first prompt" {
		t.Fatalf("expected first prompt, got %q", ed.Text())
	}
	tt.Key(0, ui.KeyDown)
	if ed.Text() != "second prompt" {
		t.Fatalf("expected second prompt on Down, got %q", ed.Text())
	}
	tt.Key(0, ui.KeyDown)
	if ed.Text() != "" {
		t.Fatalf("expected empty draft restored, got %q", ed.Text())
	}

	// Shell ! and !! mode indicators
	tt.Type("!ls -la")
	if !ed.BashMode() || ed.BashExcludedFromContext() {
		t.Fatal("expected BashMode=true, Excluded=false for !ls -la")
	}
	if !tt.HasText("BASH") {
		t.Fatalf("expected BASH badge in view, texts=%v", tt.Texts())
	}
	ed.SetText("!!pwd")
	tt.Frame()
	if !ed.BashMode() || !ed.BashExcludedFromContext() {
		t.Fatal("expected BashMode=true, Excluded=true for !!pwd")
	}
	if !tt.HasText("BASH (excluded from context)") {
		t.Fatalf("expected excluded bash badge, texts=%v", tt.Texts())
	}

	// Kill-ring Ctrl+W backward word delete + Ctrl+Y yank
	ed.SetText("alpha beta")
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyW)
	if ed.Text() != "alpha " {
		t.Fatalf("after Ctrl+W text = %q, want %q", ed.Text(), "alpha ")
	}
	tt.Key(ui.Ctrl, ui.KeyY)
	if ed.Text() != "alpha beta" {
		t.Fatalf("after Ctrl+Y yank text = %q, want %q", ed.Text(), "alpha beta")
	}

	// Undo / Redo
	if !ed.Undo() || ed.Text() != "alpha " {
		t.Fatalf("expected Undo to revert yank -> %q", ed.Text())
	}
	if !ed.Redo() || ed.Text() != "alpha beta" {
		t.Fatalf("expected Redo to restore yank -> %q", ed.Text())
	}
}

func TestEditorAttachmentsAndAutocomplete(t *testing.T) {
	dir := t.TempDir()
	sampleFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(sampleFile, []byte("hello notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := NewCombinedAutocompleteProvider([]SlashCommand{
		{Name: "help", Description: "Help dialog"},
		{Name: "clear", Description: "Clear conversation"},
	}, dir)

	ed := NewEditor(EditorOptions{Label: "Prompt editor"})
	ed.SetAutocompleteProvider(prov)
	ed.readClipboardPNG = func() ([]byte, error) {
		return []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, nil
	}

	var cancels int
	ed.SetOnCancel(func() { cancels++ })

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(16).Justify(ui.End).Children(func() {
			ed.View(c)
		})
	}, 640, 420)

	// Drop file onto editor box
	edBox, ok := tt.Find("Prompt editor")
	if !ok {
		t.Fatal("editor not found")
	}
	if !tt.DropFiles(edBox.X+5, edBox.Y+5, sampleFile) {
		t.Fatal("DropFiles returned false")
	}
	if len(ed.Attachments()) != 1 || ed.Attachments()[0].Name != "notes.txt" {
		t.Fatalf("unexpected dropped attachments: %+v", ed.Attachments())
	}
	if !tt.HasText("File: notes.txt") {
		t.Fatalf("expected attachment chip rendered, texts=%v", tt.Texts())
	}

	// Paste PNG image creates image chip
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyV)
	if len(ed.Attachments()) != 2 || ed.Attachments()[1].Kind != AttachmentImage {
		t.Fatalf("expected image chip appended via paste, got %+v", ed.Attachments())
	}

	// Remove first chip via Remove button click
	if err := tt.Click("Remove notes.txt"); err != nil {
		t.Fatalf("remove chip click err: %v", err)
	}
	if len(ed.Attachments()) != 1 || ed.Attachments()[0].Kind != AttachmentImage {
		t.Fatalf("expected notes.txt chip removed, got %+v", ed.Attachments())
	}

	// Typing "/" triggers slash autocomplete popup; Escape closes popup without OnCancel
	tt.Type("/he")
	if !ed.AutocompleteVisible() || !tt.HasText("Help dialog") {
		t.Fatalf("expected /help autocomplete visible, visible=%v texts=%v", ed.AutocompleteVisible(), tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if ed.AutocompleteVisible() || cancels != 0 {
		t.Fatalf("expected Esc to close popup only (vis=%v cancels=%d)", ed.AutocompleteVisible(), cancels)
	}

	// Re-open with Tab or typing @main
	ed.SetText("")
	tt.Frame()
	tt.Type("@main")
	if !ed.AutocompleteVisible() {
		t.Fatal("expected @main autocomplete popup open")
	}
	tt.Key(0, ui.KeyTab)
	if ed.Text() != "@main.go " {
		t.Fatalf("expected Tab completion -> @main.go , got %q", ed.Text())
	}
}
