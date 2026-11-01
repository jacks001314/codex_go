package doctor

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// fileIsTerminal mirrors std::io::IsTerminal: a character device such as the
// NUL device is not a terminal, so the console mode probe decides instead of
// the file mode.
func fileIsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

// detectTerminalSizeForDoctor mirrors crossterm::terminal::size(), which opens
// the process console buffer (CONOUT$) instead of the possibly redirected
// stdout handle.
func detectTerminalSizeForDoctor(file *os.File, isTerminal bool) terminalSizeProbe {
	if file != nil {
		if size, ok := consoleSizeForDoctor(windows.Handle(file.Fd())); ok {
			return size
		}
	}
	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return terminalSizeProbe{Err: "not detected"}
	}
	console, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return terminalSizeProbe{Err: "not detected"}
	}
	defer windows.CloseHandle(console)
	if size, ok := consoleSizeForDoctor(console); ok {
		return size
	}
	return terminalSizeProbe{Err: "not detected"}
}

func consoleSizeForDoctor(handle windows.Handle) (terminalSizeProbe, bool) {
	if handle == 0 || handle == windows.InvalidHandle {
		return terminalSizeProbe{}, false
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(handle, &info); err != nil {
		return terminalSizeProbe{}, false
	}
	columns := int(info.Window.Right-info.Window.Left) + 1
	rows := int(info.Window.Bottom-info.Window.Top) + 1
	return terminalSizeProbe{Columns: columns, Rows: rows}, true
}

func windowsConsoleDetailsForDoctor() []string {
	inputCP, _ := windows.GetConsoleCP()
	outputCP, _ := windows.GetConsoleOutputCP()
	details := []string{
		fmt.Sprintf("console input code page: %d", inputCP),
		fmt.Sprintf("console output code page: %d", outputCP),
	}
	details = append(details, windowsConsoleModeDetailForDoctor("stdout console mode", windows.STD_OUTPUT_HANDLE))
	details = append(details, windowsConsoleModeDetailForDoctor("stderr console mode", windows.STD_ERROR_HANDLE))
	return details
}

func windowsConsoleModeDetailForDoctor(label string, stdHandle uint32) string {
	handle, err := windows.GetStdHandle(stdHandle)
	if err != nil || handle == 0 || handle == windows.InvalidHandle {
		return label + ": unavailable"
	}
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return label + ": unavailable"
	}
	vtEnabled := mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0
	return fmt.Sprintf("%s: 0x%08x (VT processing: %t)", label, mode, vtEnabled)
}
