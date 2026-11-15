// Ported from packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts (pi v1.5.2).

package components

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

// EasterEggKind identifies which 3D bitmap easter egg to play.
type EasterEggKind string

const (
	EasterEggKindPiLogo EasterEggKind = "pi-logo"
	EasterEggKindArmin  EasterEggKind = "armin"
)

// EasterEgg3dOptions configures which 3D easter egg to launch.
type EasterEgg3dOptions struct {
	Kind   EasterEggKind
	Column int
	Row    int
}

// EasterEgg3d is an alias for EasterEgg3dOptions for backwards compatibility with TS type naming.
type EasterEgg3d = EasterEgg3dOptions

type a3Rgb [3]float64

type a3ScreenCell struct {
	text  string
	width int
	fg    a3Rgb
	bg    *a3Rgb
	delay float64
	ink   int
	seed  uint32
}

type a3Star struct {
	glyph    string
	tint     a3Rgb
	strength float64
	phase    float64
	speed    float64
}

type a3Box struct {
	min   [3]float64
	max   [3]float64
	color a3Rgb
}

type a3Pose struct {
	centerX float64
	centerY float64
	scale   float64
	yaw     float64
	pitch   float64
	roll    float64
}

type a3Cell3 [3]int

type a3Block struct {
	home  a3Cell3
	color a3Rgb
}

type a3Model struct {
	columns        int
	rows           int
	blocks         []a3Block
	cameraDistance float64
	radius         float64
	widthShare     float64
	puzzleMoves    func(random float64) int
	origin         *a3Origin
}

type a3Origin struct {
	column int
	row    int
}

const (
	a3Depth      = 0.7
	a3FlyStart   = 0.1
	a3FlyDur     = 1.3
	a3StartScale = 0.1

	a3PuzzleStart  = a3FlyStart + a3FlyDur + 3.0
	a3PuzzleStep   = 0.3
	a3PuzzleSteps  = 12
	a3PuzzleReturn = 1.0
	a3PuzzleHold   = 0.6
	a3PuzzleCycle  = a3PuzzleStep*a3PuzzleSteps + a3PuzzleReturn + a3PuzzleHold

	a3FrameInterval = time.Second / 30
	a3DustDuration  = 0.35
	a3WaveSpread    = 0.55
	a3WaveJitter    = 0.12
	a3DissolveEnd   = a3FlyStart + a3WaveSpread + a3WaveJitter + a3DustDuration
	a3BgFade        = 0.5
	a3ExitDuration  = 1.1
	a3StarsStart    = 2.5
	a3StarsFade     = 2.0
	a3StarDensity   = 0.018

	a3LogoColorStep   = 5.0
	a3LightHaloStr    = 0.3
	a3HaloRadiusX     = 6
	a3HaloRadiusY     = 3
	a3HaloLevels      = 32.0
	a3StarAlphaLevels = 8.0
	a3StarFlickerLvls = 3.0
	a3HintFade        = 0.5
	a3SpinRamp        = 1.4
	a3SpinSpeed       = (math.Pi * 2) / a3PuzzleCycle
	a3SpinLinger      = 0.6
	a3FirstFrontView  = a3PuzzleStart + a3PuzzleStep*a3PuzzleSteps + a3PuzzleReturn + a3PuzzleHold/2
	a3TwoPi           = math.Pi * 2
)

var (
	a3Coral = a3Rgb{228, 138, 122}
	a3Blue  = a3Rgb{79, 142, 179}
	a3Gold  = a3Rgb{234, 182, 93}

	a3PiLogoPixels = [4]string{"ccc.", "b.c.", "bb.y", "b..y"}
	a3PiLogoColors = map[byte]a3Rgb{'c': a3Coral, 'b': a3Blue, 'y': a3Gold}

	a3PuzzleMoves = [10]a3Cell3{
		{1, 0, 0},
		{-1, 0, 0},
		{0, 1, 0},
		{0, -1, 0},
		{1, 0, 0},
		{-1, 0, 0},
		{0, 1, 0},
		{0, -1, 0},
		{0, 0, 1},
		{0, 0, -1},
	}

	a3StarTints = [4]a3Rgb{
		{255, 255, 255},
		{170, 195, 255},
		{255, 225, 170},
		{255, 190, 205},
	}

	a3DotBits = [8]uint8{0x01, 0x08, 0x02, 0x10, 0x04, 0x20, 0x40, 0x80}
	a3Braille = func() [256]string {
		var b [256]string
		for i := range b {
			b[i] = string(rune(0x2800 + i))
		}
		return b
	}()

	a3Light      = a3Normalize([3]float64{-0.45, -0.6, 0.75})
	a3HalfVector = a3Normalize([3]float64{a3Light[0], a3Light[1], a3Light[2] + 1})

	a3SpinAlignment = a3TwoPi - math.Mod(a3BaseSpinPhase(a3FirstFrontView), a3TwoPi)

	a3PlayingMu sync.Mutex
	a3Playing   bool
)

func a3CreateModel(
	columns, rows int,
	pixel func(column, row int) (a3Rgb, bool),
	cameraDistance, widthShare float64,
	origin *a3Origin,
	puzzleMoves func(random float64, blockCount int) int,
) *a3Model {
	var blocks []a3Block
	for row := range rows {
		for col := range columns {
			if color, ok := pixel(col, row); ok {
				blocks = append(blocks, a3Block{home: a3Cell3{col, row, 0}, color: color})
			}
		}
	}
	blockCount := len(blocks)
	return &a3Model{
		columns:        columns,
		rows:           rows,
		blocks:         blocks,
		cameraDistance: cameraDistance,
		radius:         math.Hypot(float64(columns)/2, math.Hypot(float64(rows)/2, 1+a3Depth/2)),
		widthShare:     widthShare,
		puzzleMoves: func(random float64) int {
			return puzzleMoves(random, blockCount)
		},
		origin: origin,
	}
}

func a3PiLogoModel(origin a3Origin) *a3Model {
	return a3CreateModel(
		4,
		4,
		func(col, row int) (a3Rgb, bool) {
			if row < 0 || row >= len(a3PiLogoPixels) || col < 0 || col >= len(a3PiLogoPixels[row]) {
				return a3Rgb{}, false
			}
			c, ok := a3PiLogoColors[a3PiLogoPixels[row][col]]
			return c, ok
		},
		10,
		0.35,
		&origin,
		func(random float64, _ int) int {
			if random < 0.4 {
				return 2
			}
			return 1
		},
	)
}

func a3ArminModel(color a3Rgb) *a3Model {
	return a3CreateModel(
		ArminWidth,
		ArminHeight,
		func(col, row int) (a3Rgb, bool) {
			if IsArminPixel(col, row) {
				return color, true
			}
			return a3Rgb{}, false
		},
		80,
		0.45,
		nil,
		func(random float64, blockCount int) int {
			return int(math.Round(float64(blockCount) * (0.03 + random*0.04)))
		},
	)
}

