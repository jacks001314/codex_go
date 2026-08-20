package execserver

import (
	"fmt"
	"os"
)

type invalidFileInputError struct {
	message string
}

func (e *invalidFileInputError) Error() string {
	return e.message
}

func (e *invalidFileInputError) Unwrap() error {
	return os.ErrInvalid
}

func validateRegularFile(path string, file *os.File, isDiskFile bool) error {
	if info, err := os.Lstat(path); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return &invalidFileInputError{message: fmt.Sprintf("path `%s` is not a regular file", path)}
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !isDiskFile || !info.Mode().IsRegular() {
		return &invalidFileInputError{message: fmt.Sprintf("path `%s` is not a file", path)}
	}
	return nil
}

func closeFileOnError(file *os.File, err error) (*os.File, error) {
	if err == nil {
		return file, nil
	}
	_ = file.Close()
	return nil, err
}
