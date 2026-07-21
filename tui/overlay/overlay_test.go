package overlay

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// =============================================================================
// Test dialog implementation
// =============================================================================

type testDialog struct {
	id      string
	value   string
	onClose func()
}

func (d *testDialog) ID() string                    { return d.id }
func (d *testDialog) View(width, height int) string { return d.value }
func (d *testDialog) Update(msg bubbletea.Msg) DialogResult {
	switch msg := msg.(type) {
	case bubbletea.KeyMsg:
		switch msg.Type {
		case bubbletea.KeyEnter:
			return DialogResult{Action: ActionSubmit, Value: d.value}
		case bubbletea.KeyEsc:
			return DialogResult{Action: ActionCancel}
		}
	}
	return DialogResult{Action: ActionNone}
}

// =============================================================================
// Tests
// =============================================================================

func TestOverlayPushPop(t *testing.T) {
	o := NewOverlay(false)
	if o.Active() {
		t.Error("new overlay should not be active")
	}

	d := &testDialog{id: "test1", value: "hello"}
	o.Push(d)

	if !o.Active() {
		t.Error("overlay should be active after push")
	}
	if o.Top() != d {
		t.Error("Top() should return pushed dialog")
	}

	popped := o.Pop()
	if popped != d {
		t.Error("Pop() should return pushed dialog")
	}
	if o.Active() {
		t.Error("overlay should not be active after pop")
	}
}

func TestOverlayStackOrder(t *testing.T) {
	o := NewOverlay(false)
	d1 := &testDialog{id: "first"}
	d2 := &testDialog{id: "second"}
	d3 := &testDialog{id: "third"}

	o.Push(d1)
	o.Push(d2)
	o.Push(d3)

	if o.Depth() != 3 {
		t.Errorf("depth = %d, want 3", o.Depth())
	}
	if o.Top().ID() != "third" {
		t.Errorf("top = %s, want third", o.Top().ID())
	}

	o.Pop() // pop d3
	if o.Top().ID() != "second" {
		t.Errorf("after pop, top = %s, want second", o.Top().ID())
	}

	o.Pop() // pop d2
	if o.Top().ID() != "first" {
		t.Errorf("after second pop, top = %s, want first", o.Top().ID())
	}
}

func TestOverlayMigrateMode(t *testing.T) {
	o := NewOverlay(true) // migration mode: max depth 1
	d1 := &testDialog{id: "first"}
	d2 := &testDialog{id: "second"}

	o.Push(d1)
	o.Push(d2) // should replace d1

	if o.Depth() != 1 {
		t.Errorf("migrate mode depth = %d, want 1", o.Depth())
	}
	if o.Top().ID() != "second" {
		t.Errorf("migrate mode top = %s, want second", o.Top().ID())
	}
}

func TestOverlayUpdate(t *testing.T) {
	o := NewOverlay(false)
	d := &testDialog{id: "dialog", value: "content"}
	o.Push(d)

	// Update with Enter should trigger ActionSubmit + pop
	cmd := o.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if cmd != nil {
		_ = cmd() // consume
	}
	if o.Active() {
		t.Error("overlay should be inactive after submit")
	}
}

func TestOverlayUpdateCancel(t *testing.T) {
	o := NewOverlay(false)
	d := &testDialog{id: "dialog", value: "content"}
	o.Push(d)

	o.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if o.Active() {
		t.Error("overlay should be inactive after cancel")
	}
}

func TestOverlayView(t *testing.T) {
	o := NewOverlay(false)
	d := &testDialog{id: "view-test", value: "dialog content"}
	o.Push(d)

	view := o.View(80, 24)
	if view != "dialog content" {
		t.Errorf("View = %q, want %q", view, "dialog content")
	}
}

func TestOverlayViewEmpty(t *testing.T) {
	o := NewOverlay(false)
	if view := o.View(80, 24); view != "" {
		t.Errorf("empty overlay View = %q, want empty", view)
	}
}

func TestOverlayClear(t *testing.T) {
	o := NewOverlay(false)
	o.Push(&testDialog{id: "a"})
	o.Push(&testDialog{id: "b"})
	o.Clear()

	if o.Active() {
		t.Error("overlay should be inactive after clear")
	}
	if o.stack != nil {
		t.Error("stack should be nil after clear")
	}
}

func TestOverlayUpdateOnEmpty(t *testing.T) {
	o := NewOverlay(false)
	cmd := o.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if cmd != nil {
		t.Error("Update on empty overlay should return nil")
	}
}
