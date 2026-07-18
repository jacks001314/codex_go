package pets

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultPetID    = "codex"
	DisabledPetID   = "disabled"
	MaxPetFrames    = 256
	MaxAnimationFPS = 60.0
	CustomPetPrefix = "custom:"
)

type Model struct {
	CurrentPet string
}

type AnimationFrame struct {
	SpriteIndex int
	Duration    time.Duration
}

type Animation struct {
	Frames    []AnimationFrame
	LoopStart *int
	Fallback  string
}

func (a Animation) TotalDuration() time.Duration {
	var total time.Duration
	for _, frame := range a.Frames {
		total += frame.Duration
	}
	return total
}

type Pet struct {
	ID              string
	DisplayName     string
	Description     string
	SpritesheetPath string
	FrameWidth      int
	FrameHeight     int
	Columns         int
	Rows            int
	FrameCount      int
	Animations      map[string]Animation
}

func BuiltinPetModel(id string, codexHome string) (Pet, error) {
	catalogPet, ok := BuiltinPet(id)
	if !ok {
		return Pet{}, errors.New("unknown pet " + id)
	}
	if strings.TrimSpace(codexHome) == "" {
		return Pet{}, errors.New("CODEX_HOME is not available")
	}
	return Pet{
		ID:              catalogPet.ID,
		DisplayName:     catalogPet.DisplayName,
		Description:     catalogPet.Description,
		SpritesheetPath: BuiltinSpritesheetPath(codexHome, catalogPet.SpritesheetFile),
		FrameWidth:      DefaultFrameWidth,
		FrameHeight:     DefaultFrameHeight,
		Columns:         DefaultFrameColumns,
		Rows:            DefaultFrameRows,
		FrameCount:      DefaultFrameCount(),
		Animations:      DefaultAnimations(),
	}, nil
}

func CustomPetSelector(id string) string {
	return CustomPetPrefix + strings.TrimSpace(id)
}

