// Ported from packages/durable/src/tools/image.ts (pi v1.0.0).

package tools

var timgPngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// DetectSupportedImageMimeType detects a supported static image MIME type from magic bytes, or returns "".
func DetectSupportedImageMimeType(buffer []byte) string {
	if timgStartsWith(buffer, []byte{0xff, 0xd8, 0xff}) {
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if timgStartsWith(buffer, timgPngSignature) {
		if timgIsPng(buffer) && !timgIsAnimatedPng(buffer) {
			return "image/png"
		}
		return ""
	}
	if timgStartsWithAscii(buffer, 0, "GIF87a") || timgStartsWithAscii(buffer, 0, "GIF89a") {
		return "image/gif"
	}
	if timgStartsWithAscii(buffer, 0, "RIFF") && timgStartsWithAscii(buffer, 8, "WEBP") {
		return "image/webp"
	}
	if timgStartsWithAscii(buffer, 0, "BM") && timgIsBmp(buffer) {
		return "image/bmp"
	}
	return ""
}

func timgIsPng(buffer []byte) bool {
	return len(buffer) >= 16 &&
		timgReadUint32BE(buffer, len(timgPngSignature)) == 13 &&
		timgStartsWithAscii(buffer, 12, "IHDR")
}

func timgIsAnimatedPng(buffer []byte) bool {
	offset := int64(len(timgPngSignature))
	limit := int64(len(buffer))
	for offset+8 <= limit {
		chunkLength := int64(timgReadUint32BE(buffer, int(offset)))
		chunkTypeOffset := int(offset + 4)
		if timgStartsWithAscii(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if timgStartsWithAscii(buffer, chunkTypeOffset, "IDAT") {
			return false
		}
		nextOffset := offset + 8 + chunkLength + 4
		if nextOffset <= offset || nextOffset > limit {
			return false
		}
		offset = nextOffset
	}
	return false
}

func timgIsBmp(buffer []byte) bool {
	if len(buffer) < 26 {
		return false
	}
	declaredFileSize := timgReadUint32LE(buffer, 2)
	pixelDataOffset := timgReadUint32LE(buffer, 10)
	dibHeaderSize := timgReadUint32LE(buffer, 14)
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if uint64(pixelDataOffset) < 14+uint64(dibHeaderSize) {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}

	var colorPlanes uint16
	var bitsPerPixel uint16
	if dibHeaderSize == 12 {
		colorPlanes = timgReadUint16LE(buffer, 22)
		bitsPerPixel = timgReadUint16LE(buffer, 24)
	} else if dibHeaderSize >= 40 && dibHeaderSize <= 124 {
		if len(buffer) < 30 {
			return false
		}
		colorPlanes = timgReadUint16LE(buffer, 26)
		bitsPerPixel = timgReadUint16LE(buffer, 28)
	} else {
		return false
	}
	if colorPlanes != 1 {
		return false
	}
	switch bitsPerPixel {
	case 1, 4, 8, 16, 24, 32:
		return true
	default:
		return false
	}
}

func timgReadUint16LE(buffer []byte, offset int) uint16 {
	return uint16(buffer[offset]) | uint16(buffer[offset+1])<<8
}

func timgReadUint32BE(buffer []byte, offset int) uint32 {
	return uint32(buffer[offset])<<24 |
		uint32(buffer[offset+1])<<16 |
		uint32(buffer[offset+2])<<8 |
		uint32(buffer[offset+3])
}

func timgReadUint32LE(buffer []byte, offset int) uint32 {
	return uint32(buffer[offset]) |
		uint32(buffer[offset+1])<<8 |
		uint32(buffer[offset+2])<<16 |
		uint32(buffer[offset+3])<<24
}

func timgStartsWith(buffer []byte, prefix []byte) bool {
	if len(buffer) < len(prefix) {
		return false
	}
	for i, b := range prefix {
		if buffer[i] != b {
			return false
		}
	}
	return true
}

func timgStartsWithAscii(buffer []byte, offset int, text string) bool {
	if offset < 0 || len(buffer) < offset+len(text) {
		return false
	}
	for i := 0; i < len(text); i++ {
		if buffer[offset+i] != text[i] {
			return false
		}
	}
	return true
}
