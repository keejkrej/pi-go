package utils

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

const (
	testTinyPngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACAQMAAABIeJ9nAAAAIGNIUk0AAHomAACAhAAA+gAAAIDoAAB1MAAA6mAAADqYAAAXcJy6UTwAAAAGUExURf8AAP///0EdNBEAAAABYktHRAH/Ai3eAAAAB3RJTUUH6gEOADM5Ddoh/wAAAAxJREFUCNdjYGBgAAAABAABJzQnCgAAACV0RVh0ZGF0ZTpjcmVhdGUAMjAyNi0wMS0xNFQwMDo1MTo1NyswMDowMOnKzHgAAAAldEVYdGRhdGU6bW9kaWZ5ADIwMjYtMDEtMTRUMDA6NTE6NTcrMDA6MDCYl3TEAAAAKHRFWHRkYXRlOnRpbWVzdGFtcAAyMDI2LTAxLTE0VDAwOjUxOjU3KzAwOjAwz4JVGwAAAABJRU5ErkJggg=="
	testTinyJpgB64 = "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAMCAgMCAgMDAwMEAwMEBQgFBQQEBQoHBwYIDAoMDAsKCwsNDhIQDQ4RDgsLEBYQERMUFRUVDA8XGBYUGBIUFRT/2wBDAQMEBAUEBQkFBQkUDQsNFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBT/wAARCAACAAIDAREAAhEBAxEB/8QAFAABAAAAAAAAAAAAAAAAAAAACf/EABQQAQAAAAAAAAAAAAAAAAAAAAD/xAAVAQEBAAAAAAAAAAAAAAAAAAAGCf/EABQRAQAAAAAAAAAAAAAAAAAAAAD/2gAMAwEAAhEDEQA/AD3VTB3/2Q=="
	testTinyJpg2x1 = "/9j/4AAQSkZJRgABAgAAAQABAAD/wAARCAABAAIDAREAAhEBAxEB/9sAQwADAgIDAgIDAwMDBAMDBAUIBQUEBAUKBwcGCAwKDAwLCgsLDQ4SEA0OEQ4LCxAWEBETFBUVFQwPFxgWFBgSFBUU/9sAQwEDBAQFBAUJBQUJFA0LDRQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQU/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD4H8Q/8h/Uv+vmX/0M1/o1wJ/ySWU/9g1D/wBNRMOM/wDkp8z/AOv9b/05I//Z"
	testMedPng100  = "iVBORw0KGgoAAAANSUhEUgAAAGQAAABkCAAAAABVicqIAAAAAmJLR0QA/4ePzL8AAAAHdElNRQfqAQ4AMzkN2iH/AAAAP0lEQVRo3u3NQQEAAAQEMASXXYrz2gqst/Lm4ZBIJBKJRCKRSCQSiUQikUgkEolEIpFIJBKJRCKRSCQSiSTsAP1cAUZeKtreAAAAJXRFWHRkYXRlOmNyZWF0ZQAyMDI2LTAxLTE0VDAwOjUxOjU3KzAwOjAw6crMeAAAACV0RVh0ZGF0ZTptb2RpZnkAMjAyNi0wMS0xNFQwMDo1MTo1NyswMDowMJiXdMQAAAAodEVYdGRhdGU6dGltZXN0YW1wADIwMjYtMDEtMTRUMDA6NTE6NTcrMDA6MDDPglUbAAAAAElFTkSuQmCC"
	testLarge200   = "iVBORw0KGgoAAAANSUhEUgAAAMgAAADIEAIAAAByquWKAAAAIGNIUk0AAHomAACAhAAA+gAAAIDoAAB1MAAA6mAAADqYAAAXcJy6UTwAAAAGYktHRP///////wlY99wAAAAHdElNRQfqAQ4AMzqU03BFAAAEEElEQVR42u3dvWqUURSF4XXMRAyIhSAWQvwnWtjaWusFx9bW1kKD/wELESxEiBjDjJdwysOC57mCXW32+zGcGbtdACpsdi+T8Xz1GABzm91xMl6sHgNgblzcT/Y+rh4DYG6cv0/2H64eA2BOEgI1fHQHaoyzbXIwVo8BMCcJgRqSEKgxft9Lrn5aPQbA3Ph1klw7Wj0GwJwkBGpYWECN8fMiub63egyAORcWUMPCAmqMH3eTG59XjwEwN76/S24+Wj0GwJwkBGpYWECN8e1fcmuzegyAORcWUMPCAmqM09vJ4dfVYwDMjS9vkzuPV48BMCcJgRqeSAZqjA/nyYP91WMAzElCoIYkBGqMk8Pk6HT1GABzm+3L5JIkBApIQqCGj+5AjfHmb/Lk8uoxAOYkIVBDEgI1LCygxmZ7nFyShEABFxZQw8ICaozXf5KnV1aPATDnwgJqWFhADQsLqOFnDUANFxZQw8ICaoxXZ8mzg9VjAMy5sIAaFhZQw3tYQA1/QgHUkIRADUkI1JCEQA1JCNSQhEANFxZQwzcsoIYkBGpIQqCGJARqSEKghiQEalhYQA1/QgHUcGEBNSwsoIYkBGq4sIAaFhZQw8ICaviGBdRwYQE1LCyghiQEariwgBoWFlDDe1hADS+OAjUkIVBDEgI1JCFQQxICNSQhUMOFBdTwDQuoIQmBGpIQqCEJgRqSEKghCYEaFhZQw4ujQA0XFlDDwgJqSEKghgsLqGFhATUsLKCGb1hADRcWUMPCAmpIQqCGCwuoYWEBNbyHBdTw4ihQQxICNSQhUEMSAjUkIVBDEgI1XFhADd+wgBqSEKghCYEakhCoIQmBGpIQqGFhATW8OArUcGEBNSwsoIYkBGq4sIAaFhZQw8ICaviGBdRwYQE1LCyghiQEariwgBoWFlDDe1hADS+OAjUkIVBDEgI1JCFQQxICNSQhUMOFBdTwDQuoIQmBGpIQqCEJgRqSEKghCYEa49lB8ups9RgAc14cBWpIQqCGhQXUkIRADRcWUMPCAmqMp1eS139WjwEw5xsWUEMSAjUsLKCGJARquLCAGhYWUGM8uZy8+bt6DIA5L44CNSQhUMMTyUCNcXSanByuHgNgThICNSQhUGM82E8+nK8eA2DOzxqAGpIQqDHuPE6+vF09BsDcOPyanN5ePQbAnCQEavgdFlBj3Nok3/6tHgNgzoujQA1JCNQYNx8l39+tHgNgbtz4nPy4u3oMgDlJCNSwsIAa4/pe8vNi9RgAc37WANSQhECNce0o+XWyegyAuXH1U/L73uoxAOYkIVDDwgJqjIORnG1XjwEw508ogBqSEKgx9h8m5+9XjwEwN/Y+Jhf3V48BMCcJgRpjPE+2x6vHAJgbSbLbrR4DYO4/GqiSgXN+ksgAAAAldEVYdGRhdGU6Y3JlYXRlADIwMjYtMDEtMTRUMDA6NTE6NTcrMDA6MDDpysx4AAAAJXRFWHRkYXRlOm1vZGlmeQAyMDI2LTAxLTE0VDAwOjUxOjU3KzAwOjAwmJd0xAAAACh0RVh0ZGF0ZTp0aW1lc3RhbXAAMjAyNi0wMS0xNFQwMDo1MTo1NyswMDowMM+CVRsAAAAASUVORK5CYII="
	test1x1PngB64  = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
)

