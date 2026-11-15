package gui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// SRGB represents an 8-bit per channel sRGB color.
type SRGB struct {
	R, G, B uint8
}

// OKLab represents a perceptually uniform color in Björn Ottosson's OKLab space.
type OKLab struct {
	L, A, B float64
}

// OKHSL represents a cylindrical color in Ottosson's OKHSL space:
// H is hue in degrees [0, 360), S is saturation [0, 1], L is lightness [0, 1].
type OKHSL struct {
	H, S, L float64
}

// clamp01 clamps x into [0, 1].
func clamp01(x float64) float64 {
	if x <= 0 || math.IsNaN(x) {
		return 0
	}
	if x >= 1 {
		return 1
	}
	return x
}

func clampByte(x float64) uint8 {
	v := math.Round(clamp01(x) * 255.0)
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// SRGBToLinear converts a normalized sRGB channel [0, 1] to linear light [0, 1].
func SRGBToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// LinearToSRGB converts a linear light channel [0, 1] to sRGB [0, 1].
func LinearToSRGB(c float64) float64 {
	if c <= 0 {
		return 0
	}
	if c >= 0.0031308 {
		return 1.055*math.Pow(c, 1.0/2.4) - 0.055
	}
	return 12.92 * c
}

// LinearSRGBToOKLab converts linear sRGB [0, 1] components to OKLab.
func LinearSRGBToOKLab(r, g, b float64) OKLab {
	l := 0.4122214708*r + 0.5363325363*g + 0.0514459929*b
	m := 0.2119034982*r + 0.6806995451*g + 0.1073969566*b
	s := 0.0883024619*r + 0.2817188376*g + 0.6299787005*b

	lRoot := math.Cbrt(l)
	mRoot := math.Cbrt(m)
	sRoot := math.Cbrt(s)

	return OKLab{
		L: 0.2104542553*lRoot + 0.7936177850*mRoot - 0.0040720468*sRoot,
		A: 1.9779984951*lRoot - 2.4285922050*mRoot + 0.4505937099*sRoot,
		B: 0.0259040371*lRoot + 0.7827717662*mRoot - 0.8086757660*sRoot,
	}
}

// OKLabToLinearSRGB converts OKLab to linear sRGB components.
func OKLabToLinearSRGB(c OKLab) (r, g, b float64) {
	lRoot := c.L + 0.3963377774*c.A + 0.2158037573*c.B
	mRoot := c.L - 0.1055613458*c.A - 0.0638541728*c.B
	sRoot := c.L - 0.0894841775*c.A - 1.2914855480*c.B

	l := lRoot * lRoot * lRoot
	m := mRoot * mRoot * mRoot
	s := sRoot * sRoot * sRoot

	r = +4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g = -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b = -0.0041960863*l - 0.7034186147*m + 1.7076147010*s
	return r, g, b
}

// SRGBToOKLab converts 8-bit sRGB to OKLab.
func SRGBToOKLab(c SRGB) OKLab {
	return LinearSRGBToOKLab(
		SRGBToLinear(float64(c.R)/255.0),
		SRGBToLinear(float64(c.G)/255.0),
		SRGBToLinear(float64(c.B)/255.0),
	)
}

// OKLabToSRGB converts OKLab to 8-bit clamped sRGB.
func OKLabToSRGB(c OKLab) SRGB {
	r, g, b := OKLabToLinearSRGB(c)
	return SRGB{
		R: clampByte(LinearToSRGB(r)),
		G: clampByte(LinearToSRGB(g)),
		B: clampByte(LinearToSRGB(b)),
	}
}

func computeMaxSaturation(a, b float64) float64 {
	var k0, k1, k2, k3, k4, wl, wm, ws float64
	if -1.88170328*a-0.80936493*b > 1 {
		k0, k1, k2, k3, k4 = +1.19086277, +1.76576728, +0.59662641, +0.75515197, +0.56771245
		wl, wm, ws = +4.0767416621, -3.3077115913, +0.2309699292
	} else if 1.81444104*a-1.19445276*b > 1 {
		k0, k1, k2, k3, k4 = +0.73956515, -0.45954404, +0.08285427, +0.12541070, +0.14503204
		wl, wm, ws = -1.2684380046, +2.6097574011, -0.3413193965
	} else {
		k0, k1, k2, k3, k4 = +1.35733652, -0.00915799, -1.15130210, -0.50559606, +0.00692167
		wl, wm, ws = -0.0041960863, -0.7034186147, +1.7076147010
	}

	sat := k0 + k1*a + k2*b + k3*a*a + k4*a*b

	kL := +0.3963377774*a + 0.2158037573*b
	kM := -0.1055613458*a - 0.0638541728*b
	kS := -0.0894841775*a - 1.2914855480*b

	lRoot := 1.0 + sat*kL
	mRoot := 1.0 + sat*kM
	sRoot := 1.0 + sat*kS

	l := lRoot * lRoot * lRoot
	m := mRoot * mRoot * mRoot
	s := sRoot * sRoot * sRoot

	lds := 3.0 * kL * lRoot * lRoot
	mds := 3.0 * kM * mRoot * mRoot
	sds := 3.0 * kS * sRoot * sRoot

	l2ds := 6.0 * kL * kL * lRoot
	m2ds := 6.0 * kM * kM * mRoot
	s2ds := 6.0 * kS * kS * sRoot

	f := wl*l + wm*m + ws*s
	f1 := wl*lds + wm*mds + ws*sds
	f2 := wl*l2ds + wm*m2ds + ws*s2ds

	sat -= f * f1 / (f1*f1 - 0.5*f*f2)
	return sat
}

func findCusp(a, b float64) (l, c float64) {
	sCusp := computeMaxSaturation(a, b)
	rgbR, rgbG, rgbB := OKLabToLinearSRGB(OKLab{L: 1, A: sCusp * a, B: sCusp * b})
	maxRGB := math.Max(math.Max(rgbR, rgbG), rgbB)
	if maxRGB <= 0 {
		return 0, 0
	}
	lCusp := math.Cbrt(1.0 / maxRGB)
	cCusp := lCusp * sCusp
	return lCusp, cCusp
}

func findGamutIntersection(a, b, l1, c1, l0 float64, lCusp, cCusp float64) float64 {
	if (l1-l0)*cCusp-(lCusp-l0)*c1 <= 0 {
		denom := c1*lCusp + cCusp*(l0-l1)
		if denom == 0 {
			return 0
		}
		return (cCusp * l0) / denom
	}
	denom := (l1-l0)*cCusp - c1*(lCusp-1.0)
	if denom == 0 {
		return 0
	}
	t := (cCusp * (1.0 - l0)) / denom

	dl := l1 - l0
	dc := c1

	kL := +0.3963377774*a + 0.2158037573*b
	kM := -0.1055613458*a - 0.0638541728*b
	kS := -0.0894841775*a - 1.2914855480*b

	lDt := dl + dc*kL
	mDt := dl + dc*kM
	sDt := dl + dc*kS

	L := l0*(1.0-t) + t*l1
	C := t * c1

	lRoot := L + C*kL
	mRoot := L + C*kM
	sRoot := L + C*kS

	l := lRoot * lRoot * lRoot
	m := mRoot * mRoot * mRoot
	s := sRoot * sRoot * sRoot

	ldt := 3.0 * lDt * lRoot * lRoot
	mdt := 3.0 * mDt * mRoot * mRoot
	sdt := 3.0 * sDt * sRoot * sRoot

	ldt2 := 6.0 * lDt * lDt * lRoot
	mdt2 := 6.0 * mDt * mDt * mRoot
	sdt2 := 6.0 * sDt * sDt * sRoot

	r := 4.0767416621*l - 3.3077115913*m + 0.2309699292*s - 1.0
	r1 := 4.0767416621*ldt - 3.3077115913*mdt + 0.2309699292*sdt
	r2 := 4.0767416621*ldt2 - 3.3077115913*mdt2 + 0.2309699292*sdt2
	ur := r1 / (r1*r1 - 0.5*r*r2)
	tr := -r * ur

	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s - 1.0
	g1 := -1.2684380046*ldt + 2.6097574011*mdt - 0.3413193965*sdt
	g2 := -1.2684380046*ldt2 + 2.6097574011*mdt2 - 0.3413193965*sdt2
	ug := g1 / (g1*g1 - 0.5*g*g2)
	tg := -g * ug

	bv := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s - 1.0
	b1 := -0.0041960863*ldt - 0.7034186147*mdt + 1.7076147010*sdt
	b2 := -0.0041960863*ldt2 - 0.7034186147*mdt2 + 1.7076147010*sdt2
	ub := b1 / (b1*b1 - 0.5*bv*b2)
	tb := -bv * ub

	if !(ur >= 0) {
		tr = math.MaxFloat64
	}
	if !(ug >= 0) {
		tg = math.MaxFloat64
	}
	if !(ub >= 0) {
		tb = math.MaxFloat64
	}

	step := math.Min(tr, math.Min(tg, tb))
	if step < math.MaxFloat64/2 {
		t += step
	}
	return t
}

func toe(x float64) float64 {
	const k1 = 0.206
	const k2 = 0.03
	const k3 = (1.0 + k1) / (1.0 + k2)
	return 0.5 * (k3*x - k1 + math.Sqrt((k3*x-k1)*(k3*x-k1)+4*k2*k3*x))
}

func toeInv(x float64) float64 {
	const k1 = 0.206
	const k2 = 0.03
	const k3 = (1.0 + k1) / (1.0 + k2)
	return (x*x + k1*x) / (k3 * (x + k2))
}

func getSTMax(a, b, lCusp, cCusp float64) (s, t float64) {
	return cCusp / lCusp, cCusp / (1.0 - lCusp)
}

func getSTMid(a, b float64) (s, t float64) {
	s = 0.11516993 + 1.0/(+7.44778970+4.15901240*b+a*(-2.19557347+1.75198401*b+a*(-2.13704948-10.02301043*b+a*(-4.24894561+5.38770819*b+4.69891013*a))))
	t = 0.11239642 + 1.0/(+1.61320320-0.68124379*b+a*(+0.40370612+0.90148123*b+a*(-0.27087943+0.61223990*b+a*(+0.00299215-0.45399568*b-0.14661872*a))))
	return s, t
}

func getCs(L, a, b float64) (c0, cMid, cMax float64) {
	lCusp, cCusp := findCusp(a, b)
	cMax = findGamutIntersection(a, b, L, 1, L, lCusp, cCusp)
	sMax, tMax := getSTMax(a, b, lCusp, cCusp)

	k := cMax / math.Min(L*sMax, (1.0-L)*tMax)
	sMid, tMid := getSTMid(a, b)

	cA := L * sMid
	cB := (1.0 - L) * tMid
	cMid = 0.9 * k * math.Sqrt(math.Sqrt(1.0/(1.0/(cA*cA*cA*cA)+1.0/(cB*cB*cB*cB))))

	cA = L * 0.4
	cB = (1.0 - L) * 0.8
	c0 = math.Sqrt(1.0 / (1.0/(cA*cA) + 1.0/(cB*cB)))
	return c0, cMid, cMax
}

// OKHSLToSRGB converts OKHSL (H in degrees, S and L in [0, 1]) to 8-bit sRGB.
func OKHSLToSRGB(hsl OKHSL) SRGB {
	h := math.Mod(hsl.H, 360.0)
	if h < 0 {
		h += 360.0
	}
	s := clamp01(hsl.S)
	l := clamp01(hsl.L)

	if l >= 1.0 {
		return SRGB{255, 255, 255}
	}
	if l <= 0.0 {
		return SRGB{0, 0, 0}
	}

	rad := h * math.Pi / 180.0
	aUnit := math.Cos(rad)
	bUnit := math.Sin(rad)

	L := toeInv(l)
	c0, cMid, cMax := getCs(L, aUnit, bUnit)

	var C float64
	if s < 0.8 {
		t := 1.25 * s
		k0 := 0.0
		k1 := 0.8 * c0
		k2 := 1.0 - k1/cMid
		C = k0 + (t*k1)/(1.0-k2*t)
	} else {
		t := 5.0 * (s - 0.8)
		k0 := cMid
		k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
		k2 := 1.0 - k1/(cMax-cMid)
		C = k0 + (t*k1)/(1.0-k2*t)
	}

	r, g, b := OKLabToLinearSRGB(OKLab{L: L, A: C * aUnit, B: C * bUnit})
	return SRGB{
		R: clampByte(LinearToSRGB(r)),
		G: clampByte(LinearToSRGB(g)),
		B: clampByte(LinearToSRGB(b)),
	}
}

// SRGBToOKHSL converts an 8-bit sRGB color to OKHSL.
func SRGBToOKHSL(c SRGB) OKHSL {
	lab := SRGBToOKLab(c)
	C := math.Hypot(lab.A, lab.B)
	if C < 1e-6 || lab.L <= 1e-6 || lab.L >= 1.0-1e-6 {
		return OKHSL{H: 0, S: 0, L: clamp01(toe(lab.L))}
	}

	aUnit := lab.A / C
	bUnit := lab.B / C

	L := lab.L
	h := math.Atan2(-lab.B, -lab.A)*180.0/math.Pi + 180.0
	if h >= 360.0 {
		h -= 360.0
	}

	c0, cMid, cMax := getCs(L, aUnit, bUnit)
	var s float64
	if C < cMid {
		k1 := 0.8 * c0
		k2 := 1.0 - k1/cMid
		t := C / (k1 + k2*C)
		s = t * 0.8
	} else {
		k0 := cMid
		k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
		k2 := 1.0 - k1/(cMax-cMid)
		t := (C - k0) / (k1 + k2*(C-k0))
		s = 0.8 + 0.2*t
	}
	return OKHSL{
		H: h,
		S: clamp01(s),
		L: clamp01(toe(L)),
	}
}

// OKHSLToUI converts OKHSL directly to an opaque ui.Color.
func OKHSLToUI(h, s, l float64) ui.Color {
	rgb := OKHSLToSRGB(OKHSL{H: h, S: s, L: l})
	return ui.RGB(rgb.R, rgb.G, rgb.B)
}

// ParseOKHSL parses strings of the form "okhsl(295 50% 67%)" or
// "okhsl(295, 50%, 67%, 0.5)" into a ui.Color.
func ParseOKHSL(spec string) (ui.Color, bool) {
	s := strings.TrimSpace(strings.ToLower(spec))
	if !strings.HasPrefix(s, "okhsl(") || !strings.HasSuffix(s, ")") {
		return ui.Color{}, false
	}
	inner := strings.TrimSpace(s[len("okhsl(") : len(s)-1])
	inner = strings.ReplaceAll(inner, "/", " ")
	inner = strings.ReplaceAll(inner, ",", " ")
	fields := strings.Fields(inner)
	if len(fields) < 3 || len(fields) > 4 {
		return ui.Color{}, false
	}

	hStr := strings.TrimSuffix(fields[0], "deg")
	h, err := strconv.ParseFloat(hStr, 64)
	if err != nil {
		return ui.Color{}, false
	}
	sat, okS := parsePercentOrUnit(fields[1])
	lit, okL := parsePercentOrUnit(fields[2])
	if !okS || !okL {
		return ui.Color{}, false
	}

	alpha := 1.0
	if len(fields) == 4 {
		a, okA := parsePercentOrUnit(fields[3])
		if !okA {
			return ui.Color{}, false
		}
		alpha = clamp01(a)
	}

	rgb := OKHSLToSRGB(OKHSL{H: h, S: sat, L: lit})
	if alpha >= 1.0 {
		return ui.RGB(rgb.R, rgb.G, rgb.B), true
	}
	return ui.RGBA(rgb.R, rgb.G, rgb.B, float32(alpha)), true
}

func parsePercentOrUnit(raw string) (float64, bool) {
	isPct := strings.HasSuffix(raw, "%")
	numStr := strings.TrimSuffix(raw, "%")
	v, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, false
	}
	if isPct {
		v /= 100.0
	}
	return clamp01(v), true
}

