//go:build !windows

package windowssandbox

// RegisteredCoreNeedsRefresh is always false outside Windows.
func RegisteredCoreNeedsRefresh(codexHome string) (bool, error) {
	return false, nil
}
