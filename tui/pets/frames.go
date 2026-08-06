package pets

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
)

func FrameAt(frames []string, index int) string {
	if len(frames) == 0 {
		return ""
	}
	if index < 0 {
		index = -index
	}
	return frames[index%len(frames)]
}

// PreparePNGFrames slices a spritesheet into per-frame PNG files under frameDir
// and returns the expected frame paths in row-major order. Existing complete
// caches are reused; stale frame_*.png files are removed before regeneration,
// mirroring the Rust frame-cache behavior.
func PreparePNGFrames(spritesheetPath string, frameDir string, frameWidth int, frameHeight int, columns int, rows int) ([]string, error) {
	if err := os.MkdirAll(frameDir, 0o755); err != nil {
		return nil, err
	}
	frameCount := columns * rows
	if frameCount < 1 {
		return nil, fmt.Errorf("invalid pet frame grid %dx%d", columns, rows)
	}
	expected := make([]string, frameCount)
	complete := true
	for index := 0; index < frameCount; index++ {
		path := filepath.Join(frameDir, fmt.Sprintf("frame_%03d.png", index))
		expected[index] = path
		if _, err := os.Stat(path); err != nil {
			complete = false
		}
	}
	if complete {
		return expected, nil
	}
	if err := removeStaleFrameFiles(frameDir); err != nil {
		return nil, err
	}
	file, err := os.Open(spritesheetPath)
	if err != nil {
		return nil, err
	}
	source, _, err := image.Decode(file)
	file.Close()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", spritesheetPath, err)
	}
	sourceBounds := source.Bounds()
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			index := row*columns + column
			x := column * frameWidth
			y := row * frameHeight
			if x < 0 || y < 0 || x+frameWidth > sourceBounds.Dx() || y+frameHeight > sourceBounds.Dy() {
				return nil, fmt.Errorf("pet frame %d (%d,%d %dx%d) exceeds spritesheet %v", index, x, y, frameWidth, frameHeight, sourceBounds)
			}
			frame := image.NewRGBA(image.Rect(0, 0, frameWidth, frameHeight))
			draw.Draw(frame, frame.Bounds(), source, image.Pt(x, y), draw.Src)
			output, err := os.Create(expected[index])
			if err != nil {
				return nil, err
			}
			encodeErr := png.Encode(output, frame)
			closeErr := output.Close()
			if encodeErr != nil {
				return nil, encodeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
	}
	return expected, nil
}

func removeStaleFrameFiles(frameDir string) error {
	entries, err := os.ReadDir(frameDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "frame_") && strings.HasSuffix(name, ".png") {
			if err := os.Remove(filepath.Join(frameDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}
