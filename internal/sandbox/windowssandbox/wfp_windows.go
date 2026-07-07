//go:build windows

package windowssandbox

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	wfpspec "codex_go/internal/sandbox/windowssandbox/wfp"
	"golang.org/x/sys/windows"
)

const (
	wfpSessionName         = "Codex Windows Sandbox WFP"
	wfpProviderName        = "Codex Windows Sandbox WFP"
	wfpProviderDescription = "Persistent WFP provider for Codex Windows sandbox filters"
	wfpSublayerName        = "Codex Windows Sandbox WFP"
	wfpSublayerDescription = "Persistent WFP sublayer for Codex Windows sandbox filters"

	wfpProviderKey = "{2e31d31c-3948-4753-9117-e5d1a6496f41}"
	wfpSublayerKey = "{e65054fd-4d32-4c7c-95ef-621f0cf6431a}"

	fwpEmpty                  uint32 = 0
	fwpUint8                  uint32 = 1
	fwpUint16                 uint32 = 2
	fwpSecurityDescriptorType uint32 = 14
	fwpMatchEqual             uint32 = 0
	fwpActionBlock            uint32 = 0x00001001
	fwpActrlMatchFilter       uint32 = 0x00000001

	fwpmProviderFlagPersistent uint32 = 0x00000001
	fwpmSublayerFlagPersistent uint32 = 0x00000001
	fwpmFilterFlagPersistent   uint32 = 0x00000001

	rpcCAuthnDefault uint32 = 0xffffffff
	infiniteTimeout  uint32 = 0xffffffff

	fwpEFilterNotFound uint32 = 0x80320003
	fwpENotFound       uint32 = 0x80320008
	fwpEAlreadyExists  uint32 = 0x80320009
)

var (
	modfwpuclnt                = windows.NewLazySystemDLL("fwpuclnt.dll")
	procFwpmEngineOpen0        = modfwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0       = modfwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmTransactionBegin0  = modfwpuclnt.NewProc("FwpmTransactionBegin0")
	procFwpmTransactionCommit0 = modfwpuclnt.NewProc("FwpmTransactionCommit0")
	procFwpmTransactionAbort0  = modfwpuclnt.NewProc("FwpmTransactionAbort0")
	procFwpmProviderAdd0       = modfwpuclnt.NewProc("FwpmProviderAdd0")
	procFwpmSubLayerAdd0       = modfwpuclnt.NewProc("FwpmSubLayerAdd0")
	procFwpmFilterAdd0         = modfwpuclnt.NewProc("FwpmFilterAdd0")
	procFwpmFilterDeleteByKey0 = modfwpuclnt.NewProc("FwpmFilterDeleteByKey0")
)

type fwpByteBlob struct {
	Size uint32
	Data *byte
}

type fwpValue0 struct {
	Type  uint32
	Value uintptr
}

type fwpConditionValue0 = fwpValue0

type fwpmDisplayData0 struct {
	Name        *uint16
	Description *uint16
}

type fwpmSession0 struct {
	SessionKey           windows.GUID
	DisplayData          fwpmDisplayData0
	Flags                uint32
	TxnWaitTimeoutInMSec uint32
	ProcessID            uint32
	SID                  *windows.SID
	Username             *uint16
	KernelMode           int32
}

type fwpmProvider0 struct {
	ProviderKey  windows.GUID
	DisplayData  fwpmDisplayData0
	Flags        uint32
	ProviderData fwpByteBlob
	ServiceName  *uint16
}

type fwpmSubLayer0 struct {
	SubLayerKey  windows.GUID
	DisplayData  fwpmDisplayData0
	Flags        uint32
	ProviderKey  *windows.GUID
	ProviderData fwpByteBlob
	Weight       uint16
}

type fwpmAction0 struct {
	Type       uint32
	FilterType windows.GUID
}

type fwpmFilterCondition0 struct {
	FieldKey       windows.GUID
	MatchType      uint32
	ConditionValue fwpConditionValue0
}

type fwpmFilterContext0 struct {
	RawContext uint64
	_          uint64
}

