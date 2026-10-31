//go:build windows

package windowssandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WriteFileAtomically replaces path with a fresh file created relative to a
// pinned, reparse-free parent directory (Rust write_file_atomically, #42342).
// The destination is never opened for writing, so a caller-controlled hard link
// cannot redirect the contents outside the validated directory.
func WriteFileAtomically(path string, contents []byte) error {
	if err := ValidateLocalDirectoryPath(path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	name := filepath.Base(path)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return errors.New("output must have a file name")
	}
	file, directory, err := CreateTemporaryFileInDir(parent, ".tmp")
	if err != nil {
		return err
	}
	defer windows.CloseHandle(directory)
	renameErr := func() error {
		if _, err := file.Write(contents); err != nil {
			removeTemporaryFile(file)
			return fmt.Errorf("write temporary sandbox output: %w", err)
		}
		nameUTF16, err := windows.UTF16FromString(name)
		if err != nil {
			removeTemporaryFile(file)
			return err
		}
		size := int(unsafe.Offsetof(fileRenameInfo{}.fileName)) + len(nameUTF16)*2
		words := make([]uintptr, (size+int(unsafe.Sizeof(uintptr(0)))-1)/int(unsafe.Sizeof(uintptr(0))))
		info := (*fileRenameInfo)(unsafe.Pointer(&words[0]))
		info.replaceIfExists = 1
		info.rootDirectory = directory
		info.fileNameLength = uint32((len(nameUTF16) - 1) * 2)
		copy(unsafe.Slice(&info.fileName[0], len(nameUTF16)), nameUTF16)
		var ioStatus windows.IO_STATUS_BLOCK
		if err := windows.NtSetInformationFile(
			windows.Handle(file.Fd()),
			&ioStatus,
			(*byte)(unsafe.Pointer(&words[0])),
			uint32(size),
			windows.FileRenameInformation,
		); err != nil {
			removeTemporaryFile(file)
			return fmt.Errorf("replace output %s: %w", path, noReparseStatusError(err))
		}
		return nil
	}()
	closeErr := file.Close()
	if renameErr != nil {
		return renameErr
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary sandbox output: %w", closeErr)
	}
	return nil
}

// CreateTemporaryFileInDir creates a fresh sandbox-<id><suffix> entry relative
// to a pinned, reparse-free directory (Rust create_temporary_file). The caller
// keeps the returned directory handle so the name stays bound to the validated
// directory, and the file is opened without FILE_SHARE_DELETE so it cannot be
// renamed or removed while held.
func CreateTemporaryFileInDir(parent string, suffix string) (*os.File, windows.Handle, error) {
	directory, err := OpenDirectoryNoReparse(
		parent,
		windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		DirectoryOpenExisting,
	)
	if err != nil {
		return nil, windows.InvalidHandle, err
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		windows.CloseHandle(directory)
		return nil, windows.InvalidHandle, fmt.Errorf("generate sandbox output name: %w", err)
	}
	name := "sandbox-" + hex.EncodeToString(random) + suffix
	nameUTF16, err := windows.UTF16FromString(name)
	if err != nil {
		windows.CloseHandle(directory)
		return nil, windows.InvalidHandle, err
	}
	handle, err := openNoReparse(
		directory,
		nameUTF16,
		windows.FILE_WRITE_DATA|windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ,
		windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT,
	)
	if err != nil {
		windows.CloseHandle(directory)
		return nil, windows.InvalidHandle, fmt.Errorf("create temporary sandbox output: %w", err)
	}
	return os.NewFile(uintptr(handle), name), directory, nil
}

// removeTemporaryFile deletes only the entry this write created, never a
// pathname the caller could have replaced.
func removeTemporaryFile(file *os.File) {
	var disposition [1]byte
	disposition[0] = 1
	_ = windows.SetFileInformationByHandle(
		windows.Handle(file.Fd()),
		windows.FileDispositionInfo,
		&disposition[0],
		uint32(len(disposition)),
	)
}

type fileRenameInfo struct {
	replaceIfExists uint32
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [1]uint16
}
