//go:build !linux

package integrity

func platformIntegrityDependencies(*RunnerRequest, *PreparedPolicy) []integrityDependency {
	return nil
}
