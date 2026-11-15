// Ported from packages/coding-agent/test/interactive-mode-extension-dialogs.test.ts and
// interactive-mode-extension-changelog.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

func timedWaitEditorOverlay(t *testing.T, mode *InteractiveMode, substr string) {
	t.Helper()
	tithWaitUntil(t, 3*time.Second, "editorContainer contains "+substr, func() bool {
		return strings.Contains(tithRenderOnUI(t, mode.Ui(), mode.editorContainer, 100), substr)
	})
}

func TestExtensionUIContextDialogsWidgetsHeaderFooterAndTheme(t *testing.T) {
	mf := tithNewModeFixture(t, nil, nil)
	mode := mf.Mode
	uiCtx := mode.imeuCreateExtensionUIContext()

	// 1. Select dialog -> navigate down once and choose "Beta".
	type selectRes struct {
		val *string
		err error
	}
	selCh := make(chan selectRes, 1)
	go func() {
		v, err := uiCtx.Select(context.Background(), "Pick target", []string{"Alpha", "Beta", "Gamma"})
		selCh <- selectRes{val: v, err: err}
	}()
	timedWaitEditorOverlay(t, mode, "Pick target")
	mf.Term.tithSendInput("\x1b[B")
	mf.Term.tithSendInput("\r")
	sr := <-selCh
	if sr.err != nil || sr.val == nil || *sr.val != "Beta" {
		t.Fatalf("Select expected Beta, got val=%v err=%v", sr.val, sr.err)
	}

	// 2. Confirm dialog -> select Yes (Enter).
	confCh := make(chan bool, 1)
	go func() {
		ok, err := uiCtx.Confirm(context.Background(), "Deploy release", "Proceed with deploy?")
		if err != nil {
			t.Errorf("Confirm error: %v", err)
		}
		confCh <- ok
	}()
	timedWaitEditorOverlay(t, mode, "Deploy release")
	mf.Term.tithSendInput("\r")
	if !<-confCh {
		t.Fatalf("expected Confirm to return true on Enter")
	}

	// 3. Input dialog -> type value and submit.
	inCh := make(chan *string, 1)
	go func() {
		v, err := uiCtx.Input(context.Background(), "Release tag", tithStrPtr("v1.0.0"))
		if err != nil {
			t.Errorf("Input error: %v", err)
		}
		inCh <- v
	}()
	timedWaitEditorOverlay(t, mode, "Release tag")
	mf.Term.tithTypeAndSubmit("v2.3.4")
	gotIn := <-inCh
	if gotIn == nil || *gotIn != "v2.3.4" {
		t.Fatalf("expected Input v2.3.4, got %v", gotIn)
	}

	// 4. Widgets above/below editor, custom header/footer, status, tools expanded, theme switching.
	uiCtx.SetWidgetLines("ext-top", []string{"TOP WIDGET LINE"}, &core.ExtensionWidgetOptions{Placement: core.WidgetPlacementAboveEditor})
	uiCtx.SetWidgetFactory("ext-bottom", func(_ tui.TUI, _ *theme.Theme) tui.Component {
		return &tithStubComponent{lines: []string{"BOTTOM WIDGET LINE"}}
	}, &core.ExtensionWidgetOptions{Placement: core.WidgetPlacementBelowEditor})
	uiCtx.SetHeader(func(_ tui.TUI, _ *theme.Theme) tui.Component {
		return &tithStubComponent{lines: []string{"CUSTOM EXTENSION HEADER"}}
	})
	uiCtx.SetFooter(func(_ tui.TUI, _ *theme.Theme, _ core.ReadonlyFooterDataProvider) tui.Component {
		return &tithStubComponent{lines: []string{"CUSTOM EXTENSION FOOTER"}}
	})
	stText := "indexing-complete"
	uiCtx.SetStatus("ext-stat", &stText)
	uiCtx.SetTitle("Custom Pi Title")
	uiCtx.SetToolsExpanded(true)
	if !uiCtx.GetToolsExpanded() {
		t.Fatalf("expected GetToolsExpanded() true after SetToolsExpanded(true)")
	}
	res := uiCtx.SetThemeByName("light")
	if !res.Success {
		t.Fatalf("expected SetThemeByName(light) success, got %+v", res)
	}

	tithDrainUI(t, mode)
	if above := tithRenderOnUI(t, mode.Ui(), mode.aboveEditorWidgetContainer, 100); !strings.Contains(above, "TOP WIDGET LINE") {
		t.Fatalf("expected top widget rendered:\n%s", above)
	}
	if below := tithRenderOnUI(t, mode.Ui(), mode.belowEditorWidgetContainer, 100); !strings.Contains(below, "BOTTOM WIDGET LINE") {
		t.Fatalf("expected bottom widget rendered:\n%s", below)
	}
	if hdr := tithRenderOnUI(t, mode.Ui(), mode.headerContainer, 100); !strings.Contains(hdr, "CUSTOM EXTENSION HEADER") {
		t.Fatalf("expected custom header rendered:\n%s", hdr)
	}
	if ftr := tithRenderOnUI(t, mode.Ui(), mode.customFooter, 100); !strings.Contains(ftr, "CUSTOM EXTENSION FOOTER") {
		t.Fatalf("expected custom footer rendered:\n%s", ftr)
	}

	// Reset extension UI restores default header/footer and clears custom widgets.
	_ = mode.imCallUI(context.Background(), func() {
		mode.imeuResetExtensionUI()
	})
	tithDrainUI(t, mode)
	if above := tithRenderOnUI(t, mode.Ui(), mode.aboveEditorWidgetContainer, 100); above != "" {
		t.Fatalf("expected cleared aboveEditorWidgetContainer after reset, got:\n%s", above)
	}
}
