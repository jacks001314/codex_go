package codemode

import (
	"bytes"
	"errors"
	"sync"
)

// codeModeCatalogCache mirrors Rust #61846's client-side `Catalog` plus
// `CatalogAdmission`: one revision per session binding, an admission in flight
// blocks other admissions, and a replacement is committed only after the host
// accepts the execution.
type codeModeCatalogCache struct {
	mu        sync.Mutex
	revision  uint64
	tools     []ProtocolToolDefinition
	haveTools bool
	pending   bool
}

type codeModeCatalogAdmission struct {
	cache       *codeModeCatalogCache
	revision    uint64
	replacement []ProtocolToolDefinition
}

// prepare mirrors `CatalogAdmission::prepare`: an empty catalog or a
// concurrent admission falls back to a self-contained request, identical tools
// reuse the installed revision, and changed tools reserve the next revision
// until acceptance.
func (c *codeModeCatalogCache) prepare(tools []ProtocolToolDefinition) *codeModeCatalogAdmission {
	if c == nil || len(tools) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending {
		return nil
	}
	c.pending = true
	if c.haveTools && protocolToolDefinitionsEqual(c.tools, tools) {
		return &codeModeCatalogAdmission{cache: c, revision: c.revision}
	}
	if c.revision == ^uint64(0) {
		// Rust's checked_add disables reuse instead of wrapping.
		c.pending = false
		return nil
	}
	c.revision++
	// Clearing the snapshot first keeps cancellation from reusing a stale
	// revision (Rust CatalogAdmission::prepare).
	c.tools = nil
	c.haveTools = false
	return &codeModeCatalogAdmission{cache: c, revision: c.revision, replacement: cloneProtocolToolDefinitions(tools)}
}

func (a *codeModeCatalogAdmission) reusesCatalog() bool {
	return a != nil && a.replacement == nil
}

func (a *codeModeCatalogAdmission) accept() {
	if a == nil || a.cache == nil {
		return
	}
	a.cache.mu.Lock()
	defer a.cache.mu.Unlock()
	if a.replacement != nil {
		a.cache.tools = a.replacement
		a.cache.haveTools = true
	}
	a.cache.pending = false
}

func (a *codeModeCatalogAdmission) abandon() {
	if a == nil || a.cache == nil {
		return
	}
	a.cache.mu.Lock()
	defer a.cache.mu.Unlock()
	a.cache.pending = false
}

// cloneProtocolToolDefinitions snapshots a catalog so later mutations by the
// caller cannot change an installed revision.
func cloneProtocolToolDefinitions(tools []ProtocolToolDefinition) []ProtocolToolDefinition {
	if tools == nil {
		return nil
	}
	cloned := make([]ProtocolToolDefinition, len(tools))
	for index, definition := range tools {
		cloned[index] = definition
		cloned[index].ToolName = cloneToolName(definition.ToolName)
		cloned[index].InputSchema = append([]byte(nil), definition.InputSchema...)
		cloned[index].OutputSchema = append([]byte(nil), definition.OutputSchema...)
	}
	return cloned
}

func cloneToolName(name ToolName) ToolName {
	if name.Namespace != nil {
		namespace := *name.Namespace
		name.Namespace = &namespace
	}
	return name
}

// protocolToolDefinitionsEqual compares the fields Rust's
// `ToolDefinition` PartialEq covers, treating nil and empty bytes alike.
func protocolToolDefinitionsEqual(left, right []ProtocolToolDefinition) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !protocolToolDefinitionEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func protocolToolDefinitionEqual(left, right ProtocolToolDefinition) bool {
	if left.Name != right.Name ||
		left.ToolName.Name != right.ToolName.Name ||
		left.Description != right.Description ||
		left.Kind != right.Kind ||
		left.InputSchemaMaxBytes != right.InputSchemaMaxBytes {
		return false
	}
	if !optionalStringEqual(left.ToolName.Namespace, right.ToolName.Namespace) {
		return false
	}
	return bytes.Equal(left.InputSchema, right.InputSchema) && bytes.Equal(left.OutputSchema, right.OutputSchema)
}

func optionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// resolveHostCatalog mirrors Rust InProcessCodeModeSession::runtime_request
// (#61846): validate the revision, install a newer snapshot, reuse an exact
// match, and reject unknown or conflicting references.
func (s *stdioHostSession) resolveHostCatalog(request *ExecuteRequest) ([]ProtocolToolDefinition, error) {
	if request == nil {
		return nil, nil
	}
	if request.CatalogRevision == nil {
		return cloneProtocolToolDefinitions(request.EnabledTools), nil
	}
	revision := *request.CatalogRevision
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if revision == 0 {
		return nil, errors.New("invalid code-mode catalog revision")
	}
	tools := cloneProtocolToolDefinitions(request.EnabledTools)
	if len(tools) == 0 {
		if revision != s.catalogRevision {
			return nil, errors.New("unknown code-mode catalog revision")
		}
		return cloneProtocolToolDefinitions(s.catalogTools), nil
	}
	switch {
	case revision > s.catalogRevision:
		s.catalogRevision = revision
		s.catalogTools = cloneProtocolToolDefinitions(tools)
	case revision == s.catalogRevision && !protocolToolDefinitionsEqual(tools, s.catalogTools):
		return nil, errors.New("conflicting code-mode catalog revision")
	}
	// A late full request runs with its own tools (Rust #61846).
	return tools, nil
}
