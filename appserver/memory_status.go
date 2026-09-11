package appserver

// Rust parity: codex-rs/app-server/src/request_processors/memory_status.rs and
// codex-rs/app-server-protocol/src/protocol/v2/memory.rs (#43827). The
// experimental memory/status endpoint reports whether background v2
// consolidation has produced enough context to switch to v2 memories, without
// exposing memory contents.

import (
	"os"
	"path/filepath"
	"strings"

	"codex_go/memories"
)

const (
	defaultMinConsolidatedThreads = 20
	minConsolidatedThreadsFloor   = 1
	minConsolidatedThreadsCeiling = 4096
)

// MemoryStatusParams is the experimental memory/status request.
type MemoryStatusParams struct {
	// Required distinct consolidated threads. Defaults to 20; supported range is 1..=4096.
	MinConsolidatedThreads *uint32 `json:"minConsolidatedThreads,omitempty"`
}

// MemoryStatusResponse reports v2 consolidation progress and readiness.
type MemoryStatusResponse struct {
	V2ConsolidatedThreads uint32 `json:"v2ConsolidatedThreads"`
	V2Ready               bool   `json:"v2Ready"`
}

func (r *RuntimeRouter) handleMemoryStatus(request *Request) (*MemoryStatusResponse, error) {
	var params MemoryStatusParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	minimum := uint32(defaultMinConsolidatedThreads)
	if params.MinConsolidatedThreads != nil {
		minimum = *params.MinConsolidatedThreads
	}
	if minimum < minConsolidatedThreadsFloor || minimum > minConsolidatedThreadsCeiling {
		return nil, invalidParams("minConsolidatedThreads must be between 1 and 4096")
	}
	codexHome := ""
	if r != nil && r.services.Config != nil {
		codexHome = strings.TrimSpace(r.services.Config.CodexHome())
	}
	count := memories.MaxConsolidatedThreadCount(codexHome)
	ready := count >= minimum && v2MemorySummaryValid(codexHome)
	return &MemoryStatusResponse{V2ConsolidatedThreads: count, V2Ready: ready}, nil
}

// v2MemorySummaryValid reads the v2 memory summary from the v2 root and applies
// the shared Rust summary validation.
func v2MemorySummaryValid(codexHome string) bool {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(memories.V2Root(codexHome), memories.MemorySummaryFilename))
	if err != nil {
		return false
	}
	return memories.IsValidV2Summary(string(data))
}