func a3ShuffleSteps(model *a3Model, cycle int) [][]a3Cell3 {
	cols, rows := model.columns, model.rows
	positions := make([]a3Cell3, len(model.blocks))
	for i, b := range model.blocks {
		positions[i] = b.home
	}
	steps := make([][]a3Cell3, 0, a3PuzzleSteps+1)
	steps = append(steps, positions)
	lastMoved := make(map[int]struct{})
	randomSeed := uint32(cycle*7919 + 1)
	next := func() float64 {
		v := a3Hash(randomSeed)
		randomSeed++
		return v
	}
	key := func(c a3Cell3) int {
		return ((c[2]+1)*rows+c[1])*cols + c[0]
	}

	type a3Candidate struct {
		block  int
		target a3Cell3
	}

	for range a3PuzzleSteps {
		occupied := make(map[int]struct{}, len(positions))
		for _, pos := range positions {
			occupied[key(pos)] = struct{}{}
		}
		nextPositions := slices.Clone(positions)
		moved := make(map[int]struct{})
		moveCount := model.puzzleMoves(next())
		for range moveCount {
			var candidates []a3Candidate
			for block, pos := range positions {
				if _, alreadyMoved := moved[block]; alreadyMoved {
					continue
				}
				for _, delta := range a3PuzzleMoves {
					target := a3Cell3{pos[0] + delta[0], pos[1] + delta[1], pos[2] + delta[2]}
					if target[0] < 0 || target[0] >= cols || target[1] < 0 || target[1] >= rows {
						continue
					}
					if target[2] < -1 || target[2] > 1 {
						continue
					}
					if _, isOcc := occupied[key(target)]; isOcc {
						continue
					}
					candidates = append(candidates, a3Candidate{block: block, target: target})
				}
			}
			var fresh []a3Candidate
			for _, c := range candidates {
				if _, wasLast := lastMoved[c.block]; !wasLast {
					fresh = append(fresh, c)
				}
			}
			pool := fresh
			if len(pool) == 0 {
				pool = candidates
			}
			if len(pool) == 0 {
				break
			}
			choice := pool[int(math.Floor(next()*float64(len(pool))))]
			occupied[key(choice.target)] = struct{}{}
			nextPositions[choice.block] = choice.target
			moved[choice.block] = struct{}{}
		}
		clear(lastMoved)
		for block := range moved {
			lastMoved[block] = struct{}{}
		}
		positions = nextPositions
		steps = append(steps, positions)
	}
	return steps
}

func a3Normalize(v [3]float64) [3]float64 {
	length := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
	return [3]float64{v[0] / length, v[1] / length, v[2] / length}
}

func a3Clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func a3Smooth(value float64) float64 {
	t := a3Clamp01(value)
	return t * t * t * (t*(t*6-15) + 10)
}

func a3EaseOut(value float64) float64 {
	t := 1 - a3Clamp01(value)
	return 1 - t*t*t
}

func a3Mix(a, b a3Rgb, amount float64) a3Rgb {
	return a3Rgb{
		a[0] + (b[0]-a[0])*amount,
		a[1] + (b[1]-a[1])*amount,
		a[2] + (b[2]-a[2])*amount,
	}
}

func a3Hash(value uint32) float64 {
	x := value ^ 0x9e3779b9
	x *= 0x85ebca6b
	x ^= x >> 13
	x *= 0xc2b2ae35
	x ^= x >> 16
	return float64(x) / 4294967296.0
}

func a3IsLight(c a3Rgb) bool {
	return 0.2126*c[0]+0.7152*c[1]+0.0722*c[2] > 128
}

func a3ToRgb(color tui.Color) a3Rgb {
	rgb := tui.ColorToRgb(color)
	return a3Rgb{rgb.R, rgb.G, rgb.B}
}

func a3GlyphInk(text string, seed float64) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(text)
	if size == len(text) {
		switch r {
		case '.', ',', ':', ';', '\'', '`', '-', '_', '·':
			return 2
		}
		if r >= 0x2500 && r <= 0x257f {
			return 3
		}
	}
	return 4 + int(math.Floor(seed*3))
}

func a3ParseScreen(lines []string, width int, foreground, background a3Rgb) [][]a3ScreenCell {
	palette := func(index int) a3Rgb {
		if index < 0 {
			index = 0
		} else if index > 255 {
			index = 255
		}
		c, err := tui.NewIndexedColor(index)
		if err != nil {
			return foreground
		}
		return a3ToRgb(c)
	}
	seg := tui.GetGraphemeSegmenter()
	result := make([][]a3ScreenCell, len(lines))

	for rowIdx, line := range lines {
		var cells []a3ScreenCell
		var fg *a3Rgb
		var bg *a3Rgb
		dim := false
		inverse := false

		applySgr := func(params string) {
			var codes []string
			if params == "" {
				codes = []string{"0"}
			} else {
				codes = strings.Split(params, ";")
			}
			for i := 0; i < len(codes); i++ {
				partStrs := strings.Split(codes[i], ":")
				parts := make([]int, len(partStrs))
				partsValid := make([]bool, len(partStrs))
				for idx, p := range partStrs {
					if v, err := strconv.Atoi(p); err == nil {
						parts[idx] = v
						partsValid[idx] = true
					}
				}
				code := 0
				if len(parts) > 0 && partsValid[0] {
					code = parts[0]
				}
				switch {
				case code == 38 || code == 48:
					var args []int
					if len(parts) > 1 {
						for idx := 1; idx < len(parts); idx++ {
							if partsValid[idx] {
								args = append(args, parts[idx])
							}
						}
					} else {
						kind := -1
						if i+1 < len(codes) {
							if v, err := strconv.Atoi(codes[i+1]); err == nil {
								kind = v
							}
						}
						count := 1
						if kind == 5 {
							count = 2
						} else if kind == 2 {
							count = 4
						}
						end := min(len(codes), i+1+count)
						for j := i + 1; j < end; j++ {
							if v, err := strconv.Atoi(codes[j]); err == nil {
								args = append(args, v)
							} else {
								args = append(args, -1)
							}
						}
						i += count
					}
					var color *a3Rgb
					if len(args) >= 2 && args[0] == 5 && args[1] >= 0 {
						c := palette(args[1])
						color = &c
					} else if len(args) >= 4 && args[0] == 2 && args[1] >= 0 && args[2] >= 0 && args[3] >= 0 {
						c := a3Rgb{float64(args[1]), float64(args[2]), float64(args[3])}
						color = &c
					}
					if color != nil {
						if code == 38 {
							fg = color
						} else {
							bg = color
						}
					}
				case code == 0:
					fg = nil
					bg = nil
					dim = false
					inverse = false
				case code == 2:
					dim = true
				case code == 22:
					dim = false
				case code == 7:
					inverse = true
				case code == 27:
					inverse = false
				case code >= 30 && code <= 37:
					c := palette(code - 30)
					fg = &c
				case code >= 90 && code <= 97:
					c := palette(code - 90 + 8)
					fg = &c
				case code == 39:
					fg = nil
				case code >= 40 && code <= 47:
					c := palette(code - 40)
					bg = &c
				case code >= 100 && code <= 107:
					c := palette(code - 100 + 8)
					bg = &c
				case code == 49:
					bg = nil
				}
			}
		}

		pushText := func(text string) {
			for _, item := range seg.Segment(text) {
				glyphWidth := tui.VisibleWidth(item.Segment)
				if glyphWidth == 0 || len(cells)+glyphWidth > width {
					continue
				}
				cellFg := foreground
				if fg != nil {
					cellFg = *fg
				}
				var cellBg *a3Rgb
				if bg != nil {
					bgCopy := *bg
					cellBg = &bgCopy
				}
				if inverse {
					if bg != nil {
						cellFg = *bg
					} else {
						cellFg = background
					}
					bgCopy := foreground
					if fg != nil {
						bgCopy = *fg
					}
					cellBg = &bgCopy
				}
				if dim {
					dimBg := background
					if cellBg != nil {
						dimBg = *cellBg
					}
					cellFg = a3Mix(cellFg, dimBg, 0.4)
				}
				cell := a3ScreenCell{
					text:  item.Segment,
					width: glyphWidth,
					fg:    cellFg,
					bg:    cellBg,
				}
				cells = append(cells, cell)
				if glyphWidth == 2 {
					cont := cell
					cont.text = ""
					cont.width = 0
					cells = append(cells, cont)
				}
			}
		}

		i := 0
		for i < len(line) {
			escOffset := strings.IndexByte(line[i:], '\x1b')
			if escOffset < 0 {
				pushText(line[i:])
				break
			}
			seqStart := i + escOffset
			if seqStart > i {
				pushText(line[i:seqStart])
			}
			if seqStart+1 >= len(line) {
				break
			}
			kind := line[seqStart+1]
			switch kind {
			case '[':
				end := seqStart + 2
				for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
					end++
				}
				if end < len(line) && line[end] == 'm' {
					applySgr(line[seqStart+2 : end])
				}
				i = end + 1
			case ']', '_', 'P', '^':
				rest := line[seqStart+2:]
				bell := strings.IndexByte(rest, '\x07')
				st := strings.Index(rest, "\x1b\\")
				if bell < 0 && st < 0 {
					i = len(line)
				} else if bell >= 0 && (st < 0 || bell < st) {
					i = seqStart + 2 + bell + 1
				} else {
					i = seqStart + 2 + st + 2
				}
			default:
				i = seqStart + 2
			}
		}
		result[rowIdx] = cells
	}
	return result
}

