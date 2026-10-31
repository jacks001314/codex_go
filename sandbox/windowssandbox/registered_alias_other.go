//go:build !windows

package windowssandbox

// RegisteredRunnerAlias is unavailable outside Windows.
func RegisteredRunnerAlias(string, string) (string, error) {
	return "", unsupported("app_package.registered_runner_alias")
}
