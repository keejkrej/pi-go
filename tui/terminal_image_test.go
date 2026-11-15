// Ported from packages/tui/test/terminal-image.test.ts (pi v1.5.2).

package tui_test

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func tiTestBoolPtr(v bool) *bool    { return &v }
func tiTestIntPtr(v int) *int       { return &v }
func tiTestStrPtr(v string) *string { return &v }

func tiTestMakePNG(w, h uint32) string {
	buf := make([]byte, 24)
	copy(buf[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	binary.BigEndian.PutUint32(buf[8:12], 13)
	copy(buf[12:16], []byte("IHDR"))
	binary.BigEndian.PutUint32(buf[16:20], w)
	binary.BigEndian.PutUint32(buf[20:24], h)
	return base64.StdEncoding.EncodeToString(buf)
}

func tiTestMakeJPEG(w, h uint16, marker byte) string {
	buf := []byte{
		0xff, 0xd8, // SOI
		0xff, 0xe0, 0x00, 0x04, 0x00, 0x00, // APP0 dummy
		0xff, marker, 0x00, 0x0b, 0x08, // SOF0/1/2 header
		byte(h >> 8), byte(h),
		byte(w >> 8), byte(w),
		0x03, 0x01, 0x22, 0x00,
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func tiTestMakeGIF(sig string, w, h uint16) string {
	buf := make([]byte, 10)
	copy(buf[:6], sig)
	binary.LittleEndian.PutUint16(buf[6:8], w)
	binary.LittleEndian.PutUint16(buf[8:10], h)
	return base64.StdEncoding.EncodeToString(buf)
}

func tiTestMakeWebP(chunk string, w, h int) string {
	buf := make([]byte, 30)
	copy(buf[0:4], "RIFF")
	copy(buf[8:12], "WEBP")
	copy(buf[12:16], chunk)
	switch chunk {
	case "VP8 ":
		binary.LittleEndian.PutUint16(buf[26:28], uint16(w))
		binary.LittleEndian.PutUint16(buf[28:30], uint16(h))
	case "VP8L":
		bits := uint32((w - 1) | ((h - 1) << 14))
		binary.LittleEndian.PutUint32(buf[21:25], bits)
	case "VP8X":
		buf[24] = byte(w - 1)
		buf[25] = byte((w - 1) >> 8)
		buf[26] = byte((w - 1) >> 16)
		buf[27] = byte(h - 1)
		buf[28] = byte((h - 1) >> 8)
		buf[29] = byte((h - 1) >> 16)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func tiTestClearEnv(t *testing.T) {
	for _, k := range []string{
		"TERM_PROGRAM", "TERMINAL_EMULATOR", "TERM", "COLORTERM",
		"TMUX", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE",
		"WARP_SESSION_ID", "WARP_TERMINAL_SESSION_UUID", "ITERM_SESSION_ID",
		"WT_SESSION", "ALACRITTY_WINDOW_ID", "ORCA_IMAGE_PROTOCOL",
		"PI_HYPERLINKS", "PI_IMAGE_PROTOCOL", "PI_TRUE_COLOR",
	} {
		t.Setenv(k, "")
	}
}

func TestIsImageLineAndExtent(t *testing.T) {
	if !tui.IsImageLine("\x1b_Ga=T,f=100;base64data\x1b\\") {
		t.Fatalf("expected kitty line to be image line")
	}
	if !tui.IsImageLine("prefix\x1b[3A\x1b]1337;File=inline=1:data\x07suffix") {
		t.Fatalf("expected iterm2 line with prefix/suffix to be image line")
	}
	if tui.IsImageLine("plain text line") {
		t.Fatalf("expected plain text not to be image line")
	}

	// Kitty placement extent & StripImageSequences
	tui.RegisterKittyImageMetadata(tui.KittyImageMetadata{
		ImageId: 42, Columns: 10, Rows: 4, WidthPx: 90, HeightPx: 72,
	})
	kittyLine := "pre\x1b_Ga=T,f=100,q=2,c=8,r=3,i=42;AAAA\x1b\\post"
	ext := tui.GetImageLineExtent(kittyLine)
	if ext == nil || ext.FirstRow != 0 || ext.LastRow != 2 || ext.StartCol != 3 || ext.EndCol != 11 {
		t.Fatalf("kitty extent mismatch: %+v", ext)
	}
	if got := tui.GetKittyImagePlacementRows(kittyLine); got == nil || *got != 3 {
		t.Fatalf("placement rows mismatch: %v", got)
	}
	if got := tui.GetKittyImagePlacementRows("\x1b_Ga=p,i=42;\x1b\\"); got == nil || *got != 4 {
		t.Fatalf("registered placement rows fallback mismatch: %v", got)
	}
	if tui.GetMaxKittyImageRows() < 4 {
		t.Fatalf("expected max kitty rows >= 4, got %d", tui.GetMaxKittyImageRows())
	}

	// Unknown column width falls back to math.MaxInt
	extUnk := tui.GetImageLineExtent("\x1b_Ga=T,f=100;AAAA\x1b\\")
	if extUnk == nil || extUnk.EndCol != math.MaxInt {
		t.Fatalf("expected unknown kitty width EndCol=MaxInt, got %+v", extUnk)
	}

	// iTerm2 extent with cursor-up
	iterm2Line := "ab\x1b[5A\x1b]1337;File=inline=1;size=3;width=12:AAAA\x07cd"
	iExt := tui.GetImageLineExtent(iterm2Line)
	if iExt == nil || iExt.FirstRow != -5 || iExt.LastRow != 0 || iExt.StartCol != 2 || iExt.EndCol != 14 {
		t.Fatalf("iterm2 extent mismatch: %+v", iExt)
	}

	if stripped := tui.StripImageSequences(kittyLine); stripped != "prepost" {
		t.Fatalf("strip kitty mismatch: %q", stripped)
	}
	if stripped := tui.StripImageSequences(iterm2Line); stripped != "abcd" {
		t.Fatalf("strip iterm2 mismatch: %q", stripped)
	}
}

func TestDetectCapabilitiesAndOverrides(t *testing.T) {
	defer tui.ResetCapabilitiesCache()
	defer tui.SetCapabilityOverrides(nil)

	tiTestClearEnv(t)

	// Ghostty
	t.Setenv("TERM_PROGRAM", "ghostty")
	caps := tui.DetectCapabilities(nil)
	if caps != (tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true}) {
		t.Fatalf("ghostty mismatch: %+v", caps)
	}

	// Herdr ignores leaked KITTY_WINDOW_ID and keeps terminal trueColor hint semantics
	tiTestClearEnv(t)
	t.Setenv("TERM_PROGRAM", "Herdr")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("COLORTERM", "24bit")
	caps = tui.DetectCapabilities(nil)
	if caps != (tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true}) {
		t.Fatalf("herdr mismatch: %+v", caps)
	}

	// Rex
	tiTestClearEnv(t)
	t.Setenv("TERM_PROGRAM", "rex")
	caps = tui.DetectCapabilities(nil)
	if caps != (tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true}) {
		t.Fatalf("rex mismatch: %+v", caps)
	}

	// Orca requires both TERM_PROGRAM=orca and ORCA_IMAGE_PROTOCOL=kitty
	tiTestClearEnv(t)
	t.Setenv("TERM_PROGRAM", "orca")
	caps = tui.DetectCapabilities(nil)
	if caps.Images != "" {
		t.Fatalf("orca without ORCA_IMAGE_PROTOCOL should not claim images: %+v", caps)
	}
	t.Setenv("ORCA_IMAGE_PROTOCOL", "kitty")
	caps = tui.DetectCapabilities(nil)
	if caps != (tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true}) {
		t.Fatalf("orca with kitty protocol mismatch: %+v", caps)
	}

	// Warp, iTerm2, WT, Alacritty, Zed, JetBrains
	tiTestClearEnv(t)
	t.Setenv("WARP_SESSION_ID", "warp-1")
	if c := tui.DetectCapabilities(nil); c.Images != tui.ImageProtocolKitty || !c.Hyperlinks {
		t.Fatalf("warp mismatch: %+v", c)
	}
	tiTestClearEnv(t)
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if c := tui.DetectCapabilities(nil); c.Images != tui.ImageProtocolIterm2 {
		t.Fatalf("iterm mismatch: %+v", c)
	}
	tiTestClearEnv(t)
	t.Setenv("ALACRITTY_WINDOW_ID", "123")
	if c := tui.DetectCapabilities(nil); c.Images != "" || !c.TrueColor || !c.Hyperlinks {
		t.Fatalf("alacritty mismatch: %+v", c)
	}
	tiTestClearEnv(t)
	t.Setenv("TERMINAL_EMULATOR", "JetBrains-JediTerm")
	if c := tui.DetectCapabilities(nil); c.Hyperlinks || !c.TrueColor {
		t.Fatalf("jediterm mismatch: %+v", c)
	}

	// tmux probe & PI_* env overrides
	tiTestClearEnv(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("COLORTERM", "truecolor")
	if c := tui.DetectCapabilities(func() bool { return true }); !c.Hyperlinks || c.Images != "" || !c.TrueColor {
		t.Fatalf("tmux true probe mismatch: %+v", c)
	}
	t.Setenv("PI_HYPERLINKS", "0")
	t.Setenv("PI_IMAGE_PROTOCOL", "kitty")
	t.Setenv("PI_TRUE_COLOR", "0")
	if c := tui.DetectCapabilities(func() bool { return true }); c != (tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: false, Hyperlinks: false}) {
		t.Fatalf("env overrides mismatch: %+v", c)
	}

	// Partial programmatic overrides and caching
	tiTestClearEnv(t)
	tui.ResetCapabilitiesCache()
	tui.SetCapabilityOverrides(&tui.TerminalCapabilityOverrides{
		Images:    func() *tui.ImageProtocol { p := tui.ImageProtocolIterm2; return &p }(),
		TrueColor: tiTestBoolPtr(false),
	})
	got := tui.GetCapabilities()
	if got.Images != tui.ImageProtocolIterm2 || got.TrueColor {
		t.Fatalf("overrides mismatch: %+v", got)
	}
	if tui.GetTerminalColorMode(&got) != tui.TerminalColorMode256color {
		t.Fatalf("expected 256color")
	}
	trueColor := true
	if tui.GetTerminalColorMode(&tui.TerminalCapabilities{TrueColor: trueColor}) != tui.TerminalColorModeTruecolor {
		t.Fatalf("expected truecolor")
	}
}

func TestEncodeCropAndRenderImages(t *testing.T) {
	defer tui.ResetCapabilitiesCache()
	defer tui.SetCellDimensions(tui.CellDimensions{WidthPx: 9, HeightPx: 18})

	id := tui.AllocateImageId()
	if id < 1 || id > 0xffffffff {
		t.Fatalf("allocated image id out of range: %d", id)
	}
	if del := tui.DeleteKittyImage(77); del != "\x1b_Ga=d,d=I,i=77,q=2\x1b\\" {
		t.Fatalf("unexpected delete sequence: %q", del)
	}
	if tui.DeleteAllKittyImages() != "\x1b_Ga=d,d=A,q=2\x1b\\" || tui.DeleteAllKittyPlacements() != "\x1b_Ga=d,d=a,q=2\x1b\\" {
		t.Fatalf("unexpected delete-all sequences")
	}

	// Kitty single + chunked transmission
	shortSeq := tui.EncodeKitty("AAAA", &tui.EncodeKittyOptions{
		Columns: 10, Rows: 5, ImageId: 99, MoveCursor: tiTestBoolPtr(false),
	})
	if shortSeq != "\x1b_Ga=T,f=100,q=2,C=1,c=10,r=5,i=99;AAAA\x1b\\" {
		t.Fatalf("unexpected short kitty seq: %q", shortSeq)
	}

	largeData := strings.Repeat("A", 9000)
	chunked := tui.EncodeKitty(largeData, &tui.EncodeKittyOptions{ImageId: 100})
	if !strings.Contains(chunked, "m=1;") || !strings.Contains(chunked, "\x1b_Gm=0;") {
		t.Fatalf("chunked kitty markers missing")
	}

	// Placement extraction + caching
	tui.RegisterKittyImageMetadata(tui.KittyImageMetadata{
		ImageId: 100, Columns: 20, Rows: 6, WidthPx: 180, HeightPx: 108,
	})
	placement := tui.GetKittyImagePlacement("lead" + chunked + "tail")
	if placement == nil || placement.ImageId != 100 || placement.Rows != 6 ||
		placement.EstimatedDecodedBytes != 180*108*4 ||
		placement.Sequence != "\x1b_Ga=p,q=2,i=100\x1b\\" ||
		placement.ReplacementLine != "lead\x1b_Ga=p,q=2,i=100\x1b\\tail" {
		t.Fatalf("placement mismatch: %+v", placement)
	}

	// CropKittyImageLine
	cropped := tui.CropKittyImageLine(shortSeq, 1, 2)
	// Metadata for 99 not registered with 5 rows yet -> register and retest
	tui.RegisterKittyImageMetadata(tui.KittyImageMetadata{
		ImageId: 99, Columns: 10, Rows: 5, WidthPx: 100, HeightPx: 200,
	})
	cropped = tui.CropKittyImageLine(shortSeq, 1, 2)
	if !strings.Contains(cropped, "y=40,h=80,r=2;AAAA\x1b\\") {
		t.Fatalf("cropped kitty line mismatch: %q", cropped)
	}

	// iTerm2 encoding
	it2 := tui.EncodeITerm2("SGVsbG8=", &tui.EncodeITerm2Options{
		Width: 20, Height: "auto", Name: tiTestStrPtr("pic.png"), PreserveAspectRatio: tiTestBoolPtr(false),
	})
	wantName := base64.StdEncoding.EncodeToString([]byte("pic.png"))
	if it2 != "\x1b]1337;File=inline=1;size=5;width=20;height=auto;name="+wantName+";preserveAspectRatio=0:SGVsbG8=\x07" {
		t.Fatalf("unexpected iterm2 sequence: %q", it2)
	}

	// RenderImage in Kitty & iTerm2 & null modes
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	res := tui.RenderImage("AAAA", tui.ImageDimensions{WidthPx: 180, HeightPx: 180}, &tui.ImageRenderOptions{
		MaxWidthCells: tiTestIntPtr(20), ImageId: tiTestIntPtr(1234), MoveCursor: tiTestBoolPtr(false),
	})
	if res == nil || res.Columns != 20 || res.Rows != 10 || res.ImageId == nil || *res.ImageId != 1234 {
		t.Fatalf("RenderImage kitty mismatch: %+v", res)
	}

	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolIterm2, TrueColor: true, Hyperlinks: true})
	res2 := tui.RenderImage("AAAA", tui.ImageDimensions{WidthPx: 180, HeightPx: 180}, &tui.ImageRenderOptions{MaxWidthCells: tiTestIntPtr(20)})
	if res2 == nil || res2.ImageId != nil || !strings.Contains(res2.Sequence, "width=20") {
		t.Fatalf("RenderImage iterm2 mismatch: %+v", res2)
	}

	tui.SetCapabilities(tui.TerminalCapabilities{})
	if r := tui.RenderImage("AAAA", tui.ImageDimensions{WidthPx: 100, HeightPx: 100}, nil); r != nil {
		t.Fatalf("expected nil when images unsupported")
	}
}

