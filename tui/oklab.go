// Ported from packages/tui/src/oklab.ts (pi v1.0.0).

package tui

import "math"

// Oklab and OKHSL <-> sRGB conversion. colors.go builds its OKLCH, OKHSL, and color mixing on it.
//
// Oklab and OKHSL are Björn Ottosson's color spaces; OKHSL's saturation is relative to the sRGB gamut at
// each hue and lightness. This is a port of his reference implementation (https://bottosson.github.io/posts/colorpicker/),
// Copyright (c) 2021 Björn Ottosson, used under the MIT license:
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of this software and
// associated documentation files (the "Software"), to deal in the Software without restriction, including
// without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the
// following conditions: The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software. THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY
// KIND, EXPRESS OR IMPLIED.

type oklabVector = [3]float64
type oklabMatrix = [3]oklabVector

func oklabMultiply(m oklabMatrix, v oklabVector) oklabVector {
	var out oklabVector
	for i := range out {
		row := m[i]
		out[i] = row[0]*v[0] + row[1]*v[1] + row[2]*v[2]
	}
	return out
}

func oklabCube(v oklabVector) oklabVector {
	for i := range v {
		v[i] = v[i] * v[i] * v[i]
	}
	return v
}

// oklabLinearSrgbToLms is LINEAR_SRGB_TO_LMS.
var oklabLinearSrgbToLms = oklabMatrix{
	{0.4122214694707629, 0.5363325372617349, 0.0514459932675022},
	{0.2119034958178251, 0.6806995506452344, 0.1073969535369405},
	{0.0883024591900564, 0.2817188391361215, 0.6299787016738222},
}

// oklabLmsToLab is LMS_TO_LAB.
var oklabLmsToLab = oklabMatrix{
	{0.210454268309314, 0.793617774702305, -0.0040720430116193},
	{1.9779985324311684, -2.42859224204858, 0.450593709617411},
	{0.0259040424655478, 0.7827717124575296, -0.8086757549230774},
}

// oklabLabToLms is LAB_TO_LMS.
var oklabLabToLms = oklabMatrix{
	{1, 0.3963377773761749, 0.2158037573099136},
	{1, -0.1055613458156586, -0.0638541728258133},
	{1, -0.0894841775298119, -1.2914855480194092},
}

// oklabLmsToLinearSrgb is LMS_TO_LINEAR_SRGB.
var oklabLmsToLinearSrgb = oklabMatrix{
	{4.0767416360759583, -3.3077115392580629, 0.2309699031821043},
	{-1.2684379732850315, 2.6097573492876882, -0.341319376002657},
	{-0.0041960761386756, -0.7034186179359362, 1.7076146940746117},
}

// oklabSaturationChannel is one SATURATION_FIT row: the (a, b) half-plane where that sRGB channel clips
// first, and the polynomial approximating the maximum saturation there.
type oklabSaturationChannel struct {
	plane [2]float64
	coeff [5]float64
}

// oklabSaturationFit is SATURATION_FIT, in channel order red, green, blue.
var oklabSaturationFit = [3]oklabSaturationChannel{
	{plane: [2]float64{-1.8817031, -0.80936501}, coeff: [5]float64{1.19086277, 1.76576728, 0.59662641, 0.75515197, 0.56771245}},
	{plane: [2]float64{1.8144408, -1.19445267}, coeff: [5]float64{0.73956515, -0.45954404, 0.08285427, 0.12541073, -0.14503204}},
	{plane: [2]float64{0.13110758, 1.81333971}, coeff: [5]float64{1.35733652, -0.00915799, -1.1513021, -0.50559606, 0.00692167}},
}

const (
	oklabK1 = 0.206
	oklabK2 = 0.03
	oklabK3 = (1 + oklabK1) / (1 + oklabK2)
)

// OklabToOkhslLightness converts Oklab lightness to OKHSL lightness.
func OklabToOkhslLightness(x float64) float64 {
	t := oklabK3*x - oklabK1
	return 0.5 * (t + math.Sqrt(t*t+4*oklabK2*oklabK3*x))
}