type fwpmFilter0 struct {
	FilterKey           windows.GUID
	DisplayData         fwpmDisplayData0
	Flags               uint32
	ProviderKey         *windows.GUID
	ProviderData        fwpByteBlob
	LayerKey            windows.GUID
	SubLayerKey         windows.GUID
	Weight              fwpValue0
	NumFilterConditions uint32
	FilterCondition     *fwpmFilterCondition0
	Action              fwpmAction0
	Context             fwpmFilterContext0
	Reserved            *windows.GUID
	FilterID            uint64
	EffectiveWeight     fwpValue0
}

type wfpEngine struct {
	handle windows.Handle
}

type wfpTransaction struct {
	engine    *wfpEngine
	committed bool
}

type wfpUserMatchCondition struct {
	securityDescriptor *windows.SECURITY_DESCRIPTOR
	blob               fwpByteBlob
}

func installWFPFiltersForAccount(account string) (int, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return 0, fmt.Errorf("%w: WFP account is required", ErrInvalidRequest)
	}
	engine, err := openWFPEngine()
	if err != nil {
		return 0, err
	}
	defer engine.close()

	transaction, err := engine.beginTransaction()
	if err != nil {
		return 0, err
	}
	defer transaction.abortIfNeeded()

	if err := ensureWFPProvider(engine.handle); err != nil {
		return 0, err
	}
	if err := ensureWFPSublayer(engine.handle); err != nil {
		return 0, err
	}

	userCondition, err := newWFPUserMatchCondition(account)
	if err != nil {
		return 0, err
	}

	installed := 0
	for _, spec := range wfpspec.DefaultFilterSpecs() {
		key, err := parseWFPGUID(spec.Key)
		if err != nil {
			return installed, fmt.Errorf("parse WFP filter key for %s: %w", spec.Name, err)
		}
		if err := deleteWFPFilterIfPresent(engine.handle, &key); err != nil {
			return installed, err
		}
		if err := addWFPFilter(engine.handle, spec, userCondition); err != nil {
			return installed, err
		}
		installed++
	}

	if err := transaction.commit(); err != nil {
		return installed, err
	}
	runtime.KeepAlive(userCondition)
	return installed, nil
}

func openWFPEngine() (*wfpEngine, error) {
	sessionName := ToWide(wfpSessionName)
	session := fwpmSession0{
		DisplayData: fwpmDisplayData0{Name: &sessionName[0]},
		// Match Rust's INFINITE transaction wait timeout so setup does not fail
		// simply because another WFP writer is active for a short window.
		TxnWaitTimeoutInMSec: infiniteTimeout,
	}
	var handle windows.Handle
	result, _, _ := procFwpmEngineOpen0.Call(
		0,
		uintptr(rpcCAuthnDefault),
		0,
		uintptr(unsafe.Pointer(&session)),
		uintptr(unsafe.Pointer(&handle)),
	)
	runtime.KeepAlive(sessionName)
	if err := ensureWFPSuccess(uint32(result), "FwpmEngineOpen0"); err != nil {
		return nil, err
	}
	return &wfpEngine{handle: handle}, nil
}

func (e *wfpEngine) close() {
	if e == nil || e.handle == 0 {
		return
	}
	_, _, _ = procFwpmEngineClose0.Call(uintptr(e.handle))
	e.handle = 0
}

func (e *wfpEngine) beginTransaction() (*wfpTransaction, error) {
	result, _, _ := procFwpmTransactionBegin0.Call(uintptr(e.handle), 0)
	if err := ensureWFPSuccess(uint32(result), "FwpmTransactionBegin0"); err != nil {
		return nil, err
	}
	return &wfpTransaction{engine: e}, nil
}

func (t *wfpTransaction) commit() error {
	if t == nil || t.engine == nil {
		return fmt.Errorf("%w: WFP transaction is nil", ErrInvalidRequest)
	}
	result, _, _ := procFwpmTransactionCommit0.Call(uintptr(t.engine.handle))
	if err := ensureWFPSuccess(uint32(result), "FwpmTransactionCommit0"); err != nil {
		return err
	}
	t.committed = true
	return nil
}

func (t *wfpTransaction) abortIfNeeded() {
	if t == nil || t.committed || t.engine == nil || t.engine.handle == 0 {
		return
	}
	_, _, _ = procFwpmTransactionAbort0.Call(uintptr(t.engine.handle))
}

