package mcp

import "sync"

// CatalogTracer opens the spans the client tool catalog records (Rust #62320
// instruments `mcp.catalog.*` with the ambient tracing subscriber). Package mcp
// cannot import the telemetry client because telemetry reaches back through the
// turn package, so the app server installs an adapter over its request tracer
// and a missing tracer keeps every span a no-op.
type CatalogTracer interface {
	// StartCatalogSpan opens one named span and returns its end function.
	StartCatalogSpan(name string) (end func())
}

var (
	catalogTracerMu sync.RWMutex
	catalogTracer   CatalogTracer
)

// SetCatalogTracer installs the tracer the MCP catalog spans record to. A nil
// tracer disables them, matching a reload that turns tracing off.
func SetCatalogTracer(tracer CatalogTracer) {
	catalogTracerMu.Lock()
	defer catalogTracerMu.Unlock()
	catalogTracer = tracer
}

// catalogSpan is the nil-safe handle a catalog span caller defers.
type catalogSpan struct {
	end func()
}

func (s catalogSpan) End() {
	if s.end != nil {
		s.end()
	}
}

// startCatalogSpan opens one catalog span, or returns a handle that ends nothing
// when tracing is off.
func startCatalogSpan(name string) catalogSpan {
	catalogTracerMu.RLock()
	tracer := catalogTracer
	catalogTracerMu.RUnlock()
	if tracer == nil {
		return catalogSpan{}
	}
	return catalogSpan{end: tracer.StartCatalogSpan(name)}
}