// oklabOkhslToOklabLightness converts OKHSL lightness to Oklab lightness.
func oklabOkhslToOklabLightness(x float64) float64 {
	return (x*x + oklabK1*x) / (oklabK3 * (x + oklabK2))
}

// oklabLinearToSrgb is the sRGB transfer function: linear to encoded channel, both 0-1.
func oklabLinearToSrgb(value float64) float64 {
	panic("unported: oklabLinearToSrgb")
}

// oklabSrgbToLinear is the inverse sRGB transfer function: encoded to linear channel, both 0-1.
func oklabSrgbToLinear(value float64) float64 {
	panic("unported: oklabSrgbToLinear")
}

// OklabToLinearSrgb converts Oklab [L, a, b] to linear sRGB [r, g, b] (0-1, may leave the gamut).
func OklabToLinearSrgb(lab [3]float64) [3]float64 {
	return oklabMultiply(oklabLmsToLinearSrgb, oklabCube(oklabMultiply(oklabLabToLms, lab)))
}

// oklabLinearSrgbToOklab converts linear sRGB [r, g, b] (0-1) to Oklab [L, a, b].
func oklabLinearSrgbToOklab(rgb [3]float64) [3]float64 {
	lms := oklabMultiply(oklabLinearSrgbToLms, rgb)
	for i := range lms {
		lms[i] = math.Cbrt(lms[i])
	}
	return oklabMultiply(oklabLmsToLab, lms)
}

// RgbToOklab converts sRGB channels (0-255) to Oklab [L, a, b].
func RgbToOklab(rgb RgbColor) [3]float64 {
	panic("unported: RgbToOklab")
}

// LinearSrgbToRgb converts linear sRGB [r, g, b] to sRGB channels (0-255, rounded), clipping out-of-gamut channels.
func LinearSrgbToRgb(linear [3]float64) RgbColor {
	panic("unported: LinearSrgbToRgb")
}

// oklabLmsSlopes is the rate of change of each cube-root LMS component along chroma direction (a, b).
func oklabLmsSlopes(a float64, b float64) [3]float64 {
	var out [3]float64
	for i := range out {
		row := oklabLabToLms[i]
		out[i] = row[1]*a + row[2]*b
	}
	return out
}

// oklabMaxSaturation is the largest saturation (C/L) inside sRGB for hue (a, b).
func oklabMaxSaturation(a float64, b float64) float64 {
	panic("unported: oklabMaxSaturation")
}

// oklabCusp returns the Oklab lightness and chroma of the most saturated sRGB color of hue (a, b).
func oklabCusp(a float64, b float64) (float64, float64) {
	panic("unported: oklabCusp")
}

// oklabMaxChroma is the chroma where the constant-lightness line at lightness leaves the sRGB gamut.
// cuspL and cuspC are the cusp lightness and chroma.
func oklabMaxChroma(a float64, b float64, lightness float64, cuspL float64, cuspC float64) float64 {
	panic("unported: oklabMaxChroma")
}

// oklabChromaStops returns OKHSL's chroma reference points at lightness L and hue (a, b): c0, cMid, cMax.
func oklabChromaStops(L float64, a float64, b float64) (float64, float64, float64) {
	panic("unported: oklabChromaStops")
}

// OkhslToRgb converts OKHSL to sRGB channels (0-255, rounded), clipping out-of-gamut channels.
// hue is in degrees. saturation and lightness are 0-1.
func OkhslToRgb(hue float64, saturation float64, lightness float64) RgbColor {
	panic("unported: OkhslToRgb")
}

// RgbToOkhsl converts sRGB channels (0-255) to OKHSL.
// Hue is in degrees (0 for grays). Saturation and lightness are 0-1.
func RgbToOkhsl(rgb RgbColor) OkhslChannels {
	panic("unported: RgbToOkhsl")
}
