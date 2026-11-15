// Ported from packages/coding-agent/src/modes/interactive/components/auth-url.ts (pi v1.5.2).

package components

import (
	"runtime"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

var auCopyToClipboard = utils.CopyToClipboard

// AuthUrlComponent displays an OAuth sign-in URL with clickable hyperlink and clipboard copy hint.
type AuthUrlComponent struct {
	*tui.Container
	url  string
	ui   tui.TUI
	hint *tui.Text
}

// NewAuthUrlComponent constructs an AuthUrlComponent for url and initializes its click/copy hint.
func NewAuthUrlComponent(ui tui.TUI, url string) *AuthUrlComponent {
	th := theme.CurrentTheme()
	c := &AuthUrlComponent{
		Container: tui.NewContainer(),
		url:       url,
		ui:        ui,
		hint:      tui.NewText("", 1, 0, nil),
	}
	c.AddChild(tui.NewText(th.Fg(theme.FgAccent, tui.Hyperlink(url, url)), 1, 0, nil))
	c.AddChild(c.hint)
	c.setHint(theme.AppKeyHint(th, core.KeybindingAppMessageCopy, "to copy"))
	return c
}

// Url returns the sign-in URL displayed by the component.
func (a *AuthUrlComponent) Url() string {
	return a.url
}

func (a *AuthUrlComponent) setHint(suffix string) {
	th := theme.CurrentTheme()
	clickHint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		clickHint = "Cmd+click to open"
	}
	a.hint.SetText(th.Fg(theme.FgDim, tui.Hyperlink(clickHint, a.url)) + " " + th.Fg(theme.FgDim, "•") + " " + suffix)
	if a.ui != nil {
		a.ui.RequestRender(nil)
	}
}

// Copy copies the URL to the clipboard and updates the hint line with success or error feedback.
func (a *AuthUrlComponent) Copy() error {
	th := theme.CurrentTheme()
	if err := auCopyToClipboard(a.url); err != nil {
		a.setHint(th.Fg(theme.FgError, err.Error()))
		return err
	}
	a.setHint(th.Fg(theme.FgSuccess, "Copied URL to clipboard"))
	return nil
}
