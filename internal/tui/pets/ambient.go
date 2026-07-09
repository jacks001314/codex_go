package pets

import (
	"path/filepath"
	"time"
)

const (
	PetTargetHeightPX = 75
	PetComposerGapPX  = 10
	TerminalRowPX     = 15

	RunningLifetime = 3 * time.Minute
	FailedLifetime  = time.Hour
	WaitingLifetime = 24 * time.Hour
	ReviewLifetime  = 7 * 24 * time.Hour
)

type AmbientPetState struct {
	PetID              string
	Visible            bool
	Pet                Pet
	Frames             []string
	SixelDir           string
	Support            PetImageSupport
	Notification       *PetNotification
	AnimationStartedAt time.Time
	AnimationsEnabled  bool
}

type PetNotificationKind string

const (
	PetNotificationRunning PetNotificationKind = "running"
	PetNotificationWaiting PetNotificationKind = "waiting"
	PetNotificationReview  PetNotificationKind = "review"
	PetNotificationFailed  PetNotificationKind = "failed"
)

type PetNotification struct {
	Kind      PetNotificationKind
	Body      string
	UpdatedAt time.Time
}

type Rect struct {
	X      uint16
	Y      uint16
	Width  uint16
	Height uint16
}

type AmbientPetDraw struct {
	Frame     string
	Protocol  ImageProtocol
	X         uint16
	Y         uint16
	ClearTopY uint16
	Columns   uint16
	Rows      uint16
	HeightPX  uint16
	SixelDir  string
}

func NewAmbientPetState(pet Pet, frames []string, sixelDir string, support PetImageSupport, animationsEnabled bool, now time.Time) AmbientPetState {
	if now.IsZero() {
		now = time.Now()
	}
	return AmbientPetState{
		PetID:              pet.ID,
		Visible:            support.Supported(),
		Pet:                pet,
		Frames:             append([]string(nil), frames...),
		SixelDir:           sixelDir,
		Support:            support,
		AnimationStartedAt: now,
		AnimationsEnabled:  animationsEnabled,
	}
}

func (s *AmbientPetState) SetNotification(kind PetNotificationKind, body string, now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	if body == "" {
		body = kind.FallbackBody()
	}
	s.Notification = &PetNotification{Kind: kind, Body: body, UpdatedAt: now}
	s.AnimationStartedAt = now
}

func (s AmbientPetState) DrawRequest(area Rect, composerBottomY uint16, now time.Time) (AmbientPetDraw, bool) {
	if !s.Support.Supported() {
		return AmbientPetDraw{}, false
	}
	size := s.imageSize()
	notification := s.VisibleNotification(now)
	notificationRows := uint16(0)
	if notification != nil {
		notificationRows = notification.Height()
	}
	requiredHeight := size.Rows + notificationRows
	spriteBottomY := saturatingSub(composerBottomY, composerGapRows())
	if spriteBottomY < area.Y+requiredHeight || area.Width < size.Columns {
		return AmbientPetDraw{}, false
	}
	frame, ok := s.CurrentFrame(now)
	if !ok {
		return AmbientPetDraw{}, false
	}
	return AmbientPetDraw{
		Frame:     frame,
		Protocol:  s.Support.Protocol,
		X:         area.X + area.Width - size.Columns,
		Y:         spriteBottomY - size.Rows,
		ClearTopY: area.Y,
		Columns:   size.Columns,
		Rows:      size.Rows,
		HeightPX:  size.HeightPX,
		SixelDir:  s.SixelDir,
	}, true
}

func (s AmbientPetState) PreviewDrawRequest(area Rect) (AmbientPetDraw, bool) {
	if !s.Support.Supported() {
		return AmbientPetDraw{}, false
	}
	size := s.imageSize()
	if area.Width < size.Columns || area.Height < size.Rows {
		return AmbientPetDraw{}, false
	}
	frame, ok := s.FirstIdleFrame()
	if !ok {
		return AmbientPetDraw{}, false
	}
	y := area.Y + (area.Height-size.Rows)/2
	return AmbientPetDraw{
		Frame:     frame,
		Protocol:  s.Support.Protocol,
		X:         area.X + (area.Width-size.Columns)/2,
		Y:         y,
		ClearTopY: y,
		Columns:   size.Columns,
		Rows:      size.Rows,
		HeightPX:  size.HeightPX,
		SixelDir:  s.SixelDir,
	}, true
}

func (s AmbientPetState) VisibleNotification(now time.Time) *PetNotification {
	if now.IsZero() {
		now = time.Now()
	}
	if s.Notification == nil || s.Notification.Expired(now) {
		return nil
	}
	notification := *s.Notification
	return &notification
}

func (s AmbientPetState) CurrentAnimation(now time.Time) (Animation, bool) {
	name := "idle"
	if notification := s.VisibleNotification(now); notification != nil {
		name = notification.Kind.AnimationName()
	}
	animation, ok := s.Pet.Animations[name]
	if !ok {
		animation, ok = s.Pet.Animations["idle"]
	}
	if !ok {
		return Animation{}, false
	}
	if animation.LoopStart == nil {
		elapsed := now.Sub(s.AnimationStartedAt)
		if elapsed >= animation.TotalDuration() {
			if fallback, fallbackOK := s.Pet.Animations[animation.Fallback]; fallbackOK {
				return fallback, true
			}
		}
	}
	return animation, true
}

