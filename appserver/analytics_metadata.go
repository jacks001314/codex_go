package appserver

// analyticsEnabledOptionForThread reports the thread's effective analytics
// collection state for Responses turn metadata (Rust #44628/#44646). It is
// omitted when no session analytics context is initialized, and is false when
// the thread itself opted out through its configuration.
func (r *RuntimeRouter) analyticsEnabledOptionForThread(threadID string) *bool {
	if r == nil || r.services.Analytics == nil {
		return nil
	}
	provider, ok := r.services.Analytics.(interface{ Enabled() bool })
	if !ok {
		return nil
	}
	enabled := provider.Enabled()
	if enabled && r.threadAnalyticsDisabled(threadID) {
		enabled = false
	}
	return &enabled
}
