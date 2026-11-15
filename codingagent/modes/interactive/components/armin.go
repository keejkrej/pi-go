// Ported from packages/coding-agent/src/modes/interactive/components/armin.ts (pi v1.5.2).

package components

import (
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// XBM image: 31x36 pixels, LSB first, 1=background, 0=foreground.
const (
	ArminWidth  = 31
	ArminHeight = 36

	arminBytesPerRow   = (ArminWidth + 7) / 8
	arminDisplayHeight = (ArminHeight + 1) / 2 // Half-block rendering
)

var arminBits = [...]byte{
	0xff, 0xff, 0xff, 0x7f, 0xff, 0xf0, 0xff, 0x7f, 0xff, 0xed, 0xff, 0x7f, 0xff, 0xdb, 0xff, 0x7f, 0xff, 0xb7, 0xff,
	0x7f, 0xff, 0x77, 0xfe, 0x7f, 0x3f, 0xf8, 0xfe, 0x7f, 0xdf, 0xff, 0xfe, 0x7f, 0xdf, 0x3f, 0xfc, 0x7f, 0x9f, 0xc3,
	0xfb, 0x7f, 0x6f, 0xfc, 0xf4, 0x7f, 0xf7, 0x0f, 0xf7, 0x7f, 0xf7, 0xff, 0xf7, 0x7f, 0xf7, 0xff, 0xe3, 0x7f, 0xf7,
	0x07, 0xe8, 0x7f, 0xef, 0xf8, 0x67, 0x70, 0x0f, 0xff, 0xbb, 0x6f, 0xf1, 0x00, 0xd0, 0x5b, 0xfd, 0x3f, 0xec, 0x53,
	0xc1, 0xff, 0xef, 0x57, 0x9f, 0xfd, 0xee, 0x5f, 0x9f, 0xfc, 0xae, 0x5f, 0x1f, 0x78, 0xac, 0x5f, 0x3f, 0x00, 0x50,
	0x6c, 0x7f, 0x00, 0xdc, 0x77, 0xff, 0xc0, 0x3f, 0x78, 0xff, 0x01, 0xf8, 0x7f, 0xff, 0x03, 0x9c, 0x78, 0xff, 0x07,
	0x8c, 0x7c, 0xff, 0x0f, 0xce, 0x78, 0xff, 0xff, 0xcf, 0x7f, 0xff, 0xff, 0xcf, 0x78, 0xff, 0xff, 0xdf, 0x78, 0xff,
	0xff, 0xdf, 0x7d, 0xff, 0xff, 0x3f, 0x7e, 0xff, 0xff, 0xff, 0x7f,
}

type arminEffect string

const (
	arminEffectTypewriter arminEffect = "typewriter"
	arminEffectScanline   arminEffect = "scanline"
	arminEffectRain       arminEffect = "rain"
	arminEffectFade       arminEffect = "fade"
	arminEffectCrt        arminEffect = "crt"
	arminEffectGlitch     arminEffect = "glitch"
	arminEffectDissolve   arminEffect = "dissolve"
)

var arminEffects = [...]arminEffect{
	arminEffectTypewriter,
	arminEffectScanline,
	arminEffectRain,
	arminEffectFade,
	arminEffectCrt,
	arminEffectGlitch,
	arminEffectDissolve,
}

type arminCoord struct {
	row int
	x   int
}

type arminRainDrop struct {
	y       int
	settled int
}

type arminEffectState struct {
	pos          int
	row          int
	drops        []arminRainDrop
	positions    []arminCoord
	idx          int
	expansion    int
	phase        int
	glitchFrames int
}

// IsArminPixel returns whether the pixel at (x, y) in the Armin bitmap is foreground.
func IsArminPixel(x, y int) bool {
	if x < 0 || x >= ArminWidth || y < 0 || y >= ArminHeight {
		return false
	}
	byteIndex := y*arminBytesPerRow + x/8
	bitIndex := uint(x % 8)
	return ((arminBits[byteIndex] >> bitIndex) & 1) == 0
}

func arminGetChar(x, row int) string {
	upper := IsArminPixel(x, row*2)
	lower := IsArminPixel(x, row*2+1)
	if upper && lower {
		return "█"
	}
	if upper {
		return "▀"
	}
	if lower {
		return "▄"
	}
	return " "
}

func arminBuildFinalGrid() [][]string {
	grid := make([][]string, arminDisplayHeight)
	for row := range grid {
		line := make([]string, ArminWidth)
		for x := range line {
			line[x] = arminGetChar(x, row)
		}
		grid[row] = line
	}
	return grid
}

func arminCreateEmptyGrid() [][]string {
	grid := make([][]string, arminDisplayHeight)
	for row := range grid {
		line := make([]string, ArminWidth)
		for x := range line {
			line[x] = " "
		}
		grid[row] = line
	}
	return grid
}

// ArminComponent renders an animated half-block XBM portrait of Armin.
type ArminComponent struct {
	ui            tui.TUI
	mu            sync.Mutex
	stopCh        chan struct{}
	stopped       bool
	effect        arminEffect
	finalGrid     [][]string
	currentGrid   [][]string
	state         arminEffectState
	cachedLines   []string
	cachedWidth   int
	gridVersion   int
	cachedVersion int
}

// NewArminComponent creates an animated ArminComponent with a randomly selected reveal effect.
func NewArminComponent(ui tui.TUI) *ArminComponent {
	effect := arminEffects[rand.IntN(len(arminEffects))]
	return arminNewComponentWithEffect(ui, effect)
}

func arminNewComponentWithEffect(ui tui.TUI, effect arminEffect) *ArminComponent {
	c := &ArminComponent{
		ui:            ui,
		stopCh:        make(chan struct{}),
		effect:        effect,
		finalGrid:     arminBuildFinalGrid(),
		currentGrid:   arminCreateEmptyGrid(),
		cachedVersion: -1,
	}
	c.initEffect()
	c.startAnimation()
	return c
}

// Invalidate drops the cached rendered lines.
func (c *ArminComponent) Invalidate() {
	c.mu.Lock()
	c.cachedWidth = 0
	c.mu.Unlock()
}

// Render renders the current frame of the portrait followed by "ARMIN SAYS HI".
func (c *ArminComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if width == c.cachedWidth && c.cachedVersion == c.gridVersion {
		return slices.Clone(c.cachedLines)
	}

	const padding = 1
	availableWidth := max(0, width-padding)
	clipCols := min(availableWidth, ArminWidth)
	th := theme.CurrentTheme()

	lines := make([]string, 0, len(c.currentGrid)+1)
	for _, row := range c.currentGrid {
		clipped := strings.Join(row[:clipCols], "")
		padRight := max(0, width-padding-clipCols)
		lines = append(lines, " "+th.Fg(theme.FgAccent, clipped)+strings.Repeat(" ", padRight))
	}

	const message = "ARMIN SAYS HI"
	msgPadRight := max(0, width-padding-len(message))
	lines = append(lines, " "+th.Fg(theme.FgAccent, message)+strings.Repeat(" ", msgPadRight))

	c.cachedLines = lines
	c.cachedWidth = width
	c.cachedVersion = c.gridVersion

	return slices.Clone(lines)
}

func (c *ArminComponent) initEffect() {
	switch c.effect {
	case arminEffectTypewriter:
		c.state = arminEffectState{pos: 0}
	case arminEffectScanline:
		c.state = arminEffectState{row: 0}
	case arminEffectRain:
		drops := make([]arminRainDrop, ArminWidth)
		for i := range drops {
			drops[i] = arminRainDrop{
				y: -rand.IntN(arminDisplayHeight * 2),
			}
		}
		c.state = arminEffectState{drops: drops}
	case arminEffectFade:
		positions := make([]arminCoord, 0, arminDisplayHeight*ArminWidth)
		for row := range arminDisplayHeight {
			for x := range ArminWidth {
				positions = append(positions, arminCoord{row: row, x: x})
			}
		}
		rand.Shuffle(len(positions), func(i, j int) {
			positions[i], positions[j] = positions[j], positions[i]
		})
		c.state = arminEffectState{positions: positions}
	case arminEffectCrt:
		c.state = arminEffectState{expansion: 0}
	case arminEffectGlitch:
		c.state = arminEffectState{phase: 0, glitchFrames: 8}
	case arminEffectDissolve:
		chars := [...]string{" ", "░", "▒", "▓", "█", "▀", "▄"}
		for row := range arminDisplayHeight {
			for x := range ArminWidth {
				c.currentGrid[row][x] = chars[rand.IntN(len(chars))]
			}
		}
		positions := make([]arminCoord, 0, arminDisplayHeight*ArminWidth)
		for row := range arminDisplayHeight {
			for x := range ArminWidth {
				positions = append(positions, arminCoord{row: row, x: x})
			}
		}
		rand.Shuffle(len(positions), func(i, j int) {
			positions[i], positions[j] = positions[j], positions[i]
		})
		c.state = arminEffectState{positions: positions}
	}
}

func (c *ArminComponent) startAnimation() {
	fps := 30
	if c.effect == arminEffectGlitch {
		fps = 60
	}
	interval := time.Second / time.Duration(fps)
	stopCh := c.stopCh

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				c.mu.Lock()
				if c.stopped {
					c.mu.Unlock()
					return
				}
				done := c.tickEffect()
				c.gridVersion++
				if done {
					c.stopLocked()
				}
				ui := c.ui
				c.mu.Unlock()

				if ui != nil {
					ui.RequestRender(nil)
				}
				if done {
					return
				}
			}
		}
	}()
}

