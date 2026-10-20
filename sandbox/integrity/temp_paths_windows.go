package integrity

import (
	"path/filepath"
	"sort"
	"strings"
)

// policyTemporaryDirectories mirrors MXC's Windows temporary-path binding:
// only absolute TEMP/TMP values from the command environment are honored, and
// duplicate spellings collapse case-insensitively.
func policyTemporaryDirectories(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		upper := strings.ToUpper(key)
		if upper == "TEMP" || upper == "TMP" {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := strings.ToUpper(keys[i]), strings.ToUpper(keys[j])
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	seenKeys := map[string]bool{}
	seenPaths := map[string]bool{}
	var values []string
	for _, key := range keys {
		upper := strings.ToUpper(key)
		if seenKeys[upper] {
			continue
		}
		seenKeys[upper] = true
		value := strings.TrimSpace(env[key])
		if value == "" || !filepath.IsAbs(value) {
			continue
		}
		value = filepath.Clean(value)
		pathKey := strings.ToLower(value)
		if seenPaths[pathKey] {
			continue
		}
		seenPaths[pathKey] = true
		values = append(values, value)
	}
	if len(values) == 0 {
		// The generic integrity runner also serves the legacy Windows sandbox,
		// whose portable policy input may carry TMPDIR. MXC itself uses the
		// TEMP/TMP values above.
		if value := strings.TrimSpace(env["TMPDIR"]); value != "" {
			return []string{value}
		}
	}
	return values
}
