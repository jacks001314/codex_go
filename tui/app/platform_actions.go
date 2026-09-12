package app

import (
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/app/platform_actions.rs.

type PlatformAction string

const PlatformActionOpenURL PlatformAction = "open_url"

type WindowsSandboxState struct {
	SetupStartedAt time.Time
	SetupStarted   bool
}

func (s *WindowsSandboxState) MarkSetupStarted(at time.Time) {
	if s == nil {
		return
	}
	s.SetupStartedAt = at
	s.SetupStarted = true
}

func SideReturnShortcutMatches(key string, control bool, press bool) bool {
	if !press || !control {
		return false
	}
	key = strings.ToLower(key)
	return key == "c" || key == "d" || key == "ctrl-c" || key == "ctrl-d"
}
