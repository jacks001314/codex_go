//go:build windows

package mxc

import (
	"math"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Mirrors mxc-sdk's `set_ui_limits_all_flags_succeeds_on_any_build`: requesting
// every defined flag can never fail, because the applied mask is intersected
// with what the host build supports before it reaches the kernel.
func TestApplyJobUILimitsAllFlagsSucceedsOnAnyBuild(t *testing.T) {
	job, err := newKillOnCloseJob()
	if err != nil {
		t.Fatalf("CreateJobObject error = %v", err)
	}
	defer windows.CloseHandle(job)
	if err := applyJobUILimits(job, uint64(allDefinedUILimits)); err != nil {
		t.Fatalf("applyJobUILimits(every defined flag) error = %v", err)
	}
	var applied jobBasicUIRestrictions
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectBasicUIRestrictions,
		uintptr(unsafePointer(&applied)),
		uint32(unsafe.Sizeof(applied)),
		nil,
	); err != nil {
		t.Fatalf("QueryInformationJobObject(UI) error = %v", err)
	}
	if want := SupportedUILimitMask(); applied.UIRestrictionsClass != want {
		t.Fatalf("applied UI restrictions = %#04x, want host-supported %#04x", applied.UIRestrictionsClass, want)
	}
}

// The launcher's job must carry the policy's encoder mask intersected with the
// host support mask; a job without UI limits is a weaker sandbox.
func TestNewSandboxJobAppliesPolicyUILimits(t *testing.T) {
	policy := NativeContainerPolicy{UI: UIPolicy{Disable: true}, BaseProcessUI: BaseProcessUI{Isolation: "desktop"}}
	job, err := newSandboxJob(policy)
	if err != nil {
		t.Fatalf("newSandboxJob error = %v", err)
	}
	defer windows.CloseHandle(job)
	var applied jobBasicUIRestrictions
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectBasicUIRestrictions,
		uintptr(unsafePointer(&applied)),
		uint32(unsafe.Sizeof(applied)),
		nil,
	); err != nil {
		t.Fatalf("QueryInformationJobObject(UI) error = %v", err)
	}
	want := EffectiveJobUILimitMask(EffectiveUIRestrictionMask(policy.UI, policy.BaseProcessUI))
	if applied.UIRestrictionsClass != want {
		t.Fatalf("job UI restrictions = %#04x, want %#04x", applied.UIRestrictionsClass, want)
	}
	if want == 0 {
		t.Fatal("expected a non-empty UI restriction mask for disable=true")
	}
}

// A nil job handle is a programming error, not a silent no-op: the caller would
// otherwise launch with no UI restrictions at all.
func TestApplyJobUILimitsRejectsNilJob(t *testing.T) {
	if err := applyJobUILimits(0, uint64(allDefinedUILimits)); err == nil {
		t.Fatal("applyJobUILimits(0, ...) succeeded, want error")
	}
}

// The host probe must agree with the pure per-build map; an unreadable build
// number stays on the "modern" fail-secure branch.
func TestSupportedUILimitMaskMatchesHostBuild(t *testing.T) {
	build := OSBuildNumber()
	if build == 0 {
		t.Fatal("OSBuildNumber() = 0, want a real build or math.MaxUint32")
	}
	if got, want := SupportedUILimitMask(), SupportedUILimitMaskForBuild(build); got != want {
		t.Fatalf("SupportedUILimitMask() = %#04x, want %#04x for build %d", got, want, build)
	}
	if build == math.MaxUint32 && SupportedUILimitMask() != allDefinedUILimits {
		t.Fatalf("unknown build mask = %#04x, want full %#04x", SupportedUILimitMask(), allDefinedUILimits)
	}
}
