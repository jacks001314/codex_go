package tea

import (
	"io"
	"strings"
	"sync"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	chatwidget "codex_go/tui/chatwidget"
	pets "codex_go/tui/pets"
)

// petLoadMsg delivers the result of an asynchronous pet asset load.
type petLoadMsg struct {
	petID string
	state pets.AmbientPetState
	err   error
}

// petTickMsg advances the ambient pet animation on its frame schedule.
type petTickMsg struct{}

// petRuntime coordinates the ambient pet image between the model (which
// computes draw requests) and the terminal output writer (which emits the
// Kitty/Sixel payload after every frame). All output writes are serialized
// through mu so the pet payload never interleaves with frame text.
type petRuntime struct {
	mu          sync.Mutex
	out         io.Writer
	env         map[string]string
	renderState pets.PetImageRenderState
	ambient     *pets.AmbientPetState
	request     *pets.AmbientPetDraw
	clearNext   bool
}

func newPetRuntime(out io.Writer, env map[string]string) *petRuntime {
	return &petRuntime{out: out, env: env}
}

func (r *petRuntime) setOutput(out io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.out = out
}

// install replaces the active ambient pet; the next frame write renders it.
func (r *petRuntime) install(state pets.AmbientPetState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ambient = &state
	r.request = nil
	r.clearNext = false
}

// disable drops the pet and clears its image on the next frame write.
func (r *petRuntime) disable() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ambient = nil
	r.request = nil
	r.clearNext = true
}

// hide stops drawing the pet while a modal or popup covers the surface. It
// only schedules a clear when the pet was actually visible.
func (r *petRuntime) hide() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.request == nil && !r.clearNext {
		return
	}
	r.request = nil
	r.clearNext = true
}

// clearImmediately writes the clear sequence now (used on shutdown).
func (r *petRuntime) clearImmediately() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ambient = nil
	r.request = nil
	r.clearNext = false
	if r.out == nil {
		return
	}
	_ = pets.RenderAmbientPetImage(r.out, &r.renderState, nil, r.env)
}

func (r *petRuntime) installed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ambient != nil
}

func (r *petRuntime) storeDrawRequest(request *pets.AmbientPetDraw) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.request = request
}

func (r *petRuntime) computeDrawRequestLocked(area pets.Rect, composerBottomY uint16, now time.Time) *pets.AmbientPetDraw {
	if r.ambient == nil || !r.ambient.Support.Supported() {
		return nil
	}
	request, ok := r.ambient.DrawRequest(area, composerBottomY, now)
	if !ok {
		return nil
	}
	return &request
}

// storeDrawRequestFromState recomputes the frame request for the current layout.
func (r *petRuntime) storeDrawRequestFromState(area pets.Rect, composerBottomY uint16, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.request = r.computeDrawRequestLocked(area, composerBottomY, now)
}

// drawFromState writes an animation frame without asking Bubble Tea to render
// the inline TUI. The same mutex guards renderer output and direct pet output,
// so the image payload cannot interleave with a normal frame flush.
func (r *petRuntime) drawFromState(area pets.Rect, composerBottomY uint16, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return
	}
	request := r.computeDrawRequestLocked(area, composerBottomY, now)
	if r.clearNext {
		r.clearNext = false
		_ = pets.RenderAmbientPetImage(r.out, &r.renderState, nil, r.env)
	}
	r.request = request
	if request == nil {
		_ = pets.RenderAmbientPetImage(r.out, &r.renderState, nil, r.env)
		return
	}
	requestCopy := *request
	_ = pets.RenderAmbientPetImage(r.out, &r.renderState, &requestCopy, r.env)
}

func (r *petRuntime) nextFrameDelay(now time.Time) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ambient == nil {
		return 0, false
	}
	return r.ambient.NextFrameDelay(now)
}

// appendPayloadLocked emits the pending pet payload after a frame write. The
// caller must hold r.mu.
func (r *petRuntime) appendPayloadLocked() {
	if r.clearNext {
		r.clearNext = false
		r.request = nil
		if r.out != nil {
			_ = pets.RenderAmbientPetImage(r.out, &r.renderState, nil, r.env)
		}
		return
	}
	if r.request == nil || r.out == nil {
		return
	}
	request := *r.request
	_ = pets.RenderAmbientPetImage(r.out, &r.renderState, &request, r.env)
}

// petOutputWriter appends the pending pet image payload after every write that
// reaches the terminal so the sprite is redrawn after each Bubble Tea frame.
type petOutputWriter struct {
	runtime *petRuntime
}

var _ term.File = (*petOutputWriter)(nil)

func (w *petOutputWriter) Write(p []byte) (int, error) {
	if w.runtime == nil {
		return 0, nil
	}
	w.runtime.mu.Lock()
	defer w.runtime.mu.Unlock()
	n, err := w.runtime.out.Write(p)
	if err != nil {
		return n, err
	}
	w.runtime.appendPayloadLocked()
	return n, nil
}

