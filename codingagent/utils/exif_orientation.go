// Ported from packages/coding-agent/src/utils/exif-orientation.ts (pi v1.5.2).

package utils

import (
	"encoding/binary"
	"strings"
)

// ReadExifOrientation inspects JPEG APP1 or WebP EXIF chunks for TIFF tag 0x0112 (Orientation).
// Returns 1..8 when found, or 0 if no valid EXIF orientation tag is present.
func ReadExifOrientation(data []byte, mimeType string) int {
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		return eoReadJpegOrientation(data)
	case "image/webp":
		return eoReadWebpOrientation(data)
	default:
		return 0
	}
}

// ApplyExifOrientation transforms img according to any EXIF orientation tag in the source
// image bytes. Returns the original img unchanged when no transformation is needed.
func ApplyExifOrientation(img *PhotonImage, data []byte, mimeType string) *PhotonImage {
	if img == nil {
		return nil
	}
	switch ReadExifOrientation(data, mimeType) {
	case 2:
		return img.FlipH()
	case 3:
		return img.Rotate180()
	case 4:
		return img.FlipV()
	case 5:
		return img.Rotate90CW().FlipH()
	case 6:
		return img.Rotate90CW()
	case 7:
		return img.Rotate270CW().FlipH()
	case 8:
		return img.Rotate270CW()
	default:
		return img
	}
}

func eoReadJpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 0
	}
	offset := 2
	for offset+2 <= len(data) {
		if data[offset] != 0xff {
			break
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if marker == 0x00 || marker == 0x01 || marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if offset+2 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if segLen < 2 || offset+segLen > len(data) {
			break
		}
		if marker == 0xe1 && segLen >= 8 {
			payload := data[offset+2 : offset+segLen]
			if len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
				if orient := eoParseTiffOrientation(payload[6:]); orient > 0 {
					return orient
				}
			}
		}
		offset += segLen
	}
	return 0
}

func eoReadWebpOrientation(data []byte) int {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0
	}
	offset := 12
	for offset+8 <= len(data) {
		fourcc := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if chunkSize < 0 || offset+8+chunkSize > len(data) {
			break
		}
		if fourcc == "EXIF" {
			payload := data[offset+8 : offset+8+chunkSize]
			if len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
				payload = payload[6:]
			}
			if orient := eoParseTiffOrientation(payload); orient > 0 {
				return orient
			}
		}
		offset += 8 + chunkSize + (chunkSize & 1)
	}
	return 0
}

func eoParseTiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0
	}
	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset < 0 || ifdOffset+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	for i := 0; i < count; i++ {
		entry := ifdOffset + 2 + i*12
		if entry+12 > len(tiff) {
			break
		}
		tag := order.Uint16(tiff[entry : entry+2])
		if tag != 0x0112 {
			continue
		}
		fieldType := order.Uint16(tiff[entry+2 : entry+4])
		valCount := order.Uint32(tiff[entry+4 : entry+8])
		if fieldType == 3 && valCount >= 1 {
			val := int(order.Uint16(tiff[entry+8 : entry+10]))
			if val >= 1 && val <= 8 {
				return val
			}
		}
		return 0
	}
	return 0
}
