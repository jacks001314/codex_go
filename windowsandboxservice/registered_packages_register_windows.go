//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// packageManager2RegisterByFullName is
	// IPackageManager2.RegisterPackageByFullNameAsync.
	packageManager2RegisterByFullName = iInspectableMethodCount + 2
	// deploymentOptionsNone leaves dependency, force and staging options off.
	deploymentOptionsNone = 0
)

// packageManager2IID is IID_IPackageManager2.
var packageManager2IID = windows.GUID{
	Data1: 0xf7aad08d, Data2: 0x0840, Data3: 0x46f2,
	Data4: [8]byte{0xb5, 0xd8, 0xca, 0xd4, 0x76, 0x93, 0xa0, 0x95},
}

// RegisterPackageByFullNameAsync starts registering one package for the
// calling user without any dependencies (Rust
// PackageManager::RegisterPackageByFullNameAsync with DeploymentOptions::None).
func (manager *PackageManager) RegisterPackageByFullNameAsync(fullName string) (*AsyncOperation, error) {
	if manager == nil || manager.instance == 0 {
		return nil, fmt.Errorf("deployment package manager is not open")
	}
	registration, err := comQueryInterface(manager.instance, &packageManager2IID)
	if err != nil {
		return nil, fmt.Errorf("open the deployment registration surface: %w", err)
	}
	defer comRelease(registration)
	name, err := newRuntimeString(fullName)
	if err != nil {
		return nil, err
	}
	defer deleteRuntimeString(name)
	var operation uintptr
	if err := comCall(
		registration,
		packageManager2RegisterByFullName,
		name,
		0,
		deploymentOptionsNone,
		uintptr(unsafe.Pointer(&operation)),
	); err != nil {
		return nil, err
	}
	return wrapAsyncOperation(operation)
}

// registrationResult retains the distinct native registration failure codes and
// the deployment activity ID (Rust registered_runtime/registration_result.rs).
// Diagnostic getters never replace the completion error or turn success into
// failure.
type registrationResult struct {
	status        int32
	asyncError    uint32
	asyncErrorErr error
	details       *deploymentDetails
	detailsErr    error
}

// readRegistrationResult reads a terminal operation while the caller still
// retains the profile and token.
func readRegistrationResult(async *AsyncOperation, status int32) *registrationResult {
	result := &registrationResult{status: status}
	result.asyncError, result.asyncErrorErr = async.ErrorCode()
	result.details, result.detailsErr = async.results()
	return result
}

// errorPackagedServiceRequiresAdminPrivileges is Win32 error 15656 surfaced as
// an HRESULT by deployment results (Rust #60944).
const errorPackagedServiceRequiresAdminPrivileges uint32 = 0x80073D28

// check reports the deployment outcome with Rust's phase context.
// registeredForTarget may reconcile a terminal service-privilege error: Windows
// reports it after another deployment already registered the same package
// version for the target account, so the exact postcondition is checked before
// the error is accepted as a success.
func (result *registrationResult) check(registeredForTarget func() bool) error {
	if result == nil {
		return errors.New("missing managed runtime registration result")
	}
	asyncError := fmt.Sprintf("0x%08X", result.asyncError)
	if result.asyncErrorErr != nil {
		asyncError = fmt.Sprintf("unavailable(%s)", hresultText(result.asyncErrorErr))
	}
	context := fmt.Sprintf(
		"finish managed runtime package registration: status=%d async_error=%s",
		result.status,
		asyncError,
	)
	if result.detailsErr != nil {
		return fmt.Errorf("%s phase=GetResults get_results=%s", context, hresultText(result.detailsErr))
	}
	if result.details == nil {
		return fmt.Errorf("%s phase=GetResults get_results=missing", context)
	}
	activityID := fmt.Sprintf("unavailable(%s)", hresultText(result.details.activityIDErr))
	if result.details.activityIDErr == nil {
		activityID = formatGUID(result.details.activityID)
	}
	context = fmt.Sprintf("%s get_results=0x00000000 activity_id=%s", context, activityID)
	if result.details.extendedErrorErr != nil {
		return fmt.Errorf(
			"%s phase=ExtendedErrorCode getter_error=%s",
			context,
			hresultText(result.details.extendedErrorErr),
		)
	}
	if result.status == asyncStatusError &&
		result.details.extendedError == errorPackagedServiceRequiresAdminPrivileges &&
		registeredForTarget != nil && registeredForTarget() {
		return nil
	}
	if int32(result.details.extendedError) < 0 {
		return fmt.Errorf(
			"%s phase=deployment extended_error=0x%08X",
			context,
			result.details.extendedError,
		)
	}
	return nil
}

// hresultText renders one failure as Rust's HRESULT debug text does.
func hresultText(err error) string {
	if err == nil {
		return "0x00000000"
	}
	var status hresultError
	if errors.As(err, &status) {
		return fmt.Sprintf("0x%08X", uint32(status))
	}
	return err.Error()
}

// formatGUID renders a deployment activity ID.
func formatGUID(guid windows.GUID) string {
	return fmt.Sprintf(
		"%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		guid.Data1,
		guid.Data2,
		guid.Data3,
		guid.Data4[0],
		guid.Data4[1],
		guid.Data4[2],
		guid.Data4[3],
		guid.Data4[4],
		guid.Data4[5],
		guid.Data4[6],
		guid.Data4[7],
	)
}
