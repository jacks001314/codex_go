//go:build !windows

package windowssandbox

func AllowNullDevice(sid string) error {
	return unsupported("allow.allow_null_device")
}
