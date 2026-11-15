package gui

import "github.com/egoist/mygo/ui"

// RenderContextGauge draws Waku's circular context-window utilization arc gauge
// using ui.Path stroke trimming. Percent is 0-100; threshold colors shift from
// Gauge (<80%) to Warning (>=80%) to Error (>=95%).
func RenderContextGauge(
	c *ui.Context,
	percent float64,
	hasPercent bool,
	size float32,
	strokeWidth float32,
	th *GuiTheme,
) ui.Element {
	theme := ResolveGuiTheme(c, th)
	if size <= 0 {
		size = 13
	}
	if strokeWidth <= 0 {
		strokeWidth = 2.3
	}

	trackCol := theme.BorderStrong
	if trackCol.A == 0 {
		trackCol = theme.Border
	}
	ringCol := theme.Gauge
	if ringCol.A == 0 {
		ringCol = theme.Accent
	}
	if hasPercent {
		switch {
		case percent >= 95:
			ringCol = theme.Error
		case percent >= 80:
			ringCol = theme.Warning
		}
	}

	frac := float32(0)
	if hasPercent && percent > 0 {
		frac = float32(percent / 100.0)
		if frac > 1 {
			frac = 1
		}
		if frac < 0.02 {
			frac = 0.02
		}
	}

	return ui.Box(c).Size(size, size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		cx := r.X + r.W/2
		cy := r.Y + r.H/2
		rad := min(r.W, r.H)/2 - strokeWidth/2 - 0.5
		if rad <= 0.5 {
			return
		}
		var track ui.Path
		track.Circle(cx, cy, rad)
		p.StrokePath(&track, strokeWidth, trackCol)

		if frac > 0 {
			p.Transform(ui.Rotation(-90), cx, cy, func() {
				var arc ui.Path
				arc.Circle(cx, cy, rad).Trim(0, frac).Cap(ui.RoundCap)
				p.StrokePath(&arc, strokeWidth, ringCol)
			})
		}
	})
}