func a3Rotation(yaw, pitch, roll float64) [9]float64 {
	sy, cy := math.Sin(yaw), math.Cos(yaw)
	sx, cx := math.Sin(pitch), math.Cos(pitch)
	sz, cz := math.Sin(roll), math.Cos(roll)

	ry := [9]float64{cy, 0, sy, 0, 1, 0, -sy, 0, cy}
	rx := [9]float64{1, 0, 0, 0, cx, -sx, 0, sx, cx}
	rz := [9]float64{cz, -sz, 0, sz, cz, 0, 0, 0, 1}
	return a3Multiply(rz, a3Multiply(rx, ry))
}

func a3Multiply(a, b [9]float64) [9]float64 {
	var res [9]float64
	for row := range 3 {
		for col := range 3 {
			res[row*3+col] = a[row*3]*b[col] + a[row*3+1]*b[3+col] + a[row*3+2]*b[6+col]
		}
	}
	return res
}

func a3BaseSpinPhase(t float64) float64 {
	elapsed := t - a3FlyStart
	if elapsed <= 0 {
		return 0
	}
	u := elapsed / a3SpinRamp
	if u < 1 {
		u4 := u * u * u * u
		return a3SpinSpeed * a3SpinRamp * (u4*u*u - 3*u4*u + 2.5*u4)
	}
	return a3SpinSpeed * (a3SpinRamp*0.5 + elapsed - a3SpinRamp)
}

func a3SpinPhase(t float64) float64 {
	return a3BaseSpinPhase(t) + a3SpinAlignment*a3Smooth((t-a3FlyStart)/a3FlyDur)
}

func a3SpinAngle(t float64) float64 {
	phase := a3SpinPhase(t)
	return phase - a3SpinLinger*math.Sin(phase)
}

type a3Face struct {
	axis  int
	plane float64
	uMin  float64
	uMax  float64
	vMin  float64
	vMax  float64
	minX  int
	minY  int
	maxX  int
	maxY  int
	red   float64
	green float64
	blue  float64
}

type a3Rect struct {
	minX int
	minY int
	maxX int
	maxY int
}

type a3BlockRaster struct {
	bits           []uint8
	counts         []uint8
	rgb            []float32
	haloAmount     []float32
	haloRgb        []float32
	width          int
	height         int
	depth          []float32
	faceIds        []uint16
	dirty          *a3Rect
	haloDirty      *a3Rect
	cameraDistance float64
}

func a3NewBlockRaster(cameraDistance float64) *a3BlockRaster {
	return &a3BlockRaster{cameraDistance: cameraDistance}
}

