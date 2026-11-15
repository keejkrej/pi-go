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
	if value > 0.0031308 {
		return 1.055*math.Pow(value, 1.0/2.4) - 0.055
	}
	return 12.92 * value
}

// oklabSrgbToLinear is the inverse sRGB transfer function: encoded to linear channel, both 0-1.
func oklabSrgbToLinear(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
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
	return oklabLinearSrgbToOklab([3]float64{
		oklabSrgbToLinear(rgb.R / 255),
		oklabSrgbToLinear(rgb.G / 255),
		oklabSrgbToLinear(rgb.B / 255),
	})
}

// LinearSrgbToRgb converts linear sRGB [r, g, b] to sRGB channels (0-255, rounded), clipping out-of-gamut channels.
func LinearSrgbToRgb(linear [3]float64) RgbColor {
	clampByte := func(v float64) float64 {
		c := math.Min(1, math.Max(0, oklabLinearToSrgb(v)))
		r := math.Round(c * 255)
		if r == 0 {
			return 0
		}
		return r
	}
	return RgbColor{
		R: clampByte(linear[0]),
		G: clampByte(linear[1]),
		B: clampByte(linear[2]),
	}
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
	channel := 2
	for index := 0; index < 2; index++ {
		plane := oklabSaturationFit[index].plane
		if plane[0]*a+plane[1]*b > 1 {
			channel = index
			break
		}
	}
	k := oklabSaturationFit[channel].coeff
	weights := oklabLmsToLinearSrgb[channel]
	saturation := k[0] + k[1]*a + k[2]*b + k[3]*a*a + k[4]*a*b

	slopes := oklabLmsSlopes(a, b)
	var f, f1, f2 float64
	for i := 0; i < 3; i++ {
		base := 1 + saturation*slopes[i]
		w := weights[i]
		f += w * base * base * base
		f1 += w * 3 * slopes[i] * base * base
		f2 += w * 6 * slopes[i] * slopes[i] * base
	}
	return saturation - (f*f1)/(f1*f1-0.5*f*f2)
}

// oklabCusp returns the Oklab lightness and chroma of the most saturated sRGB color of hue (a, b).
func oklabCusp(a float64, b float64) (float64, float64) {
	saturation := oklabMaxSaturation(a, b)
	srgb := OklabToLinearSrgb([3]float64{1, saturation * a, saturation * b})
	maxComp := math.Max(srgb[0], math.Max(srgb[1], srgb[2]))
	lightness := math.Cbrt(1 / maxComp)
	return lightness, lightness * saturation
}

// oklabMaxChroma is the chroma where the constant-lightness line at lightness leaves the sRGB gamut.
// cuspL and cuspC are the cusp lightness and chroma.
func oklabMaxChroma(a float64, b float64, lightness float64, cuspL float64, cuspC float64) float64 {
	if lightness <= cuspL {
		return (cuspC * lightness) / cuspL
	}
	// Upper half: triangle edge, then one Halley step against each channel reaching 1.
	t := (cuspC * (lightness - 1)) / (cuspL - 1)
	slopes := oklabLmsSlopes(a, b)
	var cubes, first, second [3]float64
	for i := 0; i < 3; i++ {
		lms := lightness + t*slopes[i]
		cubes[i] = lms * lms * lms
		first[i] = 3 * slopes[i] * lms * lms
		second[i] = 6 * slopes[i] * slopes[i] * lms
	}
	dot := func(row, vals [3]float64) float64 {
		return row[0]*vals[0] + row[1]*vals[1] + row[2]*vals[2]
	}
	minStep := math.MaxFloat64
	for _, row := range oklabLmsToLinearSrgb {
		f := dot(row, cubes) - 1
		f1 := dot(row, first)
		f2 := dot(row, second)
		u := f1 / (f1*f1 - 0.5*f*f2)
		step := math.MaxFloat64
		if u >= 0 {
			step = -f * u
		}
		if step < minStep {
			minStep = step
		}
	}
	return t + minStep
}

