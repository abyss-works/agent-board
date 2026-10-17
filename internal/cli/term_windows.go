//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// readSecret reads a line from the console with echo turned off.
func readSecret(f *os.File) (string, error) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return readLine(f)
	}
	if err := windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
		return readLine(f)
	}
	defer windows.SetConsoleMode(h, mode)
	return readLine(f)
}

// terminalWidth is the console window width in cells, or 0 when unknown.
func terminalWidth(f *os.File) int {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(f.Fd()), &info); err != nil {
		return 0
	}
	return int(info.Window.Right-info.Window.Left) + 1
}
