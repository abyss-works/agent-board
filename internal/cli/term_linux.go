//go:build linux

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// readSecret reads a line from the terminal with echo turned off.
func readSecret(f *os.File) (string, error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return readLine(f)
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &quiet); err != nil {
		return readLine(f)
	}
	defer unix.IoctlSetTermios(fd, unix.TCSETS, old)
	return readLine(f)
}

// terminalWidth is the terminal width in cells, or 0 when unknown.
func terminalWidth(f *os.File) int {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0
	}
	return int(ws.Col)
}
