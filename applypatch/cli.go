package applypatch

import (
	"fmt"
	"io"
	"strings"
)

func RunCLI(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) int {
	patch := strings.Join(args, " ")
	if strings.TrimSpace(patch) == "" && stdin != nil {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		patch = string(data)
	}
	if strings.TrimSpace(patch) == "" {
		fmt.Fprintln(stderr, "Error: apply_patch requires a PATCH argument or stdin input.")
		return 1
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	action, err := Parse(patch)
	if err != nil {
		writeCLIError(stderr, err)
		return 1
	}
	// The CLI mirrors Rust's apply_patch_with_mode: hunks are applied
	// sequentially and a failure after a partial success leaves the already
	// applied changes on disk (scenario 015). The verify-first flow is only
	// used by the app-server tool, matching Rust's verify_apply_patch_args.
	result, err := action.applyCommitted(cwd, FileUpdateModeFromEnv())
	if err != nil {
		writeCLIError(stderr, err)
		return 1
	}
	fmt.Fprint(stdout, result.Summary())
	return 0
}

func writeCLIError(stderr io.Writer, err error) {
	if stderr == nil {
		return
	}
	if strings.HasPrefix(err.Error(), "failed to find expected lines:") {
		fmt.Fprintln(stderr, err.Error())
		return
	}
	fmt.Fprintln(stderr, FormatError(err))
}