func testDecodeB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testMakePngChunk(chunkType string, body []byte) []byte {
	out := make([]byte, 8+len(body)+4)
	binary.BigEndian.PutUint32(out[0:4], uint32(len(body)))
	copy(out[4:8], chunkType)
	copy(out[8:8+len(body)], body)
	sum := crc32.ChecksumIEEE(out[4 : 8+len(body)])
	binary.BigEndian.PutUint32(out[8+len(body):], sum)
	return out
}

func testCreatePng(t *testing.T, w, h int) []byte {
	t.Helper()
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8] = 8 // 8-bit grayscale
	ihdr[9] = 0

	raw := make([]byte, (w+1)*h)
	for row := 0; row < h; row++ {
		for col := 1; col < w+1; col++ {
			raw[row*(w+1)+col] = byte(row % 256)
		}
	}
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	_, _ = zw.Write(raw)
	_ = zw.Close()

	var buf bytes.Buffer
	buf.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	buf.Write(testMakePngChunk("IHDR", ihdr))
	buf.Write(testMakePngChunk("IDAT", zbuf.Bytes()))
	buf.Write(testMakePngChunk("IEND", nil))
	return buf.Bytes()
}

func testApp1Segment(payload []byte) []byte {
	seg := make([]byte, len(payload)+4)
	seg[0] = 0xff
	seg[1] = 0xe1
	binary.BigEndian.PutUint16(seg[2:4], uint16(len(payload)+2))
	copy(seg[4:], payload)
	return seg
}