func newWFPUserMatchCondition(account string) (*wfpUserMatchCondition, error) {
	accountPtr, err := windows.UTF16PtrFromString(account)
	if err != nil {
		return nil, err
	}
	trustee := windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_NAME,
		TrusteeType:  windows.TRUSTEE_IS_USER,
		TrusteeValue: windows.TrusteeValue(uintptr(unsafe.Pointer(accountPtr))),
	}
	access := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(fwpActrlMatchFilter),
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           trustee,
	}
	sd, err := windows.BuildSecurityDescriptor(nil, nil, []windows.EXPLICIT_ACCESS{access}, nil, nil)
	runtime.KeepAlive(accountPtr)
	if err != nil {
		return nil, fmt.Errorf("BuildSecurityDescriptorW: %w", err)
	}
	return &wfpUserMatchCondition{
		securityDescriptor: sd,
		blob: fwpByteBlob{
			Size: sd.Length(),
			Data: (*byte)(unsafe.Pointer(sd)),
		},
	}, nil
}

func ensureWFPProvider(engine windows.Handle) error {
	providerKey, err := parseWFPGUID(wfpProviderKey)
	if err != nil {
		return err
	}
	name := ToWide(wfpProviderName)
	description := ToWide(wfpProviderDescription)
	provider := fwpmProvider0{
		ProviderKey: providerKey,
		DisplayData: fwpmDisplayData0{
			Name:        &name[0],
			Description: &description[0],
		},
		Flags:        fwpmProviderFlagPersistent,
		ProviderData: emptyWFPBlob(),
	}
	result, _, _ := procFwpmProviderAdd0.Call(
		uintptr(engine),
		uintptr(unsafe.Pointer(&provider)),
		0,
	)
	runtime.KeepAlive(name)
	runtime.KeepAlive(description)
	return ensureWFPSuccessOr(uint32(result), "FwpmProviderAdd0", fwpEAlreadyExists)
}

func ensureWFPSublayer(engine windows.Handle) error {
	providerKey, err := parseWFPGUID(wfpProviderKey)
	if err != nil {
		return err
	}
	sublayerKey, err := parseWFPGUID(wfpSublayerKey)
	if err != nil {
		return err
	}
	name := ToWide(wfpSublayerName)
	description := ToWide(wfpSublayerDescription)
	sublayer := fwpmSubLayer0{
		SubLayerKey: sublayerKey,
		DisplayData: fwpmDisplayData0{
			Name:        &name[0],
			Description: &description[0],
		},
		Flags:        fwpmSublayerFlagPersistent,
		ProviderKey:  &providerKey,
		ProviderData: emptyWFPBlob(),
		Weight:       0x8000,
	}
	result, _, _ := procFwpmSubLayerAdd0.Call(
		uintptr(engine),
		uintptr(unsafe.Pointer(&sublayer)),
		0,
	)
	runtime.KeepAlive(name)
	runtime.KeepAlive(description)
	runtime.KeepAlive(providerKey)
	return ensureWFPSuccessOr(uint32(result), "FwpmSubLayerAdd0", fwpEAlreadyExists)
}