func TestImageDimensionParsersAndFallback(t *testing.T) {
	defer tui.ResetCapabilitiesCache()

	png := tiTestMakePNG(640, 480)
	jpeg := tiTestMakeJPEG(800, 600, 0xc2)
	gif := tiTestMakeGIF("GIF89a", 320, 240)
	webpVP8 := tiTestMakeWebP("VP8 ", 1024, 768)
	webpVP8L := tiTestMakeWebP("VP8L", 512, 256)
	webpVP8X := tiTestMakeWebP("VP8X", 1920, 1080)

	if d := tui.GetImageDimensions(png, "image/png"); d == nil || d.WidthPx != 640 || d.HeightPx != 480 {
		t.Fatalf("PNG dimensions mismatch: %+v", d)
	}
	if d := tui.GetImageDimensions(jpeg, "image/jpeg"); d == nil || d.WidthPx != 800 || d.HeightPx != 600 {
		t.Fatalf("JPEG dimensions mismatch: %+v", d)
	}
	if d := tui.GetImageDimensions(gif, "image/gif"); d == nil || d.WidthPx != 320 || d.HeightPx != 240 {
		t.Fatalf("GIF dimensions mismatch: %+v", d)
	}
	if d := tui.GetImageDimensions(webpVP8, "image/webp"); d == nil || d.WidthPx != 1024 || d.HeightPx != 768 {
		t.Fatalf("WebP VP8 dimensions mismatch: %+v", d)
	}
	if d := tui.GetImageDimensions(webpVP8L, "image/webp"); d == nil || d.WidthPx != 512 || d.HeightPx != 256 {
		t.Fatalf("WebP VP8L dimensions mismatch: %+v", d)
	}
	if d := tui.GetImageDimensions(webpVP8X, "image/webp"); d == nil || d.WidthPx != 1920 || d.HeightPx != 1080 {
		t.Fatalf("WebP VP8X dimensions mismatch: %+v", d)
	}
	if d := tui.GetImageDimensions(png, "image/tiff"); d != nil {
		t.Fatalf("unknown mime should return nil")
	}

	// CalculateImageRows
	if rows := tui.CalculateImageRows(tui.ImageDimensions{WidthPx: 90, HeightPx: 36}, 10, nil); rows != 2 {
		t.Fatalf("expected 2 rows, got %d", rows)
	}

	// ImageFallback with HOME shortening and OSC 8 hyperlinks
	t.Setenv("HOME", "/home/alice")
	tui.SetCapabilities(tui.TerminalCapabilities{Hyperlinks: true})
	fb := tui.ImageFallback("image/png", &tui.ImageDimensions{WidthPx: 100, HeightPx: 50}, tiTestStrPtr("/home/alice/my image.png"))
	wantLink := tui.Hyperlink("~/my image.png", "file:///home/alice/my%20image.png")
	if fb != "[Image: "+wantLink+" [image/png] 100x50]" {
		t.Fatalf("fallback with hyperlink mismatch: %q", fb)
	}

	tui.SetCapabilities(tui.TerminalCapabilities{Hyperlinks: false})
	fbNoLink := tui.ImageFallback("image/png", nil, tiTestStrPtr("/home/alice/pic.png"))
	if fbNoLink != "[Image: ~/pic.png [image/png]]" {
		t.Fatalf("fallback without hyperlink mismatch: %q", fbNoLink)
	}
}
