//go:build !windows && !darwin && !linux

package app

func screenReaderProbe() bool {
	return false
}