func (s AmbientPetState) CurrentFrame(now time.Time) (string, bool) {
	animation, ok := s.CurrentAnimation(now)
	if !ok {
		return s.frameForSpriteIndex(0)
	}
	spriteIndex := 0
	if s.AnimationsEnabled {
		if tick, tickOK := CurrentAnimationFrame(animation, now.Sub(s.AnimationStartedAt)); tickOK {
			spriteIndex = tick.SpriteIndex
		}
	} else if len(animation.Frames) > 0 {
		spriteIndex = animation.Frames[0].SpriteIndex
	}
	return s.frameForSpriteIndex(spriteIndex)
}

func (s AmbientPetState) FirstIdleFrame() (string, bool) {
	spriteIndex := 0
	if idle, ok := s.Pet.Animations["idle"]; ok && len(idle.Frames) > 0 {
		spriteIndex = idle.Frames[0].SpriteIndex
	}
	return s.frameForSpriteIndex(spriteIndex)
}

func (s AmbientPetState) NextFrameDelay(now time.Time) (time.Duration, bool) {
	if !s.Support.Supported() || !s.AnimationsEnabled {
		return 0, false
	}
	animation, ok := s.CurrentAnimation(now)
	if !ok {
		return 0, false
	}
	tick, ok := CurrentAnimationFrame(animation, now.Sub(s.AnimationStartedAt))
	if !ok || !tick.HasDelay {
		return 0, false
	}
	return tick.Delay, true
}

func (s AmbientPetState) frameForSpriteIndex(spriteIndex int) (string, bool) {
	if len(s.Frames) == 0 {
		return "", false
	}
	if spriteIndex < 0 {
		spriteIndex = 0
	}
	if spriteIndex >= len(s.Frames) {
		spriteIndex = len(s.Frames) - 1
	}
	return filepath.Clean(s.Frames[spriteIndex]), true
}

type imageSize struct {
	Columns  uint16
	Rows     uint16
	HeightPX uint16
}

func (s AmbientPetState) imageSize() imageSize {
	rows := uint16(5)
	if TerminalRowPX > 0 {
		rows = uint16(floatRound(float64(PetTargetHeightPX) / float64(TerminalRowPX)))
		if rows < 1 {
			rows = 1
		}
	}
	frameWidth := s.Pet.FrameWidth
	frameHeight := s.Pet.FrameHeight
	if frameWidth <= 0 {
		frameWidth = DefaultFrameWidth
	}
	if frameHeight <= 0 {
		frameHeight = DefaultFrameHeight
	}
	aspect := float64(frameHeight) / float64(frameWidth) * 0.52
	columns := uint16(floatRound(float64(rows) / aspect))
	if columns < 1 {
		columns = 1
	}
	return imageSize{Columns: columns, Rows: rows, HeightPX: PetTargetHeightPX}
}

func (n PetNotification) Expired(now time.Time) bool {
	return !n.UpdatedAt.IsZero() && now.Sub(n.UpdatedAt) >= n.Kind.Lifetime()
}

func (n PetNotification) Height() uint16 {
	if n.Body == n.Kind.Label() {
		return 1
	}
	return 2
}

func (k PetNotificationKind) AnimationName() string {
	switch k {
	case PetNotificationRunning:
		return "running"
	case PetNotificationWaiting:
		return "waiting"
	case PetNotificationReview:
		return "review"
	case PetNotificationFailed:
		return "failed"
	default:
		return "idle"
	}
}

func (k PetNotificationKind) Label() string {
	switch k {
	case PetNotificationRunning:
		return "Running"
	case PetNotificationWaiting:
		return "Needs input"
	case PetNotificationReview:
		return "Ready"
	case PetNotificationFailed:
		return "Blocked"
	default:
		return ""
	}
}

func (k PetNotificationKind) FallbackBody() string {
	switch k {
	case PetNotificationRunning:
		return "Thinking"
	case PetNotificationWaiting:
		return "Needs input"
	case PetNotificationReview:
		return "Ready"
	case PetNotificationFailed:
		return "Blocked"
	default:
		return ""
	}
}

func (k PetNotificationKind) Lifetime() time.Duration {
	switch k {
	case PetNotificationRunning:
		return RunningLifetime
	case PetNotificationWaiting:
		return WaitingLifetime
	case PetNotificationReview:
		return ReviewLifetime
	case PetNotificationFailed:
		return FailedLifetime
	default:
		return 0
	}
}

func composerGapRows() uint16 {
	rows := uint16(floatRound(float64(PetComposerGapPX) / float64(TerminalRowPX)))
	if rows < 1 {
		return 1
	}
	return rows
}

func saturatingSub(value uint16, subtract uint16) uint16 {
	if subtract > value {
		return 0
	}
	return value - subtract
}

func floatRound(value float64) int {
	if value < 0 {
		return int(value - 0.5)
	}
	return int(value + 0.5)
}
