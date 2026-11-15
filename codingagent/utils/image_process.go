// Ported from packages/coding-agent/src/utils/image-process.ts (pi v1.5.2).

package utils

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// ProcessImageOptions configures whether images are auto-resized and which resize profile applies.
type ProcessImageOptions struct {
	AutoResizeImages *bool
	Resize           *ImageResizeOptions
}

// ProcessImageResult represents the processed image payload and any informational hints.
type ProcessImageResult struct {
	Ok           bool
	Data         string
	MimeType     string
	Hints        []string
	Message      string
	decodeFailed bool
}

func imgprocBaseMimeType(mimeType string) string {
	trimmed := strings.TrimSpace(mimeType)
	if idx := strings.IndexByte(trimmed, ';'); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	lower := strings.ToLower(trimmed)
	if lower == "image/jpg" {
		return "image/jpeg"
	}
	return lower
}

// ProcessImage validates/converts non-LLM image formats (e.g. BMP -> PNG) and optionally
// resizes images to fit model input limits.
func ProcessImage(data []byte, mimeType string, options *ProcessImageOptions) ProcessImageResult {
	autoResize := true
	if options != nil && options.AutoResizeImages != nil {
		autoResize = *options.AutoResizeImages
	}
	var resizeOpts *ImageResizeOptions
	if options != nil {
		resizeOpts = options.Resize
	}

	workingData := data
	workingMime := imgprocBaseMimeType(mimeType)
	var hints []string

	if !IsLlmSupportedImageMimeType(workingMime) {
		pngBytes, err := ConvertImageBytesToPng(workingData)
		if err != nil || len(pngBytes) == 0 {
			return ProcessImageResult{
				Ok:           false,
				Message:      "[Image omitted: could not be converted to a supported inline image format.]",
				decodeFailed: true,
			}
		}
		hints = append(hints, fmt.Sprintf("[Image converted from %s to image/png.]", workingMime))
		workingData = pngBytes
		workingMime = "image/png"
	}

	if autoResize {
		resized, err := ResizeImage(workingData, workingMime, resizeOpts)
		if err != nil {
			return ProcessImageResult{
				Ok:           false,
				Message:      "[Image omitted: could not be resized below the inline image size limit.]",
				decodeFailed: true,
			}
		}
		if resized == nil {
			return ProcessImageResult{
				Ok:      false,
				Message: "[Image omitted: could not be resized below the inline image size limit.]",
			}
		}
		workingMime = resized.MimeType
		if note := FormatDimensionNote(*resized); note != nil {
			hints = append(hints, *note)
		}
		return ProcessImageResult{
			Ok:       true,
			Data:     resized.Data,
			MimeType: workingMime,
			Hints:    hints,
		}
	}

	return ProcessImageResult{
		Ok:       true,
		Data:     base64.StdEncoding.EncodeToString(workingData),
		MimeType: workingMime,
		Hints:    hints,
	}
}
