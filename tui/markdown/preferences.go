package markdown

import (
	"sync"

	"codex_go/config"
)

// Rust parity: codex-rs/tui/src/markdown_render/preferences.rs. Content
// preferences shared by the streaming and completed Markdown renderers. Seed
// before startup previews, then refresh when resolved session settings become
// active.

// Rendering is the resolved rich-rendering preference set (Rust `TuiRendering`).
// A disabled renderer preserves the original source.
type Rendering struct {
	Mermaid bool
	Math    bool
	Tables  bool
	Lists   bool
}

// DefaultRendering mirrors `TuiRendering::default`: every rich renderer is on.
func DefaultRendering() Rendering {
	return Rendering{Mermaid: true, Math: true, Tables: true, Lists: true}
}

var (
	renderingMu      sync.RWMutex
	currentRendering = DefaultRendering()
)

// InitRendering seeds the renderers before startup previews and refreshes them
// when resolved session settings become active (Rust `preferences::init`).
func InitRendering(rendering Rendering) {
	renderingMu.Lock()
	currentRendering = rendering
	renderingMu.Unlock()
}

// CurrentRendering returns the active preferences (Rust `preferences::current`).
func CurrentRendering() Rendering {
	renderingMu.RLock()
	defer renderingMu.RUnlock()
	return currentRendering
}

// InitRenderingFromConfig mirrors Rust's `preferences::init(config.tui_rendering)`:
// the effective configuration seeds the renderer preferences.
func InitRenderingFromConfig(rendering config.TuiRendering) {
	InitRendering(Rendering{
		Mermaid: rendering.Mermaid,
		Math:    rendering.Math,
		Tables:  rendering.Tables,
		Lists:   rendering.Lists,
	})
}
