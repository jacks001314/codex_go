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
	result, err := Apply(patch, &ApplyOptions{CWD: cwd})
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
