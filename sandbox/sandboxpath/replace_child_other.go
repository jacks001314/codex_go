//go:build !unix

package sandboxpath

func currentUserCanReplaceChild(string, string) bool {
	return true
}