func testJpegWithXmpBeforeOrientation(t *testing.T) string {
	t.Helper()
	jpeg := testDecodeB64(t, testTinyJpg2x1)
	xmp := testApp1Segment([]byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta xmlns:x=\"adobe:ns:meta/\"/>"))
	exifTiff := []byte{
		0x49, 0x49, 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x12, 0x01, 0x03, 0x00, 0x01, 0x00,
		0x00, 0x00, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
	}
	orient6 := testApp1Segment(append([]byte("Exif\x00\x00"), exifTiff...))
	var combined bytes.Buffer
	combined.Write(jpeg[:2])
	combined.Write(xmp)
	combined.Write(orient6)
	combined.Write(jpeg[2:])
	return base64.StdEncoding.EncodeToString(combined.Bytes())
}

func testPhotoLikeJpeg(t *testing.T, width, height int) []byte {
	t.Helper()
	pixels := make([]byte, width*height*4)
	seed := uint32(1)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			seed = (seed*1103515245 + 12345) & 0x7fffffff
			noise := byte(seed % 6)
			i := (y*width + x) * 4
			pixels[i] = byte(float64(x)/float64(width)*200) + noise
			pixels[i+1] = byte(float64(y)/float64(height)*200) + noise
			pixels[i+2] = byte(128+math.Sin(float64(x)/30)*60) + noise
			pixels[i+3] = 255
		}
	}
	img, err := NewPhotonImage(pixels, width, height)
	if err != nil {
		t.Fatal(err)
	}
	b, err := img.GetBytesJpeg(85)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMimeDetectionAndProcessImage(t *testing.T) {
	for _, sig := range []string{"GIF87a", "GIF89a"} {
		if got := DetectSupportedImageMimeType([]byte(sig)); got != "image/gif" {
			t.Fatalf("expected image/gif for %s, got %q", sig, got)
		}
	}
	bmp := testCreateTinyBmp1x1()
	if got := DetectSupportedImageMimeType(bmp); got != "image/bmp" {
		t.Fatalf("expected image/bmp, got %q", got)
	}
	// Reject APNG
	apng := testCreatePng(t, 2, 2)
	// Splice an acTL chunk right after IHDR (byte 33).
	var apngBuf bytes.Buffer
	apngBuf.Write(apng[:33])
	apngBuf.Write(testMakePngChunk("acTL", make([]byte, 8)))
	apngBuf.Write(apng[33:])
	if got := DetectSupportedImageMimeType(apngBuf.Bytes()); got != "" {
		t.Fatalf("expected APNG rejection, got %q", got)
	}
	// Reject JPEG-LS (0xFF 0xF7)
	jpegls := []byte{0xff, 0xd8, 0xff, 0xf7, 0x00, 0x0b, 0x08}
	if got := DetectSupportedImageMimeType(jpegls); got != "" {
		t.Fatalf("expected JPEG-LS rejection, got %q", got)
	}
	// File sniffing
	tmp := t.TempDir()
	pngPath := filepath.Join(tmp, "tiny.png")
	if err := os.WriteFile(pngPath, testDecodeB64(t, testTinyPngB64), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := DetectSupportedImageMimeTypeFromFile(pngPath); err != nil || got != "image/png" {
		t.Fatalf("DetectSupportedImageMimeTypeFromFile = (%q, %v)", got, err)
	}

	autoFalse := false
	resNoResize := ProcessImage(bmp, "image/bmp", &ProcessImageOptions{AutoResizeImages: &autoFalse})
	if !resNoResize.Ok || resNoResize.MimeType != "image/png" || len(resNoResize.Hints) == 0 ||
		resNoResize.Hints[0] != "[Image converted from image/bmp to image/png.]" {
		t.Fatalf("BMP no-resize mismatch: %+v", resNoResize)
	}

	resResize := ProcessImage(bmp, "image/bmp", nil)
	if !resResize.Ok || resResize.MimeType != "image/png" {
		t.Fatalf("BMP resize mismatch: %+v", resResize)
	}
}

func TestConvertAndResizeImages(t *testing.T) {
	// ConvertToPng
	samePng, err := ConvertToPng(testTinyPngB64, "image/png")
	if err != nil || samePng.Data != testTinyPngB64 || samePng.MimeType != "image/png" {
		t.Fatalf("PNG pass-through mismatch: %+v err=%v", samePng, err)
	}
	fromJpg, err := ConvertToPng(testTinyJpgB64, "image/jpeg")
	if err != nil || fromJpg.MimeType != "image/png" {
		t.Fatalf("JPEG->PNG failed: %+v err=%v", fromJpg, err)
	}
	pngBytes := testDecodeB64(t, fromJpg.Data)
	if pngBytes[0] != 0x89 || pngBytes[1] != 'P' || pngBytes[2] != 'N' || pngBytes[3] != 'G' {
		t.Fatal("invalid PNG magic header")
	}

	// EXIF orientation after XMP APP1
	oriented, err := ConvertToPng(testJpegWithXmpBeforeOrientation(t), "image/jpeg")
	if err != nil || oriented == nil {
		t.Fatalf("oriented ConvertToPng failed: %v", err)
	}
	oPng := testDecodeB64(t, oriented.Data)
	w := binary.BigEndian.Uint32(oPng[16:20])
	h := binary.BigEndian.Uint32(oPng[20:24])
	if w != 1 || h != 2 {
		t.Fatalf("expected oriented 1x2 dimensions, got %dx%d", w, h)
	}

	// Transcoder synchronous conversion + error returns nil
	transcoder := LoadPngTranscoder()
	if transcoder == nil {
		t.Fatal("expected non-nil PNG transcoder")
	}
	if bad := transcoder(base64.StdEncoding.EncodeToString([]byte("not an image")), "image/jpeg"); bad != nil {
		t.Fatalf("expected nil on invalid image, got %v", *bad)
	}

	// Resize preserves input slice & returns untouched within limits
	in := testDecodeB64(t, testTinyPngB64)
	origFirst := in[0]
	max100 := 100
	maxCap := 1024 * 1024
	resTiny, err := ResizeImage(in, "image/png", &ImageResizeOptions{MaxWidth: &max100, MaxHeight: &max100, MaxBytes: &maxCap})
	if err != nil || resTiny == nil || resTiny.WasResized || resTiny.Data != testTinyPngB64 {
		t.Fatalf("within-limits resize mismatch: %+v err=%v", resTiny, err)
	}
	if in[0] != origFirst {
		t.Fatal("caller input slice mutated")
	}

	// Exceeding dimensions
	max50 := 50
	resMed, err := ResizeImage(testDecodeB64(t, testMedPng100), "image/png", &ImageResizeOptions{MaxWidth: &max50, MaxHeight: &max50, MaxBytes: &maxCap})
	if err != nil || resMed == nil || !resMed.WasResized || resMed.OriginalWidth != 100 || resMed.Width > 50 || resMed.Height > 50 {
		t.Fatalf("dimension resize mismatch: %+v err=%v", resMed, err)
	}

	// Exceeding byte limit
	largeRaw := testDecodeB64(t, testLarge200)
	budget := len(largeRaw) * 9 / 10
	max2k := 2000
	resBytes, err := ResizeImage(largeRaw, "image/png", &ImageResizeOptions{MaxWidth: &max2k, MaxHeight: &max2k, MaxBytes: &budget})
	if err != nil || resBytes == nil || len(testDecodeB64(t, resBytes.Data)) > budget {
		t.Fatalf("byte-limit resize failed: res=%+v err=%v", resBytes, err)
	}

	// Impossible maxBytes=1 -> nil, nil
	maxOne := 1
	resImpossible, err := ResizeImage(testDecodeB64(t, testLarge200), "image/png", &ImageResizeOptions{MaxWidth: &max2k, MaxHeight: &max2k, MaxBytes: &maxOne})
	if err != nil || resImpossible != nil {
		t.Fatalf("expected (nil, nil) on maxBytes=1, got (%+v, %v)", resImpossible, err)
	}

	// Regression #11551: picks JPEG over larger PNG for photo-like inputs
	photo := testPhotoLikeJpeg(t, 1200, 800)
	max1k := 1000
	resPhoto, err := ResizeImageInProcess(photo, "image/jpeg", &ImageResizeOptions{MaxWidth: &max1k, MaxHeight: &max1k})
	if err != nil || resPhoto == nil || !resPhoto.WasResized || resPhoto.MimeType != "image/jpeg" || resPhoto.Width != 1000 {
		t.Fatalf("#11551 photo resize mismatch: %+v err=%v", resPhoto, err)
	}

	// Serial calls work cleanly
	tiny1x1 := testDecodeB64(t, test1x1PngB64)
	for i := 0; i < 2; i++ {
		r, err := ResizeImage(tiny1x1, "image/png", nil)
		if err != nil || r == nil {
			t.Fatalf("serial resize %d failed: %v", i, err)
		}
	}

	// FormatDimensionNote
	if note := FormatDimensionNote(ResizedImage{WasResized: false}); note != nil {
		t.Fatalf("expected nil note for unresized image, got %q", *note)
	}
	note := FormatDimensionNote(ResizedImage{OriginalWidth: 2000, OriginalHeight: 1000, Width: 1000, Height: 500, WasResized: true})
	if note == nil || !strings.Contains(*note, "original 2000x1000") || !strings.Contains(*note, "displayed at 1000x500") || !strings.Contains(*note, "2.00") {
		t.Fatalf("unexpected FormatDimensionNote: %v", note)
	}
}

func TestNormalizeToolResultImages(t *testing.T) {
	noImg := []ToolResultContent{&ai.TextContent{Type: "text", Text: "no images"}}
	if got := NormalizeToolResultImages(noImg, nil); &got[0] != &noImg[0] {
		t.Fatal("expected identical slice when no image blocks exist")
	}

	within := []ToolResultContent{
		&ai.TextContent{Type: "text", Text: "screenshot"},
		&ai.ImageContent{Type: "image", Data: test1x1PngB64, MimeType: "image/png"},
	}
	if got := NormalizeToolResultImages(within, nil); &got[0] != &within[0] {
		t.Fatal("expected identical slice when within limits")
	}

	// Oversized 2400x4800 PNG
	oversized := testCreatePng(t, 2400, 4800)
	overContent := []ToolResultContent{
		&ai.ImageContent{Type: "image", Data: base64.StdEncoding.EncodeToString(oversized), MimeType: "image/png"},
	}
	normalized := NormalizeToolResultImages(overContent, nil)
	if len(normalized) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(normalized))
	}
	note, ok := normalized[1].(*ai.TextContent)
	if !ok || !strings.Contains(note.Text, "original 2400x4800") {
		t.Fatalf("expected dimension note, got %#v", normalized[1])
	}

	// Undecodable image preserved
	undecodable := []ToolResultContent{
		&ai.ImageContent{Type: "image", Data: base64.StdEncoding.EncodeToString([]byte("not-an-image")), MimeType: "image/png"},
	}
	if got := NormalizeToolResultImages(undecodable, nil); &got[0] != &undecodable[0] {
		t.Fatal("expected undecodable content preserved unchanged")
	}

	// Surrounded by text blocks preserves order
	wide := testCreatePng(t, 2400, 100)
	surrounded := []ToolResultContent{
		&ai.TextContent{Type: "text", Text: "before"},
		&ai.ImageContent{Type: "image", Data: base64.StdEncoding.EncodeToString(wide), MimeType: "image/png"},
		&ai.TextContent{Type: "text", Text: "after"},
	}
	normSurr := NormalizeToolResultImages(surrounded, nil)
	if len(normSurr) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(normSurr))
	}
	if normSurr[0].(*ai.TextContent).Text != "before" || normSurr[3].(*ai.TextContent).Text != "after" {
		t.Fatalf("surrounding order broken: %#v", normSurr)
	}
}
