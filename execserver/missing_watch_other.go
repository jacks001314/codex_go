//go:build !linux

package execserver

func missingWatchFileSystemSupported(string) bool { return false }

func missingWatchMountSource() (<-chan struct{}, func()) { return nil, func() {} }
