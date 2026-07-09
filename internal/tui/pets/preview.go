package pets

import "sync"

type PreviewStatus string

const (
	PreviewHidden   PreviewStatus = "hidden"
	PreviewLoading  PreviewStatus = "loading"
	PreviewDisabled PreviewStatus = "disabled"
	PreviewReady    PreviewStatus = "ready"
	PreviewError    PreviewStatus = "error"
)

type PreviewState struct {
	mu       sync.Mutex
	Loading  bool
	Error    string
	Status   PreviewStatus
	LastArea *Rect
}

func (s *PreviewState) SetLoading() {
	s.update(PreviewLoading, "", true)
}

func (s *PreviewState) SetDisabled() {
	s.update(PreviewDisabled, "", false)
}

func (s *PreviewState) SetReady() {
	s.update(PreviewReady, "", false)
}

func (s *PreviewState) SetError(message string) {
	s.update(PreviewError, message, false)
}

func (s *PreviewState) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = PreviewHidden
	s.Loading = false
	s.Error = ""
	s.LastArea = nil
}

func (s *PreviewState) Area() (Rect, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.LastArea == nil {
		return Rect{}, false
	}
	return *s.LastArea, true
}

func (s *PreviewState) RenderLines(area Rect) ([]string, Rect, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	areaCopy := area
	s.LastArea = &areaCopy
	status := s.statusLocked()
	var lines []string
	switch status {
	case PreviewLoading:
		lines = []string{"Loading preview..."}
	case PreviewDisabled:
		lines = []string{"Terminal pets disabled", "No pet will be shown."}
	case PreviewError:
		lines = []string{"Preview unavailable", s.Error}
	case PreviewHidden, PreviewReady:
		return nil, Rect{}, false
	default:
		return nil, Rect{}, false
	}
	textArea := CenteredTextArea(area, uint16(len(lines)))
	return lines, textArea, true
}

func (s *PreviewState) update(status PreviewStatus, err string, loading bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
	s.Error = err
	s.Loading = loading
}

func (s *PreviewState) statusLocked() PreviewStatus {
	if s.Status != "" {
		return s.Status
	}
	if s.Loading {
		return PreviewLoading
	}
	if s.Error != "" {
		return PreviewError
	}
	return PreviewHidden
}

func CenteredTextArea(area Rect, height uint16) Rect {
	if height > area.Height {
		height = area.Height
	}
	y := area.Y + (area.Height-height)/2
	return Rect{X: area.X, Y: y, Width: area.Width, Height: height}
}
