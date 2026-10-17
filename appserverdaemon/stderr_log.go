package appserverdaemon

import (
	"io"
	"log/slog"
	"os"

	"codex_go/utils"
)

const maxPreviousStderrLogBytes = 256 * 1024

// preserveStderrLog saves the last 256 KiB of a detached launch's stderr log
// before the next launch truncates it. Missing, empty, symlinked, or nonregular
// files are ignored; preservation failures never block launch.
func preserveStderrLog(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || opened.Size() == 0 {
		return nil
	}
	offset := opened.Size() - maxPreviousStderrLogBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPreviousStderrLogBytes))
	if err != nil || len(data) == 0 {
		return err
	}
	return utils.WriteAtomically(path+".previous", string(data))
}

func preserveStderrLogBestEffort(path string) {
	if err := preserveStderrLog(path); err != nil {
		slog.Warn("failed to preserve previous stderr log", "path", path, "error", err)
	}
}