func (r *a3BlockRaster) render(width, height int, pose a3Pose, boxes []a3Box, background a3Rgb, haloStrength float64) {
	dotWidth := width * 2
	dotHeight := height * 4
	if width != r.width || height != r.height {
		r.width = width
		r.height = height
		cells := width * height
		dots := dotWidth * dotHeight
		r.bits = make([]uint8, cells)
		r.counts = make([]uint8, cells)
		r.rgb = make([]float32, cells*3)
		r.haloAmount = make([]float32, cells)
		r.haloRgb = make([]float32, cells*3)
		r.haloDirty = nil
		r.depth = make([]float32, dots)
		inf := float32(math.Inf(1))
		for i := range r.depth {
			r.depth[i] = inf
		}
		r.faceIds = make([]uint16, dots)
		r.dirty = nil
	} else if r.dirty != nil {
		d := r.dirty
		for row := d.minY; row <= d.maxY; row++ {
			start := row*width + d.minX
			end := row*width + d.maxX + 1
			clear(r.bits[start:end])
			clear(r.counts[start:end])
			clear(r.rgb[start*3 : end*3])
		}
		r.dirty = nil
	}
	if r.haloDirty != nil {
		d := r.haloDirty
		for row := d.minY; row <= d.maxY; row++ {
			start := row*width + d.minX
			end := row*width + d.maxX + 1
			clear(r.haloAmount[start:end])
			clear(r.haloRgb[start*3 : end*3])
		}
		r.haloDirty = nil
	}

	m := a3Rotation(pose.yaw, pose.pitch, pose.roll)
	centerX, centerY, scale := pose.centerX, pose.centerY, pose.scale
	cameraDistance := r.cameraDistance
	origin := [3]float64{m[6] * cameraDistance, m[7] * cameraDistance, m[8] * cameraDistance}
	light := a3IsLight(background)
	faces := r.visibleFaces(m, origin, pose, boxes, dotWidth, dotHeight, light)
	if len(faces) == 0 {
		return
	}

	minX := dotWidth
	minY := dotHeight
	maxX := -1
	maxY := -1
	for _, face := range faces {
		minX = min(minX, face.minX)
		minY = min(minY, face.minY)
		maxX = max(maxX, face.maxX)
		maxY = max(maxY, face.maxY)
	}
	if maxX < minX || maxY < minY {
		return
	}

	depth := r.depth
	faceIds := r.faceIds
	for faceIdx, face := range faces {
		a := face.axis
		u := (a + 1) % 3
		v := (a + 2) % 3
		originA := origin[a]
		originU := origin[u]
		originV := origin[v]
		stepA := m[a] / scale
		stepU := m[u] / scale
		stepV := m[v] / scale
		sx := (float64(face.minX) + 0.5 - centerX) / scale
		for dotY := face.minY; dotY <= face.maxY; dotY++ {
			sy := (float64(dotY) + 0.5 - centerY) / scale
			dirA := m[a]*sx + m[3+a]*sy - m[6+a]*cameraDistance
			dirU := m[u]*sx + m[3+u]*sy - m[6+u]*cameraDistance
			dirV := m[v]*sx + m[3+v]*sy - m[6+v]*cameraDistance
			idx := dotY*dotWidth + face.minX
			for dotX := face.minX; dotX <= face.maxX; dotX, idx = dotX+1, idx+1 {
				t := (face.plane - originA) / dirA
				if t > 0 && float32(t) < depth[idx] {
					hitU := originU + t*dirU
					hitV := originV + t*dirV
					if hitU >= face.uMin && hitU <= face.uMax && hitV >= face.vMin && hitV <= face.vMax {
						depth[idx] = float32(t)
						faceIds[idx] = uint16(faceIdx + 1)
					}
				}
				dirA += stepA
				dirU += stepU
				dirV += stepV
			}
		}
	}

	cellMinX := width
	cellMinY := height
	cellMaxX := -1
	cellMaxY := -1
	inf := float32(math.Inf(1))
	fogFactor := 1.0
	if light {
		fogFactor = 0.5
	}
	for dotY := minY; dotY <= maxY; dotY++ {
		idx := dotY*dotWidth + minX
		for dotX := minX; dotX <= maxX; dotX, idx = dotX+1, idx+1 {
			id := faceIds[idx]
			if id == 0 {
				continue
			}
			face := faces[id-1]
			fog := a3Clamp01(0.15-cameraDistance*(1-float64(depth[idx]))*0.12) * fogFactor
			faceIds[idx] = 0
			depth[idx] = inf
			cellX := dotX >> 1
			cellY := dotY >> 2
			cell := cellY*width + cellX
			r.bits[cell] |= a3DotBits[(dotY&3)*2+(dotX&1)]
			r.counts[cell]++
			r.rgb[cell*3] += float32(face.red + (background[0]-face.red)*fog)
			r.rgb[cell*3+1] += float32(face.green + (background[1]-face.green)*fog)
			r.rgb[cell*3+2] += float32(face.blue + (background[2]-face.blue)*fog)
			if cellX < cellMinX {
				cellMinX = cellX
			}
			if cellX > cellMaxX {
				cellMaxX = cellX
			}
			if cellY < cellMinY {
				cellMinY = cellY
			}
			if cellY > cellMaxY {
				cellMaxY = cellY
			}
		}
	}
	if cellMaxX >= 0 {
		r.dirty = &a3Rect{minX: cellMinX, minY: cellMinY, maxX: cellMaxX, maxY: cellMaxY}
	}
	if haloStrength > 0 {
		r.renderHalo(haloStrength)
	}
}

func (r *a3BlockRaster) renderHalo(strength float64) {
	cells := r.dirty
	if cells == nil {
		return
	}
	width, height := r.width, r.height
	minX := max(0, cells.minX-a3HaloRadiusX)
	maxX := min(width-1, cells.maxX+a3HaloRadiusX)
	minY := max(0, cells.minY-a3HaloRadiusY)
	maxY := min(height-1, cells.maxY+a3HaloRadiusY)
	regionWidth := maxX - minX + 1

	tent := func(radius int) []float64 {
		weights := make([]float64, radius*2+1)
		total := 0.0
		for i := range weights {
			w := float64(radius + 1 - int(math.Abs(float64(i-radius))))
			weights[i] = w
			total += w
		}
		for i := range weights {
			weights[i] /= total
		}
		return weights
	}
	weightsX := tent(a3HaloRadiusX)
	weightsY := tent(a3HaloRadiusY)

	rows := cells.maxY - cells.minY + 1
	horizontal := make([]float64, rows*regionWidth*4)
	for y := cells.minY; y <= cells.maxY; y++ {
		for x := minX; x <= maxX; x++ {
			var coverage, red, green, blue float64
			for dx := -a3HaloRadiusX; dx <= a3HaloRadiusX; dx++ {
				sourceX := x + dx
				if sourceX < cells.minX || sourceX > cells.maxX {
					continue
				}
				source := y*width + sourceX
				count := r.counts[source]
				if count == 0 {
					continue
				}
				weight := weightsX[dx+a3HaloRadiusX] / 8.0
				coverage += float64(count) * weight
				red += float64(r.rgb[source*3]) * weight
				green += float64(r.rgb[source*3+1]) * weight
				blue += float64(r.rgb[source*3+2]) * weight
			}
			target := ((y-cells.minY)*regionWidth + (x - minX)) * 4
			horizontal[target] = coverage
			horizontal[target+1] = red
			horizontal[target+2] = green
			horizontal[target+3] = blue
		}
	}

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			var coverage, red, green, blue float64
			for dy := -a3HaloRadiusY; dy <= a3HaloRadiusY; dy++ {
				sourceY := y + dy
				if sourceY < cells.minY || sourceY > cells.maxY {
					continue
				}
				weight := weightsY[dy+a3HaloRadiusY]
				source := ((sourceY-cells.minY)*regionWidth + (x - minX)) * 4
				coverage += horizontal[source] * weight
				red += horizontal[source+1] * weight
				green += horizontal[source+2] * weight
				blue += horizontal[source+3] * weight
			}
			target := y*width + x
			count := r.counts[target]
			if count > 0 && float64(count)/8.0 > coverage {
				coverage = float64(count) / 8.0
				red = (float64(r.rgb[target*3]) / float64(count)) * coverage
				green = (float64(r.rgb[target*3+1]) / float64(count)) * coverage
				blue = (float64(r.rgb[target*3+2]) / float64(count)) * coverage
			}
			if coverage <= 0 {
				continue
			}
			amount := math.Round(strength*math.Pow(min(1.0, coverage), 0.7)*a3HaloLevels) / a3HaloLevels
			if amount <= 0 {
				continue
			}
			r.haloAmount[target] = float32(amount)
			r.haloRgb[target*3] = float32(math.Round(red/coverage/a3LogoColorStep) * a3LogoColorStep)
			r.haloRgb[target*3+1] = float32(math.Round(green/coverage/a3LogoColorStep) * a3LogoColorStep)
			r.haloRgb[target*3+2] = float32(math.Round(blue/coverage/a3LogoColorStep) * a3LogoColorStep)
		}
	}
	r.haloDirty = &a3Rect{minX: minX, minY: minY, maxX: maxX, maxY: maxY}
}

