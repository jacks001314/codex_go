package mxc

// UI-limit capability gating, ported from mxc-sdk
// `backends/process_container/common/job_object.rs`.
//
// Two distinct encodings consume the resolved UI restriction policy:
//
//   - The PSEC `ui_restrictions` field (see psec_spec.go) carries the *full*
//     requested mask. The SDK serializes
//     `to_job_object_uilimit_mask(resolve_ui_restrictions(..))` with no build
//     gating (`base_container_helpers.rs`).
//   - The job-object application path intersects the requested mask with the
//     flags the running OS build accepts, because older builds reject the newer
//     bits with ERROR_INVALID_PARAMETER for
//     `SetInformationJobObject(JobObjectBasicUIRestrictions)`.
//
// Go reproduces both halves: EffectiveUIRestrictionMask feeds the PSEC field
// unchanged, and EffectiveJobUILimitMask is what reaches the kernel.

const (
	// allDefinedUILimits is every JOB_OBJECT_UILIMIT_* bit this package's
	// encoder (EffectiveUIRestrictionMask) can emit. It is the universe for the
	// build-gated intersection, so it must stay in sync with that encoder; the
	// SDK pins the same invariant with its `encoder_known_bit_positions` test.
	allDefinedUILimits uint32 = 0x0000_03FF

	// minBuildForIMELimit is the first OS build that accepts
	// JOB_OBJECT_UILIMIT_IME (0x100). Windows Server 2022 (20348) rejects it and
	// Windows 11 22H2 (22621) accepts it, so the flag is gated at the 22H2
	// boundary: builds below omit it, which never hands the kernel a flag it
	// would reject because UI-limit support is monotonic.
	minBuildForIMELimit uint32 = 22621

	// minBuildForInjectionLimit is the first OS build that accepts
	// JOB_OBJECT_UILIMIT_INJECTION (0x200): Windows 11 24H2 (26100).
	minBuildForInjectionLimit uint32 = 26100
)

// SupportedUILimitMaskForBuild returns the subset of JOB_OBJECT_UILIMIT_* flags
// the given OS build can enforce. Every flag except the two build-gated ones is
// universally supported.
func SupportedUILimitMaskForBuild(build uint32) uint32 {
	supported := allDefinedUILimits
	if build < minBuildForIMELimit {
		supported &^= uint32(jobUIlimitIME)
	}
	if build < minBuildForInjectionLimit {
		supported &^= uint32(jobUIlimitInjection)
	}
	return supported
}

// SupportedUILimitMask returns the flags this host can enforce. An
// indeterminable build number reports the full mask, so a failed probe keeps the
// more restrictive flag instead of silently dropping it (fail secure).
func SupportedUILimitMask() uint32 {
	return SupportedUILimitMaskForBuild(OSBuildNumber())
}

// EffectiveJobUILimitMask is the mask to hand to
// SetInformationJobObject(JobObjectBasicUIRestrictions): requested ∩ supported.
func EffectiveJobUILimitMask(requested uint64) uint32 {
	return uint32(requested) & SupportedUILimitMask()
}
