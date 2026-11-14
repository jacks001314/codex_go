package apps

// AvailableForGuidance returns callable connector metadata without fetching the
// remote app directory. Guidance is based on tools already available to the
// thread, not on the marketplace's discoverable inventory.
func (s *AppService) AvailableForGuidance(threadID string, values map[string]any) ([]AppEntry, error) {
	snapshot := s.snapshot()
	accessible, _, err := s.accessibleAppsForList(snapshot.accessibleProvider, &AppAccessibleListParams{ThreadID: threadID}, snapshot.staticApps)
	if err != nil {
		return nil, err
	}
	return WithAppEnabledState(accessible, AppsConfigFromValues(values), nil), nil
}