func (r *a3BlockRaster) visibleFaces(
	m [9]float64,
	origin [3]float64,
	pose a3Pose,
	boxes []a3Box,
	dotWidth, dotHeight int,
	lightBackground bool,
) []a3Face {
	var faces []a3Face
	cameraDistance := r.cameraDistance
	for boxIdx := range boxes {
		box := boxes[boxIdx]
		for a := range 3 {
			u := (a + 1) % 3
			v := (a + 2) % 3
			for _, side := range [2]float64{-1, 1} {
				plane := box.min[a]
				if side > 0 {
					plane = box.max[a]
				}
				if (origin[a]-plane)*side <= 0 {
					continue
				}
				covered := false
				for otherIdx := range boxes {
					if otherIdx == boxIdx {
						continue
					}
					other := boxes[otherIdx]
					otherPlane := other.max[a]
					if side > 0 {
						otherPlane = other.min[a]
					}
					if otherPlane == plane &&
						other.min[u] <= box.min[u] &&
						other.max[u] >= box.max[u] &&
						other.min[v] <= box.min[v] &&
						other.max[v] >= box.max[v] {
						covered = true
						break
					}
				}
				if covered {
					continue
				}

				minX := math.Inf(1)
				minY := math.Inf(1)
				maxX := math.Inf(-1)
				maxY := math.Inf(-1)
				var corner [3]float64
				uCorners := [2]float64{box.min[u], box.max[u]}
				vCorners := [2]float64{box.min[v], box.max[v]}
				for _, cu := range uCorners {
					for _, cv := range vCorners {
						corner[a] = plane
						corner[u] = cu
						corner[v] = cv
						x := m[0]*corner[0] + m[1]*corner[1] + m[2]*corner[2]
						y := m[3]*corner[0] + m[4]*corner[1] + m[5]*corner[2]
						z := m[6]*corner[0] + m[7]*corner[1] + m[8]*corner[2]
						perspective := (pose.scale * cameraDistance) / (cameraDistance - z)
						screenX := pose.centerX + x*perspective
						screenY := pose.centerY + y*perspective
						minX = min(minX, screenX)
						minY = min(minY, screenY)
						maxX = max(maxX, screenX)
						maxY = max(maxY, screenY)
					}
				}
				fMinX := max(0, int(math.Floor(minX)))
				fMinY := max(0, int(math.Floor(minY)))
				fMaxX := min(dotWidth-1, int(math.Ceil(maxX)))
				fMaxY := min(dotHeight-1, int(math.Ceil(maxY)))
				if fMaxX < fMinX || fMaxY < fMinY {
					continue
				}

				nx := m[a] * side
				ny := m[3+a] * side
				nz := m[6+a] * side
				diffuse := max(0.0, nx*a3Light[0]+ny*a3Light[1]+nz*a3Light[2])
				rim := max(0.0, nx*0.8-nz*0.3)
				var specular, lightVal float64
				if lightBackground {
					lightVal = min(1.0, 0.55+diffuse*0.45+rim*0.1)
				} else {
					halfDot := max(0.0, nx*a3HalfVector[0]+ny*a3HalfVector[1]+nz*a3HalfVector[2])
					specular = math.Pow(halfDot, 24) * 0.6 * 255
					lightVal = 0.45 + diffuse*0.78 + rim*0.25
				}
				faces = append(faces, a3Face{
					axis:  a,
					plane: plane,
					uMin:  box.min[u],
					uMax:  box.max[u],
					vMin:  box.min[v],
					vMax:  box.max[v],
					minX:  fMinX,
					minY:  fMinY,
					maxX:  fMaxX,
					maxY:  fMaxY,
					red:   min(255.0, box.color[0]*lightVal+specular),
					green: min(255.0, box.color[1]*lightVal+specular),
					blue:  min(255.0, box.color[2]*lightVal+specular),
				})
			}
		}
	}
	return faces
}

// PlayEasterEgg3d shows the 3D easter-egg fullscreen overlay until dismissed.
// Returns false when the TUI does not expose alt-screen lines.
func PlayEasterEgg3d(ui tui.TUI, options EasterEgg3dOptions) bool {
	alt, ok := ui.(a3lScreenProvider)
	if !ok {
		return false
	}
	if ui.HasOverlay() {
		return true
	}

	a3PlayingMu.Lock()
	if a3Playing {
		a3PlayingMu.Unlock()
		return true
	}
	a3Playing = true
	a3PlayingMu.Unlock()

	screen := slices.Clone(alt.GetScreenLines())
	th := theme.CurrentTheme()
	dark := th.Appearance == tui.TerminalColorSchemeDark || string(th.Appearance) == "dark"
	accentRgb := a3Rgb{228, 138, 122}
	if c, ok := th.Colors[theme.FgAccent]; ok {
		accentRgb = a3ToRgb(c)
	}
	textRgb := a3Rgb{255, 255, 255}
	if c, ok := th.Colors[theme.FgText]; ok {
		textRgb = a3ToRgb(c)
	}

	go func() {
		reported, _ := ui.QueryTerminalColors(tui.TuiQueryTerminalColorsOptions{TimeoutMs: 100})
		fgFallback := textRgb
		bgFallback := a3Rgb{255, 255, 255}
		if dark {
			bgFallback = a3Rgb{0, 0, 0}
		}
		foreground := fgFallback
		if reported.Foreground != nil {
			foreground = a3Rgb{reported.Foreground.R, reported.Foreground.G, reported.Foreground.B}
		}
		background := bgFallback
		if reported.Background != nil {
			background = a3Rgb{reported.Background.R, reported.Background.G, reported.Background.B}
		}

		ui.Post(func() {
			if ui.HasOverlay() {
				a3PlayingMu.Lock()
				a3Playing = false
				a3PlayingMu.Unlock()
				return
			}

			var model *a3Model
			if options.Kind == EasterEggKindArmin {
				model = a3ArminModel(accentRgb)
			} else {
				model = a3PiLogoModel(a3Origin{column: options.Column, row: options.Row})
			}

			var overlayMu sync.Mutex
			var overlay tui.OverlayHandle
			var overlayDone bool
			anim := a3NewEasterEgg3dAnimation(ui, screen, model, foreground, background, func() {
				a3PlayingMu.Lock()
				a3Playing = false
				a3PlayingMu.Unlock()
				ui.Post(func() {
					overlayMu.Lock()
					overlayDone = true
					h := overlay
					overlay = nil
					overlayMu.Unlock()
					if h != nil {
						h.Hide()
					}
				})
			})
			anchor := tui.OverlayAnchorTopLeft
			h := ui.ShowOverlay(anim, &tui.OverlayOptions{
				Anchor:    &anchor,
				Width:     &tui.SizeValuePercent{Text: "100%"},
				MaxHeight: &tui.SizeValuePercent{Text: "100%"},
			})
			overlayMu.Lock()
			alreadyDone := overlayDone
			if !alreadyDone {
				overlay = h
			}
			overlayMu.Unlock()
			if alreadyDone && h != nil {
				h.Hide()
			}
		})
	}()
	return true
}

type a3ExitState struct {
	start     time.Time
	time      float64
	yaw       float64
	targetYaw float64
	offsets   [][3]float64
}

type a3ShuffleCache struct {
	cycle int
	steps [][]a3Cell3
}

