//go:build windows

package mxc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobBasicUIRestrictions mirrors JOBOBJECT_BASIC_UI_RESTRICTIONS. x/sys/windows
// exposes the JobObjectBasicUIRestrictions information class but not the struct.
type jobBasicUIRestrictions struct {
	UIRestrictionsClass uint32
}

// newSandboxJob creates the kill-on-close job and applies the requested UI
// restrictions, mirroring mxc-sdk's `UiJobObject::new()` + `set_ui_limits()`
// sequence. Both steps must succeed: a job missing either is a weaker sandbox,
// so callers treat an error as a failed launch and terminate the child.
func newSandboxJob(policy NativeContainerPolicy) (windows.Handle, error) {
	job, err := newKillOnCloseJob()
	if err != nil {
		return 0, err
	}
	requested := EffectiveUIRestrictionMask(policy.UI, policy.BaseProcessUI)
	if err := applyJobUILimits(job, requested); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// applyJobUILimits applies `requested & supported` to the job object. The SDK
// deliberately masks instead of failing so a build that rejects the newer bits
// still receives every flag it can enforce (`job_object.rs::set_ui_limits`).
func applyJobUILimits(job windows.Handle, requested uint64) error {
	if job == 0 {
		return fmt.Errorf("MXC requires a job object handle")
	}
	info := jobBasicUIRestrictions{UIRestrictionsClass: EffectiveJobUILimitMask(requested)}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectBasicUIRestrictions,
		uintptr(unsafePointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return fmt.Errorf("SetInformationJobObject(UI): %w", err)
	}
	return nil
}
