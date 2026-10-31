package windowsandboxservice

import (
	"errors"
	"path/filepath"
	"strings"
)

// RequireRuntimePackageFamily ports Rust
// package_identity::require_runtime_package_family: registered Core may only
// register from the service's own installed package family, with no unpackaged
// or debug-build exemption.
func RequireRuntimePackageFamily(client, service *string) error {
	if client != nil && service != nil && *client == *service {
		return nil
	}
	return errors.New("runtime registration requires a client in the service's installed package family")
}

// isInstalledServiceImage reports whether image is one of the packaged entry
// points next to the service binary (Rust authorize_runtime_client_process).
func isInstalledServiceImage(image, serviceDirectory string) bool {
	for _, name := range []string{"codex.exe", "codex-code-mode-host.exe"} {
		if strings.EqualFold(image, filepath.Join(serviceDirectory, name)) {
			return true
		}
	}
	return false
}
