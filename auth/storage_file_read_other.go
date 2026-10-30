//go:build !windows

package auth

import "os"

func readAuthFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
