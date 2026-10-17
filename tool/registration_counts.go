package tool

// RegistrationCounts is the finalized tool inventory grouped by exposure.
// Hosted tools are not part of a Registry and therefore never appear here.
type RegistrationCounts struct {
	Direct            int
	DirectModelOnly   int
	Deferred          int
	DeferredModelOnly int
	CodeModeOnly      int
	Hidden            int
}

// RegistrationCounts returns the registered tools grouped by exposure
// (Rust ToolRegistry::entries plus #51868
// record_registration_metrics).
func (r *Registry) RegistrationCounts() RegistrationCounts {
	if r == nil {
		return RegistrationCounts{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	var counts RegistrationCounts
	for _, spec := range r.specs {
		switch spec.Exposure {
		case "", ExposureModelVisible:
			counts.Direct++
		case ExposureDirectModelOnly:
			counts.DirectModelOnly++
		case ExposureDiscoverable:
			counts.Deferred++
		case ExposureDeferredModelOnly:
			counts.DeferredModelOnly++
		case ExposureCodeModeOnly:
			counts.CodeModeOnly++
		case ExposureHidden:
			counts.Hidden++
		}
	}
	return counts
}