// ParseHexColor parses #RGB, #RGBA, #RRGGBB, or #RRGGBBAA hex color strings safely.
func ParseHexColor(spec string) (ui.Color, bool) {
	s := strings.TrimSpace(spec)
	if !strings.HasPrefix(s, "#") {
		return ui.Color{}, false
	}
	hex := s[1:]
	switch len(hex) {
	case 3:
		r, ok1 := parseHexNibble(hex[0])
		g, ok2 := parseHexNibble(hex[1])
		b, ok3 := parseHexNibble(hex[2])
		if !ok1 || !ok2 || !ok3 {
			return ui.Color{}, false
		}
		return ui.RGB(r*17, g*17, b*17), true
	case 4:
		r, ok1 := parseHexNibble(hex[0])
		g, ok2 := parseHexNibble(hex[1])
		b, ok3 := parseHexNibble(hex[2])
		a, ok4 := parseHexNibble(hex[3])
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return ui.Color{}, false
		}
		return ui.RGBA(r*17, g*17, b*17, float32(a*17)/255.0), true
	case 6:
		v, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return ui.Color{}, false
		}
		return ui.RGB(uint8(v>>16), uint8(v>>8), uint8(v)), true
	case 8:
		v, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return ui.Color{}, false
		}
		a := uint8(v)
		return ui.RGBA(uint8(v>>24), uint8(v>>16), uint8(v>>8), float32(a)/255.0), true
	default:
		return ui.Color{}, false
	}
}

