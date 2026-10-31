//go:build windows

package windowsandboxservice

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file carries the minimal Windows Runtime ABI the service needs for the
// package deployment surface the Rust service uses. WinRT interfaces are
// reached through their vtable slots; every constant below is the slot index
// from the C++/WinRT projection of the same interface in the Windows SDK.

const (
	// roInitMultithreaded is the apartment model service threads use.
	roInitMultithreaded = 1
	// rpcEChangedMode reports an apartment that another model initialized.
	rpcEChangedMode = 0x80010106

	// iUnknownQueryInterface and iUnknownRelease are the IUnknown slots every
	// COM object starts with.
	iUnknownQueryInterface = 0
	iUnknownRelease        = 2

	// iInspectableMethodCount counts IUnknown plus GetIids,
	// GetRuntimeClassName and GetTrustLevel, so the first method of the
	// projected interface sits at slot 6.
	iInspectableMethodCount = 6

	// iIterableFirst is Windows.Foundation.Collections.IIterable<T>.First.
	iIterableFirst = iInspectableMethodCount + 0
	// iIteratorCurrent, iIteratorHasCurrent and iIteratorMoveNext are the
	// IIterator<T> members the package loop uses.
	iIteratorCurrent    = iInspectableMethodCount + 0
	iIteratorHasCurrent = iInspectableMethodCount + 1
	iIteratorMoveNext   = iInspectableMethodCount + 2

	// iPackageID is Windows.ApplicationModel.IPackage.Id.
	iPackageID = iInspectableMethodCount + 0
	// iPackageIdFullName is Windows.ApplicationModel.IPackageId.FullName.
	iPackageIdFullName = iInspectableMethodCount + 6
)

var (
	modCombase                    = windows.NewLazySystemDLL("combase.dll")
	procRoInitialize              = modCombase.NewProc("RoInitialize")
	procRoUninitialize            = modCombase.NewProc("RoUninitialize")
	procRoActivateInstance        = modCombase.NewProc("RoActivateInstance")
	procWindowsCreateString       = modCombase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = modCombase.NewProc("WindowsDeleteString")
	procWindowsGetStringLen       = modCombase.NewProc("WindowsGetStringLen")
	procWindowsGetStringRawBuffer = modCombase.NewProc("WindowsGetStringRawBuffer")
)

// hresultError is a failed COM/WinRT status code.
type hresultError uint32

func (err hresultError) Error() string {
	return fmt.Sprintf("HRESULT 0x%08X", uint32(err))
}

func hresultFailed(status uintptr) bool {
	return int32(uint32(status)) < 0
}

// comPointer recovers a pointer a syscall reported as an integer.
func comPointer(value uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&value))
}

// comMethod resolves one vtable entry of a COM object.
func comMethod(instance uintptr, slot uintptr) uintptr {
	vtable := *(*uintptr)(comPointer(instance))
	return *(*uintptr)(comPointer(vtable + slot*unsafe.Sizeof(uintptr(0))))
}

// comCall invokes one vtable entry and converts a failing status to an error.
func comCall(instance uintptr, slot uintptr, arguments ...uintptr) error {
	call := make([]uintptr, 0, len(arguments)+1)
	call = append(call, instance)
	call = append(call, arguments...)
	status, _, _ := syscall.SyscallN(comMethod(instance, slot), call...)
	if hresultFailed(status) {
		return hresultError(uint32(status))
	}
	return nil
}

// comRelease drops one reference to a COM object.
func comRelease(instance uintptr) {
	if instance != 0 {
		_ = comCall(instance, iUnknownRelease)
	}
}

// comQueryInterface obtains one interface from an activated object.
func comQueryInterface(instance uintptr, iid *windows.GUID) (uintptr, error) {
	var queried uintptr
	if err := comCall(
		instance,
		iUnknownQueryInterface,
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&queried)),
	); err != nil {
		return 0, err
	}
	return queried, nil
}

// newRuntimeString wraps a Go string as an HSTRING the caller must delete.
func newRuntimeString(value string) (uintptr, error) {
	units, err := windows.UTF16FromString(value)
	if err != nil {
		return 0, err
	}
	var handle uintptr
	status, _, _ := procWindowsCreateString.Call(
		uintptr(unsafe.Pointer(&units[0])),
		uintptr(len(units)-1),
		uintptr(unsafe.Pointer(&handle)),
	)
	runtime.KeepAlive(units)
	if hresultFailed(status) {
		return 0, hresultError(uint32(status))
	}
	return handle, nil
}

// deleteRuntimeString releases an HSTRING created by newRuntimeString.
func deleteRuntimeString(handle uintptr) {
	if handle != 0 {
		procWindowsDeleteString.Call(handle)
	}
}

// runtimeStringValue copies an HSTRING's contents.
func runtimeStringValue(handle uintptr) string {
	if handle == 0 {
		return ""
	}
	length, _, _ := procWindowsGetStringLen.Call(handle)
	buffer, _, _ := procWindowsGetStringRawBuffer.Call(handle, 0)
	if buffer == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(comPointer(buffer)), int(uint32(length))))
}

// withRuntimeApartment runs operation on a thread inside the multithreaded
// Windows Runtime apartment, because apartment state is per thread.
func withRuntimeApartment(operation func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	status, _, _ := procRoInitialize.Call(roInitMultithreaded)
	switch {
	case !hresultFailed(status):
		defer procRoUninitialize.Call()
	case uint32(status) == rpcEChangedMode:
		// Another model already initialized this thread; WinRT stays usable.
	default:
		return fmt.Errorf("initialize the Windows Runtime: %w", hresultError(uint32(status)))
	}
	return operation()
}

// activateRuntimeClass activates one WinRT runtime class and returns its
// default interface, the way Rust PackageManager::new does.
func activateRuntimeClass(classID string, defaultInterface *windows.GUID) (uintptr, error) {
	var activated uintptr
	err := withRuntimeApartment(func() error {
		class, err := newRuntimeString(classID)
		if err != nil {
			return err
		}
		defer deleteRuntimeString(class)
		status, _, _ := procRoActivateInstance.Call(class, uintptr(unsafe.Pointer(&activated)))
		if hresultFailed(status) {
			return hresultError(uint32(status))
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if defaultInterface == nil {
		return activated, nil
	}
	queried, err := comQueryInterface(activated, defaultInterface)
	comRelease(activated)
	if err != nil {
		return 0, err
	}
	return queried, nil
}
