//go:build !windows

package windowssandbox

func DPAPIProtect(data []byte) ([]byte, error) {
	return nil, unsupported("dpapi.protect")
}

func DPAPIUnprotect(data []byte) ([]byte, error) {
	return nil, unsupported("dpapi.unprotect")
}
