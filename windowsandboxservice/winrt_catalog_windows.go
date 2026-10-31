//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This file carries the Windows.ApplicationModel package-catalog surface the
// service subscribes to. One notification stream per authenticated owner
// outlives the call that created it, so it needs its own apartment and a
// COM delegate Go implements directly (Rust package_lifecycle
// register_authenticated_user and Drop).

const (
	// packageCatalogClassID is the runtime class Rust opens with
	// PackageCatalog::OpenForCurrentUser.
	packageCatalogClassID = "Windows.ApplicationModel.PackageCatalog"

	// packageCatalogOpenForCurrentUser is
	// IPackageCatalogStatics.OpenForCurrentUser.
	packageCatalogOpenForCurrentUser = iInspectableMethodCount + 1
	// packageCatalogPackageUninstalling and
	// packageCatalogRemovePackageUninstalling are the uninstall event pair on
	// IPackageCatalog.
	packageCatalogPackageUninstalling       = iInspectableMethodCount + 6
	packageCatalogRemovePackageUninstalling = iInspectableMethodCount + 7

	// packageUninstallingArgsPackage is IPackageUninstallingEventArgs.Package.
	packageUninstallingArgsPackage = iInspectableMethodCount + 1
	// packageUninstallingArgsComplete is
	// IPackageUninstallingEventArgs.IsComplete.
	packageUninstallingArgsComplete = iInspectableMethodCount + 3

	// typedEventHandlerAddRef and typedEventHandlerInvoke are the
	// ITypedEventHandler<TSender, TResult> slots that follow IUnknown.
	typedEventHandlerAddRef = 1
	typedEventHandlerInvoke = iUnknownRelease + 1
	typedEventHandlerSlots  = typedEventHandlerInvoke + 1

	// eFail and ePointer are the generic COM statuses a Go-side handler
	// returns when it has no HRESULT of its own.
	eFail    = 0x80004005
	ePointer = 0x80004003

	// typedEventHandlerObjectSize is the delegate's vtable pointer; every
	// remaining field lives in the Go handler the registry recovers from that
	// address.
	typedEventHandlerObjectSize = unsafe.Sizeof(uintptr(0))
)

var (
	// packageCatalogStaticsIID is IID_IPackageCatalogStatics.
	packageCatalogStaticsIID = windows.GUID{
		Data1: 0xa18c9696, Data2: 0xe65b, Data3: 0x4634,
		Data4: [8]byte{0xba, 0x21, 0x5e, 0x63, 0xeb, 0x72, 0x44, 0xa7},
	}

	// The delegate callbacks are process-wide thunks, so they are created once
	// and shared by every handler object.
	typedEventHandlerQueryInterfaceCallback = windows.NewCallback(typedEventHandlerQueryInterface)
	typedEventHandlerAddRefCallback         = windows.NewCallback(typedEventHandlerAddRefX)
	typedEventHandlerReleaseCallback        = windows.NewCallback(typedEventHandlerRelease)
	typedEventHandlerInvokeCallback         = windows.NewCallback(typedEventHandlerInvokeX)

	// typedEventHandlerVtable keeps the four exports at one stable address
	// every handler object points at.
	typedEventHandlerVtable = [typedEventHandlerSlots]uintptr{
		typedEventHandlerQueryInterfaceCallback,
		typedEventHandlerAddRefCallback,
		typedEventHandlerReleaseCallback,
		typedEventHandlerInvokeCallback,
	}

	// typedEventHandlers recovers the Go handler behind a delegate pointer the
	// Windows Runtime hands to one of the callbacks.
	typedEventHandlers sync.Map
)

// runtimeApartment holds one multithreaded Windows Runtime apartment open on a
// dedicated thread (Rust PackageLifecycle::new initializes once and
// uninitializes on drop). A subscription created here stays valid only while
// the apartment exists, so every catalog call is marshaled onto it.
type runtimeApartment struct {
	calls  chan func()
	stop   chan struct{}
	done   chan struct{}
	closed atomic.Bool
}