type a3Hint struct {
	row       int
	start     int
	runes     []rune
	keyLength int
	keyColor  a3Rgb
	color     a3Rgb
}

// EasterEgg3dAnimation renders the dissolving terminal screen and 3D spinning block puzzle overlay.
type EasterEgg3dAnimation struct {
	ui         tui.TUI
	screen     []string
	model      *a3Model
	foreground a3Rgb
	background a3Rgb
	onDone     func()
	startTime  time.Time

	mu           sync.Mutex
	lastRender   time.Time
	stopCh       chan struct{}
	stopped      bool
	exit         *a3ExitState
	shuffle      *a3ShuffleCache
	screenWidth  int
	screenHeight int
	stars        []*a3Star
	cells        [][]a3ScreenCell
	ansiCache    map[int]string
	raster       *a3BlockRaster
}

func a3NewEasterEgg3dAnimation(
	ui tui.TUI,
	screen []string,
	model *a3Model,
	foreground, background a3Rgb,
	onDone func(),
) *EasterEgg3dAnimation {
	now := time.Now()
	a := &EasterEgg3dAnimation{
		ui:           ui,
		screen:       screen,
		model:        model,
		foreground:   foreground,
		background:   background,
		onDone:       onDone,
		startTime:    now,
		lastRender:   now,
		stopCh:       make(chan struct{}),
		screenWidth:  -1,
		screenHeight: -1,
		ansiCache:    make(map[int]string),
		raster:       a3NewBlockRaster(model.cameraDistance),
	}
	a.startTimer()
	return a
}

func (a *EasterEgg3dAnimation) startTimer() {
	stopCh := a.stopCh
	go func() {
		ticker := time.NewTicker(a3FrameInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				shouldFinish := false
				a.mu.Lock()
				if a.stopped {
					a.mu.Unlock()
					return
				}
				if (a.exit != nil && a.exitProgressLocked() >= 1) || time.Since(a.lastRender) > time.Second {
					shouldFinish = true
				}
				a.mu.Unlock()

				if shouldFinish {
					a.finish()
					return
				}
				if a.ui != nil {
					a.ui.RequestRender(nil)
				}
			}
		}
	}()
}

// Close triggers the reverse exit animation, or finishes immediately if called a second time.
func (a *EasterEgg3dAnimation) Close() {
	a.mu.Lock()
	if a.exit != nil {
		a.mu.Unlock()
		a.finish()
		return
	}
	t := a.elapsed()
	yaw := a3SpinAngle(t)
	a.exit = &a3ExitState{
		start:     time.Now(),
		time:      t,
		yaw:       yaw,
		targetYaw: math.Ceil(yaw/a3TwoPi) * a3TwoPi,
		offsets:   a.blockOffsetsLocked(t),
	}
	a.mu.Unlock()
}

// HandleInput closes the overlay on cancel or app.clear.
func (a *EasterEgg3dAnimation) HandleInput(data string) {
	kb := tui.GetKeybindings()
	if kb.Matches(data, tui.KeybindingTuiSelectCancel) || kb.Matches(data, "app.clear") {
		a.Close()
	}
}

// HandleMouse captures clicks to close the overlay and absorbs other mouse events.
func (a *EasterEgg3dAnimation) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
	if event.Type == tui.TuiMouseEventTypeClick {
		a.Close()
	}
	handled := true
	render := false
	return &tui.TuiMouseEventResult{Handled: &handled, Render: &render}
}

// Invalidate drops cached cells and ANSI escape strings.
func (a *EasterEgg3dAnimation) Invalidate() {
	a.mu.Lock()
	a.screenWidth = -1
	a.screenHeight = -1
	clear(a.ansiCache)
	a.mu.Unlock()
}

