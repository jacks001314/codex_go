package tea

import (
	"io"
	"strings"
	"sync"
	"time"

	bubbletea "charm.land/bubbletea/v2"
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
	// paneBase is the tmux pane's Kitty image-id base while fullscreen
	// passthrough is available (Rust #59580).
	paneBase *uint32
	// drawnArea is the cell rectangle of the last rendered sprite.
	drawnArea petDrawArea
	// repaintRequested asks the owner to rewrite the rows the sprite covered.
	repaintRequested bool
}

// petDrawArea is the comparable cell rectangle of one sprite frame.
type petDrawArea struct {
	set     bool
	x       uint16
	y       uint16
	columns uint16
	rows    uint16
}

func petDrawAreaOf(request *pets.AmbientPetDraw) petDrawArea {
	if request == nil {
		return petDrawArea{}
	}
	return petDrawArea{
		set:     true,
		x:       request.X,
		y:       request.Y,
		columns: request.Columns,
		rows:    request.Rows,
	}
}

func newPetRuntime(out io.Writer, env map[string]string) *petRuntime {
	return &petRuntime{out: out, env: env}
}

// setPaneBase records the tmux pane's Kitty image-id base, or clears it when
// the sprite is cursor-placed.
func (r *petRuntime) setPaneBase(base *uint32) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paneBase = nil
	if base != nil {
		value := *base
		r.paneBase = &value
	}
}

// takeRepaintRequest consumes the pending full-frame repaint request.
func (r *petRuntime) takeRepaintRequest() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	requested := r.repaintRequested
	r.repaintRequested = false
	return requested
}

// renderLocked writes one sprite frame. The caller must hold r.mu.
func (r *petRuntime) renderLocked(request *pets.AmbientPetDraw) {
	if r.out == nil {
		return
	}
	next := petDrawAreaOf(request)
	if r.paneBase != nil && next != r.drawnArea {
		// Rust mark_pet_placeholders (#59580): tmux keeps the placeholder
		// cells after the image is deleted, so a moved or cleared sprite
		// needs the text underneath written again.
		r.repaintRequested = true
	}
	r.drawnArea = next
	_ = pets.RenderAmbientPetImage(r.out, &r.renderState, request, r.env, pets.PetImageOptions{PaneBase: r.paneBase})
}

func (r *petRuntime) setOutput(out io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.out = out
}

// writeTerminalSequence writes a control sequence directly to the terminal
// while holding the same lock as frame output, so it neither interleaves with a
// repaint nor gets suppressed by Bubble Tea's alternate-screen handling.
func (r *petRuntime) writeTerminalSequence(sequence string) error {
	if r == nil || sequence == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return nil
	}
	_, err := io.WriteString(r.out, sequence)
	return err
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
	r.renderLocked(nil)
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
		r.renderLocked(nil)
	}
	r.request = request
	if request == nil {
		r.renderLocked(nil)
		return
	}
	requestCopy := *request
	r.renderLocked(&requestCopy)
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
		r.renderLocked(nil)
		return
	}
	if r.request == nil || r.out == nil {
		return
	}
	request := *r.request
	r.renderLocked(&request)
}

// petOutputWriter appends the pending pet image payload after every write that
// reaches the terminal so the sprite is redrawn after each Bubble Tea frame.
type petOutputWriter struct {
	runtime *petRuntime
	images  *transcriptImageRuntime
	// repaint asks Bubble Tea to rewrite every text row. tmux image
	// placeholders live in the text buffer, so releasing one leaves blank
	// cells behind until the frame is rewritten (Rust invalidate_viewport).
	repaint func()
}

var _ term.File = (*petOutputWriter)(nil)

func (w *petOutputWriter) Write(p []byte) (int, error) {
	if w.runtime == nil {
		return 0, nil
	}
	n, err := w.writeFrame(p)
	if err != nil {
		return n, err
	}
	if w.takeRepaintRequest() {
		w.repaint()
	}
	return n, nil
}

// writeFrame writes one Bubble Tea frame under the shared output lock.
func (w *petOutputWriter) writeFrame(p []byte) (int, error) {
	w.runtime.mu.Lock()
	defer w.runtime.mu.Unlock()
	// Clear graphics that no longer fit before the frame text repaints their
	// rows, then redraw the live placements after the text is flushed.
	if w.images != nil {
		w.images.beforeWrite()
	}
	n, err := w.runtime.out.Write(p)
	if err != nil {
		return n, err
	}
	w.runtime.appendPayloadLocked()
	if w.images != nil {
		w.images.afterWrite()
	}
	return n, nil
}

// takeRepaintRequest reports whether a released tmux placeholder needs the
// text frame rewritten.
func (w *petOutputWriter) takeRepaintRequest() bool {
	if w.repaint == nil {
		return false
	}
	needed := false
	if w.images != nil && w.images.takeRepaintRequest() {
		needed = true
	}
	if w.runtime != nil && w.runtime.takeRepaintRequest() {
		needed = true
	}
	return needed
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
	return pets.DetectImageSupportInFullscreen(m.petEnv, m.tmuxImageProbe, m.ownedScreen)
}

// tmuxImageBase returns the pane-specific Kitty image-id base while this
// process owns the screen inside a tmux pane whose client can carry passthrough
// images (Rust #59579).
func (m *Model) tmuxImageBase() *uint32 {
	if m == nil || !m.ownedScreen || m.tmuxImageProbe == nil {
		return nil
	}
	if !m.tmuxImageProbe.KittyPassthrough() {
		return nil
	}
	base, ok := m.tmuxImageProbe.KittyImageBase()
	if !ok {
		return nil
	}
	return &base
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
	m.petRuntime.setPaneBase(m.tmuxImageBase())
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
	animationsEnabled := m.animationsEnabled
	return func() bubbletea.Msg {
		state, err := pets.LoadAmbientPet(petID, codexHome, animationsEnabled, env, fetch)
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
	runtime.setPaneBase(m.tmuxImageBase())
	width, height := m.width, m.height
	return func() bubbletea.Msg {
		runtime.drawFromState(area, composerBottomY, time.Now())
		if runtime.takeRepaintRequest() {
			return bubbletea.WindowSizeMsg{Width: width, Height: height}
		}
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