func parseHexNibble(ch byte) (uint8, bool) {
	switch {
	case ch >= '0' && ch <= '9':
		return ch - '0', true
	case ch >= 'a' && ch <= 'f':
		return ch - 'a' + 10, true
	case ch >= 'A' && ch <= 'F':
		return ch - 'A' + 10, true
	default:
		return 0, false
	}
}

// ParseColor parses hex (#RGB, #RRGGBB, #RRGGBBAA), okhsl(...), rgb(...), or rgba(...).
func ParseColor(spec string) (ui.Color, error) {
	s := strings.TrimSpace(spec)
	if c, ok := ParseHexColor(s); ok {
		return c, nil
	}
	if c, ok := ParseOKHSL(s); ok {
		return c, nil
	}
	lower := strings.ToLower(s)
	if (strings.HasPrefix(lower, "rgb(") || strings.HasPrefix(lower, "rgba(")) && strings.HasSuffix(lower, ")") {
		start := strings.IndexByte(lower, '(')
		inner := lower[start+1 : len(lower)-1]
		inner = strings.ReplaceAll(inner, "/", " ")
		inner = strings.ReplaceAll(inner, ",", " ")
		fields := strings.Fields(inner)
		if len(fields) == 3 || len(fields) == 4 {
			r, errR := strconv.Atoi(fields[0])
			g, errG := strconv.Atoi(fields[1])
			b, errB := strconv.Atoi(fields[2])
			if errR == nil && errG == nil && errB == nil {
				alpha := 1.0
				if len(fields) == 4 {
					if a, okA := parsePercentOrUnit(fields[3]); okA {
						alpha = a
					} else {
						return ui.Color{}, fmt.Errorf("invalid alpha in color %q", spec)
					}
				}
				return ui.RGBA(clampU8(r), clampU8(g), clampU8(b), float32(clamp01(alpha))), nil
			}
		}
	}
	return ui.Color{}, fmt.Errorf("unsupported color format %q", spec)
}

