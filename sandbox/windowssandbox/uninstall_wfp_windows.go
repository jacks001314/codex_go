//go:build windows

package windowssandbox

import (
	"unsafe"

	wfpspec "codex_go/sandbox/windowssandbox/wfp"
)

// FWP_E_PROVIDER_NOT_FOUND and FWP_E_SUBLAYER_NOT_FOUND: deleting an absent
// object is not an error (Rust wfp::remove_wfp_filters).
const (
	fwpEProviderNotFound uint32 = 0x80320005
	fwpESubLayerNotFound uint32 = 0x80320007
)

var (
	procFwpmProviderDeleteByKey0 = modfwpuclnt.NewProc("FwpmProviderDeleteByKey0")
	procFwpmSubLayerDeleteByKey0 = modfwpuclnt.NewProc("FwpmSubLayerDeleteByKey0")
)

// removeLegacyWFPFilters deletes the persistent WFP filters, sublayer, and
// provider the sandbox setup installed
// (Rust wfp::remove_wfp_filters, #50437). A policy writer holding the
// transaction lock only delays the attempt, matching Rust's 1 s wait.
func removeLegacyWFPFilters() error {
	engine, err := openWFPEngineWithTimeout(1000)
	if err != nil {
		return err
	}
	defer engine.close()

	transaction, err := engine.beginTransaction()
	if err != nil {
		return err
	}
	defer transaction.abortIfNeeded()

	for _, spec := range wfpspec.DefaultFilterSpecs() {
		key, err := parseWFPGUID(spec.Key)
		if err != nil {
			return err
		}
		if err := deleteWFPFilterIfPresent(engine.handle, &key); err != nil {
			return err
		}
	}

	sublayerKey, err := parseWFPGUID(wfpSublayerKey)
	if err != nil {
		return err
	}
	result, _, _ := procFwpmSubLayerDeleteByKey0.Call(
		uintptr(engine.handle),
		uintptr(unsafe.Pointer(&sublayerKey)),
	)
	if err := ensureWFPSuccessOr(uint32(result), "FwpmSubLayerDeleteByKey0", fwpESubLayerNotFound, fwpENotFound); err != nil {
		return err
	}

	providerKey, err := parseWFPGUID(wfpProviderKey)
	if err != nil {
		return err
	}
	result, _, _ = procFwpmProviderDeleteByKey0.Call(
		uintptr(engine.handle),
		uintptr(unsafe.Pointer(&providerKey)),
	)
	if err := ensureWFPSuccessOr(uint32(result), "FwpmProviderDeleteByKey0", fwpEProviderNotFound, fwpENotFound); err != nil {
		return err
	}

	return transaction.commit()
}
