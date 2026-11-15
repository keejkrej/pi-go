package components

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tlgndlgMockTUI struct {
	tui.TUI
	mu      sync.Mutex
	renders int
}

func (m *tlgndlgMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func (m *tlgndlgMockTUI) Post(fn func()) {
	fn()
}

func tlgndlgBoolPtr(v bool) *bool { return &v }

func tlgndlgRender(d *LoginDialogComponent) string {
	return utils.StripAnsi(strings.Join(d.Render(120), "\n"))
}

func tlgndlgWaitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for async login dialog condition")
}

func TestLoginDialogComponent(t *testing.T) {
	device := ai.AuthEventDeviceCode{
		Type:            ai.AuthEventKindDeviceCode,
		UserCode:        "ABCD-EFGH",
		VerificationUri: "https://example.com/device",
	}

	setup := func(t *testing.T) (
		*[]string,
		*[]string,
		func() (*LoginDialogComponent, *[][]any),
	) {
		t.Helper()
		_, _ = theme.InitTheme("dark", false)
		origKb := tui.GetKeybindings()
		tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
		origCopy := lgndlgCopyToClipboard
		origOpen := lgndlgOpenBrowser
		origAuCopy := auCopyToClipboard

		var mu sync.Mutex
		var copied []string
		var opened []string
		copyErr := error(nil)
		_ = copyErr
		lgndlgCopyToClipboard = func(text string) error {
			mu.Lock()
			copied = append(copied, text)
			mu.Unlock()
			return nil
		}
		auCopyToClipboard = func(text string) error {
			mu.Lock()
			copied = append(copied, text)
			mu.Unlock()
			return nil
		}
		lgndlgOpenBrowser = func(target string) {
			mu.Lock()
			opened = append(opened, target)
			mu.Unlock()
		}

		t.Cleanup(func() {
			tui.SetKeybindings(origKb)
			lgndlgCopyToClipboard = origCopy
			lgndlgOpenBrowser = origOpen
			auCopyToClipboard = origAuCopy
		})

		createDialog := func() (*LoginDialogComponent, *[][]any) {
			var compMu sync.Mutex
			var completions [][]any
			dlg := NewLoginDialogComponent(&tlgndlgMockTUI{}, "custom-provider", func(success bool, msg *string) {
				compMu.Lock()
				var m any
				if msg != nil {
					m = *msg
				}
				completions = append(completions, []any{success, m})
				compMu.Unlock()
			})
			dlg.SetFocused(true)
			return dlg, &completions
		}
		return &copied, &opened, createDialog
	}

	t.Run("opens the verification page and copies the code once after confirmation", func(t *testing.T) {
		copied, opened, createDialog := setup(t)
		dialog, _ := createDialog()

		type res struct {
			val string
			err error
		}
		done := make(chan res, 1)
		go func() {
			v, err := dialog.ShowPrompt(context.Background(), "GitHub Enterprise URL/domain (blank for github.com)")
			done <- res{val: v, err: err}
		}()
		tlgndlgWaitUntil(t, func() bool {
			return strings.Contains(tlgndlgRender(dialog), "GitHub Enterprise")
		})
		dialog.HandleInput("\r")
		got := <-done
		if got.err != nil || got.val != "" {
			t.Fatalf("ShowPrompt = (%q, %v), want (\"\", nil)", got.val, got.err)
		}

		dialog.ShowDeviceCode(device)
		dialog.ShowWaiting("Waiting for authentication...")
		rendered := tlgndlgRender(dialog)
		if !strings.Contains(rendered, "to open browser and copy code") {
			t.Fatalf("missing confirm hint:\n%s", rendered)
		}
		if len(*opened) != 0 || len(*copied) != 0 {
			t.Fatalf("opened=%v copied=%v before confirm", *opened, *copied)
		}

		dialog.HandleInput("\r")
		tlgndlgWaitUntil(t, func() bool {
			return strings.Contains(tlgndlgRender(dialog), "Code copied to clipboard")
		})
		dialog.HandleInput("\r")

		if !slices.Equal(*opened, []string{device.VerificationUri}) {
			t.Fatalf("opened = %v, want [%s]", *opened, device.VerificationUri)
		}
		if !slices.Equal(*copied, []string{device.UserCode}) {
			t.Fatalf("copied = %v, want [%s]", *copied, device.UserCode)
		}
		renderedAfter := tlgndlgRender(dialog)
		for _, want := range []string{"Code copied to clipboard", "Enter code: " + device.UserCode, "Waiting for authentication..."} {
			if !strings.Contains(renderedAfter, want) {
				t.Fatalf("missing %q:\n%s", want, renderedAfter)
			}
		}
		if strings.Contains(renderedAfter, "to open browser and copy code") {
			t.Fatalf("confirm hint should be replaced:\n%s", renderedAfter)
		}
	})

	for _, tc := range []struct {
		openBrowserOnConfirm bool
		copyCodeOnConfirm    bool
	}{
		{false, false},
		{true, false},
		{false, true},
	} {
		t.Run("honors provider overrides", func(t *testing.T) {
			copied, opened, createDialog := setup(t)
			dialog, _ := createDialog()
			info := device
			info.OpenBrowserOnConfirm = tlgndlgBoolPtr(tc.openBrowserOnConfirm)
			info.CopyCodeOnConfirm = tlgndlgBoolPtr(tc.copyCodeOnConfirm)
			dialog.ShowDeviceCode(info)
			dialog.HandleInput("\r")
			if tc.copyCodeOnConfirm {
				tlgndlgWaitUntil(t, func() bool {
					return strings.Contains(tlgndlgRender(dialog), "Code copied to clipboard")
				})
			}
			wantOpen := 0
			if tc.openBrowserOnConfirm {
				wantOpen = 1
			}
			wantCopy := 0
			if tc.copyCodeOnConfirm {
				wantCopy = 1
			}
			if len(*opened) != wantOpen || len(*copied) != wantCopy {
				t.Fatalf("opened=%d (want %d), copied=%d (want %d)", len(*opened), wantOpen, len(*copied), wantCopy)
			}
			if dialog.Signal().Err() != nil {
				t.Fatal("signal should not be aborted")
			}
		})
	}

	t.Run("clears actions cleanly across transitions", func(t *testing.T) {
		copied, opened, createDialog := setup(t)
		dialog, _ := createDialog()

		dialog.ShowAuth("https://example.com/authorize")
		*opened = nil
		dialog.ShowDeviceCode(device)
		dialog.HandleInput("\x18") // ctrl+x copy
		if len(*copied) != 0 || len(*opened) != 0 {
			t.Fatalf("expected authUrl cleared after ShowDeviceCode: copied=%v opened=%v", *copied, *opened)
		}
		dialog.HandleInput("\r")
		tlgndlgWaitUntil(t, func() bool {
			return strings.Contains(tlgndlgRender(dialog), "Code copied to clipboard")
		})
		if len(*copied) != 1 || (*copied)[0] != device.UserCode {
			t.Fatalf("copied = %v, want [%s]", *copied, device.UserCode)
		}

		*copied = nil
		*opened = nil
		dialog.ShowDeviceCode(device)
		dialog.ShowAuth("https://example.com/authorize")
		*opened = nil
		dialog.HandleInput("\r")
		if len(*copied) != 0 || len(*opened) != 0 {
			t.Fatalf("expected deviceCode cleared after ShowAuth: copied=%v opened=%v", *copied, *opened)
		}
		dialog.HandleInput("\x18")
		if !slices.Equal(*copied, []string{"https://example.com/authorize"}) {
			t.Fatalf("copied = %v, want [https://example.com/authorize]", *copied)
		}

		*copied = nil
		*opened = nil
		dialog.ShowAuth("https://example.com/authorize")
		dialog.ShowDetails([]string{"Choose a login method"})
		dialog.HandleInput("\x18")
		if len(*copied) != 0 {
			t.Fatalf("expected authUrl cleared after ShowDetails: copied=%v", *copied)
		}
		dialog.ShowDeviceCode(device)
		dialog.ShowDetails([]string{"Choose a login method"})
		*opened = nil
		dialog.HandleInput("\r")
		if len(*copied) != 0 || len(*opened) != 0 {
			t.Fatalf("expected deviceCode cleared after ShowDetails: copied=%v opened=%v", *copied, *opened)
		}
	})

	t.Run("cancels without opening browser or copying clipboard", func(t *testing.T) {
		copied, opened, createDialog := setup(t)
		dialog, completions := createDialog()
		dialog.ShowDeviceCode(device)
		dialog.HandleInput("\x1b")
		dialog.HandleInput("\r")
		if dialog.Signal().Err() == nil {
			t.Fatal("expected aborted Signal()")
		}
		if len(*completions) != 1 || (*completions)[0][0] != false || (*completions)[0][1] != "Login cancelled" {
			t.Fatalf("completions = %v, want [[false Login cancelled]]", *completions)
		}
		if len(*opened) != 0 || len(*copied) != 0 {
			t.Fatalf("opened=%v copied=%v after cancel", *opened, *copied)
		}
	})

	t.Run("keeps manual login available when browser and clipboard fail", func(t *testing.T) {
		_, _, createDialog := setup(t)
		lgndlgOpenBrowser = func(string) { panic("Browser unavailable") }
		lgndlgCopyToClipboard = func(string) error { return errors.New("Clipboard unavailable") }
		dialog, completions := createDialog()
		dialog.ShowDeviceCode(device)
		dialog.ShowWaiting("Waiting for authentication...")
		dialog.HandleInput("\r")
		tlgndlgWaitUntil(t, func() bool {
			return strings.Contains(tlgndlgRender(dialog), "Copy code manually")
		})
		out := tlgndlgRender(dialog)
		for _, want := range []string{device.VerificationUri, "Enter code: " + device.UserCode, "Copy code manually", "Waiting for authentication..."} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q:\n%s", want, out)
			}
		}
		if dialog.Signal().Err() != nil || len(*completions) != 0 {
			t.Fatal("dialog should stay active after clipboard/browser error")
		}
	})

	t.Run("opens only http(s) URLs and sanitizes control characters", func(t *testing.T) {
		_, opened, createDialog := setup(t)
		dialog, _ := createDialog()

		dialog.ShowAuth("file:///Users/victim/Downloads/payload.app")
		if len(*opened) != 0 {
			t.Fatalf("unexpected openBrowser on file:// URL: %v", *opened)
		}
		dialog.ShowAuth("https://example.com/authorize")
		if !slices.Equal(*opened, []string{"https://example.com/authorize"}) {
			t.Fatalf("opened = %v, want [https://example.com/authorize]", *opened)
		}

		*opened = nil
		dialog.ShowDeviceCode(ai.AuthEventDeviceCode{
			Type:            ai.AuthEventKindDeviceCode,
			UserCode:        device.UserCode,
			VerificationUri: "https://example.com/device\x07\x1b]52;c;Zm9v\x07",
		})
		label := "Help"
		dialog.ShowInfo("Docs", []ai.AuthInfoLink{{Label: &label, Url: "https://example.com/help\x07\x1b]52;c;Zm9v\x07"}})
		raw := strings.Join(dialog.Render(120), "\n")
		if strings.Contains(raw, "\x1b]52") {
			t.Fatalf("raw output leaked OSC 52 sequence:\n%q", raw)
		}
		plain := tlgndlgRender(dialog)
		if !strings.Contains(plain, "https://example.com/device%07%1B]52;c;Zm9v") ||
			!strings.Contains(plain, "Help: https://example.com/help%07%1B]52;c;Zm9v") {
			t.Fatalf("unexpected sanitized URLs:\n%s", plain)
		}

		injected := "x\x07\x1b]52;c;Zm9v\x07"
		dialog.ShowAuth("file:///tmp/" + injected)
		if strings.Contains(strings.Join(dialog.Render(120), "\n"), "\x1b]52") {
			t.Fatal("auth non-web URL leaked OSC 52")
		}
		dialog.ShowDeviceCode(ai.AuthEventDeviceCode{
			Type:            ai.AuthEventKindDeviceCode,
			UserCode:        device.UserCode,
			VerificationUri: injected,
		})
		if strings.Contains(strings.Join(dialog.Render(120), "\n"), "\x1b]52") ||
			!strings.Contains(tlgndlgRender(dialog), "x ]52;c;Zm9v") {
			t.Fatalf("device non-web URL sanitization failed:\n%s", tlgndlgRender(dialog))
		}
		dialog.ShowInfo("Docs", []ai.AuthInfoLink{{Label: &label, Url: injected}})
		if strings.Contains(strings.Join(dialog.Render(120), "\n"), "\x1b]52") ||
			!strings.Contains(tlgndlgRender(dialog), "Help: x ]52;c;Zm9v") ||
			len(*opened) != 0 {
			t.Fatalf("info non-web URL sanitization failed:\n%s", tlgndlgRender(dialog))
		}
	})
}
