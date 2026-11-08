package mxc

import "unsafe"

// Capability bits from `QueryProcessSecurityEnvironmentSupport`. They mirror the
// MXC SDK's `SecurityEnvironmentSupport` enum: deny paths need
// PSE_SUPPORT_FS_DENY, enumeration-only grants need
// PSE_SUPPORT_FS_ENUMERATE, and ingress policy needs PSE_SUPPORT_NET_INGRESS.
const (
	psecSupportFileSystemDeny      uint64 = 0x0000_0000_0000_0001
	psecSupportFileSystemEnumerate uint64 = 0x0000_0000_0000_0004
	psecSupportNetworkIngress      uint64 = 0x0000_0000_0000_0008
)

// unsafePointer keeps the cgo-free syscall calls readable: every PSEC export
// takes a pointer to a stack value whose address must be passed as uintptr.
func unsafePointer[T any](value *T) unsafe.Pointer {
	return unsafe.Pointer(value)
}

// TTYUnsupportedMessage is the stable diagnostic for a TTY launch that this
// build cannot serve. Callers assert against this constant so the message can
// never drift away from the capability it describes.
const TTYUnsupportedMessage = "MXC live session does not support TTY launches yet"

// nativeTTYLaunchSupported is the single switch behind NativeTTYLaunchSupported.
//
// It is true because the two-stage launcher exists and its console output is
// proven to reach the caller: SpawnLauncherSession starts our own helper under a
// pseudoconsole, the helper enters the PSEC sandbox, and the workload's console
// bytes come back through the session readers
// (TestLauncherSessionDeliversConsoleContent, plus the opt-in end-to-end
// TestLauncherSessionRunsSandboxedCommandThroughHelper; see
// update/plan_2026_11_08_win.md sections 17 and 18). A PSEC child cannot carry a
// pseudoconsole, which is why TTY never runs through SpawnLiveSession.
//
// Turning this off again is only correct together with the tests that assert the
// content, because a TTY launch that starts a process without a usable console
// would be worse than failing closed.
const nativeTTYLaunchSupported = true

// NativeTTYLaunchSupported reports whether an MXC live session can carry a
// pseudoconsole. It is the single source of truth for SpawnLauncherSession's gate
// and the exec-server's capability assertions, so the two cannot disagree;
// promoting or revoking TTY support is a one-constant change.
func NativeTTYLaunchSupported() bool {
	return nativeTTYLaunchSupported
}

// NativeLaunchSupported reports whether this helper can actually start a command
// inside the native MXC sandbox. It is the single source of truth shared by the
// launcher and by sandbox.WindowsMXCAvailable(): availability is the host's PSEC
// probe, because the "PSEC" spec encoder covers every policy field the helper
// emits. A future policy field the encoder cannot represent must be added here
// (fail closed) rather than silently dropped.
func NativeLaunchSupported() bool {
	return ProbeNativeSupport().Available
}