func addWFPFilter(engine windows.Handle, spec wfpspec.FilterSpec, userCondition *wfpUserMatchCondition) error {
	filterKey, err := parseWFPGUID(spec.Key)
	if err != nil {
		return fmt.Errorf("parse WFP filter key for %s: %w", spec.Name, err)
	}
	providerKey, err := parseWFPGUID(wfpProviderKey)
	if err != nil {
		return err
	}
	layerKey, err := parseWFPGUID(spec.LayerKey)
	if err != nil {
		return fmt.Errorf("parse WFP layer key for %s: %w", spec.Name, err)
	}
	sublayerKey, err := parseWFPGUID(wfpSublayerKey)
	if err != nil {
		return err
	}
	name := ToWide(spec.Name)
	description := ToWide(spec.Description)
	conditions, err := buildWFPConditions(spec.Conditions, userCondition)
	if err != nil {
		return fmt.Errorf("build WFP conditions for %s: %w", spec.Name, err)
	}
	var firstCondition *fwpmFilterCondition0
	if len(conditions) > 0 {
		firstCondition = &conditions[0]
	}
	filter := fwpmFilter0{
		FilterKey: filterKey,
		DisplayData: fwpmDisplayData0{
			Name:        &name[0],
			Description: &description[0],
		},
		Flags:               fwpmFilterFlagPersistent,
		ProviderKey:         &providerKey,
		ProviderData:        emptyWFPBlob(),
		LayerKey:            layerKey,
		SubLayerKey:         sublayerKey,
		Weight:              emptyWFPValue(),
		NumFilterConditions: uint32(len(conditions)),
		FilterCondition:     firstCondition,
		Action: fwpmAction0{
			Type:       fwpActionBlock,
			FilterType: windows.GUID{},
		},
		Context:         fwpmFilterContext0{},
		EffectiveWeight: emptyWFPValue(),
	}
	var filterID uint64
	result, _, _ := procFwpmFilterAdd0.Call(
		uintptr(engine),
		uintptr(unsafe.Pointer(&filter)),
		0,
		uintptr(unsafe.Pointer(&filterID)),
	)
	runtime.KeepAlive(name)
	runtime.KeepAlive(description)
	runtime.KeepAlive(providerKey)
	runtime.KeepAlive(conditions)
	runtime.KeepAlive(userCondition)
	return ensureWFPSuccess(uint32(result), "FwpmFilterAdd0("+spec.Name+")")
}

func buildWFPConditions(specs []wfpspec.ConditionSpec, userCondition *wfpUserMatchCondition) ([]fwpmFilterCondition0, error) {
	conditions := make([]fwpmFilterCondition0, 0, len(specs))
	for _, spec := range specs {
		switch spec.Kind {
		case wfpspec.ConditionUser:
			key, err := parseWFPGUID(wfpspec.ConditionALEUserID)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, fwpmFilterCondition0{
				FieldKey:  key,
				MatchType: fwpMatchEqual,
				ConditionValue: fwpConditionValue0{
					Type:  fwpSecurityDescriptorType,
					Value: uintptr(unsafe.Pointer(&userCondition.blob)),
				},
			})
		case wfpspec.ConditionProtocol:
			key, err := parseWFPGUID(wfpspec.ConditionIPProtocol)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, fwpmFilterCondition0{
				FieldKey:  key,
				MatchType: fwpMatchEqual,
				ConditionValue: fwpConditionValue0{
					Type:  fwpUint8,
					Value: uintptr(spec.Protocol),
				},
			})
		case wfpspec.ConditionRemotePort:
			key, err := parseWFPGUID(wfpspec.ConditionIPRemotePort)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, fwpmFilterCondition0{
				FieldKey:  key,
				MatchType: fwpMatchEqual,
				ConditionValue: fwpConditionValue0{
					Type:  fwpUint16,
					Value: uintptr(spec.RemotePort),
				},
			})
		default:
			return nil, fmt.Errorf("unknown WFP condition kind %q", spec.Kind)
		}
	}
	return conditions, nil
}

func deleteWFPFilterIfPresent(engine windows.Handle, key *windows.GUID) error {
	result, _, _ := procFwpmFilterDeleteByKey0.Call(
		uintptr(engine),
		uintptr(unsafe.Pointer(key)),
	)
	return ensureWFPSuccessOr(uint32(result), "FwpmFilterDeleteByKey0", fwpEFilterNotFound, fwpENotFound)
}

func ensureWFPSuccess(result uint32, operation string) error {
	return ensureWFPSuccessOr(result, operation)
}

func ensureWFPSuccessOr(result uint32, operation string, allowed ...uint32) error {
	if result == 0 {
		return nil
	}
	for _, code := range allowed {
		if result == code {
			return nil
		}
	}
	return fmt.Errorf("%s failed: 0x%08X", operation, result)
}

func parseWFPGUID(value string) (windows.GUID, error) {
	value = strings.TrimSpace(value)
	if value != "" && !strings.HasPrefix(value, "{") {
		value = "{" + value + "}"
	}
	return windows.GUIDFromString(value)
}

func emptyWFPBlob() fwpByteBlob {
	return fwpByteBlob{}
}

func emptyWFPValue() fwpValue0 {
	return fwpValue0{Type: fwpEmpty}
}
