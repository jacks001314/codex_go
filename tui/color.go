package tui

import "math"

// Rust parity: codex-rs/tui/src/color.rs.

func IsLight(bg RGB) bool {
	y := 0.299*float64(bg.R) + 0.587*float64(bg.G) + 0.114*float64(bg.B)
	return y > 128.0
}

func BlendRGB(fg RGB, bg RGB, alpha float64) RGB {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	return RGB{
		R: uint8(float64(fg.R)*alpha + float64(bg.R)*(1-alpha)),
		G: uint8(float64(fg.G)*alpha + float64(bg.G)*(1-alpha)),
		B: uint8(float64(fg.B)*alpha + float64(bg.B)*(1-alpha)),
	}
}

func PerceptualDistance(a RGB, b RGB) float64 {
	x1, y1, z1 := rgbToXYZ(a)
	x2, y2, z2 := rgbToXYZ(b)
	l1, aa1, bb1 := xyzToLab(x1, y1, z1)
	l2, aa2, bb2 := xyzToLab(x2, y2, z2)
	dl := l1 - l2
	da := aa1 - aa2
	db := bb1 - bb2
	return math.Sqrt(dl*dl + da*da + db*db)
}

func rgbToXYZ(color RGB) (float64, float64, float64) {
	r := srgbToLinear(color.R)
	g := srgbToLinear(color.G)
	b := srgbToLinear(color.B)
	x := r*0.4124 + g*0.3576 + b*0.1805
	y := r*0.2126 + g*0.7152 + b*0.0722
	z := r*0.0193 + g*0.1192 + b*0.9505
	return x, y, z
}

func srgbToLinear(value uint8) float64 {
	c := float64(value) / 255.0
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func xyzToLab(x float64, y float64, z float64) (float64, float64, float64) {
	xr := x / 0.95047
	yr := y / 1.00000
	zr := z / 1.08883
	fx := labF(xr)
	fy := labF(yr)
	fz := labF(zr)
	return 116.0*fy - 16.0, 500.0 * (fx - fy), 200.0 * (fy - fz)
}

func labF(value float64) float64 {
	if value > 0.008856 {
		return math.Pow(value, 1.0/3.0)
	}
	return 7.787*value + 16.0/116.0
}
