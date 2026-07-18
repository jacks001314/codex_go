//go:build !windows

package windowssandbox

func SIDBytesFromString(value string) ([]byte, error) {
	if value == "" {
		return nil, ErrInvalidRequest
	}
	return nil, unsupported("winutil.sid_bytes_from_string")
}

func StringFromSIDBytes(sid []byte) string {
	return string(sid)
}
