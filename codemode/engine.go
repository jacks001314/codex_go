package codemode

import "context"

// Engine executes one isolated code-mode JavaScript program.
type Engine interface {
	Execute(context.Context, EngineRequest) (*EngineResult, error)
	Interrupt(error)
	Close() error
}

type EngineRequest struct {
	ToolCallID   string
	Source       string
	EnabledTools []ProtocolToolDefinition
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
