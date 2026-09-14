//go:build !cgo

package voicehost

// NewMiniAudioRuntime returns the lifecycle-only runtime for builds without
// cgo. The miniaudio backend is a cgo binding
// (github.com/gen2brain/malgo), so its device types vanish when cgo is
// disabled - which is the case for the cross-compiled release binaries. Those
// builds keep the helper's control plane and report the null runtime (no
// devices, audio opens rejected) instead of failing to compile, matching a
// helper-only voice package.
func NewMiniAudioRuntime() Runtime {
	return NullRuntime{}
}
