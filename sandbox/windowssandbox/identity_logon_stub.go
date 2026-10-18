//go:build !windows

package windowssandbox

import "errors"

func logonSandboxAccount(SandboxCredentials) error {
	return nil
}

func resolveSandboxAccountSID(string) (string, error) {
	return "", errors.New("sandbox account SID lookup is only available on Windows")
}
