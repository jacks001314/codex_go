package tea

// Rust parity: tui/src/tui/frame_requester.rs schedules a draw only when a
// widget asks for one, and each animating widget re-requests its next frame
// from its own render pass. Bubble Tea renders after every message, so the Go
// side keeps the same shape by asking for frames only while a live animated
// surface exists.

// frameDemandActive reports whether any surface currently produces
// time-dependent output. Each rule mirrors the Rust owner that schedules the
// frame:
//   - bottom_pane/chat_composer/sparkle.rs animates the starfield only while
//     its waiting/visible phase is live (#44879),
//   - the retry activity spinner advances once per animation frame,
//   - empty_state_animation.rs re-arms only while a blossom replay runs.
func (m *Model) frameDemandActive() bool {
	if m == nil || m.animEngine == nil {
		return false
	}
	if !m.animationsEnabled || !m.terminalFocused {
		return false
	}
	if m.sparkleActive() && m.sparkleEffectsEnabled() {
		return true
	}
	if m.retryActivityActive {
		return true
	}
	if !m.welcomeBlossom.replayStartedAt.IsZero() && m.welcomeBlossomEligible() {
		return true
	}
	return false
}

// syncFrameDemand reconciles the shared animation chain with the live surfaces.
// Update calls it after every message so a surface that just became visible
// starts requesting frames and an idle UI stops drawing.
func (m *Model) syncFrameDemand() {
	if m == nil || m.animEngine == nil || !m.frames.attached() {
		// Models without a Program keep the command-driven tick chain started by
		// Init; there is no notification channel to schedule frames through.
		return
	}
	if m.frameDemandActive() {
		m.animEngine.Resume()
		m.frames.requestAnim(m.animEngine.FrameDelay())
		return
	}
	m.animEngine.Pause()
	m.frames.stop()
}