func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// ColorToHex formats a ui.Color as #RRGGBB (or #RRGGBBAA when A < 255).
func ColorToHex(c ui.Color) string {
	srgb := c.SRGB()
	if srgb.A < 255 {
		return fmt.Sprintf("#%02x%02x%02x%02x", srgb.R, srgb.G, srgb.B, srgb.A)
	}
	return fmt.Sprintf("#%02x%02x%02x", srgb.R, srgb.G, srgb.B)
}

// MixOklab interpolates between a and b in OKLab space via MyGo's Color.MixOklab.
func MixOklab(a, b ui.Color, t float32) ui.Color {
	return a.MixOklab(b, t)
}

// WithAlpha returns c with its opacity multiplied/set to alpha [0, 1].
func WithAlpha(c ui.Color, alpha float32) ui.Color {
	return c.Alpha(alpha)
}

// Tint blends target color into base in OKLab space by amount [0, 1].
func Tint(base, target ui.Color, amount float32) ui.Color {
	return base.MixOklab(target, amount)
}

// Luminance returns the WCAG 2.x relative luminance [0, 1] of c.
func Luminance(c ui.Color) float64 {
	s := c.SRGB()
	r := SRGBToLinear(float64(s.R) / 255.0)
	g := SRGBToLinear(float64(s.G) / 255.0)
	b := SRGBToLinear(float64(s.B) / 255.0)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// ContrastRatio returns the WCAG contrast ratio (between 1 and 21) between a and b.
func ContrastRatio(a, b ui.Color) float64 {
	l1 := Luminance(a)
	l2 := Luminance(b)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// ContrastingText picks light or dark depending on which has higher contrast against bg.
func ContrastingText(bg, light, dark ui.Color) ui.Color {
	if ContrastRatio(bg, light) >= ContrastRatio(bg, dark) {
		return light
	}
	return dark
}
