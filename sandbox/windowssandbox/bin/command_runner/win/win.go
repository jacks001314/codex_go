package win

import (
	"io"
)

func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	return run(args, stdin, stdout, stderr)
}
