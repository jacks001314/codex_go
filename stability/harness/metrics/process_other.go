//go:build !windows && !linux

package metrics

func readRSSBytes() (uint64, bool) { return 0, false }

func readOpenFDs() (int, bool) { return 0, false }
