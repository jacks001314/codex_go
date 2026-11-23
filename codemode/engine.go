package codemode

import "context"

// Engine executes one isolated code-mode JavaScript program.
type Engine interface {
	Execute(context.Context, EngineRequest) (*EngineResult, error)
	Interrupt(error)
	Close() error
}

type EngineRequest struct {
	ToolCallID string
	Source     string
	// EnabledTools is the cell's tool catalog. Preloads are matched against it.
	EnabledTools []ProtocolToolDefinition
	// Preloads are trusted host helpers evaluated before the cell's source.
	Preloads []ToolPreload
}

type EngineResult struct {
	ContentItems []ContentItem
}

type EngineFactory interface {
	NewEngine() (Engine, error)
}

type SobekEngineFactory struct{}

func (SobekEngineFactory) NewEngine() (Engine, error) {
	return NewSobekEngine(), nil
}
