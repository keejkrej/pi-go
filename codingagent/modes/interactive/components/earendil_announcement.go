// Ported from packages/coding-agent/src/modes/interactive/components/earendil-announcement.ts (pi v1.5.2).

package components

import (
	"encoding/base64"
	"os"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

const (
	eaBlogUrl       = "https://mariozechner.at/posts/2026-04-08-ive-sold-out/"
	eaImageFilename = "clankolas.png"
)

var (
	eaImageOnce   sync.Once
	eaCachedImage string
)

func eaLoadImageBase64() string {
	eaImageOnce.Do(func() {
		raw, err := os.ReadFile(utils.GetBundledInteractiveAssetPath(eaImageFilename))
		if err != nil {
			return
		}
		eaCachedImage = base64.StdEncoding.EncodeToString(raw)
	})
	return eaCachedImage
}

// EarendilAnnouncementComponent displays the bordered Earendil announcement banner and optional mascot image.
type EarendilAnnouncementComponent struct {
	*tui.Container
}

// NewEarendilAnnouncementComponent creates the announcement banner with the current theme colors.
func NewEarendilAnnouncementComponent() *EarendilAnnouncementComponent {
	th := theme.CurrentTheme()
	c := &EarendilAnnouncementComponent{
		Container: tui.NewContainer(),
	}
	accentBorder := func(text string) string { return th.Fg(theme.FgAccent, text) }

	c.AddChild(NewDynamicBorder(accentBorder))
	c.AddChild(tui.NewText(th.Bold(th.Fg(theme.FgAccent, "pi has joined Earendil")), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(th.Fg(theme.FgMuted, "Read the blog post:"), 1, 0, nil))
	c.AddChild(tui.NewText(th.Fg(theme.FgMdLink, eaBlogUrl), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))

	if imageBase64 := eaLoadImageBase64(); imageBase64 != "" {
		maxW := 56
		fn := eaImageFilename
		c.AddChild(tui.NewImage(
			imageBase64,
			"image/png",
			tui.ImageTheme{
				FallbackColor: func(text string) string { return th.Fg(theme.FgMuted, text) },
			},
			&tui.ImageOptions{
				MaxWidthCells: &maxW,
				Filename:      &fn,
			},
			nil,
		))
		c.AddChild(tui.NewSpacer(1))
	}

	c.AddChild(NewDynamicBorder(accentBorder))
	return c
}