// startRuntimeApartment initializes the service apartment and keeps its thread
// resident until Close.
func startRuntimeApartment() (*runtimeApartment, error) {
	apartment := &runtimeApartment{
		calls: make(chan func()),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	started := make(chan error, 1)
	go apartment.run(started)
	if err := <-started; err != nil {
		return nil, err
	}
	return apartment, nil
}

func (apartment *runtimeApartment) run(started chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(apartment.done)
	status, _, _ := procRoInitialize.Call(roInitMultithreaded)
	switch {
	case !hresultFailed(status):
		defer procRoUninitialize.Call()
	case uint32(status) == rpcEChangedMode:
		// Another model already initialized this thread; WinRT stays usable.
	default:
		started <- fmt.Errorf("initialize the sandbox service Windows Runtime apartment: %w", hresultError(uint32(status)))
		return
	}
	started <- nil
	for {
		select {
		case call := <-apartment.calls:
			call()
		case <-apartment.stop:
			return
		}
	}
}

// do runs one operation on the apartment thread.
func (apartment *runtimeApartment) do(operation func() error) error {
	if apartment == nil {
		return errors.New("the Windows Runtime apartment is not running")
	}
	result := make(chan error, 1)
	select {
	case apartment.calls <- func() { result <- operation() }:
		return <-result
	case <-apartment.done:
		return errors.New("the Windows Runtime apartment has stopped")
	}
}

// Close stops the apartment thread once; the Windows Runtime apartment itself
// is uninitialized as that thread returns.
func (apartment *runtimeApartment) Close() {
	if apartment == nil || !apartment.closed.CompareAndSwap(false, true) {
		return
	}
	close(apartment.stop)
	<-apartment.done
}

// packageUninstallSubscription is the owner's live uninstall notification
// stream (Rust UserInstallation's catalog and registration token).
type packageUninstallSubscription struct {
	apartment *runtimeApartment
	catalog   uintptr
	token     int64
	handler   *typedEventHandler
	closed    atomic.Bool
}

// subscribePackageUninstalling opens the owner's package catalog and registers
// the uninstall handler; tests replace this seam.
var subscribePackageUninstalling = openPackageUninstallSubscription

// openPackageUninstallSubscription subscribes as the authenticated owner, which
// is how Rust reaches the owner's catalog inside with_owner_impersonation.
func openPackageUninstallSubscription(
	packageName string,
	ownerSID string,
	uninstalling *atomic.Bool,
	token windows.Token,
) (*packageUninstallSubscription, error) {
	apartment, err := startRuntimeApartment()
	if err != nil {
		return nil, err
	}
	subscription := &packageUninstallSubscription{apartment: apartment}
	err = apartment.do(func() error {
		return withOwnerImpersonation(token, func() error {
			catalog, err := openPackageCatalogForCurrentUser()
			if err != nil {
				return fmt.Errorf("open the authenticated user's package catalog: %w", err)
			}
			subscription.catalog = catalog
			handler, err := newTypedEventHandler(func(sender uintptr, args uintptr) error {
				if sender == 0 || args == 0 {
					return nil
				}
				event := packageUninstallEventArgs{instance: args}
				name, err := event.PackageFullName()
				if err != nil {
					return err
				}
				if name != packageName {
					return nil
				}
				return onPackageUninstalling(event, ownerSID, uninstalling)
			})
			if err != nil {
				return err
			}
			subscription.handler = handler
			registered, err := catalogPackageUninstalling(catalog, handler.pointer)
			if err != nil {
				return fmt.Errorf("subscribe to authenticated package uninstall notifications: %w", err)
			}
			subscription.token = registered
			return nil
		})
	})
	if err != nil {
		subscription.Close()
		return nil, err
	}
	return subscription, nil
}

// Close removes the registration and drops the catalog and apartment; Rust does
// the same from PackageLifecycle::drop.
func (subscription *packageUninstallSubscription) Close() {
	if subscription == nil || !subscription.closed.CompareAndSwap(false, true) {
		return
	}
	apartment := subscription.apartment
	if apartment == nil {
		return
	}
	_ = apartment.do(func() error {
		if subscription.catalog != 0 {
			if subscription.token != 0 {
				_ = comCall(
					subscription.catalog,
					packageCatalogRemovePackageUninstalling,
					uintptr(subscription.token),
				)
				subscription.token = 0
			}
			comRelease(subscription.catalog)
			subscription.catalog = 0
		}
		subscription.handler.release()
		subscription.handler = nil
		return nil
	})
	apartment.Close()
	subscription.apartment = nil
}

// openPackageCatalogForCurrentUser activates the catalog statics and opens the
// calling user's catalog (Rust PackageCatalog::OpenForCurrentUser).
func openPackageCatalogForCurrentUser() (uintptr, error) {
	factory, err := runtimeActivationFactory(packageCatalogClassID, &packageCatalogStaticsIID)
	if err != nil {
		return 0, err
	}
	defer comRelease(factory)
	var catalog uintptr
	if err := comCall(
		factory,
		packageCatalogOpenForCurrentUser,
		uintptr(unsafe.Pointer(&catalog)),
	); err != nil {
		return 0, err
	}
	if catalog == 0 {
		return 0, errors.New("the package catalog is missing")
	}
	return catalog, nil
}

// runtimeActivationFactory resolves one WinRT runtime class's activation
// factory (Rust windows_core's FactoryCache).
func runtimeActivationFactory(classID string, iid *windows.GUID) (uintptr, error) {
	class, err := newRuntimeString(classID)
	if err != nil {
		return 0, err
	}
	defer deleteRuntimeString(class)
	var factory uintptr
	status, _, _ := procRoGetActivationFactory.Call(
		class,
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if hresultFailed(status) {
		return 0, hresultError(uint32(status))
	}
	if factory == 0 {
		return 0, errors.New("the Windows Runtime activation factory is missing")
	}
	return factory, nil
}

// catalogPackageUninstalling adds the uninstall handler and returns its token.
func catalogPackageUninstalling(catalog uintptr, handler uintptr) (int64, error) {
	if catalog == 0 {
		return 0, errors.New("the package catalog is not open")
	}
	var token int64
	if err := comCall(
		catalog,
		packageCatalogPackageUninstalling,
		handler,
		uintptr(unsafe.Pointer(&token)),
	); err != nil {
		return 0, err
	}
	return token, nil
}

// packageUninstallEventArgs is one live
// Windows.ApplicationModel.PackageUninstallingEventArgs.
type packageUninstallEventArgs struct {
	instance uintptr
}

// PackageFullName reads event.Package().Id().FullName().
func (args packageUninstallEventArgs) PackageFullName() (string, error) {
	if args.instance == 0 {
		return "", errors.New("the package uninstall event is missing")
	}
	var pkg uintptr
	if err := comCall(
		args.instance,
		packageUninstallingArgsPackage,
		uintptr(unsafe.Pointer(&pkg)),
	); err != nil {
		return "", err
	}
	if pkg == 0 {
		return "", errors.New("the package uninstall event has no package")
	}
	defer comRelease(pkg)
	return packageFullName(pkg)
}

// IsComplete reads the event's completion flag. Windows removes an old version
// during an update, so only a finished uninstall may retire resources.
func (args packageUninstallEventArgs) IsComplete() (bool, error) {
	if args.instance == 0 {
		return false, errors.New("the package uninstall event is missing")
	}
	// WinRT booleans occupy one byte; a zeroed word reads them portably.
	var complete uint8
	if err := comCall(
		args.instance,
		packageUninstallingArgsComplete,
		uintptr(unsafe.Pointer(&complete)),
	); err != nil {
		return false, err
	}
	return complete != 0, nil
}

// typedEventHandler is the minimal ITypedEventHandler<PackageCatalog,
// PackageUninstallingEventArgs> the catalog keeps a reference to. The
// projection has a single Invoke method, so the Go object needs IUnknown plus
// that one slot.
type typedEventHandler struct {
	pointer uintptr
	refs    atomic.Int32
	invoke  func(sender uintptr, args uintptr) error
}

// newTypedEventHandler allocates a delegate whose vtable is the shared thunk
// array and whose Go handler is reachable from the object address.
func newTypedEventHandler(invoke func(sender uintptr, args uintptr) error) (*typedEventHandler, error) {
	if invoke == nil {
		return nil, errors.New("missing Windows Runtime event handler")
	}
	memory, err := windows.LocalAlloc(windows.LMEM_FIXED, uint32(typedEventHandlerObjectSize))
	if err != nil {
		return nil, err
	}
	*(*uintptr)(comPointer(memory)) = uintptr(unsafe.Pointer(&typedEventHandlerVtable[0]))
	handler := &typedEventHandler{pointer: memory, invoke: invoke}
	handler.refs.Store(1)
	typedEventHandlers.Store(memory, handler)
	return handler, nil
}

// release drops the reference this process holds; the catalog keeps its own
// until the registration is removed.
func (handler *typedEventHandler) release() {
	if handler == nil || handler.pointer == 0 {
		return
	}
	typedEventHandlerRelease(handler.pointer)
	handler.pointer = 0
}

// typedEventHandlerQueryInterface answers every interface identity, because the
// parameterized delegate IID depends on the WinRT type signature; the catalog
// only ever asks for the one delegate interface it was given.
func typedEventHandlerQueryInterface(this uintptr, _ uintptr, object uintptr) uintptr {
	if object == 0 {
		return uintptr(uint32(ePointer))
	}
	if _, ok := typedEventHandlers.Load(this); !ok {
		return uintptr(uint32(eFail))
	}
	typedEventHandlerAddRefX(this)
	*(*uintptr)(comPointer(object)) = this
	return 0
}

func typedEventHandlerAddRefX(this uintptr) uintptr {
	value, ok := typedEventHandlers.Load(this)
	if !ok {
		return 0
	}
	return uintptr(uint32(value.(*typedEventHandler).refs.Add(1)))
}

func typedEventHandlerRelease(this uintptr) uintptr {
	value, ok := typedEventHandlers.Load(this)
	if !ok {
		return 0
	}
	remaining := value.(*typedEventHandler).refs.Add(-1)
	if remaining <= 0 {
		typedEventHandlers.Delete(this)
		_, _ = windows.LocalFree(windows.Handle(this))
	}
	return uintptr(uint32(remaining))
}

// typedEventHandlerInvoke delivers one catalog notification; a failing status
// is what Windows logs for a handler that could not read its own ownership.
func typedEventHandlerInvokeX(this uintptr, sender uintptr, args uintptr) (status uintptr) {
	defer func() {
		if recovered := recover(); recovered != nil {
			status = uintptr(uint32(eFail))
		}
	}()
	value, ok := typedEventHandlers.Load(this)
	if !ok {
		return uintptr(uint32(eFail))
	}
	return hresultForError(value.(*typedEventHandler).invoke(sender, args))
}

// hresultForError reports a Go error as the status a COM caller expects.
func hresultForError(err error) uintptr {
	if err == nil {
		return 0
	}
	var status hresultError
	if errors.As(err, &status) {
		return uintptr(uint32(status))
	}
	return uintptr(uint32(eFail))
}
