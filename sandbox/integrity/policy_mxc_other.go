//go:build !windows

package integrity

func preparePlatformIntegrityPolicy(*RunnerRequest) error {
	return nil
}
