package win

import "io"

func Run(args []string, stdout io.Writer, stderr io.Writer) error {
	return run(args, stdout, stderr)
}
