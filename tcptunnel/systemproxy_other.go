//go:build !windows && !darwin

package tcptunnel

// systemProxySetting reports no OS proxy configuration on platforms where Rust
// only consults the environment.
func systemProxySetting(noProxy string) (string, string) {
	return "", noProxy
}
