package backends

func CommonBackendKind(elevated bool) BackendKind {
	if elevated {
		return BackendElevated
	}
	return BackendLegacy
}
