package config

import "sync"

// GlobalInstructionsManager mirrors Rust's CodexHomeUserInstructionsProvider
// state (Rust #44675): a failed refresh retains the last successful global
// instructions, and a warning is reported once per failure episode rather than
// on every refresh.
type GlobalInstructionsManager struct {
	mu     sync.Mutex
	states map[string]*globalInstructionsState
	// loadFn overrides the filesystem read (tests only).
	loadFn func(codexHome string) *LoadedUserInstructions
}

type globalInstructionsState struct {
	// lastSuccessful is the most recent snapshot that either carried
	// instructions or confirmed their absence.
	lastSuccessful *Instructions
	// activeWarnings are the warnings reported by the previous refresh.
	activeWarnings map[string]struct{}
}

func NewGlobalInstructionsManager() *GlobalInstructionsManager {
	return &GlobalInstructionsManager{states: map[string]*globalInstructionsState{}}
}

// Load refreshes the global instructions for codexHome. When a refresh fails, the
// last successful instructions are preserved; warnings already active from the
// previous refresh are suppressed until the failure clears and recurs.
func (m *GlobalInstructionsManager) Load(codexHome string) *LoadedUserInstructions {
	if m == nil {
		return newLoadUserInstructions(codexHome, nil)
	}
	raw := newLoadUserInstructions(codexHome, m.loadFn)
	loaded := &LoadedUserInstructions{
		Instructions: cloneInstructions(raw.Instructions),
		Warnings:     append([]string(nil), raw.Warnings...),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.states == nil {
		m.states = map[string]*globalInstructionsState{}
	}
	state := m.states[codexHome]
	if state == nil {
		state = &globalInstructionsState{}
		m.states[codexHome] = state
	}
	if loaded.Instructions != nil || len(loaded.Warnings) == 0 {
		// Either a successful read or a confirmed absence (removed/blank source).
		state.lastSuccessful = cloneInstructions(loaded.Instructions)
	} else {
		loaded.Instructions = cloneInstructions(state.lastSuccessful)
	}
	previous := state.activeWarnings
	active := make(map[string]struct{}, len(loaded.Warnings))
	fresh := make([]string, 0, len(loaded.Warnings))
	for _, warning := range loaded.Warnings {
		if _, wasActive := previous[warning]; !wasActive {
			fresh = append(fresh, warning)
		}
		active[warning] = struct{}{}
	}
	state.activeWarnings = active
	loaded.Warnings = fresh
	return loaded
}

func newLoadUserInstructions(codexHome string, loadFn func(string) *LoadedUserInstructions) *LoadedUserInstructions {
	var loaded *LoadedUserInstructions
	if loadFn != nil {
		loaded = loadFn(codexHome)
	} else {
		loaded = NewUserInstructionsProvider(codexHome).Load()
	}
	if loaded == nil {
		return &LoadedUserInstructions{}
	}
	return loaded
}

func cloneInstructions(instructions *Instructions) *Instructions {
	if instructions == nil {
		return nil
	}
	clone := *instructions
	return &clone
}
