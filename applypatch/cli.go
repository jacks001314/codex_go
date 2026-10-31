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
	return runPatch(patch, stdout, stderr, cwd)
}

// RunCoreFlagCLI mirrors Rust's `codex --codex-run-as-apply-patch <PATCH>`
// dispatch: the first argument is the patch, stdin is never read, extra
// arguments are ignored, and a missing argument is an anyhow error.
func RunCoreFlagCLI(args []string, stdout, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		if stderr != nil {
			fmt.Fprintln(stderr, "Error: --codex-run-as-apply-patch requires a UTF-8 PATCH argument.")
		}
		return 1
	}
	return runPatch(args[0], stdout, stderr, cwd)
}

func runPatch(patch string, stdout, stderr io.Writer, cwd string) int {
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
	result, err := action.applyCommitted(osFilesystem{}, cwd)
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
	fmt.Fprintln(stderr, FormatCLIError(err))
}
