package windowssandbox

import (
	"fmt"
	"strings"
)

const privateDesktopNamePrefix = "CodexSandboxDesktop-"

// ValidatePrivateDesktopNameFormat mirrors the private-desktop naming rule
// Rust enforces when a wrapper receives --windows-sandbox-private-desktop-name:
// only names this package generated are accepted, so a caller cannot point the
// sandbox at the default desktop or at an arbitrary window station path.
func ValidatePrivateDesktopNameFormat(name string) error {
	nonce, ok := strings.CutPrefix(name, privateDesktopNamePrefix)
	if !ok || nonce == "" || len(nonce) > 32 {
		return fmt.Errorf("invalid private desktop name")
	}
	for _, ch := range nonce {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return fmt.Errorf("invalid private desktop name")
		}
	}
	return nil
}

func (r *WindowsSandboxCommandArgsRequest) privateDesktopNameForLaunch() (string, error) {
	name := strings.TrimSpace(r.PrivateDesktopName)
	if name == "" && r.PreparePrivateDesktop {
		selected, err := PrepareWindowsSandboxPrivateDesktopName(*r)
		if err != nil {
			return "", err
		}
		name = selected
	}
	if name == "" {
		return "", nil
	}
	if err := ValidatePrivateDesktopNameFormat(name); err != nil {
		return "", err
	}
	return name, nil
}
