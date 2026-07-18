//go:build !windows

package windowssandbox

func installWFPFiltersForAccount(account string) (int, error) {
	return 0, unsupported("wfp.install_wfp_filters_for_account")
}
