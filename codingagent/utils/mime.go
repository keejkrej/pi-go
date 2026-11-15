// Ported from packages/coding-agent/src/utils/mime.ts (pi v1.5.2).

package utils

import (
	"encoding/binary"
	"io"
	"os"
)

// mimeSniffBytes bounds header inspection for format detection, large enough to inspect
// PNG chunk headers before IDAT and WebP/JPEG headers without reading full large files.
const mimeSniffBytes = 4100

// IsLlmSupportedImageMimeType reports whether mime is one of the four image MIME types
// accepted natively by vision models (PNG, JPEG, GIF, WebP).
func IsLlmSupportedImageMimeType(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// DetectSupportedImageMimeType inspects raw image header bytes and returns the detected
// MIME type ("image/png", "image/jpeg", "image/gif", "image/webp", or "image/bmp"),
// or "" for unsupported formats (including APNG and JPEG-LS).
func DetectSupportedImageMimeType(data []byte) string {
	if mimeIsPng(data) {
		return "image/png"
	}
	if mimeIsJpeg(data) {
		return "image/jpeg"
	}
	if mimeIsGif(data) {
		return "image/gif"
	}
	if mimeIsWebp(data) {
		return "image/webp"
	}
	if mimeIsBmp(data) {
		return "image/bmp"
	}
	return ""
}

// DetectSupportedImageMimeTypeFromFile reads the leading bytes of filePath and returns
// its detected supported MIME type, or "" if the format is unrecognized or unsupported.
func DetectSupportedImageMimeTypeFromFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, mimeSniffBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	return DetectSupportedImageMimeType(buf[:n]), nil
}

func mimeIsPng(data []byte) bool {
	// 8-byte PNG signature + at least chunk length/type of IHDR (16 bytes total).
	if len(data) < 16 {
		return false
	}
	sig := [8]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	for i := range sig {
		if data[i] != sig[i] {
			return false
		}
	}
	// First chunk must be IHDR with at least 13 bytes.
	ihdrLen := binary.BigEndian.Uint32(data[8:12])
	if ihdrLen != 13 || string(data[12:16]) != "IHDR" {
		return false
	}
	// Reject animated PNG (APNG) carrying an acTL chunk before IDAT.
	offset := 8
	for offset+8 <= len(data) {
		chunkLen := binary.BigEndian.Uint32(data[offset : offset+4])
		chunkType := string(data[offset+4 : offset+8])
		if chunkType == "acTL" {
			return false
		}
		if chunkType == "IDAT" || chunkType == "IEND" {
			break
		}
		next := offset + 12 + int(chunkLen)
		if next <= offset {
			break
		}
		offset = next
	}
	return true
}

func mimeIsJpeg(data []byte) bool {
	if len(data) < 3 || data[0] != 0xff || data[1] != 0xd8 || data[2] != 0xff {
		return false
	}
	// Scan JPEG markers before SOS/EOI to exclude SOF55 / JPEG-LS (0xFF 0xF7).
	pos := 2
	for pos < len(data) {
		if data[pos] != 0xff {
			pos++
			continue
		}
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos >= len(data) {
			break
		}
		marker := data[pos]
		pos++
		if marker == 0xf7 {
			return false
		}
		if marker == 0xd9 || marker == 0xda {
			break
		}
		if marker == 0x00 || marker == 0x01 || marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if pos+2 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if segLen < 2 {
			break
		}
		pos += segLen
	}
	return true
}

func mimeIsGif(data []byte) bool {
	if len(data) < 6 {
		return false
	}
	sig := string(data[:6])
	return sig == "GIF87a" || sig == "GIF89a"
}

func mimeIsWebp(data []byte) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

func mimeIsBmp(data []byte) bool {
	if len(data) < 26 || data[0] != 'B' || data[1] != 'M' {
		return false
	}
	declaredFileSize := binary.LittleEndian.Uint32(data[2:6])
	pixelDataOffset := binary.LittleEndian.Uint32(data[10:14])
	dibHeaderSize := binary.LittleEndian.Uint32(data[14:18])
	if declaredFileSize < 26 || pixelDataOffset < 14+dibHeaderSize || pixelDataOffset > declaredFileSize {
		return false
	}
	if dibHeaderSize == 12 {
		planes := binary.LittleEndian.Uint16(data[22:24])
		bpp := binary.LittleEndian.Uint16(data[24:26])
		if planes != 1 {
			return false
		}
		switch bpp {
		case 1, 4, 8, 24:
			return true
		default:
			return false
		}
	}
	if dibHeaderSize >= 40 && dibHeaderSize <= 124 && len(data) >= 30 {
		planes := binary.LittleEndian.Uint16(data[26:28])
		bpp := binary.LittleEndian.Uint16(data[28:30])
		if planes != 1 {
			return false
		}
		switch bpp {
		case 1, 4, 8, 16, 24, 32:
			return true
		default:
			return false
		}
	}
	return false
}