// Render computes and returns one frame of the fullscreen 3D easter-egg overlay.
func (a *EasterEgg3dAnimation) Render(width int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	rows := 24
	if a.ui != nil && a.ui.Terminal() != nil {
		rows = a.ui.Terminal().Rows()
	}
	height := max(1, rows)
	if width < 0 {
		width = 0
	}

	foreground := a.foreground
	background := a.background
	a.lastRender = time.Now()
	if width != a.screenWidth || height != a.screenHeight {
		a.screenWidth = width
		a.screenHeight = height
		a.cells = a.prepareCells(width, height, foreground)
		a.stars = a.prepareStars(width, height)
	}

	var dissolveTime, hintAlpha, starAlpha float64
	var pose a3Pose
	var offsets [][3]float64
	if a.exit != nil {
		progress := a.exitProgressLocked()
		landing := a3Clamp01(progress / 0.8)
		settle := 1 - a3Smooth(landing)
		dissolveTime = min(a.exit.time, a3DissolveEnd) * (1 - progress)
		pose = a.pose(width, height, a.flyProgress(a.exit.time)*settle, a.exit.time)
		pose.yaw = a.exit.yaw + (a.exit.targetYaw-a.exit.yaw)*a3EaseOut(landing)
		pose.pitch *= settle
		pose.roll *= settle
		hintAlpha = a.hintAlpha(a.exit.time) * (1 - a3Smooth(progress/0.2))
		starAlpha = a.starAlpha(a.exit.time) * (1 - a3Smooth(progress/0.3))
		gather := 1 - a3Smooth(progress/0.5)
		offsets = make([][3]float64, len(a.exit.offsets))
		for i, off := range a.exit.offsets {
			offsets[i] = [3]float64{off[0] * gather, off[1] * gather, off[2] * gather}
		}
	} else {
		t := a.elapsed()
		dissolveTime = t
		pose = a.pose(width, height, a.flyProgress(t), t)
		hintAlpha = a.hintAlpha(t)
		starAlpha = a.starAlpha(t)
		offsets = a.blockOffsetsLocked(t)
	}

	cols := float64(a.model.columns)
	modelRows := float64(a.model.rows)
	boxes := make([]a3Box, len(a.model.blocks))
	for i, block := range a.model.blocks {
		off := offsets[i]
		x := float64(block.home[0]) - cols/2 + off[0]
		y := float64(block.home[1]) - modelRows/2 + off[1]
		boxes[i] = a3Box{
			min:   [3]float64{x, y, off[2] - a3Depth/2},
			max:   [3]float64{x + 1, y + 1, off[2] + a3Depth/2},
			color: block.color,
		}
	}

	haloStr := 0.0
	if a3IsLight(background) {
		haloStr = a3LightHaloStr
	}
	a.raster.render(width, height, pose, boxes, background, haloStr)

	halo := func(index int, base *a3Rgb) *a3Rgb {
		if index < 0 || index >= len(a.raster.haloAmount) {
			return base
		}
		amount := float64(a.raster.haloAmount[index])
		if amount <= 0 {
			return base
		}
		src := background
		if base != nil {
			src = *base
		}
		c := a3Mix(src, a3Rgb{
			float64(a.raster.haloRgb[index*3]),
			float64(a.raster.haloRgb[index*3+1]),
			float64(a.raster.haloRgb[index*3+2]),
		}, amount)
		return &c
	}

	backgroundFade := a3Smooth(dissolveTime / a3BgFade)
	textFade := a3Smooth(dissolveTime/0.6) * 0.3
	textActive := dissolveTime < a3DissolveEnd || backgroundFade < 1
	hint := a.hint(width, height, hintAlpha, background)
	frame := uint32(math.Floor(dissolveTime * 14))
	now := a.elapsed()
	starLevel := math.Round(starAlpha*a3StarAlphaLevels) / a3StarAlphaLevels

	starfield := func(index int) (string, a3Rgb, bool) {
		if starLevel <= 0 || index < 0 || index >= len(a.stars) {
			return "", a3Rgb{}, false
		}
		star := a.stars[index]
		if star == nil {
			return "", a3Rgb{}, false
		}
		flicker := math.Round((0.5+0.5*math.Sin(now*star.speed+star.phase))*a3StarFlickerLvls) / a3StarFlickerLvls
		fg := a3Mix(background, a3Mix(foreground, star.tint, 0.5), star.strength*(0.7+0.3*flicker)*starLevel)
		return star.glyph, fg, true
	}

	lines := make([]string, 0, height)
	for row := range height {
		var rowCells []a3ScreenCell
		if row < len(a.cells) {
			rowCells = a.cells[row]
		}
		var sb strings.Builder
		currentFg := -1
		currentBg := -1
		emit := func(text string, fg, bg *a3Rgb) {
			fgKey := -1
			if fg != nil {
				fgKey = a.pack(*fg)
			}
			bgKey := -1
			if bg != nil {
				bgKey = a.pack(*bg)
			}
			if fgKey != currentFg && text != " " {
				if fg != nil {
					sb.WriteString(a.ansiLocked(fgKey, false))
				} else {
					sb.WriteString("\x1b[39m")
				}
				currentFg = fgKey
			}
			if bgKey != currentBg {
				if bg != nil {
					sb.WriteString(a.ansiLocked(bgKey, true))
				} else {
					sb.WriteString("\x1b[49m")
				}
				currentBg = bgKey
			}
			sb.WriteString(text)
		}

		for col := 0; col < width; col++ {
			idx := row*width + col
			var cell *a3ScreenCell
			if textActive && col < len(rowCells) {
				cell = &rowCells[col]
			}
			var bgBase *a3Rgb
			if cell != nil && cell.bg != nil && backgroundFade < 1 {
				mixed := a3Mix(*cell.bg, background, backgroundFade)
				bgBase = &mixed
			}
			cellBg := halo(idx, bgBase)

			if hint != nil && row == hint.row && col >= hint.start && col < hint.start+len(hint.runes) {
				offset := col - hint.start
				fg := hint.color
				if offset < hint.keyLength {
					fg = hint.keyColor
				}
				emit(string(hint.runes[offset]), &fg, cellBg)
				continue
			}

			dots := uint8(0)
			if idx < len(a.raster.bits) {
				dots = a.raster.bits[idx]
			}
			if dots != 0 {
				step := a3LogoColorStep * float64(a.raster.counts[idx])
				c := a3Rgb{
					math.Round(float64(a.raster.rgb[idx*3])/step) * a3LogoColorStep,
					math.Round(float64(a.raster.rgb[idx*3+1])/step) * a3LogoColorStep,
					math.Round(float64(a.raster.rgb[idx*3+2])/step) * a3LogoColorStep,
				}
				emit(a3Braille[dots], &c, cellBg)
				continue
			}

			if cell == nil {
				if g, fg, ok := starfield(idx); ok {
					emit(g, &fg, cellBg)
				} else {
					emit(" ", nil, cellBg)
				}
				continue
			}

			dust := (dissolveTime - cell.delay) / a3DustDuration
			nextHasBits := idx+1 < len(a.raster.bits) && a.raster.bits[idx+1] != 0
			if dust < 0 && cell.width == 2 && !nextHasBits {
				fg := a3Mix(cell.fg, background, textFade)
				emit(cell.text, &fg, cellBg)
				col++
				continue
			}
			if dust < 0 && cell.width == 1 {
				fg := a3Mix(cell.fg, background, textFade)
				emit(cell.text, &fg, cellBg)
				continue
			}

			dotCount := cell.ink
			if dust >= 0 {
				dotCount = int(math.Round(float64(cell.ink) * (1 - a3Clamp01(dust))))
			}
			if dotCount <= 0 {
				if g, fg, ok := starfield(idx); ok {
					emit(g, &fg, cellBg)
				} else {
					emit(" ", nil, cellBg)
				}
				continue
			}

			var bits uint8
			placed := 0
			for attempt := uint32(0); placed < dotCount && attempt < 32; attempt++ {
				bit := a3DotBits[int(math.Floor(a3Hash(cell.seed*7919+frame*131+attempt)*8))]
				if bits&bit != 0 {
					continue
				}
				bits |= bit
				placed++
			}
			fade := textFade + (1-textFade)*math.Pow(a3Clamp01(dust), 0.8)
			fg := a3Mix(cell.fg, background, fade)
			emit(a3Braille[bits], &fg, cellBg)
		}
		sb.WriteString("\x1b[0m")
		lines = append(lines, sb.String())
	}
	return lines
}

func (a *EasterEgg3dAnimation) elapsed() float64 {
	return time.Since(a.startTime).Seconds()
}

func (a *EasterEgg3dAnimation) exitProgressLocked() float64 {
	if a.exit == nil {
		return 0
	}
	return a3Clamp01(time.Since(a.exit.start).Seconds() / a3ExitDuration)
}

func (a *EasterEgg3dAnimation) finish() {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	close(a.stopCh)
	onDone := a.onDone
	a.mu.Unlock()

	if onDone != nil {
		onDone()
	}
}

func (a *EasterEgg3dAnimation) blockOffsetsLocked(t float64) [][3]float64 {
	blocks := a.model.blocks
	offsets := make([][3]float64, len(blocks))
	if t < a3PuzzleStart {
		return offsets
	}
	cycle := int(math.Floor((t - a3PuzzleStart) / a3PuzzleCycle))
	local := t - a3PuzzleStart - float64(cycle)*a3PuzzleCycle
	if a.shuffle == nil || a.shuffle.cycle != cycle {
		a.shuffle = &a3ShuffleCache{cycle: cycle, steps: a3ShuffleSteps(a.model, cycle)}
	}
	steps := a.shuffle.steps
	offsetOf := func(pos a3Cell3, index int) [3]float64 {
		home := blocks[index].home
		return [3]float64{float64(pos[0] - home[0]), float64(pos[1] - home[1]), float64(pos[2] - home[2])}
	}
	shuffleEnd := a3PuzzleStep * a3PuzzleSteps
	if local < shuffleEnd {
		step := int(math.Floor(local / a3PuzzleStep))
		progress := a3Smooth((local - float64(step)*a3PuzzleStep) / a3PuzzleStep)
		for idx, from := range steps[step] {
			aOff := offsetOf(from, idx)
			bOff := offsetOf(steps[step+1][idx], idx)
			offsets[idx] = [3]float64{
				aOff[0] + (bOff[0]-aOff[0])*progress,
				aOff[1] + (bOff[1]-aOff[1])*progress,
				aOff[2] + (bOff[2]-aOff[2])*progress,
			}
		}
		return offsets
	}
	if local < shuffleEnd+a3PuzzleReturn {
		u := (local - shuffleEnd) / a3PuzzleReturn
		remaining := 1 - a3Smooth(u)
		arc := math.Sin(math.Pi*a3Clamp01(u)) * 0.8
		for idx, pos := range steps[a3PuzzleSteps] {
			o := offsetOf(pos, idx)
			zArc := -arc
			if idx%2 == 0 {
				zArc = arc
			}
			offsets[idx] = [3]float64{o[0] * remaining, o[1] * remaining, o[2]*remaining + zArc}
		}
		return offsets
	}
	return offsets
}

