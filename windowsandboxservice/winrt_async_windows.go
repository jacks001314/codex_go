//go:build windows

package windowsandboxservice

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// iAsyncOperationResults is IAsyncOperationWithProgress<T,P>.GetResults.
	iAsyncOperationResults = iInspectableMethodCount + 0

	// iAsyncInfoStatus and iAsyncInfoErrorCode are IAsyncInfo members.
	iAsyncInfoStatus    = iInspectableMethodCount + 1
	iAsyncInfoErrorCode = iInspectableMethodCount + 2

	// iDeploymentResultErrorText, iDeploymentResultActivityID and
	// iDeploymentResultExtendedError are IDeploymentResult members.
	iDeploymentResultErrorText     = iInspectableMethodCount + 0
	iDeploymentResultActivityID    = iInspectableMethodCount + 1
	iDeploymentResultExtendedError = iInspectableMethodCount + 2

	// asyncStatusStarted and asyncStatusCompleted are Windows.Foundation
	// AsyncStatus values; canceled and error follow.
	asyncStatusStarted   = 0
	asyncStatusCompleted = 1
	asyncStatusCanceled  = 2
	asyncStatusError     = 3
	asyncStatusClosed    = 4
)

// asyncInfoIID is IID_IAsyncInfo, the fixed interface every asynchronous
// WinRT operation implements.
var asyncInfoIID = windows.GUID{
	Data1: 0x00000036, Data2: 0x0000, Data3: 0x0000,
	Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
}

// AsyncOperation is one WinRT IAsyncOperationWithProgress with the IAsyncInfo
// view the status loop needs.
type AsyncOperation struct {
	operation uintptr
	info      uintptr
}

// wrapAsyncOperation takes ownership of an operation pointer.
func wrapAsyncOperation(operation uintptr) (*AsyncOperation, error) {
	if operation == 0 {
		return nil, fmt.Errorf("WinRT operation is missing")
	}
	info, err := comQueryInterface(operation, &asyncInfoIID)
	if err != nil {
		comRelease(operation)
		return nil, fmt.Errorf("open the WinRT operation status: %w", err)
	}
	return &AsyncOperation{operation: operation, info: info}, nil
}

// Close releases the operation. A retained operation must not be closed.
func (async *AsyncOperation) Close() {
	if async == nil {
		return
	}
	if async.info != 0 {
		comRelease(async.info)
		async.info = 0
	}
	if async.operation != 0 {
		comRelease(async.operation)
		async.operation = 0
	}
}

// Retain drops every reference without releasing them, which keeps an
// in-flight deployment alive until this own-process service exits (Rust
// std::mem::forget).
func (async *AsyncOperation) Retain() {
	if async == nil {
		return
	}
	async.info = 0
	async.operation = 0
}

// Status reads Windows.Foundation.AsyncStatus.
func (async *AsyncOperation) Status() (int32, error) {
	if async == nil || async.info == 0 {
		return 0, fmt.Errorf("WinRT operation is not open")
	}
	var status int32
	if err := comCall(async.info, iAsyncInfoStatus, uintptr(unsafe.Pointer(&status))); err != nil {
		return 0, err
	}
	return status, nil
}

// ErrorCode reads the operation's failure HRESULT.
func (async *AsyncOperation) ErrorCode() (uint32, error) {
	if async == nil || async.info == 0 {
		return 0, fmt.Errorf("WinRT operation is not open")
	}
	var code uint32
	if err := comCall(async.info, iAsyncInfoErrorCode, uintptr(unsafe.Pointer(&code))); err != nil {
		return 0, err
	}
	return code, nil
}

// deploymentDetails is the diagnostic subset of IDeploymentResult the service
// reports; free-form Windows ErrorText stays in deployment diagnostics.
type deploymentDetails struct {
	extendedError    uint32
	extendedErrorErr error
	activityID       windows.GUID
	activityIDErr    error
}

// results reads IDeploymentResult for a completed operation.
func (async *AsyncOperation) results() (*deploymentDetails, error) {
	if async == nil || async.operation == 0 {
		return nil, fmt.Errorf("WinRT operation is not open")
	}
	var result uintptr
	if err := comCall(async.operation, iAsyncOperationResults, uintptr(unsafe.Pointer(&result))); err != nil {
		return nil, err
	}
	if result == 0 {
		return nil, fmt.Errorf("WinRT operation returned no deployment result")
	}
	defer comRelease(result)
	details := &deploymentDetails{}
	if err := comCall(result, iDeploymentResultExtendedError, uintptr(unsafe.Pointer(&details.extendedError))); err != nil {
		details.extendedErrorErr = err
	}
	if err := comCall(result, iDeploymentResultActivityID, uintptr(unsafe.Pointer(&details.activityID))); err != nil {
		details.activityIDErr = err
	}
	return details, nil
}
