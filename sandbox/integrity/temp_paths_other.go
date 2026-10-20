//go:build !windows

package integrity

import "strings"

func policyTemporaryDirectories(env map[string]string) []string {
	if env == nil {
		return nil
	}
	if value := strings.TrimSpace(env["TMPDIR"]); value != "" {
		return []string{value}
	}
	return nil
}
