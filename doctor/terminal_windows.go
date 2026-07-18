package doctor

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func detectTerminalSizeForDoctor(file *os.File, isTerminal bool) terminalSizeProbe {
	if !isTerminal || file == nil {
		return terminalSizeProbe{Err: "not detected"}
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(file.Fd()), &info); err != nil {
		return terminalSizeProbe{Err: err.Error()}
	}
	columns := int(info.Window.Right - info.Window.Left + 1)
	rows := int(info.Window.Bottom - info.Window.Top + 1)
	return terminalSizeProbe{Columns: columns, Rows: rows}
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
