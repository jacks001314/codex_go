package backends

type BackendKind string

const (
	BackendLegacy   BackendKind = "legacy"
	BackendElevated BackendKind = "elevated"
)
