package pets

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	sixelST                        = "\x1b\\"
	sixelBandHeight                = uint32(6)
	sixelPaletteColorCount         = 256
	sixelTransparentAlphaThreshold = byte(128)
	sixelTransparentBackgroundDCS  = "\x1bP9;1;0q"
)

var sixelMaxInt = uint64(^uint(0) >> 1)

func SixelSupported(term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	return strings.Contains(term, "sixel") ||
		strings.Contains(term, "mlterm") ||
		strings.Contains(term, "foot") ||
		term == "wezterm"
}

func EncodeRGBA(rgba []byte, width uint32, height uint32) ([]byte, error) {
	if width == 0 || height == 0 {
		return nil, errors.New("sixel image dimensions must be non-zero")
	}

	count, err := sixelPixelCount(width, height)
	if err != nil {
		return nil, err
	}
	if uint64(count) > sixelMaxInt/4 {
		return nil, errors.New("sixel RGBA buffer length overflow")
	}
	expectedLen := count * 4
	if len(rgba) != expectedLen {
		return nil, fmt.Errorf("sixel RGBA buffer has %d bytes, expected %d", len(rgba), expectedLen)
	}

	palette := sixelPaletteFromRGBA(rgba)
	output := []byte(sixelTransparentBackgroundDCS)
	output = append(output, []byte(fmt.Sprintf("\"1;1;%d;%d", width, height))...)
	palette.writeDefinitions(&output)
	if err := sixelWritePixels(&output, rgba, width, height, palette); err != nil {
		return nil, err
	}
	output = append(output, sixelST...)
	return output, nil
}

func sixelWritePixels(output *[]byte, rgba []byte, width uint32, height uint32, palette sixelPalette) error {
	bandCount := (height + sixelBandHeight - 1) / sixelBandHeight
	for bandIndex := uint32(0); bandIndex < bandCount; bandIndex++ {
		bandTop := bandIndex * sixelBandHeight
		colors, err := sixelActiveColorsForBand(rgba, width, height, bandTop, palette)
		if err != nil {
			return err
		}
		for position, colorIndex := range colors {
			*output = append(*output, []byte(fmt.Sprintf("#%d", colorIndex))...)
			var runChar *byte
			runLen := 0
			for x := uint32(0); x < width; x++ {
				data, err := sixelDataForColumn(rgba, width, height, bandTop, x, colorIndex)
				if err != nil {
					return err
				}
				sixelPushRun(&runChar, &runLen, output, data)
			}
			sixelFlushRun(&runChar, &runLen, output)
			if position+1 < len(colors) {
				*output = append(*output, '$')
			}
		}

		if bandIndex+1 < bandCount {
			if len(colors) == 0 {
				*output = append(*output, '-')
			} else {
				*output = append(*output, '$', '-')
			}
		}
	}
	return nil
}

func sixelActiveColorsForBand(rgba []byte, width uint32, height uint32, bandTop uint32, palette sixelPalette) ([]byte, error) {
	var active [sixelPaletteColorCount]bool
	bandBottom := bandTop + sixelBandHeight
	if bandBottom > height {
		bandBottom = height
	}
	for y := bandTop; y < bandBottom; y++ {
		for x := uint32(0); x < width; x++ {
			colorIndex, ok, err := sixelColorIndexAt(rgba, width, x, y)
			if err != nil {
				return nil, err
			}
			if ok {
				active[colorIndex] = true
			}
		}
	}

	colors := []byte{}
	for _, colorIndex := range palette.indices() {
		if active[colorIndex] {
			colors = append(colors, colorIndex)
		}
	}
	return colors, nil
}

func sixelDataForColumn(rgba []byte, width uint32, height uint32, bandTop uint32, x uint32, colorIndex byte) (byte, error) {
	mask := byte(0)
	for bit := uint32(0); bit < sixelBandHeight; bit++ {
		y := bandTop + bit
		if y >= height {
			continue
		}
		index, ok, err := sixelColorIndexAt(rgba, width, x, y)
		if err != nil {
			return 0, err
		}
		if ok && index == colorIndex {
			mask |= 1 << bit
		}
	}
	return '?' + mask, nil
}