func PathLikeSelector(value string) bool {
	value = strings.TrimSpace(value)
	return value == "." ||
		value == ".." ||
		strings.HasPrefix(value, "~/") ||
		strings.HasPrefix(value, "../") ||
		strings.HasPrefix(value, "./") ||
		filepath.IsAbs(value) ||
		strings.Contains(value, "/") ||
		strings.Contains(value, `\`)
}

func DefaultFrameCount() int {
	return DefaultFrameColumns * DefaultFrameRows
}

func DefaultAnimations() map[string]Animation {
	return map[string]Animation{
		"idle":          IdleAnimation(),
		"running-right": AppStateAnimation(1, 8, 120*time.Millisecond, 220*time.Millisecond),
		"running-left":  AppStateAnimation(2, 8, 120*time.Millisecond, 220*time.Millisecond),
		"waving":        AppStateAnimation(3, 4, 140*time.Millisecond, 280*time.Millisecond),
		"jumping":       AppStateAnimation(4, 5, 140*time.Millisecond, 280*time.Millisecond),
		"failed":        AppStateAnimation(5, 8, 140*time.Millisecond, 240*time.Millisecond),
		"waiting":       AppStateAnimation(6, 6, 150*time.Millisecond, 260*time.Millisecond),
		"running":       AppStateAnimation(7, 6, 120*time.Millisecond, 220*time.Millisecond),
		"review":        AppStateAnimation(8, 6, 150*time.Millisecond, 280*time.Millisecond),
		"move_right":    AppStateAnimation(1, 8, 120*time.Millisecond, 220*time.Millisecond),
		"move_left":     AppStateAnimation(2, 8, 120*time.Millisecond, 220*time.Millisecond),
		"wave":          AppStateAnimation(3, 4, 140*time.Millisecond, 280*time.Millisecond),
		"bounce":        AppStateAnimation(4, 5, 140*time.Millisecond, 280*time.Millisecond),
		"sad":           AppStateAnimation(5, 8, 140*time.Millisecond, 240*time.Millisecond),
	}
}

func IdleAnimation() Animation {
	return Animation{
		Frames: []AnimationFrame{
			{SpriteIndex: 0, Duration: 1680 * time.Millisecond},
			{SpriteIndex: 1, Duration: 660 * time.Millisecond},
			{SpriteIndex: 2, Duration: 660 * time.Millisecond},
			{SpriteIndex: 3, Duration: 840 * time.Millisecond},
			{SpriteIndex: 4, Duration: 840 * time.Millisecond},
			{SpriteIndex: 5, Duration: 1920 * time.Millisecond},
		},
		LoopStart: intPtr(0),
		Fallback:  "idle",
	}
}

func AppStateAnimation(rowIndex int, frameCount int, frameDuration time.Duration, finalFrameDuration time.Duration) Animation {
	primary := make([]AnimationFrame, 0, frameCount)
	for columnIndex := 0; columnIndex < frameCount; columnIndex++ {
		duration := frameDuration
		if columnIndex == frameCount-1 {
			duration = finalFrameDuration
		}
		primary = append(primary, AnimationFrame{
			SpriteIndex: rowIndex*DefaultFrameColumns + columnIndex,
			Duration:    duration,
		})
	}
	idle := IdleAnimation()
	frames := make([]AnimationFrame, 0, len(primary)*3+len(idle.Frames))
	for i := 0; i < 3; i++ {
		frames = append(frames, primary...)
	}
	frames = append(frames, idle.Frames...)
	return Animation{
		Frames:    frames,
		LoopStart: intPtr(len(primary) * 3),
		Fallback:  "idle",
	}
}

type AnimationFrameTick struct {
	SpriteIndex int
	Delay       time.Duration
	HasDelay    bool
}

func CurrentAnimationFrame(animation Animation, elapsed time.Duration) (AnimationFrameTick, bool) {
	if len(animation.Frames) == 0 {
		return AnimationFrameTick{}, false
	}
	if len(animation.Frames) == 1 {
		return AnimationFrameTick{SpriteIndex: animation.Frames[0].SpriteIndex}, true
	}
	elapsedNanos := elapsed.Nanoseconds()
	if elapsedNanos < 0 {
		elapsedNanos = 0
	}
	if animation.LoopStart != nil && *animation.LoopStart >= 0 && *animation.LoopStart < len(animation.Frames) {
		totalNanos := animation.TotalDuration().Nanoseconds()
		prefixNanos := durationNanos(animation.Frames[:*animation.LoopStart])
		loopNanos := durationNanos(animation.Frames[*animation.LoopStart:])
		effective := elapsedNanos
		if elapsedNanos >= totalNanos && loopNanos > 0 {
			effective = prefixNanos + (elapsedNanos-prefixNanos)%loopNanos
		}
		return frameAtElapsed(animation, effective)
	}
	if elapsedNanos >= animation.TotalDuration().Nanoseconds() {
		last := animation.Frames[len(animation.Frames)-1]
		return AnimationFrameTick{SpriteIndex: last.SpriteIndex}, true
	}
	return frameAtElapsed(animation, elapsedNanos)
}

func frameAtElapsed(animation Animation, elapsedNanos int64) (AnimationFrameTick, bool) {
	remaining := elapsedNanos
	for _, frame := range animation.Frames {
		frameNanos := frame.Duration.Nanoseconds()
		if frameNanos < 1 {
			frameNanos = 1
		}
		if remaining < frameNanos {
			delay := time.Duration(frameNanos - remaining)
			return AnimationFrameTick{SpriteIndex: frame.SpriteIndex, Delay: delay, HasDelay: true}, true
		}
		remaining -= frameNanos
	}
	last := animation.Frames[len(animation.Frames)-1]
	return AnimationFrameTick{SpriteIndex: last.SpriteIndex}, true
}

func durationNanos(frames []AnimationFrame) int64 {
	var total int64
	for _, frame := range frames {
		total += frame.Duration.Nanoseconds()
	}
	return total
}

func intPtr(value int) *int {
	return &value
}
