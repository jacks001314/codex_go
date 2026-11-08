package mxc

// NativeRequest is the portable projection of the request MXC's native
// launcher consumes. Go has no binding to the native SDK, so this type exists
// for policy parity and regression tests rather than for serialization.
type NativeRequest struct {
	// ScriptCode is the exact argv rendered with Windows CreateProcess
	// quoting; the launcher never re-parses it.
	ScriptCode string
	// WorkingDirectory is the helper's directory, which is deliberately
	// separate from the policy working directory.
	WorkingDirectory string
	// Env is the filtered child environment, or nil to inherit nothing.
	Env    []string
	Policy NativeContainerPolicy
}

// NativeContainerPolicy carries the kernel policy the native launcher applies.
type NativeContainerPolicy struct {
	Capabilities         []string
	ReadWritePaths       []string
	ReadOnlyPaths        []string
	DeniedPaths          []string
	NetworkEgress        NetworkEgressPolicy
	NetworkIngress       NetworkIngressPolicy
	NetworkSpecified     bool
	NetworkModeSpecified bool
	// ProxyURL selects MXC's proxy network-policy form (`network_policy.proxy`).
	// It is mutually exclusive with direct egress rules; empty means direct
	// egress (Rust `build_psec_v1_network_policy`).
	ProxyURL string
	// UI keeps clipboard, input-injection, and system-control restrictions in
	// place. Only Win32k calls and desktop handles are required, and those come
	// from the base-process desktop isolation below (Rust #44903).
	UI            UIPolicy
	BaseProcessUI BaseProcessUI
}

// UIPolicy mirrors MXC's UiPolicy for the fields the helper sets explicitly.
// The remaining fields inherit the SDK defaults, which keep the restrictions.
type UIPolicy struct {
	Disable bool
}

// BaseProcessUI mirrors MXC's BaseProcessUiConfig. PowerShell needs desktop
// handles during DLL startup, so the helper isolates to a desktop.
type BaseProcessUI struct {
	Isolation string
}
