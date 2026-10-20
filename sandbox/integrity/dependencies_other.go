//go:build !linux && !windows

package integrity

func platformIntegrityDependencies(*RunnerRequest, *PreparedPolicy) []integrityDependency {
	return nil
}
