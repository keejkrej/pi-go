// Ported from packages/durable/src/tools/image.ts (pi v1.0.0).

package tools

var imagePngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// DetectSupportedImageMimeType detects a supported static image MIME type from magic bytes, or returns "".
func DetectSupportedImageMimeType(buffer []byte) string {
	if imageStartsWith(buffer, []byte{0xff, 0xd8, 0xff}) {
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if imageStartsWith(buffer, imagePngSignature) {
		if imageIsPng(buffer) && !imageIsAnimatedPng(buffer) {
			return "image/png"
		}
		return ""
	}
	if imageStartsWithAscii(buffer, 0, "GIF87a") || imageStartsWithAscii(buffer, 0, "GIF89a") {
		return "image/gif"
	}
	if imageStartsWithAscii(buffer, 0, "RIFF") && imageStartsWithAscii(buffer, 8, "WEBP") {
		return "image/webp"
	}
	if imageStartsWithAscii(buffer, 0, "BM") && imageIsBmp(buffer) {
		return "image/bmp"
	}
	return ""
}

func imageIsPng(buffer []byte) bool {
	return len(buffer) >= 16 &&
		imageReadUint32BE(buffer, len(imagePngSignature)) == 13 &&
		imageStartsWithAscii(buffer, 12, "IHDR")
}

func imageIsAnimatedPng(buffer []byte) bool {
	offset := int64(len(imagePngSignature))
	limit := int64(len(buffer))
	for offset+8 <= limit {
		chunkLength := int64(imageReadUint32BE(buffer, int(offset)))
		chunkTypeOffset := int(offset + 4)
		if imageStartsWithAscii(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if imageStartsWithAscii(buffer, chunkTypeOffset, "IDAT") {
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

func imageIsBmp(buffer []byte) bool {
	if len(buffer) < 26 {
		return false
	}
	declaredFileSize := imageReadUint32LE(buffer, 2)
	pixelDataOffset := imageReadUint32LE(buffer, 10)
	dibHeaderSize := imageReadUint32LE(buffer, 14)
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
		colorPlanes = imageReadUint16LE(buffer, 22)
		bitsPerPixel = imageReadUint16LE(buffer, 24)
	} else if dibHeaderSize >= 40 && dibHeaderSize <= 124 {
		if len(buffer) < 30 {
			return false
		}
		colorPlanes = imageReadUint16LE(buffer, 26)
		bitsPerPixel = imageReadUint16LE(buffer, 28)
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

func imageReadUint16LE(buffer []byte, offset int) uint16 {
	return uint16(buffer[offset]) | uint16(buffer[offset+1])<<8
}

func imageReadUint32BE(buffer []byte, offset int) uint32 {
	return uint32(buffer[offset])<<24 |
		uint32(buffer[offset+1])<<16 |
		uint32(buffer[offset+2])<<8 |
		uint32(buffer[offset+3])
}

func imageReadUint32LE(buffer []byte, offset int) uint32 {
	return uint32(buffer[offset]) |
		uint32(buffer[offset+1])<<8 |
		uint32(buffer[offset+2])<<16 |
		uint32(buffer[offset+3])<<24
}

func imageStartsWith(buffer []byte, prefix []byte) bool {
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

func imageStartsWithAscii(buffer []byte, offset int, text string) bool {
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
