package appserver

// analyticsEnabledOption reports the selected session analytics client's
// collection state for Responses turn metadata (Rust #44628). It is omitted
// when no session analytics context is initialized, matching Rust's
// `Option<bool>` payload field.
func (r *RuntimeRouter) analyticsEnabledOption() *bool {
	if r == nil || r.services.Analytics == nil {
		return nil
	}
	provider, ok := r.services.Analytics.(interface{ Enabled() bool })
	if !ok {
		return nil
	}
	enabled := provider.Enabled()
	return &enabled
}