func (c *ArminComponent) stopLocked() {
	if !c.stopped {
		c.stopped = true
		close(c.stopCh)
	}
}

func (c *ArminComponent) tickEffect() bool {
	switch c.effect {
	case arminEffectTypewriter:
		return c.tickTypewriter()
	case arminEffectScanline:
		return c.tickScanline()
	case arminEffectRain:
		return c.tickRain()
	case arminEffectFade:
		return c.tickFade()
	case arminEffectCrt:
		return c.tickCrt()
	case arminEffectGlitch:
		return c.tickGlitch()
	case arminEffectDissolve:
		return c.tickDissolve()
	default:
		return true
	}
}

func (c *ArminComponent) tickTypewriter() bool {
	const pixelsPerFrame = 3
	for range pixelsPerFrame {
		row := c.state.pos / ArminWidth
		x := c.state.pos % ArminWidth
		if row >= arminDisplayHeight {
			return true
		}
		c.currentGrid[row][x] = c.finalGrid[row][x]
		c.state.pos++
	}
	return false
}

func (c *ArminComponent) tickScanline() bool {
	if c.state.row >= arminDisplayHeight {
		return true
	}
	copy(c.currentGrid[c.state.row], c.finalGrid[c.state.row])
	c.state.row++
	return false
}

func (c *ArminComponent) tickRain() bool {
	allSettled := true
	c.currentGrid = arminCreateEmptyGrid()

	for x := range ArminWidth {
		drop := &c.state.drops[x]
		for row := arminDisplayHeight - 1; row >= arminDisplayHeight-drop.settled; row-- {
			if row >= 0 {
				c.currentGrid[row][x] = c.finalGrid[row][x]
			}
		}

		if drop.settled >= arminDisplayHeight {
			continue
		}
		allSettled = false

		targetRow := -1
		for row := arminDisplayHeight - 1 - drop.settled; row >= 0; row-- {
			if c.finalGrid[row][x] != " " {
				targetRow = row
				break
			}
		}

		drop.y++
		if drop.y >= 0 && drop.y < arminDisplayHeight {
			if targetRow >= 0 && drop.y >= targetRow {
				drop.settled = arminDisplayHeight - targetRow
				drop.y = -rand.IntN(5) - 1
			} else {
				c.currentGrid[drop.y][x] = "▓"
			}
		}
	}

	return allSettled
}