func (a *EasterEgg3dAnimation) flyProgress(t float64) float64 {
	return a3Smooth((t - a3FlyStart) / a3FlyDur)
}

func (a *EasterEgg3dAnimation) starAlpha(t float64) float64 {
	return a3Smooth((t - a3StarsStart) / a3StarsFade)
}

func (a *EasterEgg3dAnimation) hintAlpha(t float64) float64 {
	return a3Smooth((t - a3FlyStart - a3FlyDur) / a3HintFade)
}

func (a *EasterEgg3dAnimation) pose(width, height int, progress, t float64) a3Pose {
	cols := float64(a.model.columns)
	rows := float64(a.model.rows)
	camDist := a.model.cameraDistance
	radius := a.model.radius
	widthShare := a.model.widthShare
	origin := a.model.origin

	reach := radius * (camDist / (camDist - radius))
	startScale := a3StartScale
	if origin != nil {
		startScale = (2 * (camDist - a3Depth/2)) / camDist
	}
	endScale := max(startScale, min(float64(width*2)*widthShare, float64(height*4-8)*0.48)/reach)
	endX := float64(width)
	endY := float64(height*2 - 2)
	startX := endX
	startY := endY
	if origin != nil {
		startX = float64(origin.column*2) + cols
		startY = float64(origin.row*4) + rows
	}
	phase := a3SpinPhase(t)
	return a3Pose{
		centerX: startX + (endX-startX)*progress,
		centerY: startY + (endY-startY)*progress,
		scale:   startScale * math.Pow(endScale/startScale, progress),
		yaw:     a3SpinAngle(t),
		pitch:   0.3 * math.Sin(phase) * progress,
		roll:    0.06 * math.Sin(2*phase) * progress,
	}
}

func (a *EasterEgg3dAnimation) hint(width, height int, alpha float64, background a3Rgb) *a3Hint {
	if alpha <= 0 {
		return nil
	}
	rawKey := "escape"
	if keys := tui.GetKeybindings().GetKeys(tui.KeybindingTuiSelectCancel); len(keys) > 0 {
		rawKey = string(keys[0])
	}
	key := theme.FormatKeyText(rawKey)
	text := fmt.Sprintf("%s to return", key)
	runes := []rune(text)
	if len(runes) > width {
		return nil
	}
	th := theme.CurrentTheme()
	mutedRgb := a3Rgb{160, 160, 160}
	if c, ok := th.Colors[theme.FgMuted]; ok {
		mutedRgb = a3ToRgb(c)
	}
	dimRgb := a3Rgb{120, 120, 120}
	if c, ok := th.Colors[theme.FgDim]; ok {
		dimRgb = a3ToRgb(c)
	}
	return &a3Hint{
		row:       height - 2,
		start:     (width - len(runes)) / 2,
		runes:     runes,
		keyLength: len([]rune(key)),
		keyColor:  a3Mix(background, mutedRgb, alpha),
		color:     a3Mix(background, dimRgb, alpha),
	}
}

func (a *EasterEgg3dAnimation) prepareStars(width, height int) []*a3Star {
	stars := make([]*a3Star, width*height)
	for index := range stars {
		if a3Hash(uint32(index*3+0x51ed)) >= a3StarDensity {
			continue
		}
		random := func(salt int) float64 {
			return a3Hash(uint32(index*7 + salt*0x9e37))
		}
		stars[index] = &a3Star{
			glyph:    a3Braille[a3DotBits[int(math.Floor(random(1)*8))]],
			tint:     a3StarTints[int(math.Floor(random(2)*float64(len(a3StarTints))))],
			strength: 0.1 + random(3)*0.12,
			phase:    random(4) * a3TwoPi,
			speed:    0.4 + random(5)*0.8,
		}
	}
	return stars
}

func (a *EasterEgg3dAnimation) prepareCells(width, height int, foreground a3Rgb) [][]a3ScreenCell {
	cells := a3ParseScreen(a.screen, width, foreground, a.background)
	cols := float64(a.model.columns)
	rows := float64(a.model.rows)
	origin := a.model.origin

	centerX := float64(width) / 2
	centerY := float64(height) / 2
	if origin != nil {
		centerX = float64(origin.column) + cols/2
		centerY = float64(origin.row) + rows/4
	}
	farthest := math.Hypot(max(centerX, float64(width)-centerX), max(centerY, float64(height)-centerY)*2)
	if farthest <= 0 {
		farthest = 1
	}

	for row := range cells {
		line := cells[row]
		for col := range line {
			cell := &line[col]
			owner := cell
			if cell.width == 0 && col > 0 {
				owner = &line[col-1]
			}
			seed := a3Hash(uint32(row*65537 + col))
			if cell.width != 0 {
				distance := math.Hypot(float64(col)-centerX, (float64(row)-centerY)*2) / farthest
				cell.delay = a3FlyStart + distance*a3WaveSpread + seed*a3WaveJitter
			} else {
				cell.delay = owner.delay
			}
			cell.seed = uint32(row*65537 + col)
			cell.ink = a3GlyphInk(owner.text, seed)
		}
	}

	if origin != nil {
		endRow := origin.row + (a.model.rows+1)/2
		for row := origin.row; row < endRow; row++ {
			if row < 0 || row >= len(cells) {
				continue
			}
			line := cells[row]
			for col := origin.column; col < origin.column+a.model.columns; col++ {
				if col < 0 || col >= len(line) {
					continue
				}
				line[col].text = " "
				line[col].width = 1
				line[col].ink = 0
				line[col].bg = nil
			}
		}
	}
	return cells
}

func (a *EasterEgg3dAnimation) pack(color a3Rgb) int {
	return (int(math.Round(color[0])) << 16) | (int(math.Round(color[1])) << 8) | int(math.Round(color[2]))
}

func (a *EasterEgg3dAnimation) ansiLocked(key int, isBackground bool) string {
	cacheKey := key * 2
	if isBackground {
		cacheKey++
	}
	if value, ok := a.ansiCache[cacheKey]; ok {
		return value
	}
	c := &tui.RgbColorValue{
		Kind: "rgb",
		R:    float64((key >> 16) & 255),
		G:    float64((key >> 8) & 255),
		B:    float64(key & 255),
	}
	mode := theme.CurrentTheme().GetColorMode()
	var value string
	if isBackground {
		value = tui.BackgroundAnsi(c, mode)
	} else {
		value = tui.ForegroundAnsi(c, mode)
	}
	a.ansiCache[cacheKey] = value
	return value
}