// oklabChromaStops returns OKHSL's chroma reference points at lightness L and hue (a, b): c0, cMid, cMax.
func oklabChromaStops(L float64, a float64, b float64) (float64, float64, float64) {
	cuspL, cuspC := oklabCusp(a, b)
	cMax := oklabMaxChroma(a, b, L, cuspL, cuspC)
	scale := math.Min(L*(cuspC/cuspL), (1-L)*(cuspC/(1-cuspL)))
	k := cMax / scale
	midS := 0.11516993 +
		1/(7.4477897+
			4.1590124*b+
			a*(-2.19557347+
				1.75198401*b+
				a*(-2.13704948-10.02301043*b+a*(-4.24894561+5.38770819*b+4.69891013*a))))
	midT := 0.11239642 +
		1/(1.6132032-
			0.68124379*b+
			a*(0.40370612+
				0.90148123*b+
				a*(-0.27087943+0.6122399*b+a*(0.00299215-0.45399568*b-0.14661872*a))))
	cMid := 0.9 * k * math.Sqrt(math.Sqrt(1/(math.Pow(L*midS, -4)+math.Pow((1-L)*midT, -4))))
	c0 := math.Sqrt(1 / (math.Pow(L*0.4, -2) + math.Pow((1-L)*0.8, -2)))
	return c0, cMid, cMax
}

// OkhslToRgb converts OKHSL to sRGB channels (0-255, rounded), clipping out-of-gamut channels.
// hue is in degrees. saturation and lightness are 0-1.
func OkhslToRgb(hue float64, saturation float64, lightness float64) RgbColor {
	L := oklabOkhslToOklabLightness(lightness)
	lab := [3]float64{L, 0, 0}
	if L > 0 && L < 1 && saturation > 0 {
		normHue := math.Mod(math.Mod(hue, 360)+360, 360)
		angle := (2 * math.Pi * normHue) / 360
		a := math.Cos(angle)
		b := math.Sin(angle)
		c0, cMid, cMax := oklabChromaStops(L, a, b)
		var chroma float64
		if saturation < 0.8 {
			t := 1.25 * saturation
			k1 := 0.8 * c0
			chroma = (t * k1) / (1 - (1-k1/cMid)*t)
		} else {
			t := 5 * (saturation - 0.8)
			k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
			chroma = cMid + (t*k1)/(1-(1-k1/(cMax-cMid))*t)
		}
		lab = [3]float64{L, chroma * a, chroma * b}
	}
	return LinearSrgbToRgb(OklabToLinearSrgb(lab))
}

// RgbToOkhsl converts sRGB channels (0-255) to OKHSL.
// Hue is in degrees (0 for grays). Saturation and lightness are 0-1.
func RgbToOkhsl(rgb RgbColor) OkhslChannels {
	lab := RgbToOklab(rgb)
	L, labA, labB := lab[0], lab[1], lab[2]
	chroma := math.Hypot(labA, labB)
	lightness := OklabToOkhslLightness(L)
	if chroma < 1e-9 || lightness <= 0 || lightness >= 1 {
		return OkhslChannels{H: 0, S: 0, L: lightness}
	}
	hue := math.Mod((math.Atan2(labB, labA)*180)/math.Pi+360, 360)
	c0, cMid, cMax := oklabChromaStops(L, labA/chroma, labB/chroma)
	var saturation float64
	if chroma < cMid {
		k1 := 0.8 * c0
		saturation = 0.8 * (chroma / (k1 + (1-k1/cMid)*chroma))
	} else {
		k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
		offset := chroma - cMid
		saturation = 0.8 + 0.2*(offset/(k1+(1-k1/(cMax-cMid))*offset))
	}
	return OkhslChannels{
		H: hue,
		S: math.Min(1, math.Max(0, saturation)),
		L: lightness,
	}
}