func (c *ArminComponent) tickFade() bool {
	const pixelsPerFrame = 15
	for range pixelsPerFrame {
		if c.state.idx >= len(c.state.positions) {
			return true
		}
		pos := c.state.positions[c.state.idx]
		c.currentGrid[pos.row][pos.x] = c.finalGrid[pos.row][pos.x]
		c.state.idx++
	}
	return false
}

func (c *ArminComponent) tickCrt() bool {
	midRow := arminDisplayHeight / 2
	c.currentGrid = arminCreateEmptyGrid()

	top := midRow - c.state.expansion
	bottom := midRow + c.state.expansion
	for row := max(0, top); row <= min(arminDisplayHeight-1, bottom); row++ {
		copy(c.currentGrid[row], c.finalGrid[row])
	}

	c.state.expansion++
	return c.state.expansion > arminDisplayHeight
}

func (c *ArminComponent) tickGlitch() bool {
	if c.state.phase < c.state.glitchFrames {
		nextGrid := make([][]string, len(c.finalGrid))
		for i, row := range c.finalGrid {
			offset := rand.IntN(7) - 3
			glitchRow := slices.Clone(row)
			if rand.Float64() < 0.3 {
				split := offset
				if split < 0 {
					split = ArminWidth + offset
				}
				if split < 0 {
					split = 0
				} else if split > ArminWidth {
					split = ArminWidth
				}
				shifted := make([]string, 0, ArminWidth)
				shifted = append(shifted, glitchRow[split:]...)
				shifted = append(shifted, glitchRow[:split]...)
				nextGrid[i] = shifted[:ArminWidth]
				continue
			}
			if rand.Float64() < 0.2 {
				swapRow := rand.IntN(arminDisplayHeight)
				nextGrid[i] = slices.Clone(c.finalGrid[swapRow])
				continue
			}
			nextGrid[i] = glitchRow
		}
		c.currentGrid = nextGrid
		c.state.phase++
		return false
	}

	nextGrid := make([][]string, len(c.finalGrid))
	for i, row := range c.finalGrid {
		nextGrid[i] = slices.Clone(row)
	}
	c.currentGrid = nextGrid
	return true
}

func (c *ArminComponent) tickDissolve() bool {
	const pixelsPerFrame = 20
	for range pixelsPerFrame {
		if c.state.idx >= len(c.state.positions) {
			return true
		}
		pos := c.state.positions[c.state.idx]
		c.currentGrid[pos.row][pos.x] = c.finalGrid[pos.row][pos.x]
		c.state.idx++
	}
	return false
}

// Dispose stops the animation goroutine and cleans up resources.
func (c *ArminComponent) Dispose() {
	c.mu.Lock()
	c.stopLocked()
	c.mu.Unlock()
}