func sixelColorIndexAt(rgba []byte, width uint32, x uint32, y uint32) (byte, bool, error) {
	pixelIndex, err := sixelPixelOffset(width, x, y)
	if err != nil {
		return 0, false, err
	}
	alpha := rgba[pixelIndex+3]
	if alpha < sixelTransparentAlphaThreshold {
		return 0, false, nil
	}
	return sixelRGB332Index(rgba[pixelIndex], rgba[pixelIndex+1], rgba[pixelIndex+2]), true, nil
}

func sixelPushRun(runChar **byte, runLen *int, output *[]byte, value byte) {
	if *runChar != nil && **runChar == value {
		*runLen = *runLen + 1
		return
	}
	sixelFlushRun(runChar, runLen, output)
	valueCopy := value
	*runChar = &valueCopy
	*runLen = 1
}

func sixelFlushRun(runChar **byte, runLen *int, output *[]byte) {
	if *runChar == nil {
		return
	}
	value := **runChar
	if *runLen > 3 {
		*output = append(*output, '!')
		*output = append(*output, []byte(strconv.Itoa(*runLen))...)
		*output = append(*output, value)
	} else {
		for i := 0; i < *runLen; i++ {
			*output = append(*output, value)
		}
	}
	*runChar = nil
	*runLen = 0
}

func sixelPixelOffset(width uint32, x uint32, y uint32) (int, error) {
	pixelIndex := uint64(y)*uint64(width) + uint64(x)
	byteIndex := pixelIndex * 4
	if byteIndex > sixelMaxInt {
		return 0, errors.New("sixel byte index does not fit int")
	}
	return int(byteIndex), nil
}

func sixelPixelCount(width uint32, height uint32) (int, error) {
	count := uint64(width) * uint64(height)
	if count > sixelMaxInt {
		return 0, errors.New("sixel pixel count does not fit int")
	}
	return int(count), nil
}

func sixelRGB332Index(red byte, green byte, blue byte) byte {
	red >>= 5
	green >>= 5
	blue >>= 6
	return (red << 5) | (green << 2) | blue
}

func sixelRGB332Color(index byte) (byte, byte, byte) {
	red := index >> 5
	green := (index >> 2) & 0b111
	blue := index & 0b11
	return sixelScaleBucketToByte(red, 7), sixelScaleBucketToByte(green, 7), sixelScaleBucketToByte(blue, 3)
}

func sixelScaleBucketToByte(bucket byte, max byte) byte {
	value := (uint16(bucket) * 255) / uint16(max)
	return byte(value)
}

func sixelByteToPercent(value byte) byte {
	percent := (uint16(value) * 100) / 255
	if percent > 100 {
		return 100
	}
	return byte(percent)
}

type sixelPalette struct {
	used [sixelPaletteColorCount]bool
}

func sixelPaletteFromRGBA(rgba []byte) sixelPalette {
	var used [sixelPaletteColorCount]bool
	for i := 0; i+3 < len(rgba); i += 4 {
		if rgba[i+3] < sixelTransparentAlphaThreshold {
			continue
		}
		used[sixelRGB332Index(rgba[i], rgba[i+1], rgba[i+2])] = true
	}
	return sixelPalette{used: used}
}

func (p sixelPalette) indices() []byte {
	indices := []byte{}
	for index, used := range p.used {
		if used {
			indices = append(indices, byte(index))
		}
	}
	return indices
}

func (p sixelPalette) writeDefinitions(output *[]byte) {
	for _, colorIndex := range p.indices() {
		red, green, blue := sixelRGB332Color(colorIndex)
		*output = append(*output, []byte(fmt.Sprintf(
			"#%d;2;%d;%d;%d",
			colorIndex,
			sixelByteToPercent(red),
			sixelByteToPercent(green),
			sixelByteToPercent(blue),
		))...)
	}
}
