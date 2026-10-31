//go:build !windows

package provisionservice

func currentPackageFamily() (string, bool, error) {
	return "", false, nil
}
