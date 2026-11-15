package gui

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestOKLabAndOKHSLRoundtrips(t *testing.T) {
	samples := []SRGB{
		{0, 0, 0},
		{255, 255, 255},
		{255, 0, 0},
		{0, 255, 0},
		{0, 0, 255},
		{120, 80, 200},
		{42, 190, 140},
	}

	for _, s := range samples {
		lab := SRGBToOKLab(s)
		back := OKLabToSRGB(lab)
		if math.Abs(float64(s.R)-float64(back.R)) > 1 ||
			math.Abs(float64(s.G)-float64(back.G)) > 1 ||
			math.Abs(float64(s.B)-float64(back.B)) > 1 {
			t.Fatalf("OKLab roundtrip mismatch for %+v -> %+v", s, back)
		}

		if s.R == 0 && s.G == 0 && s.B == 0 {
			continue
		}
		if s.R == 255 && s.G == 255 && s.B == 255 {
			continue
		}
		hsl := SRGBToOKHSL(s)
		backHSL := OKHSLToSRGB(hsl)
		if math.Abs(float64(s.R)-float64(backHSL.R)) > 2 ||
			math.Abs(float64(s.G)-float64(backHSL.G)) > 2 ||
			math.Abs(float64(s.B)-float64(backHSL.B)) > 2 {
			t.Fatalf("OKHSL roundtrip mismatch for %+v (hsl=%+v) -> %+v", s, hsl, backHSL)
		}
	}
}

func TestParseColorsAndHelpers(t *testing.T) {
	if c, ok := ParseHexColor("#abc"); !ok || c.SRGB() != ui.RGB(0xaa, 0xbb, 0xcc) {
		t.Fatalf("ParseHexColor(#abc) = %+v, %v", c, ok)
	}
	if c, ok := ParseHexColor("#11223380"); !ok || c.R != 0x11 || c.G != 0x22 || c.B != 0x33 || c.A != 0x80 {
		t.Fatalf("ParseHexColor(#11223380) = %+v, %v", c, ok)
	}
	if _, ok := ParseHexColor("112233"); ok {
		t.Fatal("expected missing # to fail")
	}

	okhslColor, ok := ParseOKHSL("okhsl(295 50% 67%)")
	if !ok {
		t.Fatal("expected okhsl parse to succeed")
	}
	if okhslColor.A != 255 {
		t.Fatalf("expected opaque color, got alpha %d", okhslColor.A)
	}

	rgbaColor, err := ParseColor("rgba(10, 20, 30, 50%)")
	if err != nil {
		t.Fatalf("ParseColor(rgba) unexpected err: %v", err)
	}
	if rgbaColor.R != 10 || rgbaColor.G != 20 || rgbaColor.B != 30 || rgbaColor.A < 126 || rgbaColor.A > 129 {
		t.Fatalf("unexpected rgba result: %+v", rgbaColor)
	}

	hexOut := ColorToHex(ui.RGB(0x1a, 0x2b, 0x3c))
	if hexOut != "#1a2b3c" {
		t.Fatalf("ColorToHex = %q, want #1a2b3c", hexOut)
	}

	black := ui.RGB(0, 0, 0)
	white := ui.RGB(255, 255, 255)
	if ratio := ContrastRatio(black, white); ratio < 20.9 {
		t.Fatalf("ContrastRatio(black, white) = %.2f, want ~21", ratio)
	}
	picked := ContrastingText(black, white, ui.RGB(20, 20, 20))
	if picked != white {
		t.Fatalf("expected white on black background, got %+v", picked)
	}
	mixed := MixOklab(black, white, 0.5)
	if mixed.R == 0 || mixed.R == 255 {
		t.Fatalf("expected mid mix, got %+v", mixed)
	}
}

func TestGuiThemePresetsAndContextResolution(t *testing.T) {
	dark := DarkGuiTheme()
	light := LightGuiTheme()

	if !dark.Dark || light.Dark {
		t.Fatal("theme dark flags incorrect")
	}
	if Luminance(dark.Background) >= Luminance(light.Background) {
		t.Fatal("expected dark background luminance < light background luminance")
	}

	uiDark := dark.UITheme()
	mdDark := dark.MarkdownTheme()
	edDark := dark.EditorTheme()
	if !uiDark.Dark || mdDark.CodeColors == nil || edDark.Added != dark.DiffAdded {
		t.Fatal("derived themes did not carry expected tokens")
	}

	if dark.ThinkingColor("high") != dark.ThinkingHigh || dark.ThinkingColor("max") != dark.ThinkingMax {
		t.Fatal("ThinkingColor mapping mismatch")
	}

	var resolvedDark bool
	tt := ui.NewTesterWith(func(c *ui.Context) {
		th := ResolveGuiTheme(c, nil)
		resolvedDark = th.Dark
		ui.Text(c, th.Name)
	}, 200, 100, ui.TesterOptions{Dark: true})

	if !resolvedDark || !tt.HasText("dark") {
		t.Fatalf("expected resolved dark theme, got dark=%v texts=%v", resolvedDark, tt.Texts())
	}
	tt.SetDark(false)
	if resolvedDark || !tt.HasText("light") {
		t.Fatalf("expected auto switch to light theme, got dark=%v texts=%v", resolvedDark, tt.Texts())
	}
}