func (w *petOutputWriter) Read(p []byte) (int, error) {
	output := w.output()
	reader, ok := output.(io.Reader)
	if !ok {
		return 0, io.EOF
	}
	return reader.Read(p)
}

// Close intentionally leaves the caller-owned terminal open. Bubble Tea only
// needs this method so the wrapper preserves the terminal File interface.
func (w *petOutputWriter) Close() error {
	return nil
}

func (w *petOutputWriter) Fd() uintptr {
	output := w.output()
	file, ok := output.(interface{ Fd() uintptr })
	if !ok {
		return ^uintptr(0)
	}
	return file.Fd()
}

func (w *petOutputWriter) output() io.Writer {
	if w == nil || w.runtime == nil {
		return nil
	}
	w.runtime.mu.Lock()
	defer w.runtime.mu.Unlock()
	return w.runtime.out
}

// Model helpers -------------------------------------------------------------

func (m *Model) petImageSupport() pets.PetImageSupport {
	if m == nil || m.petEnv == nil {
		return pets.PetImageSupport{Reason: pets.PetImageUnsupportedTerminal}
	}
	return pets.DetectImageSupport(m.petEnv)
}

func (m *Model) petSurfaceActive() bool {
	if m == nil {
		return true
	}
	return m.modal != nil ||
		m.overlay != nil ||
		m.slashPopup.Active ||
		m.skillPopup.Active ||
		m.mentionPopup != nil
}

// petComposerBottomY returns the bottom edge (exclusive) of the composer
// region, mirroring Rust's composer-anchored pet placement.
func (m *Model) petComposerBottomY() uint16 {
	if m == nil {
		return 0
	}
	y := m.height - 1
	if strings.TrimSpace(m.notice) != "" {
		y--
	}
	if sideLabel := strings.TrimSpace(m.sideContextLabel()); sideLabel != "" && sideLabel != strings.TrimSpace(m.notice) {
		y--
	}
	if agentLabel := strings.TrimSpace(m.activeAgentLabel); agentLabel != "" && agentLabel != strings.TrimSpace(m.notice) {
		y--
	}
	if m.ideContext.Enabled {
		y--
	}
	if y < 1 {
		y = 1
	}
	return uint16(y)
}

// storePetDrawRequest computes the current pet frame request before each View
// render so the output writer can emit it after the frame text.
func (m *Model) storePetDrawRequest() {
	if m == nil || m.petRuntime == nil {
		return
	}
	if m.petSurfaceActive() {
		m.petRuntime.hide()
		return
	}
	area := pets.Rect{X: 0, Y: 0, Width: uint16(max(m.width, 0)), Height: uint16(max(m.height, 0))}
	m.petRuntime.storeDrawRequestFromState(area, m.petComposerBottomY(), time.Now())
}

func (m *Model) loadPetCmd(petID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	petID = normalizePetIDTea(petID)
	if petID == "" || petID == chatwidget.DisabledPetID {
		return nil
	}
	if m.petCodexHome == "" || m.petFetch == nil || !m.petImageSupport().Supported() {
		return nil
	}
	codexHome := m.petCodexHome
	env := m.petEnv
	fetch := m.petFetch
	return func() bubbletea.Msg {
		state, err := pets.LoadAmbientPet(petID, codexHome, true, env, fetch)
		return petLoadMsg{petID: petID, state: state, err: err}
	}
}

func (m *Model) petTickCmd() bubbletea.Cmd {
	if m == nil || m.petRuntime == nil {
		return nil
	}
	delay, ok := m.petRuntime.nextFrameDelay(time.Now())
	if !ok {
		return nil
	}
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg {
		return petTickMsg{}
	})
}

// petDrawCmd draws the next pet frame directly through the synchronized output
// writer. Pet animation must not trigger a Bubble Tea View refresh: even a
// minimal inline refresh walks every terminal row and can scroll the composer
// when the terminal's cursor position differs from the renderer's bookkeeping.
func (m *Model) petDrawCmd() bubbletea.Cmd {
	if m == nil || m.petRuntime == nil || !m.petRuntime.installed() || m.petSurfaceActive() {
		return nil
	}
	runtime := m.petRuntime
	area := pets.Rect{X: 0, Y: 0, Width: uint16(max(m.width, 0)), Height: uint16(max(m.height, 0))}
	composerBottomY := m.petComposerBottomY()
	return func() bubbletea.Msg {
		runtime.drawFromState(area, composerBottomY, time.Now())
		return nil
	}
}

func (m *Model) applyPetLoad(msg petLoadMsg) bubbletea.Cmd {
	if m == nil || m.petRuntime == nil {
		return nil
	}
	if msg.err != nil {
		m.notice = "Failed to load pet: " + msg.err.Error()
		m.refreshTranscript()
		return nil
	}
	if normalizePetIDTea(msg.petID) != m.tuiPet {
		return nil
	}
	m.petRuntime.install(msg.state)
	return bubbletea.Batch(m.petDrawCmd(), m.petTickCmd())
}
