//go:build !windows

package tcptunnel

import "os"

func openTokenFile(path string) (*os.File, error) { return os.Open(path) }
