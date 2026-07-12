//go:build darwin

package network

import "os/exec"

func loadProxyPlatformRootBundle() (string, error) {
	contents, err := exec.Command("security", "find-certificate", "-a", "-p", "/System/Library/Keychains/SystemRootCertificates.keychain").Output()
	if err != nil {
		return "", err
	}
	return string(contents), nil
}
